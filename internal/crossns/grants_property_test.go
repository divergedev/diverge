package crossns

import (
	"context"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hegel.dev/go/hegel"
	gatewayv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"
)

func TestEnsureReferenceGrant_Property_Uniqueness(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		ns1 := hegel.Draw(ht, hegel.Text())
		ns2 := hegel.Draw(ht, hegel.Text())

		if ns1 == ns2 {
			return
		}

		c := buildFakeClient()
		err := EnsureReferenceGrant(context.Background(), c, ns1, "to-ns")
		require.NoError(ht, err)
		err = EnsureReferenceGrant(context.Background(), c, ns2, "to-ns")
		require.NoError(ht, err)

		var grants gatewayv1beta1.ReferenceGrantList
		err = c.List(context.Background(), &grants)
		require.NoError(ht, err)
		assert.Len(ht, grants.Items, 2)
		assert.NotEqual(ht, grants.Items[0].Name, grants.Items[1].Name)
	})
}

func TestEnsureReferenceGrant_Property_Determinism(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		ns := hegel.Draw(ht, hegel.Text())

		c1 := buildFakeClient()
		err := EnsureReferenceGrant(context.Background(), c1, ns, "to-ns")
		require.NoError(ht, err)

		c2 := buildFakeClient()
		err = EnsureReferenceGrant(context.Background(), c2, ns, "to-ns")
		require.NoError(ht, err)

		var grants1 gatewayv1beta1.ReferenceGrantList
		err = c1.List(context.Background(), &grants1)
		require.NoError(ht, err)
		require.Len(ht, grants1.Items, 1)

		var grants2 gatewayv1beta1.ReferenceGrantList
		err = c2.List(context.Background(), &grants2)
		require.NoError(ht, err)
		require.Len(ht, grants2.Items, 1)

		assert.Equal(ht, grants1.Items[0].Name, grants2.Items[0].Name)
	})
}

var dnsFirstChars = []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n", "o", "p", "q", "r", "s", "t", "u", "v", "w", "x", "y", "z", "0", "1", "2", "3", "4", "5", "6", "7", "8", "9"}
var dnsMidChars = []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n", "o", "p", "q", "r", "s", "t", "u", "v", "w", "x", "y", "z", "0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "-"}

func genDNSName(ht *hegel.T, maxLen int) string {
	length := hegel.Draw(ht, hegel.Integers(1, maxLen))
	first := hegel.Draw(ht, hegel.SampledFrom(dnsFirstChars))
	if length == 1 {
		return first
	}
	rest := ""
	for i := 0; i < length-2; i++ {
		rest += hegel.Draw(ht, hegel.SampledFrom(dnsMidChars))
	}
	return first + rest + hegel.Draw(ht, hegel.SampledFrom(dnsFirstChars))
}

func TestEnsureReferenceGrant_Property_ValidDNS(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		ns := genDNSName(ht, 63-len("diverge-crossns-"))

		c := buildFakeClient()
		err := EnsureReferenceGrant(context.Background(), c, ns, "to-ns")
		require.NoError(ht, err)

		var grants gatewayv1beta1.ReferenceGrantList
		err = c.List(context.Background(), &grants)
		require.NoError(ht, err)
		require.Len(ht, grants.Items, 1)

		grantName := grants.Items[0].Name
		assert.LessOrEqual(ht, len(grantName), 63)
		matched, _ := regexp.MatchString(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, grantName)
		assert.True(ht, matched, "Generated grant name %q is not a valid DNS label", grantName)
	})
}
