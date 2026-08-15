//go:build !no_kafka

package async

import (
	"context"
	"fmt"
	"strings"

	v1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
)

// KafkaProvisioner provisions Kafka topics and consumer groups for preview environments
// using the Kafka AdminClient API. Compatible with Apache Kafka, AutoMQ, Redpanda,
// MSK, and any Kafka-protocol broker.
type KafkaProvisioner struct {
	Brokers           []string
	NumPartitions     int32
	ReplicationFactor int16
}

// Name returns the provisioner name.
func (k *KafkaProvisioner) Name() string { return "kafka" }

// Provision creates a preview-scoped Kafka topic and/or consumer group.
func (k *KafkaProvisioner) Provision(ctx context.Context, env *v1alpha1.Environment, route v1alpha1.AsyncRouteSpec) (*ProvisionResult, error) {
	target := fmt.Sprintf("%s-%s", route.Target, env.Name)

	client, err := kgo.NewClient(kgo.SeedBrokers(k.Brokers...))
	if err != nil {
		return nil, fmt.Errorf("kafka client error: %w", err)
	}
	defer client.Close()

	admin := kadm.NewClient(client)
	defer admin.Close()

	// Create topic with preview-specific name
	resp, err := admin.CreateTopics(ctx, k.NumPartitions, k.ReplicationFactor, nil, target)
	if err != nil {
		return nil, fmt.Errorf("kafka create topic failed: %w", err)
	}
	for _, t := range resp.Sorted() {
		if t.Err != nil {
			// TopicAlreadyExists is not an error (idempotent)
			if strings.Contains(t.Err.Error(), "already exists") {
				continue
			}
			return nil, fmt.Errorf("kafka topic %q: %w", t.Topic, t.Err)
		}
	}

	envVars := make(map[string]string)
	if len(route.EnvVarMapping) == 0 {
		envVars["KAFKA_TOPIC"] = target
		envVars["KAFKA_CONSUMER_GROUP"] = target
		envVars["KAFKA_BROKERS"] = joinBrokers(k.Brokers)
	} else {
		for envVar, tmpl := range route.EnvVarMapping {
			if tmpl == "{{ .ResolvedTarget }}" || tmpl == "" {
				envVars[envVar] = target
			}
		}
	}

	return &ProvisionResult{
		ResolvedTarget: target,
		EnvVars:        envVars,
	}, nil
}

// Teardown deletes the preview-scoped Kafka topic.
func (k *KafkaProvisioner) Teardown(ctx context.Context, env *v1alpha1.Environment, route v1alpha1.AsyncRouteSpec) error {
	target := fmt.Sprintf("%s-%s", route.Target, env.Name)

	client, err := kgo.NewClient(kgo.SeedBrokers(k.Brokers...))
	if err != nil {
		return fmt.Errorf("kafka client error: %w", err)
	}
	defer client.Close()

	admin := kadm.NewClient(client)
	defer admin.Close()

	resp, err := admin.DeleteTopics(ctx, target)
	if err != nil {
		return fmt.Errorf("kafka delete topic failed: %w", err)
	}
	for _, t := range resp.Sorted() {
		if t.Err != nil {
			// Topic not found is not an error (idempotent)
			if strings.Contains(t.Err.Error(), "does not host this topic-partition") || strings.Contains(t.Err.Error(), "UnknownTopicOrPartition") {
				continue
			}
			return fmt.Errorf("kafka delete topic %q: %w", t.Topic, t.Err)
		}
	}

	return nil
}

func joinBrokers(brokers []string) string {
	result := ""
	for i, b := range brokers {
		if i > 0 {
			result += ","
		}
		result += b
	}
	return result
}
