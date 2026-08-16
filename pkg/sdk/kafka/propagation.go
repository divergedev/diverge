package kafka

import (
	"errors"
	"fmt"
	"os"
	"strings"
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

var ErrInvalidTopic = errors.New("topic name must not contain '--' delimiter")

// Topic returns the preview-scoped Kafka topic name.
// Uses "--" as delimiter to avoid collision with topics containing single hyphens.
func Topic(baseTopic, envName string) (string, error) {
	if envName == "" {
		return baseTopic, nil
	}
	if strings.Contains(baseTopic, "--") {
		return "", ErrInvalidTopic
	}
	return fmt.Sprintf("%s--%s", baseTopic, envName), nil
}

// ConsumerGroup returns the environment-scoped consumer group.
func ConsumerGroup(base string) string {
	env := os.Getenv("DIVERGE_ENV")
	if env == "" {
		return base
	}
	return fmt.Sprintf("%s-%s", base, env)
}
