package deployer

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1apply "k8s.io/client-go/applyconfigurations/core/v1"
	discoveryv1apply "k8s.io/client-go/applyconfigurations/discovery/v1"
	metav1apply "k8s.io/client-go/applyconfigurations/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/divergedev/diverge/api/v1alpha1"
)

var (
	// ErrInvalidEndpoint ...
	ErrInvalidEndpoint = errors.New("invalid endpoint format: expected host:port")
	// ErrMissingEndpoint ...
	ErrMissingEndpoint = errors.New("local mode requires endpoint to be set")
)

// LocalDeployer represents the configuration or state for this type.
type LocalDeployer struct {
	Client client.Client
}

func (d *LocalDeployer) targetNamespace(env *v1alpha1.Environment) string {
	targetNS := env.Namespace
	if env.Spec.Deploy.Namespace == "create" {
		targetNS = env.PreviewNamespace()
	}
	return targetNS
}

// Deploy performs its designated operation.
func (d *LocalDeployer) Deploy(ctx context.Context, env *v1alpha1.Environment) error {
	if env.Spec.ServiceConfig == nil || env.Spec.ServiceConfig.Endpoint == "" {
		return ErrMissingEndpoint
	}

	targetNS := d.targetNamespace(env)
	svcName := env.Name // Using environment name as service name for preview config

	host, portStr, err := net.SplitHostPort(env.Spec.ServiceConfig.Endpoint)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidEndpoint, err)
	}

	if net.ParseIP(host).To4() == nil {
		return fmt.Errorf("%w: invalid IPv4 address %q", ErrInvalidEndpoint, host)
	}

	portNum, err := strconv.ParseInt(portStr, 10, 32)
	if err != nil {
		return fmt.Errorf("%w: invalid port %q: %v", ErrInvalidEndpoint, portStr, err)
	}
	if portNum < 1 || portNum > 65535 {
		return fmt.Errorf("%w: port must be between 1 and 65535", ErrInvalidEndpoint)
	}
	port := int32(portNum)

	labels := map[string]string{
		"diverge.io/managed-by":  "diverge",
		"diverge.io/environment": env.Name,
	}

	// Create or Update Headless Service
	svcApply := corev1apply.Service(svcName, targetNS).
		WithLabels(labels).
		WithSpec(corev1apply.ServiceSpec().
			WithType(corev1.ServiceTypeClusterIP).
			WithClusterIP(corev1.ClusterIPNone).
			WithPorts(corev1apply.ServicePort().WithName("http").WithPort(port)))

	err = d.Client.Apply(ctx, svcApply, client.FieldOwner("diverge"), client.ForceOwnership)
	if err != nil {
		return fmt.Errorf("failed to apply headless service: %w", err)
	}

	// Create or Update EndpointSlice
	isReady := true
	epsLabels := make(map[string]string)
	for k, v := range labels {
		epsLabels[k] = v
	}
	epsLabels["endpointslice.kubernetes.io/managed-by"] = "diverge"
	epsLabels["kubernetes.io/service-name"] = svcName

	// To set the owner reference properly, we need the UID of the created service.
	var actualSvc corev1.Service
	if err := d.Client.Get(ctx, client.ObjectKey{Name: svcName, Namespace: targetNS}, &actualSvc); err != nil {
		return fmt.Errorf("failed to get headless service to set owner reference: %w", err)
	}

	epsApply := discoveryv1apply.EndpointSlice(svcName, targetNS).
		WithLabels(epsLabels).
		WithOwnerReferences(metav1apply.OwnerReference().
			WithAPIVersion("v1").
			WithKind("Service").
			WithName(svcName).
			WithUID(actualSvc.UID)).
		WithAddressType(discoveryv1.AddressTypeIPv4).
		WithEndpoints(discoveryv1apply.Endpoint().
			WithAddresses(host).
			WithConditions(discoveryv1apply.EndpointConditions().WithReady(isReady))).
		WithPorts(discoveryv1apply.EndpointPort().WithPort(port))

	if err := d.Client.Apply(ctx, epsApply, client.FieldOwner("diverge"), client.ForceOwnership); err != nil {
		return fmt.Errorf("failed to apply endpointslice: %w", err)
	}

	return nil
}

// Teardown performs its designated operation.
func (d *LocalDeployer) Teardown(ctx context.Context, env *v1alpha1.Environment) error {
	targetNS := d.targetNamespace(env)
	svcName := env.Name

	eps := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      svcName,
			Namespace: targetNS,
		},
	}
	if err := d.Client.Delete(ctx, eps); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete endpointslice: %w", err)
	}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      svcName,
			Namespace: targetNS,
		},
	}
	if err := d.Client.Delete(ctx, svc); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete headless service: %w", err)
	}

	return nil
}

// Status performs its designated operation.
func (d *LocalDeployer) Status(ctx context.Context, env *v1alpha1.Environment) ([]ServiceStatus, error) {
	targetNS := d.targetNamespace(env)
	svcName := env.Name

	eps := &discoveryv1.EndpointSlice{}
	err := d.Client.Get(ctx, client.ObjectKey{Name: svcName, Namespace: targetNS}, eps)

	status := ServiceStatus{
		Name:       svcName,
		Service:    svcName, // The Diverge service name
		SyncStatus: "Applied",
	}

	if err != nil {
		if apierrors.IsNotFound(err) {
			status.Health = "Missing"
			return []ServiceStatus{status}, nil
		}
		return nil, fmt.Errorf("failed to get endpointslice status: %w", err)
	}

	if len(eps.Endpoints) > 0 && eps.Endpoints[0].Conditions.Ready != nil && *eps.Endpoints[0].Conditions.Ready {
		status.Health = "Healthy"
	} else {
		status.Health = "Missing" // Or some other degraded state
	}

	return []ServiceStatus{status}, nil
}
