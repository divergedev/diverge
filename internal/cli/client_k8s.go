package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/fatih/color"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	divergev1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

// K8sClient implements EnvironmentClient via direct K8s API.
type K8sClient struct {
	client    client.Client
	clientset kubernetes.Interface
	app       *App
}

// NewK8sClient creates a K8s-backed environment client.
func NewK8sClient(c client.Client, cs kubernetes.Interface, app *App) *K8sClient {
	return &K8sClient{
		client:    c,
		clientset: cs,
		app:       app,
	}
}

func (c *K8sClient) ListEnvironments(ctx context.Context, namespace string) ([]divergev1alpha1.Environment, error) {
	var envList divergev1alpha1.EnvironmentList
	listOpts := []client.ListOption{}
	if namespace != "" {
		listOpts = append(listOpts, client.InNamespace(namespace))
	}
	if err := c.client.List(ctx, &envList, listOpts...); err != nil {
		return nil, err
	}
	return envList.Items, nil
}

func (c *K8sClient) GetEnvironment(ctx context.Context, namespace, name string) (*divergev1alpha1.Environment, error) {
	var env divergev1alpha1.Environment
	if err := c.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &env); err != nil {
		return nil, err
	}
	return &env, nil
}

func (c *K8sClient) DeleteEnvironment(ctx context.Context, namespace, name string) error {
	env := &divergev1alpha1.Environment{}
	env.Namespace = namespace
	env.Name = name
	return c.client.Delete(ctx, env)
}

func (c *K8sClient) StreamLogs(ctx context.Context, namespace, envName, serviceFilter, container string, follow bool, tail int64, since string, timestamps bool, previous bool) (io.ReadCloser, error) {
	// Replicate runLogs logic inside K8sClient
	var env divergev1alpha1.Environment
	if err := c.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: envName}, &env); err != nil {
		return nil, fmt.Errorf("environment not found: %w", err)
	}

	podNs := namespace
	if env.Spec.Deploy.Namespace == "create" {
		podNs = env.PreviewNamespace()
	}

	podList, err := c.clientset.CoreV1().Pods(podNs).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("diverge.io/environment=%s", envName),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list pods: %w", err)
	}

	if len(podList.Items) == 0 {
		return nil, fmt.Errorf("no pods found for environment %s", envName)
	}

	r, w := io.Pipe()
	go streamK8sPods(ctx, c.clientset, &env, podList.Items, serviceFilter, follow, tail, w, c.app.NoColor)
	return r, nil
}

func (c *K8sClient) ListPreviewGroups(ctx context.Context, namespace string) ([]divergev1alpha1.PreviewGroup, error) {
	var pgList divergev1alpha1.PreviewGroupList
	if err := c.client.List(ctx, &pgList); err != nil {
		return nil, err
	}
	return pgList.Items, nil
}

func streamK8sPods(ctx context.Context, clientset kubernetes.Interface, env *divergev1alpha1.Environment, pods []corev1.Pod, serviceFilter string, follow bool, tail int64, w *io.PipeWriter, noColor bool) {
	var wg sync.WaitGroup
	var mu sync.Mutex
	colors := []*color.Color{
		color.New(color.FgHiCyan),
		color.New(color.FgHiGreen),
		color.New(color.FgHiMagenta),
		color.New(color.FgHiYellow),
		color.New(color.FgHiBlue),
		color.New(color.FgHiRed),
	}

	colorIdx := 0
	svcColors := make(map[string]*color.Color)
	podCount := 0

	for _, pod := range pods {
		svcName := pod.Labels["diverge.io/service"]
		if svcName == "" {
			svcName = pod.Name // fallback
		}

		if serviceFilter != "" && svcName != serviceFilter {
			continue
		}

		podCount++

		if _, ok := svcColors[svcName]; !ok {
			svcColors[svcName] = colors[colorIdx%len(colors)]
			colorIdx++
		}

		podColor := svcColors[svcName]

		for _, container := range pod.Spec.Containers {
			wg.Add(1)
			go func(p corev1.Pod, c corev1.Container, svc string, col *color.Color) {
				defer wg.Done()
				streamSingleContainer(ctx, clientset, p, c.Name, svc, col, follow, tail, w, &mu, noColor)
			}(pod, container, svcName, podColor)
		}
	}

	if podCount == 0 {
		w.CloseWithError(fmt.Errorf("no pods found for environment %s matching service filter", env.Name))
		return
	}

	wg.Wait()
	_ = w.Close()
}

func streamSingleContainer(ctx context.Context, clientset kubernetes.Interface, pod corev1.Pod, containerName, svcName string, col *color.Color, follow bool, tail int64, out io.Writer, mu *sync.Mutex, noColor bool) {
	opts := &corev1.PodLogOptions{
		Container: containerName,
		Follow:    follow,
	}
	if tail > 0 {
		opts.TailLines = &tail
	}

	req := clientset.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, opts)
	stream, err := req.Stream(ctx)
	if err != nil {
		mu.Lock()
		_, _ = fmt.Fprintf(out, "failed to stream logs for %s: %v\n", pod.Name, err)
		mu.Unlock()
		return
	}
	defer func() { _ = stream.Close() }()

	reader := bufio.NewReader(stream)

	prefixBase := svcName
	if len(pod.Spec.Containers) > 1 {
		prefixBase = fmt.Sprintf("%s/%s", svcName, containerName)
	}
	prefixStr := fmt.Sprintf("[%s]", prefixBase)

	var prefix string
	if noColor {
		prefix = prefixStr
	} else {
		prefix = col.Sprint(prefixStr)
	}

	for {
		line, err := reader.ReadString('\n')
		if line != "" {
			mu.Lock()
			_, _ = fmt.Fprintf(out, "%s  %s", prefix, line)
			mu.Unlock()
		}
		if err != nil {
			break
		}
	}
}
