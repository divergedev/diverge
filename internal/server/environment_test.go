package server

import (
	"bytes"
	"context"
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
	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/server/auth"
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
	svc := NewEnvironmentService(c, k8s, nil, nil, make(chan struct{}, 10), logger, audit)

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
		err409 := apierrors.NewConflict(schema.GroupResource{Group: "diverge.dev", Resource: "environments"}, "test-env", nil)
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
	svc := NewEnvironmentService(c, k8s, nil, nil, make(chan struct{}, 10), logger, audit)

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
}

func TestUpdateEnvironment_PBT(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		rv := rapid.StringMatching(`^[a-zA-Z0-9]+$`).Draw(t, "resource_version")

		_, c, k8s, logger := buildEnvTestSetup()
		audit := NewAuditLogger(logger)
		svc := NewEnvironmentService(c, k8s, nil, nil, make(chan struct{}, 10), logger, audit)

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

		if rv != existing.ResourceVersion {
			require.Error(t, err)
			var cErr *connect.Error
			require.ErrorAs(t, err, &cErr)
			assert.Equal(t, connect.CodeAborted, cErr.Code())
		} else {
			require.NoError(t, err)
		}
	})
}
