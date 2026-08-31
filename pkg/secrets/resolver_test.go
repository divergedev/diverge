package secrets

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnvResolver_Success(t *testing.T) {
	t.Setenv("TEST_ENV_VAR", "mysecret")
	r := NewEnvResolver()
	val, err := r.Resolve(context.Background(), SecretRef{Path: "TEST_ENV_VAR"})
	require.NoError(t, err)
	assert.Equal(t, "mysecret", val)
}

func TestEnvResolver_Empty(t *testing.T) {
	r := NewEnvResolver()
	_, err := r.Resolve(context.Background(), SecretRef{Path: "NON_EXISTENT_VAR"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty or not set")
}

func TestFileResolver_Success(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.txt")
	require.NoError(t, os.WriteFile(path, []byte("file-secret\n"), 0600))

	r := NewFileResolver()
	val, err := r.Resolve(context.Background(), SecretRef{Path: path})
	require.NoError(t, err)
	assert.Equal(t, "file-secret", val)
}

func TestFileResolver_NotFound(t *testing.T) {
	r := NewFileResolver()
	_, err := r.Resolve(context.Background(), SecretRef{Path: "/does/not/exist.txt"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to stat file")
}

func TestFileResolver_TooLarge(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "large.txt")
	// Make a file exactly maxFileSize + 1
	f, err := os.Create(path)
	require.NoError(t, err)
	err = f.Truncate(maxFileSize + 1)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	r := NewFileResolver()
	_, err = r.Resolve(context.Background(), SecretRef{Path: path})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "too large")
}

func TestFileResolver_PathTraversal(t *testing.T) {
	r := NewFileResolver()
	_, err := r.Resolve(context.Background(), SecretRef{Path: "/etc/../etc/passwd"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "path traversal not allowed")
}

func TestMulti_RoutesToCorrectResolver(t *testing.T) {
	t.Setenv("MULTI_ENV", "multi-val")
	m := NewMulti(map[string]Resolver{
		"env": NewEnvResolver(),
	})
	val, err := m.Resolve(context.Background(), SecretRef{Provider: "env", Path: "MULTI_ENV"})
	require.NoError(t, err)
	assert.Equal(t, "multi-val", val)
}

func TestMulti_UnknownProvider(t *testing.T) {
	m := NewMulti(map[string]Resolver{})
	_, err := m.Resolve(context.Background(), SecretRef{Provider: "nope"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown secret provider")
}
