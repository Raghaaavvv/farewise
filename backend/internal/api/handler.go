package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"farewise/backend/internal/fare"
	"farewise/backend/internal/routing"
)

type Option func(*handlerConfig)
type handlerConfig struct{ router routing.Router }

func WithRouter(router routing.Router) Option { return func(c *handlerConfig) { c.router = router } }

func NewHandler(service *fare.Service, options ...Option) http.Handler {
	config := handlerConfig{}
	for _, option := range options {
		option(&config)
	}
	routingSource := "approximate"
	if config.router != nil {
		routingSource = config.router.Source()
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "mode": "demo"})
	})
	mux.HandleFunc("GET /api/locations", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"locations": fare.Locations, "source": "demo catalog", "routingSource": routingSource})
	})
	mux.HandleFunc("POST /api/locations/current", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1024)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		var coordinates fare.Coordinates
		if err := decoder.Decode(&coordinates); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "send valid device coordinates"})
			return
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "send only one coordinate object"})
			return
		}
		location, err := fare.CurrentLocation(coordinates)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"location": location, "source": "device", "persisted": false})
	})
	mux.HandleFunc("POST /api/compare", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		var request fare.Request
		if err := decoder.Decode(&request); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "send one valid JSON trip object"})
			return
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "send only one JSON object"})
			return
		}
		trip, err := fare.Validate(request)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if config.router != nil {
			ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
			started := time.Now()
			route, routeErr := config.router.DrivingRoute(ctx,
				routing.Point{Latitude: trip.Pickup.Latitude, Longitude: trip.Pickup.Longitude},
				routing.Point{Latitude: trip.Dropoff.Latitude, Longitude: trip.Dropoff.Longitude})
			cancel()
			slog.Info("routing.result", "source", routingSource, "latency_ms", time.Since(started).Milliseconds(), "success", routeErr == nil)
			if routeErr != nil {
				if r.Context().Err() != nil {
					return
				}
				status, message := http.StatusBadGateway, "Could not retrieve the driving route. Try again."
				var failure *routing.Error
				if errors.As(routeErr, &failure) {
					status, message = failure.Status, failure.Message
					if failure.RetryAfter > 0 {
						seconds := int64(failure.RetryAfter / time.Second)
						if failure.RetryAfter%time.Second != 0 {
							seconds++
						}
						w.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
					}
				} else if errors.Is(routeErr, context.DeadlineExceeded) {
					status, message = http.StatusGatewayTimeout, "Driving route lookup timed out. Try again."
				}
				writeJSON(w, status, map[string]string{"error": message})
				return
			}
			trip.Route, trip.DistanceKM = route, route.DistanceMeters/1000
		}
		writeJSON(w, http.StatusOK, service.Compare(r.Context(), trip))
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		mux.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
