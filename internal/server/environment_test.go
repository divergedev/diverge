package server

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	fakek8s "k8s.io/client-go/kubernetes/fake"
	"log/slog"
	"pgregory.net/rapid"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	pb "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	"github.com/divergedev/diverge/api/gen/diverge/v1alpha1/divergev1alpha1connect"
	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/multicluster"
	"github.com/divergedev/diverge/internal/server/auth"
	"github.com/divergedev/diverge/internal/server/streaming"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	authorizationv1 "k8s.io/api/authorization/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	coretesting "k8s.io/client-go/testing"
)

func buildEnvTestSetup() (*runtime.Scheme, client.Client, kubernetes.Interface, *slog.Logger) {
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	k8s := fakek8s.NewSimpleClientset()
	k8s.PrependReactor("create", "subjectaccessreviews", func(action coretesting.Action) (handled bool, ret runtime.Object, err error) {
		sar := action.(coretesting.CreateAction).GetObject().(*authorizationv1.SubjectAccessReview)
		sar.Status.Allowed = true
		return true, sar, nil
	})

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	return scheme, c, k8s, logger
}

func TestUpdateEnvironment_ResourceVersion(t *testing.T) {
	_, c, k8s, logger := buildEnvTestSetup()
	audit := NewAuditLogger(logger)
	svc := NewEnvironmentService(c, k8s, nil, nil, NewStreamLimiter(250, 20), logger, audit)

	ctx := context.Background()
	ctx = auth.ContextWithUserInfo(ctx, &auth.UserInfo{Username: "test"})
	existing := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
		Spec: v1alpha1.EnvironmentSpec{},
	}
	require.NoError(t, c.Create(ctx, existing))
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: "test-env", Namespace: "default"}, existing))

	t.Run("successful update with correct ResourceVersion", func(t *testing.T) {
		req := &pb.UpdateEnvironmentRequest{
			Environment: &pb.Environment{
				Name:            "test-env",
				Namespace:       "default",
				ResourceVersion: existing.ResourceVersion,
				Labels:          map[string]string{"foo": "bar"},
			},
		}
		resp, err := svc.UpdateEnvironment(ctx, connect.NewRequest(req))
		require.NoError(t, err)
		assert.Equal(t, "bar", resp.Msg.Environment.Labels["foo"])

		var updated v1alpha1.Environment
		require.NoError(t, c.Get(ctx, client.ObjectKey{Name: "test-env", Namespace: "default"}, &updated))
		// ResourceVersion changes after update in a real cluster, but fake client increments it
		assert.NotEqual(t, existing.ResourceVersion, updated.ResourceVersion)
	})

	t.Run("conflict error with stale ResourceVersion", func(t *testing.T) {
		// Mock fake client doesn't automatically fail on resource version conflict
		// unless we specifically configure it to, but we can test the error mapping if it did.
		// Since we're using fake client, let's just make sure the field is passed along.

		// To properly test the conflict, we can inject a real 409 error
		err409 := apierrors.NewConflict(schema.GroupResource{Group: "divergedev.com", Resource: "environments"}, "test-env", nil)
		sErr := SanitizeK8sError(logger, err409)
		assert.Error(t, sErr)
		var cErr *connect.Error
		require.ErrorAs(t, sErr, &cErr)
		assert.Equal(t, connect.CodeAborted, cErr.Code())
		assert.Contains(t, cErr.Message(), "resource was modified, please retry with the latest resource_version")
	})
}

