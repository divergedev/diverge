//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TestEnvironment_HookReconcile verifies that a migration hook Job is created
// and runs to completion when the Environment specifies a MigrationJobSpec.
func TestEnvironment_HookReconcile(t *testing.T) {
	f := NewFramework(t)
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	f.CreateNamespace(ctx)
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		f.CleanupNamespace(cleanupCtx)
	}()

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hook-success",
			Namespace: f.Namespace,
		},
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "github",
				Project:  "divergedev/test-app",
				Branch:   "feat/hooks-e2e",
			},
			Database: v1alpha1.EnvironmentDatabase{
				Mode: "shared",
				MigrationJob: &v1alpha1.MigrationJobSpec{
					Image:    "alpine:3.20",
					Args:     []string{"sh", "-c", "echo migration-complete"},
					Blocking: ptr.To(true),
				},
			},
		},
	}

	err := f.CreateEnvironment(ctx, env)
	require.NoError(t, err, "Failed to create environment")

	if !f.ControllerRunning(ctx) {
		t.Skip("controller not deployed — skipping reconciliation assertions")
	}

	// Wait for migration Job to appear
	var jobs batchv1.JobList
	require.Eventually(t, func() bool {
		err := f.Client.List(ctx, &jobs,
			client.InNamespace(f.Namespace),
			client.MatchingLabels{
				"diverge.io/hook-type":   "migration",
				"diverge.io/environment": "hook-success",
			},
		)
		return err == nil && len(jobs.Items) > 0
	}, 2*time.Minute, 2*time.Second, "migration Job was not created")

	job := jobs.Items[0]

	// Verify PSS-compliant SecurityContext
	container := job.Spec.Template.Spec.Containers[0]
	require.NotNil(t, container.SecurityContext, "SecurityContext must be set")
	assert.True(t, *container.SecurityContext.RunAsNonRoot, "RunAsNonRoot must be true")
	require.NotNil(t, container.SecurityContext.RunAsUser, "RunAsUser must be set")
	assert.Equal(t, int64(65534), *container.SecurityContext.RunAsUser, "RunAsUser must be 65534 (nobody)")
	assert.False(t, *container.SecurityContext.AllowPrivilegeEscalation, "AllowPrivilegeEscalation must be false")
	assert.True(t, *container.SecurityContext.ReadOnlyRootFilesystem, "ReadOnlyRootFilesystem must be true")

	// Verify owner reference points back to Environment
	require.Len(t, job.OwnerReferences, 1)
	assert.Equal(t, "hook-success", job.OwnerReferences[0].Name)
	assert.True(t, *job.OwnerReferences[0].Controller)

	// Wait for environment to reach Ready (migration succeeded → deploy continues)
	err = f.WaitForCondition(ctx, env.Name, "Ready", metav1.ConditionTrue, 2*time.Minute)
	require.NoError(t, err, "Environment did not become Ready after migration")

	// Verify migration status on the Environment
	var updated v1alpha1.Environment
	err = f.Client.Get(ctx, client.ObjectKey{Name: env.Name, Namespace: f.Namespace}, &updated)
	require.NoError(t, err)
	assert.Equal(t, "Succeeded", updated.Status.MigrationStatus)
}

// TestEnvironment_HookFailure verifies that a failing migration hook causes
// the Environment to report MigrationFailed status.
func TestEnvironment_HookFailure(t *testing.T) {
	f := NewFramework(t)
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	f.CreateNamespace(ctx)
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		f.CleanupNamespace(cleanupCtx)
	}()

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hook-fail",
			Namespace: f.Namespace,
		},
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "github",
				Project:  "divergedev/test-app",
				Branch:   "feat/hooks-fail",
			},
			Database: v1alpha1.EnvironmentDatabase{
				Mode: "shared",
				MigrationJob: &v1alpha1.MigrationJobSpec{
					Image:          "alpine:3.20",
					Args:           []string{"sh", "-c", "echo 'syntax error'; exit 1"},
					Blocking:       ptr.To(true),
					TimeoutSeconds: 30,
				},
			},
		},
	}

	err := f.CreateEnvironment(ctx, env)
	require.NoError(t, err)

	if !f.ControllerRunning(ctx) {
		t.Skip("controller not deployed — skipping reconciliation assertions")
	}

	// Wait for migration Job to appear and fail
	require.Eventually(t, func() bool {
		var updated v1alpha1.Environment
		if err := f.Client.Get(ctx, client.ObjectKey{Name: env.Name, Namespace: f.Namespace}, &updated); err != nil {
			return false
		}
		return updated.Status.MigrationStatus == "Failed"
	}, 2*time.Minute, 2*time.Second, "migration did not report Failed status")

	var updated v1alpha1.Environment
	err = f.Client.Get(ctx, client.ObjectKey{Name: env.Name, Namespace: f.Namespace}, &updated)
	require.NoError(t, err)
	assert.Equal(t, "Failed", updated.Status.MigrationStatus)
	assert.NotEmpty(t, updated.Status.MigrationMessage, "MigrationMessage should describe the failure")
}

