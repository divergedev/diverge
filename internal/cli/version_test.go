package cli

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVersionCmd(t *testing.T) {
	app := &App{Version: "1.2.3", Commit: "abc123", Date: "2024-01-01"}
	cmd := newVersionCmd(app)

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{})

	err := cmd.Execute()
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "1.2.3")
	assert.Contains(t, out, "abc123")
	assert.Contains(t, out, "2024-01-01")
}
