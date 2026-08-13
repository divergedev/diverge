package diverge

import (
	"github.com/spf13/cobra"

	"github.com/divergedev/diverge/internal/cli"
)

// App encapsulates the internal CLI app state.
type App struct {
	inner *cli.App
}

// NewApp creates a new isolated instance of the Diverge app.
func NewApp(version, commit, date string) *App {
	return &App{
		inner: &cli.App{
			Version: version,
			Commit:  commit,
			Date:    date,
		},
	}
}

// NewRootCmd returns a fresh instance of the root command bound to the app.
func (a *App) NewRootCmd() *cobra.Command {
	return cli.NewRootCmd(a.inner)
}

// NewRootCmd returns a fresh instance of the root command without version info (useful for tests).
func NewRootCmd() *cobra.Command {
	return cli.NewRootCmd(&cli.App{})
}

// RootCmd returns the root cobra command. Use this to add subcommands
// from external modules before calling Execute.
func RootCmd() *cobra.Command {
	return cli.RootCmd()
}

// Execute runs the Diverge CLI with the given version metadata.
func Execute(version, commit, date string) {
	cli.Execute(version, commit, date)
}
