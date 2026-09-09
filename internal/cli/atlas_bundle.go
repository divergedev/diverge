package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/divergedev/diverge/internal/config"
)

const (
	// maxAtlasConfigMapPayloadBytes is the 800 KiB ceiling to avoid etcd 1MB limits.
	maxAtlasConfigMapPayloadBytes = 800 * 1024

	// LabelAtlasMode identifies the Atlas mode ("versioned" or "declarative").
	LabelAtlasMode = "divergedev.com/atlas-mode"
)

var (
	// allowedAtlasExtensions defines the safe file extensions permitted in a migration bundle.
	allowedAtlasExtensions = map[string]bool{
		".sql":  true,
		".hcl":  true,
		".sum":  true,
		".json": true,
		".yaml": true,
		".yml":  true,
	}

	// sensitivePatterns flag files that must never be bundled into a public ConfigMap.
	sensitivePatterns = []string{
		".env", ".key", ".pem", ".crt", ".pfx", "id_rsa", "credentials", "secret",
	}
)

// cleanPathInput normalizes a path input, stripping any "file://" prefix and cleaning separators.
func cleanPathInput(input string) string {
	cleaned := strings.TrimPrefix(input, "file://")
	return filepath.Clean(cleaned)
}

// validatePathWithinRoot ensures that targetPath resolves within rootDir and does not escape via symlinks.
func validatePathWithinRoot(rootDir, targetPath string) (string, error) {
	absRoot, err := filepath.Abs(rootDir)
	if err != nil {
		return "", fmt.Errorf("failed to resolve root directory: %w", err)
	}

	absTarget := targetPath
	if !filepath.IsAbs(targetPath) {
		absTarget = filepath.Join(absRoot, targetPath)
	}
	absTarget = filepath.Clean(absTarget)

	// Check clean path relative to root first to catch directory traversal
	relToRoot, err := filepath.Rel(absRoot, absTarget)
	if err != nil || strings.HasPrefix(relToRoot, "..") || relToRoot == ".." {
		return "", fmt.Errorf("path %q escapes workspace boundary %q", targetPath, rootDir)
	}

	// Evaluate symlinks if file exists
	evalTarget, err := filepath.EvalSymlinks(absTarget)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("path does not exist: %s", targetPath)
		}
		return "", fmt.Errorf("failed to evaluate path symlinks: %w", err)
	}

	evalRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		evalRoot = absRoot
	}

	rel, err := filepath.Rel(evalRoot, evalTarget)
	if err != nil || strings.HasPrefix(rel, "..") || rel == ".." {
		return "", fmt.Errorf("path %q escapes workspace boundary %q", targetPath, rootDir)
	}

	return evalTarget, nil
}

