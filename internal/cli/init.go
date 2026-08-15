package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

type initOptions struct {
	clusterName      string
	installGateway   bool
	installKeda      bool
	installSampleApp bool
	dryRun           bool
}

var (
	lookPath           = exec.LookPath
	execCommandContext = exec.CommandContext
)

func newInitCmd(app *App) *cobra.Command {
	opts := &initOptions{}

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize a ready-to-use local development playground",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInit(cmd.Context(), opts)
		},
	}

	cmd.Flags().StringVar(&opts.clusterName, "cluster-name", "diverge-dev", "k3d cluster name")
	cmd.Flags().BoolVar(&opts.installGateway, "install-gateway", true, "install Envoy Gateway")
	cmd.Flags().BoolVar(&opts.installKeda, "install-keda", false, "install KEDA HTTP Add-on")
	cmd.Flags().BoolVar(&opts.installSampleApp, "install-sample-app", true, "deploy echo-server sample")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "print commands without executing")

	return cmd
}

func runInit(ctx context.Context, opts *initOptions) error {
	totalSteps := 5
	if opts.installGateway {
		totalSteps++
	}
	if opts.installKeda {
		totalSteps++
	}
	if opts.installSampleApp {
		totalSteps++
	}

	stepIndex := 1
	printStep := func(msg string) {
		fmt.Fprintf(os.Stderr, "[\033[36m%d/%d\033[0m] %s\n", stepIndex, totalSteps, msg)
		stepIndex++
	}

	runCmd := func(name string, args ...string) error {
		cmdStr := name + " " + strings.Join(args, " ")
		if opts.dryRun {
			fmt.Fprintf(os.Stderr, "  (dry-run) %s\n", cmdStr)
			return nil
		}
		cmdCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		cmd := execCommandContext(cmdCtx, name, args...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("command failed: %s\n%s", cmdStr, string(out))
		}
		return nil
	}

	// 1. Check prerequisites
	printStep("Checking prerequisites...")
	missing := []string{}
	for _, bin := range []string{"k3d", "kubectl", "helm"} {
		if _, err := lookPath(bin); err != nil {
			missing = append(missing, bin)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("\033[31m✗ missing prerequisites: %s\033[0m, please install them first", strings.Join(missing, ", "))
	}

	// 2. Create k3d cluster
	printStep(fmt.Sprintf("Creating k3d cluster '%s'...", opts.clusterName))
	if !opts.dryRun {
		err := runCmd("k3d", "cluster", "get", opts.clusterName)
		if err == nil {
			fmt.Fprintf(os.Stderr, "  Cluster %s already exists, skipping creation.\n", opts.clusterName)
		} else {
			if err := runCmd("k3d", "cluster", "create", opts.clusterName, "--no-lb", "--k3s-arg", "--disable=traefik@server:0", "--wait"); err != nil {
				return err
			}
		}
	} else {
		fmt.Fprintf(os.Stderr, "  (dry-run) k3d cluster create %s --no-lb --k3s-arg --disable=traefik@server:0 --wait\n", opts.clusterName)
	}

	// 3. Install Gateway API CRDs
	printStep("Installing Gateway API CRDs...")
	if err := runCmd("kubectl", "apply", "-f", "https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.2.1/standard-install.yaml"); err != nil {
		return err
	}

	// 4. Install Envoy Gateway
	if opts.installGateway {
		printStep("Installing Envoy Gateway...")
		if !opts.dryRun {
			if err := runCmd("helm", "upgrade", "--install", "eg", "oci://docker.io/envoyproxy/gateway-helm", "--version", "v1.2.0", "-n", "envoy-gateway-system", "--create-namespace", "--wait"); err != nil {
				return err
			}
		} else {
			fmt.Fprintf(os.Stderr, "  (dry-run) helm install eg oci://docker.io/envoyproxy/gateway-helm --version v1.2.0 -n envoy-gateway-system --create-namespace --wait\n")
		}
	}

	// 5. Install Diverge CRDs
	printStep("Installing Diverge CRDs...")
	if err := runCmd("kubectl", "apply", "-f", "config/crd/bases/"); err != nil {
		return err
	}

	// 6. Install Diverge controller
	printStep("Installing Diverge controller...")
	if !opts.dryRun {
		if err := runCmd("helm", "upgrade", "--install", "diverge", "charts/diverge/", "-n", "diverge-system", "--create-namespace", "--wait"); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(os.Stderr, "  (dry-run) helm install diverge charts/diverge/ -n diverge-system --create-namespace --wait\n")
	}

	// 7. Install KEDA
	if opts.installKeda {
		printStep("Installing KEDA...")
		if !opts.dryRun {
			if err := runCmd("helm", "upgrade", "--install", "keda", "kedacore/keda", "-n", "keda", "--create-namespace"); err != nil {
				return err
			}
			if err := runCmd("helm", "upgrade", "--install", "keda-http", "kedacore/keda-add-ons-http", "-n", "keda"); err != nil {
				return err
			}
		} else {
			fmt.Fprintf(os.Stderr, "  (dry-run) helm install keda kedacore/keda -n keda --create-namespace\n")
			fmt.Fprintf(os.Stderr, "  (dry-run) helm install keda-http kedacore/keda-add-ons-http -n keda\n")
		}
	}

	// 8. Deploy sample app
	if opts.installSampleApp {
		printStep("Deploying sample app...")
		if !opts.dryRun {
			if err := runCmd("kubectl", "create", "deployment", "echo-server", "--image=ealen/echo-server"); err != nil {
				return err
			}
			if err := runCmd("kubectl", "expose", "deployment", "echo-server", "--port=8080", "--target-port=80"); err != nil {
				return err
			}
		} else {
			fmt.Fprintf(os.Stderr, "  (dry-run) kubectl create deployment echo-server --image=ealen/echo-server\n")
			fmt.Fprintf(os.Stderr, "  (dry-run) kubectl expose deployment echo-server --port=8080 --target-port=80\n")
		}
	}

	// 9. Print success message
	fmt.Fprintf(os.Stderr, "\n\033[32m✅ Diverge playground ready!\033[0m\n\n")
	fmt.Fprintf(os.Stderr, "Cluster:  k3d-%s\n", opts.clusterName)
	if opts.installGateway {
		fmt.Fprintf(os.Stderr, "Gateway:  Envoy Gateway v1.2.0\n")
	}
	if opts.installSampleApp {
		fmt.Fprintf(os.Stderr, "Sample:   echo-server (default namespace)\n")
	}
	fmt.Fprintf(os.Stderr, "\nNext steps:\n")
	fmt.Fprintf(os.Stderr, "  diverge dev --service echo-server -- curl localhost:8080\n")
	fmt.Fprintf(os.Stderr, "  kubectl get environments\n")
	fmt.Fprintf(os.Stderr, "  kubectl get previewgroups\n")

	return nil
}
