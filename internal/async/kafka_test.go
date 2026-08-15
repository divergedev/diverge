//go:build !no_kafka

package async

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestKafkaProvisioner_Name(t *testing.T) {
	p := &KafkaProvisioner{Brokers: []string{"localhost:9092"}}
	assert.Equal(t, "kafka", p.Name())
}

func TestJoinBrokers(t *testing.T) {
	assert.Equal(t, "a:9092", joinBrokers([]string{"a:9092"}))
	assert.Equal(t, "a:9092,b:9093", joinBrokers([]string{"a:9092", "b:9093"}))
	assert.Equal(t, "", joinBrokers(nil))
}
