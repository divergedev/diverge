package main

import (
	"log"
	"net/http"
	"os"
	"time"

	fullstackdemo "github.com/divergedev/diverge/examples/fullstack-demo"
)

// NewWorkerHandler constructs the HTTP handler for fulfillment-worker.
func NewWorkerHandler() http.Handler {
	return fullstackdemo.NewWorkerHandler()
}

func main() {
	port := getenv("PORT", "8082")

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           NewWorkerHandler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	log.Printf("fulfillment-worker listening on :%s", port)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("fulfillment-worker server error: %v", err)
	}
}

func getenv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}
