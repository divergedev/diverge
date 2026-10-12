package fullstackdemo_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/divergedev/diverge/api/v1alpha1"
	fullstackdemo "github.com/divergedev/diverge/examples/fullstack-demo"
	"github.com/divergedev/diverge/internal/config"
	"github.com/divergedev/diverge/pkg/topology"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xeipuuv/gojsonschema"
	k8syaml "sigs.k8s.io/yaml"
)

func TestFullstackDemoConfigAndSchema(t *testing.T) {
	rawYAML, err := os.ReadFile(".diverge.yaml")
	require.NoError(t, err)

	firstLine := strings.SplitN(string(rawYAML), "\n", 2)[0]
	assert.Equal(t, "# yaml-language-server: $schema=https://divergedev.com/schemas/diverge.schema.json", strings.TrimSpace(firstLine))

	cfg, err := config.Parse(rawYAML)
	require.NoError(t, err)
	require.NoError(t, cfg.Validate())

	assert.Len(t, cfg.Services, 3)
	assert.True(t, cfg.Services["storefront-web"].Entrypoint)
	assert.Equal(t, []string{"checkout-api"}, cfg.Services["storefront-web"].DependsOn)
	assert.Equal(t, []string{"fulfillment-worker"}, cfg.Services["checkout-api"].DependsOn)
	assert.Equal(t, "delta", cfg.Defaults.Deploy.Mode)
	assert.Equal(t, "header", cfg.Defaults.Routing.Mode)
	assert.Equal(t, "x-diverge-env", cfg.Defaults.Routing.HeaderKey)
	assert.Equal(t, "schema", cfg.Defaults.Database.Mode)
	require.NotNil(t, cfg.Defaults.Database.Atlas)
	assert.Equal(t, "versioned", cfg.Defaults.Database.Atlas.Mode)
	assert.Equal(t, "job", cfg.Defaults.Database.Atlas.Engine)
	require.NotNil(t, cfg.Defaults.Features)
	assert.Equal(t, "flipt", cfg.Defaults.Features.Provider)

	schemaBytes, err := config.GenerateJSONSchema()
	require.NoError(t, err)

	jsonBytes, err := k8syaml.YAMLToJSON(rawYAML)
	require.NoError(t, err)

	res, err := gojsonschema.Validate(
		gojsonschema.NewBytesLoader(schemaBytes),
		gojsonschema.NewBytesLoader(jsonBytes),
	)
	require.NoError(t, err)
	assert.True(t, res.Valid(), "expected .diverge.yaml to pass JSON Schema validation, errors: %v", res.Errors())
}

func TestFullstackDemoTopologyGraph(t *testing.T) {
	cfg, err := config.Load(".diverge.yaml")
	require.NoError(t, err)

	staticCfg := topology.StaticConfig{
		Services: make(map[string]topology.StaticServiceConfig, len(cfg.Services)),
	}
	for name, svc := range cfg.Services {
		staticCfg.Services[name] = topology.StaticServiceConfig{
			DependsOn:  svc.DependsOn,
			Entrypoint: svc.Entrypoint,
		}
	}

	discoverer := &topology.StaticDiscoverer{Config: staticCfg}
	g, err := discoverer.Discover(context.Background(), nil)
	require.NoError(t, err)
	require.NoError(t, g.Validate(), "topology graph must be acyclic")

	assert.Equal(t, []string{"storefront-web"}, g.Entrypoints())

	checkoutPaths := g.AllIngressPaths("checkout-api")
	require.Len(t, checkoutPaths, 1)
	assert.Equal(t, []string{"storefront-web", "checkout-api"}, checkoutPaths[0].Hops)

	workerPaths := g.AllIngressPaths("fulfillment-worker")
	require.Len(t, workerPaths, 1)
	assert.Equal(t, []string{"storefront-web", "checkout-api", "fulfillment-worker"}, workerPaths[0].Hops)
}