// TestEnvironment_PostDeployHook verifies that a non-blocking migration hook Job runs
// alongside deployment without blocking the Environment from becoming Ready.
func TestEnvironment_PostDeployHook(t *testing.T) {
	f := NewFramework(t)
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	f.CreateNamespace(ctx)
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		f.CleanupNamespace(cleanupCtx)
	}()

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hook-nonblocking",
			Namespace: f.Namespace,
		},
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "github",
				Project:  "divergedev/test-app",
				Branch:   "feat/postdeploy",
			},
			Database: v1alpha1.EnvironmentDatabase{
				Mode: "shared",
				MigrationJob: &v1alpha1.MigrationJobSpec{
					Image:    "alpine:3.20",
					Args:     []string{"sh", "-c", "sleep 2 && echo non-blocking-done"},
					Blocking: ptr.To(false),
				},
			},
		},
	}

	err := f.CreateEnvironment(ctx, env)
	require.NoError(t, err)

	if !f.ControllerRunning(ctx) {
		t.Skip("controller not deployed — skipping reconciliation assertions")
	}

	// Wait for migration Job to be created
	var jobs batchv1.JobList
	require.Eventually(t, func() bool {
		err := f.Client.List(ctx, &jobs,
			client.InNamespace(f.Namespace),
			client.MatchingLabels{
				"diverge.io/hook-type":   "migration",
				"diverge.io/environment": "hook-nonblocking",
			},
		)
		return err == nil && len(jobs.Items) > 0
	}, 2*time.Minute, 2*time.Second)

	// Verify labels are correctly set
	job := jobs.Items[0]
	assert.Equal(t, "migration", job.Labels["diverge.io/hook-type"])
	assert.Equal(t, "hook-nonblocking", job.Labels["diverge.io/environment"])

	// Wait for environment Ready (non-blocking migration should not delay readiness)
	err = f.WaitForCondition(ctx, env.Name, "Ready", metav1.ConditionTrue, 2*time.Minute)
	require.NoError(t, err, "Environment did not become Ready with non-blocking migration")
}

// TestEnvironment_HookCleanup verifies that hook Jobs are cleaned up when
// the parent Environment is deleted (via OwnerReference cascade).
func TestEnvironment_HookCleanup(t *testing.T) {
	f := NewFramework(t)
	ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
	defer cancel()
	f.CreateNamespace(ctx)
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		f.CleanupNamespace(cleanupCtx)
	}()

	env := &v1alpha1.Environment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hook-cleanup",
			Namespace: f.Namespace,
		},
		Spec: v1alpha1.EnvironmentSpec{
			Source: v1alpha1.EnvironmentSource{
				Provider: "github",
				Project:  "divergedev/test-app",
				Branch:   "feat/cleanup",
			},
			Database: v1alpha1.EnvironmentDatabase{
				Mode: "shared",
				MigrationJob: &v1alpha1.MigrationJobSpec{
					Image:    "alpine:3.20",
					Args:     []string{"sh", "-c", "echo ok"},
					Blocking: ptr.To(true),
				},
			},
		},
	}

	err := f.CreateEnvironment(ctx, env)
	require.NoError(t, err)

	if !f.ControllerRunning(ctx) {
		t.Skip("controller not deployed — skipping reconciliation assertions")
	}

	// Wait for migration Job
	require.Eventually(t, func() bool {
		var jobs batchv1.JobList
		err := f.Client.List(ctx, &jobs,
			client.InNamespace(f.Namespace),
			client.MatchingLabels{"diverge.io/environment": "hook-cleanup"},
		)
		return err == nil && len(jobs.Items) > 0
	}, 2*time.Minute, 2*time.Second)

	// Delete environment
	err = f.Client.Delete(ctx, env)
	require.NoError(t, err)

	err = f.WaitForEnvironmentDeleted(ctx, env.Name, 2*time.Minute)
	require.NoError(t, err)

	// Poll until hook Jobs are cascade-deleted (GC may take a moment)
	require.Eventually(t, func() bool {
		var jobs batchv1.JobList
		if err := f.Client.List(ctx, &jobs,
			client.InNamespace(f.Namespace),
			client.MatchingLabels{"diverge.io/environment": "hook-cleanup"},
		); err != nil {
			return false
		}
		return len(jobs.Items) == 0
	}, 30*time.Second, 1*time.Second, "Hook Jobs should be cascade-deleted with Environment")
}
