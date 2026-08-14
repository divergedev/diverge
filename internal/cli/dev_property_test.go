package cli

import (
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"

	"github.com/divergedev/diverge/internal/git"
)

func TestProperty_GroupNameAlwaysValidK8s(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		username := rapid.StringMatching(`[a-z0-9_-]{1,20}`).Draw(t, "username")
		service := rapid.StringMatching(`[a-z0-9_-]{1,30}`).Draw(t, "service")
		groupName := fmt.Sprintf("dev-%s-%s", strings.ToLower(username), service)
		groupName = strings.ReplaceAll(groupName, "_", "-")
		groupName = strings.ToLower(groupName)
		groupName = strings.TrimRight(groupName, "-")
		// Assert valid K8s name
		require.Regexp(t, `^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`, groupName)
		require.LessOrEqual(t, len(groupName), 253)
	})
}

func TestProperty_HeaderValueAlwaysValidSlug(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		branch := rapid.StringMatching(`[a-zA-Z0-9_\-\./]{1,100}`).Draw(t, "branch")
		slug := git.SlugifyBranch(branch)
		require.Regexp(t, `^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`, slug)
		require.LessOrEqual(t, len(slug), 63)
	})
}

func TestProperty_EndpointAlwaysHostPort(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		ip1 := rapid.Byte().Draw(t, "ip1")
		ip2 := rapid.Byte().Draw(t, "ip2")
		ip3 := rapid.Byte().Draw(t, "ip3")
		ip4 := rapid.Byte().Draw(t, "ip4")
		ipStr := fmt.Sprintf("%d.%d.%d.%d", ip1, ip2, ip3, ip4)

		port := rapid.Int32Range(1, 65535).Draw(t, "port")

		endpoint := fmt.Sprintf("%s:%d", ipStr, port)

		host, p, err := net.SplitHostPort(endpoint)
		require.NoError(t, err)
		require.Equal(t, ipStr, host)
		require.Equal(t, fmt.Sprintf("%d", port), p)
	})
}
