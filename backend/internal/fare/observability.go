// Describe provider outcomes in structured logs without exposing upstream response data.
package fare

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// Classify errors without logging arbitrary adapter text, routes, tokens or
// response bodies. Those can contain private or sensitive upstream data.
func errorKind(err error) string {
	if err == nil {
		return "none"
	}

	if errors.Is(err, context.Canceled) {
		return "canceled"
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline"
	}

	if errors.Is(err, ErrProviderBusy) {
		return "capacity"
	}

	var upstream *ProviderHTTPError

	if errors.As(err, &upstream) {
		return "http"
	}

	return "adapter"
}

// logProvider records latency, cache state and a sanitized summary of the product results.
func logProvider(ctx context.Context, logger *slog.Logger, event, providerID, cache string, start time.Time, quotes []Quote, err error, extra ...any) {
	outcome := "success"
	usable := false

	for _, quote := range quotes {
		if quote.Status == "available" || quote.Status == "partial" {
			usable = true
		}

		if !validQuote(quote) {
			outcome = "partial"
		}
	}

	if err != nil {
		outcome = "partial"
	}

	if !usable {
		outcome = "failure"
	}

	attrs := []any{
		"provider_id",
		providerID,
		"duration_ms",
		float64(time.Since(start).Microseconds()) / 1000,
		"outcome",
		outcome,
		"cache",
		cache,
		"product_count",
		len(quotes),
		"error_kind",
		errorKind(err),
	}

	var upstream *ProviderHTTPError

	if errors.As(err, &upstream) {
		attrs = append(attrs, "http_status", upstream.StatusCode)
	}

	attrs = append(attrs, extra...)

	level := slog.LevelInfo

	if outcome != "success" {
		level = slog.LevelWarn
	}

	logger.Log(ctx, level, event, attrs...)
}
