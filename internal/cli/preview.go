package cli

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/yaml"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/git"
)

// newPreviewCmd creates the `diverge preview` parent command with
// create, status, delete, and watch subcommands.
func newPreviewCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "preview",
		Aliases: []string{"pg"},
		Short:   "Manage preview groups (multi-service preview environments)",
		Long: `Preview groups orchestrate multiple services into a single preview
environment. Each service can run a preview image, intercept locally,
or use the baseline (production) version.

Examples:
  diverge preview create --service payments-api,consent-mgr
  diverge preview status mr-42
  diverge preview delete mr-42
  diverge preview watch mr-42`,
	}

	cmd.AddCommand(newPreviewCreateCmd(app))
	cmd.AddCommand(newPreviewStatusCmd(app))
	cmd.AddCommand(newPreviewDeleteCmd(app))
	cmd.AddCommand(newPreviewWatchCmd(app))
	cmd.AddCommand(newPreviewInterceptCmd(app))
	cmd.AddCommand(newPreviewReleaseCmd(app))
	return cmd
}

// --- preview create ---

func newPreviewCreateCmd(app *App) *cobra.Command {
	var (
		name        string
		services    []string
		headerKey   string
		headerValue string
		ttl         string
		dryRun      bool
		mrNumber    int
	)

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a preview group from the current branch",
		Long: `Create a PreviewGroup CR that orchestrates multiple services
into a single header-routed preview environment.

Each --service flag takes the format:
  name=image[:tag][:port]   (image mode — deploy this container)
  name                      (baseline mode — use existing service as-is)

Examples:
  # Preview payments-api with a new image, baseline everything else
  diverge preview create \
    --service payments-api=registry.azra-ai.com/payments:mr-42 \
    --service consent-mgr \
    --mr 42

  # Dry-run to inspect the generated YAML
  diverge preview create --service payments-api=img:8080 --dry-run`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPreviewCreate(cmd, app, name, services, headerKey, headerValue, ttl, mrNumber, dryRun)
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "override preview group name (default: auto from MR/branch)")
	cmd.Flags().StringSliceVar(&services, "service", nil, "service spec: name:image[:port] or name (baseline)")
	cmd.Flags().StringVar(&headerKey, "header-key", "x-preview-env", "routing header key")
	cmd.Flags().StringVar(&headerValue, "header-value", "", "routing header value (default: preview group name)")
	cmd.Flags().StringVar(&ttl, "ttl", "", "auto-delete after duration (e.g. 72h)")
	cmd.Flags().IntVar(&mrNumber, "mr", 0, "MR/PR number")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print PreviewGroup YAML without creating")
	_ = cmd.MarkFlagRequired("service")

	return cmd
}

func runPreviewCreate(cmd *cobra.Command, app *App, name string, services []string, headerKey, headerValue, ttl string, mrNumber int, dryRun bool) error {
	// Detect git context
	gitCtx, err := git.Detect()
	if err != nil {
		return fmt.Errorf("failed to detect git context: %w", err)
	}

	// Generate name
	if name == "" {
		name = generateEnvName("preview", mrNumber, gitCtx.Branch)
	}

	if headerValue == "" {
		headerValue = name
	}

	// Parse service specs
	svcSpecs, err := parseServiceSpecs(services)
	if err != nil {
		return err
	}

	// Build PreviewGroup CR
	pg := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: divergeiov1alpha1.PreviewGroupSpec{
			Source: divergeiov1alpha1.EnvironmentSource{
				Provider: gitCtx.Provider,
				Project:  gitCtx.Project,
				Branch:   gitCtx.Branch,
				MR:       mrNumber,
			},
			Routing: divergeiov1alpha1.PreviewGroupRouting{
				HeaderKey:   headerKey,
				HeaderValue: headerValue,
			},
			Services: svcSpecs,
		},
	}

	// TTL
	if ttl != "" {
		d, err := time.ParseDuration(ttl)
		if err != nil {
			return fmt.Errorf("invalid --ttl %q: %w", ttl, err)
		}
		pg.Spec.Lifecycle = &divergeiov1alpha1.PreviewGroupLifecycle{
			TTL: &metav1.Duration{Duration: d},
		}
	}

	if dryRun {
		data, err := yaml.Marshal(pg)
		if err != nil {
			return fmt.Errorf("failed to marshal PreviewGroup: %w", err)
		}
		fmt.Println("---")
		fmt.Printf("# dry-run: would create PreviewGroup %q\n", pg.Name)
		fmt.Print(string(data))
		return nil
	}

	c, _, err := app.KubeClient()
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	if err := c.Create(cmd.Context(), pg); err != nil {
		return fmt.Errorf("failed to create PreviewGroup: %w", err)
	}

	fmt.Printf("✅ PreviewGroup %q created (%d services)\n", pg.Name, len(svcSpecs))
	fmt.Printf("   Header: %s: %s\n", headerKey, headerValue)
	fmt.Printf("   Run 'diverge preview status %s' to monitor.\n", pg.Name)
	return nil
}

