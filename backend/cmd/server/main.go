package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"farewise/backend/internal/api"
	"farewise/backend/internal/fare"
	"farewise/backend/internal/routing"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	slog.SetDefault(logger)
	addr := os.Getenv("FAREWISE_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	providers := fare.DemoProviders()
	for i, provider := range providers {
		protected, err := fare.NewProtectedProvider(provider, fare.DemoProviderPolicy())
		if err != nil {
			logger.Error("provider.configuration", "error", err.Error())
			os.Exit(1)
		}
		providers[i] = protected
	}
	// This total budget includes rate-limit waits, retries and adapter latency.
	service := fare.NewService(providers, 5*time.Second, fare.WithMaxConcurrentProviders(4))
	var options []api.Option
	routingSource := "approximate"
	switch mode := os.Getenv("FAREWISE_ROUTING_PROVIDER"); mode {
	case "", "approximate":
	case "onemap":
		router, err := routing.NewOneMap(os.Getenv("ONEMAP_TOKEN"))
		if err != nil {
			logger.Error("routing.configuration", "error", err.Error())
			os.Exit(1)
		}
		options = append(options, api.WithRouter(router))
		routingSource = router.Source()
	default:
		logger.Error("routing.configuration", "error", "FAREWISE_ROUTING_PROVIDER must be approximate or onemap")
		os.Exit(1)
	}
	server := &http.Server{
		Addr: addr, Handler: api.NewHandler(service, options...),
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
			logger.Error("server.shutdown", "error", err.Error())
		}
	}()
	logger.Info("server.start", "address", addr, "mode", "demo", "routing_source", routingSource, "max_concurrent_providers", 4)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("server.failure", "error", err.Error())
		os.Exit(1)
	}
}
