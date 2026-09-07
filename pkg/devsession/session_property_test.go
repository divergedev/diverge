package devsession

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"hegel.dev/go/hegel"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func genSessionPropString(ht *hegel.T, alphabet []string, minLen, maxLen int) string {
	length := hegel.Draw(ht, hegel.Integers(minLen, maxLen))
	res := ""
	for i := 0; i < length; i++ {
		res += hegel.Draw(ht, hegel.SampledFrom(alphabet))
	}
	return res
}

func TestDevSessionManager_Property(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		scheme := newTestScheme()
		client := fake.NewClientBuilder().WithScheme(scheme).Build()
		mgr := NewSessionManager(client)

		userAlphabet := []string{"a", "b", "c", "d", "e"}
		svcAlphabet := []string{"s", "v", "c", "-", "1", "2"}

		devA := genSessionPropString(ht, userAlphabet, 3, 6)
		devB := genSessionPropString(ht, userAlphabet, 3, 6)
		svc := genSessionPropString(ht, svcAlphabet, 3, 8)

		sessA := DevSession{
			ID:        "id-a",
			Service:   svc,
			Namespace: "default",
			Developer: devA,
			Hostname:  "host-a",
			Branch:    "branch-a",
		}

		_, conflicted, err := mgr.Acquire(context.Background(), sessA, ConflictPolicyBlock, false)
		if err != nil {
			ht.Fatalf("DevA acquire failed: %v", err)
		}
		if conflicted {
			ht.Fatalf("DevA unexpected conflict on empty cluster")
		}

		sessB := DevSession{
			ID:        "id-b",
			Service:   svc,
			Namespace: "default",
			Developer: devB,
			Hostname:  "host-b",
			Branch:    "branch-b",
		}

		policyStr := hegel.Draw(ht, hegel.SampledFrom([]string{"warn", "block", "allow"}))
		policy := ConflictPolicy(policyStr)
		force := hegel.Draw(ht, hegel.Booleans())

		_, confB, errB := mgr.Acquire(context.Background(), sessB, policy, force)

		if devA == devB {
			// Same developer: never a conflict regardless of policy/force
			assert.False(ht, confB)
			assert.NoError(ht, errB)
		} else {
			// Different developers on the same service
			if force || policy == ConflictPolicyAllow {
				assert.False(ht, confB)
				assert.NoError(ht, errB)
			} else if policy == ConflictPolicyBlock {
				assert.True(ht, confB)
				assert.Error(ht, errB)
			} else { // warn
				assert.True(ht, confB)
				assert.NoError(ht, errB)
			}
		}
	})
}
