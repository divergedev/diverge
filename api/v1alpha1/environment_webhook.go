package v1alpha1

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// SetupEnvironmentWebhookWithManager registers the validating webhook for Environment.
func SetupEnvironmentWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &Environment{}).
		WithValidator(&environmentValidator{}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-divergedev-com-v1alpha1-environment,mutating=false,failurePolicy=fail,sideEffects=None,groups=divergedev.com,resources=environments,verbs=create;update,versions=v1alpha1,name=venvironment.divergedev.com,admissionReviewVersions=v1

// environmentValidator validates Environment resources.
type environmentValidator struct{}

var _ admission.Validator[*Environment] = &environmentValidator{}

// ValidateCreate validates a new Environment.
func (v *environmentValidator) ValidateCreate(_ context.Context, env *Environment) (admission.Warnings, error) {
	return nil, ValidateEnvironment(env).ToAggregate()
}

// ValidateUpdate validates an updated Environment.
func (v *environmentValidator) ValidateUpdate(_ context.Context, _ *Environment, newEnv *Environment) (admission.Warnings, error) {
	if newEnv != nil && newEnv.DeletionTimestamp != nil {
		return nil, nil
	}
	return nil, ValidateEnvironment(newEnv).ToAggregate()
}

// ValidateDelete is a no-op for deletion.
func (v *environmentValidator) ValidateDelete(_ context.Context, _ *Environment) (admission.Warnings, error) {
	return nil, nil
}

// ValidateEnvironment performs structural and security validation on an Environment.
func ValidateEnvironment(env *Environment) field.ErrorList {
	var allErrs field.ErrorList
	specPath := field.NewPath("spec")

	// Blocked namespace check
	if blockedNamespaces[env.Namespace] {
		allErrs = append(allErrs, field.Forbidden(
			field.NewPath("metadata", "namespace"),
			fmt.Sprintf("namespace %q is blocked for preview environments", env.Namespace),
		))
	}

	// Validate source
	sourcePath := specPath.Child("source")
	provider := strings.TrimSpace(env.Spec.Source.Provider)
	switch provider {
	case "":
		allErrs = append(allErrs, field.Required(sourcePath.Child("provider"), "source provider is required"))
	case "github", "gitlab", "local":
	default:
		allErrs = append(allErrs, field.NotSupported(sourcePath.Child("provider"), env.Spec.Source.Provider, []string{"github", "gitlab", "local"}))
	}

	if strings.TrimSpace(env.Spec.Source.Branch) == "" {
		allErrs = append(allErrs, field.Required(sourcePath.Child("branch"), "source branch is required"))
	}

	// Validate cross-namespace SecretRefs
	allErrs = append(allErrs, validateSecretRefs(env.Spec.EnvFrom, env.Namespace, specPath.Child("envFrom"))...)
	allErrs = append(allErrs, validateSecretRefs(env.Spec.Deploy.EnvFrom, env.Namespace, specPath.Child("deploy", "envFrom"))...)
	if env.Spec.Database.MigrationJob != nil {
		allErrs = append(allErrs, validateSecretRefs(
			env.Spec.Database.MigrationJob.EnvFrom,
			env.Namespace,
			specPath.Child("database", "migrationJob", "envFrom"),
		)...)
	}

	// Validate routing
	if env.Spec.Routing.Mode == "subdomain" && strings.TrimSpace(env.Spec.Routing.BaseDomain) == "" {
		allErrs = append(allErrs, field.Required(
			specPath.Child("routing", "baseDomain"),
			"baseDomain is required when routing mode is \"subdomain\"",
		))
	}

	// Validate deploy.manifests
	if env.Spec.Deploy.Manifests != nil && env.Spec.Deploy.Manifests.Type == "url" {
		manifestsPath := specPath.Child("deploy", "manifests")
		u := strings.TrimSpace(env.Spec.Deploy.Manifests.URL)
		if u == "" {
			allErrs = append(allErrs, field.Required(manifestsPath.Child("url"), "url is required when manifests type is \"url\""))
		} else if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
			allErrs = append(allErrs, field.Invalid(manifestsPath.Child("url"), env.Spec.Deploy.Manifests.URL, "must start with http:// or https://"))
		}
	}

	// Validate database.atlas
	if env.Spec.Database.Atlas != nil {
		atlasPath := specPath.Child("database", "atlas")
		switch env.Spec.Database.Atlas.Mode {
		case "versioned":
			if strings.TrimSpace(env.Spec.Database.Atlas.MigrationConfigMap) == "" {
				allErrs = append(allErrs, field.Required(
					atlasPath.Child("migrationConfigMap"),
					"migrationConfigMap is required when atlas mode is \"versioned\"",
				))
			}
		case "declarative":
			if strings.TrimSpace(env.Spec.Database.Atlas.SchemaConfigMap) == "" {
				allErrs = append(allErrs, field.Required(
					atlasPath.Child("schemaConfigMap"),
					"schemaConfigMap is required when atlas mode is \"declarative\"",
				))
			}
		}
	}

	// Validate serviceConfig.endpoint
	if env.Spec.ServiceConfig != nil && env.Spec.ServiceConfig.Endpoint != "" {
		if err := validateEndpoint(env.Spec.ServiceConfig.Endpoint); err != nil {
			allErrs = append(allErrs, field.Invalid(
				specPath.Child("serviceConfig", "endpoint"),
				env.Spec.ServiceConfig.Endpoint,
				err.Error(),
			))
		}
	}

	return allErrs
}

func validateSecretRefs(refs []SecretRef, envNamespace string, path *field.Path) field.ErrorList {
	var errs field.ErrorList
	for i, ref := range refs {
		if ref.Namespace != "" && ref.Namespace != envNamespace {
			errs = append(errs, field.Forbidden(
				path.Index(i).Child("namespace"),
				"cross-namespace SecretRef is not allowed",
			))
		}
	}
	return errs
}
