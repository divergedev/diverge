package v1alpha1

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// SetupPreviewGroupWebhookWithManager registers the validating webhook.
func SetupPreviewGroupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(&PreviewGroup{}).
		WithValidator(&previewGroupValidator{}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-diverge-io-v1alpha1-previewgroup,mutating=false,failurePolicy=fail,sideEffects=None,groups=diverge.io,resources=previewgroups,verbs=create;update,versions=v1alpha1,name=vpreviewgroup.diverge.io,admissionReviewVersions=v1

// previewGroupValidator validates PreviewGroup resources.
type previewGroupValidator struct{}

var _ webhook.CustomValidator = &previewGroupValidator{}

// ValidateCreate validates a new PreviewGroup.
func (v *previewGroupValidator) ValidateCreate(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	pg, ok := obj.(*PreviewGroup)
	if !ok {
		return nil, fmt.Errorf("expected PreviewGroup, got %T", obj)
	}
	return nil, validatePreviewGroup(pg).ToAggregate()
}

// ValidateUpdate validates an updated PreviewGroup.
func (v *previewGroupValidator) ValidateUpdate(_ context.Context, _ runtime.Object, newObj runtime.Object) (admission.Warnings, error) {
	pg, ok := newObj.(*PreviewGroup)
	if !ok {
		return nil, fmt.Errorf("expected PreviewGroup, got %T", newObj)
	}
	return nil, validatePreviewGroup(pg).ToAggregate()
}

// ValidateDelete is a no-op for deletion.
func (v *previewGroupValidator) ValidateDelete(_ context.Context, _ runtime.Object) (admission.Warnings, error) {
	return nil, nil
}

// blockedNamespaces are namespaces that cannot be targeted by preview services.
var blockedNamespaces = map[string]bool{
	"kube-system":     true,
	"kube-public":     true,
	"kube-node-lease": true,
	"istio-system":    true,
}

// validatePreviewGroup performs structural validation on a PreviewGroup.
func validatePreviewGroup(pg *PreviewGroup) field.ErrorList {
	var allErrs field.ErrorList
	specPath := field.NewPath("spec")

	// Validate routing
	routingPath := specPath.Child("routing")
	if pg.Spec.Routing.HeaderValue == "" {
		allErrs = append(allErrs, field.Required(routingPath.Child("headerValue"), "routing header value is required"))
	}

	// Validate services
	servicesPath := specPath.Child("services")
	if len(pg.Spec.Services) == 0 {
		allErrs = append(allErrs, field.Required(servicesPath, "at least one service is required"))
	}

	serviceNames := make(map[string]bool)
	for i, svc := range pg.Spec.Services {
		svcPath := servicesPath.Index(i)
		allErrs = append(allErrs, validatePreviewGroupService(svc, svcPath, serviceNames)...)
	}

	return allErrs
}

// validatePreviewGroupService validates a single service spec within a PreviewGroup.
func validatePreviewGroupService(svc PreviewGroupServiceSpec, path *field.Path, seen map[string]bool) field.ErrorList {
	var errs field.ErrorList

	// Name is required
	if svc.Name == "" {
		errs = append(errs, field.Required(path.Child("name"), "service name is required"))
	}

	// Duplicate service names
	if seen[svc.Name] {
		errs = append(errs, field.Duplicate(path.Child("name"), svc.Name))
	}
	seen[svc.Name] = true

	// Mode-specific validation
	switch svc.Mode {
	case ServiceModeImage, "":
		if svc.Image == "" {
			errs = append(errs, field.Required(path.Child("image"), "image is required when mode is \"image\""))
		}
		if svc.Endpoint != "" {
			errs = append(errs, field.Invalid(path.Child("endpoint"), svc.Endpoint, "endpoint is only valid when mode is \"local\""))
		}
	case ServiceModeLocal:
		if svc.Endpoint == "" {
			errs = append(errs, field.Required(path.Child("endpoint"), "endpoint is required when mode is \"local\""))
		} else if err := validateEndpoint(svc.Endpoint); err != nil {
			errs = append(errs, field.Invalid(path.Child("endpoint"), svc.Endpoint, err.Error()))
		}
		if svc.Image != "" {
			errs = append(errs, field.Invalid(path.Child("image"), svc.Image, "image is not used when mode is \"local\""))
		}
	case ServiceModeBaseline:
		if svc.Image != "" {
			errs = append(errs, field.Invalid(path.Child("image"), svc.Image, "image is not used when mode is \"baseline\""))
		}
		if svc.Endpoint != "" {
			errs = append(errs, field.Invalid(path.Child("endpoint"), svc.Endpoint, "endpoint is not used when mode is \"baseline\""))
		}
	}

	// Blocked namespaces
	if svc.Namespace != "" && blockedNamespaces[svc.Namespace] {
		errs = append(errs, field.Forbidden(path.Child("namespace"),
			fmt.Sprintf("namespace %q is blocked for preview deployments", svc.Namespace)))
	}

	// PathPrefix must start with /
	if svc.PathPrefix != "" && !strings.HasPrefix(svc.PathPrefix, "/") {
		errs = append(errs, field.Invalid(path.Child("pathPrefix"), svc.PathPrefix, "must start with \"/\""))
	}

	return errs
}

// validateEndpoint checks that an endpoint is a valid host:port.
func validateEndpoint(endpoint string) error {
	host, portStr, err := net.SplitHostPort(endpoint)
	if err != nil {
		if net.ParseIP(endpoint) != nil {
			return fmt.Errorf("endpoint must include a port (e.g. %s:8080)", endpoint)
		}
		return fmt.Errorf("must be host:port (e.g. 100.64.23.42:8080): %v", err)
	}
	if host == "" {
		return fmt.Errorf("host cannot be empty")
	}
	if portStr == "" {
		return fmt.Errorf("port cannot be empty")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return fmt.Errorf("port must be numeric, got %q", portStr)
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535, got %d", port)
	}
	return nil
}

// previewGroupGVR is used for error formatting.
var _ = schema.GroupResource{Group: "diverge.io", Resource: "previewgroups"}
var _ = apierrors.StatusError{}
