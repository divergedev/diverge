package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	logsService string
	logsFollow  bool
	logsTail    int64
)

var logsCmd = &cobra.Command{
	Use:   "logs <name>",
	Short: "Stream logs from services in an environment",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		_, clientset, err := getKubeClient()
		if err != nil {
			return err
		}

		envName := args[0]
		
		// In a real implementation we would fetch the env and look up its namespace/pods.
		// For simplicity we assume it's running in the requested namespace.
		targetNamespace := namespace
		if targetNamespace == "" {
			targetNamespace = envName
		}
		
		labelSelector := ""
		if logsService != "" {
			labelSelector = fmt.Sprintf("app=%s", logsService)
		} else {
			labelSelector = "app"
		}

		pods, err := clientset.CoreV1().Pods(targetNamespace).List(context.Background(), metav1.ListOptions{
			LabelSelector: labelSelector,
		})
		if err != nil {
			return fmt.Errorf("failed to list pods: %w", err)
		}

		if len(pods.Items) == 0 {
			return fmt.Errorf("no pods found for environment %s", envName)
		}

		// Simplified log streaming for the first pod
		pod := pods.Items[0]
		
		req := clientset.CoreV1().Pods(targetNamespace).GetLogs(pod.Name, &corev1.PodLogOptions{
			Follow: logsFollow,
			TailLines: &logsTail,
		})

		stream, err := req.Stream(context.Background())
		if err != nil {
			return fmt.Errorf("failed to get log stream: %w", err)
		}
		defer stream.Close()

		_, err = io.Copy(os.Stdout, stream)
		return err
	},
}

func init() {
	logsCmd.Flags().StringVar(&logsService, "service", "", "filter logs to one service")
	logsCmd.Flags().BoolVarP(&logsFollow, "follow", "f", false, "follow log output")
	logsCmd.Flags().Int64Var(&logsTail, "tail", 100, "number of lines to show")
	rootCmd.AddCommand(logsCmd)
}
