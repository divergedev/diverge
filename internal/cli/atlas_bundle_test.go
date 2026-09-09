package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/divergedev/diverge/internal/config"
)

func TestBundleAtlasConfigMap_Versioned(t *testing.T) {
	tempDir := t.TempDir()
	migDir := filepath.Join(tempDir, "migrations")
	require.NoError(t, os.MkdirAll(migDir, 0755))

	initSQL := "CREATE TABLE users (id serial primary key, name text);"
	addColSQL := "ALTER TABLE users ADD COLUMN email text;"
	atlasSum := "h1:samplehash123="

	require.NoError(t, os.WriteFile(filepath.Join(migDir, "20240101_init.sql"), []byte(initSQL), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(migDir, "20240102_add_email.sql"), []byte(addColSQL), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(migDir, "atlas.sum"), []byte(atlasSum), 0644))

	atlasCfg := &config.AtlasSettings{
		Mode: "versioned",
		Dir:  "./migrations",
	}

	cm, cmName, err := BundleAtlasConfigMap(context.Background(), nil, "preview-ns", "pr-123", tempDir, atlasCfg, true)
	require.NoError(t, err)
	require.NotNil(t, cm)
	assert.Contains(t, cmName, "atlas-pr-123-mig-")
	assert.Equal(t, cmName, cm.Name)
	assert.Equal(t, "preview-ns", cm.Namespace)
	assert.Equal(t, "pr-123", cm.Labels["divergedev.com/environment"])
	assert.Equal(t, "versioned", cm.Labels[LabelAtlasMode])

	assert.Equal(t, initSQL, cm.Data["20240101_init.sql"])
	assert.Equal(t, addColSQL, cm.Data["20240102_add_email.sql"])
	assert.Equal(t, atlasSum, cm.Data["atlas.sum"])
}

func TestBundleAtlasConfigMap_Declarative(t *testing.T) {
	tempDir := t.TempDir()
	schemaFile := filepath.Join(tempDir, "schema.sql")
	schemaSQL := "CREATE TABLE orders (id serial primary key, amount numeric);"
	require.NoError(t, os.WriteFile(schemaFile, []byte(schemaSQL), 0644))

	atlasCfg := &config.AtlasSettings{
		Mode:   "declarative",
		Schema: "./schema.sql",
	}

	cm, cmName, err := BundleAtlasConfigMap(context.Background(), nil, "preview-ns", "pr-123", tempDir, atlasCfg, true)
	require.NoError(t, err)
	require.NotNil(t, cm)
	assert.Contains(t, cmName, "atlas-pr-123-schema-")
	assert.Equal(t, "declarative", cm.Labels[LabelAtlasMode])
	assert.Equal(t, schemaSQL, cm.Data["schema.sql"])

	// Test HCL file
	hclFile := filepath.Join(tempDir, "schema.hcl")
	hclContent := `table "posts" { schema = schema.public }`
	require.NoError(t, os.WriteFile(hclFile, []byte(hclContent), 0644))

	hclCfg := &config.AtlasSettings{
		Mode:   "declarative",
		Schema: "./schema.hcl",
	}

	cmHCL, _, err := BundleAtlasConfigMap(context.Background(), nil, "preview-ns", "pr-123", tempDir, hclCfg, true)
	require.NoError(t, err)
	assert.Equal(t, hclContent, cmHCL.Data["schema.hcl"])
}

func TestBundleAtlasConfigMap_DeterministicContentHash(t *testing.T) {
	tempDir := t.TempDir()
	migDir := filepath.Join(tempDir, "migrations")
	require.NoError(t, os.MkdirAll(migDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(migDir, "init.sql"), []byte("CREATE TABLE a (id int);"), 0644))

	atlasCfg := &config.AtlasSettings{
		Mode: "versioned",
		Dir:  "migrations",
	}

	_, name1, err := BundleAtlasConfigMap(context.Background(), nil, "default", "env-a", tempDir, atlasCfg, true)
	require.NoError(t, err)

	// Second run without modifications should yield identical name
	_, name2, err := BundleAtlasConfigMap(context.Background(), nil, "default", "env-a", tempDir, atlasCfg, true)
	require.NoError(t, err)
	assert.Equal(t, name1, name2)

	// Modifying file contents must yield different hash name
	require.NoError(t, os.WriteFile(filepath.Join(migDir, "init.sql"), []byte("CREATE TABLE a (id bigint);"), 0644))
	_, name3, err := BundleAtlasConfigMap(context.Background(), nil, "default", "env-a", tempDir, atlasCfg, true)
	require.NoError(t, err)
	assert.NotEqual(t, name1, name3)
}

