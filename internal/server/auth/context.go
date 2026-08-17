package auth

import (
	"context"

	authorizationv1 "k8s.io/api/authorization/v1"
)

// UserInfo represents the authenticated user's identity from a Kubernetes TokenReview.
type UserInfo struct {
	Username string
	UID      string
	Groups   []string
	Extra    map[string]authorizationv1.ExtraValue
}

type contextKey string

const userInfoKey contextKey = "diverge.dev/userinfo"

// UserInfoFromContext extracts the authenticated user's identity from the context.
func UserInfoFromContext(ctx context.Context) (*UserInfo, bool) {
	info, ok := ctx.Value(userInfoKey).(*UserInfo)
	return info, ok
}

// ContextWithUserInfo returns a new context with the user's identity attached.
func ContextWithUserInfo(ctx context.Context, info *UserInfo) context.Context {
	return context.WithValue(ctx, userInfoKey, info)
}
