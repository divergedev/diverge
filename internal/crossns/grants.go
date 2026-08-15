package crossns

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"
)

// EnsureReferenceGrant creates a Gateway API ReferenceGrant allowing HTTPRoute
// references across namespaces. It is idempotent and a no-op if the grant exists.
func EnsureReferenceGrant(ctx context.Context, c client.Client, fromNamespace, toNamespace string) error {
	if fromNamespace == toNamespace {
		return nil
	}

	grant := &gatewayv1beta1.ReferenceGrant{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("diverge-crossns-%s", fromNamespace),
			Namespace: toNamespace,
		},
		Spec: gatewayv1beta1.ReferenceGrantSpec{
			From: []gatewayv1beta1.ReferenceGrantFrom{
				{
					Group:     "gateway.networking.k8s.io",
					Kind:      "HTTPRoute",
					Namespace: gatewayv1beta1.Namespace(fromNamespace),
				},
			},
			To: []gatewayv1beta1.ReferenceGrantTo{
				{
					Group: "",
					Kind:  "Service",
				},
			},
		},
	}

	err := c.Create(ctx, grant)
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}

	return nil
}
