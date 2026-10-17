package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

const (
	defaultOCIChartRef = "oci://ghcr.io/divergedev/charts/diverge"
)

type initOptions struct {
	clusterName      string
	provider         string
	kubeContext      string
	installGateway   bool
	installKeda      bool
	installSampleApp bool
	dryRun           bool
	configOnly       bool
	outputPath       string
	force            bool
	errOut           io.Writer
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
			if app != nil && opts.kubeContext == "" {
				opts.kubeContext = strings.TrimSpace(app.Context)
			}
			opts.errOut = cmd.ErrOrStderr()
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

type starterHelmYAML struct {
	Path string `yaml:"path"`
}

type starterImageYAML struct {
	Repository  string `yaml:"repository"`
	TagTemplate string `yaml:"tag_template"`
}

type starterServiceYAML struct {
	Paths       []string                           `yaml:"paths"`
	Image       starterImageYAML                   `yaml:"image"`
	Helm        *starterHelmYAML                   `yaml:"helm,omitempty"`
	AsyncRoutes []divergeiov1alpha1.AsyncRouteSpec `yaml:"asyncRoutes,omitempty"`
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

func containsGitComponent(p string) bool {
	cleaned := filepath.Clean(p)
	for _, part := range strings.Split(cleaned, string(filepath.Separator)) {
		if part == ".git" {
			return true
		}
	}
	return false
}

func resolvePathFollowingSymlinks(absPath string) (string, error) {
	cleaned := filepath.Clean(absPath)
	cur := cleaned
	var missing []string
	for {
		resolved, err := filepath.EvalSymlinks(cur)
		if err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return filepath.Clean(resolved), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return cleaned, nil
		}
		missing = append(missing, filepath.Base(cur))
		cur = parent
	}
}

func isSameOrSubpath(candidate, root string) bool {
	candidate = filepath.Clean(candidate)
	root = filepath.Clean(root)
	if candidate == root {
		return true
	}
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func parseGitDirFile(dotGitFile string) string {
	data, err := os.ReadFile(dotGitFile)
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(data))
	if !strings.HasPrefix(line, "gitdir:") {
		return ""
	}
	raw := strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
	if raw == "" {
		return ""
	}
	if !filepath.IsAbs(raw) {
		raw = filepath.Join(filepath.Dir(dotGitFile), raw)
	}
	if resolved, err := resolvePathFollowingSymlinks(raw); err == nil {
		return resolved
	}
	return filepath.Clean(raw)
}

func checkAncestorsForGitMetadata(startDir, absPath, resolvedPath string) error {
	dir := filepath.Clean(startDir)
	for {
		dotGit := filepath.Join(dir, ".git")
		if info, err := os.Lstat(dotGit); err == nil {
			if isSameOrSubpath(absPath, dotGit) || isSameOrSubpath(resolvedPath, dotGit) {
				return fmt.Errorf("refusing to write configuration into Git metadata path %q", dotGit)
			}
			if resolvedDotGit, err := resolvePathFollowingSymlinks(dotGit); err == nil {
				if isSameOrSubpath(resolvedPath, resolvedDotGit) {
					return fmt.Errorf("refusing to write configuration into resolved Git path %q", resolvedDotGit)
				}
				if info.Mode().IsRegular() || (info.Mode()&os.ModeSymlink != 0) {
					if gitDir := parseGitDirFile(resolvedDotGit); gitDir != "" {
						if isSameOrSubpath(resolvedPath, gitDir) {
							return fmt.Errorf("refusing to write configuration into linked worktree Git directory %q", gitDir)
						}
						if commonBytes, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
							commonRaw := strings.TrimSpace(string(commonBytes))
							if commonRaw != "" {
								if !filepath.IsAbs(commonRaw) {
									commonRaw = filepath.Join(gitDir, commonRaw)
								}
								if resolvedCommon, err := resolvePathFollowingSymlinks(commonRaw); err == nil {
									if isSameOrSubpath(resolvedPath, resolvedCommon) {
										return fmt.Errorf("refusing to write configuration into common Git directory %q", resolvedCommon)
									}
								}
							}
						}
					}
				}
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return nil
}

func validateSafeConfigOutputPath(outputPath string) error {
	if containsGitComponent(outputPath) {
		return fmt.Errorf("refusing to write configuration into Git directory %q", outputPath)
	}
	absPath, err := filepath.Abs(outputPath)
	if err != nil {
		return fmt.Errorf("failed to resolve output path %q: %w", outputPath, err)
	}
	if containsGitComponent(absPath) {
		return fmt.Errorf("refusing to write configuration into Git directory %q", outputPath)
	}
	resolvedPath, err := resolvePathFollowingSymlinks(absPath)
	if err != nil {
		return fmt.Errorf("failed to evaluate symlinks for %q: %w", outputPath, err)
	}
	if containsGitComponent(resolvedPath) {
		return fmt.Errorf("refusing to write configuration into resolved Git path %q", resolvedPath)
	}
	if err := checkAncestorsForGitMetadata(filepath.Dir(absPath), absPath, resolvedPath); err != nil {
		return err
	}
	if err := checkAncestorsForGitMetadata(filepath.Dir(resolvedPath), absPath, resolvedPath); err != nil {
		return err
	}
	return nil
}

func detectRepoStack(baseDir string) (hasPostgres bool, asyncRoutes []divergeiov1alpha1.AsyncRouteSpec) {
	scanDirs := []string{baseDir}

	entries, err := os.ReadDir(baseDir)
	if err == nil {
		for _, entry := range entries {
			if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			childDir := filepath.Join(baseDir, entry.Name())
			scanDirs = append(scanDirs, childDir)
			if entry.Name() == "services" || entry.Name() == "apps" {
				subEntries, subErr := os.ReadDir(childDir)
				if subErr == nil {
					for _, sub := range subEntries {
						if sub.IsDir() && !strings.HasPrefix(sub.Name(), ".") {
							scanDirs = append(scanDirs, filepath.Join(childDir, sub.Name()))
						}
					}
				}
			}
		}
	}

	var combined strings.Builder
	for _, dir := range scanDirs {
		if info, err := os.Stat(filepath.Join(dir, "atlas.hcl")); err == nil && !info.IsDir() {
			hasPostgres = true
		}
		if info, err := os.Stat(filepath.Join(dir, "migrations")); err == nil && info.IsDir() {
			hasPostgres = true
		}
		if info, err := os.Stat(filepath.Join(dir, "prisma", "schema.prisma")); err == nil && !info.IsDir() {
			hasPostgres = true
		}

		for _, fileName := range []string{
			"package.json",
			"go.mod",
			"pyproject.toml",
			"requirements.txt",
			"atlas.hcl",
			"docker-compose.yml",
			"compose.yaml",
		} {
			data, err := os.ReadFile(filepath.Join(dir, fileName))
			if err != nil {
				continue
			}
			combined.Write(data)
			combined.WriteByte('\n')
		}
	}

	content := combined.String()
	hasAny := func(tokens ...string) bool {
		for _, tok := range tokens {
			if strings.Contains(content, tok) {
				return true
			}
		}
		return false
	}

	if hasAny("pgx", `"pg"`, "psycopg", "asyncpg", "sqlalchemy", "prisma", "drizzle-orm", "postgres") {
		hasPostgres = true
	}

	if hasAny("ioredis", `"redis"`, "go-redis", "valkey") {
		asyncRoutes = append(asyncRoutes, divergeiov1alpha1.AsyncRouteSpec{
			Protocol: "redis",
			Target:   "cache",
		})
	}

	if hasAny("@aws-sdk/client-s3", "aws-sdk-go-v2/service/s3", "boto3", "minio") {
		asyncRoutes = append(asyncRoutes, divergeiov1alpha1.AsyncRouteSpec{
			Protocol: "s3",
			Target:   "uploads",
		})
	} else if hasAny("@google-cloud/storage", "cloud.google.com/go/storage") {
		asyncRoutes = append(asyncRoutes, divergeiov1alpha1.AsyncRouteSpec{
			Protocol: "gcs",
			Target:   "uploads",
		})
	}

	if hasAny("kafkajs", "franz-go", "sarama", "confluent-kafka", "aiokafka") {
		asyncRoutes = append(asyncRoutes, divergeiov1alpha1.AsyncRouteSpec{
			Protocol: "kafka",
			Target:   "events",
		})
	}

	if hasAny("@temporalio/", "go.temporal.io/sdk", "temporalio") {
		asyncRoutes = append(asyncRoutes, divergeiov1alpha1.AsyncRouteSpec{
			Protocol: "temporal",
			Target:   "default-task-queue",
		})
	}

	return hasPostgres, asyncRoutes
}

func runInitConfig(outputPath string, force bool) error {
	return runInitConfigWithWriter(outputPath, force, nil)
}

func runInitConfigWithWriter(outputPath string, force bool, errOut io.Writer) error {
	if outputPath == "" {
		outputPath = ".diverge.yaml"
	}
	if err := validateSafeConfigOutputPath(outputPath); err != nil {
		return err
	}
	if !force {
		if _, err := os.Lstat(outputPath); err == nil {
			return fmt.Errorf("%s already exists (use --force to overwrite)", outputPath)
		}
	}

	baseDir := filepath.Dir(outputPath)
	svcs := discoverServices(baseDir)
	hasPostgres, asyncRoutes := detectRepoStack(baseDir)

	servicesDoc := struct {
		Services map[string]starterServiceYAML `yaml:"services"`
	}{
		Services: make(map[string]starterServiceYAML, len(svcs)),
	}
	for i, svc := range svcs {
		entry := starterServiceYAML{
			Paths: svc.Paths,
			Image: starterImageYAML{
				Repository:  fmt.Sprintf("ghcr.io/example/%s", svc.Name),
				TagTemplate: "mr-{{.MR}}",
			},
		}
		if svc.HelmPath != "" {
			entry.Helm = &starterHelmYAML{Path: svc.HelmPath}
		}
		if i == 0 && len(asyncRoutes) > 0 {
			entry.AsyncRoutes = asyncRoutes
		}
		servicesDoc.Services[svc.Name] = entry
	}

	var svcBuf bytes.Buffer
	enc := yaml.NewEncoder(&svcBuf)
	enc.SetIndent(2)
	if err := enc.Encode(&servicesDoc); err != nil {
		return fmt.Errorf("failed to encode services YAML: %w", err)
	}
	_ = enc.Close()

	var sb strings.Builder
	sb.WriteString("# yaml-language-server: $schema=https://divergedev.com/schemas/diverge.schema.json\n")
	sb.WriteString("# Diverge preview environment configuration\n")
	sb.WriteString("# Docs: https://divergedev.com/reference/diverge-yaml/\n")
	sb.WriteString("version: \"1\"\n\n")
	sb.WriteString(strings.TrimRight(svcBuf.String(), "\n"))
	sb.WriteString("\n\ndefaults:\n")
	sb.WriteString("  deploy:\n")
	sb.WriteString("    mode: delta\n")
	sb.WriteString("  routing:\n")
	sb.WriteString("    mode: header\n")
	sb.WriteString("    baseline_namespace: default\n")
	sb.WriteString("    header_key: x-diverge-env\n")
	if hasPostgres {
		sb.WriteString("  database:\n")
		sb.WriteString("    mode: schema\n")
		sb.WriteString("    seed_source: public\n")
	}
	sb.WriteString("  lifecycle:\n")
	sb.WriteString("    ttl: 72h\n")
	sb.WriteString("    cleanup_on_merge: true\n")

	if err := validateSafeConfigOutputPath(outputPath); err != nil {
		return err
	}
	if err := os.WriteFile(outputPath, []byte(sb.String()), 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", outputPath, err)
	}

	if hasPostgres || len(asyncRoutes) > 0 {
		var features []string
		if hasPostgres {
			features = append(features, "database=schema")
		}
		if len(asyncRoutes) > 0 {
			protos := make([]string, 0, len(asyncRoutes))
			for _, r := range asyncRoutes {
				protos = append(protos, r.Protocol)
			}
			features = append(features, fmt.Sprintf("async=[%s]", strings.Join(protos, " ")))
		}
		summaryLine := fmt.Sprintf("Auto-detected stack features: %s\n", strings.Join(features, ", "))
		fmt.Fprint(os.Stderr, summaryLine)
		if errOut != nil && errOut != os.Stderr {
			_, _ = fmt.Fprint(errOut, summaryLine)
		}
	}

	fmt.Fprintf(os.Stderr, "\033[32m✅ Generated starter configuration at %s\033[0m\n", outputPath)
	if errOut != nil && errOut != os.Stderr {
		_, _ = fmt.Fprintf(errOut, "\033[32m✅ Generated starter configuration at %s\033[0m\n", outputPath)
	}
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
		return runInitConfigWithWriter(opts.outputPath, opts.force, opts.errOut)
	}

	provider, err := resolveClusterProvider(opts.provider)
	if err != nil {
		return err
	}
	if opts.clusterName == "" {
		opts.clusterName = "diverge-dev"
	}

	var targetContext string
	switch provider {
	case "k3d":
		targetContext = "k3d-" + opts.clusterName
	case "kind":
		targetContext = "kind-" + opts.clusterName
	case "existing":
		targetContext = strings.TrimSpace(opts.kubeContext)
	}

	withKubectlContext := func(args ...string) []string {
		if targetContext == "" {
			return args
		}
		return append(args, "--context", targetContext)
	}
	withHelmContext := func(args ...string) []string {
		if targetContext == "" {
			return args
		}
		return append(args, "--kube-context", targetContext)
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
				if err := runCmd("kubectl", "config", "use-context", targetContext); err != nil {
					return err
				}
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
				if err := runCmd("kubectl", "config", "use-context", targetContext); err != nil {
					return err
				}
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
		if targetContext != "" {
			if err := runCmd("kubectl", "config", "use-context", targetContext); err != nil {
				return err
			}
		}
	}

	// 3. Install Gateway API CRDs
	printStep("Installing Gateway API CRDs...")
	if err := runCmd("kubectl", withKubectlContext("apply", "-f", "https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.2.1/standard-install.yaml")...); err != nil {
		return err
	}

	// 4. Install Envoy Gateway
	if opts.installGateway {
		printStep("Installing Envoy Gateway...")
		if !opts.dryRun {
			if err := runCmd("helm", withHelmContext("upgrade", "--install", "eg", "oci://docker.io/envoyproxy/gateway-helm", "--version", "v1.2.0", "-n", "envoy-gateway-system", "--create-namespace", "--wait")...); err != nil {
				return err
			}
		} else {
			fmt.Fprintf(os.Stderr, "  (dry-run) helm %s\n", strings.Join(withHelmContext("install", "eg", "oci://docker.io/envoyproxy/gateway-helm", "--version", "v1.2.0", "-n", "envoy-gateway-system", "--create-namespace", "--wait"), " "))
		}
	}

	useLocalChart := localChartExists()

	// 5. Install Diverge CRDs
	printStep("Installing Diverge CRDs...")
	if useLocalChart {
		if err := runCmd("kubectl", withKubectlContext("apply", "-f", "config/crd/bases/")...); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(os.Stderr, "  Using CRDs bundled in published Helm OCI chart (%s).\n", defaultOCIChartRef)
	}

	// 6. Install Diverge controller
	printStep("Installing Diverge controller...")
	chartRef := "charts/diverge/"
	if !useLocalChart {
		chartRef = defaultOCIChartRef
	}
	if !opts.dryRun {
		if err := runCmd("helm", withHelmContext("upgrade", "--install", "diverge", chartRef, "-n", "diverge-system", "--create-namespace", "--wait")...); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(os.Stderr, "  (dry-run) helm %s\n", strings.Join(withHelmContext("install", "diverge", chartRef, "-n", "diverge-system", "--create-namespace", "--wait"), " "))
	}

	// 7. Install KEDA
	if opts.installKeda {
		printStep("Installing KEDA...")
		if !opts.dryRun {
			if err := runCmd("helm", withHelmContext("upgrade", "--install", "keda", "kedacore/keda", "-n", "keda", "--create-namespace")...); err != nil {
				return err
			}
			if err := runCmd("helm", withHelmContext("upgrade", "--install", "keda-http", "kedacore/keda-add-ons-http", "-n", "keda")...); err != nil {
				return err
			}
		} else {
			fmt.Fprintf(os.Stderr, "  (dry-run) helm %s\n", strings.Join(withHelmContext("install", "keda", "kedacore/keda", "-n", "keda", "--create-namespace"), " "))
			fmt.Fprintf(os.Stderr, "  (dry-run) helm %s\n", strings.Join(withHelmContext("install", "keda-http", "kedacore/keda-add-ons-http", "-n", "keda"), " "))
		}
	}

	// 8. Deploy sample app
	if opts.installSampleApp {
		printStep("Deploying sample app...")
		if !opts.dryRun {
			if err := runCmd("kubectl", withKubectlContext("create", "deployment", "echo-server", "--image=ealen/echo-server")...); err != nil {
				return err
			}
			if err := runCmd("kubectl", withKubectlContext("expose", "deployment", "echo-server", "--port=8080", "--target-port=80")...); err != nil {
				return err
			}
		} else {
			fmt.Fprintf(os.Stderr, "  (dry-run) kubectl %s\n", strings.Join(withKubectlContext("create", "deployment", "echo-server", "--image=ealen/echo-server"), " "))
			fmt.Fprintf(os.Stderr, "  (dry-run) kubectl %s\n", strings.Join(withKubectlContext("expose", "deployment", "echo-server", "--port=8080", "--target-port=80"), " "))
		}
	}

	// 9. Print success message
	fmt.Fprintf(os.Stderr, "\n\033[32m✅ Diverge playground ready!\033[0m\n\n")
	switch provider {
	case "kind":
		fmt.Fprintf(os.Stderr, "Cluster:  kind-%s\n", opts.clusterName)
	case "existing":
		if targetContext != "" {
			fmt.Fprintf(os.Stderr, "Cluster:  existing (%s)\n", targetContext)
		} else {
			fmt.Fprintf(os.Stderr, "Cluster:  existing (current context)\n")
		}
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
