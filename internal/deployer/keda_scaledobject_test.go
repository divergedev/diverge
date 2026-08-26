package deployer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/divergedev/diverge/api/v1alpha1"
)

func TestBuildTemporalScaledObject(t *testing.T) {
	min := int32(0)
	max := int32(5)
	cooldown := int32(120)
	polling := int32(15)
	queueSize := int32(3)

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "pr-42", Namespace: "preview-ns"},
		Spec: v1alpha1.EnvironmentSpec{
			Deploy: v1alpha1.EnvironmentDeploy{Namespace: "same"},
		},
	}
	kedaSpec := &v1alpha1.KEDASpec{
		MinReplicas:     &min,
		MaxReplicas:     &max,
		CooldownPeriod:  &cooldown,
		PollingInterval: &polling,
		TargetQueueSize: &queueSize,
	}
	ar := v1alpha1.AsyncRouteSpec{Protocol: "temporal", Target: "payments-tasks"}

	so := buildTemporalScaledObject(env, kedaSpec, ar, "temporal.default.svc:7233")

	assert.Equal(t, "pr-42-temporal", so.GetName())
	assert.Equal(t, "preview-ns", so.GetNamespace())
	assert.Equal(t, "temporal", so.GetLabels()["diverge.io/async-protocol"])

	target, _, _ := unstructured.NestedString(so.Object, "spec", "scaleTargetRef", "name")
	assert.Equal(t, "pr-42", target)

	minR, _, _ := unstructured.NestedInt64(so.Object, "spec", "minReplicaCount")
	assert.Equal(t, int64(0), minR, "scale-to-zero")

	maxR, _, _ := unstructured.NestedInt64(so.Object, "spec", "maxReplicaCount")
	assert.Equal(t, int64(5), maxR)

	triggers, found, err := unstructured.NestedSlice(so.Object, "spec", "triggers")
	require.NoError(t, err)
	require.True(t, found)
	require.Len(t, triggers, 1)

	trig := triggers[0].(map[string]interface{})
	assert.Equal(t, "temporal", trig["type"])
	meta := trig["metadata"].(map[string]interface{})
	assert.Equal(t, "payments-tasks-pr-42", meta["taskQueue"])
	assert.Equal(t, "3", meta["targetQueueSize"])
}

func TestBuildTemporalScaledObject_Defaults(t *testing.T) {
	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "ns"},
		Spec:       v1alpha1.EnvironmentSpec{Deploy: v1alpha1.EnvironmentDeploy{Namespace: "same"}},
	}
	ar := v1alpha1.AsyncRouteSpec{Protocol: "temporal", Target: "tasks"}

	so := buildTemporalScaledObject(env, nil, ar, "temporal:7233")

	minR, _, _ := unstructured.NestedInt64(so.Object, "spec", "minReplicaCount")
	assert.Equal(t, int64(1), minR, "default minReplicas=1 (safe)")

	maxR, _, _ := unstructured.NestedInt64(so.Object, "spec", "maxReplicaCount")
	assert.Equal(t, int64(3), maxR)
}

func TestBuildKafkaScaledObject(t *testing.T) {
	min := int32(0)
	max := int32(8)
	queueSize := int32(20)

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "pr-99", Namespace: "ns"},
		Spec:       v1alpha1.EnvironmentSpec{Deploy: v1alpha1.EnvironmentDeploy{Namespace: "same"}},
	}
	kedaSpec := &v1alpha1.KEDASpec{MinReplicas: &min, MaxReplicas: &max, TargetQueueSize: &queueSize}
	ar := v1alpha1.AsyncRouteSpec{Protocol: "kafka", Target: "orders"}

	so := buildKafkaScaledObject(env, kedaSpec, ar, "kafka:9092")

	assert.Equal(t, "pr-99-kafka", so.GetName())

	triggers, _, _ := unstructured.NestedSlice(so.Object, "spec", "triggers")
	trig := triggers[0].(map[string]interface{})
	assert.Equal(t, "kafka", trig["type"])
	meta := trig["metadata"].(map[string]interface{})
	assert.Equal(t, "orders-pr-99", meta["consumerGroup"])
	assert.Equal(t, "20", meta["lagThreshold"])
	assert.Equal(t, "0", meta["activationLagThreshold"])

	minR, _, _ := unstructured.NestedInt64(so.Object, "spec", "minReplicaCount")
	assert.Equal(t, int64(0), minR, "scale-to-zero")
}

func TestBuildKafkaScaledObject_Defaults(t *testing.T) {
	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "env-1", Namespace: "ns"},
		Spec:       v1alpha1.EnvironmentSpec{Deploy: v1alpha1.EnvironmentDeploy{Namespace: "same"}},
	}
	ar := v1alpha1.AsyncRouteSpec{Protocol: "kafka", Target: "events"}

	so := buildKafkaScaledObject(env, nil, ar, "kafka:9092")

	triggers, _, _ := unstructured.NestedSlice(so.Object, "spec", "triggers")
	trig := triggers[0].(map[string]interface{})
	meta := trig["metadata"].(map[string]interface{})
	assert.Equal(t, "10", meta["lagThreshold"], "default lag threshold")

	minR, _, _ := unstructured.NestedInt64(so.Object, "spec", "minReplicaCount")
	assert.Equal(t, int64(1), minR, "default minReplicas=1 (safe)")
}
