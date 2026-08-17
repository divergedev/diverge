package server

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

func TestValidateDNS1123Label(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"valid", "my-label-123", false},
		{"empty", "", true},
		{"too long", string(make([]byte, 254)), true},
		{"invalid chars", "My_Label", true},
		{"uppercase", "Mylabel", true},
		{"starts with hyphen", "-mylabel", true},
		{"ends with hyphen", "mylabel-", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateDNS1123Label(tt.value, "field")
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestValidateNamespaceMatch(t *testing.T) {
	tests := []struct {
		name       string
		requestNS  string
		resourceNS string
		wantErr    bool
	}{
		{"match", "default", "default", false},
		{"mismatch", "default", "kube-system", true},
		{"empty request rejects", "", "default", true},
		{"empty resource ok", "default", "", false},
		{"both empty rejects", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateNamespaceMatch(tt.requestNS, tt.resourceNS)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestMaxStreamLogsPods(t *testing.T) {
	assert.Equal(t, 5, MaxStreamLogsPods)
}

func TestValidateDNS1123Label_PBT(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		str := rapid.String().Draw(t, "str")

		err := ValidateDNS1123Label(str, "field")

		hasUpper := strings.ToLower(str) != str
		hasSpecial := false
		for _, r := range str {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				hasSpecial = true
				break
			}
		}

		if hasUpper || hasSpecial {
			require.Error(t, err)
		}

		validPattern := regexp.MustCompile(`^[a-z0-9]([a-z0-9\-]{0,61}[a-z0-9])?$`)
		if validPattern.MatchString(str) {
			require.NoError(t, err)
		}
	})
}
