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

// TestFlagsmithIdentityName_Property runs property-based tests verifying prefix, character length, and determinism.
func TestFlagsmithIdentityName_Property(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		alphabet := []string{"a", "b", "c", "1", "2", "-", "_", "x", "y", "z"}
		nameLen := hegel.Draw(ht, hegel.Integers(0, 100))
		envName := ""
		for i := 0; i < nameLen; i++ {
			envName += hegel.Draw(ht, hegel.SampledFrom(alphabet))
		}

		id := FlagsmithIdentityName(envName)

		// Property 1: Must always start with "diverge-"
		if !strings.HasPrefix(id, "diverge-") {
			ht.Fatalf("expected identity %q to start with diverge-", id)
		}

		// Property 2: Length must never exceed 63 characters (RFC 1123 limit)
		if len(id) > 63 {
			ht.Fatalf("expected identity length <= 63, got %d (%s)", len(id), id)
		}

		// Property 3: If original was short enough, contains entire name
		if len(envName)+8 <= 63 {
			expected := fmt.Sprintf("diverge-%s", envName)
			if id != expected {
				ht.Fatalf("expected %s, got %s", expected, id)
			}
		}
	})
}

// TestFlagsmithProvision_Property runs property-based tests verifying provisioning and override invariants across arbitrary overrides.
func TestFlagsmithProvision_Property(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		ts, mock := newMockFlagsmithServer("pbt-env-key", "")
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
				"url":            []byte(ts.URL),
				"environmentKey": []byte("pbt-env-key"),
			},
		}

		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()
		provider := NewFlagsmithProvider(c, scheme, logr.Discard())

		env := &v1alpha1.Environment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      envName,
				Namespace: "default",
			},
			Spec: v1alpha1.EnvironmentSpec{
				Features: &v1alpha1.FeatureSpec{
					Provider:      "flagsmith",
					ConnectionRef: secName,
					Overrides:     overrides,
				},
			},
		}

		ctx := context.Background()
		res, err := provider.Provision(ctx, env)
		require.NoError(t, err)
		assert.Equal(t, "flagsmith", res.ProviderType)
		assert.Equal(t, ts.URL, res.EnvVars["FLAGSMITH_API_URL"])
		assert.Equal(t, FlagsmithIdentityName(envName), res.EnvVars["FLAGSMITH_IDENTITY"])

		// Verify on mock server
		mock.mu.Lock()
		identityKey := FlagsmithIdentityName(envName)
		assert.Contains(t, mock.identities, identityKey)
		states := mock.featureStates[identityKey]
		for k, v := range overrides {
			state, exists := states[k]
			assert.True(t, exists, "feature %s should exist in mock states", k)
			switch v {
			case "true":
				assert.True(t, state.Enabled)
			case "false":
				assert.False(t, state.Enabled)
			default:
				assert.True(t, state.Enabled)
				if intVal, intErr := strconv.ParseInt(v, 10, 64); intErr == nil {
					assert.EqualValues(t, intVal, state.FeatureStateValue)
				} else if floatVal, floatErr := strconv.ParseFloat(v, 64); floatErr == nil {
					assert.EqualValues(t, floatVal, state.FeatureStateValue)
				} else {
					assert.Equal(t, v, state.FeatureStateValue)
				}
			}
		}
		mock.mu.Unlock()

		// Teardown
		err = provider.Teardown(ctx, env)
		require.NoError(t, err)

		mock.mu.Lock()
		assert.NotContains(t, mock.identities, identityKey)
		mock.mu.Unlock()
	})
}