func TestFullstackDemoK8sManifestsUnmarshal(t *testing.T) {
	t.Run("01-previewgroup-pr42.yaml", func(t *testing.T) {
		data, err := os.ReadFile(filepath.Join("k8s", "01-previewgroup-pr42.yaml"))
		require.NoError(t, err)

		var pg v1alpha1.PreviewGroup
		require.NoError(t, k8syaml.UnmarshalStrict(data, &pg))
		assert.Equal(t, "divergedev.com/v1alpha1", pg.APIVersion)
		assert.Equal(t, "PreviewGroup", pg.Kind)
		assert.Equal(t, "pr-42", pg.Name)
		assert.Equal(t, "default", pg.Namespace)
		assert.Equal(t, "x-diverge-env", pg.Spec.Routing.HeaderKey)
		assert.Equal(t, "pr-42", pg.Spec.Routing.HeaderValue)
		require.Len(t, pg.Spec.Services, 3)

		serviceModes := make(map[string]v1alpha1.ServiceMode, 3)
		for _, svc := range pg.Spec.Services {
			serviceModes[svc.Name] = svc.Mode
		}
		assert.Equal(t, v1alpha1.ServiceModeBaseline, serviceModes["storefront-web"])
		assert.Equal(t, v1alpha1.ServiceModeImage, serviceModes["checkout-api"])
		assert.Equal(t, v1alpha1.ServiceModeBaseline, serviceModes["fulfillment-worker"])
	})

	t.Run("02-environment-scale-to-zero.yaml", func(t *testing.T) {
		data, err := os.ReadFile(filepath.Join("k8s", "02-environment-scale-to-zero.yaml"))
		require.NoError(t, err)

		var env v1alpha1.Environment
		require.NoError(t, k8syaml.UnmarshalStrict(data, &env))
		assert.Equal(t, "divergedev.com/v1alpha1", env.APIVersion)
		assert.Equal(t, "Environment", env.Kind)
		assert.Equal(t, "pr-42-checkout-api", env.Name)
		assert.Equal(t, "default", env.Namespace)
		assert.Equal(t, "15m", env.Annotations["divergedev.com/hibernate-after"])
		assert.Equal(t, "schema", env.Spec.Database.Mode)
		require.NotNil(t, env.Spec.Features)
		assert.Equal(t, "flipt", env.Spec.Features.Provider)
		require.NotEmpty(t, env.Spec.Routing.AsyncRoutes)
	})

	t.Run("03-agenttask-fix-checkout.yaml", func(t *testing.T) {
		data, err := os.ReadFile(filepath.Join("k8s", "03-agenttask-fix-checkout.yaml"))
		require.NoError(t, err)

		var task v1alpha1.AgentTask
		require.NoError(t, k8syaml.UnmarshalStrict(data, &task))
		assert.Equal(t, "divergedev.com/v1alpha1", task.APIVersion)
		assert.Equal(t, "AgentTask", task.Kind)
		assert.Equal(t, "fix-pr-42-rounding", task.Name)
		assert.Equal(t, "default", task.Namespace)
		assert.Equal(t, "true", task.Annotations["divergedev.com/preview-enabled"])
		assert.Equal(t, "checkout-api", task.Annotations["divergedev.com/preview-service"])
		assert.Equal(t, "5.00", task.Spec.BudgetUSD)
		assert.Equal(t, int64(200000), task.Spec.BudgetTokens)
		assert.NotEmpty(t, task.Spec.Repository.URL)
		assert.NotEmpty(t, task.Spec.Objective)
	})
}

