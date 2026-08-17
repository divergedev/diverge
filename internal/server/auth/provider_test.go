package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	authenticationv1 "k8s.io/api/authentication/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	coretesting "k8s.io/client-go/testing"
)

func TestTokenReviewProvider_Authenticate(t *testing.T) {
	cases := []struct {
		name          string
		authenticated bool
		statusError   string
		clientError   error
		wantErr       bool
		wantUser      *UserInfo
	}{
		{"authenticated token", true, "", nil, false, &UserInfo{Username: "testuser", UID: "123", Groups: []string{"dev"}}},
		{"rejected token", false, "token expired", nil, true, nil},
		{"unauthenticated", false, "", nil, true, nil},
		{"client error", false, "", errors.New("client error"), true, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := fake.NewSimpleClientset()
			client.PrependReactor("create", "tokenreviews", func(action coretesting.Action) (handled bool, ret runtime.Object, err error) {
				if tc.clientError != nil {
					return true, nil, tc.clientError
				}
				tr := action.(coretesting.CreateAction).GetObject().(*authenticationv1.TokenReview)

				tr.Status = authenticationv1.TokenReviewStatus{
					Authenticated: tc.authenticated,
					Error:         tc.statusError,
				}
				if tc.authenticated && tc.wantUser != nil {
					tr.Status.User = authenticationv1.UserInfo{
						Username: tc.wantUser.Username,
						UID:      tc.wantUser.UID,
						Groups:   tc.wantUser.Groups,
					}
				}

				return true, tr, nil
			})

			provider := NewTokenReviewProvider(client, []string{"diverge"})
			got, err := provider.Authenticate(context.Background(), "my-token")

			if tc.wantErr {
				require.Error(t, err)
				assert.Nil(t, got)
				if tc.statusError != "" {
					assert.Contains(t, err.Error(), tc.statusError)
				}
			} else {
				require.NoError(t, err)
				require.NotNil(t, got)
				assert.Equal(t, tc.wantUser, got)
			}
		})
	}
}
