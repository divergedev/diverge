package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	divergev1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type ConfigWatcher struct {
	crdClient    client.Client
	pgName       string
	envFile      string
	lastEnvHash  string
	mu           sync.Mutex
	onUpdate     func(services []divergev1alpha1.PreviewGroupServiceStatus)
	onEnvChange  func(diff map[string]string) // called when env vars change (for --watch-env)
	proxyAddr    string
	proxyMode    string
	lastServices string
	lastEnvMap   map[string]string // tracks previous env for diff
	synced       bool
}

// ConfigWatcherOption configures optional behavior for a ConfigWatcher.
type ConfigWatcherOption func(*ConfigWatcher)

// WithOnUpdate registers a callback invoked when PreviewGroup service status changes.
func WithOnUpdate(fn func([]divergev1alpha1.PreviewGroupServiceStatus)) ConfigWatcherOption {
	return func(cw *ConfigWatcher) { cw.onUpdate = fn }
}

// WithProxyAddr sets the loopback proxy address for .env.diverge output.
func WithProxyAddr(addr string) ConfigWatcherOption {
	return func(cw *ConfigWatcher) { cw.proxyAddr = addr }
}

// WithProxyMode sets the loopback proxy mode for .env.diverge output.
func WithProxyMode(mode string) ConfigWatcherOption {
	return func(cw *ConfigWatcher) { cw.proxyMode = mode }
}

// WithOnEnvChange registers a callback invoked when env vars change.
// The diff map contains changed keys with "old → new" descriptions.
func WithOnEnvChange(fn func(diff map[string]string)) ConfigWatcherOption {
	return func(cw *ConfigWatcher) { cw.onEnvChange = fn }
}

// LatestEnvMap returns a copy of the most recently computed env map.
// Thread-safe — called by Supervisor's envBuilder on each restart.
func (cw *ConfigWatcher) LatestEnvMap() map[string]string {
	cw.mu.Lock()
	defer cw.mu.Unlock()

	if cw.lastEnvMap == nil {
		return nil
	}
	cp := make(map[string]string, len(cw.lastEnvMap))
	for k, v := range cw.lastEnvMap {
		cp[k] = v
	}
	return cp
}

// SetOnEnvChange registers an env change callback after construction.
// Must be called before Watch() starts.
func (cw *ConfigWatcher) SetOnEnvChange(fn func(diff map[string]string)) {
	cw.onEnvChange = fn
}

func NewConfigWatcher(crdClient client.Client, pgName string, envFile string, opts ...ConfigWatcherOption) *ConfigWatcher {
	cw := &ConfigWatcher{
		crdClient: crdClient,
		pgName:    pgName,
		envFile:   envFile,
	}
	for _, opt := range opts {
		opt(cw)
	}
	return cw
}

func (cw *ConfigWatcher) Watch(ctx context.Context) error {
	// Poll PreviewGroup status every 5 seconds
	// (Using polling instead of informer since CLI may not have watch RBAC on CRDs)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	// Initial sync
	cw.syncOnce(ctx)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			cw.syncOnce(ctx)
		}
	}
}

func (cw *ConfigWatcher) syncOnce(ctx context.Context) {
	// Use a short timeout per poll to avoid blocking the loop if API server is slow
	pollCtx, pollCancel := context.WithTimeout(ctx, 5*time.Second)
	defer pollCancel()

	var pg divergev1alpha1.PreviewGroup
	err := cw.crdClient.Get(pollCtx, client.ObjectKey{Name: cw.pgName}, &pg)
	if err != nil {
		return // PG may not exist yet or may be deleted
	}

	envMap := cw.buildEnvMap(&pg)
	cw.writeEnvFile(envMap)

	if cw.onUpdate != nil {
		svcKey := serializeServices(pg.Status.Services)
		if !cw.synced || svcKey != cw.lastServices {
			cw.synced = true
			cw.lastServices = svcKey
			cw.onUpdate(pg.Status.Services)
		}
	}

	// Env change detection for --watch-env (phase-gated).
	// Only trigger restart when PreviewGroup is Running — avoids restart storms
	// during rolling deployments when services come online gradually.
	if cw.onEnvChange != nil && pg.Status.Phase == divergev1alpha1.PreviewGroupPhaseRunning {
		diff := cw.computeEnvDiff(envMap)
		if len(diff) > 0 {
			cw.mu.Lock()
			cw.lastEnvMap = envMap
			cw.mu.Unlock()
			cw.onEnvChange(diff)
		}
	}

	// Track env map for diff on first sync.
	if cw.lastEnvMap == nil {
		cw.mu.Lock()
		cw.lastEnvMap = envMap
		cw.mu.Unlock()
	}
}

