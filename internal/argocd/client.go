package argocd

import (
	"context"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

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

func (c *Client) Apply(ctx context.Context, appSet *unstructured.Unstructured) error {
	appSet.SetNamespace(c.namespace)
	return c.k8sClient.Patch(ctx, appSet, client.Apply, client.FieldOwner("diverge-controller"))
}

func (c *Client) Delete(ctx context.Context, name string) error {
	appSet := &unstructured.Unstructured{}
	appSet.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "argoproj.io",
		Version: "v1alpha1",
		Kind:    "ApplicationSet",
	})
	appSet.SetName(name)
	appSet.SetNamespace(c.namespace)

	return c.k8sClient.Delete(ctx, appSet)
}

func (c *Client) GetSyncStatus(ctx context.Context, name string) ([]ApplicationStatus, error) {
	appList := &unstructured.UnstructuredList{}
	appList.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "argoproj.io",
		Version: "v1alpha1",
		Kind:    "Application",
	})

	err := c.k8sClient.List(ctx, appList, client.InNamespace(c.namespace), client.MatchingLabels{"diverge.io/environment": name})
	if err != nil {
		return nil, err
	}

	var statuses []ApplicationStatus
	for _, app := range appList.Items {
		appName := app.GetName()
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
			Name:       appName,
			SyncStatus: syncStatus,
			Health:     health,
		})
	}

	return statuses, nil
}
