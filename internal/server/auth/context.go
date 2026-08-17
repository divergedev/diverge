package auth

import "context"

// UserInfo represents the authenticated user's identity from a Kubernetes TokenReview.
type UserInfo struct {
	Username string
	UID      string
	Groups   []string
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
