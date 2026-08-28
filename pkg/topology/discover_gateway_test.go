package topology

import (
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func TestGatewayAPIDiscoverer_Name(t *testing.T) {
	d := &GatewayAPIDiscoverer{}
	assert.Equal(t, "gateway-api", d.Name())
}

func TestIsRouteAccepted_Accepted(t *testing.T) {
	status := gatewayv1.RouteStatus{
		Parents: []gatewayv1.RouteParentStatus{
			{
				Conditions: []metav1.Condition{
					{
						Type:   string(gatewayv1.RouteConditionAccepted),
						Status: metav1.ConditionTrue,
					},
				},
			},
		},
	}
	assert.True(t, isRouteAccepted(status))
}

func TestIsRouteAccepted_Rejected(t *testing.T) {
	status := gatewayv1.RouteStatus{
		Parents: []gatewayv1.RouteParentStatus{
			{
				Conditions: []metav1.Condition{
					{
						Type:   string(gatewayv1.RouteConditionAccepted),
						Status: metav1.ConditionFalse,
					},
				},
			},
		},
	}
	assert.False(t, isRouteAccepted(status))
}

func TestIsRouteAccepted_ColdStart(t *testing.T) {
	status := gatewayv1.RouteStatus{
		Parents: []gatewayv1.RouteParentStatus{},
	}
	assert.True(t, isRouteAccepted(status))
}

func TestIsRouteAccepted_NoAcceptedCondition(t *testing.T) {
	status := gatewayv1.RouteStatus{
		Parents: []gatewayv1.RouteParentStatus{
			{
				Conditions: []metav1.Condition{
					{
						Type:   string(gatewayv1.RouteConditionResolvedRefs),
						Status: metav1.ConditionTrue,
					},
				},
			},
		},
	}
	assert.False(t, isRouteAccepted(status))
}
