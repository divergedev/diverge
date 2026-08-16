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

// KafkaAdmin abstracts the Kafka admin operations for testability.
type KafkaAdmin interface {
	CreateTopics(ctx context.Context, partitions int32, replication int16, configs map[string]*string, topics ...string) (kadm.CreateTopicResponses, error)
	DeleteTopics(ctx context.Context, topics ...string) (kadm.DeleteTopicResponses, error)
	Close()
}

// KafkaAdminFactory creates KafkaAdmin instances. Defaults to real kadm.Client.
type KafkaAdminFactory func(brokers []string) (KafkaAdmin, error)

func defaultAdminFactory(brokers []string) (KafkaAdmin, error) {
	client, err := kgo.NewClient(kgo.SeedBrokers(brokers...))
	if err != nil {
		return nil, err
	}
	admin := kadm.NewClient(client)
	return &realKafkaAdmin{admin: admin, client: client}, nil
}

type realKafkaAdmin struct {
	admin  *kadm.Client
	client *kgo.Client
}

func (r *realKafkaAdmin) CreateTopics(ctx context.Context, partitions int32, replication int16, configs map[string]*string, topics ...string) (kadm.CreateTopicResponses, error) {
	return r.admin.CreateTopics(ctx, partitions, replication, configs, topics...)
}

func (r *realKafkaAdmin) DeleteTopics(ctx context.Context, topics ...string) (kadm.DeleteTopicResponses, error) {
	return r.admin.DeleteTopics(ctx, topics...)
}

func (r *realKafkaAdmin) Close() {
	r.admin.Close()
	r.client.Close()
}

// KafkaProvisioner provisions Kafka topics and consumer groups for preview environments
// using the Kafka AdminClient API. Compatible with Apache Kafka, AutoMQ, Redpanda,
// MSK, and any Kafka-protocol broker.
type KafkaProvisioner struct {
	Brokers           []string
	NumPartitions     int32
	ReplicationFactor int16
	AdminFactory      KafkaAdminFactory // nil = use real kadm
}

func (k *KafkaProvisioner) getAdmin() (KafkaAdmin, error) {
	factory := k.AdminFactory
	if factory == nil {
		factory = defaultAdminFactory
	}
	return factory(k.Brokers)
}

// Name returns the provisioner name.
func (k *KafkaProvisioner) Name() string { return "kafka" }

func (k *KafkaProvisioner) Provision(ctx context.Context, env *v1alpha1.Environment, route v1alpha1.AsyncRouteSpec) (*ProvisionResult, error) {
	target := fmt.Sprintf("%s--%s", route.Target, env.Name)

	admin, err := k.getAdmin()
	if err != nil {
		return nil, fmt.Errorf("kafka client error: %w", err)
	}
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
	target := fmt.Sprintf("%s--%s", route.Target, env.Name)

	admin, err := k.getAdmin()
	if err != nil {
		return fmt.Errorf("kafka client error: %w", err)
	}
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
