package cli

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

func TestStatusCmd(t *testing.T) {
	now := metav1.Now()
	expired := metav1.NewTime(now.Add(-1 * time.Hour))
	future := metav1.NewTime(now.Add(23 * time.Hour))

	env1 := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "env1",
			Namespace:         "default",
			CreationTimestamp: now,
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Source: divergeiov1alpha1.EnvironmentSource{
				Branch:   "feat/one",
				Provider: "github",
			},
			Routing: divergeiov1alpha1.EnvironmentRouting{
				Mode:        "header",
				HeaderValue: "feat-one",
			},
		},
		Status: divergeiov1alpha1.EnvironmentStatus{
			Phase:     "Ready",
			URL:       "https://env1.example.com",
			Services:  []string{"api", "web"},
			ExpiresAt: &future,
		},
	}

	env2 := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "env2",
			Namespace:         "staging",
			CreationTimestamp: now,
		},
		Status: divergeiov1alpha1.EnvironmentStatus{
			Phase:     "Failed",
			ExpiresAt: &expired,
		},
	}

	pg1 := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "pg1",
			Namespace:         "default",
			CreationTimestamp: now,
		},
		Spec: divergeiov1alpha1.PreviewGroupSpec{
			Source: divergeiov1alpha1.EnvironmentSource{
				Branch:   "feat/pg",
				Provider: "github",
			},
			Routing: divergeiov1alpha1.PreviewGroupRouting{
				Mode:        "subdomain",
				HeaderValue: "feat-pg",
			},
			Services: []divergeiov1alpha1.PreviewGroupServiceSpec{
				{Name: "auth"},
				{Name: "billing"},
			},
		},
		Status: divergeiov1alpha1.PreviewGroupStatus{
			Phase: "Deploying",
		},
	}

	env3 := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "env3",
			Namespace:         "staging",
			CreationTimestamp: now,
		},
		Status: divergeiov1alpha1.EnvironmentStatus{
			Phase:     "Pending",
			ExpiresAt: nil, // Nil TTL
			Services:  []string{"db"},
		},
	}

	pg2 := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "pg2",
			Namespace:         "other", // pg is cluster-scoped
			CreationTimestamp: now,
		},
		Spec: divergeiov1alpha1.PreviewGroupSpec{
			Services: []divergeiov1alpha1.PreviewGroupServiceSpec{
				{Name: "cache"},
			},
		},
		Status: divergeiov1alpha1.PreviewGroupStatus{
			Phase: "Ready",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(newTestScheme()).
		WithObjects(env1, env2, env3, pg1, pg2).
		Build()

	app := &App{Namespace: "default", Client: fakeClient, NoColor: true}

	t.Run("table output", func(t *testing.T) {
		cmd := newStatusCmd(app)
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetArgs([]string{})
		require.NoError(t, cmd.Execute())

		out := buf.String()
		assert.Contains(t, out, "PREVIEW ENVIRONMENTS")
		assert.Contains(t, out, "env1")
		assert.NotContains(t, out, "env2")
		assert.NotContains(t, out, "env3")
		assert.Contains(t, out, "Ready")
		assert.Contains(t, out, "PREVIEW GROUPS")
		assert.Contains(t, out, "pg1")
		assert.Contains(t, out, "pg2")
		assert.Contains(t, out, "Deploying")
		assert.Contains(t, out, "SUMMARY: 1 environments, 2 preview groups, 5 services total")
	})

	t.Run("all namespaces", func(t *testing.T) {
		cmd := newStatusCmd(app)
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetArgs([]string{"-A"})
		require.NoError(t, cmd.Execute())

		out := buf.String()
		assert.Contains(t, out, "env1")
		assert.Contains(t, out, "env2")
		assert.Contains(t, out, "env3")
		assert.Contains(t, out, "pg1")
		assert.Contains(t, out, "pg2")
		assert.Contains(t, out, "expired")
		assert.Contains(t, out, "SUMMARY: 3 environments, 2 preview groups, 6 services total")
	})

	t.Run("wide output", func(t *testing.T) {
		cmd := newStatusCmd(app)
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetArgs([]string{"--wide"})
		require.NoError(t, cmd.Execute())

		out := buf.String()
		assert.Contains(t, out, "ROUTING MODE")
		assert.Contains(t, out, "HEADER VALUE")
		assert.Contains(t, out, "SERVICES")
		assert.Contains(t, out, "api,web")
		assert.Contains(t, out, "auth,billing")
	})

	t.Run("json output", func(t *testing.T) {
		cmd := newStatusCmd(app)
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetArgs([]string{"-o", "json"})
		require.NoError(t, cmd.Execute())

		var result map[string]interface{}
		require.NoError(t, json.Unmarshal(buf.Bytes(), &result))

		envs := result["environments"].([]interface{})
		pgs := result["previewGroups"].([]interface{})
		assert.Len(t, envs, 1)
		assert.Len(t, pgs, 2)
	})

	t.Run("yaml output", func(t *testing.T) {
		cmd := newStatusCmd(app)
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetArgs([]string{"-o", "yaml"})
		require.NoError(t, cmd.Execute())

		out := buf.String()
		assert.Contains(t, out, "environments:")
		assert.Contains(t, out, "previewGroups:")
		assert.Contains(t, out, "env1")
		assert.Contains(t, out, "pg1")
	})

	t.Run("no environments", func(t *testing.T) {
		emptyClient := fake.NewClientBuilder().WithScheme(newTestScheme()).Build()
		emptyApp := &App{Namespace: "default", Client: emptyClient, NoColor: true}
		cmd := newStatusCmd(emptyApp)
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetArgs([]string{})
		require.NoError(t, cmd.Execute())

		out := buf.String()
		assert.Contains(t, out, "No active environments found.")
		assert.Contains(t, out, "No active preview groups found.")
		assert.Contains(t, out, "SUMMARY: 0 environments, 0 preview groups, 0 services total")
	})
}