// isSensitiveFile checks if a file name matches patterns commonly associated with credentials.
func isSensitiveFile(filename string) bool {
	lower := strings.ToLower(filename)
	for _, pattern := range sensitivePatterns {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}

// BundleAtlasConfigMap bundles a local migration directory or schema file into an ephemeral ConfigMap.
func BundleAtlasConfigMap(ctx context.Context, kubeClient client.Client, namespace, envName, baseDir string, atlasCfg *config.AtlasSettings, dryRun bool) (*corev1.ConfigMap, string, error) {
	if atlasCfg == nil {
		return nil, "", nil
	}

	if atlasCfg.Dir == "" && atlasCfg.Schema == "" {
		// Nothing local to bundle
		return nil, "", nil
	}

	if baseDir == "" {
		var err error
		baseDir, err = os.Getwd()
		if err != nil {
			return nil, "", fmt.Errorf("failed to determine working directory: %w", err)
		}
	}

	mode := atlasCfg.Mode
	if mode == "" {
		if atlasCfg.Schema != "" {
			mode = "declarative"
		} else {
			mode = "versioned"
		}
	}

	cmData := make(map[string]string)
	var totalSize int

	switch mode {
	case "versioned":
		if atlasCfg.Dir == "" {
			return nil, "", fmt.Errorf("versioned atlas mode requires dir to bundle")
		}
		dirPath := cleanPathInput(atlasCfg.Dir)
		resolvedDir, err := validatePathWithinRoot(baseDir, dirPath)
		if err != nil {
			return nil, "", fmt.Errorf("invalid atlas migration dir: %w", err)
		}

		info, err := os.Stat(resolvedDir)
		if err != nil {
			return nil, "", fmt.Errorf("failed to access atlas migration dir: %w", err)
		}
		if !info.IsDir() {
			return nil, "", fmt.Errorf("atlas migration dir %q is not a directory", dirPath)
		}

		err = filepath.WalkDir(resolvedDir, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				if d.Name() == ".git" || d.Name() == "node_modules" || d.Name() == ".diverge" {
					return filepath.SkipDir
				}
				return nil
			}

			fileName := d.Name()
			if strings.HasPrefix(fileName, ".") && fileName != ".sum" {
				return nil
			}

			if isSensitiveFile(fileName) {
				return fmt.Errorf("refusing to bundle sensitive file in atlas migrations: %s", fileName)
			}

			ext := strings.ToLower(filepath.Ext(fileName))
			if fileName == "atlas.sum" {
				ext = ".sum"
			}
			if !allowedAtlasExtensions[ext] {
				return nil
			}

			relPath, err := filepath.Rel(resolvedDir, path)
			if err != nil {
				return err
			}
			key := filepath.Base(relPath)

			content, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("failed to read migration file %s: %w", relPath, err)
			}

			totalSize += len(content)
			if totalSize > maxAtlasConfigMapPayloadBytes {
				return fmt.Errorf("atlas migration payload exceeds %d KiB limit (current: %d KiB). Exclude large seed datasets", maxAtlasConfigMapPayloadBytes/1024, totalSize/1024)
			}

			cmData[key] = string(content)
			return nil
		})
		if err != nil {
			return nil, "", err
		}

		if len(cmData) == 0 {
			return nil, "", fmt.Errorf("no valid migration files (.sql, .hcl, atlas.sum) found in %s", atlasCfg.Dir)
		}

	case "declarative":
		if atlasCfg.Schema == "" {
			return nil, "", fmt.Errorf("declarative atlas mode requires schema to bundle")
		}
		filePath := cleanPathInput(atlasCfg.Schema)
		resolvedFile, err := validatePathWithinRoot(baseDir, filePath)
		if err != nil {
			return nil, "", fmt.Errorf("invalid atlas schema file: %w", err)
		}

		info, err := os.Stat(resolvedFile)
		if err != nil {
			return nil, "", fmt.Errorf("failed to access atlas schema file: %w", err)
		}
		if info.IsDir() {
			return nil, "", fmt.Errorf("atlas schema %q is a directory, expected file", filePath)
		}

		baseName := filepath.Base(resolvedFile)
		if isSensitiveFile(baseName) {
			return nil, "", fmt.Errorf("refusing to bundle sensitive file in atlas schema: %s", baseName)
		}

		ext := strings.ToLower(filepath.Ext(baseName))
		if ext != ".sql" && ext != ".hcl" {
			return nil, "", fmt.Errorf("unsupported atlas schema extension %q: must be .sql or .hcl", ext)
		}

		content, err := os.ReadFile(resolvedFile)
		if err != nil {
			return nil, "", fmt.Errorf("failed to read schema file %s: %w", resolvedFile, err)
		}

		totalSize = len(content)
		if totalSize > maxAtlasConfigMapPayloadBytes {
			return nil, "", fmt.Errorf("atlas schema payload exceeds %d KiB limit", maxAtlasConfigMapPayloadBytes/1024)
		}

		key := "schema.sql"
		if ext == ".hcl" {
			key = "schema.hcl"
		}
		cmData[key] = string(content)

	default:
		return nil, "", fmt.Errorf("unsupported atlas mode %q", mode)
	}

	// Deterministic content hash across all keys
	keys := make([]string, 0, len(cmData))
	for k := range cmData {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	h := sha256.New()
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte{0})
		h.Write([]byte(cmData[k]))
		h.Write([]byte{0})
	}
	hash8 := hex.EncodeToString(h.Sum(nil))[:8]

	kindPrefix := "mig"
	if mode == "declarative" {
		kindPrefix = "schema"
	}
	cmName := fmt.Sprintf("atlas-%s-%s-%s", envName, kindPrefix, hash8)
	if len(cmName) > 63 {
		allowedEnvLen := 63 - len(fmt.Sprintf("atlas--%s-%s", kindPrefix, hash8))
		if allowedEnvLen > 0 && len(envName) > allowedEnvLen {
			cmName = fmt.Sprintf("atlas-%s-%s-%s", envName[:allowedEnvLen], kindPrefix, hash8)
		}
	}

	cm := &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ConfigMap",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      cmName,
			Namespace: namespace,
			Labels: map[string]string{
				"divergedev.com/environment": envName,
				"divergedev.com/managed-by":  "diverge",
				LabelAtlasMode:               mode,
			},
		},
		Data: cmData,
	}

	if !dryRun && kubeClient != nil {
		var existing corev1.ConfigMap
		err := kubeClient.Get(ctx, types.NamespacedName{Name: cmName, Namespace: namespace}, &existing)
		if err != nil {
			if apierrors.IsNotFound(err) {
				if err := kubeClient.Create(ctx, cm); err != nil {
					return nil, "", fmt.Errorf("failed to create atlas configmap %s: %w", cmName, err)
				}
			} else {
				return nil, "", fmt.Errorf("failed to check existing atlas configmap %s: %w", cmName, err)
			}
		} else {
			existing.Data = cm.Data
			if existing.Labels == nil {
				existing.Labels = make(map[string]string)
			}
			for k, v := range cm.Labels {
				existing.Labels[k] = v
			}
			if err := kubeClient.Update(ctx, &existing); err != nil {
				return nil, "", fmt.Errorf("failed to update atlas configmap %s: %w", cmName, err)
			}
			cm = &existing
		}
	}

	return cm, cmName, nil
}
