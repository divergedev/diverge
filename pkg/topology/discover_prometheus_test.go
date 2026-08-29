package topology

import (
	"context"
	"fmt"
	"testing"
	"time"

	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockPromAPI struct {
	promv1.API
	queryFunc func(ctx context.Context, query string, ts time.Time, opts ...promv1.Option) (model.Value, promv1.Warnings, error)
}

func (m *mockPromAPI) Query(ctx context.Context, query string, ts time.Time, opts ...promv1.Option) (model.Value, promv1.Warnings, error) {
	if m.queryFunc != nil {
		return m.queryFunc(ctx, query, ts, opts...)
	}
	return nil, nil, nil
}

func TestPrometheusDiscoverer_DetectMesh(t *testing.T) {
	tests := []struct {
		name         string
		queryResults map[string]model.Value
		expected     MeshType
	}{
		{
			name: "detect istio",
			queryResults: map[string]model.Value{
				"count(istio_requests_total) > 0": model.Vector{&model.Sample{Value: 1}},
			},
			expected: MeshIstio,
		},
		{
			name: "detect linkerd",
			queryResults: map[string]model.Value{
				"count(request_total{direction=\"outbound\"} or response_total{direction=\"outbound\"}) > 0": model.Vector{&model.Sample{Value: 1}},
			},
			expected: MeshLinkerd,
		},
		{
			name: "detect cilium",
			queryResults: map[string]model.Value{
				"count(hubble_flows_processed_total or hubble_http_requests_total) > 0": model.Vector{&model.Sample{Value: 1}},
			},
			expected: MeshCilium,
		},
		{
			name:         "detect vanilla",
			queryResults: map[string]model.Value{},
			expected:     MeshVanilla,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &mockPromAPI{
				queryFunc: func(ctx context.Context, query string, ts time.Time, opts ...promv1.Option) (model.Value, promv1.Warnings, error) {
					if res, ok := tt.queryResults[query]; ok {
						return res, nil, nil
					}
					return model.Vector{}, nil, nil
				},
			}
			d := &PrometheusDiscoverer{api: api, lookbackWindow: "1m"}
			mesh, err := d.DetectMesh(context.Background())
			require.NoError(t, err)
			assert.Equal(t, tt.expected, mesh)
		})
	}
}

func TestPrometheusDiscoverer_Discover_Istio(t *testing.T) {
	api := &mockPromAPI{
		queryFunc: func(ctx context.Context, query string, ts time.Time, opts ...promv1.Option) (model.Value, promv1.Warnings, error) {
			assert.Contains(t, query, "istio_requests_total")
			return model.Vector{
				&model.Sample{
					Metric: model.Metric{
						"source_workload":          "frontend",
						"destination_service_name": "backend",
						"request_protocol":         "http",
					},
					Value: 1,
				},
			}, nil, nil
		},
	}

	d := &PrometheusDiscoverer{api: api, meshType: MeshIstio, lookbackWindow: "1m"}
	graph, err := d.Discover(context.Background(), nil)
	require.NoError(t, err)
	edges := graph.Edges()
	require.Len(t, edges, 1)
	assert.Equal(t, "frontend", edges[0].From)
	assert.Equal(t, "backend", edges[0].To)
	assert.Equal(t, "http", edges[0].Protocol)
	assert.Equal(t, "prometheus", edges[0].Source)
}

func TestPrometheusDiscoverer_Discover_Namespaces(t *testing.T) {
	api := &mockPromAPI{
		queryFunc: func(ctx context.Context, query string, ts time.Time, opts ...promv1.Option) (model.Value, promv1.Warnings, error) {
			assert.Contains(t, query, "source_workload_namespace=~\"ns1|ns2\"")
			return model.Vector{}, nil, nil
		},
	}

	d := &PrometheusDiscoverer{api: api, meshType: MeshIstio, lookbackWindow: "1m"}
	_, err := d.Discover(context.Background(), []string{"ns1", "ns2"})
	require.NoError(t, err)
}

func TestPrometheusDiscoverer_Discover_Linkerd(t *testing.T) {
	api := &mockPromAPI{
		queryFunc: func(ctx context.Context, query string, ts time.Time, opts ...promv1.Option) (model.Value, promv1.Warnings, error) {
			assert.Contains(t, query, "response_total")
			return model.Vector{
				&model.Sample{
					Metric: model.Metric{
						"deployment":  "frontend",
						"dst_service": "backend",
					},
					Value: 1,
				},
			}, nil, nil
		},
	}

	d := &PrometheusDiscoverer{api: api, meshType: MeshLinkerd, lookbackWindow: "1m"}
	graph, err := d.Discover(context.Background(), nil)
	require.NoError(t, err)
	edges := graph.Edges()
	require.Len(t, edges, 1)
	assert.Equal(t, "frontend", edges[0].From)
	assert.Equal(t, "backend", edges[0].To)
}

func TestPrometheusDiscoverer_Discover_Cilium(t *testing.T) {
	api := &mockPromAPI{
		queryFunc: func(ctx context.Context, query string, ts time.Time, opts ...promv1.Option) (model.Value, promv1.Warnings, error) {
			assert.Contains(t, query, "hubble_flows_processed_total")
			return model.Vector{
				&model.Sample{
					Metric: model.Metric{
						"source":      "frontend",
						"destination": "backend",
						"protocol":    "tcp",
					},
					Value: 1,
				},
			}, nil, nil
		},
	}

	d := &PrometheusDiscoverer{api: api, meshType: MeshCilium, lookbackWindow: "1m"}
	graph, err := d.Discover(context.Background(), nil)
	require.NoError(t, err)
	edges := graph.Edges()
	require.Len(t, edges, 1)
	assert.Equal(t, "frontend", edges[0].From)
	assert.Equal(t, "backend", edges[0].To)
}

func TestPrometheusDiscoverer_Discover_Error(t *testing.T) {
	api := &mockPromAPI{
		queryFunc: func(ctx context.Context, query string, ts time.Time, opts ...promv1.Option) (model.Value, promv1.Warnings, error) {
			return nil, nil, fmt.Errorf("prom error")
		},
	}

	d := &PrometheusDiscoverer{api: api, meshType: MeshIstio, lookbackWindow: "1m"}
	_, err := d.Discover(context.Background(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "prom error")
}