func TestUpdateEnvironment_FieldMask(t *testing.T) {
	_, c, k8s, logger := buildEnvTestSetup()
	audit := NewAuditLogger(logger)
	svc := NewEnvironmentService(c, k8s, nil, nil, NewStreamLimiter(250, 20), logger, audit)

	ctx := context.Background()
	ctx = auth.ContextWithUserInfo(ctx, &auth.UserInfo{Username: "test"})
	existing := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "test-env",
			Namespace:   "default",
			Labels:      map[string]string{"keep": "me"},
			Annotations: map[string]string{"keep": "me-too"},
		},
		Spec: v1alpha1.EnvironmentSpec{},
	}
	require.NoError(t, c.Create(ctx, existing))
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: "test-env", Namespace: "default"}, existing))

	t.Run("partial update with FieldMask", func(t *testing.T) {
		req := &pb.UpdateEnvironmentRequest{
			Environment: &pb.Environment{
				Name:            "test-env",
				Namespace:       "default",
				ResourceVersion: existing.ResourceVersion,
				Labels:          map[string]string{"new": "label"},
				Annotations:     map[string]string{"new": "annotation"},
			},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"labels"}},
		}

		_, err := svc.UpdateEnvironment(ctx, connect.NewRequest(req))
		require.NoError(t, err)

		var updated v1alpha1.Environment
		require.NoError(t, c.Get(ctx, client.ObjectKey{Name: "test-env", Namespace: "default"}, &updated))

		// Labels should be updated
		assert.Equal(t, map[string]string{"new": "label"}, updated.Labels)
		// Annotations should remain unchanged because it wasn't in the mask
		assert.Equal(t, map[string]string{"keep": "me-too"}, updated.Annotations)
	})

	t.Run("full replacement when FieldMask is nil", func(t *testing.T) {
		// fetch latest to get current resourceversion
		var cur v1alpha1.Environment
		require.NoError(t, c.Get(ctx, client.ObjectKey{Name: "test-env", Namespace: "default"}, &cur))

		req := &pb.UpdateEnvironmentRequest{
			Environment: &pb.Environment{
				Name:            "test-env",
				Namespace:       "default",
				ResourceVersion: cur.ResourceVersion,
				Labels:          map[string]string{"full": "replace"},
			},
		}

		_, err := svc.UpdateEnvironment(ctx, connect.NewRequest(req))
		require.NoError(t, err)

		var updated v1alpha1.Environment
		require.NoError(t, c.Get(ctx, client.ObjectKey{Name: "test-env", Namespace: "default"}, &updated))

		// Labels updated
		assert.Equal(t, map[string]string{"full": "replace"}, updated.Labels)
		// Annotations replaced (cleared) because fieldmask was nil
		assert.Empty(t, updated.Annotations)
	})

	t.Run("partial update of spec.features.overrides preserves other spec fields", func(t *testing.T) {
		var cur v1alpha1.Environment
		require.NoError(t, c.Get(ctx, client.ObjectKey{Name: "test-env", Namespace: "default"}, &cur))
		cur.Spec.Source = v1alpha1.EnvironmentSource{
			Provider: "github",
			Branch:   "feat/checkout",
		}
		cur.Spec.Features = &v1alpha1.FeatureSpec{
			Provider:  "configmap",
			Overrides: map[string]string{"old_flag": "true"},
		}
		require.NoError(t, c.Update(ctx, &cur))
		require.NoError(t, c.Get(ctx, client.ObjectKey{Name: "test-env", Namespace: "default"}, &cur))

		req := &pb.UpdateEnvironmentRequest{
			Environment: &pb.Environment{
				Name:            "test-env",
				Namespace:       "default",
				ResourceVersion: cur.ResourceVersion,
				Spec: &pb.EnvironmentSpec{
					Features: &pb.FeatureSpec{
						Overrides: map[string]string{
							"dark_mode": "true",
							"beta_ui":   "false",
						},
					},
				},
			},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"spec.features.overrides"}},
		}

		resp, err := svc.UpdateEnvironment(ctx, connect.NewRequest(req))
		require.NoError(t, err)
		require.NotNil(t, resp.Msg.Environment.Spec)
		require.NotNil(t, resp.Msg.Environment.Spec.Features)
		assert.Equal(t, map[string]string{"dark_mode": "true", "beta_ui": "false"}, resp.Msg.Environment.Spec.Features.Overrides)

		var updated v1alpha1.Environment
		require.NoError(t, c.Get(ctx, client.ObjectKey{Name: "test-env", Namespace: "default"}, &updated))
		assert.Equal(t, "github", updated.Spec.Source.Provider)
		assert.Equal(t, "feat/checkout", updated.Spec.Source.Branch)
		require.NotNil(t, updated.Spec.Features)
		assert.Equal(t, "configmap", updated.Spec.Features.Provider)
		assert.Equal(t, map[string]string{"dark_mode": "true", "beta_ui": "false"}, updated.Spec.Features.Overrides)
	})
}

