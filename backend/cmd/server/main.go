// Configure dependencies, start the HTTP server and stop background work on shutdown.
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
	"farewise/backend/internal/observations"
	"farewise/backend/internal/routing"
)

// main configures the selected integrations and owns the server shutdown sequence.
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	defer stop()

	var options []api.Option

	if databaseURL := os.Getenv("DATABASE_URL"); databaseURL != "" {
		setup, cancel := context.WithTimeout(ctx, 10*time.Second)

		repo, err := observations.Open(setup, databaseURL)

		cancel()

		if err != nil {
			logger.Error("observations.configuration", "error", err.Error())

			os.Exit(1)
		}

		defer repo.Close()

		options = append(options, api.WithObservations(repo))
	}

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

	trafficMode := os.Getenv("FAREWISE_TRAFFIC_PROVIDER")

	if trafficMode == "" && os.Getenv("LTA_ACCOUNT_KEY") != "" {
		trafficMode = "lta"
	}

	switch trafficMode {
	case "", "off":

	case "lta":
		if routingSource != "onemap" {
			logger.Error("traffic.configuration", "error", "LTA traffic requires OneMap routing")

			os.Exit(1)
		}
		traffic, err := routing.NewLTA(os.Getenv("LTA_ACCOUNT_KEY"))
		if err != nil {
			logger.Error("traffic.configuration", "error", err.Error())

			os.Exit(1)
		}
		options = append(options, api.WithTraffic(traffic))
		go traffic.Run(ctx)

	default:
		logger.Error("traffic.configuration", "error", "FAREWISE_TRAFFIC_PROVIDER must be off or lta")
		os.Exit(1)
	}

	server := &http.Server{
		Addr:              addr,
		Handler:           api.NewHandler(service, options...),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		<-ctx.Done()

		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)

		defer cancel()

		shutdownError := server.Shutdown(shutdown)

		if shutdownError != nil {
			logger.Error("server.shutdown", "error", shutdownError.Error())
		}
	}()

	logger.Info(
		"server.start",
		"address",
		addr,
		"mode",
		"demo",
		"routing_source",
		routingSource,
		"traffic_source",
		trafficMode,
		"max_concurrent_providers",
		4,
	)

	listenAndServeError := server.ListenAndServe()

	if listenAndServeError != nil && listenAndServeError != http.ErrServerClosed {
		logger.Error("server.failure", "error", listenAndServeError.Error())

		os.Exit(1)
	}
}
