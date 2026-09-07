package cli

import (
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/divergedev/diverge/pkg/devsession"
)

func newDevSessionsCmd(app *App) *cobra.Command {
	var nsFlag string

	cmd := &cobra.Command{
		Use:     "sessions",
		Aliases: []string{"list"},
		Short:   "List active developer sessions across services",
		Long:    `Display all currently active local development sessions and their heartbeat leases.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := app.KubeClient()
			if err != nil {
				return fmt.Errorf("creating kubernetes client: %w", err)
			}
			ns := nsFlag
			if ns == "" {
				ns = app.Namespace
			}
			if ns == "" {
				ns = "default"
			}
			mgr := devsession.NewSessionManager(c)
			sessions, err := mgr.List(cmd.Context(), ns)
			if err != nil {
				return fmt.Errorf("listing dev sessions: %w", err)
			}
			printDevSessions(cmd.OutOrStdout(), sessions)
			return nil
		},
	}
	cmd.Flags().StringVarP(&nsFlag, "namespace", "n", "", "Kubernetes namespace (default: from kubeconfig)")
	return cmd
}

func printDevSessions(w io.Writer, sessions []devsession.DevSession) {
	if len(sessions) == 0 {
		_, _ = fmt.Fprintln(w, "No active dev sessions found.")
		return
	}

	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	_, _ = fmt.Fprintln(tw, "SERVICE\tDEVELOPER\tBRANCH\tHOST\tHEARTBEAT\tSTATUS")
	now := time.Now()
	for _, s := range sessions {
		status := "Active"
		if s.IsStale(now) {
			status = "Stale"
		}
		hbStr := "just now"
		if !s.Heartbeat.IsZero() {
			hbStr = fmt.Sprintf("%s ago", now.Sub(s.Heartbeat).Round(time.Second))
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			s.Service, s.Developer, s.Branch, s.Hostname, hbStr, status)
	}
	_ = tw.Flush()
}
