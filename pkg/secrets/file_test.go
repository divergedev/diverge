package secrets

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileResolver_Resolve_Success(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.txt")
	require.NoError(t, os.WriteFile(path, []byte("file-secret\n"), 0o600))

	r := &FileResolver{}
	val, err := r.Resolve(context.Background(), SecretRef{Path: path})
	require.NoError(t, err)
	assert.Equal(t, "file-secret", val)
}

func TestFileResolver_Resolve_NotFound(t *testing.T) {
	r := &FileResolver{}
	_, err := r.Resolve(context.Background(), SecretRef{Path: "/nonexistent/path/secret"})
	assert.Error(t, err)
}

func TestFileResolver_Resolve_PathTraversal(t *testing.T) {
	r := &FileResolver{}
	_, err := r.Resolve(context.Background(), SecretRef{Path: "/etc/../etc/passwd"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "path traversal")
}
