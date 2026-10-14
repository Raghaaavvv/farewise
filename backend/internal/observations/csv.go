package observations

import (
	"encoding/csv"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Spreadsheet applications interpret formulas even inside quoted CSV cells.
func safeCell(value string) string {
	trimmed := strings.TrimLeftFunc(value, unicode.IsSpace)
	if trimmed != "" && strings.ContainsRune("=+-@", rune(trimmed[0])) {
		return "'" + value
	}
	return value
}
func optional(value *float64) string {
	if value == nil {
		return ""
	}
	return strconv.FormatFloat(*value, 'f', -1, 64)
}
func stamp(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}
func WriteCSV(w io.Writer, records []Record) error {
	writer := csv.NewWriter(w)
	header := []string{"observation_id", "session_id", "provider", "product", "price_cents", "fare_kind", "observed_at_utc", "observed_at_sgt", "sg_weekday", "time_slot", "captured_at_utc", "pickup", "dropoff", "pickup_latitude", "pickup_longitude", "dropoff_latitude", "dropoff_longitude", "pickup_zone", "dropoff_zone", "passengers", "ride_type", "distance_meters", "route_source", "driving_seconds", "baseline_seconds", "traffic_status", "traffic_coverage", "traffic_observed_at", "traffic_expires_at", "weather", "promotion", "included_fees_cents", "context_aligned", "traffic_fresh_at_quote", "training_eligible", "feature_version", "demo_version", "demo_min_cents", "demo_max_cents", "demo_scenario", "revision", "notes"}
	if err := writer.Write(header); err != nil {
		return err
	}
	for _, r := range records {
		r.Derive()
		s := r.Snapshot
		t := s.Trip
		route := t.Route
		baseline, status, coverage, observed, expires := "", "off", "", "", ""
		if route.DurationSeconds != nil {
			baseline = optional(route.DurationSeconds)
		}
		if info := route.Traffic; info != nil {
			baseline = strconv.FormatFloat(info.BaselineSeconds, 'f', -1, 64)
			status = info.Status
			coverage = strconv.FormatFloat(info.Coverage, 'f', -1, 64)
			observed = stamp(info.ObservedAt)
			expires = stamp(info.ExpiresAt)
		}
		fees := ""
		if r.IncludedFeesCents != nil {
			fees = strconv.Itoa(*r.IncludedFeesCents)
		}
		demoMin, demoMax := "", ""
		for _, q := range s.DemoQuotes {
			if q.ProviderID == r.Provider && q.ProductID == t.RideType && q.Fare != nil {
				demoMin = strconv.Itoa(q.Fare.MinFare)
				demoMax = strconv.Itoa(q.Fare.MaxFare)
				break
			}
		}
		number := func(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
		row := []string{r.ID, r.SessionID, r.Provider, safeCell(r.Product), strconv.Itoa(*r.PriceCents), r.FareKind, r.QuotedAt.UTC().Format(time.RFC3339Nano), r.QuotedAt.In(singapore).Format(time.RFC3339Nano), strconv.Itoa(r.SingaporeWeekday), r.TimeSlot, s.CapturedAt.UTC().Format(time.RFC3339Nano), safeCell(t.Pickup.Name), safeCell(t.Dropoff.Name), number(t.Pickup.Latitude), number(t.Pickup.Longitude), number(t.Dropoff.Latitude), number(t.Dropoff.Longitude), s.PickupZone, s.DropoffZone, strconv.Itoa(t.Passengers), t.RideType, number(route.DistanceMeters), route.Source, optional(route.DurationSeconds), baseline, status, coverage, observed, expires, r.Weather, r.Promotion, fees, strconv.FormatBool(r.ContextAligned), strconv.FormatBool(r.TrafficFreshAtQuote), strconv.FormatBool(r.TrainingEligible), s.FeatureVersion, s.DemoVersion, demoMin, demoMax, t.Scenario, strconv.Itoa(r.Revision), safeCell(r.Notes)}
		if err := writer.Write(row); err != nil {
			return err
		}
	}
	writer.Flush()
	return writer.Error()
}
