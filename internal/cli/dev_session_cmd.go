package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/divergedev/diverge/pkg/devsession"
)

// DevSessionJSONItem represents a dev session in JSON format compatible with IDE views.
type DevSessionJSONItem struct {
	Service   string `json:"service"`
	Developer string `json:"developer"`
	Branch    string `json:"branch"`
	Hostname  string `json:"hostname"`
	Heartbeat string `json:"heartbeat"`
	Status    string `json:"status"`
}

func newDevSessionsCmd(app *App) *cobra.Command {
	var (
		nsFlag       string
		outputJSON   bool
		outputFormat string
	)

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

			if outputJSON || outputFormat == "json" {
				return printDevSessionsJSON(cmd.OutOrStdout(), sessions)
			}

			printDevSessions(cmd.OutOrStdout(), sessions)
			return nil
		},
	}
	cmd.Flags().StringVarP(&nsFlag, "namespace", "n", "", "Kubernetes namespace (default: from kubeconfig)")
	cmd.Flags().BoolVar(&outputJSON, "json", false, "Output in JSON format")
	cmd.Flags().StringVarP(&outputFormat, "output", "o", "table", "Output format (table, json)")
	return cmd
}

func printDevSessionsJSON(w io.Writer, sessions []devsession.DevSession) error {
	if len(sessions) == 0 {
		_, err := fmt.Fprintln(w, "[]")
		return err
	}

	now := time.Now()
	items := make([]DevSessionJSONItem, 0, len(sessions))
	for _, s := range sessions {
		status := "ACTIVE"
		if s.IsStale(now) {
			status = "STALE"
		}
		hbStr := "just now"
		if !s.Heartbeat.IsZero() {
			hbStr = fmt.Sprintf("%s ago", now.Sub(s.Heartbeat).Round(time.Second))
		}
		items = append(items, DevSessionJSONItem{
			Service:   s.Service,
			Developer: s.Developer,
			Branch:    s.Branch,
			Hostname:  s.Hostname,
			Heartbeat: hbStr,
			Status:    status,
		})
	}

	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling dev sessions JSON: %w", err)
	}
	_, err = fmt.Fprintln(w, string(data))
	return err
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
