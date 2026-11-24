package server

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	coordinationv1 "k8s.io/api/coordination/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestTunnelLease_AcquireNewAndNoExpiresAnnotation(t *testing.T) {
	fakeK8s := fake.NewSimpleClientset()
	tl := NewTunnelLease(fakeK8s, slog.Default(), "pod-1")

	res, err := tl.Acquire(context.Background(), LeaseAcquireOptions{
		Namespace:        "default",
		PreviewID:        "my-preview",
		Service:          "api",
		TunnelID:         "tunnel-abc",
		ClientInstanceID: "client-1",
		Owner:            "alice",
		K8sServiceName:   "diverge-tunnel-my-preview-api-1234",
		PodIP:            "10.0.0.5",
		Port:             8080,
		Protocol:         "v3",
		MetadataJSON:     `{"kind":"service"}`,
	})
	require.NoError(t, err)
	assert.Empty(t, res.PreviousHolder)
	assert.False(t, res.Evicted)
	assert.Equal(t, int32(0), res.LeaseTransitions)
	assert.Equal(t, "pod-1:tunnel-abc", res.HolderIdentity)

	lease, err := fakeK8s.CoordinationV1().Leases("default").Get(context.Background(), res.LeaseName, metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "alice", lease.Annotations["divergedev.com/tunnel-owner"])
	assert.Equal(t, "client-1", lease.Annotations["divergedev.com/tunnel-client-id"])
	assert.Equal(t, "v3", lease.Annotations["divergedev.com/tunnel-protocol"])
	assert.Equal(t, `{"kind":"service"}`, lease.Annotations["divergedev.com/tunnel-metadata"])
	assert.Equal(t, "8080", lease.Annotations["divergedev.com/tunnel-port"])
	assert.Equal(t, "0", lease.Annotations["divergedev.com/tunnel-generation"])
	_, hasExpires := lease.Annotations["divergedev.com/tunnel-expires"]
	assert.False(t, hasExpires, "Lease must never write divergedev.com/tunnel-expires annotation")
}

func TestTunnelLease_AcquireGetForbiddenPropagates(t *testing.T) {
	fakeK8s := fake.NewSimpleClientset()
	fakeK8s.PrependReactor("get", "leases", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Group: "coordination.k8s.io", Resource: "leases"}, "x", errors.New("rbac denied"))
	})
	tl := NewTunnelLease(fakeK8s, slog.Default(), "pod-1")

	_, err := tl.Acquire(context.Background(), LeaseAcquireOptions{
		Namespace: "default",
		PreviewID: "my-preview",
		TunnelID:  "t1",
	})
	require.Error(t, err)
	assert.True(t, apierrors.IsForbidden(err))
}

