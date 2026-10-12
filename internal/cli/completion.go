package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"sigs.k8s.io/controller-runtime/pkg/client"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

func newCompletionCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "completion [bash|zsh|fish|powershell]",
		Short: "Generate shell completion scripts with live Kubernetes resource autocomplete",
		Long: `Generate shell completion scripts for diverge commands, flags, and live
Kubernetes resources (Environment and AgentTask names).

To load completions:

Bash:
	$ source <(diverge completion bash)

	# To load completions for each session, execute once:
	# Linux:
	$ diverge completion bash > /etc/bash_completion.d/diverge
	# macOS:
	$ diverge completion bash > $(brew --prefix)/etc/bash_completion.d/diverge

Zsh:
	# If shell completion is not already enabled in your environment,
	# you will need to enable it. You can execute the following once:
	$ echo "autoload -U compinit; compinit" >> ~/.zshrc

	# To load completions for each session, execute once:
	$ diverge completion zsh > "${fpath[1]}/_diverge"

	# Or source directly in ~/.zshrc:
	$ source <(diverge completion zsh)

Fish:
	$ diverge completion fish | source

	# To load completions for each session, execute once:
	$ diverge completion fish > ~/.config/fish/completions/diverge.fish

PowerShell:
	PS> diverge completion powershell | Out-String | Invoke-Expression
`,
		DisableFlagsInUseLine: true,
		ValidArgs:             []string{"bash", "zsh", "fish", "powershell"},
		Args:                  cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			switch args[0] {
			case "bash":
				return cmd.Root().GenBashCompletionV2(out, true)
			case "zsh":
				return cmd.Root().GenZshCompletion(out)
			case "fish":
				return cmd.Root().GenFishCompletion(out, true)
			case "powershell":
				return cmd.Root().GenPowerShellCompletionWithDesc(out)
			default:
				return fmt.Errorf("unsupported shell %q (supported: bash, zsh, fish, powershell)", args[0])
			}
		},
	}

	return cmd
}

func completeEnvironmentNames(app *App) func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		_ = app.ResolveNamespace()
		ns := app.Namespace
		if ns == "" {
			ns = "default"
		}

		c, _, err := app.KubeClient()
		if err != nil || c == nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		baseCtx := context.Background()
		if cmd != nil && cmd.Context() != nil {
			baseCtx = cmd.Context()
		}
		ctx, cancel := context.WithTimeout(baseCtx, 2*time.Second)
		defer cancel()

		var envList divergeiov1alpha1.EnvironmentList
		if err := c.List(ctx, &envList, client.InNamespace(ns)); err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		var names []string
		for _, env := range envList.Items {
			if strings.HasPrefix(env.Name, toComplete) {
				names = append(names, env.Name)
			}
		}
		sort.Strings(names)
		return names, cobra.ShellCompDirectiveNoFileComp
	}
}

func completeAgentTaskNames(app *App) func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		_ = app.ResolveNamespace()
		ns := app.Namespace
		if ns == "" {
			ns = "default"
		}

		c, _, err := app.KubeClient()
		if err != nil || c == nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		baseCtx := context.Background()
		if cmd != nil && cmd.Context() != nil {
			baseCtx = cmd.Context()
		}
		ctx, cancel := context.WithTimeout(baseCtx, 2*time.Second)
		defer cancel()

		var taskList divergeiov1alpha1.AgentTaskList
		if err := c.List(ctx, &taskList, client.InNamespace(ns)); err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		var names []string
		for _, task := range taskList.Items {
			if strings.HasPrefix(task.Name, toComplete) {
				names = append(names, task.Name)
			}
		}
		sort.Strings(names)
		return names, cobra.ShellCompDirectiveNoFileComp
	}
}