// parseServiceSpecs parses --service flags into PreviewGroupServiceSpec.
// Format: name=image[:tag][:port] or name (baseline).
// If no '=' is present, the service is treated as baseline.
// The port is detected as the last segment if it's purely numeric.
func parseServiceSpecs(services []string) ([]divergeiov1alpha1.PreviewGroupServiceSpec, error) {
	specs := make([]divergeiov1alpha1.PreviewGroupServiceSpec, 0, len(services))
	for _, s := range services {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		eqIdx := strings.Index(s, "=")
		if eqIdx < 0 {
			// No '=' → baseline mode
			specs = append(specs, divergeiov1alpha1.PreviewGroupServiceSpec{
				Name: s,
				Mode: divergeiov1alpha1.ServiceModeBaseline,
			})
			continue
		}

		name := s[:eqIdx]
		rest := s[eqIdx+1:]

		// Check if last colon-segment is a port number
		var image string
		var port int32 = 8080
		lastColon := strings.LastIndex(rest, ":")
		if lastColon > 0 {
			maybePart := rest[lastColon+1:]
			if p, err := strconv.ParseInt(maybePart, 10, 32); err == nil && p > 0 && p <= 65535 {
				image = rest[:lastColon]
				port = int32(p)
			} else {
				// Not a port — whole thing is image (e.g. image:tag)
				image = rest
				port = 8080
			}
		} else {
			image = rest
		}

		if name == "" || image == "" {
			return nil, fmt.Errorf("invalid service spec %q: name and image cannot be empty", s)
		}

		specs = append(specs, divergeiov1alpha1.PreviewGroupServiceSpec{
			Name:  name,
			Image: image,
			Mode:  divergeiov1alpha1.ServiceModeImage,
			Port:  port,
		})
	}
	return specs, nil
}

// --- preview status ---

func newPreviewStatusCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status <name>",
		Short: "Show status of a preview group",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPreviewStatus(cmd.Context(), app, args[0], cmd.OutOrStdout())
		},
	}
	return cmd
}

