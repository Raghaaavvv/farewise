// Use fake routing and traffic adapters to verify what the HTTP layer passes to fare providers.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"farewise/backend/internal/fare"
	"farewise/backend/internal/routing"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type fakeRouter struct {
	calls       int
	origin      routing.Point
	destination routing.Point
	route       routing.Route
	err         error
}

func (*fakeRouter) Source() string {
	return "onemap"
}

func (f *fakeRouter) DrivingRoute(ctx context.Context, a, b routing.Point) (routing.Route, error) {
	f.calls++

	f.origin = a
	f.destination = b

	return f.route, f.err
}

type distanceProvider struct {
	trip  fare.Trip
	calls int
	id    string
}

func (p *distanceProvider) ID() string {
	return p.id
}

func (p *distanceProvider) Name() string {
	return p.id
}

func (p *distanceProvider) Estimate(ctx context.Context, trip fare.Trip) ([]fare.Quote, error) {
	p.trip = trip

	p.calls++

	return nil, errors.New("test-only result")
}

const deviceTrip = `{"pickupId":"current-location","pickupCoordinates":{"latitude":1.357,"longitude":103.987},"dropoffId":"current-location","dropoffCoordinates":{"latitude":1.304,"longitude":103.832},"passengers":1,"rideType":"standard","scenario":"normal"}`

func TestRoadDistanceFlowsToEveryProvider(t *testing.T) {
	seconds := 1234.5

	router := &fakeRouter{route: routing.Route{
		Source:          "onemap",
		DistanceMeters:  23456,
		DurationSeconds: &seconds,
	}}

	providers := []*distanceProvider{
		{id: "a"},
		{id: "b"},
		{id: "c"},
		{id: "d"},
	}

	adapters := make([]fare.Provider, len(providers))

	for i, p := range providers {
		adapters[i] = p
	}

	handler := NewHandler(fare.NewService(adapters, time.Second), WithRouter(router))

	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, httptest.NewRequest("POST", "/api/compare", bytes.NewBufferString(deviceTrip)))

	if recorder.Code != 200 {
		t.Fatalf("response: %d %s", recorder.Code, recorder.Body.String())
	}

	var comparison fare.Comparison

	unmarshalError := json.Unmarshal(recorder.Body.Bytes(), &comparison)

	if unmarshalError != nil {
		t.Fatal(unmarshalError)
	}

	if router.calls != 1 || router.origin.Latitude != 1.357 || router.destination.Longitude != 103.832 || comparison.Trip.DistanceKM != 23.456 || comparison.Trip.Route.Source != "onemap" {
		t.Fatal("routing lost coordinates/distance")
	}

	for _, p := range providers {
		if p.calls != 1 || p.trip.DistanceKM != 23.456 || *p.trip.Route.DurationSeconds != seconds {
			t.Fatal("provider did not receive road distance")
		}
	}

	catalog := httptest.NewRecorder()

	handler.ServeHTTP(catalog, httptest.NewRequest("GET", "/api/locations", nil))

	var data struct {
		RoutingSource string `json:"routingSource"`
	}

	json.Unmarshal(catalog.Body.Bytes(), &data)

	if data.RoutingSource != "onemap" {
		t.Fatal("frontend cannot disclose routing service")
	}
}

func TestRoutingFailureDoesNotUseApproximateFare(t *testing.T) {
	for _, err := range []error{
		&routing.Error{
			Status:     503,
			Message:    "Renew token",
			RetryAfter: 2 * time.Second,
		},
		context.DeadlineExceeded,
		errors.New("secret request URL"),
	} {
		router := &fakeRouter{err: err}

		provider := &distanceProvider{id: "a"}

		handler := NewHandler(fare.NewService([]fare.Provider{provider}, time.Second), WithRouter(router))

		recorder := httptest.NewRecorder()

		handler.ServeHTTP(recorder, httptest.NewRequest("POST", "/api/compare", bytes.NewBufferString(deviceTrip)))

		want := 502

		if errors.Is(err, context.DeadlineExceeded) {
			want = 504
		}

		var failure *routing.Error

		if errors.As(err, &failure) {
			want = failure.Status
		}

		if recorder.Code != want || provider.calls != 0 || bytes.Contains(recorder.Body.Bytes(), []byte("secret")) {
			t.Fatalf("unsafe fallback: %d %s", recorder.Code, recorder.Body.String())
		}

		if failure != nil && recorder.Header().Get("Retry-After") != "2" {
			t.Fatal("lost cooldown header")
		}
	}
}

func TestInvalidTripsAndLocationCaptureDoNotCallRouter(t *testing.T) {
	router := &fakeRouter{}

	handler := NewHandler(fare.NewService(nil, time.Second), WithRouter(router))

	for _, body := range []string{
		`{}`,
		`{"pickupId":"orchard","dropoffId":"orchard","passengers":1,"rideType":"standard","scenario":"normal"}`,
		`{"pickupId":"current-location","dropoffId":"orchard","passengers":1,"rideType":"standard","scenario":"normal"}`,
	} {
		recorder := httptest.NewRecorder()

		handler.ServeHTTP(recorder, httptest.NewRequest("POST", "/api/compare", bytes.NewBufferString(body)))

		if recorder.Code != http.StatusBadRequest {
			t.Fatal("accepted invalid trip")
		}
	}

	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, httptest.NewRequest("POST", "/api/locations/current", bytes.NewBufferString(`{"latitude":1.3,"longitude":103.8}`)))

	if recorder.Code != 200 || router.calls != 0 {
		t.Fatal("location capture contacted router")
	}
}

type fakeTraffic struct{ calls int }

func (f *fakeTraffic) Enrich(ctx context.Context, route routing.Route) routing.Route {
	f.calls++

	baseline := *route.DurationSeconds

	adjusted := 1500.0
	min := 1400.0
	max := 1700.0

	route.DurationSeconds = &adjusted

	route.TrafficAware = true

	route.Traffic = &routing.TrafficEstimate{
		Source:           "lta",
		Status:           "applied",
		BaselineSeconds:  baseline,
		EstimatedSeconds: &adjusted,
		MinSeconds:       &min,
		MaxSeconds:       &max,
		Coverage:         .5,
		MatchedMeters:    route.DistanceMeters * .5,
		MatchedLinks:     1,
	}

	return route
}

func TestTrafficAdjustedTimeFlowsThroughComparison(t *testing.T) {
	seconds := 1200.0

	router := &fakeRouter{route: routing.Route{
		Source:          "onemap",
		DistanceMeters:  10000,
		DurationSeconds: &seconds,
	}}

	traffic := &fakeTraffic{}

	provider := &distanceProvider{id: "a"}

	handler := NewHandler(fare.NewService([]fare.Provider{provider}, time.Second), WithRouter(router), WithTraffic(traffic))

	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, httptest.NewRequest("POST", "/api/compare", bytes.NewBufferString(deviceTrip)))

	var result fare.Comparison

	unmarshalError := json.Unmarshal(recorder.Body.Bytes(), &result)

	if unmarshalError != nil {
		t.Fatal(unmarshalError)
	}

	if recorder.Code != 200 || traffic.calls != 1 || provider.calls != 1 || *provider.trip.Route.DurationSeconds != 1500 || result.Trip.Route.Traffic.BaselineSeconds != 1200 || !result.Trip.Route.TrafficAware {
		t.Fatal("traffic result lost at API/provider boundary")
	}

	if bytes.Contains(recorder.Body.Bytes(), []byte("Geometry")) {
		t.Fatal("internal route geometry leaked")
	}
}
