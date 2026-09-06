package features

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/pkg/registry"
)

func init() {
	Providers.Register("configmap", registry.Provider[FeatureProvider]{
		Create: func(deps registry.Deps) (FeatureProvider, error) {
			return NewConfigMapProvider(deps.Client, deps.Scheme, deps.Logger), nil
		},
		Description: "Kubernetes ConfigMap feature flag provider (flagd & OpenFeature file provider)",
	})
}

// ConfigMapProvider manages feature flag overrides as a Kubernetes ConfigMap formatted for flagd and OpenFeature.
type ConfigMapProvider struct {
	client client.Client
	scheme *runtime.Scheme
	logger logr.Logger
}

// NewConfigMapProvider creates a new ConfigMapProvider.
func NewConfigMapProvider(c client.Client, scheme *runtime.Scheme, logger logr.Logger) *ConfigMapProvider {
	return &ConfigMapProvider{
		client: c,
		scheme: scheme,
		logger: logger,
	}
}

// Type returns "configmap".
func (p *ConfigMapProvider) Type() string {
	return "configmap"
}

// FlagdDefinition represents the JSON structure expected by flagd / OpenFeature file providers.
type FlagdDefinition struct {
	Flags map[string]FlagdFlag `json:"flags"`
}

// FlagdFlag represents a single flag rule in flagd format.
type FlagdFlag struct {
	State          string                 `json:"state"`
	Variants       map[string]interface{} `json:"variants"`
	DefaultVariant string                 `json:"defaultVariant"`
	Targeting      interface{}            `json:"targeting,omitempty"`
}

// ConfigMapName returns the ConfigMap resource name for a given environment.
func ConfigMapName(envName string) string {
	cmName := fmt.Sprintf("diverge-features-%s", envName)
	if len(cmName) > 253 {
		cmName = cmName[:253]
	}
	return cmName
}

// BuildFlagdJSON converts key-value overrides into flagd JSON format.
// When envName is non-empty, JSONLogic targeting rules are generated so that
// the overrides only apply when diverge.environment matches the environment.
func BuildFlagdJSON(overrides map[string]string, envName string) ([]byte, error) {
	flags := make(map[string]FlagdFlag, len(overrides))

	for k, v := range overrides {
		var flag FlagdFlag
		flag.State = "ENABLED"

		var targetVariant, fallbackVariant string
		var variants map[string]interface{}

		switch v {
		case "true":
			variants = map[string]interface{}{
				"on":  true,
				"off": false,
			}
			targetVariant = "on"
			fallbackVariant = "off"
		case "false":
			variants = map[string]interface{}{
				"on":  true,
				"off": false,
			}
			targetVariant = "off"
			fallbackVariant = "on"
		default:
			if intVal, err := strconv.ParseInt(v, 10, 64); err == nil {
				if envName != "" {
					variants = map[string]interface{}{
						"value":   intVal,
						"default": int64(0),
					}
				} else {
					variants = map[string]interface{}{
						"value": intVal,
					}
				}
				targetVariant = "value"
				fallbackVariant = "default"
			} else if floatVal, err := strconv.ParseFloat(v, 64); err == nil {
				if envName != "" {
					variants = map[string]interface{}{
						"value":   floatVal,
						"default": float64(0),
					}
				} else {
					variants = map[string]interface{}{
						"value": floatVal,
					}
				}
				targetVariant = "value"
				fallbackVariant = "default"
			} else {
				if envName != "" {
					variants = map[string]interface{}{
						"value":   v,
						"default": "",
					}
				} else {
					variants = map[string]interface{}{
						"value": v,
					}
				}
				targetVariant = "value"
				fallbackVariant = "default"
			}
		}

		flag.Variants = variants

		if envName != "" {
			flag.DefaultVariant = fallbackVariant
			flag.Targeting = map[string]interface{}{
				"if": []interface{}{
					map[string]interface{}{
						"==": []interface{}{
							map[string]interface{}{"var": "diverge.environment"},
							envName,
						},
					},
					targetVariant,
					fallbackVariant,
				},
			}
		} else {
			flag.DefaultVariant = targetVariant
		}

		flags[k] = flag
	}

	def := FlagdDefinition{Flags: flags}
	return json.MarshalIndent(def, "", "  ")
}

