package temporal

import (
	"fmt"
	"os"
)

// TaskQueueOption configures TaskQueue behavior.
type TaskQueueOption func(*taskQueueConfig)

type taskQueueConfig struct {
	global bool
}

// Global marks a task queue as global — it will NOT be scoped to preview environments.
// Use this for shared/background workflows (billing, cron) that should always run in production.
func Global() TaskQueueOption {
	return func(c *taskQueueConfig) {
		c.global = true
	}
}

// TaskQueue returns the environment-scoped task queue name.
// If DIVERGE_ENV is empty (production) or Global() is passed, returns the base name unchanged.
func TaskQueue(base string, opts ...TaskQueueOption) string {
	cfg := &taskQueueConfig{}
	for _, opt := range opts {
		if opt != nil {
			opt(cfg)
		}
	}
	if cfg.global {
		return base
	}
	env := os.Getenv("DIVERGE_ENV")
	if env == "" {
		return base
	}
	return fmt.Sprintf("%s-%s", base, env)
}
