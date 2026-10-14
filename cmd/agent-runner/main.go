package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/divergedev/diverge/internal/agentrunner"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	os.Exit(run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}

	fs := flag.NewFlagSet("diverge-agent-runner", flag.ContinueOnError)
	fs.SetOutput(stdout)
	showVersion := fs.Bool("version", false, "Print version information and exit")
	showHelp := fs.Bool("help", false, "Print usage information and exit")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		_, _ = fmt.Fprintf(stderr, "error parsing flags: %v\n", err)
		return 2
	}

	if *showHelp {
		fs.Usage()
		return 0
	}

	if *showVersion {
		_, _ = fmt.Fprintf(stdout, "diverge-agent-runner version %s (commit: %s, built: %s)\n", version, commit, date)
		return 0
	}

	cfg, err := agentrunner.LoadConfigFromEnv(getenv)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "configuration error: %v\n", err)
		return 1
	}

	runner := agentrunner.NewRunner(cfg)
	runner.Logger = stdout

	res, err := runner.Run(ctx)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "agent runner failed: %v\n", err)
		return 1
	}

	_, _ = fmt.Fprintf(
		stdout,
		"agent task %s succeeded in %d iteration(s) (committed=%t, branch=%s, tokens=%d, cost=$%.4f)\n",
		cfg.TaskName,
		res.Iterations,
		res.Committed,
		res.WorkingBranch,
		res.TotalTokensUsed,
		res.TotalCostUSD,
	)
	return 0
}
