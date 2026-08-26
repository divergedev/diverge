package otel

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fakediscovery "k8s.io/client-go/discovery/fake"
	coreclient "k8s.io/client-go/kubernetes/fake"
)

func TestIsOperatorInstalled(t *testing.T) {
	tests := []struct {
		name        string
		resources   []*metav1.APIResourceList
		wantFound   bool
		wantVersion string
	}{
		{
			name: "Found_v1alpha2",
			resources: []*metav1.APIResourceList{
				{
					GroupVersion: "opentelemetry.io/v1alpha2",
					APIResources: []metav1.APIResource{{Kind: "Instrumentation"}},
				},
			},
			wantFound:   true,
			wantVersion: "opentelemetry.io/v1alpha2",
		},
		{
			name: "Found_v1alpha1_Fallback",
			resources: []*metav1.APIResourceList{
				{
					GroupVersion: "opentelemetry.io/v1alpha1",
					APIResources: []metav1.APIResource{{Kind: "Instrumentation"}},
				},
			},
			wantFound:   true,
			wantVersion: "opentelemetry.io/v1alpha1",
		},
		{
			name:      "NotFound",
			resources: []*metav1.APIResourceList{},
			wantFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clientset := coreclient.NewSimpleClientset()
			fakeDisc := clientset.Discovery().(*fakediscovery.FakeDiscovery)
			fakeDisc.Resources = tt.resources

			found, version, err := IsOperatorInstalled(context.Background(), fakeDisc)
			require.NoError(t, err)
			assert.Equal(t, tt.wantFound, found)
			if tt.wantFound {
				assert.Equal(t, tt.wantVersion, version)
			}
		})
	}
}
