package cli

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"sigs.k8s.io/yaml"

	pb "github.com/divergedev/diverge/api/gen/diverge/v1alpha1"
	divergev1alpha1connect "github.com/divergedev/diverge/api/gen/diverge/v1alpha1/divergev1alpha1connect"
	"github.com/divergedev/diverge/internal/finops"
)

type costOptions struct {
	allNamespaces       bool
	outputFormat        string
	topLimit            int
	sortBy              string
	serverURL           string
	openCostURL         string
	failOnBudgetWarning bool
}

func newCostCmd(app *App) *cobra.Command {
	opts := &costOptions{}

	cmd := &cobra.Command{
		Use:     "cost",
		Aliases: []string{"finops"},
		Short:   "Inspect FinOps preview environment spend, savings, quotas, and top consumers",
		Long: `Compute real-time and accrued FinOps cloud spend across preview environments,
including idle hibernation savings, PreviewGroup delta-routing savings, autonomous
AgentTask LLM spend, per-namespace quota utilization, and optional OpenCost live allocation enrichment.`,
		Example: `	# Show FinOps summary and top 10 preview consumers in current namespace
	diverge cost

	# Aggregate across all namespaces and sort by cumulative accrued spend
	diverge cost -A --sort accrued

	# Export CSV chargeback report across all namespaces
	diverge cost -A --top 0 -o csv > finops-report.csv

	# Gate CI on namespace FinOps budget warnings via diverge-server
	diverge cost -n staging --server-url https://diverge.example.com --fail-on-budget-warning`,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runCostReport(cmd, app, opts, false)
		},
	}

	cmd.PersistentFlags().BoolVarP(&opts.allNamespaces, "all-namespaces", "A", false, "Aggregate FinOps metrics across all namespaces")
	cmd.PersistentFlags().StringVarP(&opts.outputFormat, "output", "o", "table", "Output format: table, json, yaml, csv")
	cmd.PersistentFlags().IntVar(&opts.topLimit, "top", 10, "Maximum environment consumers to display (0 for unlimited)")
	cmd.PersistentFlags().StringVar(&opts.sortBy, "sort", "hourly", "Sort consumers by: hourly, accrued, projected, uptime")
	cmd.PersistentFlags().StringVar(&opts.serverURL, "server-url", "", "Diverge ConnectRPC server URL (optional; uses kubeconfig when empty)")
	cmd.PersistentFlags().StringVar(&opts.openCostURL, "opencost-url", "", "OpenCost / Kubecost Allocation API URL (defaults to DIVERGE_OPENCOST_URL)")
	cmd.PersistentFlags().BoolVar(&opts.failOnBudgetWarning, "fail-on-budget-warning", false, "Exit non-zero when any namespace exceeds its FinOps budget warning threshold")

	cmd.AddCommand(&cobra.Command{
		Use:          "summary",
		Short:        "Show fleet, namespace, and top consumer FinOps summary",
		SilenceUsage: true,
		RunE: func(subCmd *cobra.Command, _ []string) error {
			return runCostReport(subCmd, app, opts, false)
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:          "top",
		Short:        "Show top preview environment and PR FinOps consumers",
		SilenceUsage: true,
		RunE: func(subCmd *cobra.Command, _ []string) error {
			return runCostReport(subCmd, app, opts, true)
		},
	})

	return cmd
}

