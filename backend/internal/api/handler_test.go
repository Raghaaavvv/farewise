// Exercise comparison responses through the HTTP handler, including invalid trips and provider failures.
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

func TestComparePartialFailureAndOrdering(t *testing.T) {
	handler := NewHandler(fare.NewService(fare.DemoProviders(), time.Second))

	body := `{"pickupId":"changi-t3","dropoffId":"orchard","passengers":2,"rideType":"standard","scenario":"outage"}`

	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/compare", bytes.NewBufferString(body)))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body.String())
	}

	var comparison fare.Comparison

	unmarshalError := json.Unmarshal(recorder.Body.Bytes(), &comparison)

	if unmarshalError != nil {
		t.Fatal(unmarshalError)
	}

	providers := make(map[string]bool)

	for _, quote := range comparison.Quotes {
		providers[quote.ProviderID] = true
	}

	if comparison.Mode != "demo" || len(providers) != 4 || len(comparison.Quotes) != 6 {
		t.Fatal("missing demo flag or provider results")
	}

	last := comparison.Quotes[len(comparison.Quotes)-1]

	if last.ProviderID != "ryde" || last.Status != "unavailable" || last.Fare != nil {
		t.Fatal("failed provider must not look like a free ride")
	}

	for i := 1; i < len(comparison.Quotes)-1; i++ {
		if comparison.Quotes[i-1].Fare.MinFare > comparison.Quotes[i].Fare.MinFare {
			t.Fatal("prices are not sorted")
		}
	}
}

func TestInvalidTrips(t *testing.T) {
	handler := NewHandler(fare.NewService(nil, time.Second))

	for _, body := range []string{
		`{"pickupId":"orchard","dropoffId":"orchard","passengers":1,"rideType":"standard","scenario":"normal"}`,
		`{"pickupId":"changi-t3","dropoffId":"orchard","passengers":5,"rideType":"standard","scenario":"normal"}`,
		`{"pickupId":"unknown","dropoffId":"orchard","passengers":1,"rideType":"standard","scenario":"normal"}`,
		`{"pickupId":"changi-t3","dropoffId":"orchard","passengers":1,"rideType":"standard","scenario":"normal","unexpected":true}`,
		`{} {}`,
	} {
		recorder := httptest.NewRecorder()

		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/compare", bytes.NewBufferString(body)))

		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("accepted invalid trip: %s", body)
		}
	}
}
