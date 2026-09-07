package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/divergedev/diverge/pkg/doctor"
)

func newDoctorCmd(app *App) *cobra.Command {
	var outputJSON bool

	cmd := &cobra.Command{
		Use:   "doctor [environment]",
		Short: "Inspect cluster workloads and diagnose environment failures",
		Long: `Runs automated root-cause diagnostics across Kubernetes workloads, init containers,
pod scheduling, and database migrations. Detects CrashLoopBackOff, OOMKilled, image pull
failures, and missing configurations with actionable remediation advice.`,
		Example: `	# Diagnose current namespace
	diverge doctor

	# Diagnose a specific preview environment
	diverge doctor pr-42 -n default

	# Machine-readable output for AI agents & CI
	diverge doctor pr-42 --json`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var envName string
			if len(args) > 0 {
				envName = args[0]
			}

			client, _, err := app.KubeClient()
			if err != nil {
				return fmt.Errorf("failed to connect to cluster: %w", err)
			}

			diagnoser := doctor.NewDiagnoser(client)
			report, err := diagnoser.Diagnose(cmd.Context(), app.Namespace, envName)
			if err != nil {
				return fmt.Errorf("diagnostic check failed: %w", err)
			}

			if outputJSON {
				if err := doctor.FormatJSON(cmd.OutOrStdout(), report); err != nil {
					return err
				}
			} else {
				if err := doctor.FormatTable(cmd.OutOrStdout(), report); err != nil {
					return err
				}
			}

			if !report.Healthy {
				return fmt.Errorf("doctor detected %d issue(s) requiring attention", len(report.Issues))
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&outputJSON, "json", false, "Output results in JSON format")
	return cmd
}
