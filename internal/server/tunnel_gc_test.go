package server

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestTunnelGC_SweepsExpiredService(t *testing.T) {
	fakeK8s := fake.NewSimpleClientset()
	logger := slog.Default()
	gc := NewTunnelGC(fakeK8s, logger)

	// Create an expired tunnel service
	expired := time.Now().Add(-1 * time.Minute).Format(time.RFC3339)
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "diverge-tunnel-old",
			Namespace: "default",
			Labels: map[string]string{
				"divergedev.com/tunnel": "true",
			},
			Annotations: map[string]string{
				"divergedev.com/tunnel-expires": expired,
			},
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: "None",
		},
	}

	_, err := fakeK8s.CoreV1().Services("default").Create(context.Background(), svc, metav1.CreateOptions{})
	require.NoError(t, err)

	// Verify it exists
	_, err = fakeK8s.CoreV1().Services("default").Get(context.Background(), "diverge-tunnel-old", metav1.GetOptions{})
	require.NoError(t, err)

	// Run sweep
	gc.sweep(context.Background(), "default")

	// Should be deleted
	_, err = fakeK8s.CoreV1().Services("default").Get(context.Background(), "diverge-tunnel-old", metav1.GetOptions{})
	assert.Error(t, err, "expired service should be deleted")
}

func TestTunnelGC_KeepsNonExpiredService(t *testing.T) {
	fakeK8s := fake.NewSimpleClientset()
	logger := slog.Default()
	gc := NewTunnelGC(fakeK8s, logger)

	// Create a non-expired tunnel service
	future := time.Now().Add(5 * time.Minute).Format(time.RFC3339)
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "diverge-tunnel-active",
			Namespace: "default",
			Labels: map[string]string{
				"divergedev.com/tunnel": "true",
			},
			Annotations: map[string]string{
				"divergedev.com/tunnel-expires": future,
			},
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: "None",
		},
	}

	_, err := fakeK8s.CoreV1().Services("default").Create(context.Background(), svc, metav1.CreateOptions{})
	require.NoError(t, err)

	gc.sweep(context.Background(), "default")

	// Should still exist
	_, err = fakeK8s.CoreV1().Services("default").Get(context.Background(), "diverge-tunnel-active", metav1.GetOptions{})
	assert.NoError(t, err, "non-expired service should be kept")
}

func TestTunnelGC_IgnoresServicesWithoutAnnotation(t *testing.T) {
	fakeK8s := fake.NewSimpleClientset()
	logger := slog.Default()
	gc := NewTunnelGC(fakeK8s, logger)

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "diverge-tunnel-noannot",
			Namespace: "default",
			Labels: map[string]string{
				"divergedev.com/tunnel": "true",
			},
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: "None",
		},
	}

	_, err := fakeK8s.CoreV1().Services("default").Create(context.Background(), svc, metav1.CreateOptions{})
	require.NoError(t, err)

	gc.sweep(context.Background(), "default")

	_, err = fakeK8s.CoreV1().Services("default").Get(context.Background(), "diverge-tunnel-noannot", metav1.GetOptions{})
	assert.NoError(t, err, "service without annotation should be kept")
}

func TestTunnelGC_SweepsExpiredEndpoints(t *testing.T) {
	fakeK8s := fake.NewSimpleClientset()
	logger := slog.Default()
	gc := NewTunnelGC(fakeK8s, logger)

	expired := time.Now().Add(-1 * time.Minute).Format(time.RFC3339)
	ep := &corev1.Endpoints{ //nolint:staticcheck // kube-dns backward compatibility
		ObjectMeta: metav1.ObjectMeta{
			Name:      "diverge-tunnel-old-ep",
			Namespace: "default",
			Labels: map[string]string{
				"divergedev.com/tunnel": "true",
			},
			Annotations: map[string]string{
				"divergedev.com/tunnel-expires": expired,
			},
		},
		Subsets: []corev1.EndpointSubset{{ //nolint:staticcheck // kube-dns backward compatibility
			Addresses: []corev1.EndpointAddress{{IP: "10.0.0.1"}},
			Ports:     []corev1.EndpointPort{{Port: 8081, Protocol: corev1.ProtocolTCP}},
		}},
	}

	_, err := fakeK8s.CoreV1().Endpoints("default").Create(context.Background(), ep, metav1.CreateOptions{})
	require.NoError(t, err)

	gc.sweep(context.Background(), "default")

	_, err = fakeK8s.CoreV1().Endpoints("default").Get(context.Background(), "diverge-tunnel-old-ep", metav1.GetOptions{})
	assert.Error(t, err, "expired endpoints should be deleted by GC")
}

