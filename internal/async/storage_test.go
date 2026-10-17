package async

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/pkg/registry"
)

func TestStorageProvisioner_NameAndRegistry(t *testing.T) {
	s := &StorageProvisioner{}
	assert.Equal(t, "s3", s.Name())

	gcs := &StorageProvisioner{Provider: "gcs"}
	assert.Equal(t, "gcs", gcs.Name())

	minio := &StorageProvisioner{Provider: "minio"}
	assert.Equal(t, "minio", minio.Name())

	assert.True(t, Providers.Has("s3"))
	assert.True(t, Providers.Has("gcs"))
	assert.True(t, Providers.Has("minio"))

	for _, proto := range []string{"s3", "gcs", "minio"} {
		prov, err := Providers.Create(proto, registry.Deps{})
		require.NoError(t, err)
		assert.Equal(t, proto, prov.Name())
	}
}

func TestFormatStorageKeyPrefix(t *testing.T) {
	assert.Equal(t, "previews/pr-42/", FormatStorageKeyPrefix("", "pr-42"))
	assert.Equal(t, "previews/pr-42/", FormatStorageKeyPrefix("default", "pr-42"))
	assert.Equal(t, "previews/pr-42/uploads/", FormatStorageKeyPrefix("uploads", "pr-42"))
	assert.Equal(t, "previews/pr-42/uploads/", FormatStorageKeyPrefix("/uploads/", "/pr-42/"))

	assert.Equal(t, "previews/staging/pr-42/", FormatStorageKeyPrefix("", "pr-42", "staging"))
	assert.Equal(t, "previews/staging/pr-42/", FormatStorageKeyPrefix("default", "pr-42", "staging"))
	assert.Equal(t, "previews/staging/pr-42/uploads/", FormatStorageKeyPrefix("uploads", "pr-42", "/staging/"))
}

func TestStorageProvisioner_Provision_ProtocolsAndDefaults(t *testing.T) {
	t.Setenv("DIVERGE_STORAGE_BUCKET", "")
	t.Setenv("DIVERGE_STORAGE_ENDPOINT", "")

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "pr-42"},
	}

	// Default target with s3 (unset namespace defaults to "default")
	s3Prov := &StorageProvisioner{Provider: "s3"}
	resDefault, err := s3Prov.Provision(context.Background(), env, v1alpha1.AsyncRouteSpec{
		Protocol: "s3",
		Target:   "default",
	})
	require.NoError(t, err)
	assert.Equal(t, "previews/default/pr-42/", resDefault.ResolvedTarget)
	assert.Equal(t, "previews/default/pr-42/", resDefault.EnvVars["DIVERGE_STORAGE_PREFIX"])
	assert.Equal(t, "previews/default/pr-42/", resDefault.EnvVars["S3_KEY_PREFIX"])
	assert.Equal(t, "previews/default/pr-42/", resDefault.EnvVars["AWS_S3_PREFIX"])
	assert.Equal(t, "previews/default/pr-42/", resDefault.EnvVars["GCS_KEY_PREFIX"])
	_, hasBucket := resDefault.EnvVars["DIVERGE_STORAGE_BUCKET"]
	assert.False(t, hasBucket)
	_, hasEndpoint := resDefault.EnvVars["DIVERGE_STORAGE_ENDPOINT"]
	assert.False(t, hasEndpoint)

	// Named target with gcs (falls back to route.Target as bucket when no explicit bucket set)
	gcsProv := &StorageProvisioner{Provider: "gcs"}
	resGCS, err := gcsProv.Provision(context.Background(), env, v1alpha1.AsyncRouteSpec{
		Protocol: "gcs",
		Target:   "uploads",
	})
	require.NoError(t, err)
	assert.Equal(t, "previews/default/pr-42/uploads/", resGCS.ResolvedTarget)
	assert.Equal(t, "previews/default/pr-42/uploads/", resGCS.EnvVars["DIVERGE_STORAGE_PREFIX"])
	assert.Equal(t, "previews/default/pr-42/uploads/", resGCS.EnvVars["S3_KEY_PREFIX"])
	assert.Equal(t, "previews/default/pr-42/uploads/", resGCS.EnvVars["AWS_S3_PREFIX"])
	assert.Equal(t, "previews/default/pr-42/uploads/", resGCS.EnvVars["GCS_KEY_PREFIX"])
	assert.Equal(t, "previews/default/pr-42/uploads/", resGCS.EnvVars["DIVERGE_STORAGE_PREFIX_UPLOADS"])
	assert.Equal(t, "previews/default/pr-42/uploads/", resGCS.EnvVars["GCS_KEY_PREFIX_UPLOADS"])
	assert.Equal(t, "uploads", resGCS.EnvVars["DIVERGE_STORAGE_BUCKET"])
	assert.Equal(t, "uploads", resGCS.EnvVars["DIVERGE_STORAGE_BUCKET_UPLOADS"])

	// MinIO with configured Bucket and Endpoint
	minioProv := &StorageProvisioner{
		Provider: "minio",
		Bucket:   "shared-assets",
		Endpoint: "http://minio.default.svc:9000",
	}
	resMinio, err := minioProv.Provision(context.Background(), env, v1alpha1.AsyncRouteSpec{
		Protocol: "minio",
		Target:   "avatars",
	})
	require.NoError(t, err)
	assert.Equal(t, "previews/default/pr-42/avatars/", resMinio.ResolvedTarget)
	assert.Equal(t, "shared-assets", resMinio.EnvVars["DIVERGE_STORAGE_BUCKET"])
	assert.Equal(t, "http://minio.default.svc:9000", resMinio.EnvVars["DIVERGE_STORAGE_ENDPOINT"])
}

