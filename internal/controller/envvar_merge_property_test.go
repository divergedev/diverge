package controller

import (
	"testing"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/require"
	"hegel.dev/go/hegel"
)

func genEnvVarList(ht *hegel.T) []divergeiov1alpha1.EnvVar {
	size := hegel.Draw(ht, hegel.Integers(0, 10))
	var out []divergeiov1alpha1.EnvVar
	seen := make(map[string]bool)
	for i := 0; i < size; i++ {
		name := hegel.Draw(ht, hegel.Text().MinSize(1).MaxSize(10))
		if seen[name] {
			continue
		}
		seen[name] = true
		val := hegel.Draw(ht, hegel.Text().MaxSize(10))
		out = append(out, divergeiov1alpha1.EnvVar{Name: name, Value: val})
	}
	return out
}

func TestEnvVarMergeProperty(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		existing := genEnvVarList(ht)
		asyncVars := genEnvVarList(ht)

		merged, err := mergeEnvVars(existing, asyncVars)

		// Check properties
		if err == nil {
			// 1. No duplicates
			seen := make(map[string]bool)
			for _, v := range merged {
				require.False(ht, seen[v.Name], "duplicate env var name")
				seen[v.Name] = true
			}

			// 2. Idempotent
			merged2, err2 := mergeEnvVars(merged, asyncVars)
			require.NoError(ht, err2)
			require.Equal(ht, merged, merged2)

			// 4. Identity preservation (all inputs appear in output if no conflicts)
			for _, v := range existing {
				found := false
				for _, m := range merged {
					if m.Name == v.Name && m.Value == v.Value {
						found = true
						break
					}
				}
				require.True(ht, found, "existing var missing from merged")
			}
			for _, v := range asyncVars {
				found := false
				for _, m := range merged {
					if m.Name == v.Name && m.Value == v.Value {
						found = true
						break
					}
				}
				require.True(ht, found, "async var missing from merged")
			}
		} else {
			// 3. Conflict detection
			// Should only happen if same name but different value
			hasConflict := false
			outVars := make(map[string]string)
			for _, e := range existing {
				outVars[e.Name] = e.Value
			}
			for _, a := range asyncVars {
				if v, ok := outVars[a.Name]; ok && v != a.Value {
					hasConflict = true
					break
				}
				outVars[a.Name] = a.Value
			}
			require.True(ht, hasConflict, "error returned but no conflict found")
		}
	})
}
