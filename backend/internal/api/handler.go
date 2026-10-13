package api

import (
	"encoding/json"
	"io"
	"net/http"

	"farewise/backend/internal/fare"
)

func NewHandler(service *fare.Service) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "mode": "demo"})
	})
	mux.HandleFunc("GET /api/locations", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"locations": fare.Locations, "source": "demo catalog"})
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
