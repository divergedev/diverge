package secrets

import (
	"context"
	"fmt"
	"os"
	"strings"
)

const maxFileSize = 1024 * 1024 // 1MB

type FileResolver struct{}

func NewFileResolver() *FileResolver {
	return &FileResolver{}
}

func (r *FileResolver) Resolve(ctx context.Context, ref SecretRef) (string, error) {
	if strings.Contains(ref.Path, "..") {
		return "", fmt.Errorf("path traversal not allowed: %q", ref.Path)
	}

	info, err := os.Stat(ref.Path)
	if err != nil {
		return "", fmt.Errorf("failed to stat file %q: %w", ref.Path, err)
	}

	if info.Size() > maxFileSize {
		return "", fmt.Errorf("file %q is too large (size: %d, max: %d)", ref.Path, info.Size(), maxFileSize)
	}

	data, err := os.ReadFile(ref.Path)
	if err != nil {
		return "", fmt.Errorf("failed to read file %q: %w", ref.Path, err)
	}

	return strings.TrimRight(string(data), "\n"), nil
}