func TestTunnelGC_SweepsLeasesViaRenewTimeAndDuration(t *testing.T) {
	fakeK8s := fake.NewSimpleClientset()
	logger := slog.Default()
	gc := NewTunnelGC(fakeK8s, logger)

	dur := int32(60)
	expiredRenew := metav1.NewMicroTime(time.Now().Add(-2 * time.Minute))
	activeRenew := metav1.NewMicroTime(time.Now().Add(-10 * time.Second))

	expiredLease := &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "diverge-tunnel-lease-expired",
			Namespace: "default",
			Labels: map[string]string{
				"divergedev.com/tunnel": "true",
			},
		},
		Spec: coordinationv1.LeaseSpec{
			RenewTime:            &expiredRenew,
			LeaseDurationSeconds: &dur,
		},
	}
	activeLease := &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "diverge-tunnel-lease-active",
			Namespace: "default",
			Labels: map[string]string{
				"divergedev.com/tunnel": "true",
			},
		},
		Spec: coordinationv1.LeaseSpec{
			RenewTime:            &activeRenew,
			LeaseDurationSeconds: &dur,
		},
	}

	_, err := fakeK8s.CoordinationV1().Leases("default").Create(context.Background(), expiredLease, metav1.CreateOptions{})
	require.NoError(t, err)
	_, err = fakeK8s.CoordinationV1().Leases("default").Create(context.Background(), activeLease, metav1.CreateOptions{})
	require.NoError(t, err)

	gc.sweep(context.Background(), "default")

	_, err = fakeK8s.CoordinationV1().Leases("default").Get(context.Background(), "diverge-tunnel-lease-expired", metav1.GetOptions{})
	assert.Error(t, err, "expired lease should be deleted via RenewTime + LeaseDurationSeconds")

	_, err = fakeK8s.CoordinationV1().Leases("default").Get(context.Background(), "diverge-tunnel-lease-active", metav1.GetOptions{})
	assert.NoError(t, err, "active lease should be kept")
}

func TestTunnelGC_DoesNotAbortOnServiceListErrorAndUsesPreconditions(t *testing.T) {
	fakeK8s := fake.NewSimpleClientset()
	fakeK8s.PrependReactor("list", "services", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("services rbac forbidden")
	})
	var capturedDelete metav1.DeleteOptions
	fakeK8s.PrependReactor("delete", "leases", func(action k8stesting.Action) (bool, runtime.Object, error) {
		capturedDelete = action.(k8stesting.DeleteAction).GetDeleteOptions()
		return false, nil, nil
	})

	dur := int32(60)
	expiredRenew := metav1.NewMicroTime(time.Now().Add(-2 * time.Minute))
	_, err := fakeK8s.CoordinationV1().Leases("default").Create(context.Background(), &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "diverge-tunnel-lease-expired-err",
			Namespace:       "default",
			UID:             "uid-999",
			ResourceVersion: "rv-88",
			Labels: map[string]string{
				"divergedev.com/tunnel": "true",
			},
		},
		Spec: coordinationv1.LeaseSpec{
			RenewTime:            &expiredRenew,
			LeaseDurationSeconds: &dur,
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	gc := NewTunnelGC(fakeK8s, slog.Default())
	gc.sweep(context.Background(), "default")

	_, err = fakeK8s.CoordinationV1().Leases("default").Get(context.Background(), "diverge-tunnel-lease-expired-err", metav1.GetOptions{})
	assert.Error(t, err, "expired lease must still be deleted when listing services fails")
	require.NotNil(t, capturedDelete.Preconditions)
	assert.Equal(t, "uid-999", string(*capturedDelete.Preconditions.UID))
	assert.Equal(t, "rv-88", *capturedDelete.Preconditions.ResourceVersion)
}

