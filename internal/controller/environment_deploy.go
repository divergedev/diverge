package controller

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

func (r *EnvironmentReconciler) reconcileDeploy(ctx context.Context, env *divergeiov1alpha1.Environment, statusBase *divergeiov1alpha1.Environment) (ctrl.Result, bool, error) {
	ctx, span := otel.Tracer("diverge").Start(ctx, "reconcileDeploy")
	defer span.End()

	if r.Deployer != nil {
		tCtxD, cancelD := context.WithTimeout(ctx, 15*time.Second)
		defer cancelD()
		if err := r.Deployer.Deploy(tCtxD, env); err != nil {
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
	}
	meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
		Type:    "ServicesReady",
		Status:  metav1.ConditionTrue,
		Reason:  "ServicesDeployed",
		Message: "Services deployed successfully",
	})
	return ctrl.Result{}, false, nil
}
