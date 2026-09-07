package features

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hegel.dev/go/hegel"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/divergedev/diverge/api/v1alpha1"
)

func TestFliptNamespaceName_Property(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		alphabet := []string{"a", "b", "c", "1", "2", "-", "_", "x", "y", "z"}
		nameLen := hegel.Draw(ht, hegel.Integers(0, 100))
		envName := ""
		for i := 0; i < nameLen; i++ {
			envName += hegel.Draw(ht, hegel.SampledFrom(alphabet))
		}

		ns := FliptNamespaceName(envName)

		// Property 1: Must always start with "diverge-"
		if !strings.HasPrefix(ns, "diverge-") {
			ht.Fatalf("expected namespace %q to start with diverge-", ns)
		}

		// Property 2: Length must never exceed 63 characters (RFC 1123 / DNS label limit)
		if len(ns) > 63 {
			ht.Fatalf("expected namespace length <= 63, got %d (%s)", len(ns), ns)
		}

		// Property 3: If original was short enough, contains entire name
		if len(envName)+8 <= 63 {
			expected := fmt.Sprintf("diverge-%s", envName)
			if ns != expected {
				ht.Fatalf("expected %s, got %s", expected, ns)
			}
		}
	})
}

func TestFliptProvision_Property(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		ts, mock := newMockFliptServer("")
		defer ts.Close()

		scheme := runtime.NewScheme()
		_ = corev1.AddToScheme(scheme)
		_ = v1alpha1.AddToScheme(scheme)

		numOverrides := hegel.Draw(ht, hegel.Integers(0, 5))
		overrides := make(map[string]string, numOverrides)

		keyAlphabet := []string{"a", "b", "c", "d", "e", "_"}
		for i := 0; i < numOverrides; i++ {
			key := genPropString(ht, keyAlphabet, 1, 8)
			valType := hegel.Draw(ht, hegel.SampledFrom([]string{"bool_true", "bool_false", "int", "variant_str"}))
			var val string
			switch valType {
			case "bool_true":
				val = "true"
			case "bool_false":
				val = "false"
			case "int":
				val = strconv.Itoa(hegel.Draw(ht, hegel.Integers(1, 999)))
			case "variant_str":
				val = genPropString(ht, []string{"v", "1", "b", "e", "t", "a"}, 1, 10)
			}
			overrides[key] = val
		}

		envName := "pbt-" + genPropString(ht, []string{"1", "2", "3", "a", "b"}, 3, 6)
		secName := "sec-" + envName

		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secName,
				Namespace: "default",
			},
			Data: map[string][]byte{
				"url":   []byte(ts.URL),
				"token": []byte("pbt-token"),
			},
		}

		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()
		provider := NewFliptProvider(c, scheme, logr.Discard())

		env := &v1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      envName,
				Namespace: "default",
			},
			Spec: v1alpha1.EnvironmentSpec{
				Features: &v1alpha1.FeatureSpec{
					Provider:      "flipt",
					ConnectionRef: secName,
					Overrides:     overrides,
				},
			},
		}

		ctx := context.Background()
		res, err := provider.Provision(ctx, env)
		require.NoError(t, err)
		assert.Equal(t, "flipt", res.ProviderType)
		assert.Equal(t, ts.URL, res.EnvVars["FLIPT_URL"])
		assert.Equal(t, FliptNamespaceName(envName), res.EnvVars["FLIPT_NAMESPACE"])

		// Verify on server
		mock.mu.Lock()
		nsKey := FliptNamespaceName(envName)
		assert.Contains(t, mock.namespaces, nsKey)
		for k, v := range overrides {
			flag, exists := mock.flags[nsKey][k]
			assert.True(t, exists, "flag %s should exist", k)
			switch v {
			case "true":
				assert.Equal(t, "BOOLEAN_FLAG_TYPE", flag["type"])
				assert.Equal(t, true, flag["enabled"])
			case "false":
				assert.Equal(t, "BOOLEAN_FLAG_TYPE", flag["type"])
				assert.Equal(t, false, flag["enabled"])
			default:
				assert.Equal(t, "VARIANT_FLAG_TYPE", flag["type"])
				assert.Equal(t, true, flag["enabled"])
				assert.Equal(t, v, mock.variants[nsKey][k]["name"])
			}
		}
		mock.mu.Unlock()

		// Teardown
		err = provider.Teardown(ctx, env)
		require.NoError(t, err)

		mock.mu.Lock()
		assert.NotContains(t, mock.namespaces, nsKey)
		mock.mu.Unlock()
	})
}
