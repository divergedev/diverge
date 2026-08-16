package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/divergedev/diverge/internal/config"
)

func TestExampleConfigsAreValid(t *testing.T) {
	err := filepath.Walk("../../examples", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(info.Name(), ".diverge.yaml") {
			t.Run(path, func(t *testing.T) {
				_, loadErr := config.Load(path)
				if loadErr != nil {
					t.Fatalf("failed to parse example config %s: %v", path, loadErr)
				}
			})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("failed to walk examples dir: %v", err)
	}
}
