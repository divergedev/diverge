package deployer

import (
	"fmt"
	"strconv"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/divergedev/diverge/api/v1alpha1"
)

var scaledObjectGVK = schema.GroupVersionKind{
	Group:   "keda.sh",
	Version: "v1alpha1",
	Kind:    "ScaledObject",
}

func kedaIntOrDefault(ptr *int32, defaultVal int32) int64 {
	if ptr != nil {
		return int64(*ptr)
	}
	return int64(defaultVal)
}

func buildTemporalScaledObject(env *v1alpha1.Environment, kedaSpec *v1alpha1.KEDASpec, asyncRoute v1alpha1.AsyncRouteSpec, temporalAddress string) *unstructured.Unstructured {
	targetName := env.Name
	if env.Spec.ServiceConfig != nil && env.Spec.ServiceConfig.ServiceName != "" {
		targetName = fmt.Sprintf("%s-%s", env.Name, env.Spec.ServiceConfig.ServiceName)
	}

	targetNS := env.Namespace
	if env.Spec.Deploy.Namespace == "create" {
		targetNS = env.PreviewNamespace()
	}

	taskQueue := fmt.Sprintf("%s-%s", asyncRoute.Target, env.Name)
	targetQueueSize := "5"

	var minRepl, maxRepl, cooldown, polling int64
	if kedaSpec != nil {
		minRepl = kedaIntOrDefault(kedaSpec.MinReplicas, 1)
		maxRepl = kedaIntOrDefault(kedaSpec.MaxReplicas, 3)
		cooldown = kedaIntOrDefault(kedaSpec.CooldownPeriod, 300)
		polling = kedaIntOrDefault(kedaSpec.PollingInterval, 30)
		if kedaSpec.TargetQueueSize != nil {
			targetQueueSize = strconv.Itoa(int(*kedaSpec.TargetQueueSize))
		}
	} else {
		minRepl, maxRepl, cooldown, polling = 1, 3, 300, 30
	}

	so := &unstructured.Unstructured{}
	so.SetGroupVersionKind(scaledObjectGVK)
	so.SetName(fmt.Sprintf("%s-temporal", targetName))
	so.SetNamespace(targetNS)
	so.SetLabels(map[string]string{
		"diverge.io/managed-by":     "diverge",
		"diverge.io/environment":    env.Name,
		"diverge.io/async-protocol": "temporal",
	})

	_ = unstructured.SetNestedField(so.Object, targetName, "spec", "scaleTargetRef", "name")
	_ = unstructured.SetNestedField(so.Object, minRepl, "spec", "minReplicaCount")
	_ = unstructured.SetNestedField(so.Object, maxRepl, "spec", "maxReplicaCount")
	_ = unstructured.SetNestedField(so.Object, cooldown, "spec", "cooldownPeriod")
	_ = unstructured.SetNestedField(so.Object, polling, "spec", "pollingInterval")

	trigger := map[string]interface{}{
		"type": "temporal",
		"metadata": map[string]interface{}{
			"endpoint":                  temporalAddress,
			"namespace":                 "default",
			"taskQueue":                 taskQueue,
			"targetQueueSize":           targetQueueSize,
			"activationTargetQueueSize": "0",
		},
	}
	_ = unstructured.SetNestedSlice(so.Object, []interface{}{trigger}, "spec", "triggers")

	return so
}

func buildKafkaScaledObject(env *v1alpha1.Environment, kedaSpec *v1alpha1.KEDASpec, asyncRoute v1alpha1.AsyncRouteSpec, kafkaBrokers string) *unstructured.Unstructured {
	targetName := env.Name
	if env.Spec.ServiceConfig != nil && env.Spec.ServiceConfig.ServiceName != "" {
		targetName = fmt.Sprintf("%s-%s", env.Name, env.Spec.ServiceConfig.ServiceName)
	}

	targetNS := env.Namespace
	if env.Spec.Deploy.Namespace == "create" {
		targetNS = env.PreviewNamespace()
	}

	consumerGroup := fmt.Sprintf("%s-%s", asyncRoute.Target, env.Name)
	topic := fmt.Sprintf("%s-%s", asyncRoute.Target, env.Name)
	lagThreshold := "10"

	var minRepl, maxRepl, cooldown, polling int64
	if kedaSpec != nil {
		minRepl = kedaIntOrDefault(kedaSpec.MinReplicas, 1)
		maxRepl = kedaIntOrDefault(kedaSpec.MaxReplicas, 3)
		cooldown = kedaIntOrDefault(kedaSpec.CooldownPeriod, 300)
		polling = kedaIntOrDefault(kedaSpec.PollingInterval, 30)
		if kedaSpec.TargetQueueSize != nil {
			lagThreshold = strconv.Itoa(int(*kedaSpec.TargetQueueSize))
		}
	} else {
		minRepl, maxRepl, cooldown, polling = 1, 3, 300, 30
	}

	so := &unstructured.Unstructured{}
	so.SetGroupVersionKind(scaledObjectGVK)
	so.SetName(fmt.Sprintf("%s-kafka", targetName))
	so.SetNamespace(targetNS)
	so.SetLabels(map[string]string{
		"diverge.io/managed-by":     "diverge",
		"diverge.io/environment":    env.Name,
		"diverge.io/async-protocol": "kafka",
	})

	_ = unstructured.SetNestedField(so.Object, targetName, "spec", "scaleTargetRef", "name")
	_ = unstructured.SetNestedField(so.Object, minRepl, "spec", "minReplicaCount")
	_ = unstructured.SetNestedField(so.Object, maxRepl, "spec", "maxReplicaCount")
	_ = unstructured.SetNestedField(so.Object, cooldown, "spec", "cooldownPeriod")
	_ = unstructured.SetNestedField(so.Object, polling, "spec", "pollingInterval")

	trigger := map[string]interface{}{
		"type": "kafka",
		"metadata": map[string]interface{}{
			"bootstrapServers":       kafkaBrokers,
			"consumerGroup":          consumerGroup,
			"topic":                  topic,
			"lagThreshold":           lagThreshold,
			"activationLagThreshold": "0",
			"offsetResetPolicy":      "latest",
		},
	}
	_ = unstructured.SetNestedSlice(so.Object, []interface{}{trigger}, "spec", "triggers")

	return so
}
