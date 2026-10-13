package fare

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
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
	quote   Quote
	expires time.Time
	used    uint64
}

type estimateFlight struct {
	done  chan struct{}
	quote Quote
	err   error
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
}

func NewProtectedProvider(p Provider, policy ProviderPolicy) (*ProtectedProvider, error) {
	if p == nil || math.IsNaN(policy.RequestsPerSecond) || math.IsInf(policy.RequestsPerSecond, 0) ||
		policy.RequestsPerSecond <= 0 || policy.Burst < 1 || policy.CacheTTL <= 0 ||
		policy.MaxCacheEntries < 1 || policy.MaxInFlight < 1 || policy.MaxAttempts < 1 ||
		policy.BaseBackoff <= 0 || policy.MaxBackoff < policy.BaseBackoff {
		return nil, errors.New("invalid provider protection policy")
	}
	return &ProtectedProvider{
		provider: p, policy: policy, cache: make(map[[32]byte]cacheEntry),
		flights: make(map[[32]byte]*estimateFlight), tokens: float64(policy.Burst),
		refilled: time.Now(),
	}, nil
}

func (p *ProtectedProvider) ID() string   { return p.provider.ID() }
func (p *ProtectedProvider) Name() string { return p.provider.Name() }

// Direction, precise coordinates, product and all demo price inputs matter.
// Hashing avoids retaining raw device coordinates in the cache keys.
func tripCacheKey(t Trip) [32]byte {
	return sha256.Sum256([]byte(fmt.Sprintf("%q|%.17g|%.17g|%q|%.17g|%.17g|%d|%q|%q|%.17g",
		t.Pickup.ID, t.Pickup.Latitude, t.Pickup.Longitude,
		t.Dropoff.ID, t.Dropoff.Latitude, t.Dropoff.Longitude,
		t.Passengers, t.RideType, t.Scenario, t.DistanceKM)))
}

func (p *ProtectedProvider) Estimate(ctx context.Context, trip Trip) (Quote, error) {
	if err := ctx.Err(); err != nil {
		return Quote{}, err
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
		quote := cloneQuote(entry.quote)
		quote.Cached = true
		return quote, nil
	}
	if flight, ok := p.flights[key]; ok {
		p.mu.Unlock()
		select {
		case <-ctx.Done():
			return Quote{}, ctx.Err()
		case <-flight.done:
			return cloneQuote(flight.quote), flight.err
		}
	}
	if len(p.flights) >= p.policy.MaxInFlight {
		p.mu.Unlock()
		return Quote{}, ErrProviderBusy
	}
	flight := &estimateFlight{done: make(chan struct{})}
	p.flights[key] = flight
	p.mu.Unlock()

	// The leader owns this request's context. Its cancellation ends the shared
	// attempt; a waiting caller's cancellation does not cancel the leader.
	quote, err := p.estimate(ctx, trip)
	p.mu.Lock()
	if err == nil && validQuote(quote) {
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
		quote.Cached = false
		p.cache[key] = cacheEntry{cloneQuote(quote), time.Now().Add(p.policy.CacheTTL), p.sequence}
	}
	flight.quote, flight.err = cloneQuote(quote), err
	delete(p.flights, key)
	close(flight.done)
	p.mu.Unlock()
	return cloneQuote(quote), err
}

func validQuote(q Quote) bool {
	return q.Status == "available" && q.AmountCents != nil && *q.AmountCents > 0 &&
		q.ETAMinutes != nil && *q.ETAMinutes >= 0
}

// The pointer fields must not let a caller mutate a cached or shared result.
func cloneQuote(q Quote) Quote {
	if q.AmountCents != nil {
		amount := *q.AmountCents
		q.AmountCents = &amount
	}
	if q.ETAMinutes != nil {
		eta := *q.ETAMinutes
		q.ETAMinutes = &eta
	}
	return q
}

func (p *ProtectedProvider) estimate(ctx context.Context, trip Trip) (Quote, error) {
	for attempt := 0; attempt < p.policy.MaxAttempts; attempt++ {
		// Every attempt, including a retry, consumes a provider token.
		if err := p.waitForToken(ctx); err != nil {
			return Quote{}, err
		}
		quote, err := p.provider.Estimate(ctx, trip)
		if err == nil {
			if err := ctx.Err(); err != nil {
				return Quote{}, err
			}
			return quote, nil
		}
		var upstream *ProviderHTTPError
		if !errors.As(err, &upstream) || !upstream.Retryable() {
			return Quote{}, err
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
		if attempt+1 == p.policy.MaxAttempts {
			return Quote{}, err
		}
		if err := waitContext(ctx, delay); err != nil {
			return Quote{}, err
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
