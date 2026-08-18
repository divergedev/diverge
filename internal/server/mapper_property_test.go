//go:build !no_schema

package server

import (
	"github.com/divergedev/diverge/api/v1alpha1"
	domain "github.com/divergedev/diverge/gen/domain/github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hegel.dev/go/hegel"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"testing"
)

func TestEnvironmentMapper_Property(t *testing.T) {
	nilDomain, err := CRDEnvToDomain(nil)
	assert.NoError(t, err)
	assert.Nil(t, nilDomain)

	nilCRD, err := DomainEnvToCRD(nil)
	assert.NoError(t, err)
	assert.Nil(t, nilCRD)

	t.Run("CRD -> Domain -> CRD roundtrip preserves fields", func(t *testing.T) {
		hegel.Test(t, func(ht *hegel.T) {
			name := hegel.Draw(ht, hegel.Text())
			namespace := hegel.Draw(ht, hegel.Text())
			rv := hegel.Draw(ht, hegel.Text())
			deployMode := hegel.Draw(ht, hegel.Text())
			routingMode := hegel.Draw(ht, hegel.Text())
			phase := hegel.Draw(ht, hegel.Text())

			crd := &v1alpha1.Environment{
				ObjectMeta: metav1.ObjectMeta{
					Name:            name,
					Namespace:       namespace,
					ResourceVersion: rv,
					Labels:          map[string]string{"foo": "bar"},
					Annotations:     map[string]string{"baz": "qux"},
				},
				Spec: v1alpha1.EnvironmentSpec{
					Deploy: v1alpha1.EnvironmentDeploy{
						Mode: deployMode,
					},
					Routing: v1alpha1.EnvironmentRouting{
						Mode: routingMode,
					},
				},
				Status: v1alpha1.EnvironmentStatus{
					Phase: v1alpha1.EnvironmentPhase(phase),
				},
			}

			dom, err := CRDEnvToDomain(crd)
			require.NoError(ht, err)
			require.NotNil(ht, dom)

			crd2, err := DomainEnvToCRD(dom)
			require.NoError(ht, err)
			require.NotNil(ht, crd2)

			assert.Equal(ht, crd.Name, crd2.Name)
			assert.Equal(ht, crd.Namespace, crd2.Namespace)
			assert.Equal(ht, crd.ResourceVersion, crd2.ResourceVersion)
			assert.Equal(ht, crd.Labels, crd2.Labels)
			assert.Equal(ht, crd.Annotations, crd2.Annotations)
			assert.Equal(ht, crd.Spec.Deploy.Mode, crd2.Spec.Deploy.Mode)
			assert.Equal(ht, crd.Spec.Routing.Mode, crd2.Spec.Routing.Mode)
			assert.Equal(ht, crd.Status.Phase, crd2.Status.Phase)
		})
	})

	t.Run("Domain -> CRD -> Domain roundtrip preserves fields", func(t *testing.T) {
		hegel.Test(t, func(ht *hegel.T) {
			deployMode := hegel.Draw(ht, hegel.Text())
			routingMode := hegel.Draw(ht, hegel.Text())
			phase := hegel.Draw(ht, hegel.Text())

			dom := &domain.Environment{
				Name:            hegel.Draw(ht, hegel.Text()),
				Namespace:       hegel.Draw(ht, hegel.Text()),
				ResourceVersion: hegel.Draw(ht, hegel.Text()),
				Labels:          map[string]string{"foo": "bar"},
				Annotations:     map[string]string{"baz": "qux"},
				Spec: &domain.EnvironmentSpec{
					Deploy: &domain.EnvironmentDeploy{
						Mode: deployMode,
					},
					Routing: &domain.EnvironmentRouting{
						Mode: routingMode,
					},
				},
				Status: &domain.EnvironmentStatus{
					Phase: phase,
				},
			}

			crd, err := DomainEnvToCRD(dom)
			require.NoError(ht, err)
			require.NotNil(ht, crd)

			dom2, err := CRDEnvToDomain(crd)
			require.NoError(ht, err)
			require.NotNil(ht, dom2)

			assert.Equal(ht, dom.Name, dom2.Name)
			assert.Equal(ht, dom.Namespace, dom2.Namespace)
			assert.Equal(ht, dom.ResourceVersion, dom2.ResourceVersion)
			assert.Equal(ht, dom.Labels, dom2.Labels)
			assert.Equal(ht, dom.Annotations, dom2.Annotations)
			require.NotNil(ht, dom2.Spec)
			assert.Equal(ht, dom.Spec.Deploy.Mode, dom2.Spec.Deploy.Mode)
			assert.Equal(ht, dom.Spec.Routing.Mode, dom2.Spec.Routing.Mode)
			require.NotNil(ht, dom2.Status)
			assert.Equal(ht, dom.Status.Phase, dom2.Status.Phase)
		})
	})
}

