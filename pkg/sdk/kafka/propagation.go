package kafka

import (
	"fmt"
	"os"
)

const HeaderKey = "x-diverge-env"

// Headers returns Kafka record headers with the diverge environment injected.
// Use with any Kafka client library (franz-go, confluent, sarama).
func Headers() map[string]string {
	headers := make(map[string]string)
	if env := os.Getenv("DIVERGE_ENV"); env != "" {
		headers[HeaderKey] = env
	}
	return headers
}

// Topic returns the environment-scoped topic name.
// If DIVERGE_ENV is empty (production), returns the base name unchanged.
func Topic(base string) string {
	env := os.Getenv("DIVERGE_ENV")
	if env == "" {
		return base
	}
	return fmt.Sprintf("%s-%s", base, env)
}

// ConsumerGroup returns the environment-scoped consumer group.
func ConsumerGroup(base string) string {
	env := os.Getenv("DIVERGE_ENV")
	if env == "" {
		return base
	}
	return fmt.Sprintf("%s-%s", base, env)
}
