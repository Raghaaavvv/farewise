package fare

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type functionProvider struct {
	id string
	fn func(context.Context, Trip) (Quote, error)
}

func (p functionProvider) ID() string   { return p.id }
func (p functionProvider) Name() string { return p.id }
func (p functionProvider) Estimate(ctx context.Context, trip Trip) (Quote, error) {
	return p.fn(ctx, trip)
}

// A test-only HTTP adapter exercises actual status/header parsing. No real
// ride-provider endpoint or device location is used by these tests.
func testHTTPProvider(server *httptest.Server) Provider {
	return functionProvider{"http-demo", func(ctx context.Context, _ Trip) (Quote, error) {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
		if err != nil {
			return Quote{}, err
		}
		response, err := server.Client().Do(request)
		if err != nil {
			return Quote{}, err
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return Quote{}, &ProviderHTTPError{response.StatusCode, response.Header.Get("Retry-After")}
		}
		var quote Quote
		err = json.NewDecoder(response.Body).Decode(&quote)
		return quote, err
	}}
}

func sampleQuote() Quote {
	amount, eta := 1800, 4
	return Quote{ProviderID: "http-demo", ProviderName: "HTTP demo", Status: "available",
		Currency: "SGD", Source: "simulated", AmountCents: &amount, ETAMinutes: &eta, CollectedAt: time.Now().UTC()}
}

func sampleTrip(t *testing.T) Trip {
	t.Helper()
	trip, err := Validate(Request{PickupID: "changi-t3", DropoffID: "orchard", Passengers: 1, RideType: "standard", Scenario: "normal"})
	if err != nil {
		t.Fatal(err)
	}
	return trip
}

func fastPolicy() ProviderPolicy {
	p := DemoProviderPolicy()
	p.RequestsPerSecond, p.Burst = 10000, 100
	p.BaseBackoff, p.MaxBackoff = 10*time.Millisecond, 40*time.Millisecond
	return p
}

func protectedForTest(t *testing.T, provider Provider, policy ProviderPolicy) *ProtectedProvider {
	t.Helper()
	p, err := NewProtectedProvider(provider, policy)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestHTTP429HonorsRetryAfterThenCachesSuccess(t *testing.T) {
	var calls atomic.Int32
	requestTimes := make(chan time.Time, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestTimes <- time.Now()
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_ = json.NewEncoder(w).Encode(sampleQuote())
	}))
	defer server.Close()
	p := protectedForTest(t, testHTTPProvider(server), fastPolicy())
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	trip := sampleTrip(t)
	first, err := p.Estimate(ctx, trip)
	if err != nil {
		t.Fatal(err)
	}
	a, b := <-requestTimes, <-requestTimes
	// Assert a lower bound, not a brittle upper bound on CI scheduling latency.
	if elapsed := b.Sub(a); elapsed < time.Second {
		t.Fatalf("retried before Retry-After elapsed: %v", elapsed)
	}
	second, err := p.Estimate(ctx, trip)
	if err != nil || calls.Load() != 2 || !second.Cached || first.Cached || !first.CollectedAt.Equal(second.CollectedAt) {
		t.Fatalf("success should be cached without a third HTTP call: calls=%d, cached=%v, err=%v", calls.Load(), second.Cached, err)
	}
}

func TestHTTP429DeadlineAndCooldownProtectOtherTrips(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	policy := fastPolicy()
	policy.MaxAttempts = 1 // Even the final attempt must record the cooldown.
	p := protectedForTest(t, testHTTPProvider(server), policy)
	trip := sampleTrip(t)
	if _, err := p.Estimate(context.Background(), trip); err == nil {
		t.Fatal("expected the first 429")
	}
	trip.Passengers++
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := p.Estimate(ctx, trip)
	if !errors.Is(err, context.DeadlineExceeded) || calls.Load() != 1 {
		t.Fatalf("cooldown must stop another trip's HTTP request: calls=%d, err=%v", calls.Load(), err)
	}
}

