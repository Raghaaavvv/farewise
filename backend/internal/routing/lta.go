package routing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"math/rand"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const speedBandsURL = "https://datamall2.mytransport.sg/ltaodataservice/v4/TrafficSpeedBands"

var singapore = time.FixedZone("SGT", 8*3600)

type speedLink struct {
	id               string
	start, end       Point
	minimum, maximum float64
}
type speedSnapshot struct {
	links                            []speedLink // Immutable after publication; contains public road data only.
	observedAt, fetchedAt, expiresAt time.Time
}
type TrafficEnricher interface {
	Enrich(context.Context, Route) Route
}

// LTA keeps one complete, bounded snapshot for five minutes and coalesces refreshes.
// One cancellable worker refreshes while the server runs. User routes and keys
// are never persisted or exposed.
type LTA struct {
	client        *http.Client
	endpoint, key string
	mu            sync.Mutex
	snapshot      *speedSnapshot
	flight        chan struct{}
	nextAttempt   time.Time
	lastError     error
	pageInterval  time.Duration
	pageWorkers   int
	now           func() time.Time
}

func NewLTA(key string) (*LTA, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, errors.New("LTA_ACCOUNT_KEY is required for LTA traffic")
	}
	return &LTA{client: &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, endpoint: speedBandsURL, key: key, pageInterval: 200 * time.Millisecond, pageWorkers: 4, now: time.Now}, nil
}

func (l *LTA) load(ctx context.Context) (*speedSnapshot, bool, error) {
	return l.loadSnapshot(ctx, false)
}

func (l *LTA) loadSnapshot(ctx context.Context, force bool) (*speedSnapshot, bool, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		l.mu.Lock()
		now := l.now()
		if !force && l.snapshot != nil && now.Before(l.snapshot.expiresAt) {
			s := l.snapshot
			l.mu.Unlock()
			return s, true, nil
		}
		if l.flight != nil {
			done := l.flight
			l.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, false, ctx.Err()
			case <-done:
				continue
			}
		}
		if now.Before(l.nextAttempt) {
			err := l.lastError
			l.mu.Unlock()
			return nil, false, err
		}
		done := make(chan struct{})
		l.flight = done
		l.mu.Unlock()
		started := time.Now()
		refresh, cancel := context.WithTimeout(ctx, 120*time.Second)
		snapshot, err := l.fetch(refresh)
		cancel()
		l.mu.Lock()
		if err == nil {
			l.snapshot = snapshot
			l.lastError = nil
		} else {
			l.lastError = err
			// Short failure cooldown prevents every comparison repeatedly fetching a bad key/feed.
			until := l.now().Add(time.Minute)
			var failure *Error
			if errors.As(err, &failure) && failure.RetryAfter > time.Minute {
				until = l.now().Add(failure.RetryAfter)
			}
			l.nextAttempt = until
		}
		l.flight = nil
		close(done)
		l.mu.Unlock()
		rows := 0
		if snapshot != nil {
			rows = len(snapshot.links)
		}
		slog.Info("traffic.refresh", "source", "lta", "success", err == nil, "latency_ms", time.Since(started).Milliseconds(), "links", rows)
		return snapshot, false, err
	}
}

type numeric float64

func (n *numeric) UnmarshalJSON(raw []byte) error {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return err
	}
	var f float64
	switch value := v.(type) {
	case float64:
		f = value
	case string:
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return err
		}
		f = parsed
	default:
		return errors.New("invalid number")
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return errors.New("invalid number")
	}
	*n = numeric(f)
	return nil
}

type rawLink struct {
	ID       string   `json:"LinkID"`
	Band     int      `json:"SpeedBand"`
	Minimum  *numeric `json:"MinimumSpeed"`
	Maximum  *numeric `json:"MaximumSpeed"`
	StartLat *numeric `json:"StartLat"`
	StartLon *numeric `json:"StartLon"`
	EndLat   *numeric `json:"EndLat"`
	EndLon   *numeric `json:"EndLon"`
}

func (r rawLink) valid() bool {
	if r.ID == "" || len(r.ID) > 100 || r.Band < 1 || r.Band > 8 || r.Minimum == nil || r.Maximum == nil || r.StartLat == nil || r.StartLon == nil || r.EndLat == nil || r.EndLon == nil {
		return false
	}
	if *r.Minimum < 0 || *r.Minimum > 150 || *r.Maximum < *r.Minimum || *r.Maximum > 200 {
		return false
	}
	for _, p := range []Point{{float64(*r.StartLat), float64(*r.StartLon)}, {float64(*r.EndLat), float64(*r.EndLon)}} {
		if p.Latitude < 1 || p.Latitude > 1.6 || p.Longitude < 103.5 || p.Longitude > 104.6 {
			return false
		}
	}
	return *r.StartLat != *r.EndLat || *r.StartLon != *r.EndLon
}

type speedPage struct {
	updated string
	rows    []json.RawMessage
}

// All workers share one pacer. Overlapping HTTP requests must not multiply
// the overall request-start rate or allow bursts after a slow response.
type pagePacer struct {
	mu       sync.Mutex
	next     time.Time
	interval time.Duration
}