func TestFullstackDemoEndToEndHeaderRouting(t *testing.T) {
	// 1. Start baseline fulfillment-worker
	t.Setenv("DIVERGE_ENV", "baseline")
	t.Setenv("ORDERS_QUEUE_TOPIC", "orders-events")
	t.Setenv("CONSUMER_GROUP_ID", "orders-cg-baseline")
	workerSrv := httptest.NewServer(fullstackdemo.NewWorkerHandler())
	defer workerSrv.Close()

	// 2. Start baseline checkout-api
	t.Setenv("DIVERGE_ENV", "baseline")
	t.Setenv("DIVERGE_PREVIEW_SCHEMA", "public")
	t.Setenv("DATABASE_URL", "postgres://postgres:baseline@postgres.default.svc:5432/orders?search_path=public")
	t.Setenv("FEATURE_INSTANT_PAYOUTS", "false")
	t.Setenv("FEATURE_CHECKOUT_V2_BANNER", "false")
	t.Setenv("ORDERS_QUEUE_TOPIC", "orders-events")
	checkoutBaselineSrv := httptest.NewServer(fullstackdemo.NewCheckoutHandler(workerSrv.URL, workerSrv.Client()))
	defer checkoutBaselineSrv.Close()

	// 3. Start PR-42 preview checkout-api (delta deployment with isolated schema, feature flags, and Kafka topic)
	t.Setenv("DIVERGE_ENV", "pr-42")
	t.Setenv("DIVERGE_PREVIEW_SCHEMA", "preview_pr_42")
	t.Setenv("DATABASE_URL", "postgres://postgres:preview@postgres.default.svc:5432/orders?search_path=preview_pr_42")
	t.Setenv("FEATURE_INSTANT_PAYOUTS", "true")
	t.Setenv("FEATURE_CHECKOUT_V2_BANNER", "true")
	t.Setenv("ORDERS_QUEUE_TOPIC", "orders-events-pr-42")
	checkoutPreviewSrv := httptest.NewServer(fullstackdemo.NewCheckoutHandler(workerSrv.URL, workerSrv.Client()))
	defer checkoutPreviewSrv.Close()

	// 4. Simulated Gateway / Mesh L7 header router in front of checkout-api
	baselineURL, err := url.Parse(checkoutBaselineSrv.URL)
	require.NoError(t, err)
	previewURL, err := url.Parse(checkoutPreviewSrv.URL)
	require.NoError(t, err)

	baselineProxy := httputil.NewSingleHostReverseProxy(baselineURL)
	previewProxy := httputil.NewSingleHostReverseProxy(previewURL)

	checkoutRouterSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-diverge-env") == "pr-42" {
			previewProxy.ServeHTTP(w, r)
			return
		}
		baselineProxy.ServeHTTP(w, r)
	}))
	defer checkoutRouterSrv.Close()

	// 5. Start baseline storefront-web pointing at the header-routed checkout-api service
	t.Setenv("DIVERGE_ENV", "baseline")
	storefrontSrv := httptest.NewServer(fullstackdemo.NewStorefrontHandler(checkoutRouterSrv.URL, checkoutRouterSrv.Client()))
	defer storefrontSrv.Close()

	t.Run("baseline request without x-diverge-env header routes to baseline checkout-api", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, storefrontSrv.URL+"/api/checkout", nil)
		require.NoError(t, err)

		resp, err := storefrontSrv.Client().Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		require.Equal(t, http.StatusOK, resp.StatusCode)

		var sfResp fullstackdemo.StorefrontResponse
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&sfResp))
		assert.Equal(t, "baseline", sfResp.StorefrontEnv)
		assert.Equal(t, "", sfResp.PropagatedHeader)

		var coResp fullstackdemo.CheckoutResponse
		require.NoError(t, json.Unmarshal(sfResp.Checkout, &coResp))
		assert.Equal(t, "checkout-api", coResp.Service)
		assert.Equal(t, "baseline", coResp.Environment)
		assert.Equal(t, "public", coResp.DatabaseSchema)
		assert.False(t, coResp.InstantPayoutsEnabled)
		assert.False(t, coResp.CheckoutV2Banner)
		assert.Equal(t, "orders-events", coResp.OrdersQueueTopic)
		assert.Equal(t, "", coResp.ReceivedPreviewHeader)

		var wkResp fullstackdemo.WorkerResponse
		require.NoError(t, json.Unmarshal(coResp.WorkerResponse, &wkResp))
		assert.Equal(t, "fulfillment-worker", wkResp.Service)
		assert.Equal(t, "baseline", wkResp.Environment)
		assert.Equal(t, "", wkResp.ReceivedPreviewHeader)
	})

	t.Run("preview request with x-diverge-env: pr-42 routes through baseline storefront to pr-42 checkout-api and propagates downstream", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, storefrontSrv.URL+"/api/checkout", nil)
		require.NoError(t, err)
		req.Header.Set("x-diverge-env", "pr-42")
		req.Header.Set("baggage", "diverge.env=pr-42")

		resp, err := storefrontSrv.Client().Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		require.Equal(t, http.StatusOK, resp.StatusCode)

		var sfResp fullstackdemo.StorefrontResponse
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&sfResp))
		assert.Equal(t, "baseline", sfResp.StorefrontEnv)
		assert.Equal(t, "pr-42", sfResp.PropagatedHeader)

		var coResp fullstackdemo.CheckoutResponse
		require.NoError(t, json.Unmarshal(sfResp.Checkout, &coResp))
		assert.Equal(t, "checkout-api", coResp.Service)
		assert.Equal(t, "pr-42", coResp.Environment)
		assert.Equal(t, "preview_pr_42", coResp.DatabaseSchema)
		assert.True(t, coResp.InstantPayoutsEnabled)
		assert.True(t, coResp.CheckoutV2Banner)
		assert.Equal(t, "orders-events-pr-42", coResp.OrdersQueueTopic)
		assert.Equal(t, "pr-42", coResp.ReceivedPreviewHeader)

		var wkResp fullstackdemo.WorkerResponse
		require.NoError(t, json.Unmarshal(coResp.WorkerResponse, &wkResp))
		assert.Equal(t, "fulfillment-worker", wkResp.Service)
		assert.Equal(t, "baseline", wkResp.Environment)
		assert.Equal(t, "pr-42", wkResp.ReceivedPreviewHeader)
	})
}
