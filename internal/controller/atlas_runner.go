package controller

import (
	"context"
	"crypto/sha256"
	"fmt"
	"reflect"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/pkg/database"
)

const defaultAtlasJobImage = "arigaio/atlas:latest"

// ensureAtlasCR creates or updates an AtlasMigration or AtlasSchema CR.
func (r *EnvironmentReconciler) ensureAtlasCR(ctx context.Context, env *divergeiov1alpha1.Environment, dbResult *database.DatabaseResult) error {
	logger := log.FromContext(ctx)
	atlasSpec := env.Spec.Database.Atlas

	if atlasSpec == nil {
		return nil
	}

	if dbResult == nil || dbResult.DSN == "" {
		return fmt.Errorf("atlas migration requires a provisioned database with DSN")
	}

	// Create DSN Secret
	secretName, err := createDSNSecret(ctx, r.Client, env.Name, env.Namespace, dbResult.DSN, env)
	if err != nil {
		return fmt.Errorf("failed to create DSN secret for atlas: %w", err)
	}

	kind := "AtlasMigration"
	if atlasSpec.Mode == "declarative" {
		kind = "AtlasSchema"
	}

	crName := generateHookJobName(env.Name, "atlas")

	u := &unstructured.Unstructured{}
	u.SetAPIVersion("db.atlasgo.io/v1alpha1")
	u.SetKind(kind)
	u.SetName(crName)
	u.SetNamespace(env.Namespace)

	// Set owner reference
	t := true
	u.SetOwnerReferences([]metav1.OwnerReference{
		{
			APIVersion:         "divergedev.com/v1alpha1",
			Kind:               "Environment",
			Name:               env.Name,
			UID:                env.UID,
			Controller:         &t,
			BlockOwnerDeletion: &t,
		},
	})

	spec := map[string]interface{}{
		"urlFrom": map[string]interface{}{
			"secretKeyRef": map[string]interface{}{
				"name": secretName,
				"key":  "url",
			},
		},
	}

	// Set dir or schema based on type
	var cmRefName string
	if kind == "AtlasMigration" {
		if atlasSpec.MigrationConfigMap != "" {
			cmRefName = atlasSpec.MigrationConfigMap
			spec["dir"] = map[string]interface{}{
				"configMapRef": map[string]interface{}{
					"name": atlasSpec.MigrationConfigMap,
				},
			}
		}
	} else {
		if atlasSpec.SchemaConfigMap != "" {
			cmRefName = atlasSpec.SchemaConfigMap
			spec["schema"] = map[string]interface{}{
				"configMapRef": map[string]interface{}{
					"name": atlasSpec.SchemaConfigMap,
				},
			}
		}
	}
	if cmRefName != "" {
		r.ensureConfigMapOwner(ctx, env, cmRefName)
	}

	if atlasSpec.Policy != nil {
		if atlasSpec.Policy.Destructive != "" {
			spec["policy"] = map[string]interface{}{
				"destructive": atlasSpec.Policy.Destructive,
			}
		}
	}

	err = unstructured.SetNestedMap(u.Object, spec, "spec")
	if err != nil {
		return fmt.Errorf("failed to set unstructured spec: %w", err)
	}

	// Create or update
	var existing unstructured.Unstructured
	existing.SetAPIVersion("db.atlasgo.io/v1alpha1")
	existing.SetKind(kind)
	err = r.Get(ctx, types.NamespacedName{Name: crName, Namespace: env.Namespace}, &existing)
	if err != nil {
		if client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to get %s: %w", kind, err)
		}
		logger.Info(fmt.Sprintf("Creating %s", kind), "name", crName)
		if err := r.Create(ctx, u); err != nil {
			return fmt.Errorf("failed to create %s: %w", kind, err)
		}
		// Treat as newly created, so wait if blocking
		existing = *u
	} else {
		existingSpec, _, _ := unstructured.NestedMap(existing.Object, "spec")
		uSpec, _, _ := unstructured.NestedMap(u.Object, "spec")
		if !reflect.DeepEqual(existingSpec, uSpec) {
			logger.Info(fmt.Sprintf("Updating %s", kind), "name", crName)
			err = unstructured.SetNestedMap(existing.Object, uSpec, "spec")
			if err != nil {
				return fmt.Errorf("failed to set unstructured spec for update: %w", err)
			}
			if err := r.Update(ctx, &existing); err != nil {
				return fmt.Errorf("failed to update %s: %w", kind, err)
			}
		}
	}

	isBlocking := true
	if atlasSpec.Blocking != nil {
		isBlocking = *atlasSpec.Blocking
	}

	if !isBlocking {
		return nil
	}

	// Check status
	// In Atlas operator, status.conditions contains type Ready.
	conditions, found, err := unstructured.NestedSlice(existing.Object, "status", "conditions")
	if err != nil {
		return fmt.Errorf("failed to parse %s status conditions: %w", kind, err)
	}
	if found {
		for _, c := range conditions {
			cond, ok := c.(map[string]interface{})
			if ok {
				ctype, _ := cond["type"].(string)
				cstatus, _ := cond["status"].(string)
				if ctype == "Ready" {
					if cstatus == "True" {
						logger.Info(fmt.Sprintf("%s ready", kind), "name", crName)
						return nil
					}
					// If it's ready false, wait or fail based on reason. We'll wait.
					creason, _ := cond["reason"].(string)
					if creason == "Failed" {
						return fmt.Errorf("%s %s failed: %w", kind, crName, ErrHookFailed)
					}
				}
			}
		}
	}

	return fmt.Errorf("%s %s is still running: %w", kind, crName, ErrHookInProgress)
}

