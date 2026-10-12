package ci_test

import (
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"sigs.k8s.io/yaml"
)

type githubActionSpec struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Branding    struct {
		Icon  string `json:"icon"`
		Color string `json:"color"`
	} `json:"branding"`
	Inputs map[string]struct {
		Description string `json:"description"`
		Required    bool   `json:"required"`
		Default     string `json:"default"`
	} `json:"inputs"`
	Outputs map[string]struct {
		Description string `json:"description"`
		Value       string `json:"value"`
	} `json:"outputs"`
	Runs struct {
		Using string `json:"using"`
		Steps []struct {
			Name  string            `json:"name"`
			ID    string            `json:"id"`
			Shell string            `json:"shell"`
			Env   map[string]string `json:"env"`
			Run   string            `json:"run"`
		} `json:"steps"`
	} `json:"runs"`
}

func TestGitHubActionYAMLSchema(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "action.yml"))
	if err != nil {
		t.Fatalf("failed to read action.yml: %v", err)
	}

	var spec githubActionSpec
	if err := yaml.Unmarshal(data, &spec); err != nil {
		t.Fatalf("failed to unmarshal action.yml: %v", err)
	}

	if spec.Name != "Diverge Preview Environment" {
		t.Errorf("unexpected action name: %q", spec.Name)
	}
	if spec.Runs.Using != "composite" {
		t.Fatalf("expected runs.using == composite, got %q", spec.Runs.Using)
	}

	requiredInputs := []string{
		"action",
		"version",
		"config",
		"kubeconfig",
		"namespace",
		"preview-name",
		"base-ref",
		"branch",
		"pr-number",
		"services",
		"image-registry",
		"image-tag",
		"header-key",
		"header-value",
		"ttl",
		"base-url",
		"wait",
		"wait-timeout",
		"dry-run",
	}
	for _, in := range requiredInputs {
		if _, ok := spec.Inputs[in]; !ok {
			t.Errorf("missing required input %q in action.yml", in)
		}
	}

	requiredOutputs := []string{
		"preview-name",
		"changed-services",
		"skipped",
		"routing-header",
		"routing-header-json",
		"preview-url",
	}
	for _, out := range requiredOutputs {
		if _, ok := spec.Outputs[out]; !ok {
			t.Errorf("missing required output %q in action.yml", out)
		}
	}

	if len(spec.Runs.Steps) < 2 {
		t.Fatalf("expected at least 2 composite steps in action.yml, got %d", len(spec.Runs.Steps))
	}
	foundPreviewStep := false
	for _, step := range spec.Runs.Steps {
		if step.ID == "preview" && strings.Contains(step.Run, "scripts/ci-preview.sh") {
			foundPreviewStep = true
		}
	}
	if !foundPreviewStep {
		t.Errorf("expected step with id 'preview' invoking scripts/ci-preview.sh")
	}
}

type gitlabComponentHeader struct {
	Spec struct {
		Inputs map[string]struct {
			Description string `json:"description"`
			Type        string `json:"type"`
			Default     string `json:"default"`
		} `json:"inputs"`
	} `json:"spec"`
}

