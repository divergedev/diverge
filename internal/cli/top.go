package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
)

const (
	topAnnotationHibernated = "divergedev.com/hibernated"
	topDefaultCPUCores      = 0.25
	topDefaultMemoryGiB     = 0.5
	topCPUHourlyRateUSD     = 0.035
	topMemoryGiBHourlyUSD   = 0.004
	topDBSchemaHourlyUSD    = 0.015
	hoursPerMonth           = 730.0
)

// TopSummary aggregates fleet-wide counts and FinOps cost metrics.
type TopSummary struct {
	TotalEnvironments      int     `json:"totalEnvironments" yaml:"totalEnvironments"`
	ActiveEnvironments     int     `json:"activeEnvironments" yaml:"activeEnvironments"`
	HibernatedEnvironments int     `json:"hibernatedEnvironments" yaml:"hibernatedEnvironments"`
	PreviewGroups          int     `json:"previewGroups" yaml:"previewGroups"`
	AgentTasks             int     `json:"agentTasks" yaml:"agentTasks"`
	ActiveAgentTasks       int     `json:"activeAgentTasks" yaml:"activeAgentTasks"`
	HourlyBurnUSD          float64 `json:"hourlyBurnUSD" yaml:"hourlyBurnUSD"`
	ProjectedMonthlyUSD    float64 `json:"projectedMonthlyUSD" yaml:"projectedMonthlyUSD"`
	AgentLLMSpendUSD       float64 `json:"agentLLMSpendUSD" yaml:"agentLLMSpendUSD"`
}

// TopEnvironmentRow represents an Environment entry in a top snapshot.
type TopEnvironmentRow struct {
	Name          string  `json:"name" yaml:"name"`
	Namespace     string  `json:"namespace" yaml:"namespace"`
	Phase         string  `json:"phase" yaml:"phase"`
	TTL           string  `json:"ttl" yaml:"ttl"`
	State         string  `json:"state" yaml:"state"`
	Hibernated    bool    `json:"hibernated" yaml:"hibernated"`
	Drifted       bool    `json:"drifted,omitempty" yaml:"drifted,omitempty"`
	HourlyCostUSD float64 `json:"hourlyCostUSD" yaml:"hourlyCostUSD"`
	URL           string  `json:"url" yaml:"url"`
}

// TopPreviewGroupRow represents a PreviewGroup entry in a top snapshot.
type TopPreviewGroupRow struct {
	Name      string `json:"name" yaml:"name"`
	Namespace string `json:"namespace" yaml:"namespace"`
	Phase     string `json:"phase" yaml:"phase"`
	Services  int    `json:"services" yaml:"services"`
	Header    string `json:"header" yaml:"header"`
	Age       string `json:"age" yaml:"age"`
}

// TopAgentTaskRow represents an AgentTask entry in a top snapshot.
type TopAgentTaskRow struct {
	Name           string `json:"name" yaml:"name"`
	Namespace      string `json:"namespace" yaml:"namespace"`
	Phase          string `json:"phase" yaml:"phase"`
	Iteration      string `json:"iteration" yaml:"iteration"`
	TokensConsumed int64  `json:"tokensConsumed" yaml:"tokensConsumed"`
	CostUSD        string `json:"costUSD" yaml:"costUSD"`
	BudgetUSD      string `json:"budgetUSD" yaml:"budgetUSD"`
	Branch         string `json:"branch" yaml:"branch"`
}

// TopSnapshot holds a point-in-time view of Diverge resources and FinOps burn.
type TopSnapshot struct {
	Timestamp     time.Time            `json:"timestamp" yaml:"timestamp"`
	Summary       TopSummary           `json:"summary" yaml:"summary"`
	Environments  []TopEnvironmentRow  `json:"environments" yaml:"environments"`
	PreviewGroups []TopPreviewGroupRow `json:"previewGroups" yaml:"previewGroups"`
	AgentTasks    []TopAgentTaskRow    `json:"agentTasks" yaml:"agentTasks"`
}

