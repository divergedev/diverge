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
	"github.com/divergedev/diverge/internal/changeset"
	"github.com/divergedev/diverge/internal/controller"
	"github.com/divergedev/diverge/internal/database"
	"github.com/divergedev/diverge/internal/deployer"
	"github.com/divergedev/diverge/internal/events"
	_ "github.com/divergedev/diverge/internal/metrics"
	"github.com/divergedev/diverge/internal/notifier"
	"github.com/divergedev/diverge/internal/routing"
	divtesting "github.com/divergedev/diverge/internal/testing"
	"github.com/divergedev/diverge/internal/webhook"
	pkgdb "github.com/divergedev/diverge/pkg/database"
	"github.com/divergedev/diverge/pkg/registry"
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
	var webhookSecretToken string
	var notifierProvider string
	var defaultNamespace string

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.IntVar(&webhookPort, "webhook-port", 9443, "The port the webhook server binds to.")
	flag.StringVar(&routingProvider, "routing-provider", "gateway", "The routing provider to use (istio|gateway|composite).")
	flag.StringVar(&deployProvider, "deploy-provider", "noop", "Deployment provider (argocd|noop|direct|knative)")
	flag.StringVar(&databaseProvider, "database-provider", "none", "Database provider (schema|none)")
	flag.StringVar(&notifierProvider, "notifier-provider", "noop", "Notification provider (gitlab|github|noop)")
	flag.StringVar(&webhookSecretToken, "webhook-secret-token", "", "The secret token for authenticating webhooks (prefer DIVERGE_WEBHOOK_SECRET env var).")
	flag.StringVar(&defaultNamespace, "default-namespace", "default", "Default namespace to create environments in")

	var kedaMinReplicas int64
	var kedaMaxReplicas int64
	var kedaCooldown int64
	flag.Int64Var(&kedaMinReplicas, "keda-min-replicas", 0, "Minimum replicas for KEDA scaling")
	flag.Int64Var(&kedaMaxReplicas, "keda-max-replicas", 3, "Maximum replicas for KEDA scaling")
	flag.Int64Var(&kedaCooldown, "keda-cooldown", 300, "Cooldown period in seconds for KEDA scaling")

	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	if kedaMinReplicas < 0 {
		setupLog.Error(fmt.Errorf("--keda-min-replicas must be >= 0, got %d", kedaMinReplicas), "invalid flag")
		os.Exit(1)
	}
	if kedaMaxReplicas == 0 {
		kedaMaxReplicas = 3 // default
	}
	if kedaMaxReplicas < kedaMinReplicas {
		setupLog.Error(fmt.Errorf("--keda-max-replicas (%d) must be >= --keda-min-replicas (%d)", kedaMaxReplicas, kedaMinReplicas), "invalid flag")
		os.Exit(1)
	}
	if kedaCooldown < 0 {
		setupLog.Error(fmt.Errorf("--keda-cooldown must be >= 0, got %d", kedaCooldown), "invalid flag")
		os.Exit(1)
	}

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

	deps := registry.Deps{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		Logger: ctrl.Log,
	}

	var routerImpl routing.Router
	if routingProvider == "" {
		routingProvider = "gateway"
	}
	routerImpl, err = routing.Providers.Create(routingProvider, deps)
	if err != nil {
		setupLog.Error(err, "creating router", "provider", routingProvider)
		os.Exit(1)
	}

	// Wrap with metrics
	routerImpl = &routing.InstrumentedRouter{Inner: routerImpl, Mode: routingProvider}

	if databaseProvider == "" {
		databaseProvider = "none"
	}
	dbProviderImpl, err := pkgdb.Providers.Create(databaseProvider, deps)
	if err != nil {
		setupLog.Error(err, "creating database provider", "provider", databaseProvider)
		os.Exit(1)
	}

	// Wrap with metrics
	dbProviderImpl = &database.InstrumentedDatabaseProvider{Inner: dbProviderImpl, Mode: databaseProvider}
	detectorImpl := &changeset.GitChangeDetector{}

	if notifierProvider == "" {
		notifierProvider = "noop"
	}
	notifierImpl, err := notifier.Providers.Create(notifierProvider, deps)
	if err != nil {
		setupLog.Error(err, "creating notifier", "provider", notifierProvider)
		os.Exit(1)
	}

	statusReporterImpl, err := notifier.StatusProviders.Create(notifierProvider, deps)
	if err != nil {
		setupLog.Error(err, "creating status reporter", "provider", notifierProvider)
		os.Exit(1)
	}

	if deployProvider == "" {
		deployProvider = "noop"
	}
	deployerImpl, err := deployer.Providers.Create(deployProvider, deps)
	if err != nil {
		setupLog.Error(err, "creating deployer", "provider", deployProvider)
		os.Exit(1)
	}

	// Wrap with KEDA Deployer (detects CRD automatically)
	if deployProvider != "noop" {
		deployerImpl = &deployer.KEDADeployer{
			Inner:  deployerImpl,
			Client: mgr.GetClient(),
			Config: deployer.KEDAConfig{
				MinReplicas: kedaMinReplicas,
				MaxReplicas: kedaMaxReplicas,
				Cooldown:    kedaCooldown,
			},
		}
	}

	// Normalize label for metrics
	if deployProvider == "" {
		deployProvider = "noop"
	}

	// Wrap with metrics
	deployerImpl = &deployer.InstrumentedDeployer{Inner: deployerImpl, Name: deployProvider}

	testRunnerImpl, err := divtesting.Providers.Create(notifierProvider, deps)
	if err != nil {
		setupLog.Error(err, "creating test runner", "provider", notifierProvider)
		os.Exit(1)
	}

	if err = (&controller.EnvironmentReconciler{
		Client:           mgr.GetClient(),
		Scheme:           mgr.GetScheme(),
		Recorder:         events.NewRecorder(mgr.GetEventRecorder("diverge-controller")),
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

	pgNotifierImpl, err := notifier.GroupProviders.Create(notifierProvider, deps)
	if err != nil {
		setupLog.Error(err, "creating preview group notifier", "provider", notifierProvider)
		os.Exit(1)
	}

	if err = (&controller.PreviewGroupReconciler{
		Client:           mgr.GetClient(),
		Scheme:           mgr.GetScheme(),
		Recorder:         events.NewRecorder(mgr.GetEventRecorder("diverge-previewgroup")),
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
