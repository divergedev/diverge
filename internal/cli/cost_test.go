package cli

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	pb "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	"github.com/divergedev/diverge/api/gen/diverge/v1alpha1/divergev1alpha1connect"
	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/metrics"
)

func buildCostCLITestApp(t *testing.T) *App {
	t.Helper()
	t.Setenv(metrics.EnvCostVCPUHourUSD, "0.04")
	t.Setenv(metrics.EnvCostMemoryGiBHourUSD, "0.004")
	t.Setenv(metrics.EnvCostDBSchemaHourUSD, "0.01")
	t.Setenv(metrics.EnvCostDBDedicatedHourUSD, "0.08")

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, divergeiov1alpha1.AddToScheme(scheme))

	now := time.Now().UTC()
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "payments",
			Annotations: map[string]string{
				"divergedev.com/max-hourly-cost-usd": "0.040",
			},
		},
	}
	env1 := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "pr-101",
			Namespace:         "payments",
			CreationTimestamp: metav1.NewTime(now.Add(-3 * time.Hour)),
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Source:   divergeiov1alpha1.EnvironmentSource{MR: 101, Branch: "feat/checkout"},
			Deploy:   divergeiov1alpha1.EnvironmentDeploy{ChangedServices: []string{"payments-api"}},
			Database: divergeiov1alpha1.EnvironmentDatabase{Mode: "schema"},
			Lifecycle: divergeiov1alpha1.EnvironmentLifecycle{
				TTL: &metav1.Duration{Duration: 24 * time.Hour},
			},
		},
		Status: divergeiov1alpha1.EnvironmentStatus{Phase: divergeiov1alpha1.PhaseRunning},
	}
	env2 := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "pr-102",
			Namespace:         "payments",
			CreationTimestamp: metav1.NewTime(now.Add(-1 * time.Hour)),
		},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			Source: divergeiov1alpha1.EnvironmentSource{MR: 102, Branch: "feat/ui"},
			Deploy: divergeiov1alpha1.EnvironmentDeploy{ChangedServices: []string{"web", "gateway"}},
			Lifecycle: divergeiov1alpha1.EnvironmentLifecycle{
				TTL: &metav1.Duration{Duration: 24 * time.Hour},
			},
		},
		Status: divergeiov1alpha1.EnvironmentStatus{Phase: divergeiov1alpha1.PhaseRunning},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(ns, env1, env2).Build()
	return &App{
		Namespace: "payments",
		Client:    c,
	}
}

func TestCostCmd_TableJSONCSVAndBudgetGate(t *testing.T) {
	app := buildCostCLITestApp(t)

	// 1. Default table output
	var tableBuf bytes.Buffer
	root := NewRootCmd(app)
	root.SetOut(&tableBuf)
	root.SetArgs([]string{"cost"})
	require.NoError(t, root.Execute())
	out := tableBuf.String()
	assert.Contains(t, out, "Diverge FinOps Summary")
	assert.Contains(t, out, "pr-101")
	assert.Contains(t, out, "pr-102")
	assert.Contains(t, out, "WARN:")

	// 2. JSON output sorted by accrued with --top 1
	var jsonBuf bytes.Buffer
	rootJSON := NewRootCmd(app)
	rootJSON.SetOut(&jsonBuf)
	rootJSON.SetArgs([]string{"cost", "-o", "json", "--sort", "accrued", "--top", "1"})
	require.NoError(t, rootJSON.Execute())

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(jsonBuf.Bytes(), &parsed))
	consumers, ok := parsed["topConsumers"].([]any)
	require.True(t, ok)
	require.Len(t, consumers, 1)
	first := consumers[0].(map[string]any)
	// pr-101 ran for 3h * $0.022/hr = $0.066 vs pr-102 1h * $0.024/hr = $0.024
	assert.Equal(t, "pr-101", first["name"])

	// 3. CSV output
	var csvBuf bytes.Buffer
	rootCSV := NewRootCmd(app)
	rootCSV.SetOut(&csvBuf)
	rootCSV.SetArgs([]string{"cost", "-o", "csv"})
	require.NoError(t, rootCSV.Execute())
	rows, err := csv.NewReader(strings.NewReader(csvBuf.String())).ReadAll()
	require.NoError(t, err)
	require.Len(t, rows, 3) // header + 2 environments
	assert.Equal(t, "namespace", rows[0][0])

	// 4. --fail-on-budget-warning exits with error when budgetWarning is true
	var gateBuf bytes.Buffer
	rootGate := NewRootCmd(app)
	rootGate.SetOut(&gateBuf)
	rootGate.SetErr(&gateBuf)
	rootGate.SetArgs([]string{"cost", "--fail-on-budget-warning"})
	err = rootGate.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "FinOps budget warning threshold exceeded")
}

type fakeClusterServerHandler struct {
	divergev1alpha1connect.UnimplementedClusterServiceHandler
}

func (f *fakeClusterServerHandler) GetCostSummary(_ context.Context, req *connect.Request[pb.GetCostSummaryRequest]) (*connect.Response[pb.GetCostSummaryResponse], error) {
	return connect.NewResponse(&pb.GetCostSummaryResponse{
		TotalHourlyCostUsd: 0.125,
		MonthlyRunRateUsd:  91.25,
		TotalEnvironments:  1,
		ActiveEnvironments: 1,
		TopConsumers: []*pb.EnvironmentCostItem{
			{
				Name:          "remote-pr-55",
				Namespace:     req.Msg.GetNamespace(),
				Phase:         "Running",
				HourlyCostUsd: 0.125,
				DatabaseMode:  "cnpg",
			},
		},
	}), nil
}

func TestCostCmd_ServerURLModeWithoutKubeconfig(t *testing.T) {
	mux := http.NewServeMux()
	path, handler := divergev1alpha1connect.NewClusterServiceHandler(&fakeClusterServerHandler{})
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	app := &App{Namespace: "remote-ns"}
	var outBuf bytes.Buffer
	root := NewRootCmd(app)
	root.SetOut(&outBuf)
	root.SetArgs([]string{"cost", "top", "--server-url", srv.URL})
	require.NoError(t, root.Execute())

	assert.Contains(t, outBuf.String(), "remote-pr-55")
	assert.Contains(t, outBuf.String(), "remote-ns")
}
