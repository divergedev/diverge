//go:build !no_kafka

package async

import (
	"context"
	"testing"
	"time"

	v1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kadm"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type capturingKafkaAdmin struct {
	lastPartitions  int32
	lastReplication int16
	lastConfigs     map[string]*string
	lastTopics      []string
	closed          bool
}

func (m *capturingKafkaAdmin) CreateTopics(_ context.Context, partitions int32, replication int16, configs map[string]*string, topics ...string) (kadm.CreateTopicResponses, error) {
	m.lastPartitions = partitions
	m.lastReplication = replication
	m.lastConfigs = configs
	m.lastTopics = topics
	resp := make(kadm.CreateTopicResponses)
	for _, t := range topics {
		resp[t] = kadm.CreateTopicResponse{Topic: t}
	}
	return resp, nil
}

func (m *capturingKafkaAdmin) DeleteTopics(_ context.Context, topics ...string) (kadm.DeleteTopicResponses, error) {
	resp := make(kadm.DeleteTopicResponses)
	for _, t := range topics {
		resp[t] = kadm.DeleteTopicResponse{Topic: t}
	}
	return resp, nil
}

func (m *capturingKafkaAdmin) Close() {
	m.closed = true
}

func TestKafkaProvisioner_Name(t *testing.T) {
	p := &KafkaProvisioner{Brokers: []string{"localhost:9092"}}
	assert.Equal(t, "kafka", p.Name())
}

func TestJoinBrokers(t *testing.T) {
	assert.Equal(t, "a:9092", joinBrokers([]string{"a:9092"}))
	assert.Equal(t, "a:9092,b:9093", joinBrokers([]string{"a:9092", "b:9093"}))
	assert.Equal(t, "", joinBrokers(nil))
}

func TestKafkaProvisioner_Provision_RetentionMs(t *testing.T) {
	route := v1alpha1.AsyncRouteSpec{
		Protocol: "kafka",
		Target:   "orders",
	}

	t.Run("default retention.ms when no TTL or annotation is set", func(t *testing.T) {
		admin := &capturingKafkaAdmin{}
		p := &KafkaProvisioner{
			Brokers:           []string{"localhost:9092"},
			NumPartitions:     3,
			ReplicationFactor: 1,
			AdminFactory: func(_ []string) (KafkaAdmin, error) {
				return admin, nil
			},
		}
		env := &v1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{Name: "pr-42"},
		}

		res, err := p.Provision(context.Background(), env, route)
		require.NoError(t, err)
		assert.Equal(t, "orders--pr-42", res.ResolvedTarget)
		require.NotNil(t, admin.lastConfigs)
		require.NotNil(t, admin.lastConfigs["retention.ms"])
		assert.Equal(t, "86400000", *admin.lastConfigs["retention.ms"])
		assert.True(t, admin.closed)
	})

	t.Run("TTL sets retention.ms", func(t *testing.T) {
		admin := &capturingKafkaAdmin{}
		p := &KafkaProvisioner{
			Brokers:           []string{"localhost:9092"},
			NumPartitions:     1,
			ReplicationFactor: 1,
			AdminFactory: func(_ []string) (KafkaAdmin, error) {
				return admin, nil
			},
		}
		env := &v1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{Name: "pr-42"},
			Spec: v1alpha1.EnvironmentSpec{
				Lifecycle: v1alpha1.EnvironmentLifecycle{
					TTL: &metav1.Duration{Duration: 4 * time.Hour},
				},
			},
		}

		_, err := p.Provision(context.Background(), env, route)
		require.NoError(t, err)
		require.NotNil(t, admin.lastConfigs["retention.ms"])
		assert.Equal(t, "14400000", *admin.lastConfigs["retention.ms"])
	})

	t.Run("annotation overrides TTL", func(t *testing.T) {
		admin := &capturingKafkaAdmin{}
		p := &KafkaProvisioner{
			Brokers:           []string{"localhost:9092"},
			NumPartitions:     1,
			ReplicationFactor: 1,
			AdminFactory: func(_ []string) (KafkaAdmin, error) {
				return admin, nil
			},
		}
		env := &v1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{
				Name: "pr-42",
				Annotations: map[string]string{
					AnnotationKafkaRetentionMs: "3600000",
				},
			},
			Spec: v1alpha1.EnvironmentSpec{
				Lifecycle: v1alpha1.EnvironmentLifecycle{
					TTL: &metav1.Duration{Duration: 4 * time.Hour},
				},
			},
		}

		_, err := p.Provision(context.Background(), env, route)
		require.NoError(t, err)
		require.NotNil(t, admin.lastConfigs["retention.ms"])
		assert.Equal(t, "3600000", *admin.lastConfigs["retention.ms"])
	})
}
