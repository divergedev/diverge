package secrets

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"hegel.dev/go/hegel"
)

func TestFileResolver_PathTraversalRejection_PBT(t *testing.T) {
	resolver := NewFileResolver()
	ctx := context.Background()

	hegel.Test(t, func(ht *hegel.T) {
		prefix := hegel.Draw(ht, hegel.Text().MinSize(0).MaxSize(20))
		suffix := hegel.Draw(ht, hegel.Text().MinSize(0).MaxSize(20))
		driveLetter := hegel.Draw(ht, hegel.SampledFrom([]string{"C", "D", "Z", "c"}))

		candidates := []string{
			prefix + ".." + suffix,
			"/" + prefix + suffix,
			`\` + prefix + suffix,
			driveLetter + ":" + prefix + suffix,
		}

		for _, badPath := range candidates {
			_, err := resolver.Resolve(ctx, SecretRef{Provider: "file", Path: badPath})
			require.Error(ht, err, "expected error for unsafe path %q", badPath)
			msg := err.Error()
			isRejected := strings.Contains(msg, "path traversal not allowed") ||
				strings.Contains(msg, "absolute paths not allowed")
			require.True(ht, isRejected, "expected traversal/absolute rejection for %q, got: %v", badPath, err)
		}
	})
}

func TestMultiResolver_SchemeAliasing_PBT(t *testing.T) {
	ctx := context.Background()

	hegel.Test(t, func(ht *hegel.T) {
		secretVal := hegel.Draw(ht, hegel.Text().MinSize(1).MaxSize(80))
		path := hegel.Draw(ht, hegel.Text().MinSize(1).MaxSize(40))
		key := hegel.Draw(ht, hegel.Text().MinSize(1).MaxSize(20))
		alias := hegel.Draw(ht, hegel.SampledFrom([]string{
			"openbao",
			"OpenBao",
			"OPENBAO",
			"bao",
			"Bao",
			"BAO",
		}))

		multi := NewMulti(map[string]Resolver{
			"vault": &mockResolver{val: secretVal},
		})

		vaultOut, err := multi.Resolve(ctx, SecretRef{
			Provider: "vault",
			Path:     path,
			Key:      key,
		})
		require.NoError(ht, err)

		aliasOut, err := multi.Resolve(ctx, SecretRef{
			Provider: alias,
			Path:     path,
			Key:      key,
		})
		require.NoError(ht, err)
		require.Equal(ht, vaultOut, aliasOut)
		require.Equal(ht, secretVal, aliasOut)
	})
}