func TestTunnelGC_SweepsLegacyResourcesWhenCreateServicesFalse(t *testing.T) {
	fakeK8s := fake.NewSimpleClientset()
	expired := time.Now().Add(-1 * time.Minute).Format(time.RFC3339)
	labels := map[string]string{"divergedev.com/tunnel": "true"}
	ann := map[string]string{"divergedev.com/tunnel-expires": expired}

	_, err := fakeK8s.CoreV1().Services("default").Create(context.Background(), &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "diverge-tunnel-legacy-svc", Namespace: "default", Labels: labels, Annotations: ann},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	_, err = fakeK8s.DiscoveryV1().EndpointSlices("default").Create(context.Background(), &discoveryv1.EndpointSlice{
		ObjectMeta:  metav1.ObjectMeta{Name: "diverge-tunnel-legacy-ep", Namespace: "default", Labels: labels, Annotations: ann},
		AddressType: discoveryv1.AddressTypeIPv4,
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	dur := int32(60)
	expiredRenew := metav1.NewMicroTime(time.Now().Add(-2 * time.Minute))
	_, err = fakeK8s.CoordinationV1().Leases("default").Create(context.Background(), &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{Name: "diverge-tunnel-lease-nosvc", Namespace: "default", Labels: labels},
		Spec:       coordinationv1.LeaseSpec{RenewTime: &expiredRenew, LeaseDurationSeconds: &dur},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	gc := NewTunnelGC(fakeK8s, slog.Default()).WithCreateServices(false)
	gc.sweep(context.Background(), "default")

	_, err = fakeK8s.CoreV1().Services("default").Get(context.Background(), "diverge-tunnel-legacy-svc", metav1.GetOptions{})
	assert.Error(t, err, "legacy expired service should be swept even when createServices=false")
	_, err = fakeK8s.DiscoveryV1().EndpointSlices("default").Get(context.Background(), "diverge-tunnel-legacy-ep", metav1.GetOptions{})
	assert.Error(t, err, "legacy expired endpointslice should be swept even when createServices=false")
	_, err = fakeK8s.CoordinationV1().Leases("default").Get(context.Background(), "diverge-tunnel-lease-nosvc", metav1.GetOptions{})
	assert.Error(t, err)
}

func TestTunnelGC_SweepsNamespaceAllAndNilClient(t *testing.T) {
	// Nil client must be a safe no-op.
	NewTunnelGC(nil, slog.Default()).sweep(context.Background(), "default")

	fakeK8s := fake.NewSimpleClientset()
	expired := time.Now().Add(-2 * time.Minute).Format(time.RFC3339)
	expiredRenew := metav1.NewMicroTime(time.Now().Add(-2 * time.Minute))
	dur := int32(60)
	labels := map[string]string{"divergedev.com/tunnel": "true"}
	ann := map[string]string{"divergedev.com/tunnel-expires": expired}

	for _, ns := range []string{"ns-a", "ns-b"} {
		_, err := fakeK8s.CoreV1().Services(ns).Create(context.Background(), &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "diverge-tunnel-" + ns, Namespace: ns, Labels: labels, Annotations: ann},
		}, metav1.CreateOptions{})
		require.NoError(t, err)

		_, err = fakeK8s.DiscoveryV1().EndpointSlices(ns).Create(context.Background(), &discoveryv1.EndpointSlice{
			ObjectMeta:  metav1.ObjectMeta{Name: "diverge-tunnel-" + ns, Namespace: ns, Labels: labels, Annotations: ann},
			AddressType: discoveryv1.AddressTypeIPv4,
		}, metav1.CreateOptions{})
		require.NoError(t, err)

		_, err = fakeK8s.CoreV1().Endpoints(ns).Create(context.Background(), &corev1.Endpoints{ //nolint:staticcheck // testing legacy Endpoints GC
			ObjectMeta: metav1.ObjectMeta{Name: "diverge-tunnel-" + ns, Namespace: ns, Labels: labels, Annotations: ann},
		}, metav1.CreateOptions{})
		require.NoError(t, err)

		_, err = fakeK8s.CoordinationV1().Leases(ns).Create(context.Background(), &coordinationv1.Lease{
			ObjectMeta: metav1.ObjectMeta{Name: "diverge-tunnel-lease-" + ns, Namespace: ns, Labels: labels},
			Spec:       coordinationv1.LeaseSpec{RenewTime: &expiredRenew, LeaseDurationSeconds: &dur},
		}, metav1.CreateOptions{})
		require.NoError(t, err)
	}

	gc := NewTunnelGC(fakeK8s, slog.Default())
	gc.sweep(context.Background(), metav1.NamespaceAll)

	for _, ns := range []string{"ns-a", "ns-b"} {
		_, err := fakeK8s.CoreV1().Services(ns).Get(context.Background(), "diverge-tunnel-"+ns, metav1.GetOptions{})
		assert.Error(t, err, "expired service in %s should be deleted via NamespaceAll", ns)
		_, err = fakeK8s.DiscoveryV1().EndpointSlices(ns).Get(context.Background(), "diverge-tunnel-"+ns, metav1.GetOptions{})
		assert.Error(t, err, "expired endpointslice in %s should be deleted via NamespaceAll", ns)
		_, err = fakeK8s.CoreV1().Endpoints(ns).Get(context.Background(), "diverge-tunnel-"+ns, metav1.GetOptions{}) //nolint:staticcheck // verifying legacy Endpoints cleanup
		assert.Error(t, err, "expired endpoints in %s should be deleted via NamespaceAll", ns)
		_, err = fakeK8s.CoordinationV1().Leases(ns).Get(context.Background(), "diverge-tunnel-lease-"+ns, metav1.GetOptions{})
		assert.Error(t, err, "expired lease in %s should be deleted via NamespaceAll", ns)
	}
}