func TestHTTPRetryBudgetAndPermanentErrors(t *testing.T) {
	for _, status := range []int{http.StatusServiceUnavailable, http.StatusUnauthorized} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32
			requestTimes := make(chan time.Time, 6)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				requestTimes <- time.Now()
				w.WriteHeader(status)
			}))
			defer server.Close()
			policy := fastPolicy()
			p := protectedForTest(t, testHTTPProvider(server), policy)
			for n := 0; n < 2; n++ {
				if _, err := p.Estimate(context.Background(), sampleTrip(t)); err == nil {
					t.Fatal("expected upstream failure")
				}
			}
			want := int32(2)
			if status == http.StatusServiceUnavailable {
				want *= int32(policy.MaxAttempts)
			}
			if calls.Load() != want {
				t.Fatalf("failures must not be cached; retry only transient statuses: got %d want %d", calls.Load(), want)
			}
			if status == http.StatusServiceUnavailable {
				a, b, c := <-requestTimes, <-requestTimes, <-requestTimes
				if b.Sub(a) < policy.BaseBackoff/2 || c.Sub(b) < policy.BaseBackoff {
					t.Fatal("HTTP retries skipped exponential backoff")
				}
			}
		})
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 13, 10, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		value string
		wait  time.Duration
		valid bool
	}{
		{" 12 ", 12 * time.Second, true}, {"0", 0, true},
		{now.Add(7 * time.Second).Format(http.TimeFormat), 7 * time.Second, true},
		{now.Add(-time.Second).Format(http.TimeFormat), 0, true},
		{"", 0, false}, {"-1", 0, false}, {"0.5", 0, false}, {"bad-date", 0, false},
		{"999999999999999999999999999", time.Duration(1<<63 - 1), true},
	} {
		wait, valid := ParseRetryAfter(test.value, now)
		if wait != test.wait || valid != test.valid {
			t.Errorf("%q: got %v,%v want %v,%v", test.value, wait, valid, test.wait, test.valid)
		}
	}
}

func TestTokenBucketBurstRefillAndProviderIsolation(t *testing.T) {
	var mu sync.Mutex
	var times []time.Time
	var calls atomic.Int32
	provider := functionProvider{"limited", func(context.Context, Trip) (Quote, error) {
		calls.Add(1)
		mu.Lock()
		times = append(times, time.Now())
		mu.Unlock()
		return sampleQuote(), nil
	}}
	policy := fastPolicy()
	policy.RequestsPerSecond, policy.Burst = 10, 2
	p := protectedForTest(t, provider, policy)
	trip := sampleTrip(t)
	for n := 0; n < 3; n++ {
		trip.Passengers = n + 1 // Distinct keys bypass the success cache.
		if _, err := p.Estimate(context.Background(), trip); err != nil {
			t.Fatal(err)
		}
	}
	if elapsed := times[2].Sub(times[0]); elapsed < 90*time.Millisecond {
		t.Fatalf("third call must wait for refill after burst: %v", elapsed)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	trip.Passengers = 4
	if _, err := p.Estimate(ctx, trip); !errors.Is(err, context.DeadlineExceeded) || calls.Load() != 3 {
		t.Fatalf("canceled token wait must not call provider: calls=%d, err=%v", calls.Load(), err)
	}
	// Another instance has its own full bucket, despite the first being empty.
	other := protectedForTest(t, provider, policy)
	if _, err := other.Estimate(ctx, trip); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("an already-canceled caller must not run even on another provider")
	}
	fresh, stop := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer stop()
	if _, err := other.Estimate(fresh, trip); err != nil {
		t.Fatalf("another provider's bucket was affected: %v", err)
	}
}

func TestCacheKeysTTLAndCopies(t *testing.T) {
	var calls atomic.Int32
	p := protectedForTest(t, functionProvider{"cache", func(context.Context, Trip) (Quote, error) {
		calls.Add(1)
		return sampleQuote(), nil
	}}, fastPolicy())
	trip := sampleTrip(t)
	first, _ := p.Estimate(context.Background(), trip)
	*first.AmountCents = 1 // Must not mutate the stored quote.
	second, err := p.Estimate(context.Background(), trip)
	if err != nil || !second.Cached || *second.AmountCents != 1800 || calls.Load() != 1 {
		t.Fatal("cache failed or returned shared mutable pointers")
	}
	for _, change := range []func(*Trip){
		func(t *Trip) { t.Pickup, t.Dropoff = t.Dropoff, t.Pickup },
		func(t *Trip) { t.Pickup.Latitude += .00001 },
		func(t *Trip) { t.Dropoff.Longitude += .00001 },
		func(t *Trip) { t.Passengers++ },
		func(t *Trip) { t.RideType = "six" },
		func(t *Trip) { t.Scenario = "rain" },
		func(t *Trip) { t.DistanceKM++ },
	} {
		changed := trip
		change(&changed)
		q, err := p.Estimate(context.Background(), changed)
		if err != nil || q.Cached {
			t.Fatal("changed price input incorrectly reused cache")
		}
	}
	// Force just this entry's deadline into the past, without sleeping for TTL.
	key := tripCacheKey(trip)
	p.mu.Lock()
	entry := p.cache[key]
	entry.expires = time.Now().Add(-time.Nanosecond)
	p.cache[key] = entry
	p.mu.Unlock()
	before := calls.Load()
	q, err := p.Estimate(context.Background(), trip)
	if err != nil || q.Cached || calls.Load() != before+1 {
		t.Fatal("expired entry must fetch again")
	}
}