func TestUpdateEnvironment_PBT(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		rv := rapid.OneOf(
			rapid.Just(""),
			rapid.StringMatching(`^[a-zA-Z0-9]+$`),
		).Draw(t, "resource_version")

		_, c, k8s, logger := buildEnvTestSetup()
		audit := NewAuditLogger(logger)
		svc := NewEnvironmentService(c, k8s, nil, nil, NewStreamLimiter(250, 20), logger, audit)

		ctx := context.Background()
		ctx = auth.ContextWithUserInfo(ctx, &auth.UserInfo{Username: "test"})
		existing := &v1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pbt-env",
				Namespace: "default",
			},
		}
		require.NoError(t, c.Create(ctx, existing))
		require.NoError(t, c.Get(ctx, client.ObjectKey{Name: "pbt-env", Namespace: "default"}, existing))

		req := &pb.UpdateEnvironmentRequest{
			Environment: &pb.Environment{
				Name:            "pbt-env",
				Namespace:       "default",
				ResourceVersion: rv,
			},
		}

		_, err := svc.UpdateEnvironment(ctx, connect.NewRequest(req))

		if rv == "" {
			require.Error(t, err)
			var cErr *connect.Error
			require.ErrorAs(t, err, &cErr)
			assert.Equal(t, connect.CodeInvalidArgument, cErr.Code())
		} else if rv != existing.ResourceVersion {
			require.Error(t, err)
			var cErr *connect.Error
			require.ErrorAs(t, err, &cErr)
			assert.Equal(t, connect.CodeAborted, cErr.Code())
		} else {
			require.NoError(t, err)
		}
	})
}

