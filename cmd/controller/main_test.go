package main

import (
	"testing"
)

// TestMainCompiles is a build canary — ensures main.go compiles
// and all wired dependencies resolve. The actual binary is tested
// via the pre-push go-build hook and e2e tests.
func TestMainCompiles(t *testing.T) {
	// This test exists to ensure this package has test coverage.
	// The main() function requires a running Kubernetes cluster,
	// so we only verify compilation here.
	t.Log("cmd/controller compiles successfully")
}
