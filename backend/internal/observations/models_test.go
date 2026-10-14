// Check observation validation, Singapore time buckets and spreadsheet-safe CSV fields.
package observations

import (
	"bytes"
	"encoding/csv"
	"testing"
	"time"

	"farewise/backend/internal/fare"
	"farewise/backend/internal/routing"
)

const testSessionID = "11111111-1111-4111-8111-111111111111"

const testObservationID = "22222222-2222-4222-8222-222222222222"

func testFields() Fields {
	cents := 3700

	return Fields{
		Provider:   "grab",
		Product:    "JustGrab",
		PriceCents: &cents,
		FareKind:   "quoted",
		QuotedAt:   time.Now().UTC().Truncate(time.Microsecond),
		Weather:    "dry",
		Promotion:  "none",
	}
}

func testSnapshot() Snapshot {
	now := time.Now().UTC().Truncate(time.Microsecond)

	duration := 1200.0

	return Snapshot{
		ID:             testSessionID,
		CapturedAt:     now,
		FeatureVersion: "route-traffic-v1",
		DemoVersion:    "demo-v1",
		PickupZone:     "residential",
		DropoffZone:    "retail",
		DemoQuotes:     []fare.Quote{},
		Trip: fare.Trip{
			Pickup:     fare.Locations[0],
			Dropoff:    fare.Locations[3],
			Passengers: 1,
			RideType:   "standard",
			Scenario:   "normal",
			DistanceKM: 20,
			Route: routing.Route{
				Source:          "onemap",
				DistanceMeters:  20000,
				DurationSeconds: &duration,
			},
		},
	}
}

func TestObservationValidationAndHistoricalAlignment(t *testing.T) {
	for _, mutate := range []func(*Fields){
		func(f *Fields) {
			f.PriceCents = nil
		},
		func(f *Fields) {
			n := -1

			f.PriceCents = &n
		},
		func(f *Fields) {
			f.Provider = "fake"
		},
		func(f *Fields) {
			f.Product = " "
		},
		func(f *Fields) {
			f.QuotedAt = time.Now().Add(time.Hour)
		},
		func(f *Fields) {
			f.Weather = "demo-rain"
		},
		func(f *Fields) {
			f.Promotion = ""
		},
		func(f *Fields) {
			n := 4000

			f.IncludedFeesCents = &n
		},
	} {
		fields := testFields()

		mutate(&fields)

		if fields.Validate(time.Now()) == nil {
			t.Fatal("accepted invalid observation")
		}
	}

	r := Record{
		Fields:   testFields(),
		Snapshot: testSnapshot(),
	}

	r.Derive()

	if !r.ContextAligned || !r.TrainingEligible || r.TrafficFreshAtQuote {
		t.Fatal("baseline quality flags incorrect")
	}

	r.QuotedAt = r.Snapshot.CapturedAt.Add(-24 * time.Hour)

	r.Derive()

	if r.ContextAligned || r.TrainingEligible {
		t.Fatal("attached present-day traffic to backdated fare")
	}

	r.QuotedAt = r.Snapshot.CapturedAt

	r.FareKind = "paid"

	r.Derive()

	if r.TrainingEligible {
		t.Fatal("treated paid fare as displayed quote")
	}
}

func TestObservationSingaporeBucketsAndCSV(t *testing.T) {
	r := Record{
		ID:        testObservationID,
		SessionID: testSessionID,
		Fields:    testFields(),
		Snapshot:  testSnapshot(),
		Revision:  1,
	}

	r.QuotedAt = time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)

	r.Snapshot.CapturedAt = r.QuotedAt

	r.Product = "=HYPERLINK(\"bad\")"

	r.Notes = " \t@formula"

	r.Snapshot.Trip.Pickup.Name = "+formula"

	r.Derive()

	if r.TimeSlot != "evening" || r.SingaporeWeekday != 3 {
		t.Fatal("derived weekday/hour outside Singapore")
	}

	var output bytes.Buffer

	writeCSVError := WriteCSV(&output, []Record{r})

	if writeCSVError != nil {
		t.Fatal(writeCSVError)
	}

	rows, err := csv.NewReader(&output).ReadAll()

	if err != nil || len(rows) != 2 || len(rows[0]) != len(rows[1]) {
		t.Fatal("invalid CSV", err)
	}

	fields := map[string]string{}

	for i, h := range rows[0] {
		fields[h] = rows[1][i]
	}

	if fields["product"][0] != '\'' || fields["notes"][0] != '\'' || fields["pickup"][0] != '\'' || fields["price_cents"] != "3700" || fields["observed_at_sgt"] != "2026-10-14T18:00:00+08:00" {
		t.Fatal("unsafe CSV or changed numerical target")
	}
}