// ensureAtlasJob creates and monitors a standalone Kubernetes Job for Atlas migrations.
func (r *EnvironmentReconciler) ensureAtlasJob(ctx context.Context, env *divergeiov1alpha1.Environment, dbResult *database.DatabaseResult) error {
	logger := log.FromContext(ctx)
	atlasSpec := env.Spec.Database.Atlas

	if atlasSpec == nil {
		return nil
	}

	if dbResult == nil || dbResult.DSN == "" {
		return fmt.Errorf("atlas migration requires a provisioned database with DSN")
	}

	// 1. Resolve target ConfigMap and migration arguments based on mode
	var cmName string
	var volumeName string
	var mountPath string
	var args []string

	mode := atlasSpec.Mode
	if mode == "" {
		mode = "versioned"
	}

	switch mode {
	case "versioned":
		if atlasSpec.MigrationConfigMap == "" {
			return fmt.Errorf("atlas versioned mode requires migrationConfigMap")
		}
		cmName = atlasSpec.MigrationConfigMap
		volumeName = "atlas-migrations"
		mountPath = "/migrations"
		args = []string{"migrate", "apply", "--url", "$(DATABASE_URL)", "--dir", "file:///migrations"}
	case "declarative":
		if atlasSpec.SchemaConfigMap == "" {
			return fmt.Errorf("atlas declarative mode requires schemaConfigMap")
		}
		cmName = atlasSpec.SchemaConfigMap
		volumeName = "atlas-schema"
		mountPath = "/schema"
	default:
		return fmt.Errorf("unsupported atlas mode: %s", mode)
	}

	var cm corev1.ConfigMap
	if err := r.Get(ctx, types.NamespacedName{Name: cmName, Namespace: env.Namespace}, &cm); err != nil {
		return fmt.Errorf("failed to get atlas configmap %s: %w", cmName, err)
	}
	r.ensureConfigMapOwner(ctx, env, cmName)

	if mode == "declarative" {
		schemaTarget := "file:///schema/schema.sql"
		if _, ok := cm.Data["schema.hcl"]; ok && cm.Data["schema.sql"] == "" {
			schemaTarget = "file:///schema/schema.hcl"
		}
		args = []string{"schema", "apply", "--url", "$(DATABASE_URL)", "--to", schemaTarget, "--auto-approve"}
		if atlasSpec.Policy != nil && atlasSpec.Policy.Destructive == "allow" {
			args = append(args, "--allow-destructive")
		}
	}

	if len(atlasSpec.ExtraArgs) > 0 {
		args = append(args, atlasSpec.ExtraArgs...)
	}

	// 2. Create DSN Secret
	secretName, err := createDSNSecret(ctx, r.Client, env.Name, env.Namespace, dbResult.DSN, env)
	if err != nil {
		return fmt.Errorf("failed to create DSN secret for atlas: %w", err)
	}

	image := defaultAtlasJobImage
	if atlasSpec.Image != "" {
		image = atlasSpec.Image
	}

	// 3. Compute unique Job name using Image, Mode, ConfigMap Name, ConfigMap ResourceVersion, and Args
	hashInput := fmt.Sprintf("%s:%s:%s:%s:%s", image, mode, cmName, cm.ResourceVersion, strings.Join(args, " "))
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(hashInput)))[:8]
	jobName := generateHookJobName(env.Name, "atlas-"+hash)

	cfg := HookJobConfig{
		JobName:   jobName,
		Namespace: env.Namespace,
		Image:     image,
		Args:      args,
		Timeout:   defaultMigrationTimeout,
		Owner:     env,
		Labels: map[string]string{
			labelHookType:               hookTypeMigration,
			labelEnvironment:            env.Name,
			"divergedev.com/atlas-mode": mode,
		},
		EnvVars: []corev1.EnvVar{
			{
				Name:  "HOME",
				Value: "/tmp",
			},
			{
				Name: "DATABASE_URL",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: secretName,
						},
						Key: "url",
					},
				},
			},
		},
		Volumes: []corev1.Volume{
			{
				Name: volumeName,
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{Name: cmName},
					},
				},
			},
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      volumeName,
				MountPath: mountPath,
				ReadOnly:  true,
			},
		},
	}

	job := buildJob(cfg)

	// 4. Create or get existing Job
	var existingJob batchv1.Job
	err = r.Get(ctx, types.NamespacedName{Name: jobName, Namespace: env.Namespace}, &existingJob)
	if err != nil {
		if client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to get atlas migration job: %w", err)
		}

		logger.Info("Creating atlas migration job", "jobName", jobName)
		if err := r.Create(ctx, job); err != nil {
			return fmt.Errorf("failed to create atlas migration job: %w", err)
		}
		// newly created
	} else {
		job = &existingJob
	}

	isBlocking := true
	if atlasSpec.Blocking != nil {
		isBlocking = *atlasSpec.Blocking
	}

	if !isBlocking {
		return nil
	}

	// 5. Monitor Job completion
	for _, cond := range job.Status.Conditions {
		if cond.Type == batchv1.JobComplete && cond.Status == corev1.ConditionTrue {
			logger.Info("Atlas migration job completed successfully", "jobName", jobName)
			return nil
		}
		if cond.Type == batchv1.JobFailed && cond.Status == corev1.ConditionTrue {
			return fmt.Errorf("atlas migration job %s failed: %s: %w", jobName, cond.Reason, ErrHookFailed)
		}
	}

	return fmt.Errorf("atlas migration job %s is still running: %w", jobName, ErrHookInProgress)
}

