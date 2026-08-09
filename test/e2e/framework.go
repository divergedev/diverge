//go:build e2e

// Package e2e provides a test framework for end-to-end testing of the
// Diverge controller and environment lifecycle.
package e2e

import (
	"context"
)

// Framework provides helpers for end-to-end tests, including Kubernetes
// client setup, environment creation, and condition polling.
type Framework struct {
	// ...
}

// NewFramework creates a new end-to-end test Framework with default
// configuration. Call from TestMain or individual test setup.
func NewFramework() *Framework {
	return &Framework{}
}

// WaitForCondition is a stub that will poll until the expected condition is
// met or the context is cancelled. Currently returns nil immediately.
func (f *Framework) WaitForCondition(ctx context.Context) error {
	return nil
}