func TestGitLabComponentYAMLSchema(t *testing.T) {
	componentPaths := []string{
		filepath.Join("..", "templates", "preview", "template.yml"),
		filepath.Join("..", "ci", "gitlab", ".gitlab-ci-component.yml"),
	}

	expectedInputs := []string{
		"diverge_services",
		"diverge_header_key",
		"diverge_ttl",
		"diverge_registry",
		"diverge_namespace",
		"diverge_base_url",
		"diverge_kubectl_version",
	}

	for _, p := range componentPaths {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("failed to read %s: %v", p, err)
		}

		parts := strings.SplitN(string(raw), "\n---\n", 2)
		if len(parts) != 2 {
			t.Fatalf("expected multi-document YAML separated by --- in %s", p)
		}

		var header gitlabComponentHeader
		if err := yaml.Unmarshal([]byte(parts[0]), &header); err != nil {
			t.Fatalf("failed to unmarshal component header in %s: %v", p, err)
		}
		for _, in := range expectedInputs {
			if _, ok := header.Spec.Inputs[in]; !ok {
				t.Errorf("%s missing input %q", p, in)
			}
		}

		var jobs map[string]any
		if err := yaml.Unmarshal([]byte(parts[1]), &jobs); err != nil {
			t.Fatalf("failed to unmarshal jobs document in %s: %v", p, err)
		}
		if _, ok := jobs["diverge:preview"]; !ok {
			t.Errorf("%s missing diverge:preview job", p)
		}
		if _, ok := jobs["diverge:cleanup"]; !ok {
			t.Errorf("%s missing diverge:cleanup job", p)
		}
		if !strings.Contains(parts[1], "dotenv: preview.env") {
			t.Errorf("%s missing dotenv: preview.env artifact report", p)
		}
		if !strings.Contains(parts[1], "allow_failure: true") {
			t.Errorf("%s missing allow_failure: true on manual cleanup rule", p)
		}
	}

	// editorconfig-checker-disable
	sampleManifest := `apiVersion: divergedev.com/v1alpha1
kind: PreviewGroup
metadata:
  name: mr-42
spec:
  source:
    provider: gitlab
    project: acme/platform
    mr: 42
    branch: feat/checkout-tax
    commitSHA: a1b2c3d4
  routing:
    mode: header
    headerKey: x-diverge-env
    headerValue: "mr-42"
  lifecycle:
    ttl: "4h"
    cleanupOnMerge: true
  services:
    - name: checkout-api
      mode: image
      namespace: default
      image: registry.gitlab.com/acme/platform/checkout-api:a1b2c3d4
`
	// editorconfig-checker-enable
	var pg divergeiov1alpha1.PreviewGroup
	if err := yaml.UnmarshalStrict([]byte(sampleManifest), &pg); err != nil {
		t.Fatalf("sample PreviewGroup manifest failed strict unmarshal: %v", err)
	}
	if pg.Spec.Source.Provider != "gitlab" || pg.Spec.Source.MR != 42 {
		t.Errorf("unexpected PreviewGroup source: %+v", pg.Spec.Source)
	}
	if len(pg.Spec.Services) != 1 || pg.Spec.Services[0].Mode != divergeiov1alpha1.ServiceModeImage {
		t.Errorf("unexpected PreviewGroup services: %+v", pg.Spec.Services)
	}
}

