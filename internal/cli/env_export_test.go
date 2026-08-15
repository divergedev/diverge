package cli

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

func TestEnvExport_DotenvFormat(t *testing.T) {
	env := map[string]string{
		"FOO": "bar",
		"BAZ": "qux",
	}
	out, err := formatDotenv(env)
	require.NoError(t, err)
	expected := "BAZ=qux\nFOO=bar\n"
	assert.Equal(t, expected, out)
}

func TestEnvExport_JSONFormat(t *testing.T) {
	env := map[string]string{
		"FOO": "bar",
		"BAZ": "qux",
	}
	out, err := formatJSON(env)
	require.NoError(t, err)
	expected := "{\n  \"BAZ\": \"qux\",\n  \"FOO\": \"bar\"\n}\n"
	assert.Equal(t, expected, out)
}

func TestEnvExport_ShellFormat(t *testing.T) {
	env := map[string]string{
		"FOO": "bar",
		"BAZ": "qux",
	}
	out, err := formatShell(env)
	require.NoError(t, err)
	expected := "export BAZ=\"qux\"\nexport FOO=\"bar\"\n"
	assert.Equal(t, expected, out)
}

func TestEnvExport_QuotedValues(t *testing.T) {
	env := map[string]string{
		"SPACE": "has space",
		"QUOTE": `has "quote"`,
		"NEWLN": "has\nnewline",
	}
	out, err := formatDotenv(env)
	require.NoError(t, err)
	assert.Contains(t, out, `NEWLN="has\nnewline"`)
	assert.Contains(t, out, `QUOTE="has \"quote\""`)
	assert.Contains(t, out, `SPACE="has space"`)
}

func TestEnvExport_EmptyEnv(t *testing.T) {
	env := map[string]string{}
	outDotenv, _ := formatDotenv(env)
	assert.Empty(t, outDotenv)

	outJSON, _ := formatJSON(env)
	assert.Equal(t, "{}\n", outJSON)

	outShell, _ := formatShell(env)
	assert.Empty(t, outShell)
}

func TestEnvExportCmd(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, divergeiov1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))

	pgSingle := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "mr-1"},
		Status: divergeiov1alpha1.PreviewGroupStatus{
			Services: []divergeiov1alpha1.PreviewGroupServiceStatus{
				{Name: "web", EnvironmentName: "web-env", Namespace: "web-ns"},
			},
		},
	}

	envSingle := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "web-env", Namespace: "web-ns"},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			ServiceConfig: &divergeiov1alpha1.ServicePreviewConfig{
				Env: []divergeiov1alpha1.EnvVar{
					{Name: "INJECTED_VAR", Value: "value1"},
				},
			},
		},
	}

	podWeb := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web-pod",
			Namespace: "default",
			Labels: map[string]string{
				"app": "web",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "web",
					Env: []corev1.EnvVar{
						{Name: "BASE_VAR", Value: "base1"},
					},
				},
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}

	pgMulti := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "mr-2"},
		Status: divergeiov1alpha1.PreviewGroupStatus{
			Services: []divergeiov1alpha1.PreviewGroupServiceStatus{
				{Name: "web", EnvironmentName: "web-env2", Namespace: "web-ns"},
				{Name: "api", EnvironmentName: "api-env2", Namespace: "api-ns"},
			},
		},
	}

	envMultiAPI := &divergeiov1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{Name: "api-env2", Namespace: "api-ns"},
		Spec: divergeiov1alpha1.EnvironmentSpec{
			ServiceConfig: &divergeiov1alpha1.ServicePreviewConfig{
				Env: []divergeiov1alpha1.EnvVar{
					{Name: "API_VAR", Value: "value2"},
				},
			},
		},
	}

	podAPI := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "api-pod",
			Namespace: "default",
			Labels: map[string]string{
				"app": "api",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "api",
					Env: []corev1.EnvVar{
						{Name: "BASE_API", Value: "base2"},
					},
				},
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}

	tests := []struct {
		name       string
		args       []string
		k8sObjects []runtime.Object
		crtObjects []client.Object
		wantErr    string
		wantOutput string
	}{
		{
			name:       "Single-service group auto-select",
			args:       []string{"--group", "mr-1"},
			k8sObjects: []runtime.Object{podWeb},
			crtObjects: []client.Object{pgSingle, envSingle},
			wantOutput: "BASE_VAR=base1\nINJECTED_VAR=value1\n",
		},
		{
			name:       "Multi-service group requires --service",
			args:       []string{"--group", "mr-2"},
			k8sObjects: []runtime.Object{podWeb, podAPI},
			crtObjects: []client.Object{pgMulti, envMultiAPI},
			wantErr:    "has multiple services",
		},
		{
			name:       "Multi-service group with --service specified",
			args:       []string{"--group", "mr-2", "--service", "api"},
			k8sObjects: []runtime.Object{podAPI},
			crtObjects: []client.Object{pgMulti, envMultiAPI},
			wantOutput: "API_VAR=value2\nBASE_API=base2\n",
		},
		{
			name:    "Group not found error",
			args:    []string{"--group", "nonexistent"},
			wantErr: "failed to get PreviewGroup \"nonexistent\"",
		},
		{
			name:       "JSON output format",
			args:       []string{"--group", "mr-1", "--format", "json"},
			k8sObjects: []runtime.Object{podWeb},
			crtObjects: []client.Object{pgSingle, envSingle},
			wantOutput: "{\n  \"BASE_VAR\": \"base1\",\n  \"INJECTED_VAR\": \"value1\"\n}\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := fakeclient.NewClientBuilder().WithScheme(scheme).WithObjects(tt.crtObjects...).Build()
			fakeClientset := fake.NewSimpleClientset(tt.k8sObjects...)

			app := &App{
				Client:    fakeClient,
				Clientset: fakeClientset,
				Namespace: "default",
			}

			cmd := newEnvExportCmd(app)
			var buf bytes.Buffer
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)
			cmd.SetArgs(tt.args)

			err := cmd.Execute()
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantOutput, buf.String())
			}
		})
	}
}
