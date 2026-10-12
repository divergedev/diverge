package main

import (
	"log"
	"net/http"
	"os"
	"time"

	fullstackdemo "github.com/divergedev/diverge/examples/fullstack-demo"
)

// NewStorefrontHandler constructs the HTTP handler for storefront-web.
func NewStorefrontHandler(checkoutAPIURL string, client *http.Client) http.Handler {
	return fullstackdemo.NewStorefrontHandler(checkoutAPIURL, client)
}

func main() {
	port := getenv("PORT", "8080")
	checkoutURL := getenv("CHECKOUT_API_URL", "http://checkout-api.default.svc.cluster.local:8081")

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           NewStorefrontHandler(checkoutURL, nil),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	log.Printf("storefront-web listening on :%s (upstream checkout-api: %s)", port, checkoutURL)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("storefront-web server error: %v", err)
	}
}

func getenv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}
