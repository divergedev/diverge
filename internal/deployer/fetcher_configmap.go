package deployer

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sort"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/divergedev/diverge/api/v1alpha1"
)

// ConfigMapFetcher reads pre-rendered YAML manifests from ConfigMaps
// created by CI pipelines. It looks for ConfigMaps with the labels
// diverge.io/manifests=true and diverge.io/environment=<envName>.
type ConfigMapFetcher struct {
	Client client.Client
}

func (f *ConfigMapFetcher) Fetch(ctx context.Context, env *v1alpha1.Environment) ([]unstructured.Unstructured, error) {
	// Determine target namespace
	ns := env.Namespace
	if env.Spec.Deploy.Namespace == "create" {
		ns = env.PreviewNamespace()
	}

	// List ConfigMaps with manifests labels
	var cmList corev1.ConfigMapList
	selector := labels.SelectorFromSet(map[string]string{
		"diverge.io/manifests":   "true",
		"diverge.io/environment": env.Name,
	})
	if err := f.Client.List(ctx, &cmList, client.InNamespace(ns), client.MatchingLabelsSelector{Selector: selector}); err != nil {
		return nil, fmt.Errorf("failed to list manifest ConfigMaps: %w", err)
	}

	var result []unstructured.Unstructured
	for _, cm := range cmList.Items {
		// CR4: Sort keys for deterministic apply order across reconciliations
		keys := make([]string, 0, len(cm.Data))
		for key := range cm.Data {
			keys = append(keys, key)
		}
		sort.Strings(keys)

		for _, key := range keys {
			objs, err := parseMultiDocYAML([]byte(cm.Data[key]))
			if err != nil {
				return nil, fmt.Errorf("failed to parse YAML from ConfigMap %s key %s: %w", cm.Name, key, err)
			}
			result = append(result, objs...)
		}
	}

	return result, nil
}

// parseMultiDocYAML splits a multi-document YAML stream into individual
// unstructured objects.
func parseMultiDocYAML(data []byte) ([]unstructured.Unstructured, error) {
	var result []unstructured.Unstructured
	decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)
	for {
		var obj unstructured.Unstructured
		err := decoder.Decode(&obj)
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		// Skip empty documents
		if obj.Object == nil {
			continue
		}
		result = append(result, obj)
	}
	return result, nil
}
