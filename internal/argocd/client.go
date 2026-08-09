package argocd

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var applicationGVK = schema.GroupVersionKind{
	Group:   "argoproj.io",
	Version: "v1alpha1",
	Kind:    "Application",
}

// Client manages Argo CD Application resources in the Argo namespace.
type Client struct {
	k8sClient client.Client
	namespace string
}

func NewClient(k8sClient client.Client, namespace string) *Client {
	return &Client{
		k8sClient: k8sClient,
		namespace: namespace,
	}
}

// ApplyApplication creates or updates an Argo CD Application using server-side apply.
func (c *Client) ApplyApplication(ctx context.Context, app *unstructured.Unstructured) error {
	app.SetNamespace(c.namespace)
	return c.k8sClient.Patch(ctx, app, client.Apply, client.FieldOwner("diverge-controller"))
}

// ApplyApplications applies a batch of Application resources.
func (c *Client) ApplyApplications(ctx context.Context, apps []*unstructured.Unstructured) error {
	for _, app := range apps {
		if err := c.ApplyApplication(ctx, app); err != nil {
			return fmt.Errorf("failed to apply application %s: %w", app.GetName(), err)
		}
	}
	return nil
}

// DeleteApplication removes a single Argo CD Application by name.
func (c *Client) DeleteApplication(ctx context.Context, name string) error {
	app := &unstructured.Unstructured{}
	app.SetGroupVersionKind(applicationGVK)
	app.SetName(name)
	app.SetNamespace(c.namespace)

	return c.k8sClient.Delete(ctx, app)
}

// DeleteApplicationsForEnvironment deletes all Applications managed by Diverge
// for the given environment name.
func (c *Client) DeleteApplicationsForEnvironment(ctx context.Context, envName string) error {
	apps, err := c.listApplicationsForEnvironment(ctx, envName)
	if err != nil {
		return err
	}

	for i := range apps {
		if err := c.k8sClient.Delete(ctx, &apps[i]); err != nil {
			return fmt.Errorf("failed to delete application %s: %w", apps[i].GetName(), err)
		}
	}
	return nil
}

// GetSyncStatus returns sync and health status for all Applications
// belonging to the given environment.
func (c *Client) GetSyncStatus(ctx context.Context, envName string) ([]ApplicationStatus, error) {
	apps, err := c.listApplicationsForEnvironment(ctx, envName)
	if err != nil {
		return nil, err
	}

	statuses := make([]ApplicationStatus, 0, len(apps))
	for _, app := range apps {
		syncStatus := "Unknown"
		health := "Unknown"

		status, found, _ := unstructured.NestedMap(app.Object, "status")
		if found {
			if sync, ok, _ := unstructured.NestedString(status, "sync", "status"); ok {
				syncStatus = sync
			}
			if h, ok, _ := unstructured.NestedString(status, "health", "status"); ok {
				health = h
			}
		}

		statuses = append(statuses, ApplicationStatus{
			Name:       app.GetName(),
			Service:    app.GetLabels()["diverge.io/service"],
			SyncStatus: syncStatus,
			Health:     health,
		})
	}

	return statuses, nil
}

func (c *Client) listApplicationsForEnvironment(ctx context.Context, envName string) ([]unstructured.Unstructured, error) {
	appList := &unstructured.UnstructuredList{}
	appList.SetGroupVersionKind(applicationGVK)

	err := c.k8sClient.List(ctx, appList,
		client.InNamespace(c.namespace),
		client.MatchingLabels{
			"diverge.io/environment": envName,
			"diverge.io/managed-by":  "diverge",
		},
	)
	if err != nil {
		return nil, err
	}

	return appList.Items, nil
}
