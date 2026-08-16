package temporal

import (
	"fmt"
	"os"
)

// TaskQueue returns the environment-scoped task queue name.
// If env is empty (production), returns the base name unchanged.
func TaskQueue(base string) string {
	env := os.Getenv("DIVERGE_ENV")
	if env == "" {
		return base
	}
	return fmt.Sprintf("%s-%s", base, env)
}
