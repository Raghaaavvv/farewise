// HTTP endpoints for capturing immutable trip conditions and recording manual fares.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"farewise/backend/internal/fare"
	"farewise/backend/internal/observations"
)

// WithObservations supplies the repository used by the observation endpoints.
func WithObservations(repository observations.Repository) Option {
	return func(c *handlerConfig) {
		c.observations = repository
	}
}

// decodeObservation checks the request origin and JSON format before decoding one bounded payload.
func decodeObservation(writer http.ResponseWriter, httpRequest *http.Request, value any) bool {
	// Only accept browser changes from this app's origin; CLI requests may omit Origin.
	if origin := httpRequest.Header.Get("Origin"); origin != "" {
		parsed, err := url.Parse(origin)

		if err != nil || parsed.Host == "" || (parsed.Host != httpRequest.Host && parsed.Host != httpRequest.Header.Get("X-Forwarded-Host")) {
			writeJSON(writer, 403, map[string]string{
				"error": "send changes from the Farewise dashboard",
			})

			return false
		}
	}

	// Require JSON and cap the body before attempting to decode user input.
	media, _, err := mime.ParseMediaType(httpRequest.Header.Get("Content-Type"))

	if err != nil || media != "application/json" {
		writeJSON(writer, 415, map[string]string{
			"error": "send application/json",
		})

		return false
	}

	httpRequest.Body = http.MaxBytesReader(writer, httpRequest.Body, 8192)

	decoder := json.NewDecoder(httpRequest.Body)

	decoder.DisallowUnknownFields()

	// Decode one payload before checking whether anything follows it.
	decodeError := decoder.Decode(value)

	if decodeError != nil {
		writeJSON(writer, 400, map[string]string{
			"error": "send one valid observation object",
		})

		return false
	}

	// Preserve the same error response for a second JSON value or invalid trailing content.
	trailingContentError := decoder.Decode(new(any))

	if trailingContentError != io.EOF {
		writeJSON(writer, 400, map[string]string{
			"error": "send one valid observation object",
		})
		return false
	}

	return true
}

// observationFailure maps repository errors to public responses without exposing database details.
func observationFailure(writer http.ResponseWriter, err error) {
	status := 503
	message := "The observation database is unavailable. Try again after starting PostgreSQL."

	if errors.Is(err, observations.ErrNotFound) {
		status = 404
		message = "This saved trip or observation was not found."
	}

	if errors.Is(err, observations.ErrConflict) {
		status = 409
		message = "This record changed, or the save ID was reused with different details. Reload before retrying."
	}

	if status == 503 {
		slog.Error("observations.database", "success", false)
	}

	writeJSON(writer, status, map[string]string{
		"error": message,
	})
}

// observationFilter validates search and pagination parameters before a database query.
func observationFilter(httpRequest *http.Request, max int) (observations.Filter, error) {
	filter := observations.Filter{
		Provider: httpRequest.URL.Query().Get("provider"),
		Search:   strings.TrimSpace(httpRequest.URL.Query().Get("q")),
		Limit:    50,
	}

	if filter.Provider != "" && filter.Provider != "grab" && filter.Provider != "gojek" && filter.Provider != "zig" && filter.Provider != "ryde" {
		return filter, errors.New("choose a valid provider filter")
	}

	if len(filter.Search) > 120 {
		return filter, errors.New("search must be at most 120 characters")
	}

	var err error

	if v := httpRequest.URL.Query().Get("limit"); v != "" {
		filter.Limit, err = strconv.Atoi(v)

		if err != nil || filter.Limit < 1 || filter.Limit > max {
			return filter, errors.New("invalid page size")
		}
	}

	if v := httpRequest.URL.Query().Get("offset"); v != "" {
		filter.Offset, err = strconv.Atoi(v)

		if err != nil || filter.Offset < 0 || filter.Offset > 100000 {
			return filter, errors.New("invalid page offset")
		}
	}

	return filter, nil
}

