package cli

import (
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"hegel.dev/go/hegel"

	"github.com/divergedev/diverge/internal/git"
)

var nameChars = []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n", "o", "p", "q", "r", "s", "t", "u", "v", "w", "x", "y", "z", "0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "_", "-"}
var branchChars = []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n", "o", "p", "q", "r", "s", "t", "u", "v", "w", "x", "y", "z", "A", "B", "C", "D", "E", "F", "G", "H", "I", "J", "K", "L", "M", "N", "O", "P", "Q", "R", "S", "T", "U", "V", "W", "X", "Y", "Z", "0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "_", "-", ".", "/"}

func genStr(ht *hegel.T, chars []string, maxLen int) string {
	length := hegel.Draw(ht, hegel.Integers(1, maxLen))
	res := ""
	for i := 0; i < length; i++ {
		res += hegel.Draw(ht, hegel.SampledFrom(chars))
	}
	return res
}

func TestProperty_GroupNameAlwaysValidK8s(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		username := genStr(ht, nameChars, 20)
		service := genStr(ht, nameChars, 30)
		groupName := fmt.Sprintf("dev-%s-%s", strings.ToLower(username), service)
		groupName = strings.ReplaceAll(groupName, "_", "-")
		groupName = strings.ToLower(groupName)
		groupName = strings.TrimRight(groupName, "-")
		// Assert valid K8s name
		require.Regexp(ht, `^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`, groupName)
		require.LessOrEqual(ht, len(groupName), 253)
	})
}

func TestProperty_HeaderValueAlwaysValidSlug(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		branch := genStr(ht, branchChars, 100)
		slug := git.SlugifyBranch(branch)
		require.Regexp(ht, `^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`, slug)
		require.LessOrEqual(ht, len(slug), 63)
	})
}

func TestProperty_EndpointAlwaysHostPort(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		ip1 := hegel.Draw(ht, hegel.Integers(0, 255))
		ip2 := hegel.Draw(ht, hegel.Integers(0, 255))
		ip3 := hegel.Draw(ht, hegel.Integers(0, 255))
		ip4 := hegel.Draw(ht, hegel.Integers(0, 255))
		ipStr := fmt.Sprintf("%d.%d.%d.%d", ip1, ip2, ip3, ip4)

		port := hegel.Draw(ht, hegel.Integers(1, 65535))

		endpoint := fmt.Sprintf("%s:%d", ipStr, port)

		host, p, err := net.SplitHostPort(endpoint)
		require.NoError(ht, err)
		require.Equal(ht, ipStr, host)
		require.Equal(ht, fmt.Sprintf("%d", port), p)
	})
}
