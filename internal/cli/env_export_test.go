package cli

import (
	"strings"
	"testing"
)

func TestEnvExport_DotenvFormat(t *testing.T) {
	env := map[string]string{
		"FOO": "bar",
		"BAZ": "qux",
	}
	out, err := formatDotenv(env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := "BAZ=qux\nFOO=bar\n"
	if out != expected {
		t.Errorf("expected %q, got %q", expected, out)
	}
}

func TestEnvExport_JSONFormat(t *testing.T) {
	env := map[string]string{
		"FOO": "bar",
		"BAZ": "qux",
	}
	out, err := formatJSON(env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := "{\n  \"BAZ\": \"qux\",\n  \"FOO\": \"bar\"\n}\n"
	if out != expected {
		t.Errorf("expected %q, got %q", expected, out)
	}
}

func TestEnvExport_ShellFormat(t *testing.T) {
	env := map[string]string{
		"FOO": "bar",
		"BAZ": "qux",
	}
	out, err := formatShell(env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := "export BAZ=\"qux\"\nexport FOO=\"bar\"\n"
	if out != expected {
		t.Errorf("expected %q, got %q", expected, out)
	}
}

func TestEnvExport_QuotedValues(t *testing.T) {
	env := map[string]string{
		"SPACE": "has space",
		"QUOTE": `has "quote"`,
		"NEWLN": "has\nnewline",
	}
	out, err := formatDotenv(env)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `NEWLN="has\nnewline"`) {
		t.Errorf("expected newline to be quoted, got %q", out)
	}
	if !strings.Contains(out, `QUOTE="has \"quote\""`) {
		t.Errorf("expected quote to be quoted, got %q", out)
	}
	if !strings.Contains(out, `SPACE="has space"`) {
		t.Errorf("expected space to be quoted, got %q", out)
	}
}

func TestEnvExport_EmptyEnv(t *testing.T) {
	env := map[string]string{}
	outDotenv, _ := formatDotenv(env)
	if outDotenv != "" {
		t.Errorf("expected empty dotenv, got %q", outDotenv)
	}

	outJSON, _ := formatJSON(env)
	if outJSON != "{}\n" {
		t.Errorf("expected empty json, got %q", outJSON)
	}

	outShell, _ := formatShell(env)
	if outShell != "" {
		t.Errorf("expected empty shell, got %q", outShell)
	}
}
