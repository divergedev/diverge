package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

func TestDirectSetupRunner_Interface(t *testing.T) {
	runner := &DirectSetupRunner{}
	env := &divergeiov1alpha1.Environment{}
	err := runner.RunSetupSQL(context.Background(), env, "CREATE SCHEMA foo;", "postgres://admin:pass@localhost/db")
	assert.NoError(t, err)
}

func TestJobSetupRunner_Interface(t *testing.T) {
	scheme := setupSQLTestScheme()
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	r := &EnvironmentReconciler{Client: c}

	runner := &JobSetupRunner{Reconciler: r}
	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
			UID:       types.UID("test-uid"),
		},
	}

	err := runner.RunSetupSQL(context.Background(), env, "CREATE SCHEMA foo;", "postgres://admin:pass@localhost/db")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrHookInProgress)
}
