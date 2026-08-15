package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

func TestListChildEnvironments_MatchesLabels(t *testing.T) {
	pg := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-group",
		},
	}

	envMatch := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "env-match",
			Namespace: "default",
			Labels: map[string]string{
				"diverge.io/previewgroup": "test-group",
				"diverge.io/managed-by":   "diverge-previewgroup",
			},
		},
	}

	r, _ := newTestPreviewGroupReconciler(pg, envMatch)

	children, err := r.listChildEnvironments(context.Background(), pg)
	require.NoError(t, err)

	assert.Len(t, children, 1)
	assert.Equal(t, "env-match", children[0].Name)
}

func TestListChildEnvironments_IgnoresUnlabeled(t *testing.T) {
	pg := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-group",
		},
	}

	envNoManagedBy := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "env-no-managed",
			Namespace: "default",
			Labels: map[string]string{
				"diverge.io/previewgroup": "test-group",
			},
		},
	}

	r, _ := newTestPreviewGroupReconciler(pg, envNoManagedBy)

	children, err := r.listChildEnvironments(context.Background(), pg)
	require.NoError(t, err)

	assert.Len(t, children, 0)
}

func TestDeleteOrphanedEnvironments_CleansUp(t *testing.T) {
	pg := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-group",
		},
	}

	env1 := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "env-1",
			Namespace: "default",
			Labels: map[string]string{
				"diverge.io/previewgroup": "test-group",
				"diverge.io/managed-by":   "diverge-previewgroup",
			},
		},
	}
	env2 := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "env-2",
			Namespace: "default",
			Labels: map[string]string{
				"diverge.io/previewgroup": "test-group",
				"diverge.io/managed-by":   "diverge-previewgroup",
			},
		},
	}
	env3 := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "env-3",
			Namespace: "default",
			Labels: map[string]string{
				"diverge.io/previewgroup": "test-group",
				"diverge.io/managed-by":   "diverge-previewgroup",
			},
		},
	}

	r, c := newTestPreviewGroupReconciler(pg, env1, env2, env3)

	desired := map[string]bool{
		"env-1": true,
		"env-2": true,
	}

	err := r.deleteOrphanedEnvironments(context.Background(), pg, desired)
	require.NoError(t, err)

	children, err := r.listChildEnvironments(context.Background(), pg)
	require.NoError(t, err)

	assert.Len(t, children, 2)

	var remaining divergeiov1alpha1.EnvironmentList
	err = c.List(context.Background(), &remaining, client.InNamespace("default"))
	require.NoError(t, err)
	assert.Len(t, remaining.Items, 2)
	names := []string{remaining.Items[0].Name, remaining.Items[1].Name}
	assert.Contains(t, names, "env-1")
	assert.Contains(t, names, "env-2")
	assert.NotContains(t, names, "env-3")
}
