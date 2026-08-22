package cli

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

var ErrServerNotFound = fmt.Errorf("diverge server not found in cluster")
var ErrNamedTargetPortNotFound = fmt.Errorf("named target port not found in pod containers")

func discoverServer(ctx context.Context, k8sClient kubernetes.Interface, restConfig *rest.Config) (serverAddr string, stopChan chan struct{}, err error) {
	listCtx, listCancel := context.WithTimeout(ctx, 10*time.Second)
	defer listCancel()

	svcs, err := k8sClient.CoreV1().Services("").List(listCtx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/name=diverge-server",
	})
	if err != nil {
		return "", nil, fmt.Errorf("failed to list services: %w", err)
	}
	if len(svcs.Items) == 0 {
		return "", nil, ErrServerNotFound
	}
	svc := svcs.Items[0]

	pods, err := k8sClient.CoreV1().Pods(svc.Namespace).List(listCtx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/name=diverge-server",
		FieldSelector: "status.phase=Running",
	})
	if err != nil {
		return "", nil, fmt.Errorf("failed to list pods: %w", err)
	}
	if len(pods.Items) == 0 {
		return "", nil, fmt.Errorf("diverge server pod not found or not running")
	}
	pod := pods.Items[0]

	// Resolve remote port from service spec, handling named TargetPort
	remotePort, err := resolveRemotePort(svc, pod)
	if err != nil {
		return "", nil, err
	}

	// Set up SPDY port-forward with timeout
	pfCtx, pfCancel := context.WithTimeout(ctx, 30*time.Second)
	defer pfCancel()

	transport, upgrader, err := spdy.RoundTripperFor(restConfig)
	if err != nil {
		return "", nil, fmt.Errorf("failed to create round tripper: %w", err)
	}

	req := k8sClient.CoreV1().RESTClient().Post().
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
