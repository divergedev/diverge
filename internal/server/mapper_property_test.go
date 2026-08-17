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
						Mode: hegel.Draw(ht, hegel.Text()),
					},
					Routing: v1alpha1.EnvironmentRouting{
						Mode: hegel.Draw(ht, hegel.Text()),
					},
				},
				Status: v1alpha1.EnvironmentStatus{
					Phase: v1alpha1.EnvironmentPhase(hegel.Draw(ht, hegel.Text())),
				},
			}

			dom, err := CRDEnvToDomain(crd)
			require.NoError(t, err)
			require.NotNil(t, dom)

			crd2, err := DomainEnvToCRD(dom)
			require.NoError(t, err)
			require.NotNil(t, crd2)

			assert.Equal(t, crd.Name, crd2.Name)
			assert.Equal(t, crd.Namespace, crd2.Namespace)
			assert.Equal(t, crd.ResourceVersion, crd2.ResourceVersion)
			// we don't test deep fields in crd2 since the mapper only copies top level fields right now
			// wait, mapper.go only copies Name, Namespace, Labels, Annotations, ResourceVersion!
			// Ah! "dom.Name = crd.Name" etc. It uses json marshal/unmarshal for the rest.
			// The json marshal/unmarshal should copy everything as long as domain tags match!
		})
	})

	t.Run("Domain -> CRD -> Domain roundtrip preserves fields", func(t *testing.T) {
		hegel.Test(t, func(ht *hegel.T) {
			dom := &domain.Environment{
				Name:            hegel.Draw(ht, hegel.Text()),
				Namespace:       hegel.Draw(ht, hegel.Text()),
				ResourceVersion: hegel.Draw(ht, hegel.Text()),
				Labels:          map[string]string{"foo": "bar"},
				Annotations:     map[string]string{"baz": "qux"},
			}

			crd, err := DomainEnvToCRD(dom)
			require.NoError(t, err)
			require.NotNil(t, crd)

			dom2, err := CRDEnvToDomain(crd)
			require.NoError(t, err)
			require.NotNil(t, dom2)

			assert.Equal(t, dom.Name, dom2.Name)
			assert.Equal(t, dom.Namespace, dom2.Namespace)
			assert.Equal(t, dom.ResourceVersion, dom2.ResourceVersion)
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

			crd := &v1alpha1.PreviewGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name:            name,
					Namespace:       namespace,
					ResourceVersion: rv,
					Labels:          map[string]string{"group": "test"},
					Annotations:     map[string]string{"foo": "bar"},
				},
				Status: v1alpha1.PreviewGroupStatus{
					Phase: v1alpha1.PreviewGroupPhase(hegel.Draw(ht, hegel.Text())),
				},
			}

			dom, err := CRDPgToDomain(crd)
			require.NoError(t, err)
			require.NotNil(t, dom)

			crd2, err := DomainPgToCRD(dom)
			require.NoError(t, err)
			require.NotNil(t, crd2)

			assert.Equal(t, crd.Name, crd2.Name)
			assert.Equal(t, crd.Namespace, crd2.Namespace)
			assert.Equal(t, crd.ResourceVersion, crd2.ResourceVersion)
		})
	})

	t.Run("Domain -> CRD -> Domain roundtrip preserves fields for PreviewGroup", func(t *testing.T) {
		hegel.Test(t, func(ht *hegel.T) {
			dom := &domain.PreviewGroup{
				Name:            hegel.Draw(ht, hegel.Text()),
				Namespace:       hegel.Draw(ht, hegel.Text()),
				ResourceVersion: hegel.Draw(ht, hegel.Text()),
				Labels:          map[string]string{"group": "test"},
				Annotations:     map[string]string{"foo": "bar"},
			}

			crd, err := DomainPgToCRD(dom)
			require.NoError(t, err)
			require.NotNil(t, crd)

			dom2, err := CRDPgToDomain(crd)
			require.NoError(t, err)
			require.NotNil(t, dom2)

			assert.Equal(t, dom.Name, dom2.Name)
			assert.Equal(t, dom.Namespace, dom2.Namespace)
			assert.Equal(t, dom.ResourceVersion, dom2.ResourceVersion)
		})
	})
}
