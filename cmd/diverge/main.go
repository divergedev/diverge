package main

import (
	"context"
	"os"

	"github.com/divergedev/diverge/internal/cli"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	app := &cli.App{
		Version: version,
		Commit:  commit,
		Date:    date,
	}
	root := cli.NewRootCmd(app)
	if err := root.ExecuteContext(context.Background()); err != nil {
		os.Exit(1)
	}
}
