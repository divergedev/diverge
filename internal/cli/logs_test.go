package cli

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	crfake "sigs.k8s.io/controller-runtime/pkg/client/fake"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

func TestLogs_EnvironmentNotFound(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = divergeiov1alpha1.AddToScheme(scheme)

	c := crfake.NewClientBuilder().WithScheme(scheme).Build()
	clientset := fake.NewSimpleClientset()

	app := &App{
		Client:    c,
		Clientset: clientset,
		Namespace: "default",
	}

	cmd := newLogsCmd(app)
	cmd.SetArgs([]string{"missing-env"})
	var out bytes.Buffer
	cmd.SetOut(&out)

	err := cmd.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "environment not found")
}

func TestLogs_NoPodsFound(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = divergeiov1alpha1.AddToScheme(scheme)

	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
	}

	c := crfake.NewClientBuilder().WithScheme(scheme).WithObjects(env).Build()
	clientset := fake.NewSimpleClientset()

	app := &App{
		Client:    c,
		Clientset: clientset,
		Namespace: "default",
	}

	cmd := newLogsCmd(app)
	cmd.SetArgs([]string{"test-env"})
	var out bytes.Buffer
	cmd.SetOut(&out)

	err := cmd.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no pods found")
}

func TestLogs_ServiceFilter(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = divergeiov1alpha1.AddToScheme(scheme)

	env := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-env",
			Namespace: "default",
		},
	}

	pod1 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "frontend-1",
			Namespace: "default",
			Labels: map[string]string{
				"diverge.dev/environment": "test-env",
				"diverge.dev/service":     "frontend",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "c1"}},
		},
	}
	pod2 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "backend-1",
			Namespace: "default",
			Labels: map[string]string{
				"diverge.dev/environment": "test-env",
				"diverge.dev/service":     "backend",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "c1"}},
		},
	}

	c := crfake.NewClientBuilder().WithScheme(scheme).WithObjects(env).Build()
	clientset := fake.NewSimpleClientset(pod1, pod2)

	app := &App{
		Client:    c,
		Clientset: clientset,
		Namespace: "default",
	}

	cmd := newLogsCmd(app)
	cmd.SetArgs([]string{"test-env", "--service=non-existent"})
	var out bytes.Buffer
	cmd.SetOut(&out)

	err := cmd.Execute()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no pods found for environment test-env matching service filter")
}
