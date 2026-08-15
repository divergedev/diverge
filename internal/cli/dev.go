package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/git"
)

var ErrCollision = errors.New("preview group collision")

type DevOptions struct {
	Detector       EnvironmentDetector
	resolvedEnvMap map[string]string
}

type DevOption func(*DevOptions)

func WithEnvironmentDetector(d EnvironmentDetector) DevOption {
	return func(o *DevOptions) { o.Detector = d }
}

func newDevCmd(app *App) *cobra.Command {
	var (
		serviceFlag   string
		portFlag      int32
		endpointFlag  string
		envOutputFlag string
	)

	cmd := &cobra.Command{
		Use:   "dev",
		Short: "Route cluster traffic for a service to your local machine",
		Long: `Start a local development session by creating a PreviewGroup that routes
traffic for the specified service to your local machine's Tailscale IP.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDev(app, serviceFlag, portFlag, endpointFlag, envOutputFlag, args, cmd)
		},
	}
	cmd.Flags().StringVar(&serviceFlag, "service", "", "Service name (default: auto-detect)")
	cmd.Flags().Int32Var(&portFlag, "port", 0, "Local port (default: 8080)")
	cmd.Flags().StringVar(&endpointFlag, "endpoint", "", "Local endpoint IP (default: tailscale ip -4)")
	cmd.Flags().StringVar(&envOutputFlag, "env-output", "inject", "How to handle env vars: inject (in-memory), file (.env.diverge)")

	return cmd
}

func runDev(app *App, serviceFlag string, portFlag int32, endpointFlag string, envOutputFlag string, args []string, cmd *cobra.Command, opts ...DevOption) error {
	ctx := cmd.Context()

	devOpts := &DevOptions{
		Detector: &DefaultEnvironmentDetector{},
	}
	for _, opt := range opts {
		opt(devOpts)
	}
	detector := devOpts.Detector

	// 1. Auto-detect service name
	serviceName := serviceFlag
	if serviceName == "" {
		s, err := detector.DetectServiceName(ctx)
		if err != nil {
			return fmt.Errorf("failed to detect service name: %w. Use --service flag", err)
		}
		serviceName = s
	}
	if serviceName == "" {
		return fmt.Errorf("could not determine service name: use --service flag")
	}

	// 2. Auto-detect endpoint
	endpointIP := endpointFlag
	if endpointIP == "" {
		ip, err := detector.DetectLocalIP(ctx)
		if err != nil {
			return err
		}
		endpointIP = ip
	}

	// 3. Auto-detect port
	port := portFlag
	if port == 0 {
		port = 8080
	}
	endpoint := fmt.Sprintf("%s:%d", endpointIP, port)

	// 4. Construct header value
	headerValue := "local-dev"
	branch, err := detector.DetectGitBranch(ctx)
	if err == nil && branch != "" {
		headerValue = git.SlugifyBranch(branch)
	} else if err != nil {
		slog.Debug("failed to detect git branch", "error", err)
	}

	// 5. Construct group name
	username, _ := detector.DetectUsername(ctx)
	groupName := fmt.Sprintf("dev-%s-%s", username, serviceName)

	// Normalize group name
	groupName = strings.ReplaceAll(groupName, "_", "-")
	groupName = strings.ToLower(groupName)
	groupName = strings.TrimRight(groupName, "-")

	// 6. Create PreviewGroup CR
	pg := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name: groupName,
		},
		Spec: divergeiov1alpha1.PreviewGroupSpec{
			Owner: username,
			Services: []divergeiov1alpha1.PreviewGroupServiceSpec{
				{
					Name:     serviceName,
					Mode:     divergeiov1alpha1.ServiceModeLocal,
					Endpoint: endpoint,
				},
			},
			Routing: divergeiov1alpha1.PreviewGroupRouting{
				HeaderKey:   "x-diverge-env",
				HeaderValue: headerValue,
			},
			Source: divergeiov1alpha1.EnvironmentSource{
				Branch: headerValue,
			},
		},
	}

	c, clientset, err := app.KubeClient()
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	// Sync env vars from baseline pod
	ns := app.Namespace
	if ns == "" {
		ns = "default"
	}

	if envOutputFlag == "file" {
		synced, syncErr := syncBaselineEnvToFile(ctx, clientset, syncEnvOptions{
			Namespace:   ns,
			ServiceName: serviceName,
		}, ".env.diverge")
		if syncErr != nil {
			fmt.Printf("⚠️  Could not sync env vars: %v\n", syncErr)
		} else if synced > 0 {
			fmt.Printf("📋 Synced %d env vars from baseline → .env.diverge\n", synced)
		}
	} else {
		if len(args) == 0 {
			// Instead of writing to file, capture env vars in a buffer
			var envBuf bytes.Buffer
			synced, syncErr := syncBaselineEnv(ctx, clientset, syncEnvOptions{
				Namespace:   ns,
				ServiceName: serviceName,
			}, &envBuf)

			if syncErr == nil && synced > 0 {
				fmt.Printf("📋 Captured %d env vars from baseline (in-memory, no file written)\n", synced)
			} else if syncErr != nil {
				fmt.Printf("⚠️  Could not sync env vars: %v\n", syncErr)
			}
		} else {
			// Resolve env directly into a map for child process
			pod, err := findBaselinePod(ctx, clientset, ns, serviceName)
			if err != nil {
				return fmt.Errorf("failed to find baseline pod: %w", err)
			}
			resolvedEnv, err := resolveBaselineEnv(ctx, clientset, pod)
			if err != nil {
				return fmt.Errorf("failed to resolve baseline env: %w", err)
			}
			fmt.Printf("📋 Resolved %d env vars from baseline (in-memory, no file written)\n", len(resolvedEnv))
			// We will inject these into the command process below
			devOpts.resolvedEnvMap = resolvedEnv
		}
	}

	fmt.Printf("Starting dev session for service %q...\n", serviceName)
	fmt.Printf("Routing traffic with header %s: %s to %s\n", "x-diverge-env", headerValue, endpoint)

	// Atomic Create — handle collision

	if err := c.Create(ctx, pg); err != nil {
		if apierrors.IsAlreadyExists(err) {
			// Fetch existing PG
			var existing divergeiov1alpha1.PreviewGroup
			if getErr := c.Get(ctx, types.NamespacedName{Name: groupName}, &existing); getErr != nil {
				return fmt.Errorf("failed to check existing PreviewGroup: %w", getErr)
			}
			if existing.Spec.Owner != "" && existing.Spec.Owner != username {
				return fmt.Errorf("%w: group %q is owned by %q (you are %q). Delete it first with: diverge preview delete %s",
					ErrCollision, groupName, existing.Spec.Owner, username, groupName)
			}
			// Same owner — update instead
			existing.Spec = pg.Spec
			if updateErr := c.Update(ctx, &existing); updateErr != nil {
				return fmt.Errorf("failed to update PreviewGroup: %w", updateErr)
			}
		} else {
			return fmt.Errorf("failed to create PreviewGroup: %w", err)
		}
	}

	// Start lease heartbeat
	heartbeatTicker := time.NewTicker(20 * time.Second)
	defer heartbeatTicker.Stop()

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-heartbeatTicker.C:
				var current divergeiov1alpha1.PreviewGroup
				if err := c.Get(ctx, types.NamespacedName{Name: groupName}, &current); err == nil {
					now := metav1.Now()
					current.Status.LeaseRenewedAt = &now
					_ = c.Status().Update(ctx, &current)
				}
			}
		}
	}()

	defer func() {
		fmt.Printf("\nCleaning up PreviewGroup %q...\n", groupName)
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := c.Delete(cleanupCtx, pg); err != nil {
			slog.Error("failed to clean up PreviewGroup", "name", groupName, "error", err)
		} else {
			fmt.Println("Goodbye!")
		}
	}()

	// 7. Print status
	_ = runPreviewStatus(app, groupName, cmd.OutOrStdout())

	if len(args) > 0 {
		fmt.Printf("🚀 Starting child process: %v\n", args)
		childCmd, err := runChildProcess(ctx, args, devOpts.resolvedEnvMap)
		if err != nil {
			return fmt.Errorf("failed to start child process: %w", err)
		}
		if childCmd != nil {
			// Wait for the child command to finish or context to be canceled
			if err := childCmd.Wait(); err != nil {
				return fmt.Errorf("child process failed: %w", err)
			}
		}
	} else {
		fmt.Println("Press Ctrl+C to stop dev session...")
		// Wait for Ctrl+C
		<-ctx.Done()
	}

	return nil
}

func newPreviewInterceptCmd(app *App) *cobra.Command {
	var groupName string
	var endpoint string
	cmd := &cobra.Command{
		Use:   "intercept <service>",
		Short: "Intercept a service in a preview group and route it locally",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPreviewIntercept(app, args[0], groupName, endpoint, cmd.Context())
		},
	}
	cmd.Flags().StringVar(&groupName, "group", "", "PreviewGroup name")
	cmd.Flags().StringVar(&endpoint, "endpoint", "", "Local endpoint (e.g. 100.1.2.3:8080)")
	_ = cmd.MarkFlagRequired("group")
	_ = cmd.MarkFlagRequired("endpoint")
	return cmd
}

func runPreviewIntercept(app *App, service, groupName, endpoint string, ctx context.Context) error {
	if service == "" {
		return fmt.Errorf("service name is required")
	}
	c, _, err := app.KubeClient()
	if err != nil {
		return err
	}

	var pg divergeiov1alpha1.PreviewGroup
	if err := c.Get(ctx, types.NamespacedName{Name: groupName}, &pg); err != nil {
		return err
	}

	i := slices.IndexFunc(pg.Spec.Services, func(svc divergeiov1alpha1.PreviewGroupServiceSpec) bool {
		return svc.Name == service
	})

	if i != -1 {
		pg.Spec.Services[i].Mode = divergeiov1alpha1.ServiceModeLocal
		pg.Spec.Services[i].Endpoint = endpoint
	} else {
		pg.Spec.Services = append(pg.Spec.Services, divergeiov1alpha1.PreviewGroupServiceSpec{
			Name:     service,
			Mode:     divergeiov1alpha1.ServiceModeLocal,
			Endpoint: endpoint,
		})
	}

	if err := c.Update(ctx, &pg); err != nil {
		return err
	}

	fmt.Printf("Intercepting %s to %s in group %s\n", service, endpoint, groupName)
	return nil
}

func newPreviewReleaseCmd(app *App) *cobra.Command {
	var groupName string
	cmd := &cobra.Command{
		Use:   "release <service>",
		Short: "Stop intercepting a service and revert to image mode",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPreviewRelease(app, args[0], groupName, cmd.Context())
		},
	}
	cmd.Flags().StringVar(&groupName, "group", "", "PreviewGroup name")
	_ = cmd.MarkFlagRequired("group")
	return cmd
}

func runPreviewRelease(app *App, service, groupName string, ctx context.Context) error {
	c, _, err := app.KubeClient()
	if err != nil {
		return err
	}

	var pg divergeiov1alpha1.PreviewGroup
	if err := c.Get(ctx, types.NamespacedName{Name: groupName}, &pg); err != nil {
		return err
	}

	i := slices.IndexFunc(pg.Spec.Services, func(svc divergeiov1alpha1.PreviewGroupServiceSpec) bool {
		return svc.Name == service
	})

	if i != -1 {
		if pg.Spec.Services[i].Image == "" {
			pg.Spec.Services[i].Mode = divergeiov1alpha1.ServiceModeBaseline
		} else {
			pg.Spec.Services[i].Mode = divergeiov1alpha1.ServiceModeImage
		}
		pg.Spec.Services[i].Endpoint = "" // clear endpoint
	} else {
		return fmt.Errorf("service %q not found in group %q", service, groupName)
	}

	if err := c.Update(ctx, &pg); err != nil {
		return err
	}

	fmt.Printf("Released intercept for %s in group %s\n", service, groupName)
	return nil
}

func runChildProcess(ctx context.Context, args []string, envMap map[string]string) (*exec.Cmd, error) {
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	env := os.Environ()
	for k, v := range envMap {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	cmd.Env = env

	if err := cmd.Start(); err != nil {
		fmt.Printf("⚠️  Failed to start child process: %v\n", err)
		return nil, err
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		select {
		case sig := <-sigCh:
			fmt.Printf("\nReceived signal %v, terminating child process tree...\n", sig)
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)

			// Wait 5 seconds, then SIGKILL if it hasn't exited
			time.AfterFunc(5*time.Second, func() {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			})
		case <-ctx.Done():
			// Context canceled (e.g. from app teardown)
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
			time.AfterFunc(5*time.Second, func() {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			})
		}
	}()

	return cmd, nil
}
