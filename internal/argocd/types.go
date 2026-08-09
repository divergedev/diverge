package argocd

import (
	"context"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type Applicator interface {
	ApplyApplication(ctx context.Context, app *unstructured.Unstructured) error
	ApplyApplications(ctx context.Context, apps []*unstructured.Unstructured) error
	DeleteApplication(ctx context.Context, name string) error
	DeleteApplicationsForEnvironment(ctx context.Context, envName string) error
	GetSyncStatus(ctx context.Context, envName string) ([]ApplicationStatus, error)
}

type ServiceConfig struct {
	Name       string
	ChartPath  string
	ValuesFile string
	Image      string
	Tag        string
}

type ApplicationStatus struct {
	Name       string
	Service    string // the Diverge service this Application belongs to
	SyncStatus string // Synced, OutOfSync, Unknown
	Health     string // Healthy, Progressing, Degraded, Missing
}
