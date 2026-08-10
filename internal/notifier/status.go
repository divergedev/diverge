package notifier

import (
	"context"
	"fmt"
	"regexp"

	"github.com/divergedev/diverge/api/v1alpha1"
)

// StatusReporter posts commit status checks to SCM platforms for merge gating.
type StatusReporter interface {
	PostCommitStatus(ctx context.Context, env *v1alpha1.Environment, state, description string) error
}

// shaRegex matches valid git commit SHAs (short or full, hex only).
var shaRegex = regexp.MustCompile(`^[0-9a-fA-F]{4,64}$`)

// validateSHA ensures a commit SHA is safe for use in URL paths.
// Git SHAs are always hexadecimal; anything else is suspicious.
func validateSHA(sha string) error {
	if !shaRegex.MatchString(sha) {
		return fmt.Errorf("invalid commit SHA %q: must be hexadecimal", sha)
	}
	return nil
}