func TestTunnelLease_ClientIDFencingAndCrossUserForce(t *testing.T) {
	fakeK8s := fake.NewSimpleClientset()
	tl := NewTunnelLease(fakeK8s, slog.Default(), "pod-1")

	opts1 := LeaseAcquireOptions{
		Namespace:        "default",
		PreviewID:        "my-preview",
		Service:          "api",
		TunnelID:         "tunnel-1",
		ClientInstanceID: "laptop-a",
		Owner:            "alice",
		Port:             8080,
	}
	res1, err := tl.Acquire(context.Background(), opts1)
	require.NoError(t, err)

	// 1. Same owner + same client-id silently re-acquires without Force
	res1b, err := tl.Acquire(context.Background(), LeaseAcquireOptions{
		Namespace:        "default",
		PreviewID:        "my-preview",
		Service:          "api",
		TunnelID:         "tunnel-1b",
		ClientInstanceID: "laptop-a",
		Owner:            "alice",
		Port:             8080,
	})
	require.NoError(t, err)
	assert.Equal(t, "pod-1:tunnel-1", res1b.PreviousHolder)
	assert.True(t, res1b.Evicted)
	assert.Equal(t, int32(1), res1b.LeaseTransitions)
	reacqLease, err := fakeK8s.CoordinationV1().Leases("default").Get(context.Background(), res1.LeaseName, metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "1", reacqLease.Annotations["divergedev.com/tunnel-generation"])

	// 2. Same owner + different client-id without Force returns ErrTunnelConflict
	_, err = tl.Acquire(context.Background(), LeaseAcquireOptions{
		Namespace:        "default",
		PreviewID:        "my-preview",
		Service:          "api",
		TunnelID:         "tunnel-2",
		ClientInstanceID: "laptop-b",
		Owner:            "alice",
		Port:             8080,
	})
	require.ErrorIs(t, err, ErrTunnelConflict)

	// 3. Different owner + Force denied if CheckCrossUserForce fails before Lease mutation
	crossDenied := false
	_, err = tl.Acquire(context.Background(), LeaseAcquireOptions{
		Namespace:        "default",
		PreviewID:        "my-preview",
		Service:          "api",
		TunnelID:         "tunnel-bob",
		ClientInstanceID: "bob-laptop",
		Owner:            "bob",
		Port:             8080,
		Force:            true,
		CheckCrossUserForce: func(existingOwner string) error {
			crossDenied = true
			assert.Equal(t, "alice", existingOwner)
			return errors.New("forbidden")
		},
	})
	require.Error(t, err)
	assert.True(t, crossDenied)

	// Verify Lease still belongs to alice's tunnel-1b
	lease, err := fakeK8s.CoordinationV1().Leases("default").Get(context.Background(), res1.LeaseName, metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "pod-1:tunnel-1b", *lease.Spec.HolderIdentity)

	// 4. Different owner + Force succeeds when CheckCrossUserForce passes
	resBob, err := tl.Acquire(context.Background(), LeaseAcquireOptions{
		Namespace:        "default",
		PreviewID:        "my-preview",
		Service:          "api",
		TunnelID:         "tunnel-bob",
		ClientInstanceID: "bob-laptop",
		Owner:            "bob",
		Port:             8080,
		Force:            true,
		CheckCrossUserForce: func(existingOwner string) error {
			return nil
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "alice", resBob.PreviousOwner)
	assert.True(t, resBob.Evicted)
	assert.GreaterOrEqual(t, resBob.LeaseTransitions, int32(2))
}

func TestTunnelLease_EmptyClientIDAndOwnerlessCrossUserForce(t *testing.T) {
	fakeK8s := fake.NewSimpleClientset()
	tl := NewTunnelLease(fakeK8s, slog.Default(), "pod-1")

	// Authenticated owner with empty ClientInstanceID still conflicts with empty ClientInstanceID.
	_, err := tl.Acquire(context.Background(), LeaseAcquireOptions{
		Namespace: "default",
		PreviewID: "auth-empty-cid",
		TunnelID:  "tunnel-1",
		Owner:     "alice",
		Port:      8080,
	})
	require.NoError(t, err)

	_, err = tl.Acquire(context.Background(), LeaseAcquireOptions{
		Namespace:        "default",
		PreviewID:        "auth-empty-cid",
		TunnelID:         "tunnel-2",
		ClientInstanceID: "laptop-b",
		Owner:            "alice",
		Port:             8080,
	})
	require.ErrorIs(t, err, ErrTunnelConflict)

	_, err = tl.Acquire(context.Background(), LeaseAcquireOptions{
		Namespace: "default",
		PreviewID: "auth-empty-cid",
		TunnelID:  "tunnel-3",
		Owner:     "alice",
		Port:      8080,
	})
	require.ErrorIs(t, err, ErrTunnelConflict)

	// Ownerless holder with ClientInstanceID conflicts with another ClientInstanceID,
	// and Force by an authenticated user triggers CheckCrossUserForce("").
	_, err = tl.Acquire(context.Background(), LeaseAcquireOptions{
		Namespace:        "default",
		PreviewID:        "anon-preview",
		TunnelID:         "anon-1",
		ClientInstanceID: "anon-a",
		Port:             8080,
	})
	require.NoError(t, err)

	_, err = tl.Acquire(context.Background(), LeaseAcquireOptions{
		Namespace:        "default",
		PreviewID:        "anon-preview",
		TunnelID:         "anon-2",
		ClientInstanceID: "anon-b",
		Port:             8080,
	})
	require.ErrorIs(t, err, ErrTunnelConflict)

	crossUserChecked := false
	_, err = tl.Acquire(context.Background(), LeaseAcquireOptions{
		Namespace:        "default",
		PreviewID:        "anon-preview",
		TunnelID:         "bob-1",
		ClientInstanceID: "bob-laptop",
		Owner:            "bob",
		Port:             8080,
		Force:            true,
		CheckCrossUserForce: func(existingOwner string) error {
			crossUserChecked = true
			assert.Empty(t, existingOwner)
			return errors.New("admin required to evict ownerless lease")
		},
	})
	require.Error(t, err)
	assert.True(t, crossUserChecked)
}

func TestTunnelLease_CASRetryLabelsAndIsCurrentHolder(t *testing.T) {
	fakeK8s := fake.NewSimpleClientset()
	tl := NewTunnelLease(fakeK8s, slog.Default(), "pod-1")

	res, err := tl.Acquire(context.Background(), LeaseAcquireOptions{
		Namespace:        "default",
		PreviewID:        "cas-prev",
		Service:          "api",
		TunnelID:         "t1",
		ClientInstanceID: "lap-1",
		Owner:            "alice",
		Port:             8080,
	})
	require.NoError(t, err)

	held, err := tl.IsCurrentHolder(context.Background(), "default", res.LeaseName, "t1")
	require.NoError(t, err)
	assert.True(t, held)
	otherHeld, err := tl.IsCurrentHolder(context.Background(), "default", res.LeaseName, "t-other")
	require.NoError(t, err)
	assert.False(t, otherHeld)

	// Inject two transient 409 Conflicts on Update; 3rd attempt should succeed.
	var updateAttempts atomic.Int32
	fakeK8s.PrependReactor("update", "leases", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		if updateAttempts.Add(1) <= 2 {
			return true, nil, apierrors.NewConflict(schema.GroupResource{Group: "coordination.k8s.io", Resource: "leases"}, res.LeaseName, errors.New("rv race"))
		}
		return false, nil, nil
	})

	res2, err := tl.Acquire(context.Background(), LeaseAcquireOptions{
		Namespace:        "default",
		PreviewID:        "cas-prev",
		Service:          "api",
		TunnelID:         "t2",
		ClientInstanceID: "lap-1",
		Owner:            "alice",
		Port:             8080,
	})
	require.NoError(t, err)
	assert.Equal(t, int32(3), updateAttempts.Load())
	assert.Equal(t, int32(1), res2.LeaseTransitions)

	held, err = tl.IsCurrentHolder(context.Background(), "default", res.LeaseName, "t1")
	require.NoError(t, err)
	assert.False(t, held)
	held2, err := tl.IsCurrentHolder(context.Background(), "default", res.LeaseName, "t2")
	require.NoError(t, err)
	assert.True(t, held2)

	// Mismatched labels on an existing Lease refuse takeover.
	_, err = tl.Acquire(context.Background(), LeaseAcquireOptions{
		Namespace: "default",
		LeaseName: res.LeaseName,
		PreviewID: "different-prev",
		Service:   "api",
		TunnelID:  "t3",
		Force:     true,
		Port:      8080,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "belongs to preview=")
}

func TestTunnelLease_RenewStatesAndConflictRetry(t *testing.T) {
	fakeK8s := fake.NewSimpleClientset()
	tl := NewTunnelLease(fakeK8s, slog.Default(), "pod-1")

	res, err := tl.Acquire(context.Background(), LeaseAcquireOptions{
		Namespace: "default",
		PreviewID: "my-preview",
		TunnelID:  "tunnel-1",
		Port:      8080,
	})
	require.NoError(t, err)

	// 1. Healthy renew
	state, err := tl.Renew(context.Background(), "default", res.LeaseName, "tunnel-1")
	require.NoError(t, err)
	assert.Equal(t, RenewOK, state)

	// 2. Single 409 Conflict triggers one retry and succeeds
	var conflicts atomic.Int32
	fakeK8s.PrependReactor("update", "leases", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		if conflicts.Add(1) == 1 {
			return true, nil, apierrors.NewConflict(schema.GroupResource{Group: "coordination.k8s.io", Resource: "leases"}, res.LeaseName, errors.New("stale rv"))
		}
		return false, nil, nil
	})
	state, err = tl.Renew(context.Background(), "default", res.LeaseName, "tunnel-1")
	require.NoError(t, err)
	assert.Equal(t, RenewOK, state)
	assert.Equal(t, int32(2), conflicts.Load())

	// 3. Transient Get error returns RenewTransientError
	var failGet atomic.Bool
	failGet.Store(true)
	fakeK8s.PrependReactor("get", "leases", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		if failGet.Load() {
			return true, nil, errors.New("apiserver timeout")
		}
		return false, nil, nil
	})
	state, err = tl.Renew(context.Background(), "default", res.LeaseName, "tunnel-1")
	require.Error(t, err)
	assert.Equal(t, RenewTransientError, state)
	failGet.Store(false)

	// 4. Steal by another holder -> RenewEvicted
	_, err = tl.Acquire(context.Background(), LeaseAcquireOptions{
		Namespace: "default",
		PreviewID: "my-preview",
		TunnelID:  "tunnel-2",
		Port:      8080,
		Force:     true,
	})
	require.NoError(t, err)
	state, err = tl.Renew(context.Background(), "default", res.LeaseName, "tunnel-1")
	require.NoError(t, err)
	assert.Equal(t, RenewEvicted, state)
}

