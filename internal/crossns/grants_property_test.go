package crossns

import (
	"context"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
	gatewayv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"
)

func TestEnsureReferenceGrant_Property_Uniqueness(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		ns1 := rapid.String().Draw(rt, "ns1")
		ns2 := rapid.String().Draw(rt, "ns2")

		if ns1 == ns2 {
			rt.Skip("namespaces are equal")
		}

		c := buildFakeClient()
		err := EnsureReferenceGrant(context.Background(), c, ns1, "to-ns")
		require.NoError(t, err)
		err = EnsureReferenceGrant(context.Background(), c, ns2, "to-ns")
		require.NoError(t, err)

		var grants gatewayv1beta1.ReferenceGrantList
		err = c.List(context.Background(), &grants)
		require.NoError(t, err)
		assert.Len(t, grants.Items, 2)
		assert.NotEqual(t, grants.Items[0].Name, grants.Items[1].Name)
	})
}

func TestEnsureReferenceGrant_Property_Determinism(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		ns := rapid.String().Draw(rt, "ns")

		c1 := buildFakeClient()
		err := EnsureReferenceGrant(context.Background(), c1, ns, "to-ns")
		require.NoError(t, err)

		c2 := buildFakeClient()
		err = EnsureReferenceGrant(context.Background(), c2, ns, "to-ns")
		require.NoError(t, err)

		var grants1 gatewayv1beta1.ReferenceGrantList
		err = c1.List(context.Background(), &grants1)
		require.NoError(t, err)
		require.Len(t, grants1.Items, 1)

		var grants2 gatewayv1beta1.ReferenceGrantList
		err = c2.List(context.Background(), &grants2)
		require.NoError(t, err)
		require.Len(t, grants2.Items, 1)

		assert.Equal(t, grants1.Items[0].Name, grants2.Items[0].Name)
	})
}

func TestEnsureReferenceGrant_Property_ValidDNS(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		ns := rapid.StringMatching(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`).Filter(func(v string) bool {
			return len(v) <= 63-len("diverge-crossns-")
		}).Draw(rt, "ns")

		c := buildFakeClient()
		err := EnsureReferenceGrant(context.Background(), c, ns, "to-ns")
		require.NoError(t, err)

		var grants gatewayv1beta1.ReferenceGrantList
		err = c.List(context.Background(), &grants)
		require.NoError(t, err)
		require.Len(t, grants.Items, 1)

		grantName := grants.Items[0].Name
		assert.LessOrEqual(t, len(grantName), 63)
		matched, _ := regexp.MatchString(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, grantName)
		assert.True(t, matched, "Generated grant name %q is not a valid DNS label", grantName)
	})
}
