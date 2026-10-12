package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const (
	defaultOCIChartRef = "oci://ghcr.io/divergedev/charts/diverge"
)

type initOptions struct {
	clusterName      string
	provider         string
	installGateway   bool
	installKeda      bool
	installSampleApp bool
	dryRun           bool
	configOnly       bool
	outputPath       string
	force            bool
}

var (
	lookPath           = exec.LookPath
	execCommandContext = exec.CommandContext
	localChartExists   = func() bool {
		if info, err := os.Stat("config/crd/bases"); err != nil || !info.IsDir() {
			return false
		}
		if info, err := os.Stat("charts/diverge"); err != nil || !info.IsDir() {
			return false
		}
		return true
	}
)

func newInitCmd(app *App) *cobra.Command {
	opts := &initOptions{}

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize a ready-to-use local development playground or starter .diverge.yaml",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInit(cmd.Context(), opts)
		},
	}

	cmd.Flags().StringVar(&opts.clusterName, "cluster-name", "diverge-dev", "local cluster name")
	cmd.Flags().StringVar(&opts.provider, "provider", "auto", "cluster provider (auto, k3d, kind, existing)")
	cmd.Flags().BoolVar(&opts.installGateway, "install-gateway", true, "install Envoy Gateway")
	cmd.Flags().BoolVar(&opts.installKeda, "install-keda", false, "install KEDA HTTP Add-on")
	cmd.Flags().BoolVar(&opts.installSampleApp, "install-sample-app", true, "deploy echo-server sample")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "print commands without executing")
	cmd.Flags().BoolVarP(&opts.configOnly, "config", "c", false, "Generate a starter .diverge.yaml configuration file in the current directory instead of creating a cluster")
	cmd.Flags().StringVarP(&opts.outputPath, "output", "o", ".diverge.yaml", "Output path for generated .diverge.yaml configuration file")
	cmd.Flags().BoolVar(&opts.force, "force", false, "Overwrite existing .diverge.yaml")

	return cmd
}

type discoveredService struct {
	Name     string
	Paths    []string
	HelmPath string
}

func discoverServices(baseDir string) []discoveredService {
	byName := make(map[string]*discoveredService)
	var names []string

	for _, dirName := range []string{"services", "apps", "charts"} {
		entries, err := os.ReadDir(filepath.Join(baseDir, dirName))
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			name := entry.Name()
			svc, exists := byName[name]
			if !exists {
				svc = &discoveredService{Name: name}
				byName[name] = svc
				names = append(names, name)
			}
			svc.Paths = append(svc.Paths, fmt.Sprintf("%s/%s/**", dirName, name))
			if dirName == "charts" && svc.HelmPath == "" {
				svc.HelmPath = fmt.Sprintf("charts/%s", name)
			}
		}
	}

	if len(names) == 0 {
		return []discoveredService{
			{
				Name:  "api",
				Paths: []string{"src/**", "Dockerfile"},
			},
		}
	}

	sort.Strings(names)
	out := make([]discoveredService, 0, len(names))
	for _, n := range names {
		out = append(out, *byName[n])
	}
	return out
}

func runInitConfig(outputPath string, force bool) error {
	if outputPath == "" {
		outputPath = ".diverge.yaml"
	}
	if !force {
		if _, err := os.Stat(outputPath); err == nil {
			return fmt.Errorf("%s already exists (use --force to overwrite)", outputPath)
		}
	}

	baseDir := filepath.Dir(outputPath)
	svcs := discoverServices(baseDir)

	var sb strings.Builder
	sb.WriteString("# yaml-language-server: $schema=https://divergedev.com/schemas/diverge.schema.json\n")
	sb.WriteString("# Diverge preview environment configuration\n")
	sb.WriteString("# Docs: https://divergedev.com/reference/diverge-yaml/\n")
	sb.WriteString("version: \"1\"\n\n")
	sb.WriteString("services:\n")
	for _, svc := range svcs {
		fmt.Fprintf(&sb, "  %s:\n", svc.Name)
		sb.WriteString("    paths:\n")
		for _, p := range svc.Paths {
			fmt.Fprintf(&sb, "      - %q\n", p)
		}
		sb.WriteString("    image:\n")
		fmt.Fprintf(&sb, "      repository: \"ghcr.io/example/%s\"\n", svc.Name)
		sb.WriteString("      tag_template: \"mr-{{.MR}}\"\n")
		if svc.HelmPath != "" {
			sb.WriteString("    helm:\n")
			fmt.Fprintf(&sb, "      path: %q\n", svc.HelmPath)
		}
	}

	sb.WriteString("\ndefaults:\n")
	sb.WriteString("  deploy:\n")
	sb.WriteString("    mode: delta\n")
	sb.WriteString("  routing:\n")
	sb.WriteString("    mode: header\n")
	sb.WriteString("    baseline_namespace: default\n")
	sb.WriteString("    header_key: X-Preview-Env\n")
	sb.WriteString("  lifecycle:\n")
	sb.WriteString("    ttl: 72h\n")
	sb.WriteString("    cleanup_on_merge: true\n")

	if err := os.WriteFile(outputPath, []byte(sb.String()), 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", outputPath, err)
	}

	fmt.Fprintf(os.Stderr, "\033[32m✅ Generated starter configuration at %s\033[0m\n", outputPath)
	return nil
}

