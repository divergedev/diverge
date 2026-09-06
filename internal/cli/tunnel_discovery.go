package cli

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

var ErrServerNotFound = fmt.Errorf("diverge server not found in cluster")
var ErrNamedTargetPortNotFound = fmt.Errorf("named target port not found in pod containers")

// serverLabelSelectors are tried in order until one matches a Service.
//
//  1. app.kubernetes.io/part-of=diverge,app.kubernetes.io/component=server:
//     Standard chart label, invariant to nameOverride and release name.
//  2. app.kubernetes.io/name=diverge-server:
//     Legacy selector for hand-rolled manifests.
//  3. app.kubernetes.io/name=diverge,app.kubernetes.io/component=server:
//     Chart fullname matching default chart install.
//  4. app.kubernetes.io/component=server:
//     Fallback matching any server component in Diverge namespaces.
var serverLabelSelectors = []string{
	"app.kubernetes.io/part-of=diverge,app.kubernetes.io/component=server",
	"app.kubernetes.io/name=diverge-server",
	"app.kubernetes.io/name=diverge,app.kubernetes.io/component=server",
	"app.kubernetes.io/component=server",
}

// ServerDiscoverer discovers a running Diverge server in Kubernetes and establishes access.
type ServerDiscoverer interface {
	Discover(ctx context.Context) (serverAddr string, stopChan chan struct{}, err error)
}

// K8sServerDiscoverer locates the Diverge server in a Kubernetes cluster using label selectors
// across candidate namespaces and sets up a local port-forward to an active pod.
type K8sServerDiscoverer struct {
	K8sClient  kubernetes.Interface
	RestConfig *rest.Config
	Namespace  string // Target or active namespace from kubeconfig
}

// candidateNamespaces returns the ordered list of namespaces to check when
// cluster-scoped listing is forbidden by RBAC.
func (d *K8sServerDiscoverer) candidateNamespaces() []string {
	seen := make(map[string]bool)
	var list []string

	add := func(ns string) {
		ns = strings.TrimSpace(ns)
		if ns != "" && !seen[ns] {
			seen[ns] = true
			list = append(list, ns)
		}
	}

	add(d.Namespace)
	add("diverge-system")
	add("diverge")
	add("default")

	return list
}

func (d *K8sServerDiscoverer) findService(ctx context.Context) (*corev1.Service, string, error) {
	for _, selector := range serverLabelSelectors {
		// 1. Try cluster-wide listing
		svcs, listErr := d.K8sClient.CoreV1().Services("").List(ctx, metav1.ListOptions{
			LabelSelector: selector,
		})
		if listErr == nil {
			if len(svcs.Items) > 0 {
				return &svcs.Items[0], selector, nil
			}
			continue
		}

		// 2. If cluster-scoped listing is forbidden by RBAC, try candidate namespaces
		if apierrors.IsForbidden(listErr) {
			for _, ns := range d.candidateNamespaces() {
				nsSvcs, nsErr := d.K8sClient.CoreV1().Services(ns).List(ctx, metav1.ListOptions{
					LabelSelector: selector,
				})
				if nsErr == nil && len(nsSvcs.Items) > 0 {
					return &nsSvcs.Items[0], selector, nil
				}
			}
			continue
		}

		return nil, "", fmt.Errorf("failed to list services: %w", listErr)
	}

	return nil, "", ErrServerNotFound
}

func (d *K8sServerDiscoverer) findActivePod(ctx context.Context, ns, selector string) (*corev1.Pod, error) {
	pods, err := d.K8sClient.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{
		LabelSelector: selector,
		FieldSelector: "status.phase=Running",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list pods: %w", err)
	}

	for i := range pods.Items {
		pod := &pods.Items[i]
		// Skip terminating pods during rollouts
		if pod.DeletionTimestamp == nil {
			return pod, nil
		}
	}

	return nil, fmt.Errorf("diverge server pod not found or not running")
}

