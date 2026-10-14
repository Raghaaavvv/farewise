// Protect each provider with caching, request coalescing, rate limits and bounded retries.
package fare

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/rand"
	"sync"
	"time"
)

// ProviderPolicy is per provider and per process, not an inbound API limit.
// Live adapters must use budgets and cache rules permitted by their provider.
type ProviderPolicy struct {
	RequestsPerSecond float64
	Burst             int
	CacheTTL          time.Duration
	MaxCacheEntries   int
	MaxInFlight       int
	MaxAttempts       int // Includes the original attempt.
	BaseBackoff       time.Duration
	MaxBackoff        time.Duration
}

// DemoProviderPolicy supplies the rate, cache and retry budgets used by simulated providers.
func DemoProviderPolicy() ProviderPolicy {
	return ProviderPolicy{
		RequestsPerSecond: 2,
		Burst:             2,
		CacheTTL:          30 * time.Second,
		MaxCacheEntries:   256,
		MaxInFlight:       64,
		MaxAttempts:       3,
		BaseBackoff:       200 * time.Millisecond,
		MaxBackoff:        time.Second,
	}
}

var ErrProviderBusy = errors.New("provider request queue is full")

type cacheEntry struct {
	quotes  []Quote
	expires time.Time
	used    uint64
}

type estimateFlight struct {
	done   chan struct{}
	quotes []Quote
	err    error
}

// ProtectedProvider composes a Provider with a success cache, request
// coalescing, a token bucket, a shared cooldown, and bounded retries.
type ProtectedProvider struct {
	provider Provider
	policy   ProviderPolicy
	mu       sync.Mutex
	cache    map[[32]byte]cacheEntry
	flights  map[[32]byte]*estimateFlight
	sequence uint64
	tokens   float64
	refilled time.Time
	cooldown time.Time
	logger   *slog.Logger
}

type ProtectionOption func(*ProtectedProvider)

// WithProviderLogger replaces the reliability-layer logger when one is supplied.
func WithProviderLogger(logger *slog.Logger) ProtectionOption {
	return func(protectedProvider *ProtectedProvider) {
		if logger != nil {
			protectedProvider.logger = logger
		}
	}
}

// NewProtectedProvider validates its policy and initializes the per-provider shared state.
func NewProtectedProvider(provider Provider, policy ProviderPolicy, options ...ProtectionOption) (*ProtectedProvider, error) {
	if provider == nil || math.IsNaN(policy.RequestsPerSecond) || math.IsInf(policy.RequestsPerSecond, 0) ||
		policy.RequestsPerSecond <= 0 || policy.Burst < 1 || policy.CacheTTL <= 0 ||
		policy.MaxCacheEntries < 1 || policy.MaxInFlight < 1 || policy.MaxAttempts < 1 ||
		policy.BaseBackoff <= 0 || policy.MaxBackoff < policy.BaseBackoff {
		return nil, errors.New("invalid provider protection policy")
	}

	protected := &ProtectedProvider{
		provider: provider,
		policy:   policy,
		cache:    make(map[[32]byte]cacheEntry),
		flights:  make(map[[32]byte]*estimateFlight),
		tokens:   float64(policy.Burst),
		refilled: time.Now(),
		logger:   slog.Default(),
	}

	for _, option := range options {
		option(protected)
	}

	return protected, nil
}

// ID returns the stable identifier of this provider adapter.
func (protectedProvider *ProtectedProvider) ID() string {
	return protectedProvider.provider.ID()
}

// Name returns the provider name displayed with its quotes.
func (protectedProvider *ProtectedProvider) Name() string {
	return protectedProvider.provider.Name()
}

