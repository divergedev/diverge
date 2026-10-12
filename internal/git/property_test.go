package git

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
	"hegel.dev/go/hegel"
)

var validSlugRegex = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

func TestSlugifyBranch_AlwaysValidK8sLabel_PBT(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		branch := hegel.Draw(ht, hegel.Text().MinSize(0).MaxSize(200))

		slug := SlugifyBranch(branch)
		require.NotEmpty(ht, slug, "SlugifyBranch must never return empty string")
		require.LessOrEqual(ht, len(slug), 63, "SlugifyBranch must be <= 63 chars")
		require.True(ht, validSlugRegex.MatchString(slug), "SlugifyBranch(%q) = %q does not match valid K8s DNS-1123 label", branch, slug)
		require.Equal(ht, slug, SlugifyBranch(slug), "SlugifyBranch must be idempotent")
	})
}

func TestParseRemoteURL_SSHAndHTTPSEquivalence_PBT(t *testing.T) {
	hegel.Test(t, func(ht *hegel.T) {
		host := hegel.Draw(ht, hegel.SampledFrom([]string{
			"github.com",
			"gitlab.com",
			"bitbucket.org",
			"git.internal.example.com",
		}))
		ownerIdx := hegel.Draw(ht, hegel.Integers(1, 999))
		repoIdx := hegel.Draw(ht, hegel.Integers(1, 999))
		owner := fmt.Sprintf("org-%d", ownerIdx)
		repo := fmt.Sprintf("service-%d", repoIdx)

		sshURL := fmt.Sprintf("git@%s:%s/%s.git", host, owner, repo)
		httpsURL := fmt.Sprintf("https://%s/%s/%s.git", host, owner, repo)

		sshProvider, sshProject, err := ParseRemoteURL(sshURL)
		require.NoError(ht, err)

		httpsProvider, httpsProject, err := ParseRemoteURL(httpsURL)
		require.NoError(ht, err)

		require.Equal(ht, sshProvider, httpsProvider)
		require.Equal(ht, sshProject, httpsProject)
		require.Equal(ht, owner+"/"+repo, sshProject)
	})
}
