package secrets

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type VaultResolver struct {
	addr        string
	token       string
	client      *http.Client
	role        string
	mountPath   string
	tokenExpiry time.Time
	mu          sync.RWMutex
}

func NewVaultResolver() *VaultResolver {
	addr := os.Getenv("VAULT_ADDR")
	token := os.Getenv("VAULT_TOKEN")

	return &VaultResolver{
		addr:   addr,
		token:  token,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

func (r *VaultResolver) SetRole(role, mountPath string) {
	r.role = role
	r.mountPath = mountPath
}

func (r *VaultResolver) Resolve(ctx context.Context, ref SecretRef) (string, error) {
	if !strings.HasPrefix(r.addr, "https://") {
		return "", fmt.Errorf("vault address must use HTTPS: %s", r.addr)
	}

	token, err := r.getToken(ctx)
	if err != nil {
		return "", err
	}

	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	reqURL := fmt.Sprintf("%s/v1/%s", strings.TrimRight(r.addr, "/"), strings.TrimLeft(ref.Path, "/"))
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, reqURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("X-Vault-Token", token)

	resp, err := r.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("vault request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("vault returned status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Data map[string]interface{} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode vault response: %w", err)
	}

	if result.Data == nil {
		return "", fmt.Errorf("no data found at vault path %q", ref.Path)
	}

	// Try KV v2 first: actual data is inside data.data
	var secretMap map[string]interface{}
	if dataMap, ok := result.Data["data"].(map[string]interface{}); ok {
		secretMap = dataMap // KV v2
	} else {
		secretMap = result.Data // KV v1
	}

	val, ok := secretMap[ref.Key]
	if !ok {
		return "", fmt.Errorf("key %q not found in vault secret", ref.Key)
	}

	strVal, ok := val.(string)
	if !ok {
		return "", fmt.Errorf("key %q is not a string", ref.Key)
	}

	return strVal, nil
}

func (r *VaultResolver) getToken(ctx context.Context) (string, error) {
	r.mu.RLock()
	if r.token != "" && time.Now().Before(r.tokenExpiry) {
		token := r.token
		r.mu.RUnlock()
		return token, nil
	}
	// If we have a token but no expiry (e.g. from env), just use it
	if r.token != "" && r.tokenExpiry.IsZero() {
		token := r.token
		r.mu.RUnlock()
		return token, nil
	}
	r.mu.RUnlock()

	// Try Kubernetes Auth
	jwtBytes, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/token")
	if err != nil {
		if os.IsNotExist(err) && r.token == "" {
			return "", fmt.Errorf("no VAULT_TOKEN set and kubernetes token not found")
		} else if os.IsNotExist(err) {
			// fall back to whatever token we might have
			return r.token, nil
		}
		return "", fmt.Errorf("failed to read kubernetes token: %w", err)
	}

	if r.role == "" {
		return "", fmt.Errorf("kubernetes auth requires a role")
	}

	mount := r.mountPath
	if mount == "" {
		mount = "kubernetes"
	}

	payload := map[string]string{
		"role": r.role,
		"jwt":  string(jwtBytes),
	}
	body, _ := json.Marshal(payload)

	authCtx, authCancel := context.WithTimeout(ctx, 30*time.Second)
	defer authCancel()

	reqURL := fmt.Sprintf("%s/v1/auth/%s/login", strings.TrimRight(r.addr, "/"), mount)
	req, err := http.NewRequestWithContext(authCtx, http.MethodPost, reqURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("kubernetes auth request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("kubernetes auth returned status %d: %s", resp.StatusCode, string(b))
	}

	var authResp struct {
		Auth struct {
			ClientToken   string `json:"client_token"`
			LeaseDuration int    `json:"lease_duration"`
		} `json:"auth"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&authResp); err != nil {
		return "", fmt.Errorf("failed to decode auth response: %w", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.token = authResp.Auth.ClientToken
	// Cache it for slightly less than the lease duration
	r.tokenExpiry = time.Now().Add(time.Duration(authResp.Auth.LeaseDuration-10) * time.Second)

	return r.token, nil
}
