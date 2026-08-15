//go:build !no_kafka

package async

import (
	"context"
	"errors"
	"testing"

	v1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/twmb/franz-go/pkg/kadm"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type mockKafkaAdmin struct {
	createResp kadm.CreateTopicResponses
	createErr  error
	deleteResp kadm.DeleteTopicResponses
	deleteErr  error
	closeCalls int
}

func (m *mockKafkaAdmin) CreateTopics(ctx context.Context, partitions int32, replication int16, configs map[string]*string, topics ...string) (kadm.CreateTopicResponses, error) {
	return m.createResp, m.createErr
}

func (m *mockKafkaAdmin) DeleteTopics(ctx context.Context, topics ...string) (kadm.DeleteTopicResponses, error) {
	return m.deleteResp, m.deleteErr
}

func (m *mockKafkaAdmin) Close() {
	m.closeCalls++
}

func TestKafkaProvisioner_Mock(t *testing.T) {
	ctx := context.Background()
	env := &v1alpha1.Environment{ObjectMeta: metav1.ObjectMeta{Name: "testenv"}}
	route := v1alpha1.AsyncRouteSpec{Target: "my-topic"}
	targetName := "my-topic-testenv"

	// 1. Happy path provision
	t.Run("Happy path provision", func(t *testing.T) {
		mockAdmin := &mockKafkaAdmin{
			createResp: kadm.CreateTopicResponses{
				targetName: {Topic: targetName},
			},
		}
		provisioner := &KafkaProvisioner{
			Brokers: []string{"localhost:9092"},
			AdminFactory: func(brokers []string) (KafkaAdmin, error) {
				return mockAdmin, nil
			},
		}

		res, err := provisioner.Provision(ctx, env, route)
		assert.NoError(t, err)
		assert.NotNil(t, res)
		assert.Equal(t, targetName, res.ResolvedTarget)
		assert.Equal(t, targetName, res.EnvVars["KAFKA_TOPIC"])
		assert.Equal(t, targetName, res.EnvVars["KAFKA_CONSUMER_GROUP"])
		assert.Equal(t, "localhost:9092", res.EnvVars["KAFKA_BROKERS"])
		assert.Equal(t, 1, mockAdmin.closeCalls)
	})

	// 2. Topic already exists
	t.Run("Topic already exists", func(t *testing.T) {
		mockAdmin := &mockKafkaAdmin{
			createResp: kadm.CreateTopicResponses{
				targetName: {Topic: targetName, Err: errors.New("topic already exists")},
			},
		}
		provisioner := &KafkaProvisioner{
			Brokers: []string{"localhost:9092"},
			AdminFactory: func(brokers []string) (KafkaAdmin, error) {
				return mockAdmin, nil
			},
		}

		res, err := provisioner.Provision(ctx, env, route)
		assert.NoError(t, err)
		assert.NotNil(t, res)
	})

	// 3. Topic create failure
	t.Run("Topic create failure", func(t *testing.T) {
		mockAdmin := &mockKafkaAdmin{
			createErr: errors.New("some real error"),
		}
		provisioner := &KafkaProvisioner{
			Brokers: []string{"localhost:9092"},
			AdminFactory: func(brokers []string) (KafkaAdmin, error) {
				return mockAdmin, nil
			},
		}

		_, err := provisioner.Provision(ctx, env, route)
		assert.ErrorContains(t, err, "kafka create topic failed")
		assert.ErrorContains(t, err, "some real error")
	})

	// 4. Happy path teardown
	t.Run("Happy path teardown", func(t *testing.T) {
		mockAdmin := &mockKafkaAdmin{
			deleteResp: kadm.DeleteTopicResponses{
				targetName: {Topic: targetName},
			},
		}
		provisioner := &KafkaProvisioner{
			Brokers: []string{"localhost:9092"},
			AdminFactory: func(brokers []string) (KafkaAdmin, error) {
				return mockAdmin, nil
			},
		}

		err := provisioner.Teardown(ctx, env, route)
		assert.NoError(t, err)
		assert.Equal(t, 1, mockAdmin.closeCalls)
	})

	// 5. Topic not found on teardown
	t.Run("Topic not found on teardown", func(t *testing.T) {
		mockAdmin := &mockKafkaAdmin{
			deleteResp: kadm.DeleteTopicResponses{
				targetName: {Topic: targetName, Err: errors.New("UnknownTopicOrPartition")},
			},
		}
		provisioner := &KafkaProvisioner{
			Brokers: []string{"localhost:9092"},
			AdminFactory: func(brokers []string) (KafkaAdmin, error) {
				return mockAdmin, nil
			},
		}

		err := provisioner.Teardown(ctx, env, route)
		assert.NoError(t, err)
	})

	// 6. Teardown failure
	t.Run("Teardown failure", func(t *testing.T) {
		mockAdmin := &mockKafkaAdmin{
			deleteErr: errors.New("some network error"),
		}
		provisioner := &KafkaProvisioner{
			Brokers: []string{"localhost:9092"},
			AdminFactory: func(brokers []string) (KafkaAdmin, error) {
				return mockAdmin, nil
			},
		}

		err := provisioner.Teardown(ctx, env, route)
		assert.ErrorContains(t, err, "kafka delete topic failed")
	})

	// 7. Custom EnvVarMapping
	t.Run("Custom EnvVarMapping", func(t *testing.T) {
		mockAdmin := &mockKafkaAdmin{
			createResp: kadm.CreateTopicResponses{
				targetName: {Topic: targetName},
			},
		}
		provisioner := &KafkaProvisioner{
			Brokers: []string{"localhost:9092"},
			AdminFactory: func(brokers []string) (KafkaAdmin, error) {
				return mockAdmin, nil
			},
		}

		customRoute := v1alpha1.AsyncRouteSpec{
			Target: "my-topic",
			EnvVarMapping: map[string]string{
				"CUSTOM_TOPIC": "{{ .ResolvedTarget }}",
				"OTHER_VAR":    "",
			},
		}

		res, err := provisioner.Provision(ctx, env, customRoute)
		assert.NoError(t, err)
		assert.Equal(t, targetName, res.EnvVars["CUSTOM_TOPIC"])
		assert.Equal(t, targetName, res.EnvVars["OTHER_VAR"])
		assert.Empty(t, res.EnvVars["KAFKA_TOPIC"])
	})

	// 8. Admin factory failure
	t.Run("Admin factory failure", func(t *testing.T) {
		provisioner := &KafkaProvisioner{
			Brokers: []string{"localhost:9092"},
			AdminFactory: func(brokers []string) (KafkaAdmin, error) {
				return nil, errors.New("dial error")
			},
		}

		_, err := provisioner.Provision(ctx, env, route)
		assert.ErrorContains(t, err, "kafka client error")
		assert.ErrorContains(t, err, "dial error")
	})
}
