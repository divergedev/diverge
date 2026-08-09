package argocd

import (
	"context"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Applicator manages Argo CD Application custom resources for preview
// environment deployments.
type Applicator interface {
	ApplyApplication(ctx context.Context, app *unstructured.Unstructured) error
	ApplyApplications(ctx context.Context, apps []*unstructured.Unstructured) error
	DeleteApplication(ctx context.Context, name string) error
	DeleteApplicationsForEnvironment(ctx context.Context, envName, envNamespace string) error
	GetSyncStatus(ctx context.Context, envName, envNamespace string) ([]ApplicationStatus, error)
}

// ServiceConfig describes a single service within a preview environment,
// including its Helm chart path, values file, and container image.
type ServiceConfig struct {
	Name       string
	ChartPath  string
	ValuesFile string
	Image      string
	Tag        string
}

// ApplicationStatus reports the sync and health state of an Argo CD
// Application belonging to a Diverge environment.
type ApplicationStatus struct {
	Name       string
	Service    string // the Diverge service this Application belongs to
	SyncStatus string // Synced, OutOfSync, Unknown
	Health     string // Healthy, Progressing, Degraded, Missing
}
