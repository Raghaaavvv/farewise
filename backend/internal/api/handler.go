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
	"farewise/backend/internal/observations"
	"farewise/backend/internal/routing"
)

// Option configures an optional dependency when the API handler is created.
type Option func(*handlerConfig)

// handlerConfig holds the services shared by the API endpoints.
// A nil dependency means that its corresponding integration is not configured.
type handlerConfig struct {
	router       routing.Router
	traffic      routing.TrafficEnricher
	observations observations.Repository
}

// WithTraffic adds traffic estimates to a driving route.
func WithTraffic(traffic routing.TrafficEnricher) Option {
	return func(config *handlerConfig) {
		config.traffic = traffic
	}
}

// WithRouter supplies the service used to retrieve driving routes.
func WithRouter(router routing.Router) Option {
	return func(config *handlerConfig) {
		config.router = router
	}
}

// NewHandler registers the API endpoints and applies common response headers.
func NewHandler(service *fare.Service, options ...Option) http.Handler {
	// Apply the supplied options before any endpoint uses the configuration.
	config := handlerConfig{}

	for _, option := range options {
		option(&config)
	}

	// Tell the frontend whether routing uses an integration or an approximation.
	routingSource := "approximate"

	if config.router != nil {
		routingSource = config.router.Source()
	}

	mux := http.NewServeMux()

	// Health checks identify this server as a prototype with simulated fares.
	mux.HandleFunc("GET /api/health", func(writer http.ResponseWriter, request *http.Request) {
		response := map[string]string{
			"status": "ok",
			"mode":   "demo",
		}

		writeJSON(writer, http.StatusOK, response)
	})

	// Return the preset locations used by the pickup and drop-off selectors.
	mux.HandleFunc("GET /api/locations", func(writer http.ResponseWriter, request *http.Request) {
		response := map[string]any{
			"locations":     fare.Locations,
			"source":        "demo catalog",
			"routingSource": routingSource,
		}

		writeJSON(writer, http.StatusOK, response)
	})

	// Device and map coordinates follow the same validation flow. The supplied
	// resolver creates the appropriate location; this endpoint does not save it.
	coordinateEndpoint := func(
		source string,
		resolve func(fare.Coordinates) (fare.Location, error),
	) http.HandlerFunc {
		return func(writer http.ResponseWriter, request *http.Request) {
			// Limit the request size and reject fields outside the coordinate model.
			request.Body = http.MaxBytesReader(writer, request.Body, 1024)

			decoder := json.NewDecoder(request.Body)
			decoder.DisallowUnknownFields()

			var coordinates fare.Coordinates
			decodeError := decoder.Decode(&coordinates)

			if decodeError != nil {
				response := map[string]string{
					"error": "send valid coordinates",
				}

				writeJSON(writer, http.StatusBadRequest, response)
				return
			}

			// A second decode must reach the end of the body. Another JSON value
			// or malformed trailing content would make the request ambiguous.
			trailingContentError := decoder.Decode(new(any))

			if trailingContentError != io.EOF {
				response := map[string]string{
					"error": "send only one coordinate object",
				}

				writeJSON(writer, http.StatusBadRequest, response)
				return
			}

			// The resolver validates the coordinates and constructs a location.
			location, locationError := resolve(coordinates)

			if locationError != nil {
				response := map[string]string{
					"error": locationError.Error(),
				}

				writeJSON(writer, http.StatusBadRequest, response)
				return
			}

			response := map[string]any{
				"location":  location,
				"source":    source,
				"persisted": false,
			}

			writeJSON(writer, http.StatusOK, response)
		}
	}

	mux.HandleFunc("POST /api/locations/current", coordinateEndpoint("device", fare.CurrentLocation))
	mux.HandleFunc("POST /api/locations/map", coordinateEndpoint("map", fare.MapLocation))

	// Comparison flow: decode the request, validate the trip, resolve its route,
	// then ask the fare service to aggregate the simulated provider quotes.
	mux.HandleFunc("POST /api/compare", func(writer http.ResponseWriter, request *http.Request) {
		request.Body = http.MaxBytesReader(writer, request.Body, 4096)

		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()

		var tripRequest fare.Request
		decodeError := decoder.Decode(&tripRequest)

		if decodeError != nil {
			response := map[string]string{
				"error": "send one valid JSON trip object",
			}

			writeJSON(writer, http.StatusBadRequest, response)
			return
		}

		// Reject additional JSON values or malformed content after the trip.
		trailingContentError := decoder.Decode(new(any))

		if trailingContentError != io.EOF {
			response := map[string]string{
				"error": "send only one JSON object",
			}

			writeJSON(writer, http.StatusBadRequest, response)
			return
		}

		// Resolve the requested locations and validate the trip's other fields.
		trip, validationError := fare.Validate(tripRequest)

		if validationError != nil {
			response := map[string]string{
				"error": validationError.Error(),
			}

			writeJSON(writer, http.StatusBadRequest, response)
			return
		}

		// Keep routing and provider work tied to the incoming request so that
		// cancellation can propagate when the client abandons the comparison.
		requestContext := request.Context()
		trip, routeError := config.withRoute(requestContext, trip)

		if routeError != nil {
			// A canceled request no longer needs an HTTP error response.
			if requestContext.Err() == nil {
				routingFailure(writer, routeError)
			}

			return
		}

		comparison := service.Compare(requestContext, trip)

		writeJSON(writer, http.StatusOK, comparison)
	})

	// Observation endpoints are registered by their dedicated handler file.
	config.registerObservations(mux, service)

	// Apply these headers to every response, including errors from the mux.
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("X-Content-Type-Options", "nosniff")

		mux.ServeHTTP(writer, request)
	})
}

