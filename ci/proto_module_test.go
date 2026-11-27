package ci_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	divergev1alpha1 "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	"github.com/divergedev/diverge/api/gen/diverge/v1alpha1/divergev1alpha1connect"
)

const rootModulePath = "github.com/divergedev/diverge"

func hasForbiddenModule(modPath string, prefixes []string) bool {
	if modPath == rootModulePath {
		return true
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(modPath, prefix) {
			return true
		}
	}
	return false
}

func runGoInAPIGen(t *testing.T, args ...string) string {
	t.Helper()
	goBin, err := exec.LookPath("go")
	require.NoError(t, err, "go binary must be available in PATH")

	cmd := exec.CommandContext(t.Context(), goBin, args...)
	cmd.Dir = filepath.Join("..", "api", "gen")
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "go %s in api/gen failed:\n%s", strings.Join(args, " "), string(out))
	return string(out)
}

func TestRootModule_NoReplaceDirectives(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "go.mod"))
	require.NoError(t, err)
	for line := range strings.SplitSeq(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") {
			continue
		}
		assert.False(t, strings.HasPrefix(trimmed, "replace ") || strings.HasPrefix(trimmed, "replace("),
			"root go.mod must not contain replace directives (breaks go install @latest): %s", trimmed)
		assert.NotEqual(t, "github.com/divergedev/diverge/api/gen v0.0.0", trimmed,
			"root go.mod must pin a published version of github.com/divergedev/diverge/api/gen, not v0.0.0")
	}
}

func TestProtoModule_GoModClean(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "api", "gen", "go.mod"))
	require.NoError(t, err)
	content := string(raw)

	assert.Contains(t, content, "module github.com/divergedev/diverge/api/gen\n")
	assert.NotContains(t, content, "replace ", "published api/gen module must not contain replace directives")
	assert.NotContains(t, content, "\nreplace(", "published api/gen module must not contain replace blocks")

	forbiddenInGoMod := []string{
		"k8s.io/",
		"sigs.k8s.io/",
		"knative.dev/",
		"github.com/jackc/pgx",
		"github.com/twmb/franz-go",
		"github.com/spf13/cobra",
		"github.com/mark3labs/mcp-go",
		"google.golang.org/grpc",
		"hegel.dev/",
		"pgregory.net/rapid",
		"github.com/onsi/",
		"github.com/stretchr/testify",
		"github.com/ebitengine/purego",
	}

	inRequire := false
	for line := range strings.SplitSeq(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") {
			continue
		}
		if strings.HasPrefix(trimmed, "require (") {
			inRequire = true
			continue
		}
		if inRequire && trimmed == ")" {
			inRequire = false
			continue
		}
		var modPath string
		if strings.HasPrefix(trimmed, "require ") {
			fields := strings.Fields(strings.TrimPrefix(trimmed, "require "))
			if len(fields) > 0 {
				modPath = fields[0]
			}
		} else if inRequire {
			fields := strings.Fields(trimmed)
			if len(fields) > 0 {
				modPath = fields[0]
			}
		}
		if modPath != "" {
			assert.False(
				t,
				hasForbiddenModule(modPath, forbiddenInGoMod),
				"unexpected dependency %q in api/gen/go.mod",
				modPath,
			)
		}
	}
}

