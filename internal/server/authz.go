package server

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"connectrpc.com/connect"
	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/divergedev/diverge/api/v1alpha1"
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

func isMacaroonUser(user *auth.UserInfo) bool {
	if user == nil || len(user.Extra) == 0 {
		return false
	}
	vals := user.Extra[auth.ExtraKeyAuthType]
	return len(vals) > 0 && vals[0] == "macaroon"
}

func candidateToolsForAction(verb, resource string) []string {
	switch resource {
	case "environments":
		switch verb {
		case "get":
			return []string{"diverge_get_environment", "diverge_wait_for_ready", "diverge_fetch_errors", "diverge_list_hook_jobs", "diverge_doctor"}
		case "list", "watch":
			return []string{"diverge_list_environments", "diverge_watch_environments"}
		case "create":
			return []string{"diverge_create_environment"}
		case "update":
			return []string{"diverge_update_environment", "diverge_extend_ttl"}
		case "retry-hook":
			return []string{"diverge_retry_hook"}
		case "delete":
			return []string{"diverge_delete_environment"}
		}
	case "jobs":
		switch verb {
		case "get", "list", "watch":
			return []string{"diverge_list_hook_jobs", "diverge_fetch_errors"}
		case "create", "delete", "retry-hook":
			return []string{"diverge_retry_hook"}
		}
	case "previewgroups":
		switch verb {
		case "get":
			return []string{"diverge_get_preview_group"}
		case "list", "watch":
			return []string{"diverge_list_preview_groups", "diverge_watch_preview_groups"}
		case "create":
			return []string{"diverge_create_preview_group"}
		case "update":
			return []string{"diverge_update_preview_group"}
		case "delete":
			return []string{"diverge_delete_preview_group"}
		}
	case "agenttasks":
		switch verb {
		case "get":
			return []string{"diverge_get_task", "diverge_stream_task_logs"}
		case "list", "watch":
			return []string{"diverge_list_tasks"}
		case "create":
			return []string{"diverge_create_task"}
		case "update":
			return []string{"diverge_pause_task", "diverge_resume_task", "diverge_guide_task"}
		case "delete":
			return []string{"diverge_delete_task"}
		}
	case "pods/log":
		return []string{"diverge_stream_logs", "diverge_stream_task_logs", "diverge_fetch_errors", "diverge_get_task", "diverge_get_environment"}
	}
	singular := strings.TrimSuffix(resource, "s")
	return []string{
		fmt.Sprintf("diverge_%s_%s", verb, singular),
		fmt.Sprintf("diverge_%s_%s", verb, resource),
	}
}

func authorizeMacaroon(ctx context.Context, auditLogger *AuditLogger, user *auth.UserInfo, verb, namespace, resource, denialMsg string) error {
	boundNS := ""
	if nsVals := user.Extra[auth.ExtraKeyNamespace]; len(nsVals) > 0 {
		boundNS = nsVals[0]
	}
	// Task capability tokens must never manage AgentTask lifecycle, list other tasks, or modify budgets.
	if resource == "agenttasks" && verb != "get" {
		if auditLogger != nil {
			auditLogger.LogAuthz(ctx, "authz.denied", user, verb, resource, namespace)
		}
		return connect.NewError(connect.CodePermissionDenied, errors.New(denialMsg))
	}
	// Macaroon capability tokens are strictly namespace-bound; cluster-wide or cross-namespace access is denied.
	if boundNS == "" || namespace == "" || namespace != boundNS {
		if auditLogger != nil {
			auditLogger.LogAuthz(ctx, "authz.denied", user, verb, resource, namespace)
		}
		return connect.NewError(connect.CodePermissionDenied, errors.New(denialMsg))
	}

	allowedPatterns := user.Extra[auth.ExtraKeyAllowedTools]
	candidates := candidateToolsForAction(verb, resource)
	allowed := false
	for _, pattern := range allowedPatterns {
		for _, candidate := range candidates {
			if matched, err := filepath.Match(pattern, candidate); err == nil && matched {
				allowed = true
				break
			}
		}
		if allowed {
			break
		}
	}
	if !allowed {
		if auditLogger != nil {
			auditLogger.LogAuthz(ctx, "authz.denied", user, verb, resource, namespace)
		}
		return connect.NewError(connect.CodePermissionDenied, errors.New(denialMsg))
	}
	return nil
}

