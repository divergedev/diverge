package cli

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"hegel.dev/go/hegel"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// validServiceName generates a valid Kubernetes label value: lowercase
// alphanumeric, dashes, dots — must start and end with alphanumeric, max 63.
func validServiceName(ht *hegel.T) string {
	alpha := "abcdefghijklmnopqrstuvwxyz0123456789"
	inner := alpha + "-."
	n := hegel.Draw(ht, hegel.Integers(1, 63))
	if n == 1 {
		i := hegel.Draw(ht, hegel.Integers(0, len(alpha)-1))
		return string(alpha[i])
	}
	var sb strings.Builder
	// First char: alphanumeric
	sb.WriteByte(alpha[hegel.Draw(ht, hegel.Integers(0, len(alpha)-1))])
	// Middle: alphanumeric + dash + dot
	for range n - 2 {
		sb.WriteByte(inner[hegel.Draw(ht, hegel.Integers(0, len(inner)-1))])
	}
	// Last char: alphanumeric
	sb.WriteByte(alpha[hegel.Draw(ht, hegel.Integers(0, len(alpha)-1))])
	return sb.String()
}

// validEnvVarName generates a valid (non-K8s-injected) env var name.
func validEnvVarName(ht *hegel.T) string {
	prefixes := []string{"APP_", "DB_", "REDIS_", "API_", "SVC_", "MY_", "CUSTOM_"}
	prefix := prefixes[hegel.Draw(ht, hegel.Integers(0, len(prefixes)-1))]
	suffixes := []string{"URL", "HOST", "NAME", "KEY", "SECRET", "TOKEN", "MODE", "LEVEL"}
	suffix := suffixes[hegel.Draw(ht, hegel.Integers(0, len(suffixes)-1))]
	return prefix + suffix
}

// validEnvValue generates a plausible env var value (no newlines).
func validEnvValue(ht *hegel.T) string {
	chars := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_.:/@?=&"
	n := hegel.Draw(ht, hegel.Integers(0, 100))
	var sb strings.Builder
	for range n {
		sb.WriteByte(chars[hegel.Draw(ht, hegel.Integers(0, len(chars)-1))])
	}
	return sb.String()
}

func TestProperty_isKubeInjected_NeverFiltersAppEnvVars(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		name := validEnvVarName(ht)
		if isKubeInjected(name) {
			ht.Fatalf("isKubeInjected(%q) = true, but this is an app env var", name)
		}
	})
}

func TestProperty_EnvDivergeRoundTrip(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		svcName := validServiceName(ht)

		// Generate 1-10 env vars
		numVars := hegel.Draw(ht, hegel.Integers(1, 10))
		envVars := make([]corev1.EnvVar, 0, numVars)
		expected := make(map[string]string)
		for range numVars {
			name := validEnvVarName(ht)
			value := validEnvValue(ht)
			envVars = append(envVars, corev1.EnvVar{Name: name, Value: value})
			expected[name] = value
		}

		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      svcName + "-abc",
				Namespace: "default",
				Labels:    map[string]string{"app": svcName},
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: svcName, Image: svcName + ":latest", Env: envVars},
				},
			},
		}

		clientset := fake.NewSimpleClientset(pod)
		tmpDir := t.TempDir()
		outPath := filepath.Join(tmpDir, ".env.diverge")

		_, err := syncBaselineEnv(context.Background(), clientset, syncEnvOptions{
			Namespace:   "default",
			ServiceName: svcName,
			OutputPath:  outPath,
		})
		require.NoError(ht, err)

		// Parse the written file and verify round-trip
		content, err := os.ReadFile(outPath)
		require.NoError(ht, err)

		parsed := make(map[string]string)
		scanner := bufio.NewScanner(strings.NewReader(string(content)))
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "#") || line == "" {
				continue
			}
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				parsed[parts[0]] = parts[1]
			}
		}

		// Every expected env var should appear in the parsed output
		for name, value := range expected {
			got, ok := parsed[name]
			if !ok {
				ht.Fatalf("env var %q not found in .env.diverge", name)
			}
			if got != value {
				ht.Fatalf("env var %q: got %q, want %q", name, got, value)
			}
		}
	})
}

func TestProperty_EnvDivergeFileAlwaysCreated(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		svcName := validServiceName(ht)

		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      svcName + "-x",
				Namespace: "default",
				Labels:    map[string]string{"app": svcName},
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{Name: svcName, Image: svcName + ":latest"},
				},
			},
		}

		clientset := fake.NewSimpleClientset(pod)
		tmpDir := t.TempDir()
		outPath := filepath.Join(tmpDir, ".env.diverge")

		_, err := syncBaselineEnv(context.Background(), clientset, syncEnvOptions{
			Namespace:   "default",
			ServiceName: svcName,
			OutputPath:  outPath,
		})
		require.NoError(ht, err)

		// File must always exist after sync (even if pod has no env vars)
		_, err = os.Stat(outPath)
		if os.IsNotExist(err) {
			ht.Fatalf(".env.diverge was not created for service %q", svcName)
		}
	})
}
