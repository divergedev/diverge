package cli

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/protocgen/proto2mcp/pkg/mcpruntime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleVisualTest_ValidationAndErrors(t *testing.T) {
	ctx := context.Background()

	// 1. Invalid JSON arguments
	_, err := handleVisualTest(ctx, mcpruntime.ToolRequest{
		ToolName:  "diverge_visual_test",
		Arguments: json.RawMessage(`{invalid`),
	})
	require.Error(t, err)

	// 2. Missing both file paths and directory paths
	res, err := handleVisualTest(ctx, mcpruntime.ToolRequest{
		ToolName:  "diverge_visual_test",
		Arguments: json.RawMessage(`{}`),
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.IsError)

	// 3. Only baseline_dir provided without preview_dir
	res, err = handleVisualTest(ctx, mcpruntime.ToolRequest{
		ToolName:  "diverge_visual_test",
		Arguments: json.RawMessage(`{"baseline_dir": "/tmp/base"}`),
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.IsError)

	// 4. Empty baseline directory
	emptyBase := t.TempDir()
	emptyPrev := t.TempDir()
	dirArgs, _ := json.Marshal(map[string]string{
		"baseline_dir": emptyBase,
		"preview_dir":  emptyPrev,
	})
	res, err = handleVisualTest(ctx, mcpruntime.ToolRequest{
		ToolName:  "diverge_visual_test",
		Arguments: dirArgs,
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.IsError)

	// 5. Nonexistent baseline file
	fileArgs, _ := json.Marshal(map[string]string{
		"baseline_path": filepath.Join(emptyBase, "missing.png"),
		"preview_path":  filepath.Join(emptyPrev, "missing.png"),
	})
	res, err = handleVisualTest(ctx, mcpruntime.ToolRequest{
		ToolName:  "diverge_visual_test",
		Arguments: fileArgs,
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.IsError)
}
