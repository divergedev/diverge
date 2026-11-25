package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	authenticationv1 "k8s.io/api/authentication/v1"
	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// extractJWTExpiry extracts the exp claim from an already-verified JWT token so
// short-lived ServiceAccount tokens are not cached past their expiration.
// Returns zero time for non-JWT tokens or malformed/unusable exp claims.
func extractJWTExpiry(token string) time.Time {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[1] == "" {
		return time.Time{}
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		payload, err = base64.URLEncoding.DecodeString(parts[1])
		if err != nil {
			return time.Time{}
		}
	}
	var claims struct {
		Exp *float64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Exp == nil {
		return time.Time{}
	}
	exp := *claims.Exp
	if math.IsNaN(exp) || math.IsInf(exp, 0) || exp <= 0 || exp > 1e12 {
		return time.Time{}
	}
	sec, frac := math.Modf(exp)
	return time.Unix(int64(sec), int64(frac*float64(time.Second))).UTC()
}

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
		extraCount := 0
		extra = make(map[string]authorizationv1.ExtraValue, len(result.Status.User.Extra))
		for k, v := range result.Status.User.Extra {
			if extraCount >= 50 {
				slog.Warn("TokenReview returned more than 50 extra keys, truncating")
				break
			}
			val := make(authorizationv1.ExtraValue, len(v))
			copy(val, v)
			extra[k] = val
			extraCount++
		}
	}

	return &UserInfo{
		Username:  result.Status.User.Username,
		UID:       result.Status.User.UID,
		Groups:    result.Status.User.Groups,
		Extra:     extra,
		ExpiresAt: extractJWTExpiry(token),
		AuthType:  "tokenreview",
	}, nil
}
