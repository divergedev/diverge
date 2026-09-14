package server

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/durationpb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pb "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/server/auth"
)

func TestExtendTTL(t *testing.T) {
	_, c, k8s, logger := buildEnvTestSetup()
	audit := NewAuditLogger(logger)
	svc := NewEnvironmentService(c, k8s, nil, nil, NewStreamLimiter(250, 20), logger, audit)

	ctx := context.Background()
	ctx = auth.ContextWithUserInfo(ctx, &auth.UserInfo{Username: "test"})

	baseTime := time.Now()
	expiresAtTime := baseTime.Add(2 * time.Hour)

	// Create valid environment
	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
		Spec: v1alpha1.EnvironmentSpec{
			Lifecycle: v1alpha1.EnvironmentLifecycle{
				TTL: &metav1.Duration{Duration: 2 * time.Hour},
			},
		},
		Status: v1alpha1.EnvironmentStatus{
			CreatedAt: &metav1.Time{Time: baseTime},
			ExpiresAt: &metav1.Time{Time: expiresAtTime},
		},
	}
	require.NoError(t, c.Create(ctx, env))
	// Client fake doesn't update status implicitly so we need to set it manually if we were to test Status updates. Since we initialized the struct with Status, and Client fake creates it as is, it's there. However, standard fake client ignores subresources on Create, but in this repository it seems like they do just Create.

	// Helper to send request
	sendReq := func(name, namespace string, extendBy time.Duration) (*connect.Response[pb.ExtendTTLResponse], error) {
		req := connect.NewRequest(&pb.ExtendTTLRequest{
			Name:      name,
			Namespace: namespace,
			ExtendBy:  durationpb.New(extendBy),
		})
		return svc.ExtendTTL(ctx, req)
	}

	t.Run("Happy path", func(t *testing.T) {
		// extend by 1h
		_, err := sendReq("test-env", "default", 1*time.Hour)
		require.NoError(t, err)

		var updated v1alpha1.Environment
		err = c.Get(ctx, client.ObjectKey{Name: "test-env", Namespace: "default"}, &updated)
		require.NoError(t, err)

		// New expiry should be max(now, expiresAt) + 1h.
		// Since expiresAt (2025) is before now (2026), max is now.
		// So newExpiry = now + 1h
		// newTTL = now + 1h - baseTime
		require.NotNil(t, updated.Spec.Lifecycle.TTL)
		// We just ensure it's larger than 2 hours.
		assert.Greater(t, updated.Spec.Lifecycle.TTL.Duration, 2*time.Hour)
	})

	t.Run("Env not found", func(t *testing.T) {
		_, err := sendReq("not-found", "default", 1*time.Hour)
		require.Error(t, err)
		assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	})

	t.Run("Invalid duration (zero)", func(t *testing.T) {
		_, err := sendReq("test-env", "default", 0)
		require.Error(t, err)
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})

	t.Run("Duration too large", func(t *testing.T) {
		_, err := sendReq("test-env", "default", 8*24*time.Hour)
		require.Error(t, err)
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})

	t.Run("No TTL configured", func(t *testing.T) {
		noTTLEnv := &v1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "no-ttl-env",
				Namespace: "default",
			},
			Spec: v1alpha1.EnvironmentSpec{}, // No lifecycle
		}
		require.NoError(t, c.Create(ctx, noTTLEnv))

		_, err := sendReq("no-ttl-env", "default", 1*time.Hour)
		require.Error(t, err)
		assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
		assert.Contains(t, err.Error(), "environment has no TTL configured")
	})

	t.Run("Env being deleted", func(t *testing.T) {
		deletingEnv := &v1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "deleting-env",
				Namespace:  "default",
				Finalizers: []string{"test-finalizer"},
			},
			Spec: v1alpha1.EnvironmentSpec{
				Lifecycle: v1alpha1.EnvironmentLifecycle{
					TTL: &metav1.Duration{Duration: 2 * time.Hour},
				},
			},
			Status: v1alpha1.EnvironmentStatus{
				CreatedAt: &metav1.Time{Time: baseTime},
				ExpiresAt: &metav1.Time{Time: expiresAtTime},
			},
		}
		require.NoError(t, c.Create(ctx, deletingEnv))
		require.NoError(t, c.Delete(ctx, deletingEnv))

		_, err := sendReq("deleting-env", "default", 1*time.Hour)
		require.Error(t, err)
		assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
		assert.Contains(t, err.Error(), "environment is being deleted")
	})
}