func runCostReport(cmd *cobra.Command, app *App, opts *costOptions, consumersOnly bool) error {
	format := strings.ToLower(strings.TrimSpace(opts.outputFormat))
	switch format {
	case "", "table":
		format = "table"
	case "json", "yaml", "csv":
	default:
		return fmt.Errorf("unsupported output format %q (supported: table, json, yaml, csv)", opts.outputFormat)
	}

	sortField := strings.ToLower(strings.TrimSpace(opts.sortBy))
	switch sortField {
	case "", "hourly", "accrued", "projected", "uptime":
		if sortField == "" {
			sortField = "hourly"
		}
	default:
		return fmt.Errorf("unsupported --sort value %q (supported: hourly, accrued, projected, uptime)", opts.sortBy)
	}

	if opts.topLimit < 0 {
		return fmt.Errorf("--top must be >= 0, got %d", opts.topLimit)
	}

	summary, err := fetchCostSummary(cmd.Context(), app, opts)
	if err != nil {
		return err
	}

	cloned := proto.Clone(summary).(*pb.GetCostSummaryResponse)
	sortCostConsumers(cloned.TopConsumers, sortField)
	if opts.topLimit > 0 && len(cloned.TopConsumers) > opts.topLimit {
		cloned.TopConsumers = cloned.TopConsumers[:opts.topLimit]
	}

	switch format {
	case "json":
		if err := renderCostJSON(cmd.OutOrStdout(), cloned); err != nil {
			return err
		}
	case "yaml":
		if err := renderCostYAML(cmd.OutOrStdout(), cloned); err != nil {
			return err
		}
	case "csv":
		if err := renderCostCSV(cmd.OutOrStdout(), cloned.TopConsumers); err != nil {
			return err
		}
	default:
		if err := renderCostTable(cmd.OutOrStdout(), cloned, consumersOnly); err != nil {
			return err
		}
	}

	if opts.failOnBudgetWarning {
		var warnings []string
		for _, ns := range cloned.ByNamespace {
			if ns.GetBudgetWarning() {
				warnings = append(warnings, fmt.Sprintf("%s (%s)", ns.GetNamespace(), ns.GetWarningReason()))
			}
		}
		if len(warnings) > 0 {
			return fmt.Errorf("FinOps budget warning threshold exceeded: %s", strings.Join(warnings, "; "))
		}
	}
	return nil
}

func fetchCostSummary(ctx context.Context, app *App, opts *costOptions) (*pb.GetCostSummaryResponse, error) {
	explicitServer := strings.TrimSpace(opts.serverURL)
	if explicitServer != "" {
		ns := ""
		if !opts.allNamespaces && app != nil {
			if app.Namespace == "" {
				_ = app.ResolveNamespace()
			}
			ns = strings.TrimSpace(app.Namespace)
		}
		return fetchCostSummaryViaServer(ctx, explicitServer, ns)
	}

	if app == nil {
		return nil, errors.New("CLI application context is nil")
	}

	if !opts.allNamespaces && app.Namespace == "" {
		_ = app.ResolveNamespace()
	}

	c, k8sClient, kubeErr := app.KubeClient()
	if kubeErr != nil {
		if envServer := strings.TrimSpace(os.Getenv("DIVERGE_SERVER_URL")); envServer != "" {
			ns := ""
			if !opts.allNamespaces {
				ns = strings.TrimSpace(app.Namespace)
			}
			return fetchCostSummaryViaServer(ctx, envServer, ns)
		}
		return nil, kubeErr
	}

	ns := strings.TrimSpace(app.Namespace)
	if opts.allNamespaces {
		ns = ""
	}

	var ocClient *finops.OpenCostClient
	if strings.TrimSpace(opts.openCostURL) != "" {
		ocClient = finops.NewOpenCostClient(opts.openCostURL, nil)
	}

	return finops.BuildCostSummary(ctx, finops.SummaryOptions{
		Namespace: ns,
		Client:    c,
		K8sClient: k8sClient,
		OpenCost:  ocClient,
		Timeout:   10 * time.Second,
	})
}

func fetchCostSummaryViaServer(ctx context.Context, rawServerURL, namespace string) (*pb.GetCostSummaryResponse, error) {
	serverURL := resolveMCPServerURL(rawServerURL)
	if serverURL == "" {
		return nil, errors.New("server URL cannot be empty")
	}
	httpClient := newMCPHTTPClient(serverURL)
	clusterClient := divergev1alpha1connect.NewClusterServiceClient(httpClient, serverURL)

	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	resp, err := clusterClient.GetCostSummary(reqCtx, connect.NewRequest(&pb.GetCostSummaryRequest{
		Namespace: namespace,
	}))
	if err != nil {
		return nil, err
	}
	return resp.Msg, nil
}

