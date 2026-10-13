package fare

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestDemoProviderCancelsItsLatency(t *testing.T) {
	provider := DemoProvider{"slow-demo", "Slow", 500, 100, 3, time.Hour}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	quotes, err := provider.Estimate(ctx, sampleTrip(t))
	if !errors.Is(err, context.DeadlineExceeded) || len(quotes) != 0 {
		t.Fatalf("demo timer did not cancel: %v", err)
	}
}

func TestDemoProviderRejectsAlreadyCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	provider := DemoProvider{"instant", "Instant", 500, 100, 3, 0}
	quotes, err := provider.Estimate(ctx, sampleTrip(t))
	if !errors.Is(err, context.Canceled) || len(quotes) != 0 {
		t.Fatal("zero-delay demo accepted an already-canceled context")
	}
}

func TestHTTPProviderCancellationReachesServer(t *testing.T) {
	for _, mode := range []string{"parent-cancel", "service-deadline"} {
		t.Run(mode, func(t *testing.T) {
			started, serverCanceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(started)
				select {
				case <-r.Context().Done():
					close(serverCanceled)
				case <-release:
				}
			}))
			defer server.Close()
			defer close(release) // Prevent a failed assertion from hanging Close.
			provider := protectedForTest(t, testHTTPProvider(server), fastPolicy())
			budget := 3 * time.Second
			if mode == "service-deadline" {
				budget = 300 * time.Millisecond
			}
			service := NewService([]Provider{provider}, budget, WithMaxConcurrentProviders(1))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := make(chan Comparison, 1)
			trip := sampleTrip(t)
			go func() { result <- service.Compare(ctx, trip) }()
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("HTTP request never reached the fake server")
			}
			if mode == "parent-cancel" {
				cancel()
			}
			select {
			case comparison := <-result:
				if comparison.Quotes[0].Status != "unavailable" || len(service.slots) != 0 {
					t.Fatal("cancellation did not free the service permit")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("HTTP adapter did not exit after cancellation")
			}
			select {
			case <-serverCanceled:
			case <-time.After(2 * time.Second):
				t.Fatal("server did not observe HTTP context cancellation")
			}
		})
	}
}

func TestConcurrencyLimitAcrossComparisons(t *testing.T) {
	var active, maximum, calls atomic.Int32
	started, release := make(chan struct{}, 24), make(chan struct{})
	providers := make([]Provider, 12)
	for i := range providers {
		providers[i] = functionProvider{fmt.Sprintf("provider-%02d", i), func(ctx context.Context, _ Trip) ([]Quote, error) {
			calls.Add(1)
			count := active.Add(1)
			defer active.Add(-1)
			for previous := maximum.Load(); count > previous; previous = maximum.Load() {
				if maximum.CompareAndSwap(previous, count) {
					break
				}
			}
			started <- struct{}{}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-release:
				return []Quote{sampleQuote()}, nil
			}
		}}
	}
	service := NewService(providers, 3*time.Second, WithMaxConcurrentProviders(3))
	results := make(chan Comparison, 2)
	trip := sampleTrip(t)
	for i := 0; i < 2; i++ {
		go func() { results <- service.Compare(context.Background(), trip) }()
	}
	for i := 0; i < 3; i++ {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("workers never filled the available permits")
		}
	}
	select {
	case <-started:
		t.Fatal("more than three provider calls started while all permits were held")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	for i := 0; i < 2; i++ {
		select {
		case comparison := <-results:
			if len(comparison.Quotes) != len(providers) {
				t.Fatal("worker pool lost provider results")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("worker pool did not finish")
		}
	}
	if maximum.Load() > 3 || active.Load() != 0 || calls.Load() != 24 || len(service.slots) != 0 {
		t.Fatalf("global bound or cleanup failed: max=%d active=%d calls=%d", maximum.Load(), active.Load(), calls.Load())
	}
}

func TestCanceledQueueDoesNotStartMoreProviders(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{}, 8)
	providers := make([]Provider, 8)
	for i := range providers {
		providers[i] = functionProvider{fmt.Sprintf("queued-%d", i), func(ctx context.Context, _ Trip) ([]Quote, error) {
			calls.Add(1)
			started <- struct{}{}
			<-ctx.Done()
			return nil, ctx.Err()
		}}
	}
	service := NewService(providers, 3*time.Second, WithMaxConcurrentProviders(1))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan Comparison, 1)
	trip := sampleTrip(t)
	go func() { result <- service.Compare(ctx, trip) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first provider did not start")
	}
	cancel()
	select {
	case comparison := <-result:
		if len(comparison.Quotes) != 8 || calls.Load() != 1 {
			t.Fatal("canceled queue called another provider or lost fallback entries")
		}
	case <-time.After(time.Second):
		t.Fatal("canceled queue did not drain")
	}
}

