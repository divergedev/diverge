package secrets

import (
	"context"
	"fmt"
	"os"
)

type EnvResolver struct{}

func NewEnvResolver() *EnvResolver {
	return &EnvResolver{}
}

func (r *EnvResolver) Resolve(ctx context.Context, ref SecretRef) (string, error) {
	val := os.Getenv(ref.Path)
	if val == "" {
		return "", fmt.Errorf("environment variable %q is empty or not set", ref.Path)
	}
	return val, nil
}
