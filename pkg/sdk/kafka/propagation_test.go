package kafka_test

import (
	"testing"

	"github.com/divergedev/diverge/pkg/sdk/kafka"
	"github.com/stretchr/testify/assert"
)

func TestTopic(t *testing.T) {
	assert.Equal(t, "orders", kafka.Topic("orders", ""))
	assert.Equal(t, "orders--preview-1", kafka.Topic("orders", "preview-1"))
}

func TestConsumerGroup(t *testing.T) {
	assert.Equal(t, "my-group", kafka.ConsumerGroup("orders", "my-group", ""))
	assert.Equal(t, "my-group--orders--preview-1", kafka.ConsumerGroup("orders", "my-group", "preview-1"))
}

func TestParseTopic(t *testing.T) {
	base, env := kafka.ParseTopic("orders--preview-1")
	assert.Equal(t, "orders", base)
	assert.Equal(t, "preview-1", env)

	base2, env2 := kafka.ParseTopic("orders")
	assert.Equal(t, "orders", base2)
	assert.Equal(t, "", env2)
}