func TestPreviewGroupMapper_Property(t *testing.T) {
	nilDomain, err := CRDPgToDomain(nil)
	assert.NoError(t, err)
	assert.Nil(t, nilDomain)

	nilCRD, err := DomainPgToCRD(nil)
	assert.NoError(t, err)
	assert.Nil(t, nilCRD)

	t.Run("CRD -> Domain -> CRD roundtrip preserves fields for PreviewGroup", func(t *testing.T) {
		hegel.Test(t, func(ht *hegel.T) {
			name := hegel.Draw(ht, hegel.Text())
			namespace := hegel.Draw(ht, hegel.Text())
			rv := hegel.Draw(ht, hegel.Text())
			owner := hegel.Draw(ht, hegel.Text())
			routingMode := hegel.Draw(ht, hegel.Text())
			phase := hegel.Draw(ht, hegel.Text())

			crd := &v1alpha1.PreviewGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name:            name,
					Namespace:       namespace,
					ResourceVersion: rv,
					Labels:          map[string]string{"group": "test"},
					Annotations:     map[string]string{"foo": "bar"},
				},
				Spec: v1alpha1.PreviewGroupSpec{
					Owner: owner,
					Routing: v1alpha1.PreviewGroupRouting{
						Mode: routingMode,
					},
				},
				Status: v1alpha1.PreviewGroupStatus{
					Phase: v1alpha1.PreviewGroupPhase(phase),
				},
			}

			dom, err := CRDPgToDomain(crd)
			require.NoError(ht, err)
			require.NotNil(ht, dom)

			crd2, err := DomainPgToCRD(dom)
			require.NoError(ht, err)
			require.NotNil(ht, crd2)

			assert.Equal(ht, crd.Name, crd2.Name)
			assert.Equal(ht, crd.Namespace, crd2.Namespace)
			assert.Equal(ht, crd.ResourceVersion, crd2.ResourceVersion)
			assert.Equal(ht, crd.Labels, crd2.Labels)
			assert.Equal(ht, crd.Annotations, crd2.Annotations)
			assert.Equal(ht, crd.Spec.Owner, crd2.Spec.Owner)
			assert.Equal(ht, crd.Spec.Routing.Mode, crd2.Spec.Routing.Mode)
			assert.Equal(ht, crd.Status.Phase, crd2.Status.Phase)
		})
	})

	t.Run("Domain -> CRD -> Domain roundtrip preserves fields for PreviewGroup", func(t *testing.T) {
		hegel.Test(t, func(ht *hegel.T) {
			name := hegel.Draw(ht, hegel.Text())
			namespace := hegel.Draw(ht, hegel.Text())
			rv := hegel.Draw(ht, hegel.Text())
			owner := hegel.Draw(ht, hegel.Text())
			routingMode := hegel.Draw(ht, hegel.Text())
			phase := hegel.Draw(ht, hegel.Text())

			dom := &domain.PreviewGroup{
				Name:            name,
				Namespace:       namespace,
				ResourceVersion: rv,
				Labels:          map[string]string{"group": "test"},
				Annotations:     map[string]string{"foo": "bar"},
				Spec: &domain.PreviewGroupSpec{
					Owner: owner,
					Routing: &domain.PreviewGroupRouting{
						Mode: routingMode,
					},
				},
				Status: &domain.PreviewGroupStatus{
					Phase: phase,
				},
			}

			crd, err := DomainPgToCRD(dom)
			require.NoError(ht, err)
			require.NotNil(ht, crd)

			dom2, err := CRDPgToDomain(crd)
			require.NoError(ht, err)
			require.NotNil(ht, dom2)

			assert.Equal(ht, dom.Name, dom2.Name)
			assert.Equal(ht, dom.Namespace, dom2.Namespace)
			assert.Equal(ht, dom.ResourceVersion, dom2.ResourceVersion)
			assert.Equal(ht, dom.Labels, dom2.Labels)
			assert.Equal(ht, dom.Annotations, dom2.Annotations)
			require.NotNil(ht, dom2.Spec)
			assert.Equal(ht, dom.Spec.Owner, dom2.Spec.Owner)
			assert.Equal(ht, dom.Spec.Routing.Mode, dom2.Spec.Routing.Mode)
			require.NotNil(ht, dom2.Status)
			assert.Equal(ht, dom.Status.Phase, dom2.Status.Phase)
		})
	})
}