func resolveClusterProvider(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "auto":
		if _, err := lookPath("k3d"); err == nil {
			return "k3d", nil
		}
		if _, err := lookPath("kind"); err == nil {
			return "kind", nil
		}
		return "k3d", nil
	case "k3d", "kind", "existing":
		return strings.ToLower(strings.TrimSpace(raw)), nil
	default:
		return "", fmt.Errorf("unsupported provider %q (supported: auto, k3d, kind, existing)", raw)
	}
}

func runInit(ctx context.Context, opts *initOptions) error {
	if opts.configOnly {
		return runInitConfig(opts.outputPath, opts.force)
	}

	provider, err := resolveClusterProvider(opts.provider)
	if err != nil {
		return err
	}
	if opts.clusterName == "" {
		opts.clusterName = "diverge-dev"
	}

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
	var prereqs []string
	switch provider {
	case "k3d":
		prereqs = []string{"k3d", "kubectl", "helm"}
	case "kind":
		prereqs = []string{"kind", "kubectl", "helm"}
	case "existing":
		prereqs = []string{"kubectl", "helm"}
	}
	missing := []string{}
	for _, bin := range prereqs {
		if _, err := lookPath(bin); err != nil {
			missing = append(missing, bin)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("\033[31m✗ missing prerequisites: %s\033[0m, please install them first", strings.Join(missing, ", "))
	}

	// 2. Create or verify cluster
	switch provider {
	case "k3d":
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
	case "kind":
		printStep(fmt.Sprintf("Creating kind cluster '%s'...", opts.clusterName))
		if !opts.dryRun {
			cmdCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			out, err := execCommandContext(cmdCtx, "kind", "get", "clusters").CombinedOutput()
			cancel()
			exists := false
			if err == nil {
				for _, line := range strings.Split(string(out), "\n") {
					if strings.TrimSpace(line) == opts.clusterName {
						exists = true
						break
					}
				}
			}
			if exists {
				fmt.Fprintf(os.Stderr, "  Cluster %s already exists, skipping creation.\n", opts.clusterName)
			} else {
				if err := runCmd("kind", "create", "cluster", "--name", opts.clusterName, "--wait", "60s"); err != nil {
					return err
				}
			}
		} else {
			fmt.Fprintf(os.Stderr, "  (dry-run) kind create cluster --name %s --wait 60s\n", opts.clusterName)
		}
	case "existing":
		printStep("Using existing Kubernetes cluster...")
		fmt.Fprintf(os.Stderr, "  Skipping cluster creation (provider=existing).\n")
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

	useLocalChart := opts.dryRun || localChartExists()

	// 5. Install Diverge CRDs
	printStep("Installing Diverge CRDs...")
	if useLocalChart {
		if err := runCmd("kubectl", "apply", "-f", "config/crd/bases/"); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(os.Stderr, "  Using CRDs bundled in published Helm OCI chart (%s).\n", defaultOCIChartRef)
	}

	// 6. Install Diverge controller
	printStep("Installing Diverge controller...")
	if !opts.dryRun {
		chartRef := "charts/diverge/"
		if !useLocalChart {
			chartRef = defaultOCIChartRef
		}
		if err := runCmd("helm", "upgrade", "--install", "diverge", chartRef, "-n", "diverge-system", "--create-namespace", "--wait"); err != nil {
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
	switch provider {
	case "kind":
		fmt.Fprintf(os.Stderr, "Cluster:  kind-%s\n", opts.clusterName)
	case "existing":
		fmt.Fprintf(os.Stderr, "Cluster:  existing (current context)\n")
	default:
		fmt.Fprintf(os.Stderr, "Cluster:  k3d-%s\n", opts.clusterName)
	}
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
