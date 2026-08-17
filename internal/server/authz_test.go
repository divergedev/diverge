package server

import (
	"context"
	"log/slog"
	"regexp"
	"strings"
	"testing"

	"github.com/divergedev/diverge/internal/server/auth"
	authorizationv1 "k8s.io/api/authorization/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	coretesting "k8s.io/client-go/testing"

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

func TestAuthorizeAction_PassesUIDAndExtra(t *testing.T) {
	client := fake.NewSimpleClientset()
	var capturedSAR *authorizationv1.SubjectAccessReview
	client.PrependReactor("create", "subjectaccessreviews", func(action coretesting.Action) (handled bool, ret runtime.Object, err error) {
		capturedSAR = action.(coretesting.CreateAction).GetObject().(*authorizationv1.SubjectAccessReview)
		capturedSAR.Status.Allowed = true
		return true, capturedSAR, nil
	})

	ctx := context.Background()
	info := &auth.UserInfo{
		Username: "bob",
		UID:      "u-123",
		Groups:   []string{"admins"},
		Extra: map[string]authorizationv1.ExtraValue{
			"custom": {"val1", "val2"},
		},
	}
	ctx = auth.ContextWithUserInfo(ctx, info)

	logger := slog.Default()
	err := AuthorizeAction(ctx, client, logger, "get", "default", "things")
	require.NoError(t, err)
	require.NotNil(t, capturedSAR)
	assert.Equal(t, "bob", capturedSAR.Spec.User)
	assert.Equal(t, "u-123", capturedSAR.Spec.UID)
	assert.Equal(t, info.Groups, capturedSAR.Spec.Groups)
	assert.Equal(t, info.Extra, capturedSAR.Spec.Extra)
}
