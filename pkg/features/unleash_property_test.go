package features

import (
	"context"
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

// TestUnleashStrategyTitle_Property runs property-based tests verifying prefix, character length, RFC 1123 validity, and namespace isolation.
func TestUnleashStrategyTitle_Property(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		alphabet := []string{"a", "b", "c", "1", "2", "-", "_", "x", "y", "z"}
		nameLen := hegel.Draw(ht, hegel.Integers(0, 100))
		envName := ""
		for i := 0; i < nameLen; i++ {
			envName += hegel.Draw(ht, hegel.SampledFrom(alphabet))
		}
		nsLen := hegel.Draw(ht, hegel.Integers(0, 30))
		nsName := ""
		for i := 0; i < nsLen; i++ {
			nsName += hegel.Draw(ht, hegel.SampledFrom(alphabet))
		}

		title := UnleashStrategyTitle(nsName, envName)

		// Property 1: Must always start with "diverge-"
		if !strings.HasPrefix(title, "diverge-") {
			ht.Fatalf("expected strategy title %q to start with diverge-", title)
		}

		// Property 2: Length must never exceed 63 characters (RFC 1123 limit)
		if len(title) > 63 {
			ht.Fatalf("expected strategy title length <= 63, got %d (%s)", len(title), title)
		}

		// Property 3: Distinct namespaces produce distinct strategy titles
		titleAlt := UnleashStrategyTitle(nsName+"-diff", envName)
		if title == titleAlt {
			ht.Fatalf("expected different strategy titles for different namespaces")
		}
	})
}

// TestUnleashProvision_Property runs property-based tests verifying Unleash provisioning and strategy teardown across arbitrary overrides.
func TestUnleashProvision_Property(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		ts, mock := newMockUnleashServer("pbt-admin-token")
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
				"url":        []byte(ts.URL),
				"adminToken": []byte("pbt-admin-token"),
			},
		}

		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()
		provider := NewUnleashProvider(c, scheme, logr.Discard())

		env := &v1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      envName,
				Namespace: "default",
			},
			Spec: v1alpha1.EnvironmentSpec{
				Features: &v1alpha1.FeatureSpec{
					Provider:      "unleash",
					ConnectionRef: secName,
					Overrides:     overrides,
				},
			},
		}

		ctx := context.Background()
		res, err := provider.Provision(ctx, env)
		require.NoError(ht, err)
		assert.Equal(ht, "unleash", res.ProviderType)
		assert.Equal(ht, ts.URL+"/api", res.EnvVars["UNLEASH_URL"])
		expectedTitle := UnleashStrategyTitle(env.Namespace, envName)
		assert.Equal(ht, expectedTitle, res.EnvVars["UNLEASH_STRATEGY_TITLE"])

		// Verify on mock server
		mock.mu.Lock()
		for k, v := range overrides {
			assert.Contains(ht, mock.features, k)
			strats := mock.strategies[k]
			require.Len(ht, strats, 1)
			assert.Equal(ht, expectedTitle, strats[0].Title)
			switch strings.ToLower(strings.TrimSpace(v)) {
			case "true":
				assert.Equal(ht, "100", strats[0].Parameters["rollout"])
				assert.False(ht, strats[0].Disabled)
			case "false":
				assert.Equal(ht, "0", strats[0].Parameters["rollout"])
				assert.True(ht, strats[0].Disabled)
			default:
				assert.Equal(ht, "100", strats[0].Parameters["rollout"])
				assert.False(ht, strats[0].Disabled)
				require.Len(ht, strats[0].Variants, 1)
				assert.Equal(ht, v, strats[0].Variants[0].Payload.Value)
			}
		}
		mock.mu.Unlock()

		// Teardown removes all strategies for this preview environment
		err = provider.Teardown(ctx, env)
		require.NoError(ht, err)

		mock.mu.Lock()
		for k := range overrides {
			assert.Empty(ht, mock.strategies[k])
		}
		mock.mu.Unlock()
	})
}
