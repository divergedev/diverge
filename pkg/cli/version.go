package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version info",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("Diverge version: %s\nCommit: %s\nDate: %s\n", cliVersion, cliCommit, cliDate)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
