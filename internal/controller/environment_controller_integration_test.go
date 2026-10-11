//go:build integration

package controller

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/divergedev/diverge/api/v1alpha1"
	_ "github.com/divergedev/diverge/internal/sandbox"
	pkgauth "github.com/divergedev/diverge/pkg/auth"
	pkgsandbox "github.com/divergedev/diverge/pkg/sandbox"
)

var _ = Describe("Environment Controller", func() {
	Context("When creating an Environment", func() {
		It("Should create successfully", func() {
			ctx := context.Background()
			env := &v1alpha1.Environment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-env",
					Namespace: "default",
				},
				Spec: v1alpha1.EnvironmentSpec{
					Source: v1alpha1.EnvironmentSource{
						Provider: "gitlab",
						Project:  "foo",
					},
				},
			}
			Expect(k8sClient.Create(ctx, env)).Should(Succeed())

			createdEnv := &v1alpha1.Environment{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{Name: "test-env", Namespace: "default"}, createdEnv)
			}, time.Second*10, time.Millisecond*250).Should(Succeed())
		})
	})
})

var _ = Describe("AgentTask Controller", func() {
	Context("When reconciling an AgentTask lifecycle", func() {
		It("Should mint a Macaroon capability token, provision sandbox, and clean up on deletion", func() {
			ctx := context.Background()
			rootKey := []byte("0123456789abcdef0123456789abcdef")
			minter := pkgauth.NewMacaroonProvider(rootKey)
			reconciler := &AgentTaskReconciler{
				Client:          k8sClient,
				Scheme:          k8sClient.Scheme(),
				SandboxRegistry: pkgsandbox.Providers,
				TokenMinter:     minter,
				FeatureGate:     &StaticFeatureGate{Enabled: true},
				DefaultProvider: "noop",
			}

			taskKey := types.NamespacedName{Name: "test-agent-task", Namespace: "default"}
			task := &v1alpha1.AgentTask{
				ObjectMeta: metav1.ObjectMeta{
					Name:      taskKey.Name,
					Namespace: taskKey.Namespace,
				},
				Spec: v1alpha1.AgentTaskSpec{
					Objective: "Fix flaky test in auth middleware",
					Repository: v1alpha1.AgentTaskRepository{
						URL:        "https://github.com/divergedev/diverge",
						BaseBranch: "main",
					},
					Sandbox: v1alpha1.AgentTaskSandbox{
						Provider: "noop",
					},
					BudgetUSD: "15.00",
				},
			}
			Expect(k8sClient.Create(ctx, task)).Should(Succeed())

			// First reconcile adds finalizer
			_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: taskKey})
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile mints token, provisions noop sandbox, and transitions to Active
			_, err = reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: taskKey})
			Expect(err).NotTo(HaveOccurred())

			reconciled := &v1alpha1.AgentTask{}
			Expect(k8sClient.Get(ctx, taskKey, reconciled)).Should(Succeed())
			Expect(reconciled.Status.Phase).To(Equal(v1alpha1.AgentTaskPhaseActive))
			Expect(reconciled.Status.SandboxClaimRef).To(Equal("noop-claim-test-agent-task"))

			// Verify Macaroon secret exists and contains a valid namespace-scoped token
			secret := &corev1.Secret{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      pkgauth.SecretName(taskKey.Name),
				Namespace: taskKey.Namespace,
			}, secret)).Should(Succeed())
			tokenBytes := secret.Data["token"]
			Expect(tokenBytes).NotTo(BeEmpty())

			_, claims, err := minter.VerifyAndExtract(ctx, tokenBytes)
			Expect(err).NotTo(HaveOccurred())
			Expect(claims.TaskID).To(Equal(taskKey.Name))
			Expect(claims.Namespace).To(Equal(taskKey.Namespace))

			// Delete AgentTask and verify finalizer teardown removes it
			Expect(k8sClient.Delete(ctx, reconciled)).Should(Succeed())
			_, err = reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: taskKey})
			Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Get(ctx, taskKey, &v1alpha1.AgentTask{})
			Expect(apierrors.IsNotFound(err)).To(BeTrue())
		})
	})
})
