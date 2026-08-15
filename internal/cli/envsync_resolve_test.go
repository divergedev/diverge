package cli

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestResolveSecretKeyRef(t *testing.T) {
	clientset := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "my-secret", Namespace: "preview-test"},
		Data:       map[string][]byte{"apiKey": []byte("12345")},
	})

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "preview-test"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Env: []corev1.EnvVar{
						{
							Name: "API_KEY",
							ValueFrom: &corev1.EnvVarSource{
								SecretKeyRef: &corev1.SecretKeySelector{
									LocalObjectReference: corev1.LocalObjectReference{Name: "my-secret"},
									Key:                  "apiKey",
								},
							},
						},
					},
				},
			},
		},
	}

	envMap, err := resolveBaselineEnv(context.Background(), clientset, pod)
	require.NoError(t, err)
	assert.Equal(t, "12345", envMap["API_KEY"])
}

func TestResolveConfigMapKeyRef(t *testing.T) {
	clientset := fake.NewSimpleClientset(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "my-cm", Namespace: "preview-test"},
		Data:       map[string]string{"configVal": "abc"},
	})

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "preview-test"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Env: []corev1.EnvVar{
						{
							Name: "CONFIG_VAL",
							ValueFrom: &corev1.EnvVarSource{
								ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
									LocalObjectReference: corev1.LocalObjectReference{Name: "my-cm"},
									Key:                  "configVal",
								},
							},
						},
					},
				},
			},
		},
	}

	envMap, err := resolveBaselineEnv(context.Background(), clientset, pod)
	require.NoError(t, err)
	assert.Equal(t, "abc", envMap["CONFIG_VAL"])
}

func TestResolveEnvFromSecret(t *testing.T) {
	clientset := fake.NewSimpleClientset(&corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "my-secret", Namespace: "preview-test"},
		Data:       map[string][]byte{"KEY1": []byte("val1"), "KEY2": []byte("val2")},
	})

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "preview-test"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					EnvFrom: []corev1.EnvFromSource{
						{
							SecretRef: &corev1.SecretEnvSource{
								LocalObjectReference: corev1.LocalObjectReference{Name: "my-secret"},
							},
						},
					},
				},
			},
		},
	}

	envMap, err := resolveBaselineEnv(context.Background(), clientset, pod)
	require.NoError(t, err)
	assert.Equal(t, "val1", envMap["KEY1"])
	assert.Equal(t, "val2", envMap["KEY2"])
}

func TestResolveEnvFromConfigMap(t *testing.T) {
	clientset := fake.NewSimpleClientset(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "my-cm", Namespace: "preview-test"},
		Data:       map[string]string{"CM_KEY": "cm_val"},
	})

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "preview-test"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					EnvFrom: []corev1.EnvFromSource{
						{
							ConfigMapRef: &corev1.ConfigMapEnvSource{
								LocalObjectReference: corev1.LocalObjectReference{Name: "my-cm"},
							},
						},
					},
				},
			},
		},
	}

	envMap, err := resolveBaselineEnv(context.Background(), clientset, pod)
	require.NoError(t, err)
	assert.Equal(t, "cm_val", envMap["CM_KEY"])
}

func TestResolveMixed(t *testing.T) {
	clientset := fake.NewSimpleClientset(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "my-cm", Namespace: "preview-test"},
		Data:       map[string]string{"CM_KEY": "cm_val"},
	})

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "preview-test"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Env: []corev1.EnvVar{
						{
							Name: "LITERAL_KEY", Value: "literal_val",
						},
					},
					EnvFrom: []corev1.EnvFromSource{
						{
							ConfigMapRef: &corev1.ConfigMapEnvSource{
								LocalObjectReference: corev1.LocalObjectReference{Name: "my-cm"},
							},
						},
					},
				},
			},
		},
	}

	envMap, err := resolveBaselineEnv(context.Background(), clientset, pod)
	require.NoError(t, err)
	assert.Equal(t, "cm_val", envMap["CM_KEY"])
	assert.Equal(t, "literal_val", envMap["LITERAL_KEY"])
}

func TestResolveRBACError(t *testing.T) {
	clientset := fake.NewSimpleClientset() // Secret does not exist
	clientset.PrependReactor("get", "secrets", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, "db-credentials", fmt.Errorf("RBAC denied"))
	})

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "preview-test"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Env: []corev1.EnvVar{
						{
							Name: "API_KEY",
							ValueFrom: &corev1.EnvVarSource{
								SecretKeyRef: &corev1.SecretKeySelector{
									LocalObjectReference: corev1.LocalObjectReference{Name: "missing-secret"},
									Key:                  "apiKey",
								},
							},
						},
					},
				},
			},
		},
	}

	_, err := resolveBaselineEnv(context.Background(), clientset, pod)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "forbidden")
}

func TestResolveEnvOverridesEnvFrom(t *testing.T) {
	clientset := fake.NewSimpleClientset(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "my-cm", Namespace: "preview-test"},
		Data:       map[string]string{"DB_HOST": "from-envfrom"},
	})

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "preview-test"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					EnvFrom: []corev1.EnvFromSource{
						{
							ConfigMapRef: &corev1.ConfigMapEnvSource{
								LocalObjectReference: corev1.LocalObjectReference{Name: "my-cm"},
							},
						},
					},
					Env: []corev1.EnvVar{
						{
							Name: "DB_HOST", Value: "from-env",
						},
					},
				},
			},
		},
	}

	envMap, err := resolveBaselineEnv(context.Background(), clientset, pod)
	require.NoError(t, err)
	assert.Equal(t, "from-env", envMap["DB_HOST"])
}

func TestResolveFiltersInfraVars(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "preview-test"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Env: []corev1.EnvVar{
						{Name: "KUBERNETES_SERVICE_HOST", Value: "10.0.0.1"},
						{Name: "NORMAL_VAR", Value: "value"},
					},
				},
			},
		},
	}

	envMap, err := resolveBaselineEnv(context.Background(), clientset, pod)
	require.NoError(t, err)
	assert.NotContains(t, envMap, "KUBERNETES_SERVICE_HOST")
	assert.Contains(t, envMap, "NORMAL_VAR")
}

func TestResolveVolumeWarning(t *testing.T) {
	// Mostly verifying it doesn't panic and behaves normally, warning is logged internally
	clientset := fake.NewSimpleClientset()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "preview-test"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "c1"}},
			Volumes: []corev1.Volume{
				{
					Name: "sec-vol",
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{SecretName: "my-sec"},
					},
				},
			},
		},
	}

	_, err := resolveBaselineEnv(context.Background(), clientset, pod)
	require.NoError(t, err)
}
