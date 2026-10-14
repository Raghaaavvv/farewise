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

func WithObservations(repo observations.Repository) Option {
	return func(c *handlerConfig) { c.observations = repo }
}
func decodeObservation(w http.ResponseWriter, r *http.Request, value any) bool {
	if origin := r.Header.Get("Origin"); origin != "" {
		parsed, err := url.Parse(origin)
		if err != nil || parsed.Host == "" || (parsed.Host != r.Host && parsed.Host != r.Header.Get("X-Forwarded-Host")) {
			writeJSON(w, 403, map[string]string{"error": "send changes from the Farewise dashboard"})
			return false
		}
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		writeJSON(w, 415, map[string]string{"error": "send application/json"})
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(value) != nil || decoder.Decode(new(any)) != io.EOF {
		writeJSON(w, 400, map[string]string{"error": "send one valid observation object"})
		return false
	}
	return true
}
func observationFailure(w http.ResponseWriter, err error) {
	status, message := 503, "The observation database is unavailable. Try again after starting PostgreSQL."
	if errors.Is(err, observations.ErrNotFound) {
		status, message = 404, "This saved trip or observation was not found."
	}
	if errors.Is(err, observations.ErrConflict) {
		status, message = 409, "This record changed, or the save ID was reused with different details. Reload before retrying."
	}
	if status == 503 {
		slog.Error("observations.database", "success", false)
	}
	writeJSON(w, status, map[string]string{"error": message})
}
func observationFilter(r *http.Request, max int) (observations.Filter, error) {
	f := observations.Filter{Provider: r.URL.Query().Get("provider"), Search: strings.TrimSpace(r.URL.Query().Get("q")), Limit: 50}
	if f.Provider != "" && f.Provider != "grab" && f.Provider != "gojek" && f.Provider != "zig" && f.Provider != "ryde" {
		return f, errors.New("choose a valid provider filter")
	}
	if len(f.Search) > 120 {
		return f, errors.New("search must be at most 120 characters")
	}
	var err error
	if v := r.URL.Query().Get("limit"); v != "" {
		f.Limit, err = strconv.Atoi(v)
		if err != nil || f.Limit < 1 || f.Limit > max {
			return f, errors.New("invalid page size")
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		f.Offset, err = strconv.Atoi(v)
		if err != nil || f.Offset < 0 || f.Offset > 100000 {
			return f, errors.New("invalid page offset")
		}
	}
	return f, nil
}
func (config handlerConfig) registerObservations(mux *http.ServeMux, service *fare.Service) {
	mux.HandleFunc("GET /api/observations/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"enabled": config.observations != nil, "storage": "postgresql", "mode": "local", "notice": "Only explicitly captured trips and manually entered fares are stored. No ride-provider API is called."})
	})
	ready := func(w http.ResponseWriter) bool {
		if config.observations != nil {
			return true
		}
		writeJSON(w, 503, map[string]string{"error": "Observation storage is not configured. Run bash scripts/db.sh up, then restart Farewise."})
		return false
	}
	mux.HandleFunc("POST /api/observation-sessions", func(w http.ResponseWriter, r *http.Request) {
		if !ready(w) {
			return
		}
		var request observations.CaptureRequest
		if !decodeObservation(w, r, &request) {
			return
		}
		if err := request.Validate(); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		hash := observations.Fingerprint(request)
		existing, existingHash, err := config.observations.FindSession(ctx, request.ID)
		if err == nil {
			if !bytes.Equal(existingHash, hash[:]) {
				observationFailure(w, observations.ErrConflict)
				return
			}
			writeJSON(w, 200, existing)
			return
		}
		if !errors.Is(err, observations.ErrNotFound) {
			observationFailure(w, err)
			return
		}
		trip, _ := fare.Validate(request.Trip)
		trip, err = config.withRoute(ctx, trip)
		if err != nil {
			if r.Context().Err() == nil {
				routingFailure(w, err)
			}
			return
		}
		if request.PickupLabel != "" {
			trip.Pickup.Name = request.PickupLabel
		}
		if request.DropoffLabel != "" {
			trip.Dropoff.Name = request.DropoffLabel
		}
		captured := time.Now().UTC().Truncate(time.Microsecond)
		comparison := service.Compare(ctx, trip)
		snapshot := observations.Snapshot{ID: request.ID, CapturedAt: captured, FeatureVersion: "route-traffic-v1", DemoVersion: "demo-v1", Trip: trip, DemoQuotes: comparison.Quotes, PickupZone: request.PickupZone, DropoffZone: request.DropoffZone}
		saved, err := config.observations.PutSession(ctx, snapshot, hash)
		if err != nil {
			observationFailure(w, err)
			return
		}
		slog.Info("observations.capture", "success", true, "route_source", trip.Route.Source)
		writeJSON(w, 201, saved)
	})
	mux.HandleFunc("GET /api/observation-sessions/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !ready(w) {
			return
		}
		id := r.PathValue("id")
		if !observations.ValidID(id) {
			writeJSON(w, 400, map[string]string{"error": "invalid saved-trip ID"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		snapshot, _, err := config.observations.FindSession(ctx, id)
		if err != nil {
			observationFailure(w, err)
			return
		}
		writeJSON(w, 200, snapshot)
	})
	mux.HandleFunc("POST /api/observations", func(w http.ResponseWriter, r *http.Request) {
		if !ready(w) {
			return
		}
		var request observations.SaveRequest
		if !decodeObservation(w, r, &request) {
			return
		}
		if err := request.Validate(time.Now()); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		saved, created, err := config.observations.Save(ctx, request)
		if err != nil {
			observationFailure(w, err)
			return
		}
		status := 200
		if created {
			status = 201
		}
		slog.Info("observations.save", "success", true, "created", created)
		writeJSON(w, status, saved)
	})
	mux.HandleFunc("PUT /api/observations/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !ready(w) {
			return
		}
		id := r.PathValue("id")
		var request observations.EditRequest
		if !decodeObservation(w, r, &request) {
			return
		}
		if !observations.ValidID(id) || request.Revision < 1 {
			writeJSON(w, 400, map[string]string{"error": "invalid observation ID or revision"})
			return
		}
		if err := request.Fields.Validate(time.Now()); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		saved, err := config.observations.Edit(ctx, id, request)
		if err != nil {
			observationFailure(w, err)
			return
		}
		writeJSON(w, 200, saved)
	})
	mux.HandleFunc("GET /api/observations", func(w http.ResponseWriter, r *http.Request) {
		if !ready(w) {
			return
		}
		filter, err := observationFilter(r, 100)
		if err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		page, err := config.observations.List(ctx, filter)
		if err != nil {
			observationFailure(w, err)
			return
		}
		writeJSON(w, 200, page)
	})
	mux.HandleFunc("GET /api/observations/export", func(w http.ResponseWriter, r *http.Request) {
		if !ready(w) {
			return
		}
		filter, err := observationFilter(r, 100)
		if err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		filter.Limit, filter.Offset = 10000, 0
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		page, err := config.observations.List(ctx, filter)
		if err != nil {
			observationFailure(w, err)
			return
		}
		if page.Total > 10000 {
			writeJSON(w, 422, map[string]string{"error": "export is limited to 10,000 observations; narrow the filters"})
			return
		}
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="farewise-observations.csv"`)
		observations.WriteCSV(w, page.Records)
	})
}