func TestTunnelLease_ReleaseWithPreconditions(t *testing.T) {
	fakeK8s := fake.NewSimpleClientset()
	var capturedOpts metav1.DeleteOptions
	fakeK8s.PrependReactor("create", "leases", func(action k8stesting.Action) (bool, runtime.Object, error) {
		obj := action.(k8stesting.CreateAction).GetObject().(*coordinationv1.Lease)
		obj.UID = "lease-uid-123"
		obj.ResourceVersion = "rv-42"
		return false, nil, nil
	})
	fakeK8s.PrependReactor("delete", "leases", func(action k8stesting.Action) (bool, runtime.Object, error) {
		capturedOpts = action.(k8stesting.DeleteAction).GetDeleteOptions()
		return false, nil, nil
	})

	tl := NewTunnelLease(fakeK8s, slog.Default(), "pod-1")
	res, err := tl.Acquire(context.Background(), LeaseAcquireOptions{
		Namespace: "default",
		PreviewID: "my-preview",
		TunnelID:  "tunnel-abc",
		Port:      8080,
	})
	require.NoError(t, err)
	assert.Equal(t, "lease-uid-123", string(res.LeaseUID))

	// Wrong holder cannot release
	assert.False(t, tl.Release(context.Background(), "default", res.LeaseName, "other-tunnel"))

	// Correct holder releases with UID + ResourceVersion preconditions
	assert.True(t, tl.Release(context.Background(), "default", res.LeaseName, "tunnel-abc"))
	require.NotNil(t, capturedOpts.Preconditions)
	require.NotNil(t, capturedOpts.Preconditions.UID)
	require.NotNil(t, capturedOpts.Preconditions.ResourceVersion)
	assert.Equal(t, "lease-uid-123", string(*capturedOpts.Preconditions.UID))
	assert.Equal(t, "rv-42", *capturedOpts.Preconditions.ResourceVersion)

	state, err := tl.Renew(context.Background(), "default", res.LeaseName, "tunnel-abc")
	require.NoError(t, err)
	assert.Equal(t, RenewEvicted, state)

	// Expired lease can be overtaken without Force
	expiredAt := metav1.NewMicroTime(time.Now().Add(-2 * time.Minute))
	dur := int32(60)
	holder := "dead-pod:dead-tunnel"
	_, err = fakeK8s.CoordinationV1().Leases("default").Create(context.Background(), &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "diverge-tunnel-lease-expired-takeover",
			Namespace: "default",
			Annotations: map[string]string{
				"divergedev.com/tunnel-owner":     "alice",
				"divergedev.com/tunnel-client-id": "old-laptop",
			},
		},
		Spec: coordinationv1.LeaseSpec{
			HolderIdentity:       &holder,
			LeaseDurationSeconds: &dur,
			RenewTime:            &expiredAt,
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	_, err = tl.Acquire(context.Background(), LeaseAcquireOptions{
		Namespace:        "default",
		PreviewID:        "expired-takeover",
		LeaseName:        "diverge-tunnel-lease-expired-takeover",
		TunnelID:         "new-tunnel",
		ClientInstanceID: "new-laptop",
		Owner:            "bob",
		Port:             8080,
	})
	require.NoError(t, err)
}
