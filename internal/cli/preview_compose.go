package cli

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/yaml"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/config"
	"github.com/divergedev/diverge/internal/git"
	"github.com/divergedev/diverge/internal/previewgroup"
)

func formatMemberOrigin(m previewgroup.MemberRecord) string {
	var parts []string
	if m.Project != "" && m.MR > 0 {
		parts = append(parts, fmt.Sprintf("%s#%d", m.Project, m.MR))
	} else if m.Project != "" {
		parts = append(parts, m.Project)
	} else if m.MR > 0 {
		parts = append(parts, fmt.Sprintf("MR #%d", m.MR))
	}
	if m.Branch != "" {
		parts = append(parts, m.Branch)
	}
	if m.EnvironmentName != "" {
		if m.EnvironmentNamespace != "" {
			parts = append(parts, fmt.Sprintf("env:%s/%s", m.EnvironmentNamespace, m.EnvironmentName))
		} else {
			parts = append(parts, fmt.Sprintf("env:%s", m.EnvironmentName))
		}
	}
	if len(parts) == 0 {
		if m.Namespace != "" {
			return m.Namespace
		}
		return "manual"
	}
	return strings.Join(parts, " · ")
}

func validateSharedDBMode(mode string) error {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "shared", "schema", "snapshot", "fresh", "cnpg", "neon":
		return nil
	default:
		return fmt.Errorf("invalid --db-mode %q (expected shared, schema, snapshot, fresh, cnpg, or neon)", mode)
	}
}

