package cli

import (
	"context"
	"os"

	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"

	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	divergeiov1alpha1 "github.com/divergedev/diverge/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
)

var (
	kubeconfig string
	namespace  string
	contextCtx string
	noColor    bool

	cliVersion string
	cliCommit  string
	cliDate    string

	// enterpriseInits holds registered enterprise initializers.
	// These are called during Execute() to add enterprise commands and middleware.
	enterpriseInits []func(*cobra.Command)
)

var rootCmd = &cobra.Command{
	Use:   "diverge",
	Short: "Diverge CLI manages preview environments",
	Long:  `The developer's daily driver for interacting with Diverge environments.`,
}

// RegisterEnterprise adds an enterprise initializer that will be called
// during Execute() to register enterprise commands, middleware, and handlers.
// This is typically called from an init() function in an enterprise package.
func RegisterEnterprise(init func(*cobra.Command)) {
	enterpriseInits = append(enterpriseInits, init)
}

func Execute(version, commit, date string) {
	cliVersion = version
	cliCommit = commit
	cliDate = date

	// Initialize enterprise features if registered
	for _, init := range enterpriseInits {
		init(rootCmd)
	}

	err := rootCmd.ExecuteContext(context.Background())
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&kubeconfig, "kubeconfig", "", "path to kubeconfig (default: ~/.kube/config)")
	rootCmd.PersistentFlags().StringVarP(&namespace, "namespace", "n", "", "Kubernetes namespace (default: from kubeconfig context)")
	rootCmd.PersistentFlags().StringVar(&contextCtx, "context", "", "Kubernetes context")
	rootCmd.PersistentFlags().BoolVar(&noColor, "no-color", false, "disable color output")
}

func getKubeClient() (client.Client, *kubernetes.Clientset, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfig != "" {
		loadingRules.ExplicitPath = kubeconfig
	}
	configOverrides := &clientcmd.ConfigOverrides{
		CurrentContext: contextCtx,
	}
	if namespace != "" {
		configOverrides.Context.Namespace = namespace
	}

	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)

	if namespace == "" {
		ns, _, err := kubeConfig.Namespace()
		if err == nil {
			namespace = ns
		}
	}

	config, err := kubeConfig.ClientConfig()
	if err != nil {
		return nil, nil, err
	}

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(divergeiov1alpha1.AddToScheme(scheme))

	c, err := client.New(config, client.Options{Scheme: scheme})
	if err != nil {
		return nil, nil, err
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, nil, err
	}

	return c, clientset, nil
}
