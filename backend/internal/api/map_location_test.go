package api

import (
	"bytes"
	"encoding/json"
	"farewise/backend/internal/fare"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMapLocationEndpointAndComparison(t *testing.T) {
	router := &fakeRouter{}
	handler := NewHandler(fare.NewService(nil, time.Second), WithRouter(router))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest("POST", "/api/locations/map", bytes.NewBufferString(`{"latitude":1.303452345,"longitude":103.83512345}`)))
	if recorder.Code != 200 {
		t.Fatalf("status=%d, body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Location  fare.Location `json:"location"`
		Source    string        `json:"source"`
		Persisted bool          `json:"persisted"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Source != "map" || response.Persisted || response.Location.ID != "map-location" || response.Location.Latitude != 1.303452345 || response.Location.Longitude != 103.83512345 {
		t.Fatal("map point was mislabeled or rounded")
	}
	if router.calls != 0 || recorder.Header().Get("Cache-Control") != "no-store" || len(fare.Locations) != 9 {
		t.Fatal("map confirmation routed, persisted, or changed the catalog")
	}

	offline := NewHandler(fare.NewService(nil, time.Second))
	body := `{"pickupId":"changi-t3","dropoffId":"map-location","dropoffCoordinates":{"latitude":1.303452345,"longitude":103.83512345},"passengers":1,"rideType":"standard","scenario":"normal"}`
	recorder = httptest.NewRecorder()
	offline.ServeHTTP(recorder, httptest.NewRequest("POST", "/api/compare", bytes.NewBufferString(body)))
	if recorder.Code != 200 {
		t.Fatalf("map trip rejected: %s", recorder.Body.String())
	}
	var comparison fare.Comparison
	if err := json.Unmarshal(recorder.Body.Bytes(), &comparison); err != nil {
		t.Fatal(err)
	}
	if comparison.Trip.Dropoff.ID != "map-location" || comparison.Trip.Dropoff.Latitude != response.Location.Latitude || comparison.Trip.DistanceKM <= 0 {
		t.Fatal("comparison lost map point")
	}
}

func TestInvalidMapLocationBodies(t *testing.T) {
	handler := NewHandler(fare.NewService(nil, time.Second))
	for _, body := range []string{`{}`, `{"latitude":1.3}`, `{"latitude":null,"longitude":103.8}`, `{"latitude":91,"longitude":103.8}`, `{"latitude":0,"longitude":0}`, `{"latitude":1.3,"longitude":103.8,"name":"Injected"}`, `{"latitude":1.3,"longitude":103.8} {}`} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest("POST", "/api/locations/map", bytes.NewBufferString(body)))
		if recorder.Code != 400 {
			t.Fatalf("accepted invalid map input: %s", body)
		}
	}
}
