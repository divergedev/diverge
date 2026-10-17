package async

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/divergedev/diverge/pkg/registry"
)

func TestStorageRegister_ProvidersAndConfigPrecedence(t *testing.T) {
	origEndpoint := storageEndpoint
	origBucket := storageBucket
	t.Cleanup(func() {
		storageEndpoint = origEndpoint
		storageBucket = origBucket
	})

	t.Run("falls back to environment variables when flags are unset", func(t *testing.T) {
		storageEndpoint = ""
		storageBucket = ""
		t.Setenv("DIVERGE_STORAGE_ENDPOINT", "  https://s3.env.internal:9000  ")
		t.Setenv("DIVERGE_STORAGE_BUCKET", "  env-bucket  ")

		for _, name := range []string{"s3", "gcs", "minio"} {
			prov, err := Providers.Create(name, registry.Deps{})
			require.NoError(t, err)
			require.NotNil(t, prov)
			assert.Equal(t, name, prov.Name())

			sp, ok := prov.(*StorageProvisioner)
			require.True(t, ok)
			assert.Equal(t, name, sp.Provider)
			assert.Equal(t, "https://s3.env.internal:9000", sp.Endpoint)
			assert.Equal(t, "env-bucket", sp.Bucket)
		}
	})

	t.Run("flags take precedence over environment variables", func(t *testing.T) {
		storageEndpoint = "  http://minio.flag.svc:9000  "
		storageBucket = "  flag-bucket  "
		t.Setenv("DIVERGE_STORAGE_ENDPOINT", "https://s3.env.internal:9000")
		t.Setenv("DIVERGE_STORAGE_BUCKET", "env-bucket")

		for _, name := range []string{"s3", "gcs", "minio"} {
			prov, err := Providers.Create(name, registry.Deps{})
			require.NoError(t, err)
			sp, ok := prov.(*StorageProvisioner)
			require.True(t, ok)
			assert.Equal(t, "http://minio.flag.svc:9000", sp.Endpoint)
			assert.Equal(t, "flag-bucket", sp.Bucket)
		}
	})
}