// Provision creates or updates a ConfigMap holding feature flag configurations.
func (p *ConfigMapProvider) Provision(ctx context.Context, env *v1alpha1.Environment) (*FeatureResult, error) {
	if env == nil {
		return nil, fmt.Errorf("environment cannot be nil")
	}

	targetNS := env.Namespace
	if env.Spec.Deploy.Namespace == "create" {
		targetNS = env.PreviewNamespace()
	}

	cmName := ConfigMapName(env.Name)

	var overrides map[string]string
	if env.Spec.Features != nil && env.Spec.Features.Overrides != nil {
		overrides = env.Spec.Features.Overrides
	} else {
		overrides = make(map[string]string)
	}

	flagdJSON, err := BuildFlagdJSON(overrides, env.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to build flagd JSON: %w", err)
	}

	rawOverridesJSON, err := json.MarshalIndent(overrides, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal raw overrides JSON: %w", err)
	}

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cmName,
			Namespace: targetNS,
			Labels: map[string]string{
				"divergedev.com/environment": env.Name,
				"divergedev.com/managed-by":  "diverge",
			},
		},
	}

	if targetNS == env.Namespace && p.scheme != nil {
		if err := controllerutil.SetControllerReference(env, cm, p.scheme); err != nil {
			return nil, fmt.Errorf("failed to set owner reference on feature configmap: %w", err)
		}
	}

	_, err = controllerutil.CreateOrUpdate(ctx, p.client, cm, func() error {
		if cm.Data == nil {
			cm.Data = make(map[string]string)
		}
		cm.Data["flags.json"] = string(flagdJSON)
		cm.Data["overrides.json"] = string(rawOverridesJSON)
		for k, v := range overrides {
			cm.Data[k] = v
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create or update feature configmap: %w", err)
	}

	return &FeatureResult{
		ProviderType:  "configmap",
		ConfigMapName: cmName,
		EnvVars: map[string]string{
			"DIVERGE_FEATURE_CONFIGMAP": cmName,
			"FLAGD_FLAG_PATH":           "/etc/diverge/flags/flags.json",
		},
		Message: fmt.Sprintf("Provisioned %d feature overrides in ConfigMap %s", len(overrides), cmName),
	}, nil
}

// Teardown deletes the feature ConfigMap.
func (p *ConfigMapProvider) Teardown(ctx context.Context, env *v1alpha1.Environment) error {
	if env == nil {
		return nil
	}

	targetNS := env.Namespace
	if env.Spec.Deploy.Namespace == "create" {
		targetNS = env.PreviewNamespace()
	}

	cmName := ConfigMapName(env.Name)
	existing := &corev1.ConfigMap{}
	err := p.client.Get(ctx, types.NamespacedName{Name: cmName, Namespace: targetNS}, existing)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to get feature configmap during teardown: %w", err)
	}

	if err := p.client.Delete(ctx, existing); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete feature configmap: %w", err)
	}

	return nil
}

// Status returns whether the feature ConfigMap is ready.
func (p *ConfigMapProvider) Status(ctx context.Context, env *v1alpha1.Environment) (*FeatureStatus, error) {
	if env == nil {
		return &FeatureStatus{Ready: false, Message: "Environment is nil"}, nil
	}

	targetNS := env.Namespace
	if env.Spec.Deploy.Namespace == "create" {
		targetNS = env.PreviewNamespace()
	}

	cmName := ConfigMapName(env.Name)
	existing := &corev1.ConfigMap{}
	err := p.client.Get(ctx, types.NamespacedName{Name: cmName, Namespace: targetNS}, existing)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// If no overrides are requested, it is considered ready
			if env.Spec.Features == nil || len(env.Spec.Features.Overrides) == 0 {
				return &FeatureStatus{Ready: true, Message: "No feature overrides configured"}, nil
			}
			return &FeatureStatus{Ready: false, Message: fmt.Sprintf("ConfigMap %s not found", cmName)}, nil
		}
		return nil, fmt.Errorf("failed to get feature configmap: %w", err)
	}

	return &FeatureStatus{
		Ready:   true,
		Message: fmt.Sprintf("ConfigMap %s ready", cmName),
	}, nil
}
