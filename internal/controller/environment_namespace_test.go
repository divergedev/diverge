package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

func TestEnsureNamespace_AmbientMeshLabels(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, networkingv1.AddToScheme(scheme))
	require.NoError(t, divergeiov1alpha1.AddToScheme(scheme))

	tests := []struct {
		name         string
		annotations  map[string]string
		wantAmbient  string
		wantWaypoint string
	}{
		{
			name:         "without ambient-mesh or waypoint annotations",
			annotations:  nil,
			wantAmbient:  "",
			wantWaypoint: "",
		},
		{
			name: "with ambient-mesh=true only",
			annotations: map[string]string{
				"divergedev.com/ambient-mesh": "true",
			},
			wantAmbient:  "ambient",
			wantWaypoint: "",
		},
		{
			name: "with ambient-mesh=true and waypoint",
			annotations: map[string]string{
				"divergedev.com/ambient-mesh": "true",
				"divergedev.com/waypoint":     "diverge-waypoint",
			},
			wantAmbient:  "ambient",
			wantWaypoint: "diverge-waypoint",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := fake.NewClientBuilder().WithScheme(scheme).Build()
			r := &EnvironmentReconciler{
				Client: c,
				Scheme: scheme,
			}

			env := &divergeiov1alpha1.Environment{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-env",
					Namespace:   "default",
					Annotations: tt.annotations,
				},
				Spec: divergeiov1alpha1.EnvironmentSpec{
					Deploy: divergeiov1alpha1.EnvironmentDeploy{
						Namespace: "create",
					},
				},
			}

			err := r.ensureNamespace(context.Background(), env)
			require.NoError(t, err)

			var ns corev1.Namespace
			err = c.Get(context.Background(), client.ObjectKey{Name: env.PreviewNamespace()}, &ns)
			require.NoError(t, err)

			assert.Equal(t, "restricted", ns.Labels["pod-security.kubernetes.io/enforce"])
			assert.Equal(t, tt.wantAmbient, ns.Labels["istio.io/dataplane-mode"])
			assert.Equal(t, tt.wantWaypoint, ns.Labels["istio.io/use-waypoint"])
		})
	}
}