func (p *pagePacer) wait(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		p.mu.Lock()
		now := time.Now()
		if !p.next.After(now) {
			p.next = now.Add(p.interval)
			p.mu.Unlock()
			return nil
		}
		delay := p.next.Sub(now)
		p.mu.Unlock()
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (l *LTA) fetchPage(ctx context.Context, page int, pacer *pagePacer) (speedPage, error) {
	for attempt := 0; attempt < 2; attempt++ {
		result, err := l.fetchPageOnce(ctx, page, pacer)
		var failure *Error
		if err == nil || attempt == 1 || ctx.Err() != nil || !errors.As(err, &failure) || failure.RetryAfter > 0 ||
			(failure.Status != 500 && failure.Status != 502 && failure.Status != 503 && failure.Status != 504) {
			return result, err
		}
		delay := 300*time.Millisecond + time.Duration(rand.Int63n(int64(200*time.Millisecond)))
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return speedPage{}, ctx.Err()
		case <-timer.C:
		}
		slog.Info("traffic.page.retry", "offset", page*500, "attempt", 2)
	}
	panic("unreachable")
}

func (l *LTA) fetchPageOnce(ctx context.Context, page int, pacer *pagePacer) (speedPage, error) {
	if err := pacer.wait(ctx); err != nil {
		return speedPage{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s?$skip=%d", l.endpoint, page*500), nil)
	if err != nil {
		return speedPage{}, trafficFailure()
	}
	req.Header.Set("AccountKey", l.key)
	req.Header.Set("Accept", "application/json")
	response, err := l.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return speedPage{}, ctx.Err()
		}
		slog.Warn("traffic.page.failed", "offset", page*500, "reason", "transport")
		return speedPage{}, &Error{Status: 502, Message: "LTA traffic is unavailable."}
	}
	var body struct {
		Updated string             `json:"lastUpdatedTime"`
		Value   *[]json.RawMessage `json:"value"`
	}
	if response.StatusCode != 200 {
		slog.Warn("traffic.page.failed", "offset", page*500, "reason", "http_status", "status", response.StatusCode)
		failure := trafficFailure()
		failure.Status = response.StatusCode
		if value := response.Header.Get("Retry-After"); value != "" {
			failure.RetryAfter = retryAfter(value, l.now())
		}
		if response.StatusCode == 401 || response.StatusCode == 403 {
			failure.Message = "LTA access failed. Check the backend API account key."
		}
		if response.StatusCode == 429 {
			failure.Status = http.StatusTooManyRequests
			failure.Message = "LTA traffic is temporarily rate limited."
			failure.RetryAfter = retryAfter(response.Header.Get("Retry-After"), l.now())
		}
		response.Body.Close()
		return speedPage{}, failure
	}
	limited := io.LimitReader(response.Body, (2<<20)+1)
	data, readErr := io.ReadAll(limited)
	response.Body.Close()
	if ctx.Err() != nil {
		return speedPage{}, ctx.Err()
	}
	if readErr != nil || len(data) > 2<<20 || json.Unmarshal(data, &body) != nil || body.Value == nil || len(*body.Value) > 500 {
		slog.Warn("traffic.page.failed", "offset", page*500, "reason", "invalid_payload", "bytes", len(data))
		return speedPage{}, trafficFailure()
	}

	return speedPage{body.Updated, *body.Value}, nil
}