// Direction, precise coordinates, product and all demo price inputs matter.
// Hashing avoids retaining raw device coordinates in the cache keys.
func tripCacheKey(t Trip) [32]byte {
	duration := "unknown"

	if t.Route.DurationSeconds != nil {
		duration = fmt.Sprintf("%.17g", *t.Route.DurationSeconds)
	}

	return sha256.Sum256([]byte(fmt.Sprintf("%q|%.17g|%.17g|%q|%.17g|%.17g|%d|%q|%q|%.17g|%q|%s|%t",
		t.Pickup.ID, t.Pickup.Latitude, t.Pickup.Longitude,
		t.Dropoff.ID, t.Dropoff.Latitude, t.Dropoff.Longitude,
		t.Passengers, t.RideType, t.Scenario, t.DistanceKM, t.Route.Source, duration, t.Route.TrafficAware)))
}

// Estimate serves a fresh cache entry, joins matching work or leads a new protected request.
func (protectedProvider *ProtectedProvider) Estimate(ctx context.Context, trip Trip) (quotes []Quote, err error) {
	start, cacheState := time.Now(), "miss"

	defer func() {
		logProvider(ctx, protectedProvider.logger, "provider.lookup", protectedProvider.ID(), cacheState, start, quotes, err)
	}()

	contextError := ctx.Err()

	if contextError != nil {
		return nil, contextError
	}

	key := tripCacheKey(trip)

	protectedProvider.mu.Lock()

	now := time.Now()
	// Lazy eviction also bounds memory when an expired key is never reused.
	for k, entry := range protectedProvider.cache {
		if !now.Before(entry.expires) {
			delete(protectedProvider.cache, k)
		}
	}

	if entry, ok := protectedProvider.cache[key]; ok {
		protectedProvider.sequence++

		entry.used = protectedProvider.sequence

		protectedProvider.cache[key] = entry

		protectedProvider.mu.Unlock()

		quotes := cloneQuotes(entry.quotes)

		for i := range quotes {
			quotes[i].Cached = true
		}

		cacheState = "hit"

		return quotes, nil
	}

	if flight, ok := protectedProvider.flights[key]; ok {
		protectedProvider.mu.Unlock()

		cacheState = "coalesced"

		select {
		case <-ctx.Done():
			return nil, ctx.Err()

		case <-flight.done:
			return cloneQuotes(flight.quotes), flight.err
		}
	}

	if len(protectedProvider.flights) >= protectedProvider.policy.MaxInFlight {
		protectedProvider.mu.Unlock()

		return nil, ErrProviderBusy
	}

	flight := &estimateFlight{done: make(chan struct{})}

	protectedProvider.flights[key] = flight

	protectedProvider.mu.Unlock()

	// The leader owns this request's context. Its cancellation ends the shared
	// attempt; a waiting caller's cancellation does not cancel the leader.
	quotes, err = protectedProvider.estimate(ctx, trip)

	quotes = cloneQuotes(quotes)

	for i := range quotes {
		quotes[i].Cached = false
	}

	protectedProvider.mu.Lock()

	deadline, cacheable := cacheDeadline(quotes, time.Now(), protectedProvider.policy.CacheTTL)

	if err == nil && cacheable {
		if len(protectedProvider.cache) >= protectedProvider.policy.MaxCacheEntries {
			var oldest [32]byte

			var used uint64 = math.MaxUint64

			for k, entry := range protectedProvider.cache {
				if entry.used < used {
					oldest, used = k, entry.used
				}
			}

			delete(protectedProvider.cache, oldest)
		}

		protectedProvider.sequence++

		protectedProvider.cache[key] = cacheEntry{
			quotes:  cloneQuotes(quotes),
			expires: deadline,
			used:    protectedProvider.sequence,
		}
	}

	flight.quotes, flight.err = cloneQuotes(quotes), err

	delete(protectedProvider.flights, key)

	close(flight.done)

	protectedProvider.mu.Unlock()

	return cloneQuotes(quotes), err
}