func TestProtoModule_CompiledDepsAllowlist(t *testing.T) {
	allowedCompiled := map[string]struct{}{
		"github.com/divergedev/diverge/api/gen": {},
		"connectrpc.com/connect":                {},
		"github.com/protocgen/proto2mcp":        {},
		"google.golang.org/protobuf":            {},
		"go.opentelemetry.io/otel":              {},
		"go.opentelemetry.io/otel/metric":       {},
		"github.com/cespare/xxhash/v2":          {},
	}

	depsOut := runGoInAPIGen(t, "list", "-deps", "-f", "{{if .Module}}{{.Module.Path}}{{end}}", "./...")
	seenCompiled := make(map[string]struct{})
	for line := range strings.SplitSeq(depsOut, "\n") {
		modPath := strings.TrimSpace(line)
		if modPath == "" {
			continue
		}
		seenCompiled[modPath] = struct{}{}
		_, allowed := allowedCompiled[modPath]
		assert.True(t, allowed, "compiled dependency module %q is outside api/gen allowlist", modPath)
	}
	assert.Contains(t, seenCompiled, "connectrpc.com/connect")
	assert.Contains(t, seenCompiled, "google.golang.org/protobuf")

	forbiddenPlatform := []string{
		"k8s.io/",
		"sigs.k8s.io/",
		"knative.dev/",
		"github.com/jackc/pgx",
		"github.com/twmb/franz-go",
		"github.com/spf13/cobra",
		"github.com/mark3labs/mcp-go",
		"google.golang.org/grpc",
		"pgregory.net/rapid",
		"github.com/onsi/",
	}
	allModsOut := runGoInAPIGen(t, "list", "-m", "all")
	for line := range strings.SplitSeq(allModsOut, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		modPath := fields[0]
		assert.False(
			t,
			hasForbiddenModule(modPath, forbiddenPlatform),
			"forbidden platform module %q found in api/gen module graph",
			modPath,
		)
	}
}

func TestProtoModule_BuildsAndVetsStandalone(t *testing.T) {
	runGoInAPIGen(t, "build", "./...")
	runGoInAPIGen(t, "vet", "./...")
}

type stubPreviewResolverHandler struct{}

func (stubPreviewResolverHandler) ResolvePreview(
	_ context.Context,
	req *connect.Request[divergev1alpha1.ResolvePreviewRequest],
) (*connect.Response[divergev1alpha1.ResolvePreviewResponse], error) {
	return connect.NewResponse(&divergev1alpha1.ResolvePreviewResponse{
		PreviewId: req.Msg.GetPreviewId(),
		Namespace: req.Msg.GetNamespace(),
		Targets: []*divergev1alpha1.ResolvedTarget{
			{
				Service:     "web",
				Namespace:   req.Msg.GetNamespace(),
				Mode:        divergev1alpha1.ResolvedTargetMode_RESOLVED_TARGET_MODE_TUNNEL,
				Ready:       true,
				UpstreamUrl: "http://diverge-tunnel-proxy.diverge-system.svc.cluster.local:8081",
				ProxyHeaders: map[string]string{
					"X-Diverge-Preview": req.Msg.GetPreviewId(),
				},
				TunnelId: "tun-1",
				Port:     3000,
			},
		},
	}), nil
}

func TestProtoModule_PreviewResolverBinaryProtobuf(t *testing.T) {
	mux := http.NewServeMux()
	path, handler := divergev1alpha1connect.NewPreviewResolverServiceHandler(stubPreviewResolverHandler{})
	var recordedContentType string
	mux.Handle(path, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recordedContentType = r.Header.Get("Content-Type")
		handler.ServeHTTP(w, r)
	}))

	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := divergev1alpha1connect.NewPreviewResolverServiceClient(srv.Client(), srv.URL)
	resp, err := client.ResolvePreview(t.Context(), connect.NewRequest(&divergev1alpha1.ResolvePreviewRequest{
		Namespace: "default",
		PreviewId: "pr-42",
	}))
	require.NoError(t, err)
	assert.Equal(t, "application/proto", recordedContentType)
	assert.Equal(t, "pr-42", resp.Msg.GetPreviewId())
	assert.Equal(t, "default", resp.Msg.GetNamespace())
	require.Len(t, resp.Msg.GetTargets(), 1)
	assert.Equal(t, "web", resp.Msg.GetTargets()[0].GetService())
	assert.Equal(t, divergev1alpha1.ResolvedTargetMode_RESOLVED_TARGET_MODE_TUNNEL, resp.Msg.GetTargets()[0].GetMode())
	assert.True(t, resp.Msg.GetTargets()[0].GetReady())
	assert.Equal(t, int32(3000), resp.Msg.GetTargets()[0].GetPort())
}
