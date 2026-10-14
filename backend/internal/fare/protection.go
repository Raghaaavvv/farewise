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

func DemoProviderPolicy() ProviderPolicy {
	return ProviderPolicy{
		RequestsPerSecond: 2, Burst: 2, CacheTTL: 30 * time.Second,
		MaxCacheEntries: 256, MaxInFlight: 64, MaxAttempts: 3,
		BaseBackoff: 200 * time.Millisecond, MaxBackoff: time.Second,
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

func WithProviderLogger(logger *slog.Logger) ProtectionOption {
	return func(p *ProtectedProvider) {
		if logger != nil {
			p.logger = logger
		}
	}
}

func NewProtectedProvider(p Provider, policy ProviderPolicy, options ...ProtectionOption) (*ProtectedProvider, error) {
	if p == nil || math.IsNaN(policy.RequestsPerSecond) || math.IsInf(policy.RequestsPerSecond, 0) ||
		policy.RequestsPerSecond <= 0 || policy.Burst < 1 || policy.CacheTTL <= 0 ||
		policy.MaxCacheEntries < 1 || policy.MaxInFlight < 1 || policy.MaxAttempts < 1 ||
		policy.BaseBackoff <= 0 || policy.MaxBackoff < policy.BaseBackoff {
		return nil, errors.New("invalid provider protection policy")
	}
	protected := &ProtectedProvider{
		provider: p, policy: policy, cache: make(map[[32]byte]cacheEntry),
		flights: make(map[[32]byte]*estimateFlight), tokens: float64(policy.Burst),
		refilled: time.Now(), logger: slog.Default(),
	}
	for _, option := range options {
		option(protected)
	}
	return protected, nil
}

func (p *ProtectedProvider) ID() string   { return p.provider.ID() }
func (p *ProtectedProvider) Name() string { return p.provider.Name() }

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

func (p *ProtectedProvider) Estimate(ctx context.Context, trip Trip) (quotes []Quote, err error) {
	start, cacheState := time.Now(), "miss"
	defer func() { logProvider(ctx, p.logger, "provider.lookup", p.ID(), cacheState, start, quotes, err) }()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key := tripCacheKey(trip)
	p.mu.Lock()
	now := time.Now()
	// Lazy eviction also bounds memory when an expired key is never reused.
	for k, entry := range p.cache {
		if !now.Before(entry.expires) {
			delete(p.cache, k)
		}
	}
	if entry, ok := p.cache[key]; ok {
		p.sequence++
		entry.used = p.sequence
		p.cache[key] = entry
		p.mu.Unlock()
		quotes := cloneQuotes(entry.quotes)
		for i := range quotes {
			quotes[i].Cached = true
		}
		cacheState = "hit"
		return quotes, nil
	}
	if flight, ok := p.flights[key]; ok {
		p.mu.Unlock()
		cacheState = "coalesced"
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-flight.done:
			return cloneQuotes(flight.quotes), flight.err
		}
	}
	if len(p.flights) >= p.policy.MaxInFlight {
		p.mu.Unlock()
		return nil, ErrProviderBusy
	}
	flight := &estimateFlight{done: make(chan struct{})}
	p.flights[key] = flight
	p.mu.Unlock()

	// The leader owns this request's context. Its cancellation ends the shared
	// attempt; a waiting caller's cancellation does not cancel the leader.
	quotes, err = p.estimate(ctx, trip)
	quotes = cloneQuotes(quotes)
	for i := range quotes {
		quotes[i].Cached = false
	}
	p.mu.Lock()
	deadline, cacheable := cacheDeadline(quotes, time.Now(), p.policy.CacheTTL)
	if err == nil && cacheable {
		if len(p.cache) >= p.policy.MaxCacheEntries {
			var oldest [32]byte
			var used uint64 = math.MaxUint64
			for k, entry := range p.cache {
				if entry.used < used {
					oldest, used = k, entry.used
				}
			}
			delete(p.cache, oldest)
		}
		p.sequence++
		p.cache[key] = cacheEntry{cloneQuotes(quotes), deadline, p.sequence}
	}
	flight.quotes, flight.err = cloneQuotes(quotes), err
	delete(p.flights, key)
	close(flight.done)
	p.mu.Unlock()
	return cloneQuotes(quotes), err
}

func (p *ProtectedProvider) estimate(ctx context.Context, trip Trip) ([]Quote, error) {
	for attempt := 0; attempt < p.policy.MaxAttempts; attempt++ {
		// Every attempt, including a retry, consumes a provider token.
		if err := p.waitForToken(ctx); err != nil {
			return nil, err
		}
		start := time.Now()
		quotes, err := p.provider.Estimate(ctx, trip)
		logProvider(ctx, p.logger, "provider.attempt", p.ID(), "miss", start, quotes, err, "attempt", attempt+1)
		if err == nil {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return quotes, nil
		}
		var upstream *ProviderHTTPError
		if !errors.As(err, &upstream) || !upstream.Retryable() {
			return quotes, err
		}
		delay := p.backoff(attempt)
		if retryAfter, ok := ParseRetryAfter(upstream.RetryAfter, time.Now()); ok && retryAfter > delay {
			delay = retryAfter // Never shorten a provider's requested wait.
		}
		if upstream.StatusCode == 429 || upstream.RetryAfter != "" {
			p.mu.Lock()
			until := time.Now().Add(delay)
			if until.After(p.cooldown) {
				p.cooldown = until
			}
			p.mu.Unlock()
		}
		// Record the cooldown even when no attempts remain, protecting other trips.
		// Keep partial products without retrying the batch, but still record a
		// provider cooldown when a partial response carries a 429/Retry-After.
		if len(quotes) > 0 || attempt+1 == p.policy.MaxAttempts {
			return quotes, err
		}
		if err := waitContext(ctx, delay); err != nil {
			return nil, err
		}
	}
	panic("validated retry policy must allow at least one attempt")
}

func (p *ProtectedProvider) backoff(attempt int) time.Duration {
	delay := p.policy.BaseBackoff
	for n := 0; n < attempt && delay < p.policy.MaxBackoff; n++ {
		if delay > p.policy.MaxBackoff/2 {
			delay = p.policy.MaxBackoff
		} else {
			delay *= 2
		}
	}
	// Equal jitter: wait between half and all of the capped exponential delay.
	return delay/2 + time.Duration(rand.Int63n(int64(delay-delay/2)+1))
}

func (p *ProtectedProvider) waitForToken(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		p.mu.Lock()
		now := time.Now()
		p.tokens = math.Min(float64(p.policy.Burst), p.tokens+now.Sub(p.refilled).Seconds()*p.policy.RequestsPerSecond)
		p.refilled = now
		var delay time.Duration
		if now.Before(p.cooldown) {
			delay = p.cooldown.Sub(now)
		} else if p.tokens >= 1 {
			p.tokens--
			p.mu.Unlock()
			return nil
		} else {
			seconds := (1 - p.tokens) / p.policy.RequestsPerSecond
			// Saturate extreme configured waits rather than overflow Duration.
			if seconds >= float64(math.MaxInt64)/float64(time.Second) {
				delay = time.Duration(math.MaxInt64)
			} else {
				delay = time.Duration(math.Ceil(seconds * float64(time.Second)))
			}
		}
		p.mu.Unlock()
		if err := waitContext(ctx, delay); err != nil {
			return err
		}
	}
}

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
