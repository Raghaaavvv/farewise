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
	if err := json.Unmarshal(recorder.Body.Bytes(), &comparison); err != nil {
		t.Fatal(err)
	}
	if comparison.Mode != "demo" || len(comparison.Quotes) != 4 {
		t.Fatal("missing demo flag or provider results")
	}
	last := comparison.Quotes[3]
	if last.ProviderID != "ryde" || last.Status != "unavailable" || last.AmountCents != nil {
		t.Fatal("failed provider must not look like a free ride")
	}
	for i := 1; i < 3; i++ {
		if *comparison.Quotes[i-1].AmountCents > *comparison.Quotes[i].AmountCents {
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
