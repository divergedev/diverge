package controller

import (
	"context"
	"strings"
	"time"

	"go.opentelemetry.io/otel"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

func (r *EnvironmentReconciler) reconcileDeploy(ctx context.Context, env *divergeiov1alpha1.Environment, statusBase *divergeiov1alpha1.Environment) (ctrl.Result, bool, error) {
	ctx, span := otel.Tracer("diverge").Start(ctx, "reconcileDeploy")
	defer span.End()

	prevFP := strings.TrimSpace(env.Annotations[AnnotationBaselineFingerprint])
	alreadyDeployedByDriftSync := false
	if prevFP != "" {
		_, synced, driftErr := r.reconcileBaselineDrift(ctx, env)
		if driftErr != nil {
			log.FromContext(ctx).Error(driftErr, "failed to evaluate baseline drift")
		}
		alreadyDeployedByDriftSync = synced
	}

	if r.Deployer != nil && !alreadyDeployedByDriftSync {
		injectedInitialFP := false
		if prevFP == "" && !strings.EqualFold(strings.TrimSpace(env.Annotations[AnnotationDriftPolicy]), DriftPolicyIgnore) {
			if initialFP, fpErr := computeBaselineFingerprint(ctx, r.Client, env, r.APIReader); fpErr == nil && initialFP != "" {
				if env.Annotations == nil {
					env.Annotations = make(map[string]string)
				}
				env.Annotations[AnnotationBaselineFingerprint] = initialFP
				injectedInitialFP = true
			}
		}

		tCtxD, cancelD := context.WithTimeout(ctx, 15*time.Second)
		defer cancelD()
		if err := r.Deployer.Deploy(tCtxD, env); err != nil {
			if injectedInitialFP {
				delete(env.Annotations, AnnotationBaselineFingerprint)
			}
			meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
				Type:    "ServicesReady",
				Status:  metav1.ConditionFalse,
				Reason:  "DeployFailed",
				Message: err.Error(),
			})
			r.notifyFailed(ctx, env, err.Error())
			res, retErr := r.updateStatusWithRequeue(ctx, env, statusBase, err, 0)
			return res, true, retErr
		}
		if injectedInitialFP {
			delete(env.Annotations, AnnotationBaselineFingerprint)
		}
	}

	meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
		Type:    "ServicesReady",
		Status:  metav1.ConditionTrue,
		Reason:  "ServicesDeployed",
		Message: "Services deployed successfully",
	})
	if prevFP == "" {
		if _, _, driftErr := r.reconcileBaselineDrift(ctx, env); driftErr != nil {
			log.FromContext(ctx).Error(driftErr, "failed to evaluate baseline drift")
		}
	}
	return ctrl.Result{}, false, nil
}
