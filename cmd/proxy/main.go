package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/divergedev/diverge/api/v1alpha1"
	"github.com/divergedev/diverge/internal/proxy"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))
}

func main() {
	var (
		port          int
		previewDomain string
		upstream      string
		headerKey     string
		kubeconfig    string
		namespace     string
	)

	flag.IntVar(&port, "port", 8090, "Port to listen on")
	flag.StringVar(&previewDomain, "preview-domain", "", "Wildcard domain (e.g., preview.example.com)")
	flag.StringVar(&upstream, "upstream", "", "Upstream base URL (e.g., https://app.staging.example.com)")
	flag.StringVar(&headerKey, "header-key", "x-diverge-env", "Header to inject")
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to kubeconfig")
	flag.StringVar(&namespace, "namespace", "default", "Kubernetes namespace")
	flag.Parse()

	if previewDomain == "" || upstream == "" {
		fmt.Fprintln(os.Stderr, "--preview-domain and --upstream are required")
		os.Exit(1)
	}

	cfg := proxy.Config{
		BaseURL:       upstream,
		HeaderKey:     headerKey,
		PreviewDomain: previewDomain,
		Port:          port,
	}

	lister, err := proxy.NewK8sEnvironmentLister(kubeconfig, namespace, scheme)
	if err != nil {
		log.Fatalf("Failed to initialize K8s client: %v", err)
	}

	server, err := proxy.NewServer(cfg, lister)
	if err != nil {
		log.Fatalf("Failed to initialize proxy server: %v", err)
	}

	handler := proxy.LoggingMiddleware(proxy.CORSMiddleware(server))

	addr := fmt.Sprintf(":%d", port)
	log.Printf("Starting Magic URLs proxy on %s", addr)
	log.Printf("Preview Domain: %s", previewDomain)
	log.Printf("Upstream: %s", upstream)

	if err := http.ListenAndServe(addr, handler); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
