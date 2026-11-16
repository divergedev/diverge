package finops

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/divergedev/diverge/internal/metrics"
)

func TestOpenCostClient_FetchEnvironmentAllocations(t *testing.T) {
	var seenQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/allocation/compute", r.URL.Path)
		seenQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"code": 200,
			"data": [
				{
					"payments/pr-101": {
						"name": "payments/pr-101",
						"minutes": 30,
						"cpuCoreHours": 0.25,
						"cpuCost": 0.010,
						"ramByteHours": 536870912,
						"ramCost": 0.002,
						"totalCost": 0.015,
						"properties": {
							"namespace": "payments",
							"labels": {
								"divergedev_com_environment": "pr-101"
							}
						}
					},
					"payments/__idle__": {
						"name": "payments/__idle__",
						"minutes": 30,
						"totalCost": 0.50
					}
				}
			]
		}`))
	}))
	defer srv.Close()

	client := NewOpenCostClient(srv.URL, srv.Client())
	require.NotNil(t, client)

	allocs, err := client.FetchEnvironmentAllocations(context.Background(), "payments")
	require.NoError(t, err)
	assert.Contains(t, seenQuery, "window=1h")
	assert.Contains(t, seenQuery, "filter=namespace")

	require.Len(t, allocs, 1)
	pr101, ok := allocs["payments/pr-101"]
	require.True(t, ok)
	// 30-minute window scaled to 60 minutes (x2):
	// totalCost: 0.015 * 2 = 0.030/hr
	// cpuCoreHours: 0.25 * 2 = 0.50 cores
	// ramByteHours: 0.5 GiB-hr * 2 = 1.0 GiB
	assert.InDelta(t, 0.030, pr101.HourlyComputeUSD, 1e-6)
	assert.InDelta(t, 0.50, pr101.CPUCores, 1e-6)
	assert.InDelta(t, 1.0, pr101.MemoryGiB, 1e-6)

	// Verify URLs with existing query parameters preserve query params without corrupting path
	var seenPath string
	querySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		seenQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":200,"data":[]}`))
	}))
	defer querySrv.Close()

	clientWithAllocationQuery := NewOpenCostClient(querySrv.URL+"/allocation?window=2h", querySrv.Client())
	_, err = clientWithAllocationQuery.FetchEnvironmentAllocations(context.Background(), "payments")
	require.NoError(t, err)
	assert.Equal(t, "/allocation", seenPath)
	assert.Contains(t, seenQuery, "window=2h")
	assert.NotContains(t, seenQuery, "/allocation/compute")

	clientWithRootQuery := NewOpenCostClient(querySrv.URL+"?window=2h", querySrv.Client())
	_, err = clientWithRootQuery.FetchEnvironmentAllocations(context.Background(), "payments")
	require.NoError(t, err)
	assert.Equal(t, "/allocation/compute", seenPath)
	assert.Contains(t, seenQuery, "window=2h")
}

func TestOpenCostClient_EnvAndProhibitedDestination(t *testing.T) {
	t.Setenv(metrics.EnvOpenCostURL, "")
	assert.Nil(t, OpenCostClientFromEnv())

	t.Setenv(metrics.EnvOpenCostURL, "http://169.254.169.254/latest/meta-data")
	badClient := OpenCostClientFromEnv()
	require.NotNil(t, badClient)
	_, err := badClient.FetchEnvironmentAllocations(context.Background(), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "prohibited")
}

func TestOpenCostClient_ComponentSumFallbackAndMultiStepAverage(t *testing.T) {
	payload := []byte(`{
		"code": 200,
		"data": [
			{
				"pod-a": {
					"minutes": 60,
					"cpuCost": 0.02,
					"ramCost": 0.005,
					"totalCost": 0,
					"properties": {
						"namespace": "staging",
						"labels": {"divergedev.com/environment": "pr-7"}
					}
				},
				"pod-b": {
					"minutes": 60,
					"cpuCost": 0.01,
					"ramCost": 0.005,
					"totalCost": 0,
					"properties": {
						"namespace": "staging",
						"labels": {"divergedev.com/environment": "pr-7"}
					}
				}
			},
			{
				"pod-a": {
					"minutes": 60,
					"totalCost": 0.06,
					"properties": {
						"namespace": "staging",
						"labels": {"divergedev.com/environment": "pr-7"}
					}
				}
			}
		]
	}`)

	allocs, err := parseOpenCostPayload(payload, "")
	require.NoError(t, err)
	// Step 1 sum = 0.025 + 0.015 = 0.040; Step 2 sum = 0.060; average across 2 steps = 0.050/hr
	assert.InDelta(t, 0.050, allocs["staging/pr-7"].HourlyComputeUSD, 1e-6)
}