// writeJSON sets the response format and status before encoding the payload.
func writeJSON(writer http.ResponseWriter, statusCode int, payload any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(statusCode)

	// The response status has already been sent, so an encoding or write error
	// cannot be replaced with a different HTTP response here.
	_ = json.NewEncoder(writer).Encode(payload)
}

// withRoute attaches a driving route and optional traffic estimate to the trip.
// Both fare comparison and observation capture use this same routing flow.
func (config handlerConfig) withRoute(requestContext context.Context, trip fare.Trip) (fare.Trip, error) {
	// Without a router, keep the distance already calculated by trip validation.
	if config.router == nil {
		return trip, nil
	}

	// Limit the driving-route lookup to three seconds. The parent request can
	// still cancel it sooner, and defer releases the timeout resources on exit.
	routeContext, cancelRoute := context.WithTimeout(requestContext, 3*time.Second)
	defer cancelRoute()

	routeStartedAt := time.Now()

	pickupPoint := routing.Point{
		Latitude:  trip.Pickup.Latitude,
		Longitude: trip.Pickup.Longitude,
	}

	dropoffPoint := routing.Point{
		Latitude:  trip.Dropoff.Latitude,
		Longitude: trip.Dropoff.Longitude,
	}

	route, routeError := config.router.DrivingRoute(routeContext, pickupPoint, dropoffPoint)

	// Record timing and success without logging the user's coordinates.
	slog.Info(
		"routing.result",
		"source", config.router.Source(),
		"latency_ms", time.Since(routeStartedAt).Milliseconds(),
		"success", routeError == nil,
	)

	if routeError != nil {
		return trip, routeError
	}

	if config.traffic != nil {
		// Traffic enrichment uses the parent request context, independently of
		// the driving-route timeout above. Preserve that cancellation boundary.
		route = config.traffic.Enrich(requestContext, route)

		if requestContext.Err() != nil {
			return trip, requestContext.Err()
		}
	}

	// Fare calculations use kilometers; the routing service reports meters.
	trip.Route = route
	trip.DistanceKM = route.DistanceMeters / 1000

	return trip, nil
}

// routingFailure converts a routing error into its public HTTP response.
func routingFailure(writer http.ResponseWriter, routeError error) {
	// Use a generic upstream failure unless the error supplies more detail.
	statusCode := http.StatusBadGateway
	message := "Could not retrieve the driving route. Try again."

	var failure *routing.Error

	if errors.As(routeError, &failure) {
		statusCode = failure.Status
		message = failure.Message

		if failure.RetryAfter > 0 {
			// Retry-After uses whole seconds. Round upward so that the client
			// does not retry before the routing service's requested delay.
			seconds := int64(failure.RetryAfter / time.Second)

			if failure.RetryAfter%time.Second != 0 {
				seconds++
			}

			writer.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
		}
	} else if errors.Is(routeError, context.DeadlineExceeded) {
		statusCode = http.StatusGatewayTimeout
		message = "Driving route lookup timed out. Try again."
	}

	response := map[string]string{
		"error": message,
	}

	writeJSON(writer, statusCode, response)
}