func serializeServices(svcs []divergev1alpha1.PreviewGroupServiceStatus) string {
	var b strings.Builder
	for _, s := range svcs {
		fmt.Fprintf(&b, "%s=%s;", s.Name, s.URL)
	}
	return b.String()
}

func (cw *ConfigWatcher) buildEnvMap(pg *divergev1alpha1.PreviewGroup) map[string]string {
	env := make(map[string]string)

	// Routing metadata
	env["DIVERGE_PREVIEW_ID"] = pg.Name
	if pg.Spec.Routing.HeaderKey != "" {
		env["DIVERGE_HEADER_KEY"] = pg.Spec.Routing.HeaderKey
	}
	if pg.Spec.Routing.HeaderValue != "" {
		env["DIVERGE_HEADER_VALUE"] = pg.Spec.Routing.HeaderValue
	}

	if cw.proxyAddr != "" {
		env["DIVERGE_PROXY_URL"] = cw.proxyAddr
		mode := cw.proxyMode
		if mode == "" {
			mode = "path"
		}
		env["DIVERGE_PROXY_MODE"] = mode
	}

	// Service endpoints from status
	for _, svc := range pg.Status.Services {
		if svc.URL != "" {
			key := fmt.Sprintf("DIVERGE_SVC_%s_URL", strings.ToUpper(strings.ReplaceAll(svc.Name, "-", "_")))
			env[key] = svc.URL
		}
	}

	return env
}

func (cw *ConfigWatcher) writeEnvFile(envMap map[string]string) {
	// Build content
	keys := make([]string, 0, len(envMap))
	for k := range envMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var sb strings.Builder
	sb.WriteString("# Generated by diverge dev - do not edit\n")
	for _, k := range keys {
		fmt.Fprintf(&sb, "%s=%s\n", k, envMap[k])
	}

	content := sb.String()

	// Only write if changed
	cw.mu.Lock()
	defer cw.mu.Unlock()
	if content == cw.lastEnvHash {
		return
	}

	// Write atomically: temp file + rename to avoid partial reads by hot-reloaders
	dir := filepath.Dir(cw.envFile)
	tmpFile, err := os.CreateTemp(dir, ".env.diverge.tmp.*")
	if err != nil {
		return
	}
	tmpName := tmpFile.Name()

	if _, err := tmpFile.WriteString(content); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpName)
		return
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpName)
		return
	}
	if err := os.Rename(tmpName, cw.envFile); err != nil {
		_ = os.Remove(tmpName)
		return
	}
	// Restrict permissions — .env.diverge may contain service URLs and routing headers.
	_ = os.Chmod(cw.envFile, 0600)

	old := cw.lastEnvHash
	cw.lastEnvHash = content

	if old != "" {
		fmt.Printf("[diverge] 🔄 Routing config updated, wrote %s\n", cw.envFile)
	}
}

// computeEnvDiff returns a map of changed env vars between lastEnvMap and current.
// Keys map to "old_value → new_value" for changes, or just "new_value" for additions.
func (cw *ConfigWatcher) computeEnvDiff(current map[string]string) map[string]string {
	cw.mu.Lock()
	prev := cw.lastEnvMap
	cw.mu.Unlock()

	if prev == nil {
		return nil // first sync, no diff
	}

	diff := make(map[string]string)
	for k, v := range current {
		if oldV, ok := prev[k]; !ok {
			diff[k] = v + " (added)"
		} else if oldV != v {
			diff[k] = oldV + " → " + v
		}
	}
	for k := range prev {
		if _, ok := current[k]; !ok {
			diff[k] = prev[k] + " (removed)"
		}
	}
	return diff
}