func TestBundleAtlasConfigMap_Security_PathTraversal(t *testing.T) {
	tempDir := t.TempDir()
	outsideDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outsideDir, "secret.sql"), []byte("SECRET"), 0644))

	atlasCfg := &config.AtlasSettings{
		Mode: "versioned",
		Dir:  "../outside",
	}

	_, _, err := BundleAtlasConfigMap(context.Background(), nil, "default", "test-env", tempDir, atlasCfg, true)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "escapes workspace boundary")
}

func TestBundleAtlasConfigMap_Security_SymlinkOutsideRoot(t *testing.T) {
	tempDir := t.TempDir()
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "escape.sql")
	require.NoError(t, os.WriteFile(outsideFile, []byte("ESCAPED"), 0644))

	symlinkPath := filepath.Join(tempDir, "symlink-schema.sql")
	require.NoError(t, os.Symlink(outsideFile, symlinkPath))

	atlasCfg := &config.AtlasSettings{
		Mode:   "declarative",
		Schema: "symlink-schema.sql",
	}

	_, _, err := BundleAtlasConfigMap(context.Background(), nil, "default", "test-env", tempDir, atlasCfg, true)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "escapes workspace boundary")
}

func TestBundleAtlasConfigMap_Security_FileFilter(t *testing.T) {
	tempDir := t.TempDir()
	migDir := filepath.Join(tempDir, "migrations")
	require.NoError(t, os.MkdirAll(migDir, 0755))

	require.NoError(t, os.WriteFile(filepath.Join(migDir, "01.sql"), []byte("CREATE TABLE x (id int);"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(migDir, "ignore.txt"), []byte("notes"), 0644))

	atlasCfg := &config.AtlasSettings{
		Mode: "versioned",
		Dir:  "migrations",
	}

	cm, _, err := BundleAtlasConfigMap(context.Background(), nil, "default", "test-env", tempDir, atlasCfg, true)
	require.NoError(t, err)
	assert.Contains(t, cm.Data, "01.sql")
	assert.NotContains(t, cm.Data, "ignore.txt")

	// Sensitive file should return error
	require.NoError(t, os.WriteFile(filepath.Join(migDir, "prod.key"), []byte("PRIVATE KEY"), 0644))
	_, _, err = BundleAtlasConfigMap(context.Background(), nil, "default", "test-env", tempDir, atlasCfg, true)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "refusing to bundle sensitive file")
}

func TestBundleAtlasConfigMap_PayloadLimit(t *testing.T) {
	tempDir := t.TempDir()
	migDir := filepath.Join(tempDir, "migrations")
	require.NoError(t, os.MkdirAll(migDir, 0755))

	largePayload := strings.Repeat("A", 850*1024) // 850 KiB
	require.NoError(t, os.WriteFile(filepath.Join(migDir, "big.sql"), []byte(largePayload), 0644))

	atlasCfg := &config.AtlasSettings{
		Mode: "versioned",
		Dir:  "migrations",
	}

	_, _, err := BundleAtlasConfigMap(context.Background(), nil, "default", "test-env", tempDir, atlasCfg, true)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds 800 KiB limit")
}

func TestBundleAtlasConfigMap_KubeClient(t *testing.T) {
	tempDir := t.TempDir()
	migDir := filepath.Join(tempDir, "migrations")
	require.NoError(t, os.MkdirAll(migDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(migDir, "01.sql"), []byte("CREATE TABLE t (id int);"), 0644))

	s := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(s))
	client := fake.NewClientBuilder().WithScheme(s).Build()

	atlasCfg := &config.AtlasSettings{
		Mode: "versioned",
		Dir:  "migrations",
	}

	ctx := context.Background()
	cm, cmName, err := BundleAtlasConfigMap(ctx, client, "diverge-ns", "env-1", tempDir, atlasCfg, false)
	require.NoError(t, err)

	// Verify ConfigMap exists in client
	var fetched corev1.ConfigMap
	require.NoError(t, client.Get(ctx, types.NamespacedName{Namespace: "diverge-ns", Name: cmName}, &fetched))
	assert.Equal(t, cm.Data["01.sql"], fetched.Data["01.sql"])
}
