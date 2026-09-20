package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/divergedev/diverge/api/v1alpha1"
)

func newTaskLogsCmd(app *App) *cobra.Command {
	var (
		follow bool
		tail   int64
	)

	cmd := &cobra.Command{
		Use:   "logs <name>",
		Short: "Stream execution logs from an AgentTask's sandbox",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTaskLogs(cmd.Context(), app, args[0], follow, tail)
		},
	}

	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "stream logs continuously")
	cmd.Flags().Int64Var(&tail, "tail", 100, "number of lines from end of logs to show")
	return cmd
}

func runTaskLogs(ctx context.Context, app *App, name string, follow bool, tail int64) error {
	if err := app.ResolveNamespace(); err != nil {
		return err
	}

	c, cs, err := app.KubeClient()
	if err != nil {
		return fmt.Errorf("create kube client: %w", err)
	}

	task := &v1alpha1.AgentTask{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: app.Namespace, Name: name}, task); err != nil {
		return fmt.Errorf("get AgentTask %s: %w", name, err)
	}

	podName := task.Status.SandboxPodName
	if podName == "" {
		return fmt.Errorf("sandbox pod for task %s is not yet scheduled (current phase: %s)", name, task.Status.Phase)
	}

	if cs == nil {
		return fmt.Errorf("clientset unavailable for log streaming")
	}

	opts := &corev1.PodLogOptions{
		Follow:     follow,
		Timestamps: true,
	}
	if tail > 0 {
		opts.TailLines = &tail
	}

	req := cs.CoreV1().Pods(app.Namespace).GetLogs(podName, opts)
	stream, err := req.Stream(ctx)
	if err != nil {
		return fmt.Errorf("stream logs from pod %s: %w", podName, err)
	}
	defer func() { _ = stream.Close() }()

	_, err = io.Copy(os.Stdout, stream)
	return err
}