// ensureConfigMapOwner attaches the Environment as an OwnerReference to the ConfigMap
// if it was created and managed by Diverge CLI (labeled divergedev.com/managed-by: diverge).
func (r *EnvironmentReconciler) ensureConfigMapOwner(ctx context.Context, env *divergeiov1alpha1.Environment, cmName string) {
	if cmName == "" {
		return
	}
	var cm corev1.ConfigMap
	if err := r.Get(ctx, types.NamespacedName{Name: cmName, Namespace: env.Namespace}, &cm); err != nil {
		return
	}
	if cm.Namespace != env.Namespace || cm.Labels == nil || cm.Labels["divergedev.com/managed-by"] != "diverge" {
		return
	}
	for _, owner := range cm.OwnerReferences {
		if owner.UID == env.UID {
			return
		}
	}
	var scheme *runtime.Scheme
	if r.Scheme != nil {
		scheme = r.Scheme
	} else if r.Client != nil {
		scheme = r.Client.Scheme()
	}
	if scheme != nil {
		if err := controllerutil.SetOwnerReference(env, &cm, scheme); err == nil {
			_ = r.Update(ctx, &cm)
		}
	} else {
		isController := false
		blockOwnerDeletion := true
		cm.OwnerReferences = append(cm.OwnerReferences, metav1.OwnerReference{
			APIVersion:         divergeiov1alpha1.GroupVersion.String(),
			Kind:               "Environment",
			Name:               env.Name,
			UID:                env.UID,
			Controller:         &isController,
			BlockOwnerDeletion: &blockOwnerDeletion,
		})
		_ = r.Update(ctx, &cm)
	}
}