// AuthorizeTaskObjectAccess ensures that when a request is authenticated with a task-bound
// Macaroon capability token, it can only access the specific AgentTask named in ExtraKeyTaskID.
func AuthorizeTaskObjectAccess(ctx context.Context, auditLogger *AuditLogger, verb, namespace, taskName string) error {
	user, ok := auth.UserInfoFromContext(ctx)
	if !ok {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("not authenticated"))
	}
	if !isMacaroonUser(user) {
		return nil
	}
	boundTask := ""
	if taskVals := user.Extra[auth.ExtraKeyTaskID]; len(taskVals) > 0 {
		boundTask = taskVals[0]
	}
	if boundTask == "" || taskName == "" || boundTask != taskName {
		if auditLogger != nil {
			auditLogger.LogAuthz(ctx, "authz.denied", user, verb, "agenttasks", namespace)
		}
		return connect.NewError(connect.CodePermissionDenied, errors.New("permission denied"))
	}
	return nil
}

// checkSAR performs a SubjectAccessReview for the given user and resource attributes.
// This is the shared helper used by both AuthorizeAction and AuthorizePodLogs.
func checkSAR(ctx context.Context, k8sClient kubernetes.Interface, auditLogger *AuditLogger, user *auth.UserInfo, attrs *authorizationv1.ResourceAttributes, denialMsg string) error {
	sar := &authorizationv1.SubjectAccessReview{
		Spec: authorizationv1.SubjectAccessReviewSpec{
			User:               user.Username,
			UID:                user.UID,
			Groups:             user.Groups,
			Extra:              user.Extra,
			ResourceAttributes: attrs,
		},
	}

	result, err := k8sClient.AuthorizationV1().SubjectAccessReviews().Create(ctx, sar, metav1.CreateOptions{})
	if err != nil {
		resource := attrs.Resource
		if attrs.Subresource != "" {
			resource = resource + "/" + attrs.Subresource
		}
		if auditLogger != nil {
			auditLogger.LogAuthz(ctx, "authz.error", user, attrs.Verb, resource, attrs.Namespace)
		}
		return connect.NewError(connect.CodeInternal, errors.New("authorization check failed"))
	}

	if !result.Status.Allowed {
		resource := attrs.Resource
		if attrs.Subresource != "" {
			resource = resource + "/" + attrs.Subresource
		}
		if auditLogger != nil {
			auditLogger.LogAuthz(ctx, "authz.denied", user, attrs.Verb, resource, attrs.Namespace)
		}
		return connect.NewError(connect.CodePermissionDenied, errors.New(denialMsg))
	}

	return nil
}

// AuthorizeAction performs a Kubernetes SubjectAccessReview for the authenticated
// user against a Diverge resource. The user's identity is extracted from the
// request context (set by the auth middleware).
func AuthorizeAction(ctx context.Context, k8sClient kubernetes.Interface, auditLogger *AuditLogger, verb, namespace, resource string) error {
	user, ok := auth.UserInfoFromContext(ctx)
	if !ok {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("not authenticated"))
	}
	if isMacaroonUser(user) {
		return authorizeMacaroon(ctx, auditLogger, user, verb, namespace, resource, "permission denied")
	}

	return checkSAR(ctx, k8sClient, auditLogger, user, &authorizationv1.ResourceAttributes{
		Namespace: namespace,
		Verb:      verb,
		Group:     v1alpha1.GroupVersion.Group,
		Resource:  resource,
	}, "permission denied")
}

// AuthorizeRetryHook checks authorization for retrying a hook Job. For Macaroon capability tokens,
// it checks specifically for the diverge_retry_hook tool capability in the bound namespace; for
// Kubernetes RBAC principals, it requires both jobs/create and environments/update permissions.
func AuthorizeRetryHook(ctx context.Context, k8sClient kubernetes.Interface, auditLogger *AuditLogger, namespace string) error {
	user, ok := auth.UserInfoFromContext(ctx)
	if !ok {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("not authenticated"))
	}
	if isMacaroonUser(user) {
		return authorizeMacaroon(ctx, auditLogger, user, "retry-hook", namespace, "environments", "permission denied")
	}
	if err := AuthorizeAction(ctx, k8sClient, auditLogger, "create", namespace, "jobs"); err != nil {
		return err
	}
	return AuthorizeAction(ctx, k8sClient, auditLogger, "update", namespace, "environments")
}

// AuthorizePodLogs performs a SubjectAccessReview for pods/log access.
// StreamLogs requires both environment read AND pod log read permissions.
func AuthorizePodLogs(ctx context.Context, k8sClient kubernetes.Interface, auditLogger *AuditLogger, namespace string) error {
	user, ok := auth.UserInfoFromContext(ctx)
	if !ok {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("not authenticated"))
	}
	if isMacaroonUser(user) {
		return authorizeMacaroon(ctx, auditLogger, user, "get", namespace, "pods/log", "permission denied: requires get access to pods/log")
	}

	return checkSAR(ctx, k8sClient, auditLogger, user, &authorizationv1.ResourceAttributes{
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
