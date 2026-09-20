package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/divergedev/diverge/api/v1alpha1"
)

const (
	// AnnotationGuidance is the annotation key used to deliver steering guidance to an AgentTask.
	AnnotationGuidance = "divergedev.com/guidance"
	// AnnotationGuidanceTimestamp records the UTC timestamp when guidance was dispatched.
	AnnotationGuidanceTimestamp = "divergedev.com/guidance-timestamp"
)

// TaskGuider provides steering instructions to an active AgentTask.
type TaskGuider interface {
	Guide(ctx context.Context, namespace, name, message string) error
}

// KubeTaskGuider implements TaskGuider by recording steering instructions on the AgentTask resource.
type KubeTaskGuider struct {
	client client.Client
}

// NewKubeTaskGuider constructs a KubeTaskGuider.
func NewKubeTaskGuider(c client.Client) *KubeTaskGuider {
	return &KubeTaskGuider{client: c}
}

// Guide records steering guidance directly on the AgentTask resource annotations.
func (g *KubeTaskGuider) Guide(ctx context.Context, namespace, name, message string) error {
	task := &v1alpha1.AgentTask{}
	if err := g.client.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, task); err != nil {
		return fmt.Errorf("get AgentTask %s: %w", name, err)
	}

	if task.Annotations == nil {
		task.Annotations = make(map[string]string)
	}
	task.Annotations[AnnotationGuidance] = message
	task.Annotations[AnnotationGuidanceTimestamp] = time.Now().UTC().Format(time.RFC3339)

	if err := g.client.Update(ctx, task); err != nil {
		return fmt.Errorf("update AgentTask guidance: %w", err)
	}
	return nil
}

func newTaskGuideCmd(app *App) *cobra.Command {
	var message string

	cmd := &cobra.Command{
		Use:   "guide <name> [message]",
		Short: "Send steering guidance to an active AgentTask",
		Long: `Deliver real-time steering instructions or corrections to an active AgentTask.
The guidance is received by the agent runtime during its autonomous development loop.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			msg := message
			if len(args) > 1 {
				msg = strings.Join(args[1:], " ")
			}
			if strings.TrimSpace(msg) == "" {
				return fmt.Errorf("guidance message cannot be empty: provide as argument or via --message")
			}
			return runTaskGuide(cmd.Context(), app, name, msg)
		},
	}

	cmd.Flags().StringVarP(&message, "message", "m", "", "guidance message for the agent")
	return cmd
}

func runTaskGuide(ctx context.Context, app *App, name, message string) error {
	if err := app.ResolveNamespace(); err != nil {
		return err
	}

	c, _, err := app.KubeClient()
	if err != nil {
		return fmt.Errorf("create kube client: %w", err)
	}

	guider := NewKubeTaskGuider(c)
	if err := guider.Guide(ctx, app.Namespace, name, message); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(os.Stdout, "Steering guidance delivered to AgentTask %q in namespace %q.\n", name, app.Namespace)
	_, _ = fmt.Fprintf(os.Stdout, "Guidance: %s\n", message)
	return nil
}
