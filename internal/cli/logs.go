package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

func newLogsCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logs [environment-name]",
		Short: "Stream logs from a preview environment",
		Long:  "Stream logs from pods in a preview environment. Shows logs from all services by default.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLogs(app, cmd, args)
		},
	}
	// namespace is managed by app via rootCmd persistent flag
	cmd.Flags().StringP("service", "s", "", "Filter logs to a specific service")
	cmd.Flags().BoolP("follow", "f", false, "Follow log output")
	cmd.Flags().String("since", "1h", "Show logs since duration (e.g., 5m, 1h, 24h)")
	cmd.Flags().Int64("tail", 100, "Number of recent lines to show")
	cmd.Flags().Bool("timestamps", false, "Show timestamps")
	cmd.Flags().Bool("previous", false, "Show logs from previous container instance")
	return cmd
}

func runLogs(app *App, cmd *cobra.Command, args []string) error {
	c, clientset, err := app.KubeClient()
	if err != nil {
		return err
	}

	name := args[0]
	ctx := cmd.Context()

	var env divergeiov1alpha1.Environment
	if err := c.Get(ctx, client.ObjectKey{Name: name, Namespace: app.Namespace}, &env); err != nil {
		return fmt.Errorf("environment not found: %w", err)
	}

	serviceFilter, _ := cmd.Flags().GetString("service")
	follow, _ := cmd.Flags().GetBool("follow")
	sinceStr, _ := cmd.Flags().GetString("since")
	tail, _ := cmd.Flags().GetInt64("tail")
	timestamps, _ := cmd.Flags().GetBool("timestamps")
	previous, _ := cmd.Flags().GetBool("previous")

	var sinceTime *metav1.Time
	if sinceStr != "" {
		if d, err := time.ParseDuration(sinceStr); err != nil {
			return fmt.Errorf("invalid --since value %q: %w", sinceStr, err)
		} else {
			t := metav1.NewTime(time.Now().Add(-d))
			sinceTime = &t
		}
	}

	podNs := app.Namespace
	if env.Spec.Deploy.Namespace == "create" {
		podNs = env.PreviewNamespace()
	}

	pods, err := clientset.CoreV1().Pods(podNs).List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("diverge.io/environment=%s", name),
	})
	if err != nil {
		return fmt.Errorf("failed to list pods: %w", err)
	}

	if len(pods.Items) == 0 {
		return fmt.Errorf("no pods found for environment %s", name)
	}

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

	for _, pod := range pods.Items {
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
				streamLogs(ctx, clientset, app, p, c.Name, svc, col, follow, tail, sinceTime, timestamps, previous, cmd.OutOrStdout(), &mu)
			}(pod, container, svcName, podColor)
		}
	}

	if podCount == 0 {
		return fmt.Errorf("no pods found for environment %s matching service filter", name)
	}

	wg.Wait()
	return nil
}

func streamLogs(ctx context.Context, clientset kubernetes.Interface, app *App, pod corev1.Pod, containerName, svcName string, col *color.Color, follow bool, tail int64, sinceTime *metav1.Time, timestamps, previous bool, out io.Writer, mu *sync.Mutex) {
	opts := &corev1.PodLogOptions{
		Container:  containerName,
		Follow:     follow,
		Timestamps: timestamps,
		Previous:   previous,
	}
	if tail > 0 {
		opts.TailLines = &tail
	}
	if sinceTime != nil {
		opts.SinceTime = sinceTime
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
	if app.NoColor {
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
			if err != io.EOF && err != context.Canceled {
				mu.Lock()
				_, _ = fmt.Fprintf(out, "error reading logs for %s: %v\n", pod.Name, err)
				mu.Unlock()
			}
			break
		}
	}
}