func newPreviewJoinCmd(app *App) *cobra.Command {
	var (
		configPath    string
		services      []string
		fromEnvs      []string
		headerKey     string
		headerValue   string
		baseURL       string
		ttl           string
		mrNumber      int
		project       string
		branch        string
		provider      string
		dbMode        string
		dbSeedSource  string
		clusterName   string
		clusterSecret string
		forceTakeover bool
		pruneMR       bool
		dryRun        bool
	)

	cmd := &cobra.Command{
		Use:     "join <group-name>",
		Aliases: []string{"attach"},
		Short:   "Join one or more services or open MR environments into a shared PreviewGroup",
		// editorconfig-checker-disable
		Long: `Create or join a shared multi-MR PreviewGroup without overwriting services
registered by other repositories or pull requests.

By default, each joined environment keeps its own per-service database settings.
Pass --db-mode (e.g. neon, schema, cnpg) to provision one shared group-level
database across all services in the PreviewGroup.

Examples:
  # Join from two existing PR/MR Environment CRs
  diverge preview join feat-checkout --from-env frontend/pr-acme-web-app-101 --from-env backend/pr-acme-payments-api-42

  # Join from a repo CI pipeline using the branch name as the shared group
  diverge preview join feat/checkout \
    --service payments-api=ghcr.io/acme/payments-api:sha-abc123 \
    --project acme/payments-api \
    --mr 42`,
		// editorconfig-checker-enable
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			groupName, err := previewgroup.NormalizeGroupName(args[0])
			if err != nil {
				return err
			}
			if len(services) == 0 && len(fromEnvs) == 0 {
				return fmt.Errorf("specify at least one --service or --from-env to join PreviewGroup %q", groupName)
			}
			if err := validateSharedDBMode(dbMode); err != nil {
				return err
			}

			var ttlDur *time.Duration
			if strings.TrimSpace(ttl) != "" {
				d, parseErr := time.ParseDuration(strings.TrimSpace(ttl))
				if parseErr != nil || d <= 0 {
					return fmt.Errorf("invalid --ttl %q: must be a positive duration (e.g. 8h, 24h)", ttl)
				}
				ttlDur = &d
			}

			// Load optional .diverge.yaml defaults (cluster_ref, asyncRoutes, per-service database, routing header)
			var cfg *config.Config
			var resolvedCfg config.EnvironmentSettings
			if strings.TrimSpace(configPath) == "" {
				configPath = ".diverge.yaml"
			}
			loadedCfg, cfgErr := config.Load(configPath)
			if cfgErr != nil {
				if cmd.Flags().Changed("config") || !errors.Is(cfgErr, os.ErrNotExist) {
					return fmt.Errorf("failed to load config %s: %w", configPath, cfgErr)
				}
			} else if loadedCfg != nil {
				if vErr := loadedCfg.Validate(); vErr != nil {
					return fmt.Errorf("invalid config %s: %w", configPath, vErr)
				}
				res := loadedCfg.Resolve("preview", nil)
				if rErr := res.Validate(); rErr != nil {
					return fmt.Errorf("invalid resolved preview config %s: %w", configPath, rErr)
				}
				cfg = loadedCfg
				resolvedCfg = res.EnvironmentSettings
			}

			var clusterRef *divergeiov1alpha1.ClusterTargetRef
			clusterExplicit := cmd.Flags().Changed("cluster") || cmd.Flags().Changed("cluster-secret")
			if clusterExplicit {
				parsedCluster, cErr := config.ParseClusterFlags(clusterName, clusterSecret)
				if cErr != nil {
					return fmt.Errorf("invalid cluster target flags: %w", cErr)
				}
				clusterRef = parsedCluster
			} else if resolvedCfg.Deploy.ClusterRef != nil {
				clusterRef = resolvedCfg.Deploy.ClusterRef.ToClusterTargetRef()
			}

			if !cmd.Flags().Changed("header-key") && strings.TrimSpace(resolvedCfg.Routing.HeaderKey) != "" {
				headerKey = strings.TrimSpace(resolvedCfg.Routing.HeaderKey)
			}

			ns := strings.TrimSpace(app.Namespace)
			if ns == "" {
				ns = "default"
			}

			src := divergeiov1alpha1.EnvironmentSource{
				Provider: "local",
				Branch:   groupName,
			}
			if gitCtx, gErr := git.Detect(); gErr == nil && gitCtx != nil {
				if gitCtx.Provider != "" {
					src.Provider = gitCtx.Provider
				}
				if gitCtx.Project != "" {
					src.Project = gitCtx.Project
				}
				if gitCtx.Branch != "" {
					src.Branch = gitCtx.Branch
				}
			}
			if strings.TrimSpace(provider) != "" {
				src.Provider = strings.TrimSpace(provider)
			}
			if strings.TrimSpace(project) != "" {
				src.Project = strings.TrimSpace(project)
			}
			if strings.TrimSpace(branch) != "" {
				src.Branch = strings.TrimSpace(branch)
			}
			if mrNumber > 0 {
				src.MR = mrNumber
			}

			var svcSpecs []divergeiov1alpha1.PreviewGroupServiceSpec
			var members []previewgroup.MemberRecord

			if len(services) > 0 {
				// Use defaultPort=0 so Kubernetes Service port auto-discovery works unless :port is explicitly given.
				parsed, pErr := parseServiceSpecsWithDefaultPort(services, 0)
				if pErr != nil {
					return pErr
				}
				for i := range parsed {
					if parsed[i].Namespace == "" {
						parsed[i].Namespace = ns
					}
					if cfg != nil {
						if svcCfg, ok := cfg.Services[parsed[i].Name]; ok && len(parsed[i].AsyncRoutes) == 0 && len(svcCfg.AsyncRoutes) > 0 {
							parsed[i].AsyncRoutes = slices.Clone(svcCfg.AsyncRoutes)
						}
						if parsed[i].Mode != divergeiov1alpha1.ServiceModeBaseline &&
							strings.TrimSpace(dbMode) == "" &&
							parsed[i].Database == nil &&
							strings.TrimSpace(resolvedCfg.Database.Mode) != "" {
							parsed[i].Database = &divergeiov1alpha1.EnvironmentDatabase{
								Mode:       strings.TrimSpace(resolvedCfg.Database.Mode),
								SeedSource: strings.TrimSpace(resolvedCfg.Database.SeedSource),
							}
						}
					}
					svcSpecs = append(svcSpecs, parsed[i])
					if parsed[i].Mode != divergeiov1alpha1.ServiceModeBaseline {
						members = append(members, previewgroup.MemberRecord{
							Service:   parsed[i].Name,
							Namespace: parsed[i].Namespace,
							Provider:  src.Provider,
							Project:   src.Project,
							MR:        src.MR,
							Branch:    src.Branch,
						})
					}
				}
			}

			c, _, err := app.KubeClient()
			if err != nil && (!dryRun || len(fromEnvs) > 0) {
				return fmt.Errorf("failed to create Kubernetes client: %w", err)
			}

			for _, envRef := range fromEnvs {
				envNS, envName := previewgroup.SplitEnvironmentRef(envRef)
				if envNS == "" {
					envNS = ns
				}
				if envName == "" {
					continue
				}
				var env divergeiov1alpha1.Environment
				if getErr := c.Get(cmd.Context(), types.NamespacedName{Name: envName, Namespace: envNS}, &env); getErr != nil {
					return fmt.Errorf("failed to get Environment %s/%s: %w", envNS, envName, getErr)
				}
				spec, member, convErr := previewgroup.ServiceFromEnvironment(&env)
				if convErr != nil {
					return convErr
				}
				svcSpecs = append(svcSpecs, spec)
				members = append(members, member)
				if !cmd.Flags().Changed("header-key") && env.Spec.Routing.HeaderKey != "" && headerKey == previewgroup.DefaultRoutingHeaderKey {
					headerKey = env.Spec.Routing.HeaderKey
				}
			}

			if len(svcSpecs) == 0 {
				return fmt.Errorf("no valid services resolved to join PreviewGroup %q", groupName)
			}

			effHeaderVal := strings.TrimSpace(headerValue)
			if effHeaderVal == "" {
				effHeaderVal = groupName
			}

			var extURL string
			if trimmedBase := strings.TrimSpace(baseURL); trimmedBase != "" {
				extURL = strings.TrimRight(trimmedBase, "/") + "/?_diverge_env=" + effHeaderVal
			}

			var groupDB *divergeiov1alpha1.EnvironmentDatabase
			if strings.TrimSpace(dbMode) != "" {
				groupDB = &divergeiov1alpha1.EnvironmentDatabase{
					Mode:       strings.ToLower(strings.TrimSpace(dbMode)),
					SeedSource: strings.TrimSpace(dbSeedSource),
				}
				for i := range svcSpecs {
					svcSpecs[i].Database = nil
				}
			}

			if dryRun {
				previewObj := &divergeiov1alpha1.PreviewGroup{}
				previewObj.APIVersion = divergeiov1alpha1.GroupVersion.String()
				previewObj.Kind = "PreviewGroup"
				previewObj.Name = groupName
				previewObj.Spec = divergeiov1alpha1.PreviewGroupSpec{
					Source: src,
					Routing: divergeiov1alpha1.PreviewGroupRouting{
						Mode:        "header",
						HeaderKey:   headerKey,
						HeaderValue: effHeaderVal,
						ExternalURL: extURL,
					},
					Services:   svcSpecs,
					Database:   groupDB,
					ClusterRef: clusterRef,
				}
				if ttlDur != nil {
					previewObj.Spec.Lifecycle = &divergeiov1alpha1.PreviewGroupLifecycle{
						TTL:            &metav1.Duration{Duration: *ttlDur},
						CleanupOnMerge: true,
					}
				}
				_ = previewgroup.SetMembers(previewObj, members)
				data, mErr := yaml.Marshal(previewObj)
				if mErr != nil {
					return fmt.Errorf("failed to marshal PreviewGroup: %w", mErr)
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "---\n# dry-run: would join %d service(s) into PreviewGroup %q\n%s", len(svcSpecs), groupName, string(data))
				return nil
			}

			res, err := previewgroup.Join(cmd.Context(), c, previewgroup.JoinOptions{
				GroupName:           groupName,
				Namespace:           ns,
				Services:            svcSpecs,
				Members:             members,
				Source:              &src,
				HeaderKey:           headerKey,
				HeaderKeyExplicit:   cmd.Flags().Changed("header-key"),
				HeaderValue:         effHeaderVal,
				HeaderValueExplicit: cmd.Flags().Changed("header-value"),
				ExternalURL:         extURL,
				TTL:                 ttlDur,
				TTLExplicit:         cmd.Flags().Changed("ttl"),
				GroupDatabase:       groupDB,
				ClusterRef:          clusterRef,
				ClusterRefExplicit:  clusterExplicit,
				AllowTakeover:       forceTakeover,
				PruneSameMR:         pruneMR,
			})
			if err != nil {
				return fmt.Errorf("failed to join PreviewGroup %q: %w", groupName, err)
			}

			out := cmd.OutOrStdout()
			if res.DeletedGroup {
				_, _ = fmt.Fprintf(out, "🗑️  Pruned reverted services from PreviewGroup %q; no active preview services remain, so the group was deleted.\n", groupName)
				return nil
			}

			action := "Joined"
			if res.Created {
				action = "Created"
			}
			_, _ = fmt.Fprintf(out, "✅ %s PreviewGroup %q (%d total services)\n", action, res.PreviewGroup.Name, len(res.PreviewGroup.Spec.Services))
			_, _ = fmt.Fprintf(out, "   Header: %s: %s\n", res.PreviewGroup.Spec.Routing.HeaderKey, res.PreviewGroup.Spec.Routing.HeaderValue)
			if res.PreviewGroup.Spec.Routing.ExternalURL != "" {
				_, _ = fmt.Fprintf(out, "   URL:    %s\n", res.PreviewGroup.Spec.Routing.ExternalURL)
			}
			_, _ = fmt.Fprintf(out, "   Run 'diverge preview status %s' to inspect all joined services.\n", res.PreviewGroup.Name)
			return nil
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", ".diverge.yaml", "path to .diverge.yaml config file")
	cmd.Flags().StringSliceVar(&services, "service", nil, "service spec: name=image[:port] or name (baseline)")
	cmd.Flags().StringSliceVar(&fromEnvs, "from-env", nil, "join from existing Environment CR(s) (<name> or <namespace>/<name>)")
	cmd.Flags().StringVar(&headerKey, "header-key", previewgroup.DefaultRoutingHeaderKey, "routing header key")
	cmd.Flags().StringVar(&headerValue, "header-value", "", "routing header value (default: normalized group name)")
	cmd.Flags().StringVar(&baseURL, "base-url", "", "optional base URL to populate ?_diverge_env= preview URL")
	cmd.Flags().StringVar(&ttl, "ttl", "", "group TTL duration (e.g. 8h, 24h)")
	cmd.Flags().IntVar(&mrNumber, "mr", 0, "PR/MR number for membership tracking")
	cmd.Flags().StringVar(&project, "project", "", "repository path (e.g. acme/payments-api)")
	cmd.Flags().StringVar(&branch, "branch", "", "source branch override")
	cmd.Flags().StringVar(&provider, "provider", "", "SCM provider override (github, gitlab, local)")
	cmd.Flags().StringVar(&dbMode, "db-mode", "", "optional shared group-level database mode (shared, schema, snapshot, fresh, cnpg, neon)")
	cmd.Flags().StringVar(&dbSeedSource, "db-seed-source", "", "optional seed source when --db-mode is set")
	cmd.Flags().StringVar(&clusterName, "cluster", "", "target spoke cluster name (or 'local' to force hub)")
	cmd.Flags().StringVar(&clusterSecret, "cluster-secret", "", "same-namespace Secret (<name> or <name>/<key>) holding spoke kubeconfig")
	cmd.Flags().BoolVar(&forceTakeover, "force", false, "allow replacing a service currently owned by another MR or environment")
	cmd.Flags().BoolVar(&pruneMR, "prune-mr", false, "remove prior services contributed by the same --project and --mr that are no longer in --service")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print PreviewGroup YAML without applying")

	return cmd
}

func newPreviewLeaveCmd(app *App) *cobra.Command {
	var (
		services []string
		fromEnvs []string
		mrNumber int
		project  string
	)

	cmd := &cobra.Command{
		Use:     "leave <group-name>",
		Aliases: []string{"detach"},
		Short:   "Detach services or an MR from a shared PreviewGroup (auto-deletes group when empty)",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			groupName, err := previewgroup.NormalizeGroupName(args[0])
			if err != nil {
				return err
			}
			if len(services) == 0 && len(fromEnvs) == 0 && mrNumber <= 0 {
				return fmt.Errorf("specify --service, --from-env, or --mr to detach from PreviewGroup %q", groupName)
			}

			effProject := strings.TrimSpace(project)
			if effProject == "" && mrNumber > 0 {
				if gitCtx, gErr := git.Detect(); gErr == nil && gitCtx != nil {
					effProject = gitCtx.Project
				}
			}

			c, _, err := app.KubeClient()
			if err != nil {
				return fmt.Errorf("failed to create Kubernetes client: %w", err)
			}

			ns := strings.TrimSpace(app.Namespace)
			if ns == "" {
				ns = "default"
			}

			out := cmd.OutOrStdout()
			if len(fromEnvs) > 0 {
				for _, envRef := range fromEnvs {
					envNS, envName := previewgroup.SplitEnvironmentRef(envRef)
					if envName == "" {
						continue
					}
					if envNS == "" {
						envNS = ns
					}
					res, lErr := previewgroup.Leave(cmd.Context(), c, previewgroup.LeaveOptions{
						GroupName:            groupName,
						Namespace:            ns,
						EnvironmentName:      envName,
						EnvironmentNamespace: envNS,
					})
					if lErr != nil {
						return fmt.Errorf("failed to detach %q from PreviewGroup %q: %w", envRef, groupName, lErr)
					}
					printLeaveResult(out, groupName, res)
					if res.DeletedGroup || res.NotFound {
						return nil
					}
				}
				if len(services) == 0 && mrNumber <= 0 {
					return nil
				}
			}

			res, err := previewgroup.Leave(cmd.Context(), c, previewgroup.LeaveOptions{
				GroupName:    groupName,
				Namespace:    app.Namespace,
				ServiceNames: services,
				Project:      effProject,
				MR:           mrNumber,
			})
			if err != nil {
				return fmt.Errorf("failed to detach from PreviewGroup %q: %w", groupName, err)
			}
			printLeaveResult(out, groupName, res)
			return nil
		},
	}

	cmd.Flags().StringSliceVar(&services, "service", nil, "service name(s) to detach from the preview group")
	cmd.Flags().StringSliceVar(&fromEnvs, "from-env", nil, "Environment CR name(s) (<name> or <namespace>/<name>) whose service should be detached")
	cmd.Flags().IntVar(&mrNumber, "mr", 0, "PR/MR number whose contributed service(s) should be detached")
	cmd.Flags().StringVar(&project, "project", "", "repository path when detaching by --mr (defaults to current git remote)")

	return cmd
}

func printLeaveResult(out interface{ Write([]byte) (int, error) }, groupName string, res *previewgroup.LeaveResult) {
	if res == nil || res.NotFound {
		_, _ = fmt.Fprintf(out, "ℹ️  PreviewGroup %q not found (already cleaned up).\n", groupName)
		return
	}
	if res.DeletedGroup {
		_, _ = fmt.Fprintf(out, "🗑️  Detached %v from PreviewGroup %q; no active preview services remain, so the group was deleted.\n", res.RemovedServices, res.GroupName)
		return
	}
	if len(res.RemovedServices) == 0 {
		_, _ = fmt.Fprintf(out, "ℹ️  No matching services were present in PreviewGroup %q.\n", res.GroupName)
		return
	}
	_, _ = fmt.Fprintf(out, "✅ Detached %v from PreviewGroup %q (remaining active: %v).\n", res.RemovedServices, res.GroupName, res.RemainingServices)
}
