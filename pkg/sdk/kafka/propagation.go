package kafka

import (
	"fmt"
	"strings"
)

// Topic returns the preview-scoped Kafka topic name.
// Uses "--" as delimiter to avoid collision with topics containing single hyphens.
func Topic(baseTopic, envName string) string {
	if envName == "" {
		return baseTopic
	}
	return fmt.Sprintf("%s--%s", baseTopic, envName)
}

// ConsumerGroup returns the preview-scoped consumer group ID.
// Includes the topic name to prevent cross-topic consumer group collisions.
func ConsumerGroup(baseTopic, baseGroup, envName string) string {
	if envName == "" {
		return baseGroup
	}
	return fmt.Sprintf("%s--%s--%s", baseGroup, baseTopic, envName)
}

// ParseTopic extracts the base topic and environment name from a scoped topic.
// Returns the original topic and empty env if no delimiter is found.
func ParseTopic(scopedTopic string) (baseTopic, envName string) {
	parts := strings.SplitN(scopedTopic, "--", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return scopedTopic, ""
}