func runPreviewStatus(ctx context.Context, app *App, name string, out io.Writer) error {
	c, _, err := app.KubeClient()
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	var pg divergeiov1alpha1.PreviewGroup
	if err := c.Get(ctx, types.NamespacedName{Name: name}, &pg); err != nil {
		return fmt.Errorf("PreviewGroup %q not found: %w", name, err)
	}

	// Header
	phaseIcon := phaseEmoji(string(pg.Status.Phase))
	_, _ = fmt.Fprintf(out, "%s PreviewGroup: %s\n", phaseIcon, pg.Name)
	_, _ = fmt.Fprintf(out, "   Phase: %s\n", pg.Status.Phase)
	if pg.Spec.Routing.Mode == "" || pg.Spec.Routing.Mode == "header" {
		_, _ = fmt.Fprintf(out, "   Header: %s: %s\n", pg.Spec.Routing.HeaderKey, pg.Spec.Routing.HeaderValue)
	} else {
		_, _ = fmt.Fprintf(out, "   Routing: %s\n", pg.Spec.Routing.Mode)
	}
	_, _ = fmt.Fprintf(out, "   Source: %s/%s @ %s\n", pg.Spec.Source.Provider, pg.Spec.Source.Project, pg.Spec.Source.Branch)

	if pg.Status.ExpiresAt != nil {
		remaining := time.Until(pg.Status.ExpiresAt.Time)
		if remaining > 0 {
			_, _ = fmt.Fprintf(out, "   Expires: %s (%s remaining)\n", pg.Status.ExpiresAt.Format(time.RFC3339), remaining.Round(time.Minute))
		} else {
			_, _ = fmt.Fprintf(out, "   Expires: EXPIRED\n")
		}
	}

	// Services table
	_, _ = fmt.Fprintln(out)
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintf(w, "   SERVICE\tNAMESPACE\tPHASE\tURL\n")
	_, _ = fmt.Fprintf(w, "   ───────\t─────────\t─────\t───\n")
	for _, svc := range pg.Status.Services {
		icon := phaseEmoji(string(svc.Phase))
		url := svc.URL
		if url == "" {
			url = "-"
		}
		_, _ = fmt.Fprintf(w, "   %s %s\t%s\t%s\t%s\n", icon, svc.Name, svc.Namespace, svc.Phase, url)
	}
	_ = w.Flush()

	// Check for async routes in child environments
	var asyncRoutes []divergeiov1alpha1.AsyncRouteSpec
	for _, svc := range pg.Status.Services {
		if svc.EnvironmentName != "" {
			var childEnv divergeiov1alpha1.Environment
			if err := c.Get(ctx, types.NamespacedName{Name: svc.EnvironmentName, Namespace: app.Namespace}, &childEnv); err == nil {
				asyncRoutes = append(asyncRoutes, childEnv.Spec.Routing.AsyncRoutes...)
			}
		}
	}

	if len(asyncRoutes) > 0 {
		_, _ = fmt.Fprintln(out)
		_, _ = fmt.Fprintln(out, "   Async Routes:")
		for _, route := range asyncRoutes {
			envVar := route.EnvVarMapping[route.Target]
			if envVar == "" && len(route.EnvVarMapping) > 0 {
				for _, v := range route.EnvVarMapping {
					envVar = v
					break
				}
			}
			if envVar == "" {
				envVar = divergeiov1alpha1.DefaultEnvVarForProtocol(route.Protocol)
			}
			_, _ = fmt.Fprintf(out, "     %-8s → %-14s (%s)\n", route.Protocol, route.Target, envVar)
		}

		asyncStatus := "⏳ Pending"
		for _, cond := range pg.Status.Conditions {
			if cond.Type == "AsyncRoutingReady" {
				if cond.Status == metav1.ConditionTrue {
					asyncStatus = "✅ Provisioned"
				} else {
					asyncStatus = "❌ " + cond.Reason
				}
				break
			}
		}
		_, _ = fmt.Fprintf(out, "   Async Status: %s\n", asyncStatus)
	}

	// Conditions
	if len(pg.Status.Conditions) > 0 {
		_, _ = fmt.Fprintln(out)
		for _, c := range pg.Status.Conditions {
			icon := "⏳"
			switch c.Status {
			case metav1.ConditionTrue:
				icon = "✅"
			case metav1.ConditionFalse:
				icon = "❌"
			}
			_, _ = fmt.Fprintf(out, "   %s %s: %s (%s)\n", icon, c.Type, c.Message, c.Reason)
		}
	}

	return nil
}

// --- preview delete ---

