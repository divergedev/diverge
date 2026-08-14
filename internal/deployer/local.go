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
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/divergedev/diverge/api/v1alpha1"
)

var (
	ErrInvalidEndpoint = errors.New("invalid endpoint format: expected host:port")
	ErrMissingEndpoint = errors.New("local mode requires endpoint to be set")
)

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

	portNum, err := strconv.ParseInt(portStr, 10, 32)
	if err != nil {
		return fmt.Errorf("invalid port %q: %w", portStr, err)
	}
	port := int32(portNum)

	labels := map[string]string{
		"diverge.io/managed-by":  "diverge",
		"diverge.io/environment": env.Name,
	}

	// Create or Update Headless Service
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      svcName,
			Namespace: targetNS,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeClusterIP,
			ClusterIP: corev1.ClusterIPNone, // headless
			Ports:     []corev1.ServicePort{{Name: "http", Port: port}},
		},
	}
	svc.SetGroupVersionKind(schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Service"})

	err = d.Client.Patch(ctx, svc, client.Apply, client.FieldOwner("diverge"), client.ForceOwnership)
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

	eps := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      svcName,
			Namespace: targetNS,
			Labels:    epsLabels,
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "v1",
					Kind:       "Service",
					Name:       svc.Name,
					UID:        svc.UID,
				},
			},
		},
		AddressType: discoveryv1.AddressTypeIPv4,
		Endpoints: []discoveryv1.Endpoint{
			{
				Addresses: []string{host},
				Conditions: discoveryv1.EndpointConditions{
					Ready: &isReady,
				},
			},
		},
		Ports: []discoveryv1.EndpointPort{
			{
				Name: nil, // can be named, but nil is fine if single port
				Port: &port,
			},
		},
	}
	eps.SetGroupVersionKind(schema.GroupVersionKind{Group: "discovery.k8s.io", Version: "v1", Kind: "EndpointSlice"})

	err = d.Client.Patch(ctx, eps, client.Apply, client.FieldOwner("diverge"), client.ForceOwnership)
	if err != nil {
		return fmt.Errorf("failed to apply endpointslice: %w", err)
	}

	return nil
}

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
