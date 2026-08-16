package cli

import (
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"
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
	cmd.Flags().Int64("tail", 100, "Number of recent lines to show")
	cmd.Flags().String("since", "", "Show logs since a given duration (e.g. 5m, 1h)")
	cmd.Flags().Bool("timestamps", false, "Include timestamps on each line")
	cmd.Flags().Bool("previous", false, "Print the logs for the previous instance of the container")
	return cmd
}

func runLogs(app *App, cmd *cobra.Command, args []string) error {
	envClient, err := app.EnvironmentClient()
	if err != nil {
		return err
	}

	name := args[0]
	ctx := cmd.Context()

	serviceFilter, _ := cmd.Flags().GetString("service")
	follow, _ := cmd.Flags().GetBool("follow")
	tail, _ := cmd.Flags().GetInt64("tail")
	since, _ := cmd.Flags().GetString("since")
	timestamps, _ := cmd.Flags().GetBool("timestamps")
	previous, _ := cmd.Flags().GetBool("previous")

	if since != "" {
		if _, err := time.ParseDuration(since); err != nil {
			return fmt.Errorf("invalid --since value %q: %w", since, err)
		}
	}

	stream, err := envClient.StreamLogs(ctx, app.Namespace, name, serviceFilter, "", follow, tail, since, timestamps, previous)
	if err != nil {
		return err
	}
	defer func() { _ = stream.Close() }()

	_, err = io.Copy(cmd.OutOrStdout(), stream)
	if err != nil && err != io.EOF {
		return fmt.Errorf("error reading logs: %w", err)
	}
	return nil
}