func TestCacheCapacityAndInvalidResults(t *testing.T) {
	policy := fastPolicy()
	policy.MaxCacheEntries = 2
	p := protectedForTest(t, functionProvider{"cache", func(context.Context, Trip) (Quote, error) {
		return sampleQuote(), nil
	}}, policy)
	trip := sampleTrip(t)
	for _, passengers := range []int{1, 2, 1, 3} {
		trip.Passengers = passengers
		_, _ = p.Estimate(context.Background(), trip)
	}
	p.mu.Lock()
	size := len(p.cache)
	p.mu.Unlock()
	trip.Passengers = 2
	q, err := p.Estimate(context.Background(), trip)
	if err != nil || q.Cached || size != 2 {
		t.Fatal("cache must evict least recently used key at capacity")
	}
	var calls atomic.Int32
	invalid := protectedForTest(t, functionProvider{"invalid", func(context.Context, Trip) (Quote, error) {
		calls.Add(1)
		q := sampleQuote()
		q.AmountCents = nil
		return q, nil
	}}, policy)
	for n := 0; n < 2; n++ {
		_, _ = invalid.Estimate(context.Background(), trip)
	}
	if calls.Load() != 2 {
		t.Fatal("invalid quotes must not be cached")
	}
}

func TestConcurrentComparisonsCoalesceAndWaiterCanCancel(t *testing.T) {
	var calls atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	p := protectedForTest(t, functionProvider{"shared", func(ctx context.Context, _ Trip) (Quote, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		select {
		case <-ctx.Done():
			return Quote{}, ctx.Err()
		case <-release:
			return sampleQuote(), nil
		}
	}}, fastPolicy())
	trip := sampleTrip(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	results := make(chan error, 12)
	go func() { _, err := p.Estimate(ctx, trip); results <- err }()
	<-started
	for n := 0; n < 11; n++ {
		go func() { _, err := p.Estimate(ctx, trip); results <- err }()
	}
	waiter, stop := context.WithTimeout(ctx, 10*time.Millisecond)
	_, err := p.Estimate(waiter, trip)
	stop()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting caller must honor its own context: %v", err)
	}
	close(release)
	for n := 0; n < 12; n++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("concurrent comparisons made %d adapter calls", calls.Load())
	}
}

func TestBackoffHasBoundedExponentialJitter(t *testing.T) {
	p := protectedForTest(t, functionProvider{id: "jitter"}, fastPolicy())
	for attempt, ceiling := range []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 40 * time.Millisecond, 40 * time.Millisecond} {
		seen := map[time.Duration]bool{}
		for n := 0; n < 20; n++ {
			delay := p.backoff(attempt)
			if delay < ceiling/2 || delay > ceiling {
				t.Fatalf("attempt %d backoff out of bounds: %v", attempt, delay)
			}
			seen[delay] = true
		}
		if len(seen) < 2 {
			t.Fatal("backoff did not add jitter")
		}
	}
}

func TestCachedComparisonKeepsOriginalFreshness(t *testing.T) {
	quote := sampleQuote()
	quote.CollectedAt = time.Now().Add(-time.Minute).UTC()
	p := protectedForTest(t, functionProvider{"old", func(context.Context, Trip) (Quote, error) {
		return quote, nil
	}}, fastPolicy())
	service := NewService([]Provider{p}, time.Second)
	trip := sampleTrip(t)
	_ = service.Compare(context.Background(), trip)
	comparison := service.Compare(context.Background(), trip)
	if !comparison.Quotes[0].Cached || !comparison.CollectedAt.Equal(quote.CollectedAt) || !comparison.ExpiresAt.Equal(quote.CollectedAt.Add(2*time.Minute)) {
		t.Fatal("recomparison extended the cached quote's display lifetime")
	}
}

func TestFreshThenCachedComparisonHasIdenticalExpiry(t *testing.T) {
	p := protectedForTest(t, functionProvider{"fresh", func(context.Context, Trip) (Quote, error) {
		return sampleQuote(), nil // Timestamp is generated after Compare starts.
	}}, fastPolicy())
	service := NewService([]Provider{p}, time.Second)
	trip := sampleTrip(t)
	first := service.Compare(context.Background(), trip)
	second := service.Compare(context.Background(), trip)
	if first.Quotes[0].Cached || !second.Quotes[0].Cached ||
		!first.CollectedAt.Equal(second.CollectedAt) || !first.ExpiresAt.Equal(second.ExpiresAt) {
		t.Fatal("the first refresh changed a freshly collected quote's display expiry")
	}
}
