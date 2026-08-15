package async

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	v1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

// WebhookProvisioner calls an external webhook to provision async infrastructure.
type WebhookProvisioner struct {
	Endpoint   string
	HTTPClient *http.Client
}

// WebhookRequest is sent to the provisioning webhook.
type WebhookRequest struct {
	Action      string                  `json:"action"` // "provision" or "teardown"
	Environment string                  `json:"environment"`
	Namespace   string                  `json:"namespace"`
	Route       v1alpha1.AsyncRouteSpec `json:"route"`
}

// WebhookResponse is returned by the provisioning webhook.
type WebhookResponse struct {
	ResolvedTarget string            `json:"resolvedTarget"`
	EnvVars        map[string]string `json:"envVars"`
}

// NewWebhookProvisioner creates a WebhookProvisioner targeting the given endpoint URL.
func NewWebhookProvisioner(endpoint string) *WebhookProvisioner {
	return &WebhookProvisioner{
		Endpoint:   endpoint,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// Name returns the provisioner name.
func (w *WebhookProvisioner) Name() string { return "webhook" }

// Provision calls the webhook endpoint to create async infrastructure and returns
// the resolved target and environment variables to inject into preview pods.
func (w *WebhookProvisioner) Provision(ctx context.Context, env *v1alpha1.Environment, route v1alpha1.AsyncRouteSpec) (*ProvisionResult, error) {
	req := WebhookRequest{
		Action:      "provision",
		Environment: env.Name,
		Namespace:   env.Namespace,
		Route:       route,
	}
	resp, err := w.call(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("webhook provision failed: %w", err)
	}

	envVars := resp.EnvVars
	if envVars == nil {
		envVars = make(map[string]string)
	}
	// Apply sensible defaults if no explicit mapping
	if len(route.EnvVarMapping) == 0 {
		if defaultVar := v1alpha1.DefaultEnvVarForProtocol(route.Protocol); defaultVar != "" {
			envVars[defaultVar] = resp.ResolvedTarget
		}
	} else {
		for envVar, tmpl := range route.EnvVarMapping {
			if tmpl == "{{ .ResolvedTarget }}" || tmpl == "" {
				envVars[envVar] = resp.ResolvedTarget
			}
		}
	}

	return &ProvisionResult{
		ResolvedTarget: resp.ResolvedTarget,
		EnvVars:        envVars,
	}, nil
}

// Teardown calls the webhook endpoint to remove async infrastructure for the given route.
func (w *WebhookProvisioner) Teardown(ctx context.Context, env *v1alpha1.Environment, route v1alpha1.AsyncRouteSpec) error {
	req := WebhookRequest{
		Action:      "teardown",
		Environment: env.Name,
		Namespace:   env.Namespace,
		Route:       route,
	}
	_, err := w.call(ctx, req)
	if err != nil {
		return fmt.Errorf("webhook teardown failed: %w", err)
	}
	return nil
}

func (w *WebhookProvisioner) call(ctx context.Context, reqBody WebhookRequest) (*WebhookResponse, error) {
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := w.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}

	var result WebhookResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode webhook response: %w", err)
	}
	return &result, nil
}
