package secrets

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileResolver_Resolve_Success(t *testing.T) {
	err := os.WriteFile("test_secret.txt", []byte("file-secret\n"), 0o600)
	require.NoError(t, err)
	defer func() { _ = os.Remove("test_secret.txt") }()

	r := &FileResolver{}
	val, err := r.Resolve(context.Background(), SecretRef{Path: "test_secret.txt"})
	require.NoError(t, err)
	assert.Equal(t, "file-secret", val)
}

func TestFileResolver_Resolve_NotFound(t *testing.T) {
	r := &FileResolver{}
	_, err := r.Resolve(context.Background(), SecretRef{Path: "nonexistent/path/secret"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to stat file")
}

func TestFileResolver_Resolve_PathTraversal(t *testing.T) {
	r := &FileResolver{}
	_, err := r.Resolve(context.Background(), SecretRef{Path: "etc/../etc/passwd"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "path traversal")
}

func TestFileResolver_Resolve_AbsolutePath(t *testing.T) {
	r := &FileResolver{}
	_, err := r.Resolve(context.Background(), SecretRef{Path: "/etc/passwd"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "absolute paths not allowed")
}
