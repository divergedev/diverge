package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/config"
	"github.com/divergedev/diverge/internal/git"
)

func newDevCmd(app *App) *cobra.Command {
	var (
		serviceFlag  string
		portFlag     int32
		endpointFlag string
	)

	cmd := &cobra.Command{
		Use:   "dev",
		Short: "Route cluster traffic for a service to your local machine",
		Long: `Start a local development session by creating a PreviewGroup that routes
traffic for the specified service to your local machine's Tailscale IP.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDev(app, serviceFlag, portFlag, endpointFlag, cmd)
		},
	}
	cmd.Flags().StringVar(&serviceFlag, "service", "", "Service name (default: auto-detect)")
	cmd.Flags().Int32Var(&portFlag, "port", 0, "Local port (default: 8080)")
	cmd.Flags().StringVar(&endpointFlag, "endpoint", "", "Local endpoint IP (default: tailscale ip -4)")

	return cmd
}

func runDev(app *App, serviceFlag string, portFlag int32, endpointFlag string, cmd *cobra.Command) error {
	ctx := cmd.Context()

	// 1. Auto-detect service name
	serviceName := serviceFlag
	if serviceName == "" {
		cfg, err := config.Load(".diverge.yaml")
		if err == nil && len(cfg.Services) > 0 {
			// use the first service
			for k := range cfg.Services {
				serviceName = k
				break
			}
		} else {
			// fallback to directory name
			cwd, _ := os.Getwd()
			serviceName = filepath.Base(cwd)
		}
	}

	// 2. Auto-detect endpoint
	endpointIP := endpointFlag
	if endpointIP == "" {
		out, err := exec.Command("tailscale", "ip", "-4").Output()
		if err != nil {
			return fmt.Errorf("failed to detect tailscale IP: %w. Make sure tailscale is running or pass --endpoint", err)
		}
		endpointIP = strings.TrimSpace(string(out))
	}

	// 3. Auto-detect port
	port := portFlag
	if port == 0 {
		port = 8080
	}
	endpoint := fmt.Sprintf("%s:%d", endpointIP, port)

	// 4. Construct header value
	gitCtx, err := git.Detect()
	var headerValue string
	if err == nil && gitCtx != nil {
		headerValue = gitCtx.Branch
	}
	if headerValue == "" {
		headerValue = "local-dev"
	}

	// 5. Construct group name
	u, _ := user.Current()
	username := "dev"
	if u != nil {
		username = strings.ToLower(u.Username)
	}
	groupName := fmt.Sprintf("dev-%s-%s", username, serviceName)

	// Normalize group name
	groupName = strings.ReplaceAll(groupName, "_", "-")

	// 6. Create PreviewGroup CR
	pg := &divergeiov1alpha1.PreviewGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name: groupName,
		},
		Spec: divergeiov1alpha1.PreviewGroupSpec{
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
	synced, syncErr := syncBaselineEnv(ctx, clientset, syncEnvOptions{
		Namespace:   ns,
		ServiceName: serviceName,
	})
	if syncErr != nil {
		fmt.Printf("⚠️  Could not sync env vars: %v\n", syncErr)
	} else if synced > 0 {
		fmt.Printf("📋 Synced %d env vars from baseline → .env.diverge\n", synced)
	}

	fmt.Printf("Starting dev session for service %q...\n", serviceName)
	fmt.Printf("Routing traffic with header %s: %s to %s\n", "x-diverge-env", headerValue, endpoint)

	if err := c.Create(ctx, pg); err != nil {
		return fmt.Errorf("failed to create PreviewGroup: %w", err)
	}

	defer func() {
		fmt.Printf("\nCleaning up PreviewGroup %q...\n", groupName)
		_ = c.Delete(context.Background(), pg)
		fmt.Println("Goodbye!")
	}()

	// 7. Print status
	_ = runPreviewStatus(app, groupName, cmd.OutOrStdout())

	fmt.Println("Press Ctrl+C to stop dev session...")

	// 8. Wait for Ctrl+C
	<-ctx.Done()

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
			return runPreviewIntercept(app, args[0], groupName, endpoint)
		},
	}
	cmd.Flags().StringVar(&groupName, "group", "", "PreviewGroup name")
	cmd.Flags().StringVar(&endpoint, "endpoint", "", "Local endpoint (e.g. 100.1.2.3:8080)")
	_ = cmd.MarkFlagRequired("group")
	_ = cmd.MarkFlagRequired("endpoint")
	return cmd
}

func runPreviewIntercept(app *App, service, groupName, endpoint string) error {
	c, _, err := app.KubeClient()
	if err != nil {
		return err
	}

	var pg divergeiov1alpha1.PreviewGroup
	if err := c.Get(context.TODO(), types.NamespacedName{Name: groupName}, &pg); err != nil {
		return err
	}

	found := false
	for i, svc := range pg.Spec.Services {
		if svc.Name == service {
			pg.Spec.Services[i].Mode = divergeiov1alpha1.ServiceModeLocal
			pg.Spec.Services[i].Endpoint = endpoint
			found = true
			break
		}
	}

	if !found {
		pg.Spec.Services = append(pg.Spec.Services, divergeiov1alpha1.PreviewGroupServiceSpec{
			Name:     service,
			Mode:     divergeiov1alpha1.ServiceModeLocal,
			Endpoint: endpoint,
		})
	}

	if err := c.Update(context.TODO(), &pg); err != nil {
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
			return runPreviewRelease(app, args[0], groupName)
		},
	}
	cmd.Flags().StringVar(&groupName, "group", "", "PreviewGroup name")
	_ = cmd.MarkFlagRequired("group")
	return cmd
}

func runPreviewRelease(app *App, service, groupName string) error {
	c, _, err := app.KubeClient()
	if err != nil {
		return err
	}

	var pg divergeiov1alpha1.PreviewGroup
	if err := c.Get(context.TODO(), types.NamespacedName{Name: groupName}, &pg); err != nil {
		return err
	}

	found := false
	for i, svc := range pg.Spec.Services {
		if svc.Name == service {
			pg.Spec.Services[i].Mode = divergeiov1alpha1.ServiceModeImage
			pg.Spec.Services[i].Endpoint = "" // clear endpoint
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("service %q not found in group %q", service, groupName)
	}

	if err := c.Update(context.TODO(), &pg); err != nil {
		return err
	}

	fmt.Printf("Released intercept for %s in group %s\n", service, groupName)
	return nil
}
