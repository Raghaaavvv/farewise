package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"farewise/backend/internal/api"
	"farewise/backend/internal/fare"
)

func main() {
	addr := os.Getenv("FAREWISE_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	providers := fare.DemoProviders()
	for i, provider := range providers {
		protected, err := fare.NewProtectedProvider(provider, fare.DemoProviderPolicy())
		if err != nil {
			log.Fatal(err)
		}
		providers[i] = protected
	}
	// This total budget includes rate-limit waits, retries and adapter latency.
	service := fare.NewService(providers, 5*time.Second)
	server := &http.Server{
		Addr: addr, Handler: api.NewHandler(service),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			log.Printf("shutdown: %v", err)
		}
	}()
	log.Printf("Farewise DEMO API listening at http://%s (no live provider requests)", addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
