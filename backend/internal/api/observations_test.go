// Use an in-memory repository to check capture replay, input validation and safe API errors.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"farewise/backend/internal/fare"
	"farewise/backend/internal/observations"
	"farewise/backend/internal/routing"
)

type memoryObservations struct {
	snapshot observations.Snapshot
	hash     [32]byte
	saves    int
}

func (m *memoryObservations) FindSession(ctx context.Context, id string) (observations.Snapshot, []byte, error) {
	if m.snapshot.ID != id {
		return observations.Snapshot{}, nil, observations.ErrNotFound
	}

	return m.snapshot, m.hash[:], nil
}

func (m *memoryObservations) PutSession(ctx context.Context, s observations.Snapshot, h [32]byte) (observations.Snapshot, error) {
	m.snapshot = s
	m.hash = h

	return s, nil
}

func (m *memoryObservations) Save(ctx context.Context, r observations.SaveRequest) (observations.Record, bool, error) {
	m.saves++

	record := observations.Record{
		ID:        r.ID,
		SessionID: r.SessionID,
		Fields:    r.Fields,
		Snapshot:  m.snapshot,
		Revision:  1,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}

	record.Derive()

	return record, true, nil
}

func (m *memoryObservations) Edit(context.Context, string, observations.EditRequest) (observations.Record, error) {
	return observations.Record{}, errors.New("private database password")
}

func (m *memoryObservations) List(context.Context, observations.Filter) (observations.Page, error) {
	return observations.Page{}, errors.New("private database password")
}

const captureBody = `{"id":"11111111-1111-4111-8111-111111111111","trip":{"pickupId":"changi-t3","dropoffId":"orchard","passengers":1,"rideType":"standard","scenario":"normal"},"pickupLabel":"Test pickup","dropoffLabel":"Test destination","pickupZone":"airport","dropoffZone":"retail"}`

func TestObservationCaptureUsesBackendRouteAndIdempotentReplay(t *testing.T) {
	seconds := 1200.0

	router := &fakeRouter{route: routing.Route{
		Source:          "onemap",
		DistanceMeters:  20000,
		DurationSeconds: &seconds,
	}}

	repo := &memoryObservations{}

	handler := NewHandler(fare.NewService(fare.DemoProviders(), time.Second), WithRouter(router), WithObservations(repo))

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("POST", "/api/observation-sessions", bytes.NewBufferString(captureBody))

		req.Header.Set("Content-Type", "application/json")

		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		want := 201

		if i == 1 {
			want = 200
		}

		if rec.Code != want {
			t.Fatal(rec.Code, rec.Body.String())
		}
	}

	if router.calls != 1 || repo.snapshot.Trip.DistanceKM != 20 || repo.snapshot.Trip.Pickup.Name != "Test pickup" || repo.snapshot.FeatureVersion != "route-traffic-v1" || len(repo.snapshot.DemoQuotes) != 7 {
		t.Fatal("capture bypassed backend features or replay refired provider calls")
	}
}

func TestObservationRejectsInvalidAndCrossOriginInputWithoutSaving(t *testing.T) {
	repo := &memoryObservations{}

	handler := NewHandler(fare.NewService(nil, time.Second), WithObservations(repo))

	for _, tc := range []struct {
		body   string
		origin string
		kind   string
		status int
	}{
		{
			`{}`,
			"",
			"application/json",
			400,
		},
		{
			captureBody,
			"https://unrelated.example",
			"application/json",
			403,
		},
		{
			captureBody,
			"",
			"text/plain",
			415,
		},
		{
			captureBody + ` {}`,
			"",
			"application/json",
			400,
		},
	} {
		req := httptest.NewRequest("POST", "/api/observation-sessions", bytes.NewBufferString(tc.body))

		req.Header.Set("Content-Type", tc.kind)

		req.Header.Set("Origin", tc.origin)

		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != tc.status || repo.snapshot.ID != "" {
			t.Fatal("unsafe capture", rec.Code)
		}
	}

	invalid := `{"id":"22222222-2222-4222-8222-222222222222","sessionId":"11111111-1111-4111-8111-111111111111","provider":"grab","product":"JustGrab","priceCents":37.5}`

	req := httptest.NewRequest("POST", "/api/observations", bytes.NewBufferString(invalid))

	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != 400 || repo.saves != 0 {
		t.Fatal("accepted fractional cents")
	}
}

func TestObservationStorageIsOptionalAndErrorsAreSanitized(t *testing.T) {
	disabled := NewHandler(fare.NewService(nil, time.Second))

	rec := httptest.NewRecorder()

	disabled.ServeHTTP(rec, httptest.NewRequest("GET", "/api/observations/status", nil))

	var status struct{ Enabled bool }

	json.Unmarshal(rec.Body.Bytes(), &status)

	if status.Enabled {
		t.Fatal("claimed database was configured")
	}

	repo := &memoryObservations{}

	handler := NewHandler(fare.NewService(nil, time.Second), WithObservations(repo))

	rec = httptest.NewRecorder()

	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/api/observations", nil))

	if rec.Code != 503 || bytes.Contains(rec.Body.Bytes(), []byte("password")) {
		t.Fatal("database error leaked credentials")
	}
}
