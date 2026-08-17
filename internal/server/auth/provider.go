package auth

import (
	"context"
	"fmt"

	authenticationv1 "k8s.io/api/authentication/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// AuthProvider authenticates a bearer token and returns user identity.
type AuthProvider interface {
	Authenticate(ctx context.Context, token string) (*UserInfo, error)
}

// TokenReviewProvider authenticates tokens via the Kubernetes TokenReview API.
type TokenReviewProvider struct {
	client    kubernetes.Interface
	audiences []string
}

// NewTokenReviewProvider creates a provider that validates tokens against the kube-apiserver.
func NewTokenReviewProvider(client kubernetes.Interface, audiences []string) *TokenReviewProvider {
	return &TokenReviewProvider{client: client, audiences: audiences}
}

func (p *TokenReviewProvider) Authenticate(ctx context.Context, token string) (*UserInfo, error) {
	review := &authenticationv1.TokenReview{
		Spec: authenticationv1.TokenReviewSpec{
			Token:     token,
			Audiences: p.audiences,
		},
	}

	result, err := p.client.AuthenticationV1().TokenReviews().Create(ctx, review, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("token review failed: %w", err)
	}

	if !result.Status.Authenticated {
		return nil, fmt.Errorf("token not authenticated: %s", result.Status.Error)
	}

	var extra map[string]authorizationv1.ExtraValue
	if len(result.Status.User.Extra) > 0 {
		extra = make(map[string]authorizationv1.ExtraValue, len(result.Status.User.Extra))
		for k, v := range result.Status.User.Extra {
			extra[k] = authorizationv1.ExtraValue(v)
		}
	}

	return &UserInfo{
		Username: result.Status.User.Username,
		UID:      result.Status.User.UID,
		Groups:   result.Status.User.Groups,
		Extra:    extra,
	}, nil
}
