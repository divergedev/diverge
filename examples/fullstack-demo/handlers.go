package fullstackdemo

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

var propagatedHeaders = []string{
	"x-diverge-env",
	"x-preview-env",
	"baggage",
	"traceparent",
}

// HealthResponse is returned by /healthz endpoints across all demo services.
type HealthResponse struct {
	Status  string `json:"status"`
	Service string `json:"service"`
}

// StorefrontResponse wraps the storefront context and downstream checkout-api response.
type StorefrontResponse struct {
	StorefrontEnv    string          `json:"storefrontEnv"`
	PropagatedHeader string          `json:"propagatedHeader"`
	Checkout         json.RawMessage `json:"checkout"`
}

// CheckoutResponse represents the JSON payload returned by checkout-api.
type CheckoutResponse struct {
	Service               string          `json:"service"`
	Environment           string          `json:"environment"`
	DatabaseSchema        string          `json:"databaseSchema"`
	DatabaseConfigured    bool            `json:"databaseConfigured"`
	DatabaseURL           string          `json:"databaseURL,omitempty"`
	InstantPayoutsEnabled bool            `json:"instantPayoutsEnabled"`
	CheckoutV2Banner      bool            `json:"checkoutV2Banner"`
	OrdersQueueTopic      string          `json:"ordersQueueTopic"`
	ReceivedPreviewHeader string          `json:"receivedPreviewHeader"`
	WorkerResponse        json.RawMessage `json:"workerResponse,omitempty"`
}

// WorkerResponse represents the JSON payload returned by fulfillment-worker.
type WorkerResponse struct {
	Service               string `json:"service"`
	Environment           string `json:"environment"`
	OrdersQueueTopic      string `json:"ordersQueueTopic"`
	ConsumerGroup         string `json:"consumerGroup"`
	ReceivedPreviewHeader string `json:"receivedPreviewHeader"`
}

// NewStorefrontHandler constructs the HTTP handler for storefront-web.
func NewStorefrontHandler(checkoutAPIURL string, client *http.Client) http.Handler {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	storefrontEnv := getenv("DIVERGE_ENV", "baseline")
	baseURL := strings.TrimRight(checkoutAPIURL, "/")

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, HealthResponse{
			Status:  "ok",
			Service: "storefront-web",
		})
	})

	mux.HandleFunc("/api/checkout", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		req, err := http.NewRequestWithContext(r.Context(), r.Method, baseURL+"/checkout", nil)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		copyPropagationHeaders(req.Header, r.Header)

		resp, err := client.Do(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer func() { _ = resp.Body.Close() }()

		body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}

		writeJSON(w, resp.StatusCode, StorefrontResponse{
			StorefrontEnv:    storefrontEnv,
			PropagatedHeader: r.Header.Get("x-diverge-env"),
			Checkout:         json.RawMessage(body),
		})
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		activeEnv := r.Header.Get("x-diverge-env")
		if activeEnv == "" {
			activeEnv = "baseline"
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, renderStorefrontHTML(storefrontEnv, activeEnv))
	})

	return mux
}

// NewCheckoutHandler constructs the HTTP handler for checkout-api.
func NewCheckoutHandler(workerURL string, client *http.Client) http.Handler {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	env := getenv("DIVERGE_ENV", "baseline")
	schema := getenv("DIVERGE_PREVIEW_SCHEMA", "public")
	rawDBURL := os.Getenv("DATABASE_URL")
	maskedDBURL := maskDatabaseURL(rawDBURL)
	topic := getenv("ORDERS_QUEUE_TOPIC", "orders-events")
	instantPayouts := strings.EqualFold(getenv("FEATURE_INSTANT_PAYOUTS", "false"), "true")
	v2Banner := strings.EqualFold(getenv("FEATURE_CHECKOUT_V2_BANNER", "false"), "true")
	trimmedWorkerURL := strings.TrimRight(workerURL, "/")

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, HealthResponse{
			Status:  "ok",
			Service: "checkout-api",
		})
	})

	mux.HandleFunc("/checkout", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var workerPayload json.RawMessage
		if trimmedWorkerURL != "" {
			req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, trimmedWorkerURL+"/enqueue", nil)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			copyPropagationHeaders(req.Header, r.Header)

			resp, err := client.Do(req)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			defer func() { _ = resp.Body.Close() }()

			body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			workerPayload = json.RawMessage(body)
		}

		writeJSON(w, http.StatusOK, CheckoutResponse{
			Service:               "checkout-api",
			Environment:           env,
			DatabaseSchema:        schema,
			DatabaseConfigured:    rawDBURL != "",
			DatabaseURL:           maskedDBURL,
			InstantPayoutsEnabled: instantPayouts,
			CheckoutV2Banner:      v2Banner,
			OrdersQueueTopic:      topic,
			ReceivedPreviewHeader: r.Header.Get("x-diverge-env"),
			WorkerResponse:        workerPayload,
		})
	})

	return mux
}

// NewWorkerHandler constructs the HTTP handler for fulfillment-worker.
func NewWorkerHandler() http.Handler {
	env := getenv("DIVERGE_ENV", "baseline")
	topic := getenv("ORDERS_QUEUE_TOPIC", "orders-events")
	consumerGroup := getenv("CONSUMER_GROUP_ID", "orders-cg-baseline")

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, HealthResponse{
			Status:  "ok",
			Service: "fulfillment-worker",
		})
	})

	mux.HandleFunc("/enqueue", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		writeJSON(w, http.StatusOK, WorkerResponse{
			Service:               "fulfillment-worker",
			Environment:           env,
			OrdersQueueTopic:      topic,
			ConsumerGroup:         consumerGroup,
			ReceivedPreviewHeader: r.Header.Get("x-diverge-env"),
		})
	})

	return mux
}

func copyPropagationHeaders(dst, src http.Header) {
	for _, key := range propagatedHeaders {
		if val := src.Get(key); val != "" {
			dst.Set(key, val)
		}
	}
}

func getenv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

func maskDatabaseURL(raw string) string {
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "configured"
	}
	if parsed.User != nil {
		username := parsed.User.Username()
		if _, hasPassword := parsed.User.Password(); hasPassword {
			parsed.User = url.UserPassword(username, "****")
		}
	}
	return parsed.String()
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func renderStorefrontHTML(storefrontEnv, activeHeader string) string {
	return "<!DOCTYPE html>\n" +
		"<html lang=\"en\">\n" +
		"<head>\n" +
		"\t<meta charset=\"utf-8\">\n" +
		"\t<title>Diverge Fullstack Demo Storefront</title>\n" +
		"</head>\n" +
		"<body>\n" +
		"\t<h1>Diverge Demo Storefront</h1>\n" +
		"\t<p>Storefront instance environment: <strong>" + storefrontEnv + "</strong></p>\n" +
		"\t<p>Active routing header (<code>x-diverge-env</code>): <strong>" + activeHeader + "</strong></p>\n" +
		"\t<p>Switch preview environment in browser: <a href=\"/?_diverge_env=pr-42\">Activate PR #42</a> | <a href=\"/?_diverge_env=reset\">Reset to Baseline</a></p>\n" +
		"\t<button id=\"checkout-btn\" onclick=\"fetch('/api/checkout').then(r => r.json()).then(d => document.getElementById('out').textContent = JSON.stringify(d, null, 2))\">Test /api/checkout</button>\n" +
		"\t<pre id=\"out\"></pre>\n" +
		"</body>\n" +
		"</html>\n"
}
