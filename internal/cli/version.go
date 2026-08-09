package cli

import (
	"github.com/spf13/cobra"
)

func newVersionCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{

		Use:   "version",
		Short: "Print version info",
		Run: func(cmd *cobra.Command, args []string) {
			cmd.Printf("Diverge version: %s\nCommit: %s\nDate: %s\n", app.Version, app.Commit, app.Date)
		},
	}

	return cmd
}
