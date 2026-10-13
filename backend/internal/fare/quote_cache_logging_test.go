package fare

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestBundleCacheHonorsShortestProductTTLAndCopies(t *testing.T) {
	var calls atomic.Int32
	p := protectedForTest(t, functionProvider{"bundle", func(context.Context, Trip) ([]Quote, error) {
		calls.Add(1)
		one, two := sampleQuote(), sampleQuote()
		one.ExpiresAt = one.FetchedAt.Add(200 * time.Millisecond)
		two.ProductID, two.ProductType = "premium", ProductPremium
		two.Fare = &FareRange{2200, 2500, true}
		return []Quote{one, two}, nil
	}}, fastPolicy())
	trip := sampleTrip(t)
	first, err := p.Estimate(context.Background(), trip)
	if err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	deadline := p.cache[tripCacheKey(trip)].expires
	p.mu.Unlock()
	if !deadline.Equal(first[0].ExpiresAt) {
		t.Fatal("cache outlives the earliest product expiry")
	}
	first[1].Fare.MinFare = 1
	*first[0].ETAMinutes = 99
	second, _ := p.Estimate(context.Background(), trip)
	if !second[0].Cached || !second[1].Cached || second[1].Fare.MinFare != 2200 || *second[0].ETAMinutes != 4 {
		t.Fatal("bundle cache did not make defensive copies")
	}
	time.Sleep(210 * time.Millisecond)
	third, _ := p.Estimate(context.Background(), trip)
	if third[0].Cached || calls.Load() != 2 {
		t.Fatal("expired product did not invalidate the whole cached bundle")
	}
}

func TestStructuredProviderLogs(t *testing.T) {
	var buffer bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buffer, nil))
	var fail atomic.Bool
	p, err := NewProtectedProvider(functionProvider{"logged", func(context.Context, Trip) ([]Quote, error) {
		if fail.Load() {
			return nil, errors.New("secret token and private route")
		}
		return []Quote{sampleQuote()}, nil
	}}, fastPolicy(), WithProviderLogger(logger))
	if err != nil {
		t.Fatal(err)
	}
	trip := sampleTrip(t)
	_, _ = p.Estimate(context.Background(), trip)
	_, _ = p.Estimate(context.Background(), trip)
	fail.Store(true)
	trip.Passengers++
	_, _ = p.Estimate(context.Background(), trip)
	if strings.Contains(buffer.String(), "secret") || strings.Contains(buffer.String(), "latitude") || strings.Contains(buffer.String(), "changi") {
		t.Fatal("provider log leaked adapter text or route data")
	}
	hit, miss, failure := false, false, false
	scanner := bufio.NewScanner(&buffer)
	for scanner.Scan() {
		var event map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatal(err)
		}
		if event["provider_id"] != "logged" || event["duration_ms"] == nil || event["product_count"] == nil {
			t.Fatal("provider event is missing structured fields")
		}
		hit = hit || event["cache"] == "hit"
		miss = miss || event["cache"] == "miss"
		failure = failure || event["outcome"] == "failure"
	}
	if !hit || !miss || !failure {
		t.Fatal("missing success, cache or failure telemetry")
	}
}

func TestPartial429PreservesProductsAndCooldown(t *testing.T) {
	var calls atomic.Int32
	p := protectedForTest(t, functionProvider{"partial-429", func(context.Context, Trip) ([]Quote, error) {
		calls.Add(1)
		return []Quote{sampleQuote()}, &ProviderHTTPError{StatusCode: 429, RetryAfter: "60"}
	}}, fastPolicy())
	trip := sampleTrip(t)
	quotes, err := p.Estimate(context.Background(), trip)
	if len(quotes) != 1 || err == nil || calls.Load() != 1 {
		t.Fatal("partial 429 was discarded or retried")
	}
	trip.Passengers++
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err = p.Estimate(ctx, trip)
	if !errors.Is(err, context.DeadlineExceeded) || calls.Load() != 1 {
		t.Fatal("partial 429 failed to cool down the provider")
	}
}
