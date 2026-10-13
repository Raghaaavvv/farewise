package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"farewise/backend/internal/fare"
)

func TestCurrentLocationEndpoint(t *testing.T) {
	handler := NewHandler(fare.NewService(fare.DemoProviders(), time.Second))
	recorder := httptest.NewRecorder()
	body := `{"latitude":1.36,"longitude":103.989,"accuracyMeters":12}`
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/locations/current", bytes.NewBufferString(body)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Location  fare.Location `json:"location"`
		Source    string        `json:"source"`
		Persisted bool          `json:"persisted"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Source != "device" || response.Persisted || response.Location.Latitude != 1.36 || response.Location.Longitude != 103.989 {
		t.Fatalf("incorrect location response: %+v", response)
	}
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("device response should not be cached")
	}
	if len(fare.Locations) != 9 {
		t.Fatal("device location was added to the shared catalog")
	}

	for _, body := range []string{`{}`, `{"latitude":1.3}`, `{"latitude":91,"longitude":104}`, `{"latitude":1.3,"longitude":181}`, `{"latitude":1.3,"longitude":104,"accuracyMeters":-1}`, `{"latitude":1.3,"longitude":104,"extra":true}`, `{} {}`, `{"latitude":null,"longitude":104}`} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/locations/current", bytes.NewBufferString(body)))
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("accepted invalid body: %s", body)
		}
	}
}

func TestCompareUsesActualDeviceCoordinates(t *testing.T) {
	handler := NewHandler(fare.NewService(fare.DemoProviders(), time.Second))
	body := `{"pickupId":"current-location","pickupCoordinates":{"latitude":1.36,"longitude":103.989},"dropoffId":"orchard","passengers":1,"rideType":"standard","scenario":"normal"}`
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/compare", bytes.NewBufferString(body)))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
	}
	var comparison fare.Comparison
	if err := json.Unmarshal(recorder.Body.Bytes(), &comparison); err != nil {
		t.Fatal(err)
	}
	if comparison.Trip.Pickup.Latitude != 1.36 || comparison.Trip.Pickup.Longitude != 103.989 || len(comparison.Quotes) != 4 {
		t.Fatal("comparison lost device coordinates or provider results")
	}
}
