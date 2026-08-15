package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnvExport_DotenvFormat(t *testing.T) {
	env := map[string]string{
		"FOO": "bar",
		"BAZ": "qux",
	}
	out, err := formatDotenv(env)
	require.NoError(t, err)
	expected := "BAZ=qux\nFOO=bar\n"
	assert.Equal(t, expected, out)
}

func TestEnvExport_JSONFormat(t *testing.T) {
	env := map[string]string{
		"FOO": "bar",
		"BAZ": "qux",
	}
	out, err := formatJSON(env)
	require.NoError(t, err)
	expected := "{\n  \"BAZ\": \"qux\",\n  \"FOO\": \"bar\"\n}\n"
	assert.Equal(t, expected, out)
}

func TestEnvExport_ShellFormat(t *testing.T) {
	env := map[string]string{
		"FOO": "bar",
		"BAZ": "qux",
	}
	out, err := formatShell(env)
	require.NoError(t, err)
	expected := "export BAZ=\"qux\"\nexport FOO=\"bar\"\n"
	assert.Equal(t, expected, out)
}

func TestEnvExport_QuotedValues(t *testing.T) {
	env := map[string]string{
		"SPACE": "has space",
		"QUOTE": `has "quote"`,
		"NEWLN": "has\nnewline",
	}
	out, err := formatDotenv(env)
	require.NoError(t, err)
	assert.Contains(t, out, `NEWLN="has\nnewline"`)
	assert.Contains(t, out, `QUOTE="has \"quote\""`)
	assert.Contains(t, out, `SPACE="has space"`)
}

func TestEnvExport_EmptyEnv(t *testing.T) {
	env := map[string]string{}
	outDotenv, _ := formatDotenv(env)
	assert.Empty(t, outDotenv)

	outJSON, _ := formatJSON(env)
	assert.Equal(t, "{}\n", outJSON)

	outShell, _ := formatShell(env)
	assert.Empty(t, outShell)
}
