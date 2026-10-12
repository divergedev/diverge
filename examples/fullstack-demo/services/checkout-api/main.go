package main

import (
	"log"
	"net/http"
	"os"
	"time"

	fullstackdemo "github.com/divergedev/diverge/examples/fullstack-demo"
)

// NewCheckoutHandler constructs the HTTP handler for checkout-api.
func NewCheckoutHandler(workerURL string, client *http.Client) http.Handler {
	return fullstackdemo.NewCheckoutHandler(workerURL, client)
}

func main() {
	port := getenv("PORT", "8081")
	workerURL := getenv("FULFILLMENT_WORKER_URL", "http://fulfillment-worker.default.svc.cluster.local:8082")

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           NewCheckoutHandler(workerURL, nil),
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("checkout-api listening on :%s (downstream worker: %s)", port, workerURL)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("checkout-api server error: %v", err)
	}
}

func getenv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}