func TestStorageProvisioner_Provision_NamespaceIsolation(t *testing.T) {
	p := &StorageProvisioner{Provider: "s3"}
	route := v1alpha1.AsyncRouteSpec{Protocol: "s3", Target: "uploads"}

	stagingEnv := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "pr-42", Namespace: "staging"},
	}
	prodEnv := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "pr-42", Namespace: "prod"},
	}

	resStaging, err := p.Provision(context.Background(), stagingEnv, route)
	require.NoError(t, err)
	resProd, err := p.Provision(context.Background(), prodEnv, route)
	require.NoError(t, err)

	assert.Equal(t, "previews/staging/pr-42/uploads/", resStaging.ResolvedTarget)
	assert.Equal(t, "previews/prod/pr-42/uploads/", resProd.ResolvedTarget)
	assert.NotEqual(t, resStaging.ResolvedTarget, resProd.ResolvedTarget)
}

func TestStorageProvisioner_Provision_MultiRouteNoEnvVarCollision(t *testing.T) {
	p := &StorageProvisioner{
		Provider: "s3",
		Endpoint: "http://minio.local:9000",
	}
	routeUploads := v1alpha1.AsyncRouteSpec{Protocol: "s3", Target: "user-uploads"}
	routeAvatars := v1alpha1.AsyncRouteSpec{Protocol: "gcs", Target: "avatars"}

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "pr-42", Namespace: "staging"},
		Spec: v1alpha1.EnvironmentSpec{
			Routing: v1alpha1.EnvironmentRouting{
				AsyncRoutes: []v1alpha1.AsyncRouteSpec{routeUploads, routeAvatars},
			},
		},
	}

	resUploads, err := p.Provision(context.Background(), env, routeUploads)
	require.NoError(t, err)
	resAvatars, err := p.Provision(context.Background(), env, routeAvatars)
	require.NoError(t, err)

	assert.Equal(t, "previews/staging/pr-42/user-uploads/", resUploads.EnvVars["DIVERGE_STORAGE_PREFIX_USER_UPLOADS"])
	assert.Equal(t, "previews/staging/pr-42/user-uploads/", resUploads.EnvVars["S3_KEY_PREFIX_USER_UPLOADS"])
	assert.Equal(t, "user-uploads", resUploads.EnvVars["DIVERGE_STORAGE_BUCKET_USER_UPLOADS"])
	assert.Equal(t, "http://minio.local:9000", resUploads.EnvVars["DIVERGE_STORAGE_ENDPOINT_USER_UPLOADS"])
	_, hasUnsuffixedUploads := resUploads.EnvVars["DIVERGE_STORAGE_PREFIX"]
	assert.False(t, hasUnsuffixedUploads)

	assert.Equal(t, "previews/staging/pr-42/avatars/", resAvatars.EnvVars["DIVERGE_STORAGE_PREFIX_AVATARS"])
	assert.Equal(t, "previews/staging/pr-42/avatars/", resAvatars.EnvVars["GCS_KEY_PREFIX_AVATARS"])
	assert.Equal(t, "avatars", resAvatars.EnvVars["DIVERGE_STORAGE_BUCKET_AVATARS"])
	_, hasUnsuffixedAvatars := resAvatars.EnvVars["DIVERGE_STORAGE_PREFIX"]
	assert.False(t, hasUnsuffixedAvatars)

	for k, v := range resUploads.EnvVars {
		if otherV, exists := resAvatars.EnvVars[k]; exists {
			assert.Equal(t, v, otherV, "env var %s should not collide with a different value", k)
		}
	}

	// Duplicate target names without explicit EnvVarMapping return an error
	dupEnv := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "pr-42", Namespace: "staging"},
		Spec: v1alpha1.EnvironmentSpec{
			Routing: v1alpha1.EnvironmentRouting{
				AsyncRoutes: []v1alpha1.AsyncRouteSpec{
					{Protocol: "s3", Target: "uploads"},
					{Protocol: "minio", Target: "uploads"},
				},
			},
		},
	}
	_, err = p.Provision(context.Background(), dupEnv, dupEnv.Spec.Routing.AsyncRoutes[0])
	require.Error(t, err)
	assert.Contains(t, err.Error(), "require explicit envVarMapping")
}

