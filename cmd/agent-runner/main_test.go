package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRun_MissingEnvVars(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), nil, func(string) string { return "" }, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("expected non-zero exit code when required env vars are missing, got 0")
	}
	if !strings.Contains(stderr.String(), "DIVERGE_TASK_NAME is required") {
		t.Errorf("expected stderr to mention missing DIVERGE_TASK_NAME, got: %q", stderr.String())
	}
}

func TestRun_HelpAndVersionFlags(t *testing.T) {
	t.Run("--help", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run(context.Background(), []string{"--help"}, func(string) string { return "" }, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("expected exit code 0 for --help, got %d (stderr: %s)", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "Usage of diverge-agent-runner") {
			t.Errorf("expected help output in stdout, got: %q", stdout.String())
		}
	})

	t.Run("--version", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := run(context.Background(), []string{"--version"}, func(string) string { return "" }, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("expected exit code 0 for --version, got %d (stderr: %s)", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "diverge-agent-runner version") {
			t.Errorf("expected version output in stdout, got: %q", stdout.String())
		}
	})
}
