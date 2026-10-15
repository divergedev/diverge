package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

const (
	AnnotationBaselineFingerprint = "divergedev.com/baseline-fingerprint"
	AnnotationDriftPolicy         = "divergedev.com/drift-policy"
	DriftPolicyWarn               = "warn"
	DriftPolicySync               = "sync"
	DriftPolicyIgnore             = "ignore"
	ConditionBaselineDrift        = "BaselineDriftDetected"
)

func computeBaselineFingerprint(ctx context.Context, c client.Client, env *divergeiov1alpha1.Environment) (string, error) {
	if env == nil || c == nil || env.Spec.Deploy.Mode == "full" {
		return "", nil
	}

	targetSet := make(map[string]bool)
	for _, svc := range env.Spec.Deploy.ChangedServices {
		if trimmed := strings.TrimSpace(svc); trimmed != "" {
			targetSet[trimmed] = true
		}
	}
	if env.Spec.ServiceConfig != nil {
		if trimmed := strings.TrimSpace(env.Spec.ServiceConfig.ServiceName); trimmed != "" {
			targetSet[trimmed] = true
		}
	}
	targetServices := make([]string, 0, len(targetSet))
	for svc := range targetSet {
		targetServices = append(targetServices, svc)
	}
	sort.Strings(targetServices)

	baselineNS := env.Namespace
	if env.Spec.ServiceConfig != nil && strings.TrimSpace(env.Spec.ServiceConfig.Namespace) != "" {
		baselineNS = strings.TrimSpace(env.Spec.ServiceConfig.Namespace)
	}

	var deployList appsv1.DeploymentList
	if err := c.List(ctx, &deployList, client.InNamespace(baselineNS)); err != nil {
		return "", fmt.Errorf("failed to list baseline deployments in %s: %w", baselineNS, err)
	}

	var matchedDeployments []appsv1.Deployment
	for _, dep := range deployList.Items {
		role := dep.Labels["divergedev.com/role"]
		envLabel := dep.Labels["divergedev.com/environment"]
		if role == "preview" || envLabel != "" {
			continue
		}
		svcLabel := dep.Labels["divergedev.com/service"]
		matchesTarget := len(targetServices) > 0 && (containsString(targetServices, svcLabel) || containsString(targetServices, dep.Name))
		matchesBaselineRole := role == "baseline" && (len(targetServices) == 0 || matchesTarget)
		if matchesBaselineRole || matchesTarget {
			matchedDeployments = append(matchedDeployments, dep)
		}
	}

	sort.Slice(matchedDeployments, func(i, j int) bool {
		ki := matchedDeployments[i].Namespace + "/" + matchedDeployments[i].Name
		kj := matchedDeployments[j].Namespace + "/" + matchedDeployments[j].Name
		return ki < kj
	})

	cmKeys := make(map[types.NamespacedName]bool)
	secretKeys := make(map[types.NamespacedName]bool)

	for _, dep := range matchedDeployments {
		for _, container := range dep.Spec.Template.Spec.Containers {
			for _, ef := range container.EnvFrom {
				if ef.ConfigMapRef != nil && strings.TrimSpace(ef.ConfigMapRef.Name) != "" {
					cmKeys[types.NamespacedName{Namespace: dep.Namespace, Name: strings.TrimSpace(ef.ConfigMapRef.Name)}] = true
				}
				if ef.SecretRef != nil && strings.TrimSpace(ef.SecretRef.Name) != "" {
					secretKeys[types.NamespacedName{Namespace: dep.Namespace, Name: strings.TrimSpace(ef.SecretRef.Name)}] = true
				}
			}
			for _, e := range container.Env {
				if e.ValueFrom != nil {
					if e.ValueFrom.ConfigMapKeyRef != nil && strings.TrimSpace(e.ValueFrom.ConfigMapKeyRef.Name) != "" {
						cmKeys[types.NamespacedName{Namespace: dep.Namespace, Name: strings.TrimSpace(e.ValueFrom.ConfigMapKeyRef.Name)}] = true
					}
					if e.ValueFrom.SecretKeyRef != nil && strings.TrimSpace(e.ValueFrom.SecretKeyRef.Name) != "" {
						secretKeys[types.NamespacedName{Namespace: dep.Namespace, Name: strings.TrimSpace(e.ValueFrom.SecretKeyRef.Name)}] = true
					}
				}
			}
		}
		for _, vol := range dep.Spec.Template.Spec.Volumes {
			if vol.ConfigMap != nil && strings.TrimSpace(vol.ConfigMap.Name) != "" {
				cmKeys[types.NamespacedName{Namespace: dep.Namespace, Name: strings.TrimSpace(vol.ConfigMap.Name)}] = true
			}
			if vol.Secret != nil && strings.TrimSpace(vol.Secret.SecretName) != "" {
				secretKeys[types.NamespacedName{Namespace: dep.Namespace, Name: strings.TrimSpace(vol.Secret.SecretName)}] = true
			}
		}
	}

	for _, ref := range env.Spec.EnvFrom {
		if strings.TrimSpace(ref.Name) != "" {
			ns := env.Namespace
			if strings.TrimSpace(ref.Namespace) != "" {
				ns = strings.TrimSpace(ref.Namespace)
			}
			secretKeys[types.NamespacedName{Namespace: ns, Name: strings.TrimSpace(ref.Name)}] = true
		}
	}
	for _, ref := range env.Spec.Deploy.EnvFrom {
		if strings.TrimSpace(ref.Name) != "" {
			ns := env.Namespace
			if strings.TrimSpace(ref.Namespace) != "" {
				ns = strings.TrimSpace(ref.Namespace)
			}
			secretKeys[types.NamespacedName{Namespace: ns, Name: strings.TrimSpace(ref.Name)}] = true
		}
	}

	if env.Spec.Deploy.Manifests != nil && env.Spec.Deploy.Manifests.Type == "configmap" {
		targetNS := env.Namespace
		if env.Spec.Deploy.Namespace == "create" {
			targetNS = env.PreviewNamespace()
		}
		var cmList corev1.ConfigMapList
		if err := c.List(ctx, &cmList,
			client.InNamespace(targetNS),
			client.MatchingLabels{
				"divergedev.com/manifests":   "true",
				"divergedev.com/environment": env.Name,
			},
		); err != nil {
			return "", fmt.Errorf("failed to list manifest configmaps: %w", err)
		}
		for _, cm := range cmList.Items {
			cmKeys[types.NamespacedName{Namespace: cm.Namespace, Name: cm.Name}] = true
		}
		if targetNS != env.Namespace {
			var srcCMList corev1.ConfigMapList
			if err := c.List(ctx, &srcCMList,
				client.InNamespace(env.Namespace),
				client.MatchingLabels{
					"divergedev.com/manifests":   "true",
					"divergedev.com/environment": env.Name,
				},
			); err != nil {
				return "", fmt.Errorf("failed to list source manifest configmaps: %w", err)
			}
			for _, cm := range srcCMList.Items {
				cmKeys[types.NamespacedName{Namespace: cm.Namespace, Name: cm.Name}] = true
			}
		}
	}

	var foundConfigMaps []corev1.ConfigMap
	for nn := range cmKeys {
		var cm corev1.ConfigMap
		if err := c.Get(ctx, nn, &cm); err != nil {
			if client.IgnoreNotFound(err) != nil {
				return "", fmt.Errorf("failed to get configmap %s: %w", nn, err)
			}
			continue
		}
		foundConfigMaps = append(foundConfigMaps, cm)
	}
	sort.Slice(foundConfigMaps, func(i, j int) bool {
		ki := foundConfigMaps[i].Namespace + "/" + foundConfigMaps[i].Name
		kj := foundConfigMaps[j].Namespace + "/" + foundConfigMaps[j].Name
		return ki < kj
	})

	var foundSecrets []corev1.Secret
	for nn := range secretKeys {
		var sec corev1.Secret
		if err := c.Get(ctx, nn, &sec); err != nil {
			if client.IgnoreNotFound(err) != nil {
				return "", fmt.Errorf("failed to get secret %s: %w", nn, err)
			}
			continue
		}
		foundSecrets = append(foundSecrets, sec)
	}
	sort.Slice(foundSecrets, func(i, j int) bool {
		ki := foundSecrets[i].Namespace + "/" + foundSecrets[i].Name
		kj := foundSecrets[j].Namespace + "/" + foundSecrets[j].Name
		return ki < kj
	})

	if len(matchedDeployments) == 0 && len(foundConfigMaps) == 0 && len(foundSecrets) == 0 {
		return "", nil
	}

	h := sha256.New()

	for _, dep := range matchedDeployments {
		_, _ = fmt.Fprintf(h, "deploy:%s/%s\n", dep.Namespace, dep.Name)

		if len(dep.Spec.Template.Annotations) > 0 {
			annoKeys := make([]string, 0, len(dep.Spec.Template.Annotations))
			for k := range dep.Spec.Template.Annotations {
				annoKeys = append(annoKeys, k)
			}
			sort.Strings(annoKeys)
			for _, k := range annoKeys {
				_, _ = fmt.Fprintf(h, "pod-anno:%s=%s\n", k, dep.Spec.Template.Annotations[k])
			}
		}

		for _, container := range dep.Spec.Template.Spec.Containers {
			_, _ = fmt.Fprintf(h, "container:%s|image:%s|cmd:%s|args:%s\n",
				container.Name,
				container.Image,
				strings.Join(container.Command, "\x00"),
				strings.Join(container.Args, "\x00"),
			)
			for _, port := range container.Ports {
				_, _ = fmt.Fprintf(h, "port:%s:%d/%s\n", port.Name, port.ContainerPort, port.Protocol)
			}
			for _, e := range container.Env {
				valFrom := ""
				if e.ValueFrom != nil {
					if e.ValueFrom.ConfigMapKeyRef != nil {
						valFrom = fmt.Sprintf("cm:%s:%s", e.ValueFrom.ConfigMapKeyRef.Name, e.ValueFrom.ConfigMapKeyRef.Key)
					} else if e.ValueFrom.SecretKeyRef != nil {
						valFrom = fmt.Sprintf("sec:%s:%s", e.ValueFrom.SecretKeyRef.Name, e.ValueFrom.SecretKeyRef.Key)
					}
				}
				_, _ = fmt.Fprintf(h, "env:%s=%s(%s)\n", e.Name, e.Value, valFrom)
			}
			for _, ef := range container.EnvFrom {
				cmName := ""
				if ef.ConfigMapRef != nil {
					cmName = ef.ConfigMapRef.Name
				}
				secName := ""
				if ef.SecretRef != nil {
					secName = ef.SecretRef.Name
				}
				_, _ = fmt.Fprintf(h, "envFrom:prefix=%s|cm=%s|sec=%s\n", ef.Prefix, cmName, secName)
			}
		}
	}

	for _, cm := range foundConfigMaps {
		_, _ = fmt.Fprintf(h, "cm:%s/%s\n", cm.Namespace, cm.Name)
		keys := make([]string, 0, len(cm.Data))
		for k := range cm.Data {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			_, _ = fmt.Fprintf(h, "data:%s=%s\n", k, cm.Data[k])
		}
	}

	for _, sec := range foundSecrets {
		_, _ = fmt.Fprintf(h, "secret:%s/%s\n", sec.Namespace, sec.Name)
		keys := make([]string, 0, len(sec.Data))
		for k := range sec.Data {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			valSum := sha256.Sum256(sec.Data[k])
			_, _ = fmt.Fprintf(h, "data:%s=%s\n", k, hex.EncodeToString(valSum[:]))
		}
	}

	sum := h.Sum(nil)
	return "sha256:" + hex.EncodeToString(sum[:16]), nil
}

