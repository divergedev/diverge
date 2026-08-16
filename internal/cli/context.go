package cli

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func newContextCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "context",
		Short: "Manage Diverge server contexts",
	}

	cmd.AddCommand(
		newContextListCmd(app),
		newContextUseCmd(app),
		newContextDeleteCmd(app),
	)

	return cmd
}

func newContextListCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all contexts",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := LoadConfig()
			if err != nil {
				return err
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', 0)
			_, _ = fmt.Fprintln(w, "ACTIVE\tNAME\tSERVER")
			for name, ctx := range cfg.Contexts {
				active := ""
				if name == cfg.ActiveContext {
					active = "*"
				}
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", active, name, ctx.ServerURL)
			}
			_ = w.Flush()
			return nil
		},
	}
}

func newContextUseCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "use <name>",
		Short: "Switch active context",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			cfg, err := LoadConfig()
			if err != nil {
				return err
			}

			if _, ok := cfg.Contexts[name]; !ok {
				return fmt.Errorf("context %q not found", name)
			}

			cfg.ActiveContext = name
			if err := cfg.Save(); err != nil {
				return err
			}

			fmt.Printf("Switched to context %q\n", name)
			return nil
		},
	}
}

func newContextDeleteCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <name>",
		Short: "Remove a context",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			cfg, err := LoadConfig()
			if err != nil {
				return err
			}

			if _, ok := cfg.Contexts[name]; !ok {
				return fmt.Errorf("context %q not found", name)
			}

			delete(cfg.Contexts, name)
			if cfg.ActiveContext == name {
				cfg.ActiveContext = ""
			}

			if err := cfg.Save(); err != nil {
				return err
			}

			fmt.Printf("Deleted context %q\n", name)
			return nil
		},
	}
}
