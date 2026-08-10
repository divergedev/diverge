package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	webhookserver "sigs.k8s.io/controller-runtime/pkg/webhook"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/argocd"
	"github.com/divergedev/diverge/internal/changeset"
	"github.com/divergedev/diverge/internal/controller"
	"github.com/divergedev/diverge/internal/database"
	"github.com/divergedev/diverge/internal/deployer"
	_ "github.com/divergedev/diverge/internal/metrics"
	"github.com/divergedev/diverge/internal/notifier"
	"github.com/divergedev/diverge/internal/routing"
	divtesting "github.com/divergedev/diverge/internal/testing"
	"github.com/divergedev/diverge/internal/webhook"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(divergeiov1alpha1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	var webhookPort int
	var routingProvider string
	var deployProvider string
	var databaseProvider string
	var argoNamespace string
	var webhookSecretToken string
	var argoRepoURL string
	var notifierProvider string

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.IntVar(&webhookPort, "webhook-port", 9443, "The port the webhook server binds to.")
	flag.StringVar(&routingProvider, "routing-provider", "gateway", "The routing provider to use (istio|gateway).")
	flag.StringVar(&deployProvider, "deploy-provider", "noop", "Deployment provider (argocd|noop)")
	flag.StringVar(&databaseProvider, "database-provider", "shared", "Database provider (shared|noop)")
	flag.StringVar(&argoNamespace, "argo-namespace", "argocd", "Namespace where Argo CD is installed")
	flag.StringVar(&argoRepoURL, "argo-repo-url", "", "Repository URL for Argo CD Application sources")
	flag.StringVar(&notifierProvider, "notifier-provider", "noop", "Notification provider (gitlab|github|noop)")
	flag.StringVar(&webhookSecretToken, "webhook-secret-token", "", "The secret token for authenticating webhooks (prefer DIVERGE_WEBHOOK_SECRET env var).")

	var manifestSourceType string
	flag.StringVar(&manifestSourceType, "manifest-source-type", "configmap", "Manifest source type for direct deployer (configmap|url)")

	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	// C2: Read secrets from environment variables (take precedence over flags)
	notifierToken := os.Getenv("DIVERGE_NOTIFIER_TOKEN")
	if envSecret := os.Getenv("DIVERGE_WEBHOOK_SECRET"); envSecret != "" {
		webhookSecretToken = envSecret
	}

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "diverge.io",
		WebhookServer:          webhookserver.NewServer(webhookserver.Options{Port: webhookPort}),
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	var routerImpl routing.Router
	if routingProvider == "istio" {
		routerImpl = &routing.IstioRouter{Client: mgr.GetClient()}
	} else {
		routerImpl = &routing.GatewayRouter{Client: mgr.GetClient()}
	}

	// Normalize label for metrics
	if routingProvider != "istio" {
		routingProvider = "gateway"
	}

	// Wrap with metrics
	routerImpl = &routing.InstrumentedRouter{Inner: routerImpl, Mode: routingProvider}

	var dbProviderImpl database.DatabaseProvider
	switch databaseProvider {
	case "shared", "":
		dbProviderImpl = &database.SharedProvider{}
	case "schema":
		// Read database configuration for schema provider (Executor will be implemented later)
		_ = os.Getenv("DIVERGE_DB_HOST")
		_ = os.Getenv("DIVERGE_DB_PORT")
		_ = os.Getenv("DIVERGE_DB_USER")
		_ = os.Getenv("DIVERGE_DB_PASSWORD")
		_ = os.Getenv("DIVERGE_DB_NAME")
		_ = os.Getenv("DIVERGE_DB_SSLMODE")

		dbProviderImpl = &database.SchemaProvider{
			Executor: nil,
			Client:   mgr.GetClient(),
		}
	case "noop":
		dbProviderImpl = &database.NoopProvider{}
	default:
		setupLog.Error(fmt.Errorf("unsupported database provider: %q", databaseProvider), "invalid --database-provider")
		os.Exit(1)
	}

	// Normalize label for metrics
	if databaseProvider == "" {
		databaseProvider = "shared"
	}

	// Wrap with metrics
	dbProviderImpl = &database.InstrumentedDatabaseProvider{Inner: dbProviderImpl, Mode: databaseProvider}
	detectorImpl := &changeset.GitChangeDetector{}

	var notifierImpl notifier.Notifier
	var statusReporterImpl notifier.StatusReporter
	switch notifierProvider {
	case "gitlab":
		if notifierToken == "" {
			setupLog.Error(fmt.Errorf("DIVERGE_NOTIFIER_TOKEN is required for --notifier-provider=gitlab"), "missing token")
			os.Exit(1)
		}
		notifierImpl = notifier.NewGitLabNotifier("", notifierToken)
		statusReporterImpl = notifier.NewGitLabStatusReporter("", notifierToken)
	case "github":
		if notifierToken == "" {
			setupLog.Error(fmt.Errorf("DIVERGE_NOTIFIER_TOKEN is required for --notifier-provider=github"), "missing token")
			os.Exit(1)
		}
		notifierImpl = notifier.NewGitHubNotifier("", notifierToken)
		statusReporterImpl = notifier.NewGitHubStatusReporter("", notifierToken)
	case "noop", "":
		notifierImpl = &notifier.NoopNotifier{}
		statusReporterImpl = &notifier.NoopStatusReporter{}
	default:
		setupLog.Error(fmt.Errorf("unsupported notifier provider: %q", notifierProvider), "invalid --notifier-provider")
		os.Exit(1)
	}

	var deployerImpl deployer.Deployer
	switch deployProvider {
	case "argocd":
		argoClient := argocd.NewClient(mgr.GetClient(), argoNamespace)
		argoGenerator := &argocd.Generator{
			ArgoNamespace:     argoNamespace,
			RepoURL:           argoRepoURL,
			DestinationServer: "https://kubernetes.default.svc",
			Project:           "default",
		}
		deployerImpl = deployer.NewArgoDeployer(argoClient, argoGenerator, nil)
	case "direct":
		var fetcher deployer.ManifestFetcher
		switch manifestSourceType {
		case "url":
			manifestToken := os.Getenv("DIVERGE_MANIFEST_TOKEN")
			fetcher = &deployer.URLFetcher{
				HTTPClient: &http.Client{Timeout: 60 * time.Second},
				AuthToken:  manifestToken,
			}
		case "configmap", "":
			fetcher = &deployer.ConfigMapFetcher{Client: mgr.GetClient()}
		default:
			setupLog.Error(fmt.Errorf("unsupported manifest source type: %q", manifestSourceType), "invalid --manifest-source-type")
			os.Exit(1)
		}
		deployerImpl = &deployer.DirectDeployer{
			Client:  mgr.GetClient(),
			Fetcher: fetcher,
		}
	case "noop", "":
		deployerImpl = &deployer.NoopDeployer{}
	default:
		setupLog.Error(fmt.Errorf("unsupported deploy provider: %q", deployProvider), "invalid --deploy-provider")
		os.Exit(1)
	}

	// Normalize label for metrics
	if deployProvider == "" {
		deployProvider = "noop"
	}

	// Wrap with metrics
	deployerImpl = &deployer.InstrumentedDeployer{Inner: deployerImpl, Name: deployProvider}

	// Configure test runner (reuses notifier credentials)
	var testRunnerImpl divtesting.TestRunner
	switch notifierProvider {
	case "gitlab":
		testRunnerImpl = &divtesting.GitLabPipelineRunner{
			BaseURL:    "",
			Token:      notifierToken,
			HTTPClient: &http.Client{Timeout: 30 * time.Second},
		}
	case "github":
		testRunnerImpl = &divtesting.GitHubActionsRunner{
			Token:      notifierToken,
			HTTPClient: &http.Client{Timeout: 30 * time.Second},
		}
	default:
		testRunnerImpl = &divtesting.NoopTestRunner{}
	}

	if err = (&controller.EnvironmentReconciler{
		Client:           mgr.GetClient(),
		Scheme:           mgr.GetScheme(),
		Recorder:         mgr.GetEventRecorderFor("diverge-controller"),
		Router:           routerImpl,
		DatabaseProvider: dbProviderImpl,
		ChangeDetector:   detectorImpl,
		Notifier:         notifierImpl,
		StatusReporter:   statusReporterImpl,
		Deployer:         deployerImpl,
		TestRunner:       testRunnerImpl,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Environment")
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

	// Setup webhook server for GitLab events
	webhookConfig := webhook.WebhookConfig{SecretToken: webhookSecretToken}
	mgr.GetWebhookServer().Register("/gitlab-webhook", &webhook.GitLabWebhookHandler{Client: mgr.GetClient(), Config: webhookConfig})
	mgr.GetWebhookServer().Register("/github-webhook", &webhook.GitHubWebhookHandler{Client: mgr.GetClient(), Config: webhookConfig})

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