func TestStorageProvisioner_Provision_AnnotationsOverride(t *testing.T) {
	p := &StorageProvisioner{
		Provider: "s3",
		Bucket:   "default-bucket",
		Endpoint: "http://default-endpoint:9000",
	}
	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pr-77",
			Namespace: "default",
			Annotations: map[string]string{
				AnnotationStorageBucket:   "annotated-bucket",
				AnnotationStorageEndpoint: "https://s3.annotated.internal",
			},
		},
	}

	res, err := p.Provision(context.Background(), env, v1alpha1.AsyncRouteSpec{
		Protocol: "s3",
		Target:   "uploads",
	})
	require.NoError(t, err)
	assert.Equal(t, "previews/default/pr-77/uploads/", res.ResolvedTarget)
	assert.Equal(t, "annotated-bucket", res.EnvVars["DIVERGE_STORAGE_BUCKET"])
	assert.Equal(t, "https://s3.annotated.internal", res.EnvVars["DIVERGE_STORAGE_ENDPOINT"])
}

func TestStorageProvisioner_Provision_CustomEnvVarMapping(t *testing.T) {
	p := &StorageProvisioner{Provider: "s3"}
	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "pr-99", Namespace: "default"},
	}
	route := v1alpha1.AsyncRouteSpec{
		Protocol: "s3",
		Target:   "invoices",
		EnvVarMapping: map[string]string{
			"CUSTOM_S3_PREFIX":  "{{ .ResolvedTarget }}",
			"EMPTY_TMPL_PREFIX": "",
			"URI_PREFIX":        "s3://my-bucket/{{ .ResolvedTarget }}",
		},
	}

	res, err := p.Provision(context.Background(), env, route)
	require.NoError(t, err)
	assert.Equal(t, "previews/default/pr-99/invoices/", res.ResolvedTarget)
	assert.Equal(t, "previews/default/pr-99/invoices/", res.EnvVars["CUSTOM_S3_PREFIX"])
	assert.Equal(t, "previews/default/pr-99/invoices/", res.EnvVars["EMPTY_TMPL_PREFIX"])
	assert.Equal(t, "s3://my-bucket/previews/default/pr-99/invoices/", res.EnvVars["URI_PREFIX"])
	_, hasDefault := res.EnvVars["DIVERGE_STORAGE_PREFIX"]
	assert.False(t, hasDefault)
}

func TestStorageProvisioner_ValidationAndTeardown(t *testing.T) {
	p := &StorageProvisioner{Provider: "s3"}
	route := v1alpha1.AsyncRouteSpec{Protocol: "s3", Target: "uploads"}

	_, err := p.Provision(context.Background(), nil, route)
	require.Error(t, err)

	_, err = p.Provision(context.Background(), &v1alpha1.Environment{}, route)
	require.Error(t, err)

	require.Error(t, p.Teardown(context.Background(), nil, route))
	require.Error(t, p.Teardown(context.Background(), &v1alpha1.Environment{}, route))

	validEnv := &v1alpha1.Environment{ObjectMeta: metav1.ObjectMeta{Name: "pr-42"}}
	require.NoError(t, p.Teardown(context.Background(), validEnv, route))
}
