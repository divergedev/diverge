package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

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
	var defaultNamespace string

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.IntVar(&webhookPort, "webhook-port", 9443, "The port the webhook server binds to.")
	flag.StringVar(&routingProvider, "routing-provider", "gateway", "The routing provider to use (istio|gateway).")
	flag.StringVar(&deployProvider, "deploy-provider", "noop", "Deployment provider (argocd|noop|direct|knative)")
	flag.StringVar(&databaseProvider, "database-provider", "none", "Database provider (schema|none)")
	flag.StringVar(&argoNamespace, "argo-namespace", "argocd", "Namespace where Argo CD is installed")
	flag.StringVar(&argoRepoURL, "argo-repo-url", "", "Repository URL for Argo CD Application sources")
	flag.StringVar(&notifierProvider, "notifier-provider", "noop", "Notification provider (gitlab|github|noop)")
	flag.StringVar(&webhookSecretToken, "webhook-secret-token", "", "The secret token for authenticating webhooks (prefer DIVERGE_WEBHOOK_SECRET env var).")
	flag.StringVar(&defaultNamespace, "default-namespace", "default", "Default namespace to create environments in")

	var manifestSourceType string
	flag.StringVar(&manifestSourceType, "manifest-source-type", "configmap", "Manifest source type for direct deployer (configmap|url)")
	var gitlabToken string
	flag.StringVar(&gitlabToken, "gitlab-token", "", "GitLab token for preview group notifier")
	var gitlabURL string
	flag.StringVar(&gitlabURL, "gitlab-url", "", "GitLab URL for preview group notifier")

	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	// C2: Read secrets from environment variables (take precedence over flags)
	notifierToken := os.Getenv("DIVERGE_NOTIFIER_TOKEN")
	if gitlabToken == "" {
		gitlabToken = os.Getenv("DIVERGE_GITLAB_TOKEN")
	}
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
	switch routingProvider {
	case "istio":
		routerImpl = &routing.IstioRouter{Client: mgr.GetClient()}
	case "noop":
		routerImpl = &routing.NoopRouter{}
	case "gateway", "":
		routerImpl = &routing.GatewayRouter{Client: mgr.GetClient()}
	default:
		setupLog.Error(fmt.Errorf("unsupported routing provider: %q", routingProvider), "invalid --routing-provider")
		os.Exit(1)
	}

	if routingProvider == "" {
		routingProvider = "gateway"
	}

	// Wrap with metrics
	routerImpl = &routing.InstrumentedRouter{Inner: routerImpl, Mode: routingProvider}

	var dbProviderImpl database.DatabaseProvider
	switch databaseProvider {
	case "none", "":
		dbProviderImpl = &database.NoopDatabaseProvider{}
	case "schema":
		dbHost := os.Getenv("DIVERGE_DB_HOST")
		dbPort := os.Getenv("DIVERGE_DB_PORT")
		dbUser := os.Getenv("DIVERGE_DB_USER")
		dbPassword := os.Getenv("DIVERGE_DB_PASSWORD")
		dbName := os.Getenv("DIVERGE_DB_NAME")
		dbSSLMode := os.Getenv("DIVERGE_DB_SSLMODE")
		if dbSSLMode == "" {
			dbSSLMode = "disable"
		}
		if dbPort == "" {
			dbPort = "5432"
		}

		if dbHost == "" {
			setupLog.Error(fmt.Errorf("DIVERGE_DB_HOST is required for --database-provider=schema"), "missing database host")
			os.Exit(1)
		}

		dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s",
			dbUser, url.QueryEscape(dbPassword), dbHost, dbPort, dbName, dbSSLMode)
		db, err := sql.Open("pgx", dsn)
		if err != nil {
			setupLog.Error(err, "failed to open database connection")
			os.Exit(1)
		}
		// Verify connectivity
		pingCtx, pingCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer pingCancel()
		if err := db.PingContext(pingCtx); err != nil {
			setupLog.Error(err, "failed to ping database", "host", dbHost, "port", dbPort)
			_ = db.Close()
			os.Exit(1)
		}
		setupLog.Info("Connected to database", "host", dbHost, "port", dbPort, "database", dbName)
		_ = db.Close() // Close probe handle; SchemaDatabaseProvider opens its own connections

		dbProviderImpl = &database.SchemaDatabaseProvider{
			AdminDSN: dsn,
		}
	case "noop":
		dbProviderImpl = &database.NoopDatabaseProvider{}
	default:
		setupLog.Error(fmt.Errorf("unsupported database provider: %q", databaseProvider), "invalid --database-provider")
		os.Exit(1)
	}

	// Normalize label for metrics
	if databaseProvider == "" {
		databaseProvider = "none"
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
		case "serviceconfig":
			fetcher = &deployer.ServiceConfigFetcher{}
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
	case "knative":
		deployerImpl = &deployer.KNativeDeployer{
			Client: mgr.GetClient(),
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

	if (gitlabToken != "" && gitlabURL == "") || (gitlabToken == "" && gitlabURL != "") {
		setupLog.Error(fmt.Errorf("both --gitlab-token and --gitlab-url must be set together"), "incomplete GitLab configuration")
		os.Exit(1)
	}

	var pgNotifierImpl notifier.PreviewGroupNotifier = &notifier.NoopPreviewGroupNotifier{}
	if gitlabToken != "" && gitlabURL != "" {
		pgNotifierImpl = notifier.NewGitLabPreviewGroupNotifier(gitlabURL, gitlabToken)
	} else if notifierProvider == "github" && notifierToken != "" {
		pgNotifierImpl = notifier.NewGitHubPreviewGroupNotifier("", notifierToken)
	}

	if err = (&controller.PreviewGroupReconciler{
		Client:           mgr.GetClient(),
		Scheme:           mgr.GetScheme(),
		Recorder:         mgr.GetEventRecorderFor("diverge-previewgroup"),
		Notifier:         pgNotifierImpl,
		StatusReporter:   statusReporterImpl,
		DatabaseProvider: dbProviderImpl,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "PreviewGroup")
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

	if err = divergeiov1alpha1.SetupPreviewGroupWebhookWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create webhook", "webhook", "PreviewGroup")
		os.Exit(1)
	}

	// Setup webhook server for GitLab events
	webhookConfig := webhook.WebhookConfig{SecretToken: webhookSecretToken}

	var glConfigFetcher webhook.ConfigFetcher
	if notifierProvider == "gitlab" && notifierToken != "" {
		glConfigFetcher = &webhook.GitLabConfigFetcher{
			Token:      notifierToken,
			HTTPClient: &http.Client{Timeout: 15 * time.Second},
		}
	}
	mgr.GetWebhookServer().Register("/gitlab-webhook", &webhook.GitLabWebhookHandler{
		Client:        mgr.GetClient(),
		Config:        webhookConfig,
		ConfigFetcher: glConfigFetcher,
		DefaultNS:     defaultNamespace,
	})

	var ghConfigFetcher webhook.ConfigFetcher
	if notifierProvider == "github" && notifierToken != "" {
		ghConfigFetcher = &webhook.GitHubConfigFetcher{
			Token:      notifierToken,
			HTTPClient: &http.Client{Timeout: 15 * time.Second},
		}
	}
	mgr.GetWebhookServer().Register("/github-webhook", &webhook.GitHubWebhookHandler{
		Client:        mgr.GetClient(),
		Config:        webhookConfig,
		ConfigFetcher: ghConfigFetcher,
		DefaultNS:     defaultNamespace,
	})

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
