package cli

import (
	"context"
	"strings"
	"testing"
	"testing/quick"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestResolveBaselineEnvProperties(t *testing.T) {
	f := func(varNames []string) bool {
		if len(varNames) == 0 {
			return true
		}

		envVars := []corev1.EnvVar{}
		for _, name := range varNames {
			if strings.TrimSpace(name) == "" {
				continue
			}
			envVars = append(envVars, corev1.EnvVar{Name: name, Value: "test"})
		}

		if len(envVars) == 0 {
			return true
		}

		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "preview-test"},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{Env: envVars}},
			},
		}

		clientset := fake.NewSimpleClientset()
		envMap, err := resolveBaselineEnv(context.Background(), clientset, pod)
		require.NoError(t, err)

		for k := range envMap {
			if isKubeInjected(k) {
				return false
			}
		}
		return true
	}

	if err := quick.Check(f, nil); err != nil {
		t.Error(err)
	}
}