func (l *LTA) fetch(parent context.Context) (*speedSnapshot, error) {
	ctx, cancel := context.WithCancel(parent)
	var workers sync.WaitGroup
	// Wait for canceled requests before releasing the shared refresh flight.
	defer func() { cancel(); workers.Wait() }()
	pacer := &pagePacer{interval: l.pageInterval}
	type timedLink struct {
		link     speedLink
		observed time.Time
	}
	rowsByID := map[string]timedLink{}
	pages, rows := 0, 0
	consume := func(page speedPage) {
		pages++
		rows += len(page.rows)
		if len(page.rows) > 0 {
			revision, parseErr := time.ParseInLocation("2006-01-02 15:04:05", page.updated, singapore)
			now := l.now()
			// Page timestamps differ in the live feed. Skip old/invalid pages;
			// check each retained observation again at publication time.
			if parseErr == nil && !revision.After(now.Add(time.Minute)) && now.Sub(revision) <= 10*time.Minute {
				for _, raw := range page.rows {
					var row rawLink
					if json.Unmarshal(raw, &row) != nil || !row.valid() {
						continue
					}
					previous, exists := rowsByID[row.ID]
					if exists && !revision.After(previous.observed) {
						continue
					}
					rowsByID[row.ID] = timedLink{speedLink{row.ID, Point{float64(*row.StartLat), float64(*row.StartLon)}, Point{float64(*row.EndLat), float64(*row.EndLon)}, float64(*row.Minimum), float64(*row.Maximum)}, revision}
				}
			}
		}
	}
	finish := func() (*speedSnapshot, error) {
		fetched := l.now()
		var observed time.Time
		links := make([]speedLink, 0, len(rowsByID))
		for _, row := range rowsByID {
			if fetched.Sub(row.observed) >= 8*time.Minute {
				continue
			}
			links = append(links, row.link)
			if observed.IsZero() || row.observed.Before(observed) {
				observed = row.observed
			}
		}
		if len(links) == 0 {
			return nil, &Error{Message: "LTA speed observations are stale or invalid."}
		}
		sort.Slice(links, func(i, j int) bool { return links[i].id < links[j].id })
		expiry := fetched.Add(5 * time.Minute)
		if sourceExpiry := observed.Add(10 * time.Minute); sourceExpiry.Before(expiry) {
			expiry = sourceExpiry
		}
		slog.Info("traffic.collection", "pages", pages, "rows", rows, "fresh_links", len(links), "max_concurrent_requests", l.pageWorkers, "request_interval_ms", l.pageInterval.Milliseconds())
		return &speedSnapshot{links, observed.UTC(), fetched.UTC(), expiry.UTC()}, nil
	}
	// Fetch page zero first: small datasets need exactly one request.
	first, err := l.fetchPage(ctx, 0, pacer)
	if err != nil {
		return nil, err
	}
	consume(first)
	if len(first.rows) < 500 {
		return finish()
	}
	type result struct {
		index int
		page  speedPage
		err   error
	}
	concurrency := l.pageWorkers
	if concurrency < 1 {
		concurrency = 1
	}
	if concurrency > 4 {
		concurrency = 4
	}
	results := make(chan result, concurrency)
	pending := map[int]result{}
	next, dispatch := 1, 1
	for next < 300 {
		// Bound both active requests and out-of-order pages to this window.
		for dispatch < 300 && dispatch < next+concurrency {
			index := dispatch
			dispatch++
			workers.Add(1)
			go func() {
				defer workers.Done()
				page, err := l.fetchPage(ctx, index, pacer)
				select {
				case results <- result{index, page, err}:
				case <-ctx.Done():
				}
			}()
		}
		var received result
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case received = <-results:
		}
		// Stop all outstanding work immediately on a rate-limit response.
		var limited *Error
		if errors.As(received.err, &limited) && limited.Status == http.StatusTooManyRequests {
			return nil, received.err
		}
		pending[received.index] = received
		for {
			ready, ok := pending[next]
			if !ok {
				break
			}
			delete(pending, next)
			if ready.err != nil {
				return nil, ready.err
			}
			consume(ready.page)
			next++
			if len(ready.page.rows) < 500 {
				return finish()
			}
		}
	}
	return nil, &Error{Message: "LTA traffic snapshot exceeded the collection limit."}
}

func trafficFailure() *Error {
	return &Error{Message: "LTA traffic is unavailable."}
}

func (l *LTA) Enrich(ctx context.Context, route Route) Route {
	baseline := 0.0
	if route.DurationSeconds != nil {
		baseline = *route.DurationSeconds
	}
	if route.Source != "onemap" || baseline <= 0 {
		return route
	}
	info := &TrafficEstimate{Source: "lta", Status: "unavailable", BaselineSeconds: baseline, Message: "LTA traffic is unavailable; showing OneMap's baseline time."}
	route.Traffic = info
	if len(route.Geometry) < 2 {
		info.Status = "no_geometry"
		info.Message = "Route geometry is unavailable; showing OneMap's baseline time."
		return route
	}
	started := time.Now()
	l.mu.Lock()
	snapshot := l.snapshot
	if snapshot != nil && !l.now().Before(snapshot.expiresAt) {
		snapshot = nil
	}
	refreshing := l.flight != nil
	failure := l.lastError
	l.mu.Unlock()
	if snapshot == nil {
		if refreshing {
			info.Message = "LTA traffic is refreshing; showing OneMap's baseline time. Compare again shortly."
		} else if failure != nil {
			info.Message = failure.Error() + " Showing OneMap's baseline time."
		}
		return route
	}
	cached := true
	info = estimateTraffic(ctx, route, snapshot)
	route.Traffic = info
	if info.Status == "applied" {
		value := *info.EstimatedSeconds
		route.DurationSeconds = &value
		route.TrafficAware = true
	}
	slog.Info("traffic.estimate", "success", info.Status == "applied", "status", info.Status, "cache_hit", cached, "coverage", info.Coverage, "matched_links", info.MatchedLinks, "latency_ms", time.Since(started).Milliseconds())
	return route
}

// Run owns the refresh lifetime. Calling comparisons does not start additional
// downloads; closing the server cancels in-flight HTTP and pacing waits.
func (l *LTA) Run(ctx context.Context) {
	for ctx.Err() == nil {
		snapshot, _, err := l.loadSnapshot(ctx, true)
		delay := time.Minute
		if err == nil && snapshot != nil {
			delay = snapshot.expiresAt.Sub(l.now()) - 120*time.Second
			if delay < time.Minute {
				delay = time.Minute
			}
		}
		l.mu.Lock()
		if until := l.nextAttempt.Sub(l.now()); until > delay {
			delay = until
		}
		l.mu.Unlock()
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