func TestCIPreviewScriptEndToEnd(t *testing.T) {
	scriptPath, err := filepath.Abs(filepath.Join("..", "scripts", "ci-preview.sh"))
	if err != nil {
		t.Fatalf("failed to resolve script path: %v", err)
	}

	binDir := t.TempDir()
	divergeBin := filepath.Join(binDir, "diverge")
	buildCmd := exec.Command("go", "build", "-o", divergeBin, "../cmd/diverge")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to build diverge CLI: %v\n%s", err, string(out))
	}

	kubectlLog := filepath.Join(binDir, "kubectl.log")
	kubectlBin := filepath.Join(binDir, "kubectl")
	kubectlScript := "#!/usr/bin/env bash\nprintf '%s\\n' \"$*\" >> " + kubectlLog + "\n"
	if err := os.WriteFile(kubectlBin, []byte(kubectlScript), 0o755); err != nil {
		t.Fatalf("failed to write mock kubectl: %v", err)
	}

	repoDir := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
		}
	}

	runGit("init", "-b", "main")
	runGit("config", "user.email", "ci@divergedev.com")
	runGit("config", "user.name", "Diverge CI")
	runGit("remote", "add", "origin", "https://github.com/acme/commerce.git")

	// editorconfig-checker-disable
	divergeConfig := `version: "1"
services:
  storefront-web:
    paths:
      - apps/storefront/**
    image:
      repository: ghcr.io/acme/storefront-web
    entrypoint: true
    dependsOn:
      - checkout-api
  checkout-api:
    paths:
      - services/checkout/**
    image:
      repository: ghcr.io/acme/checkout-api
    dependsOn:
      - fulfillment-worker
  fulfillment-worker:
    paths:
      - services/fulfillment/**
    image:
      repository: ghcr.io/acme/fulfillment-worker
`
	// editorconfig-checker-enable
	if err := os.WriteFile(filepath.Join(repoDir, ".diverge.yaml"), []byte(divergeConfig), 0o644); err != nil {
		t.Fatalf("failed to write .diverge.yaml: %v", err)
	}
	for _, dir := range []string{"apps/storefront", "services/checkout", "services/fulfillment"} {
		if err := os.MkdirAll(filepath.Join(repoDir, dir), 0o755); err != nil {
			t.Fatalf("failed to mkdir %s: %v", dir, err)
		}
		if err := os.WriteFile(filepath.Join(repoDir, dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
			t.Fatalf("failed to write initial main.go: %v", err)
		}
	}
	runGit("add", ".")
	runGit("commit", "-m", "initial commit")

	runGit("checkout", "-b", "feat/checkout-discount")
	if err := os.WriteFile(filepath.Join(repoDir, "services/checkout/main.go"), []byte("package main\n// updated\n"), 0o644); err != nil {
		t.Fatalf("failed to update checkout main.go: %v", err)
	}
	runGit("add", ".")
	runGit("commit", "-m", "update checkout-api")

	runScript := func(env map[string]string) (string, map[string]string, map[string]string) {
		t.Helper()
		ghOutFile := filepath.Join(t.TempDir(), "github_output")
		dotenvFile := filepath.Join(t.TempDir(), "preview.env")

		cmd := exec.Command(scriptPath)
		cmd.Dir = repoDir
		cmdEnv := append(os.Environ(),
			"DIVERGE_BIN="+divergeBin,
			"KUBECTL_BIN="+kubectlBin,
			"GITHUB_OUTPUT="+ghOutFile,
			"DIVERGE_DOTENV_FILE="+dotenvFile,
		)
		for k, v := range env {
			cmdEnv = append(cmdEnv, k+"="+v)
		}
		cmd.Env = cmdEnv

		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		if err := cmd.Run(); err != nil {
			t.Fatalf("ci-preview.sh failed (%v):\n%s", err, out.String())
		}

		return out.String(), parseKeyValueFile(ghOutFile), parseKeyValueFile(dotenvFile)
	}

	t.Run("validate", func(t *testing.T) {
		out, _, _ := runScript(map[string]string{
			"INPUT_ACTION": "validate",
		})
		if !strings.Contains(out, `"valid": true`) {
			t.Errorf("expected valid JSON output, got: %s", out)
		}
	})

	t.Run("diff", func(t *testing.T) {
		_, ghOut, dotenv := runScript(map[string]string{
			"INPUT_ACTION":   "diff",
			"INPUT_BASE_REF": "main",
		})
		if ghOut["changed-services"] != `["checkout-api"]` {
			t.Errorf("expected changed-services=[\"checkout-api\"], got %q", ghOut["changed-services"])
		}
		if ghOut["changed-count"] != "1" {
			t.Errorf("expected changed-count=1, got %q", ghOut["changed-count"])
		}
		if dotenv["DIVERGE_CHANGED_SERVICES"] != `["checkout-api"]` {
			t.Errorf("expected DIVERGE_CHANGED_SERVICES=[\"checkout-api\"], got %q", dotenv["DIVERGE_CHANGED_SERVICES"])
		}
	})

	t.Run("deploy_delta_dry_run", func(t *testing.T) {
		encodedKube := base64.StdEncoding.EncodeToString([]byte("apiVersion: v1\nkind: Config\n"))
		out, ghOut, dotenv := runScript(map[string]string{
			"INPUT_ACTION":         "deploy",
			"INPUT_BASE_REF":       "main",
			"INPUT_PR_NUMBER":      "42",
			"INPUT_IMAGE_REGISTRY": "ghcr.io/acme",
			"INPUT_BASE_URL":       "https://staging.example.com/",
			"INPUT_DRY_RUN":        "true",
			"INPUT_KUBECONFIG":     encodedKube,
		})

		if !strings.Contains(out, "PreviewGroup") || !strings.Contains(out, "ghcr.io/acme/checkout-api:pr-42") {
			t.Errorf("expected dry-run PreviewGroup output with checkout-api image, got:\n%s", out)
		}
		if !strings.Contains(out, "name: storefront-web") || !strings.Contains(out, "mode: baseline") {
			t.Errorf("expected baseline services from .diverge.yaml in PreviewGroup output, got:\n%s", out)
		}

		if ghOut["preview-name"] != "pr-42" {
			t.Errorf("expected preview-name=pr-42, got %q", ghOut["preview-name"])
		}
		if ghOut["skipped"] != "false" {
			t.Errorf("expected skipped=false, got %q", ghOut["skipped"])
		}
		if ghOut["changed-services"] != `["checkout-api"]` {
			t.Errorf("expected changed-services=[\"checkout-api\"], got %q", ghOut["changed-services"])
		}
		if ghOut["changed-count"] != "1" {
			t.Errorf("expected changed-count=1, got %q", ghOut["changed-count"])
		}
		if ghOut["routing-header"] != "x-diverge-env: pr-42" {
			t.Errorf("expected routing-header='x-diverge-env: pr-42', got %q", ghOut["routing-header"])
		}
		if ghOut["routing-header-json"] != `{"x-diverge-env":"pr-42"}` {
			t.Errorf("expected routing-header-json={\"x-diverge-env\":\"pr-42\"}, got %q", ghOut["routing-header-json"])
		}
		if ghOut["preview-url"] != "https://staging.example.com/?_diverge_env=pr-42" {
			t.Errorf("expected preview-url='https://staging.example.com/?_diverge_env=pr-42', got %q", ghOut["preview-url"])
		}
		if dotenv["DIVERGE_PREVIEW_URL"] != "https://staging.example.com/?_diverge_env=pr-42" {
			t.Errorf("expected DIVERGE_PREVIEW_URL in dotenv, got %q", dotenv["DIVERGE_PREVIEW_URL"])
		}
	})

	t.Run("deploy_skipped_when_no_services_changed", func(t *testing.T) {
		_, ghOut, dotenv := runScript(map[string]string{
			"INPUT_ACTION":    "deploy",
			"INPUT_BASE_REF":  "HEAD",
			"INPUT_PR_NUMBER": "42",
		})
		if ghOut["skipped"] != "true" {
			t.Errorf("expected skipped=true when comparing HEAD to HEAD, got %q", ghOut["skipped"])
		}
		if ghOut["changed-services"] != "[]" {
			t.Errorf("expected changed-services=[], got %q", ghOut["changed-services"])
		}
		if ghOut["changed-count"] != "0" {
			t.Errorf("expected changed-count=0, got %q", ghOut["changed-count"])
		}
		if dotenv["DIVERGE_SKIPPED"] != "true" {
			t.Errorf("expected DIVERGE_SKIPPED=true in dotenv, got %q", dotenv["DIVERGE_SKIPPED"])
		}
	})

	t.Run("teardown", func(t *testing.T) {
		_, ghOut, _ := runScript(map[string]string{
			"INPUT_ACTION":    "teardown",
			"INPUT_PR_NUMBER": "42",
			"INPUT_NAMESPACE": "staging",
		})
		if ghOut["preview-name"] != "pr-42" {
			t.Errorf("expected preview-name=pr-42, got %q", ghOut["preview-name"])
		}
		logBytes, err := os.ReadFile(kubectlLog)
		if err != nil {
			t.Fatalf("failed to read kubectl log: %v", err)
		}
		logStr := string(logBytes)
		if !strings.Contains(logStr, "delete previewgroup pr-42 -n staging --ignore-not-found") {
			t.Errorf("expected kubectl delete previewgroup pr-42 -n staging in log, got:\n%s", logStr)
		}
		if !strings.Contains(logStr, "delete environment pr-42 -n staging --ignore-not-found") {
			t.Errorf("expected kubectl delete environment pr-42 in log, got:\n%s", logStr)
		}
	})
}