func (r *EnvironmentReconciler) reconcileBaselineDrift(ctx context.Context, env *divergeiov1alpha1.Environment) (drifted bool, synced bool, err error) {
	if env == nil {
		return false, false, nil
	}
	if strings.EqualFold(strings.TrimSpace(env.Annotations[AnnotationDriftPolicy]), DriftPolicyIgnore) {
		return false, false, nil
	}

	fp, err := computeBaselineFingerprint(ctx, r.Client, env)
	if err != nil || fp == "" {
		return false, false, err
	}

	prevFP := strings.TrimSpace(env.Annotations[AnnotationBaselineFingerprint])

	persistAnnotation := func(key, val string) error {
		patchTarget := env.DeepCopy()
		base := patchTarget.DeepCopy()
		if patchTarget.Annotations == nil {
			patchTarget.Annotations = make(map[string]string)
		}
		patchTarget.Annotations[key] = val
		if err := r.Patch(ctx, patchTarget, client.MergeFrom(base)); err != nil {
			return err
		}
		if env.Annotations == nil {
			env.Annotations = make(map[string]string)
		}
		env.Annotations[key] = val
		env.ResourceVersion = patchTarget.ResourceVersion
		return nil
	}

	// Case 1: Initial baseline snapshot
	if prevFP == "" {
		if err := persistAnnotation(AnnotationBaselineFingerprint, fp); err != nil {
			return false, false, fmt.Errorf("failed to persist initial baseline fingerprint: %w", err)
		}
		meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
			Type:    ConditionBaselineDrift,
			Status:  metav1.ConditionFalse,
			Reason:  "BaselineInSync",
			Message: fmt.Sprintf("Baseline fingerprint recorded (%s)", fp),
		})
		return false, false, nil
	}

	// Case 2: No drift
	if prevFP == fp {
		meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
			Type:    ConditionBaselineDrift,
			Status:  metav1.ConditionFalse,
			Reason:  "BaselineInSync",
			Message: fmt.Sprintf("Preview matches baseline configuration (%s)", fp),
		})
		return false, false, nil
	}

	// Case 3: Drift detected
	policy := strings.ToLower(strings.TrimSpace(env.Annotations[AnnotationDriftPolicy]))
	if policy == DriftPolicySync {
		if err := persistAnnotation(AnnotationBaselineFingerprint, fp); err != nil {
			return true, false, fmt.Errorf("failed to update baseline fingerprint during sync: %w", err)
		}
		if r.Deployer != nil {
			tCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			if err := r.Deployer.Deploy(tCtx, env); err != nil {
				return true, false, err
			}
		}
		meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
			Type:    ConditionBaselineDrift,
			Status:  metav1.ConditionFalse,
			Reason:  "BaselineSynced",
			Message: fmt.Sprintf("Automatically synced preview with updated baseline (%s -> %s)", prevFP, fp),
		})
		if r.Recorder != nil && r.Recorder.EventRecorder != nil {
			r.Recorder.Event(env, "Normal", "BaselineSynced",
				fmt.Sprintf("Baseline configuration changed (%s -> %s); preview automatically refreshed", prevFP, fp))
		}
		return false, true, nil
	}

	meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
		Type:    ConditionBaselineDrift,
		Status:  metav1.ConditionTrue,
		Reason:  "BaselineUpdated",
		Message: fmt.Sprintf("Baseline configuration changed (%s -> %s); set divergedev.com/drift-policy=sync to auto-refresh", prevFP, fp),
	})
	if r.Recorder != nil && r.Recorder.EventRecorder != nil {
		r.Recorder.Event(env, "Warning", "BaselineDriftDetected",
			fmt.Sprintf("Baseline workload or configuration changed (%s -> %s)", prevFP, fp))
	}
	return true, false, nil
}