// registerObservations registers capture, manual entry, correction, listing and export endpoints.
func (config handlerConfig) registerObservations(mux *http.ServeMux, service *fare.Service) {
	mux.HandleFunc("GET /api/observations/status", func(writer http.ResponseWriter, httpRequest *http.Request) {
		writeJSON(
			writer,
			200,
			map[string]any{
				"enabled": config.observations != nil,
				"storage": "postgresql",
				"mode":    "local",
				"notice":  "Only explicitly captured trips and manually entered fares are stored. No ride-provider API is called.",
			},
		)
	})

	// Comparisons remain usable without a database, but logger endpoints require storage.
	ready := func(writer http.ResponseWriter) bool {
		if config.observations != nil {
			return true
		}

		writeJSON(
			writer,
			503,
			map[string]string{
				"error": "Observation storage is not configured. Run bash scripts/db.sh up, then restart Farewise.",
			},
		)

		return false
	}

	// Capture: validate the trip, recognize a retry, then save its current route and features.
	mux.HandleFunc("POST /api/observation-sessions", func(writer http.ResponseWriter, httpRequest *http.Request) {
		if !ready(writer) {
			return
		}

		var request observations.CaptureRequest

		if !decodeObservation(writer, httpRequest, &request) {
			return
		}

		validateError := request.Validate()

		if validateError != nil {
			writeJSON(writer, 400, map[string]string{
				"error": validateError.Error(),
			})

			return
		}

		requestContext, cancel := context.WithTimeout(httpRequest.Context(), 10*time.Second)

		defer cancel()

		hash := observations.Fingerprint(request)

		// Check the saved fingerprint first so retries do not repeat upstream routing work.
		existing, existingHash, err := config.observations.FindSession(requestContext, request.ID)

		if err == nil {
			if !bytes.Equal(existingHash, hash[:]) {
				observationFailure(writer, observations.ErrConflict)

				return
			}

			writeJSON(writer, 200, existing)

			return
		}

		if !errors.Is(err, observations.ErrNotFound) {
			observationFailure(writer, err)

			return
		}

		// CaptureRequest.Validate already checked this trip before routing begins.
		trip, _ := fare.Validate(request.Trip)

		trip, err = config.withRoute(requestContext, trip)

		if err != nil {
			if httpRequest.Context().Err() == nil {
				routingFailure(writer, err)
			}

			return
		}

		if request.PickupLabel != "" {
			trip.Pickup.Name = request.PickupLabel
		}

		if request.DropoffLabel != "" {
			trip.Dropoff.Name = request.DropoffLabel
		}

		// Keep the actual observation separate from these simulated reference products.
		captured := time.Now().UTC().Truncate(time.Microsecond)

		comparison := service.Compare(requestContext, trip)

		snapshot := observations.Snapshot{
			ID:             request.ID,
			CapturedAt:     captured,
			FeatureVersion: "route-traffic-v1",
			DemoVersion:    "demo-v1",
			Trip:           trip,
			DemoQuotes:     comparison.Quotes,
			PickupZone:     request.PickupZone,
			DropoffZone:    request.DropoffZone,
		}

		saved, err := config.observations.PutSession(requestContext, snapshot, hash)

		if err != nil {
			observationFailure(writer, err)

			return
		}

		slog.Info("observations.capture", "success", true, "route_source", trip.Route.Source)

		writeJSON(writer, 201, saved)
	})

	mux.HandleFunc("GET /api/observation-sessions/{id}", func(writer http.ResponseWriter, httpRequest *http.Request) {
		if !ready(writer) {
			return
		}

		id := httpRequest.PathValue("id")

		if !observations.ValidID(id) {
			writeJSON(writer, 400, map[string]string{
				"error": "invalid saved-trip ID",
			})

			return
		}

		requestContext, cancel := context.WithTimeout(httpRequest.Context(), 3*time.Second)

		defer cancel()

		snapshot, _, err := config.observations.FindSession(requestContext, id)

		if err != nil {
			observationFailure(writer, err)

			return
		}

		writeJSON(writer, 200, snapshot)
	})

	// Save: the client supplies a stable identifier so a retry cannot create a duplicate fare.
	mux.HandleFunc("POST /api/observations", func(writer http.ResponseWriter, httpRequest *http.Request) {
		if !ready(writer) {
			return
		}

		var request observations.SaveRequest

		if !decodeObservation(writer, httpRequest, &request) {
			return
		}

		saveValidationError := request.Validate(time.Now())

		if saveValidationError != nil {
			writeJSON(writer, 400, map[string]string{
				"error": saveValidationError.Error(),
			})

			return
		}

		requestContext, cancel := context.WithTimeout(httpRequest.Context(), 3*time.Second)

		defer cancel()

		saved, created, err := config.observations.Save(requestContext, request)

		if err != nil {
			observationFailure(writer, err)

			return
		}

		status := 200

		if created {
			status = 201
		}

		slog.Info("observations.save", "success", true, "created", created)

		writeJSON(writer, status, saved)
	})

	// Correct only manual fields; the saved trip conditions and session linkage stay immutable.
	mux.HandleFunc("PUT /api/observations/{id}", func(writer http.ResponseWriter, httpRequest *http.Request) {
		if !ready(writer) {
			return
		}

		id := httpRequest.PathValue("id")

		var request observations.EditRequest

		if !decodeObservation(writer, httpRequest, &request) {
			return
		}

		if !observations.ValidID(id) || request.Revision < 1 {
			writeJSON(writer, 400, map[string]string{
				"error": "invalid observation ID or revision",
			})

			return
		}

		correctionValidationError := request.Fields.Validate(time.Now())

		if correctionValidationError != nil {
			writeJSON(writer, 400, map[string]string{
				"error": correctionValidationError.Error(),
			})

			return
		}

		requestContext, cancel := context.WithTimeout(httpRequest.Context(), 3*time.Second)

		defer cancel()

		saved, err := config.observations.Edit(requestContext, id, request)

		if err != nil {
			observationFailure(writer, err)

			return
		}

		writeJSON(writer, 200, saved)
	})

	// Read a filtered page while also returning collection-wide coverage summaries.
	mux.HandleFunc("GET /api/observations", func(writer http.ResponseWriter, httpRequest *http.Request) {
		if !ready(writer) {
			return
		}

		filter, err := observationFilter(httpRequest, 100)

		if err != nil {
			writeJSON(writer, 400, map[string]string{
				"error": err.Error(),
			})

			return
		}

		requestContext, cancel := context.WithTimeout(httpRequest.Context(), 5*time.Second)

		defer cancel()

		page, err := config.observations.List(requestContext, filter)

		if err != nil {
			observationFailure(writer, err)

			return
		}

		writeJSON(writer, 200, page)
	})

	// Export a bounded dataset; oversized exports ask the user to narrow the filters.
	mux.HandleFunc("GET /api/observations/export", func(writer http.ResponseWriter, httpRequest *http.Request) {
		if !ready(writer) {
			return
		}

		filter, err := observationFilter(httpRequest, 100)

		if err != nil {
			writeJSON(writer, 400, map[string]string{
				"error": err.Error(),
			})

			return
		}

		filter.Limit = 10000
		filter.Offset = 0

		requestContext, cancel := context.WithTimeout(httpRequest.Context(), 10*time.Second)

		defer cancel()

		page, err := config.observations.List(requestContext, filter)

		if err != nil {
			observationFailure(writer, err)

			return
		}

		if page.Total > 10000 {
			writeJSON(writer, 422, map[string]string{
				"error": "export is limited to 10,000 observations; narrow the filters",
			})

			return
		}

		writer.Header().Set("Content-Type", "text/csv; charset=utf-8")

		writer.Header().Set("Content-Disposition", `attachment; filename="farewise-observations.csv"`)

		observations.WriteCSV(writer, page.Records)
	})
}
