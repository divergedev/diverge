package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"

	"connectrpc.com/connect"
	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/divergedev/diverge/internal/server/auth"
)

// MaxStreamLogsPods is the maximum number of pod log streams per StreamLogs request.
const MaxStreamLogsPods = 5

// dns1123LabelRegex validates DNS-1123 label names (used for K8s names/namespaces).
var dns1123LabelRegex = regexp.MustCompile(`^[a-z0-9]([a-z0-9\-]{0,61}[a-z0-9])?$`)

// ValidateDNS1123Label checks that a string is a valid DNS-1123 label.
func ValidateDNS1123Label(value, field string) error {
	if value == "" {
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("%s is required", field))
	}
	if len(value) > 63 {
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("%s exceeds maximum length of 63 characters", field))
	}
	if !dns1123LabelRegex.MatchString(value) {
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("%s must be a valid DNS label (lowercase alphanumeric and hyphens)", field))
	}
	return nil
}

// checkSAR performs a SubjectAccessReview for the given user and resource attributes.
// This is the shared helper used by both AuthorizeAction and AuthorizePodLogs.
func checkSAR(ctx context.Context, k8sClient kubernetes.Interface, logger *slog.Logger, user *auth.UserInfo, attrs *authorizationv1.ResourceAttributes, denialMsg string) error {
	sar := &authorizationv1.SubjectAccessReview{
		Spec: authorizationv1.SubjectAccessReviewSpec{
			User:               user.Username,
			Groups:             user.Groups,
			ResourceAttributes: attrs,
		},
	}

	result, err := k8sClient.AuthorizationV1().SubjectAccessReviews().Create(ctx, sar, metav1.CreateOptions{})
	if err != nil {
		logger.Error("authz.error",
			"user", user.Username,
			"verb", attrs.Verb,
			"resource", attrs.Resource,
			"subresource", attrs.Subresource,
			"namespace", attrs.Namespace,
			"error", err,
		)
		return connect.NewError(connect.CodeInternal, errors.New("authorization check failed"))
	}

	if !result.Status.Allowed {
		logger.Warn("authz.denied",
			"user", user.Username,
			"groups", user.Groups,
			"verb", attrs.Verb,
			"resource", attrs.Resource,
			"subresource", attrs.Subresource,
			"namespace", attrs.Namespace,
		)
		return connect.NewError(connect.CodePermissionDenied, errors.New(denialMsg))
	}

	return nil
}

// AuthorizeAction performs a Kubernetes SubjectAccessReview for the authenticated
// user against a Diverge resource. The user's identity is extracted from the
// request context (set by the auth middleware).
func AuthorizeAction(ctx context.Context, k8sClient kubernetes.Interface, logger *slog.Logger, verb, namespace, resource string) error {
	user, ok := auth.UserInfoFromContext(ctx)
	if !ok {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("not authenticated"))
	}

	return checkSAR(ctx, k8sClient, logger, user, &authorizationv1.ResourceAttributes{
		Namespace: namespace,
		Verb:      verb,
		Group:     "diverge.dev",
		Resource:  resource,
	}, "permission denied")
}

// AuthorizePodLogs performs a SubjectAccessReview for pods/log access.
// StreamLogs requires both environment read AND pod log read permissions.
func AuthorizePodLogs(ctx context.Context, k8sClient kubernetes.Interface, logger *slog.Logger, namespace string) error {
	user, ok := auth.UserInfoFromContext(ctx)
	if !ok {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("not authenticated"))
	}

	return checkSAR(ctx, k8sClient, logger, user, &authorizationv1.ResourceAttributes{
		Namespace:   namespace,
		Verb:        "get",
		Group:       "",
		Resource:    "pods",
		Subresource: "log",
	}, "permission denied: requires get access to pods/log")
}

// ValidateNamespaceMatch ensures that the namespace in the request wrapper
// matches the namespace in the resource, preventing RBAC bypass where a user
// authorized for namespace "foo" creates a resource in namespace "bar".
// Both requestNS and resourceNS must be non-empty for comparison.
// Callers must ensure requestNS is defaulted before calling this function.
func ValidateNamespaceMatch(requestNS, resourceNS string) error {
	if requestNS == "" {
		return connect.NewError(connect.CodeInvalidArgument,
			errors.New("request namespace is required"))
	}
	if resourceNS != "" && resourceNS != requestNS {
		return connect.NewError(connect.CodeInvalidArgument,
			errors.New("namespace mismatch: request namespace does not match resource namespace"))
	}
	return nil
}