func TestUpdateEnvironment_ClusterRefImmutabilityOnceProvisioned(t *testing.T) {
	_, c, k8s, logger := buildEnvTestSetup()
	audit := NewAuditLogger(logger)
	svc := NewEnvironmentService(c, k8s, nil, nil, NewStreamLimiter(250, 20), logger, audit)

	ctx := auth.ContextWithUserInfo(context.Background(), &auth.UserInfo{Username: "alice"})
	existing := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pr-west",
			Namespace: "default",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{Provider: "github", Branch: "feat/x"},
			Deploy: v1alpha1.EnvironmentDeploy{
				ClusterRef: &v1alpha1.ClusterTargetRef{
					Name:                "us-west",
					KubeconfigSecretRef: &v1alpha1.SecretRef{Name: "west-sec"},
				},
			},
		},
		Status: v1alpha1.EnvironmentStatus{
			TargetCluster: "us-west",
		},
	}
	require.NoError(t, c.Create(ctx, existing))
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(existing), existing))

	_, err := svc.UpdateEnvironment(ctx, connect.NewRequest(&pb.UpdateEnvironmentRequest{
		Environment: &pb.Environment{
			Name:            "pr-west",
			Namespace:       "default",
			ResourceVersion: existing.ResourceVersion,
			Spec: &pb.EnvironmentSpec{
				Source: &pb.EnvironmentSource{Provider: "github", Branch: "feat/x"},
				Deploy: &pb.EnvironmentDeploy{
					ClusterRef: &pb.ClusterTargetRef{
						Name:                "us-east",
						KubeconfigSecretRef: &pb.SecretRef{Name: "east-sec"},
					},
				},
			},
		},
	}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestStreamLogs_PreviewNamespaceAuthzHookRoutingAndFollowClient(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, v1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))

	envCreate := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pr-create",
			Namespace: "team-a",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{Provider: "github", Branch: "feat/create"},
			Deploy: v1alpha1.EnvironmentDeploy{
				Namespace: "create",
				ClusterRef: &v1alpha1.ClusterTargetRef{
					Name:                "spoke-west",
					KubeconfigSecretRef: &v1alpha1.SecretRef{Name: "spoke-sec"},
				},
			},
		},
	}
	previewNS := envCreate.PreviewNamespace()

	// Migration hook pod lives on the HUB in msg.Namespace ("team-a").
	hubMigrationPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hook-pr-create-migration-abc",
			Namespace: "team-a",
			Labels: map[string]string{
				"divergedev.com/environment": "pr-create",
				"divergedev.com/hook-type":   "migration",
			},
		},
	}
	// Service pod lives on the SPOKE in PreviewNamespace().
	spokeServicePod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "orders-preview-pod",
			Namespace: previewNS,
			Labels: map[string]string{
				"divergedev.com/environment": "pr-create",
				"app.kubernetes.io/name":     "orders",
			},
		},
	}

	hubClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(envCreate, hubMigrationPod).Build()
	spokeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(spokeServicePod).Build()

	allowPreviewNS := false
	hubK8s := fakek8s.NewSimpleClientset()
	hubK8s.PrependReactor("create", "subjectaccessreviews", func(action coretesting.Action) (bool, runtime.Object, error) {
		sar := action.(coretesting.CreateAction).GetObject().(*authorizationv1.SubjectAccessReview)
		sar.Status.Allowed = true
		return true, sar, nil
	})

	spokeK8s := fakek8s.NewSimpleClientset()
	spokeK8s.PrependReactor("create", "subjectaccessreviews", func(action coretesting.Action) (bool, runtime.Object, error) {
		sar := action.(coretesting.CreateAction).GetObject().(*authorizationv1.SubjectAccessReview)
		attrs := sar.Spec.ResourceAttributes
		if attrs != nil && attrs.Namespace == previewNS && !allowPreviewNS {
			sar.Status.Allowed = false
			return true, sar, nil
		}
		sar.Status.Allowed = true
		return true, sar, nil
	})
	spokeStreamingK8s := fakek8s.NewSimpleClientset()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	svc := &EnvironmentService{
		client:      hubClient,
		k8sClient:   hubK8s,
		logStreamer: streaming.NewLogStreamer(hubK8s),
		limiter:     NewStreamLimiter(250, 20),
		logger:      logger,
		auditLogger: NewAuditLogger(logger),
		clusterClients: &multicluster.StaticResolver{
			Hub: &multicluster.Target{Name: "local", IsRemote: false, Client: hubClient, Clientset: hubK8s},
			Spokes: map[string]*multicluster.Target{
				"spoke-west": {
					Name:               "spoke-west",
					IsRemote:           true,
					Client:             spokeClient,
					Clientset:          spokeK8s,
					StreamingClientset: spokeStreamingK8s,
				},
			},
		},
	}

	_, handler := divergev1alpha1connect.NewEnvironmentServiceHandler(svc)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(auth.ContextWithUserInfo(r.Context(), &auth.UserInfo{Username: "dev"}))
		handler.ServeHTTP(w, r)
	}))
	defer srv.Close()

	rpcClient := divergev1alpha1connect.NewEnvironmentServiceClient(srv.Client(), srv.URL)
	ctx := context.Background()

	// 1. Migration HookType stays on hub in msg.Namespace ("team-a"), succeeding even when spoke/previewNS is denied.
	hookStream, err := rpcClient.StreamLogs(ctx, connect.NewRequest(&pb.StreamLogsRequest{
		EnvironmentName: "pr-create",
		Namespace:       "team-a",
		HookType:        "migration",
	}))
	require.NoError(t, err)
	require.True(t, hookStream.Receive(), "expected migration hook log line from hub pod, err=%v", hookStream.Err())
	assert.Equal(t, "hook-pr-create-migration-abc", hookStream.Msg().PodName)
	_ = hookStream.Close()

	// 2. Service logs in PreviewNamespace() on spoke are denied when user lacks pods/log in PreviewNamespace().
	deniedStream, err := rpcClient.StreamLogs(ctx, connect.NewRequest(&pb.StreamLogsRequest{
		EnvironmentName: "pr-create",
		Namespace:       "team-a",
	}))
	if err == nil {
		for deniedStream.Receive() {
		}
		err = deniedStream.Err()
		_ = deniedStream.Close()
	}
	require.Error(t, err)
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))

	// 3. Once granted pods/log in PreviewNamespace(), Follow=true uses StreamingClientset on the spoke.
	allowPreviewNS = true
	followStream, err := rpcClient.StreamLogs(ctx, connect.NewRequest(&pb.StreamLogsRequest{
		EnvironmentName: "pr-create",
		Namespace:       "team-a",
		Follow:          true,
	}))
	require.NoError(t, err)
	require.True(t, followStream.Receive(), "expected spoke service log line, err=%v", followStream.Err())
	assert.Equal(t, "orders-preview-pod", followStream.Msg().PodName)
	_ = followStream.Close()
	assert.NotEmpty(t, spokeStreamingK8s.Actions(), "Follow=true must stream using target.StreamingClientset")
}
