package deployer

import (
	"context"
	"fmt"
	"net"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/divergedev/diverge/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
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
		return fmt.Errorf("local mode requires env.Spec.ServiceConfig.Endpoint to be set")
	}

	targetNS := d.targetNamespace(env)
	svcName := env.Name // Using environment name as service name for preview config

	host, portStr, err := net.SplitHostPort(env.Spec.ServiceConfig.Endpoint)
	if err != nil {
		return fmt.Errorf("invalid endpoint format (expected host:port): %w", err)
	}

	portNum, err := strconv.ParseInt(portStr, 10, 32)
	if err != nil {
		return fmt.Errorf("invalid port %q: %w", portStr, err)
	}
	port := int32(portNum)

	// Create or Update Headless Service
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      svcName,
			Namespace: targetNS,
		},
	}

	_, err = controllerutil.CreateOrUpdate(ctx, d.Client, svc, func() error {
		svc.Spec.Type = corev1.ServiceTypeClusterIP
		svc.Spec.ClusterIP = corev1.ClusterIPNone // headless
		// Ensure port matches
		if len(svc.Spec.Ports) == 0 {
			svc.Spec.Ports = []corev1.ServicePort{{Name: "http", Port: port}}
		} else {
			svc.Spec.Ports[0].Port = port
		}
		// Clear selectors so we can manage endpoints manually via EndpointSlice
		svc.Spec.Selector = nil
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to create/update headless service: %w", err)
	}

	// Create or Update EndpointSlice
	isReady := true
	eps := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      svcName,
			Namespace: targetNS,
		},
	}

	addressType := discoveryv1.AddressTypeIPv4
	// Assume IPv4 for now as per instructions (run tailscale ip -4)

	_, err = controllerutil.CreateOrUpdate(ctx, d.Client, eps, func() error {
		if eps.Labels == nil {
			eps.Labels = make(map[string]string)
		}
		eps.Labels["kubernetes.io/service-name"] = svcName

		eps.AddressType = addressType
		eps.Endpoints = []discoveryv1.Endpoint{
			{
				Addresses: []string{host},
				Conditions: discoveryv1.EndpointConditions{
					Ready: &isReady,
				},
			},
		}
		eps.Ports = []discoveryv1.EndpointPort{
			{
				Name: nil, // can be named, but nil is fine if single port
				Port: &port,
			},
		}

		// Owner reference to headless service
		if len(eps.OwnerReferences) == 0 {
			eps.OwnerReferences = []metav1.OwnerReference{
				{
					APIVersion: "v1",
					Kind:       "Service",
					Name:       svc.Name,
					UID:        svc.UID,
				},
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to create/update endpointslice: %w", err)
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
	if err := d.Client.Delete(ctx, eps); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to delete endpointslice: %w", err)
	}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      svcName,
			Namespace: targetNS,
		},
	}
	if err := d.Client.Delete(ctx, svc); err != nil && !errors.IsNotFound(err) {
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
		if errors.IsNotFound(err) {
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