// Discover locates the Diverge server and starts a port-forward.
func (d *K8sServerDiscoverer) Discover(ctx context.Context) (serverAddr string, stopChan chan struct{}, err error) {
	listCtx, listCancel := context.WithTimeout(ctx, 10*time.Second)
	defer listCancel()

	svc, matchedSelector, err := d.findService(listCtx)
	if err != nil {
		return "", nil, err
	}

	pod, err := d.findActivePod(listCtx, svc.Namespace, matchedSelector)
	if err != nil {
		return "", nil, err
	}

	// Resolve remote port from service spec, handling named TargetPort
	remotePort, err := resolveRemotePort(*svc, *pod)
	if err != nil {
		return "", nil, err
	}

	// Set up SPDY port-forward with timeout
	pfCtx, pfCancel := context.WithTimeout(ctx, 30*time.Second)
	defer pfCancel()

	transport, upgrader, err := spdy.RoundTripperFor(d.RestConfig)
	if err != nil {
		return "", nil, fmt.Errorf("failed to create round tripper: %w", err)
	}

	req := d.K8sClient.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(pod.Namespace).
		Name(pod.Name).
		SubResource("portforward")

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, http.MethodPost, req.URL())
	stopCh := make(chan struct{}, 1)
	readyCh := make(chan struct{})
	errOut := new(bytes.Buffer)

	fw, err := portforward.New(dialer, []string{fmt.Sprintf("0:%d", remotePort)}, stopCh, readyCh, nil, errOut)
	if err != nil {
		return "", nil, fmt.Errorf("failed to create port-forwarder: %w", err)
	}

	errCh := make(chan error, 1)
	go func() {
		if err := fw.ForwardPorts(); err != nil {
			errCh <- err
		}
	}()

	select {
	case <-readyCh:
		// Port-forward is ready
	case err := <-errCh:
		close(stopCh)
		return "", nil, fmt.Errorf("port forwarding failed: %w", err)
	case <-pfCtx.Done():
		close(stopCh)
		return "", nil, fmt.Errorf("port-forward timed out: %w", pfCtx.Err())
	}

	ports, err := fw.GetPorts()
	if err != nil {
		close(stopCh)
		return "", nil, fmt.Errorf("failed to get forwarded ports: %w", err)
	}
	if len(ports) == 0 {
		close(stopCh)
		return "", nil, fmt.Errorf("no forwarded ports found")
	}

	return fmt.Sprintf("http://localhost:%d", ports[0].Local), stopCh, nil
}

// discoverServer is a convenience wrapper around K8sServerDiscoverer for backward compatibility.
func discoverServer(ctx context.Context, k8sClient kubernetes.Interface, restConfig *rest.Config) (serverAddr string, stopChan chan struct{}, err error) {
	d := &K8sServerDiscoverer{
		K8sClient:  k8sClient,
		RestConfig: restConfig,
	}
	return d.Discover(ctx)
}

// resolveRemotePort determines the remote port to forward to from the service
// spec and pod container ports. Handles numeric TargetPort, named TargetPort
// (resolved against pod containers), and fallback to Service.Port.
func resolveRemotePort(svc corev1.Service, pod corev1.Pod) (int, error) {
	remotePort := 8080
	if len(svc.Spec.Ports) > 0 {
		sp := svc.Spec.Ports[0]
		if sp.TargetPort.IntValue() != 0 {
			remotePort = sp.TargetPort.IntValue()
		} else if sp.TargetPort.String() != "" && sp.TargetPort.String() != "0" {
			// Named port — resolve against pod container ports, matching protocol.
			// Kubernetes defaults Protocol to TCP if unset.
			portName := sp.TargetPort.String()
			svcProtocol := sp.Protocol
			if svcProtocol == "" {
				svcProtocol = corev1.ProtocolTCP
			}

			// Search regular containers, then restartable init containers (native sidecars).
			// Regular init containers are excluded — they exit before the pod is Running.
			allContainers := make([]corev1.Container, 0, len(pod.Spec.Containers)+len(pod.Spec.InitContainers))
			allContainers = append(allContainers, pod.Spec.Containers...)
			for _, ic := range pod.Spec.InitContainers {
				if ic.RestartPolicy != nil && *ic.RestartPolicy == corev1.ContainerRestartPolicyAlways {
					allContainers = append(allContainers, ic)
				}
			}

			resolved := false
			for _, c := range allContainers {
				for _, cp := range c.Ports {
					cpProto := cp.Protocol
					if cpProto == "" {
						cpProto = corev1.ProtocolTCP
					}
					if cp.Name == portName && cpProto == svcProtocol {
						remotePort = int(cp.ContainerPort)
						resolved = true
						break
					}
				}
				if resolved {
					break
				}
			}
			if !resolved {
				return 0, fmt.Errorf("named port %q: %w", portName, ErrNamedTargetPortNotFound)
			}
		} else if sp.Port != 0 {
			remotePort = int(sp.Port)
		}
	}
	return remotePort, nil
}
