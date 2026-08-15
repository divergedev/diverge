//go:build !no_kafka

package async

import (
	"flag"
	"strings"

	"github.com/divergedev/diverge/pkg/registry"
)

var (
	kafkaBrokers           string
	kafkaPartitions        int
	kafkaReplicationFactor int
)

func init() {
	flag.StringVar(&kafkaBrokers, "kafka-brokers", "localhost:9092", "Comma-separated Kafka broker addresses")
	flag.IntVar(&kafkaPartitions, "kafka-partitions", 3, "Number of partitions for preview topics")
	flag.IntVar(&kafkaReplicationFactor, "kafka-replication-factor", 1, "Replication factor for preview topics")

	Providers.Register("kafka", registry.Provider[Provisioner]{
		Create: func(deps registry.Deps) (Provisioner, error) {
			brokers := strings.Split(kafkaBrokers, ",")
			return &KafkaProvisioner{
				Brokers:           brokers,
				NumPartitions:     int32(kafkaPartitions),
				ReplicationFactor: int16(kafkaReplicationFactor),
			}, nil
		},
		Description: "Kafka topic provisioner via AdminClient (AutoMQ/Kafka/Redpanda compatible)",
	})
}
