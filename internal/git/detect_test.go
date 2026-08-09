package git

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseRemoteURL(t *testing.T) {
	tests := []struct {
		name         string
		rawURL       string
		wantProvider string
		wantProject  string
		wantErr      bool
	}{
		{
			name:         "GitHub SSH",
			rawURL:       "git@github.com:divergedev/diverge.git",
			wantProvider: "github",
			wantProject:  "divergedev/diverge",
			wantErr:      false,
		},
		{
			name:         "GitHub HTTPS",
			rawURL:       "https://github.com/divergedev/diverge.git",
			wantProvider: "github",
			wantProject:  "divergedev/diverge",
			wantErr:      false,
		},
		{
			name:         "GitLab SSH",
			rawURL:       "git@gitlab.com:invenero/engineering/patient-insights.git",
			wantProvider: "gitlab",
			wantProject:  "invenero/engineering/patient-insights",
			wantErr:      false,
		},
		{
			name:         "GitLab HTTPS",
			rawURL:       "https://gitlab.com/invenero/engineering/patient-insights.git",
			wantProvider: "gitlab",
			wantProject:  "invenero/engineering/patient-insights",
			wantErr:      false,
		},
		{
			name:         "Without .git suffix",
			rawURL:       "https://github.com/divergedev/diverge",
			wantProvider: "github",
			wantProject:  "divergedev/diverge",
			wantErr:      false,
		},
		{
			name:         "Self-hosted",
			rawURL:       "git@git.example.com:myorg/myrepo.git",
			wantProvider: "gitlab",
			wantProject:  "myorg/myrepo",
			wantErr:      false,
		},
		{
			name:         "Invalid URL empty",
			rawURL:       "",
			wantProvider: "",
			wantProject:  "",
			wantErr:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotProvider, gotProject, err := ParseRemoteURL(tt.rawURL)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.wantProvider, gotProvider)
				assert.Equal(t, tt.wantProject, gotProject)
			}
		})
	}
}

func TestSlugifyBranch(t *testing.T) {
	tests := []struct {
		name   string
		branch string
		want   string
	}{
		{
			name:   "basic feature branch",
			branch: "feat/my-feature",
			want:   "feat-my-feature",
		},
		{
			name:   "with underscores and caps",
			branch: "fix/JIRA-123_some_thing",
			want:   "fix-jira-123-some-thing",
		},
		{
			name:   "main",
			branch: "main",
			want:   "main",
		},
		{
			name:   "very long branch name truncated to 63 chars",
			branch: "feat/this-is-a-very-very-long-branch-name-that-is-way-longer-than-sixty-three-characters",
			want:   "feat-this-is-a-very-very-long-branch-name-that-is-way-longer-th", // 63 chars
		},
		{
			name:   "special chars",
			branch: "feat/special--chars!!",
			want:   "feat-special-chars",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SlugifyBranch(tt.branch)
			assert.Equal(t, tt.want, got)
			assert.LessOrEqual(t, len(got), 63)
		})
	}
}
