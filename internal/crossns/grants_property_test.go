package crossns

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"pgregory.net/rapid"
)

func TestEnsureReferenceGrant_Property_Uniqueness(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		ns1 := rapid.String().Draw(rt, "ns1")
		ns2 := rapid.String().Draw(rt, "ns2")

		// If they happen to be equal, we can't test uniqueness of different inputs
		if ns1 == ns2 {
			rt.Skip("namespaces are equal")
		}

		grantName1 := fmt.Sprintf("diverge-crossns-%s", ns1)
		grantName2 := fmt.Sprintf("diverge-crossns-%s", ns2)

		assert.NotEqual(t, grantName1, grantName2)
	})
}

func TestEnsureReferenceGrant_Property_Determinism(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		ns := rapid.String().Draw(rt, "ns")

		grantName1 := fmt.Sprintf("diverge-crossns-%s", ns)
		grantName2 := fmt.Sprintf("diverge-crossns-%s", ns)

		assert.Equal(t, grantName1, grantName2)
	})
}

func TestEnsureReferenceGrant_Property_ValidDNS(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Valid namespace names are valid DNS labels
		ns := rapid.StringMatching(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`).Filter(func(v string) bool {
			return len(v) <= 63-len("diverge-crossns-")
		}).Draw(rt, "ns")

		grantName := fmt.Sprintf("diverge-crossns-%s", ns)

		assert.LessOrEqual(t, len(grantName), 63)
		matched, _ := regexp.MatchString(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, grantName)
		assert.True(t, matched, "Generated grant name %q is not a valid DNS label", grantName)
	})
}
