package async

import (
	"flag"
	"os"
	"strings"

	"github.com/divergedev/diverge/pkg/registry"
)

var (
	storageEndpoint string
	storageBucket   string
)

func init() {
	flag.StringVar(&storageEndpoint, "storage-endpoint", "", "S3/GCS/MinIO endpoint URL for object storage key-prefix isolation")
	flag.StringVar(&storageBucket, "storage-bucket", "", "Default S3/GCS/MinIO bucket name for object storage key-prefix isolation")

	register := func(providerName, desc string) {
		Providers.Register(providerName, registry.Provider[Provisioner]{
			Create: func(deps registry.Deps) (Provisioner, error) {
				endpoint := strings.TrimSpace(storageEndpoint)
				if endpoint == "" {
					endpoint = strings.TrimSpace(os.Getenv("DIVERGE_STORAGE_ENDPOINT"))
				}
				bucket := strings.TrimSpace(storageBucket)
				if bucket == "" {
					bucket = strings.TrimSpace(os.Getenv("DIVERGE_STORAGE_BUCKET"))
				}
				return &StorageProvisioner{
					Endpoint: endpoint,
					Bucket:   bucket,
					Provider: providerName,
				}, nil
			},
			Description: desc,
		})
	}

	register("s3", "S3 object key-prefix provisioner")
	register("gcs", "Google Cloud Storage object key-prefix provisioner")
	register("minio", "MinIO object key-prefix provisioner")
}
