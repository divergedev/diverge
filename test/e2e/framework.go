//go:build e2e
package e2e

import (
	"context"
)

type Framework struct {
	// ...
}

func NewFramework() *Framework {
	return &Framework{}
}

func (f *Framework) WaitForCondition(ctx context.Context) error {
	return nil
}
