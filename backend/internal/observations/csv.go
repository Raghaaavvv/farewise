// Export actual fares and their captured features, keeping demo reference prices separate.
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

// WriteCSV writes actual observations and captured features in the research export format.
func WriteCSV(w io.Writer, records []Record) error {
	writer := csv.NewWriter(w)

	header := []string{
		"observation_id",
		"session_id",
		"provider",
		"product",
		"price_cents",
		"fare_kind",
		"observed_at_utc",
		"observed_at_sgt",
		"sg_weekday",
		"time_slot",
		"captured_at_utc",
		"pickup",
		"dropoff",
		"pickup_latitude",
		"pickup_longitude",
		"dropoff_latitude",
		"dropoff_longitude",
		"pickup_zone",
		"dropoff_zone",
		"passengers",
		"ride_type",
		"distance_meters",
		"route_source",
		"driving_seconds",
		"baseline_seconds",
		"traffic_status",
		"traffic_coverage",
		"traffic_observed_at",
		"traffic_expires_at",
		"weather",
		"promotion",
		"included_fees_cents",
		"context_aligned",
		"traffic_fresh_at_quote",
		"training_eligible",
		"feature_version",
		"demo_version",
		"demo_min_cents",
		"demo_max_cents",
		"demo_scenario",
		"revision",
		"notes",
	}

	writeError := writer.Write(header)

	if writeError != nil {
		return writeError
	}

	for _, record := range records {
		record.Derive()

		snapshot := record.Snapshot

		trip := snapshot.Trip

		route := trip.Route

		baseline := ""
		status := "off"
		coverage := ""
		observed := ""
		expires := ""

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

		if record.IncludedFeesCents != nil {
			fees = strconv.Itoa(*record.IncludedFeesCents)
		}

		demoMin := ""
		demoMax := ""

		for _, quote := range snapshot.DemoQuotes {
			if quote.ProviderID == record.Provider && quote.ProductID == trip.RideType && quote.Fare != nil {
				demoMin = strconv.Itoa(quote.Fare.MinFare)

				demoMax = strconv.Itoa(quote.Fare.MaxFare)

				break
			}
		}

		number := func(v float64) string {
			return strconv.FormatFloat(v, 'f', -1, 64)
		}

		// Keep these values in exactly the same order as the CSV header above.
		row := []string{
			record.ID,
			record.SessionID,
			record.Provider,
			safeCell(record.Product),
			strconv.Itoa(*record.PriceCents),
			record.FareKind,
			record.QuotedAt.UTC().Format(time.RFC3339Nano),
			record.QuotedAt.In(singapore).Format(time.RFC3339Nano),
			strconv.Itoa(record.SingaporeWeekday),
			record.TimeSlot,
			snapshot.CapturedAt.UTC().Format(time.RFC3339Nano),
			safeCell(trip.Pickup.Name),
			safeCell(trip.Dropoff.Name),
			number(trip.Pickup.Latitude),
			number(trip.Pickup.Longitude),
			number(trip.Dropoff.Latitude),
			number(trip.Dropoff.Longitude),
			snapshot.PickupZone,
			snapshot.DropoffZone,
			strconv.Itoa(trip.Passengers),
			trip.RideType,
			number(route.DistanceMeters),
			route.Source,
			optional(route.DurationSeconds),
			baseline,
			status,
			coverage,
			observed,
			expires,
			record.Weather,
			record.Promotion,
			fees,
			strconv.FormatBool(record.ContextAligned),
			strconv.FormatBool(record.TrafficFreshAtQuote),
			strconv.FormatBool(record.TrainingEligible),
			snapshot.FeatureVersion,
			snapshot.DemoVersion,
			demoMin,
			demoMax,
			trip.Scenario,
			strconv.Itoa(record.Revision),
			safeCell(record.Notes),
		}

		rowWriteError := writer.Write(row)

		if rowWriteError != nil {
			return rowWriteError
		}
	}

	writer.Flush()

	return writer.Error()
}