func parseKeyValueFile(path string) map[string]string {
	res := make(map[string]string)
	data, err := os.ReadFile(path)
	if err != nil {
		return res
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		idx := strings.IndexByte(line, '=')
		if idx <= 0 {
			continue
		}
		res[line[:idx]] = line[idx+1:]
	}
	return res
}

func TestVisualAssetsAndDemoTapes(t *testing.T) {
	svgFiles := []string{
		filepath.Join("..", "docs", "assets", "architecture-overview.svg"),
		filepath.Join("..", "docs", "assets", "delta-vs-duplicated.svg"),
		filepath.Join("..", "docs", "assets", "terminal-quickstart-demo.svg"),
	}
	for _, svgPath := range svgFiles {
		data, err := os.ReadFile(svgPath)
		if err != nil {
			t.Fatalf("failed to read %s: %v", svgPath, err)
		}
		dec := xml.NewDecoder(bytes.NewReader(data))
		foundRootSVG := false
		for {
			tok, err := dec.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("invalid XML in %s: %v", svgPath, err)
			}
			if se, ok := tok.(xml.StartElement); ok && !foundRootSVG {
				if se.Name.Local != "svg" {
					t.Fatalf("expected root <svg> element in %s, got <%s>", svgPath, se.Name.Local)
				}
				foundRootSVG = true
			}
		}
		if !foundRootSVG {
			t.Errorf("no <svg> root element found in %s", svgPath)
		}
	}

	tapeFiles := []string{
		filepath.Join("..", "docs", "demos", "quickstart.tape"),
		filepath.Join("..", "docs", "demos", "trace-and-diff.tape"),
		filepath.Join("..", "docs", "demos", "agent-sandbox.tape"),
	}
	for _, tapePath := range tapeFiles {
		data, err := os.ReadFile(tapePath)
		if err != nil {
			t.Fatalf("failed to read %s: %v", tapePath, err)
		}
		content := string(data)
		if !strings.Contains(content, "Output ") {
			t.Errorf("expected Output directive in %s", tapePath)
		}
		if !strings.Contains(content, "Set Width ") || !strings.Contains(content, "Set Height ") {
			t.Errorf("expected Set Width and Set Height directives in %s", tapePath)
		}
	}
}