func TestMultipleProductsPartialDataAndErrors(t *testing.T) {
	standard, premium, taxi := sampleQuote(), sampleQuote(), sampleQuote()
	premium.ProductID, premium.ProductType, premium.ServiceName = "premium", ProductPremium, "Premium"
	premium.Fare, premium.ETAMinutes = &FareRange{2100, 2400, true}, nil
	taxi.ProductID, taxi.ProductType, taxi.ServiceName, taxi.Fare = "taxi", ProductTaxi, "Taxi", nil
	var calls atomic.Int32
	p := protectedForTest(t, functionProvider{"partial", func(context.Context, Trip) ([]Quote, error) {
		calls.Add(1)
		return []Quote{standard, premium, taxi}, &ProviderHTTPError{StatusCode: 503}
	}}, fastPolicy())
	service := NewService([]Provider{p}, time.Second)
	comparison := service.Compare(context.Background(), sampleTrip(t))
	if len(comparison.Quotes) != 3 || calls.Load() != 1 {
		t.Fatal("partial HTTP result was discarded or retried")
	}
	for _, quote := range comparison.Quotes {
		if quote.ProductID == "standard" && quote.Status != "available" {
			t.Fatal("healthy product was discarded")
		}
		if quote.ProductID != "standard" && quote.Status != "partial" {
			t.Fatal("partial product did not preserve useful fields")
		}
	}
}

func TestQuoteNormalizationPreservesSiblings(t *testing.T) {
	point, ranged, bad, unknown, duplicate := sampleQuote(), sampleQuote(), sampleQuote(), sampleQuote(), sampleQuote()
	point.Fare.IsEstimateRange = true // Equal bounds must collapse to a point.
	ranged.ProductID, ranged.Fare = "ranged", &FareRange{2000, 2200, false}
	bad.ProductID, bad.Fare = "bad-range", &FareRange{3000, 2000, true}
	unknown.ProductID, unknown.ProductType = "unknown", "unsupported"
	p := functionProvider{id: "http-demo"}
	quotes, invalid := normalizeQuotes(p, sampleTrip(t), []Quote{point, ranged, bad, unknown, duplicate}, nil, time.Now())
	if len(quotes) != 3 || invalid != 3 {
		t.Fatalf("invalid product poisoned siblings: quotes=%d invalid=%d", len(quotes), invalid)
	}
	if quotes[0].Fare.IsEstimateRange || !quotes[1].Fare.IsEstimateRange || quotes[2].Fare != nil || quotes[2].Status != "partial" {
		t.Fatal("range normalization or partial fallback failed")
	}
}

func TestShortProductExpiresWhileOtherProviderRuns(t *testing.T) {
	created := make(chan struct{})
	fast := functionProvider{"short", func(context.Context, Trip) ([]Quote, error) {
		quote := sampleQuote()
		quote.ExpiresAt = quote.FetchedAt.Add(20 * time.Millisecond)
		close(created)
		return []Quote{quote}, nil
	}}
	slow := functionProvider{"long", func(ctx context.Context, _ Trip) ([]Quote, error) {
		select {
		case <-created:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if err := waitContext(ctx, 50*time.Millisecond); err != nil {
			return nil, err
		}
		return []Quote{sampleQuote()}, nil
	}}
	comparison := NewService([]Provider{fast, slow}, time.Second).Compare(context.Background(), sampleTrip(t))
	if comparison.Quotes[0].ProviderID != "long" || comparison.Quotes[0].Status != "available" || comparison.Quotes[1].Status != "expired" {
		t.Fatal("a product that expired during aggregation was still ranked as fresh")
	}
	if !comparison.ExpiresAt.Equal(comparison.Quotes[1].ExpiresAt) {
		t.Fatal("comparison lost its expired product's refresh hint")
	}
}