// estimate consumes a token for each attempt and retries only eligible read-only failures.
func (protectedProvider *ProtectedProvider) estimate(ctx context.Context, trip Trip) ([]Quote, error) {
	for attempt := 0; attempt < protectedProvider.policy.MaxAttempts; attempt++ {
		// Every attempt, including a retry, consumes a provider token.
		waitForTokenError := protectedProvider.waitForToken(ctx)

		if waitForTokenError != nil {
			return nil, waitForTokenError
		}

		start := time.Now()

		quotes, err := protectedProvider.provider.Estimate(ctx, trip)

		logProvider(ctx, protectedProvider.logger, "provider.attempt", protectedProvider.ID(), "miss", start, quotes, err, "attempt", attempt+1)

		if err == nil {
			contextError := ctx.Err()

			if contextError != nil {
				return nil, contextError
			}

			return quotes, nil
		}

		var upstream *ProviderHTTPError

		if !errors.As(err, &upstream) || !upstream.Retryable() {
			return quotes, err
		}

		delay := protectedProvider.backoff(attempt)

		if retryAfter, ok := ParseRetryAfter(upstream.RetryAfter, time.Now()); ok && retryAfter > delay {
			delay = retryAfter // Never shorten a provider's requested wait.
		}

		if upstream.StatusCode == 429 || upstream.RetryAfter != "" {
			protectedProvider.mu.Lock()

			until := time.Now().Add(delay)

			if until.After(protectedProvider.cooldown) {
				protectedProvider.cooldown = until
			}

			protectedProvider.mu.Unlock()
		}
		// Record the cooldown even when no attempts remain, protecting other trips.
		// Keep partial products without retrying the batch, but still record a
		// provider cooldown when a partial response carries a 429/Retry-After.
		if len(quotes) > 0 || attempt+1 == protectedProvider.policy.MaxAttempts {
			return quotes, err
		}

		waitContextError := waitContext(ctx, delay)

		if waitContextError != nil {
			return nil, waitContextError
		}
	}

	panic("validated retry policy must allow at least one attempt")
}

// backoff calculates a capped exponential delay with equal jitter.
func (protectedProvider *ProtectedProvider) backoff(attempt int) time.Duration {
	delay := protectedProvider.policy.BaseBackoff

	for n := 0; n < attempt && delay < protectedProvider.policy.MaxBackoff; n++ {
		if delay > protectedProvider.policy.MaxBackoff/2 {
			delay = protectedProvider.policy.MaxBackoff
		} else {
			delay *= 2
		}
	}
	// Equal jitter: wait between half and all of the capped exponential delay.
	return delay/2 + time.Duration(rand.Int63n(int64(delay-delay/2)+1))
}

// waitForToken waits until both the token bucket and shared provider cooldown permit an attempt.
func (protectedProvider *ProtectedProvider) waitForToken(ctx context.Context) error {
	for {
		contextError := ctx.Err()

		if contextError != nil {
			return contextError
		}

		protectedProvider.mu.Lock()

		now := time.Now()

		protectedProvider.tokens = math.Min(float64(protectedProvider.policy.Burst), protectedProvider.tokens+now.Sub(protectedProvider.refilled).Seconds()*protectedProvider.policy.RequestsPerSecond)

		protectedProvider.refilled = now

		var delay time.Duration

		if now.Before(protectedProvider.cooldown) {
			delay = protectedProvider.cooldown.Sub(now)
		} else if protectedProvider.tokens >= 1 {
			protectedProvider.tokens--

			protectedProvider.mu.Unlock()

			return nil
		} else {
			seconds := (1 - protectedProvider.tokens) / protectedProvider.policy.RequestsPerSecond
			// Saturate extreme configured waits rather than overflow Duration.
			if seconds >= float64(math.MaxInt64)/float64(time.Second) {
				delay = time.Duration(math.MaxInt64)
			} else {
				delay = time.Duration(math.Ceil(seconds * float64(time.Second)))
			}
		}

		protectedProvider.mu.Unlock()

		waitContextError := waitContext(ctx, delay)

		if waitContextError != nil {
			return waitContextError
		}
	}
}

// waitContext waits for a delay while allowing the caller to cancel immediately.
func waitContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)

	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()

	case <-timer.C:
		return ctx.Err()
	}
}