func (r *EnvironmentReconciler) mapBaselineDeploymentToEnvironments(ctx context.Context, obj client.Object) []ctrl.Request {
	if obj == nil {
		return nil
	}
	labels := obj.GetLabels()
	if labels["divergedev.com/role"] == "preview" || labels["divergedev.com/environment"] != "" {
		return nil
	}

	var envList divergeiov1alpha1.EnvironmentList
	if err := r.List(ctx, &envList, client.InNamespace(obj.GetNamespace())); err != nil {
		return nil
	}

	var reqs []ctrl.Request
	for _, env := range envList.Items {
		if !env.DeletionTimestamp.IsZero() {
			continue
		}
		reqs = append(reqs, ctrl.Request{
			NamespacedName: types.NamespacedName{
				Namespace: env.Namespace,
				Name:      env.Name,
			},
		})
	}
	return reqs
}

func (r *EnvironmentReconciler) mapConfigMapToEnvironments(ctx context.Context, obj client.Object) []ctrl.Request {
	if obj == nil {
		return nil
	}
	labels := obj.GetLabels()
	if labels["divergedev.com/manifests"] != "true" {
		if labels["divergedev.com/managed-by"] == "diverge" || labels["divergedev.com/environment"] != "" {
			return nil
		}
		name := obj.GetName()
		if strings.HasPrefix(name, "diverge-banner-") || strings.HasPrefix(name, "diverge-features-") || strings.HasPrefix(name, "diverge-dev-") {
			return nil
		}
	}

	var envList divergeiov1alpha1.EnvironmentList
	if err := r.List(ctx, &envList, client.InNamespace(obj.GetNamespace())); err != nil {
		return nil
	}

	var reqs []ctrl.Request
	for _, env := range envList.Items {
		if !env.DeletionTimestamp.IsZero() {
			continue
		}
		reqs = append(reqs, ctrl.Request{
			NamespacedName: types.NamespacedName{
				Namespace: env.Namespace,
				Name:      env.Name,
			},
		})
	}
	return reqs
}

func containsString(slice []string, target string) bool {
	if target == "" {
		return false
	}
	for _, item := range slice {
		if item == target {
			return true
		}
	}
	return false
}
