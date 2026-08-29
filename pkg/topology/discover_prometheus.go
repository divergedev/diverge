package topology

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/prometheus/client_golang/api"
	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
)

type MeshType string

const (
	MeshIstio   MeshType = "istio"
	MeshLinkerd MeshType = "linkerd"
	MeshCilium  MeshType = "cilium"
	MeshVanilla MeshType = "vanilla"
)

type PrometheusDiscovererConfig struct {
	Address        string
	TokenEnv       string
	TokenFile      string
	CABundle       string
	InsecureTLS    bool
	LookbackWindow string
	MeshType       MeshType
}

type PrometheusDiscoverer struct {
	api            promv1.API
	meshType       MeshType
	lookbackWindow string
}

func NewPrometheusDiscoverer(cfg PrometheusDiscovererConfig) (*PrometheusDiscoverer, error) {
	var token string
	if cfg.TokenEnv != "" {
		token = os.Getenv(cfg.TokenEnv)
	} else if cfg.TokenFile != "" {
		b, err := os.ReadFile(cfg.TokenFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read token file: %w", err)
		}
		token = strings.TrimSpace(string(b))
	}

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: cfg.InsecureTLS,
		},
	}
	if cfg.CABundle != "" {
		caCertPool := x509.NewCertPool()
		caCertPool.AppendCertsFromPEM([]byte(cfg.CABundle))
		tr.TLSClientConfig.RootCAs = caCertPool
	}

	client := &http.Client{
		Timeout:   3 * time.Second,
		Transport: tr,
	}

	apiCfg := api.Config{
		Address: cfg.Address,
		Client:  client,
	}

	if token != "" {
		apiCfg.RoundTripper = roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			req.Header.Set("Authorization", "Bearer "+token)
			return tr.RoundTrip(req)
		})
	}

	c, err := api.NewClient(apiCfg)
	if err != nil {
		return nil, err
	}

	promAPI := promv1.NewAPI(c)
	lookback := cfg.LookbackWindow
	if lookback == "" {
		lookback = "1m"
	}

	return &PrometheusDiscoverer{
		api:            promAPI,
		meshType:       cfg.MeshType,
		lookbackWindow: lookback,
	}, nil
}

type roundTripperFunc func(req *http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func (d *PrometheusDiscoverer) Name() string {
	return "prometheus"
}

func (d *PrometheusDiscoverer) DetectMesh(ctx context.Context) (MeshType, error) {
	queries := map[MeshType]string{
		MeshIstio:   "count(istio_requests_total) > 0",
		MeshLinkerd: "count(request_total{direction=\"outbound\"} or response_total{direction=\"outbound\"}) > 0",
		MeshCilium:  "count(hubble_flows_processed_total or hubble_http_requests_total) > 0",
	}

	for mesh, query := range queries {
		res, _, err := d.api.Query(ctx, query, time.Now())
		if err != nil {
			return MeshVanilla, err
		}
		vec, ok := res.(model.Vector)
		if ok && len(vec) > 0 {
			return mesh, nil
		}
	}
	return MeshVanilla, nil
}

func (d *PrometheusDiscoverer) Discover(ctx context.Context, namespaces []string) (*ServiceGraph, error) {
	var query string
	mesh := d.meshType
	if mesh == "" {
		var err error
		mesh, err = d.DetectMesh(ctx)
		if err != nil {
			return nil, err
		}
	}

	switch mesh {
	case MeshIstio:
		nsFilter := ""
		if len(namespaces) > 0 {
			nsFilter = fmt.Sprintf(",source_workload_namespace=~\"%s\"", strings.Join(namespaces, "|"))
		}
		query = fmt.Sprintf("sum by (source_workload, source_workload_namespace, destination_service_name, destination_workload_namespace, request_protocol) (rate(istio_requests_total{reporter=\"source\"%s}[%s]))", nsFilter, d.lookbackWindow)
	case MeshLinkerd:
		nsFilter := ""
		if len(namespaces) > 0 {
			nsFilter = fmt.Sprintf("{namespace=~\"%s\"}", strings.Join(namespaces, "|"))
		}
		query = fmt.Sprintf("sum by (namespace, deployment, dst_namespace, dst_service, authority) (rate(response_total{direction=\"outbound\"}%s[%s]))", nsFilter, d.lookbackWindow)
	case MeshCilium:
		nsFilter := ""
		if len(namespaces) > 0 {
			nsFilter = fmt.Sprintf("{source=~\"%s/.*\"}", strings.Join(namespaces, "|")) // Adjust based on label structure
		}
		query = fmt.Sprintf("sum by (source, destination, protocol) (rate(hubble_flows_processed_total{verdict=\"FORWARDED\"}%s[%s]))", nsFilter, d.lookbackWindow)
	default:
		return NewServiceGraph(), nil
	}

	res, _, err := d.api.Query(ctx, query, time.Now())
	if err != nil {
		return nil, err
	}

	vec, ok := res.(model.Vector)
	if !ok {
		return nil, fmt.Errorf("expected vector, got %T", res)
	}

	g := NewServiceGraph()

	for _, sample := range vec {
		metrics := sample.Metric
		var from, to, protocol string
		switch mesh {
		case MeshIstio:
			from = string(metrics["source_workload"])
			to = string(metrics["destination_service_name"])
			protocol = string(metrics["request_protocol"])
		case MeshLinkerd:
			from = string(metrics["deployment"])
			to = string(metrics["dst_service"])
		case MeshCilium:
			from = string(metrics["source"])
			to = string(metrics["destination"])
			protocol = string(metrics["protocol"])
		}

		if from != "" && to != "" {
			g.AddEdge(Edge{
				From:     from,
				To:       to,
				Protocol: protocol,
				Source:   "prometheus",
			})
		}
	}

	return g, nil
}