func sortCostConsumers(items []*pb.EnvironmentCostItem, sortField string) {
	sort.Slice(items, func(i, j int) bool {
		var a, b float64
		switch sortField {
		case "accrued":
			a, b = items[i].GetAccruedCostUsd(), items[j].GetAccruedCostUsd()
		case "projected":
			a, b = items[i].GetProjectedTtlCostUsd(), items[j].GetProjectedTtlCostUsd()
		case "uptime":
			a, b = items[i].GetUptimeHours(), items[j].GetUptimeHours()
		default:
			a, b = items[i].GetHourlyCostUsd(), items[j].GetHourlyCostUsd()
		}
		if math.Abs(a-b) > 1e-9 {
			return a > b
		}
		if items[i].GetNamespace() != items[j].GetNamespace() {
			return items[i].GetNamespace() < items[j].GetNamespace()
		}
		return items[i].GetName() < items[j].GetName()
	})
}

func renderCostJSON(w io.Writer, resp *pb.GetCostSummaryResponse) error {
	raw, err := protojson.MarshalOptions{EmitUnpopulated: true}.Marshal(resp)
	if err != nil {
		return err
	}
	var pretty any
	if err := json.Unmarshal(raw, &pretty); err != nil {
		return err
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(pretty)
}

func renderCostYAML(w io.Writer, resp *pb.GetCostSummaryResponse) error {
	raw, err := protojson.MarshalOptions{EmitUnpopulated: true}.Marshal(resp)
	if err != nil {
		return err
	}
	out, err := yaml.JSONToYAML(raw)
	if err != nil {
		return err
	}
	_, err = w.Write(out)
	return err
}

func renderCostCSV(w io.Writer, consumers []*pb.EnvironmentCostItem) error {
	cw := csv.NewWriter(w)
	header := []string{
		"namespace", "name", "project", "mr", "branch", "phase", "hibernated",
		"hourly_cost_usd", "hibernation_savings_hourly_usd", "accrued_cost_usd",
		"projected_ttl_cost_usd", "uptime_hours", "cpu_cores", "memory_gib",
		"service_count", "database_mode", "ttl_warning", "ttl_warning_reason",
	}
	if err := cw.Write(header); err != nil {
		return err
	}
	for _, item := range consumers {
		row := []string{
			item.GetNamespace(),
			item.GetName(),
			item.GetProject(),
			item.GetMr(),
			item.GetBranch(),
			item.GetPhase(),
			strconv.FormatBool(item.GetHibernated()),
			fmt.Sprintf("%.4f", item.GetHourlyCostUsd()),
			fmt.Sprintf("%.4f", item.GetHibernationSavingsHourlyUsd()),
			fmt.Sprintf("%.4f", item.GetAccruedCostUsd()),
			fmt.Sprintf("%.4f", item.GetProjectedTtlCostUsd()),
			fmt.Sprintf("%.2f", item.GetUptimeHours()),
			fmt.Sprintf("%.3f", item.GetCpuCores()),
			fmt.Sprintf("%.3f", item.GetMemoryGib()),
			strconv.FormatInt(int64(item.GetServiceCount()), 10),
			item.GetDatabaseMode(),
			strconv.FormatBool(item.GetTtlWarning()),
			item.GetTtlWarningReason(),
		}
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

func renderCostTable(w io.Writer, resp *pb.GetCostSummaryResponse, consumersOnly bool) error {
	writef := func(dst io.Writer, format string, args ...any) error {
		_, err := fmt.Fprintf(dst, format, args...)
		return err
	}
	writeln := func(dst io.Writer, args ...any) error {
		_, err := fmt.Fprintln(dst, args...)
		return err
	}

	if !consumersOnly {
		if err := writef(w, "Diverge FinOps Summary\n"); err != nil {
			return err
		}
		if err := writef(w, "  Hourly Burn:          $%.3f/hr ($%.2f/mo run-rate)\n", resp.GetTotalHourlyCostUsd(), resp.GetMonthlyRunRateUsd()); err != nil {
			return err
		}
		if err := writef(w, "  Hibernation Savings:  $%.3f/hr\n", resp.GetHibernationSavingsHourlyUsd()); err != nil {
			return err
		}
		if err := writef(w, "  Delta Route Savings:  $%.3f/hr\n", resp.GetDeltaRoutingSavingsHourlyUsd()); err != nil {
			return err
		}
		if err := writef(w, "  Accrued Env Spend:    $%.3f (%.2f service-hrs)\n", resp.GetTotalAccruedCostUsd(), resp.GetTotalComputeHours()); err != nil {
			return err
		}
		if err := writef(w, "  AgentTask LLM Spend:  $%.2f\n", resp.GetAgentTaskCostUsd()); err != nil {
			return err
		}
		if err := writef(w, "  Environments:         %d total (%d active, %d hibernated) | %.2f vCPU | %.2f GiB\n",
			resp.GetTotalEnvironments(), resp.GetActiveEnvironments(), resp.GetHibernatedEnvironments(),
			resp.GetTotalCpuCores(), resp.GetTotalMemoryGib()); err != nil {
			return err
		}

		if len(resp.ByNamespace) > 0 {
			if err := writeln(w); err != nil {
				return err
			}
			tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
			if err := writeln(tw, "NAMESPACE\tACTIVE/TOTAL\tHOURLY\tACCRUED\tSAVED/HR\tQUOTA UTIL\tSTATUS"); err != nil {
				return err
			}
			for _, ns := range resp.ByNamespace {
				utilStr := "-"
				if ns.GetQuota() != nil && ns.GetBudgetUtilizationPercent() > 0 {
					utilStr = fmt.Sprintf("%.0f%%", ns.GetBudgetUtilizationPercent())
				}
				statusStr := "OK"
				if ns.GetBudgetWarning() {
					statusStr = "WARN: " + ns.GetWarningReason()
				}
				if err := writef(tw, "%s\t%d/%d\t$%.3f/hr\t$%.3f\t$%.3f/hr\t%s\t%s\n",
					ns.GetNamespace(),
					ns.GetActiveEnvironments(),
					ns.GetTotalEnvironments(),
					ns.GetHourlyCostUsd(),
					ns.GetAccruedCostUsd(),
					ns.GetHibernationSavingsHourlyUsd(),
					utilStr,
					statusStr,
				); err != nil {
					return err
				}
			}
			if err := tw.Flush(); err != nil {
				return err
			}
		}
		if err := writeln(w); err != nil {
			return err
		}
	}

	if len(resp.TopConsumers) == 0 {
		return writeln(w, "No preview environments found.")
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if err := writeln(tw, "NAMESPACE\tENVIRONMENT\tMR/BRANCH\tSTATE\tDB\tHOURLY\tACCRUED\tTTL PROJECTED\tNOTES"); err != nil {
		return err
	}
	for _, item := range resp.TopConsumers {
		state := item.GetPhase()
		if item.GetHibernated() {
			state = "Hibernated"
		}
		mrBranch := item.GetBranch()
		if item.GetMr() != "" {
			if mrBranch != "" {
				mrBranch = "#" + item.GetMr() + " (" + mrBranch + ")"
			} else {
				mrBranch = "#" + item.GetMr()
			}
		}
		if mrBranch == "" {
			mrBranch = "-"
		}
		notes := "-"
		if item.GetTtlWarning() {
			notes = item.GetTtlWarningReason()
		}
		if err := writef(tw, "%s\t%s\t%s\t%s\t%s\t$%.3f/hr\t$%.3f\t$%.3f\t%s\n",
			item.GetNamespace(),
			item.GetName(),
			mrBranch,
			state,
			item.GetDatabaseMode(),
			item.GetHourlyCostUsd(),
			item.GetAccruedCostUsd(),
			item.GetProjectedTtlCostUsd(),
			notes,
		); err != nil {
			return err
		}
	}
	return tw.Flush()
}
