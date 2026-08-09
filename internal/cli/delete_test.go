package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

func newTestScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	utilruntime.Must(divergeiov1alpha1.AddToScheme(scheme))
	return scheme
}

func TestDeleteConfirmYes(t *testing.T) {
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "preview-mr-42",
			Namespace: "default",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(newTestScheme()).
		WithObjects(env).
		Build()

	app := &App{Namespace: "default", Client: fakeClient}
	cmd := newDeleteCmd(app)

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetIn(strings.NewReader("y\n"))
	cmd.SetArgs([]string{"preview-mr-42"})

	err := cmd.Execute()
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "deleted")

	// Verify the object is actually gone from the fake cluster
	got := &divergeiov1alpha1.Environment{}
	err = fakeClient.Get(context.Background(), client.ObjectKey{Name: "preview-mr-42", Namespace: "default"}, got)
	require.Error(t, err, "environment should be deleted from cluster")
}

func TestDeleteConfirmNo(t *testing.T) {
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "preview-mr-42",
			Namespace: "default",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(newTestScheme()).
		WithObjects(env).
		Build()

	app := &App{Namespace: "default", Client: fakeClient}
	cmd := newDeleteCmd(app)

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetIn(strings.NewReader("n\n"))
	cmd.SetArgs([]string{"preview-mr-42"})

	err := cmd.Execute()
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "Cancelled")

	// Verify the object still exists in the fake cluster
	got := &divergeiov1alpha1.Environment{}
	err = fakeClient.Get(context.Background(), client.ObjectKey{Name: "preview-mr-42", Namespace: "default"}, got)
	require.NoError(t, err, "environment should still exist after cancel")
}

func TestDeleteForceSkipsConfirmation(t *testing.T) {
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "preview-mr-42",
			Namespace: "default",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(newTestScheme()).
		WithObjects(env).
		Build()

	app := &App{Namespace: "default", Client: fakeClient}
	cmd := newDeleteCmd(app)

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	// No SetIn — would hang if confirmation was requested
	cmd.SetArgs([]string{"--force", "preview-mr-42"})

	err := cmd.Execute()
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "deleted")
}

func TestDeleteNotFound(t *testing.T) {
	fakeClient := fake.NewClientBuilder().
		WithScheme(newTestScheme()).
		Build()

	app := &App{Namespace: "default", Client: fakeClient}
	cmd := newDeleteCmd(app)

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"--force", "nonexistent"})

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to delete")
}