func newTopCmd(app *App) *cobra.Command {
	var (
		allNamespaces bool
		interval      time.Duration
		once          bool
		outputFormat  string
	)

	cmd := &cobra.Command{
		Use:     "top",
		Aliases: []string{"watch"},
		Short:   "Live terminal dashboard of environments, preview groups, agent tasks, and FinOps cost",
		Long: `Display a live-updating (or single-snapshot) terminal dashboard showing
active and hibernated preview environments, preview groups, autonomous agent tasks,
and real-time FinOps fleet burn estimates.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if interval < 200*time.Millisecond {
				return fmt.Errorf("--interval must be at least 200ms, got %s", interval)
			}

			switch outputFormat {
			case "table", "", "json", "yaml":
			default:
				return fmt.Errorf("unsupported output format: %s (supported: table, json, yaml)", outputFormat)
			}

			c, _, err := app.KubeClient()
			if err != nil {
				return err
			}

			namespace := app.Namespace
			if allNamespaces {
				namespace = ""
			}

			if outputFormat == "json" || outputFormat == "yaml" || once {
				snap, err := collectTopSnapshot(cmd.Context(), c, namespace)
				if err != nil {
					return err
				}
				return renderTopSnapshot(cmd.OutOrStdout(), snap, outputFormat, app.NoColor)
			}

			ticker := time.NewTicker(interval)
			defer ticker.Stop()

			for {
				snap, err := collectTopSnapshot(cmd.Context(), c, namespace)
				if err != nil {
					return err
				}
				if !app.NoColor {
					_, _ = fmt.Fprint(cmd.OutOrStdout(), "\033[H\033[2J")
				}
				if err := renderTopSnapshot(cmd.OutOrStdout(), snap, "table", app.NoColor); err != nil {
					return err
				}

				select {
				case <-cmd.Context().Done():
					return nil
				case <-ticker.C:
				}
			}
		},
	}

	cmd.Flags().BoolVarP(&allNamespaces, "all-namespaces", "A", false, "Show resources across all namespaces")
	cmd.Flags().DurationVarP(&interval, "interval", "i", 2*time.Second, "Refresh interval (minimum 200ms)")
	cmd.Flags().BoolVar(&once, "once", false, "Render a single snapshot and exit immediately")
	cmd.Flags().StringVarP(&outputFormat, "output", "o", "table", "Output format: table, json, yaml")

	return cmd
}

func estimateEnvHourlyCost(env *divergeiov1alpha1.Environment) float64 {
	if env == nil {
		return 0
	}
	if env.Annotations[topAnnotationHibernated] == "true" {
		return 0
	}
	switch string(env.Status.Phase) {
	case "Expired", string(divergeiov1alpha1.PhaseTerminating), string(divergeiov1alpha1.PhaseFailed):
		return 0
	}

	serviceCount := len(env.Spec.Deploy.ChangedServices)
	if len(env.Status.Services) > serviceCount {
		serviceCount = len(env.Status.Services)
	}
	if serviceCount < 1 {
		serviceCount = 1
	}

	cpuCores := topDefaultCPUCores
	memoryGiB := topDefaultMemoryGiB
	if env.Spec.ServiceConfig != nil && env.Spec.ServiceConfig.Resources != nil {
		if env.Spec.ServiceConfig.Resources.CPURequest != "" {
			if q, err := resource.ParseQuantity(env.Spec.ServiceConfig.Resources.CPURequest); err == nil {
				if cores := q.AsApproximateFloat64(); cores > 0 {
					cpuCores = cores
				}
			}
		}
		if env.Spec.ServiceConfig.Resources.MemoryRequest != "" {
			if q, err := resource.ParseQuantity(env.Spec.ServiceConfig.Resources.MemoryRequest); err == nil {
				if gib := float64(q.Value()) / (1024 * 1024 * 1024); gib > 0 {
					memoryGiB = gib
				}
			}
		}
	}

	perServiceHourly := cpuCores*topCPUHourlyRateUSD + memoryGiB*topMemoryGiBHourlyUSD
	hourlyUSD := float64(serviceCount) * perServiceHourly
	if env.Spec.Database.Mode != "" && env.Spec.Database.Mode != "none" {
		hourlyUSD += topDBSchemaHourlyUSD
	}
	return hourlyUSD
}

func previewGroupMatchesNamespace(pg *divergeiov1alpha1.PreviewGroup, namespace string) bool {
	if pg == nil || namespace == "" {
		return true
	}
	if pg.Namespace == namespace {
		return true
	}
	for _, svc := range pg.Spec.Services {
		svcNS := strings.TrimSpace(svc.Namespace)
		if svcNS == "" {
			svcNS = "default"
		}
		if svcNS == namespace {
			return true
		}
	}
	return false
}

func collectTopSnapshot(ctx context.Context, c client.Client, namespace string) (*TopSnapshot, error) {
	listOpts := []client.ListOption{}
	if namespace != "" {
		listOpts = append(listOpts, client.InNamespace(namespace))
	}

	var envList divergeiov1alpha1.EnvironmentList
	if err := c.List(ctx, &envList, listOpts...); err != nil {
		return nil, fmt.Errorf("failed to list environments: %w", err)
	}

	var pgList divergeiov1alpha1.PreviewGroupList
	if err := c.List(ctx, &pgList, listOpts...); err != nil {
		return nil, fmt.Errorf("failed to list preview groups: %w", err)
	}
	if namespace != "" {
		filtered := make([]divergeiov1alpha1.PreviewGroup, 0, len(pgList.Items))
		seenPG := make(map[string]bool, len(pgList.Items))
		for _, pg := range pgList.Items {
			if previewGroupMatchesNamespace(&pg, namespace) {
				filtered = append(filtered, pg)
				seenPG[pg.Namespace+"/"+pg.Name] = true
			}
		}
		var clusterPGList divergeiov1alpha1.PreviewGroupList
		if err := c.List(ctx, &clusterPGList); err != nil {
			return nil, fmt.Errorf("failed to list cluster preview groups: %w", err)
		}
		for _, pg := range clusterPGList.Items {
			key := pg.Namespace + "/" + pg.Name
			if !seenPG[key] && previewGroupMatchesNamespace(&pg, namespace) {
				filtered = append(filtered, pg)
				seenPG[key] = true
			}
		}
		pgList.Items = filtered
	}

	var taskList divergeiov1alpha1.AgentTaskList
	if err := c.List(ctx, &taskList, listOpts...); err != nil {
		if !meta.IsNoMatchError(err) {
			return nil, fmt.Errorf("failed to list agent tasks: %w", err)
		}
	}

	snap := &TopSnapshot{
		Timestamp:     time.Now().UTC(),
		Environments:  make([]TopEnvironmentRow, 0, len(envList.Items)),
		PreviewGroups: make([]TopPreviewGroupRow, 0, len(pgList.Items)),
		AgentTasks:    make([]TopAgentTaskRow, 0, len(taskList.Items)),
	}

	var totalHourlyUSD float64
	var activeEnvs, hibernatedEnvs int

	for i := range envList.Items {
		env := &envList.Items[i]
		hibernated := env.Annotations[topAnnotationHibernated] == "true"
		drifted := meta.IsStatusConditionTrue(env.Status.Conditions, "BaselineDriftDetected")
		state := "active"
		if hibernated {
			state = "💤 hibernated"
			hibernatedEnvs++
		} else {
			activeEnvs++
			if drifted {
				state = "⚠️ drift"
			}
		}

		hourlyCost := estimateEnvHourlyCost(env)
		totalHourlyUSD += hourlyCost

		phase := string(env.Status.Phase)
		if phase == "" {
			phase = "Unknown"
		}

		ttl := "-"
		if env.Status.ExpiresAt != nil {
			ttl = formatTTL(time.Until(env.Status.ExpiresAt.Time))
		} else if env.Spec.Lifecycle.TTL != nil && env.Spec.Lifecycle.TTL.Duration > 0 && !env.CreationTimestamp.IsZero() {
			ttl = formatTTL(time.Until(env.CreationTimestamp.Add(env.Spec.Lifecycle.TTL.Duration)))
		}

		url := env.Status.URL
		if url == "" {
			url = "-"
		}

		snap.Environments = append(snap.Environments, TopEnvironmentRow{
			Name:          env.Name,
			Namespace:     env.Namespace,
			Phase:         phase,
			TTL:           ttl,
			State:         state,
			Hibernated:    hibernated,
			Drifted:       drifted,
			HourlyCostUSD: hourlyCost,
			URL:           url,
		})
	}

	for i := range pgList.Items {
		pg := &pgList.Items[i]
		phase := string(pg.Status.Phase)
		if phase == "" {
			phase = "Unknown"
		}
		ns := pg.Namespace
		if ns == "" {
			ns = "-"
		}
		svcCount := len(pg.Spec.Services)
		if int(pg.Status.ServiceCount) > svcCount {
			svcCount = int(pg.Status.ServiceCount)
		}
		headerKey := pg.Spec.Routing.HeaderKey
		if headerKey == "" {
			headerKey = "x-preview-env"
		}
		header := pg.Spec.Routing.HeaderValue
		if header != "" {
			header = fmt.Sprintf("%s=%s", headerKey, header)
		} else {
			header = "-"
		}

		snap.PreviewGroups = append(snap.PreviewGroups, TopPreviewGroupRow{
			Name:      pg.Name,
			Namespace: ns,
			Phase:     phase,
			Services:  svcCount,
			Header:    header,
			Age:       formatAge(pg.CreationTimestamp.Time),
		})
	}

	var activeTasks int
	var llmSpendUSD float64
	for i := range taskList.Items {
		task := &taskList.Items[i]
		phase := string(task.Status.Phase)
		if phase == "" {
			phase = string(divergeiov1alpha1.AgentTaskPhasePending)
		}
		if task.Status.Phase == divergeiov1alpha1.AgentTaskPhaseActive || task.Status.Phase == divergeiov1alpha1.AgentTaskPhaseProvisioning {
			activeTasks++
		}
		if rawCost := strings.TrimPrefix(strings.TrimSpace(task.Status.CostUSD), "$"); rawCost != "" {
			if parsed, err := strconv.ParseFloat(rawCost, 64); err == nil && parsed > 0 {
				llmSpendUSD += parsed
			}
		}

		maxIter := task.Spec.MaxIterations
		if maxIter <= 0 {
			maxIter = 5
		}
		iter := fmt.Sprintf("%d/%d", task.Status.Iteration, maxIter)

		costUSD := task.Status.CostUSD
		if costUSD == "" {
			costUSD = "0.00"
		}
		budgetUSD := task.Spec.BudgetUSD
		if budgetUSD == "" {
			budgetUSD = "-"
		}
		branch := task.Spec.Repository.WorkingBranch
		if branch == "" {
			branch = task.Spec.Repository.BaseBranch
		}
		if branch == "" {
			branch = "-"
		}

		snap.AgentTasks = append(snap.AgentTasks, TopAgentTaskRow{
			Name:           task.Name,
			Namespace:      task.Namespace,
			Phase:          phase,
			Iteration:      iter,
			TokensConsumed: task.Status.TokensConsumed,
			CostUSD:        costUSD,
			BudgetUSD:      budgetUSD,
			Branch:         branch,
		})
	}

	snap.Summary = TopSummary{
		TotalEnvironments:      len(envList.Items),
		ActiveEnvironments:     activeEnvs,
		HibernatedEnvironments: hibernatedEnvs,
		PreviewGroups:          len(pgList.Items),
		AgentTasks:             len(taskList.Items),
		ActiveAgentTasks:       activeTasks,
		HourlyBurnUSD:          totalHourlyUSD,
		ProjectedMonthlyUSD:    totalHourlyUSD * hoursPerMonth,
		AgentLLMSpendUSD:       llmSpendUSD,
	}

	return snap, nil
}

func renderTopSnapshot(out io.Writer, snap *TopSnapshot, outputFormat string, noColor bool) error {
	switch outputFormat {
	case "json":
		b, err := json.MarshalIndent(snap, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal JSON: %w", err)
		}
		_, _ = fmt.Fprintln(out, string(b))
		return nil
	case "yaml":
		b, err := yaml.Marshal(snap)
		if err != nil {
			return fmt.Errorf("failed to marshal YAML: %w", err)
		}
		_, _ = fmt.Fprint(out, string(b))
		return nil
	}

	_, _ = fmt.Fprintf(
		out,
		"DIVERGE TOP  |  Envs: %d (%d hibernated)  |  Groups: %d  |  AgentTasks: %d  |  Fleet Burn: $%.3f/hr ($%.2f/mo)  |  LLM Spend: $%.2f\n\n",
		snap.Summary.TotalEnvironments,
		snap.Summary.HibernatedEnvironments,
		snap.Summary.PreviewGroups,
		snap.Summary.AgentTasks,
		snap.Summary.HourlyBurnUSD,
		snap.Summary.ProjectedMonthlyUSD,
		snap.Summary.AgentLLMSpendUSD,
	)

	_, _ = fmt.Fprintln(out, "ENVIRONMENTS")
	if len(snap.Environments) == 0 {
		_, _ = fmt.Fprintln(out, "  No environments found.")
	} else {
		w := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
		_, _ = fmt.Fprintln(w, "  NAME\tNAMESPACE\tSTATUS\tTTL\tSTATE\tCOST/HR\tURL")
		for _, env := range snap.Environments {
			_, _ = fmt.Fprintf(
				w,
				"  %s\t%s\t%s\t%s\t%s\t$%.3f/hr\t%s\n",
				env.Name,
				env.Namespace,
				colorStatus(env.Phase, noColor),
				colorTTL(env.TTL, noColor),
				env.State,
				env.HourlyCostUSD,
				env.URL,
			)
		}
		_ = w.Flush()
	}
	_, _ = fmt.Fprintln(out)

	_, _ = fmt.Fprintln(out, "PREVIEW GROUPS")
	if len(snap.PreviewGroups) == 0 {
		_, _ = fmt.Fprintln(out, "  No preview groups found.")
	} else {
		w := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
		_, _ = fmt.Fprintln(w, "  NAME\tNAMESPACE\tSTATUS\tSERVICES\tHEADER\tAGE")
		for _, pg := range snap.PreviewGroups {
			_, _ = fmt.Fprintf(
				w,
				"  %s\t%s\t%s\t%d\t%s\t%s\n",
				pg.Name,
				pg.Namespace,
				colorStatus(pg.Phase, noColor),
				pg.Services,
				pg.Header,
				pg.Age,
			)
		}
		_ = w.Flush()
	}

	if len(snap.AgentTasks) > 0 {
		_, _ = fmt.Fprintln(out)
		_, _ = fmt.Fprintln(out, "AGENT TASKS")
		w := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
		_, _ = fmt.Fprintln(w, "  NAME\tNAMESPACE\tPHASE\tITERATION\tTOKENS\tCOST/BUDGET\tBRANCH")
		for _, task := range snap.AgentTasks {
			costBudget := fmt.Sprintf("$%s / $%s", strings.TrimPrefix(task.CostUSD, "$"), strings.TrimPrefix(task.BudgetUSD, "$"))
			_, _ = fmt.Fprintf(
				w,
				"  %s\t%s\t%s\t%s\t%d\t%s\t%s\n",
				task.Name,
				task.Namespace,
				task.Phase,
				task.Iteration,
				task.TokensConsumed,
				costBudget,
				task.Branch,
			)
		}
		_ = w.Flush()
	}

	return nil
}