func newPreviewDeleteCmd(app *App) *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "delete <name>",
		Short: "Delete a preview group and all its child environments",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPreviewDelete(cmd.Context(), app, args[0], force)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "skip confirmation")
	return cmd
}

func runPreviewDelete(ctx context.Context, app *App, name string, force bool) error {
	c, _, err := app.KubeClient()
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	// Verify it exists
	var pg divergeiov1alpha1.PreviewGroup
	if err := c.Get(ctx, types.NamespacedName{Name: name}, &pg); err != nil {
		return fmt.Errorf("PreviewGroup %q not found: %w", name, err)
	}

	if !force {
		fmt.Printf("Delete PreviewGroup %q and all %d child environments? [y/N] ", name, pg.Status.ServiceCount)
		var confirm string
		_, _ = fmt.Scanln(&confirm)
		if strings.ToLower(confirm) != "y" {
			fmt.Println("Cancelled.")
			return nil
		}
	}

	if err := c.Delete(ctx, &pg); err != nil {
		return fmt.Errorf("failed to delete PreviewGroup: %w", err)
	}

	fmt.Printf("🗑️  PreviewGroup %q deleted. Teardown in progress.\n", name)
	return nil
}

// --- preview watch ---

func newPreviewWatchCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "watch <name>",
		Short: "Watch a preview group until it reaches Running or Failed",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPreviewWatch(cmd.Context(), app, args[0])
		},
	}
	return cmd
}

func runPreviewWatch(ctx context.Context, app *App, name string) error {
	c, _, err := app.KubeClient()
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	// Initial status
	fmt.Printf("👁️  Watching PreviewGroup %q...\n\n", name)

	// Poll-based watch (controller-runtime client doesn't support native watches
	// on CRDs without informer setup; polling at 2s is acceptable for CLI)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	var lastPhase divergeiov1alpha1.PreviewGroupPhase
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			var pg divergeiov1alpha1.PreviewGroup
			if err := c.Get(ctx, types.NamespacedName{Name: name}, &pg); err != nil {
				return fmt.Errorf("PreviewGroup %q not found: %w", name, err)
			}

			if pg.Status.Phase != lastPhase {
				lastPhase = pg.Status.Phase
				ts := time.Now().Format("15:04:05")
				fmt.Printf("[%s] Phase: %s %s\n", ts, phaseEmoji(string(pg.Status.Phase)), pg.Status.Phase)

				for _, svc := range pg.Status.Services {
					fmt.Printf("         %s %s: %s", phaseEmoji(string(svc.Phase)), svc.Name, svc.Phase)
					if svc.Message != "" {
						fmt.Printf(" (%s)", svc.Message)
					}
					fmt.Println()
				}
				fmt.Println()
			}

			// Terminal states
			switch pg.Status.Phase {
			case divergeiov1alpha1.PreviewGroupPhaseRunning:
				fmt.Printf("✅ PreviewGroup %q is ready!\n", name)
				fmt.Printf("   Header: %s: %s\n", pg.Spec.Routing.HeaderKey, pg.Spec.Routing.HeaderValue)
				return nil
			case divergeiov1alpha1.PreviewGroupPhaseFailed:
				return fmt.Errorf("PreviewGroup %q failed", name)
			}
		}
	}
}

// --- helpers ---

// phaseEmoji returns a status emoji for a phase string.
func phaseEmoji(phase string) string {
	switch divergeiov1alpha1.PreviewGroupPhase(phase) {
	case divergeiov1alpha1.PreviewGroupPhaseRunning:
		return "✅"
	case divergeiov1alpha1.PreviewGroupPhaseFailed:
		return "❌"
	case divergeiov1alpha1.PreviewGroupPhaseDegraded:
		return "⚠️"
	case divergeiov1alpha1.PreviewGroupPhaseDeploying:
		return "🔄"
	default:
		return "⏳"
	}
}
