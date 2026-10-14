// Collect and cache LTA speed-band pages before applying them to a driving route.
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
	id      string
	start   Point
	end     Point
	minimum float64
	maximum float64
}

type speedSnapshot struct {
	links      []speedLink // Immutable after publication; contains public road data only.
	observedAt time.Time
	fetchedAt  time.Time
	expiresAt  time.Time
}

type TrafficEnricher interface {
	Enrich(context.Context, Route) Route
}

// LTA keeps one complete, bounded snapshot for five minutes and coalesces refreshes.
// One cancellable worker refreshes while the server runs. User routes and keys
// are never persisted or exposed.
type LTA struct {
	client       *http.Client
	endpoint     string
	key          string
	mu           sync.Mutex
	snapshot     *speedSnapshot
	flight       chan struct{}
	nextAttempt  time.Time
	lastError    error
	pageInterval time.Duration
	pageWorkers  int
	now          func() time.Time
}

// NewLTA validates the account key and creates a client for paginated speed-band snapshots.
func NewLTA(key string) (*LTA, error) {
	key = strings.TrimSpace(key)

	if key == "" {
		return nil, errors.New("LTA_ACCOUNT_KEY is required for LTA traffic")
	}

	return &LTA{
		client: &http.Client{
			Timeout: 5 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		endpoint:     speedBandsURL,
		key:          key,
		pageInterval: 200 * time.Millisecond,
		pageWorkers:  4,
		now:          time.Now,
	}, nil
}

// load requests a usable cached traffic snapshot without forcing a refresh.
func (trafficClient *LTA) load(ctx context.Context) (*speedSnapshot, bool, error) {
	return trafficClient.loadSnapshot(ctx, false)
}

// loadSnapshot shares a refresh between callers and publishes only complete, fresh snapshots.
func (trafficClient *LTA) loadSnapshot(ctx context.Context, force bool) (*speedSnapshot, bool, error) {
	for {
		contextError := ctx.Err()

		if contextError != nil {
			return nil, false, contextError
		}

		trafficClient.mu.Lock()

		now := trafficClient.now()

		if !force && trafficClient.snapshot != nil && now.Before(trafficClient.snapshot.expiresAt) {
			s := trafficClient.snapshot

			trafficClient.mu.Unlock()

			return s, true, nil
		}

		// Join the current refresh instead of issuing another full set of page requests.
		if trafficClient.flight != nil {
			done := trafficClient.flight

			trafficClient.mu.Unlock()

			select {
			case <-ctx.Done():
				return nil, false, ctx.Err()

			case <-done:
				continue
			}
		}

		if now.Before(trafficClient.nextAttempt) {
			err := trafficClient.lastError

			trafficClient.mu.Unlock()

			return nil, false, err
		}

		done := make(chan struct{})

		trafficClient.flight = done

		trafficClient.mu.Unlock()

		started := time.Now()

		refresh, cancel := context.WithTimeout(ctx, 120*time.Second)

		snapshot, err := trafficClient.fetch(refresh)

		cancel()

		trafficClient.mu.Lock()

		if err == nil {
			trafficClient.snapshot = snapshot

			trafficClient.lastError = nil
		} else {
			trafficClient.lastError = err
			// Short failure cooldown prevents every comparison repeatedly fetching a bad key/feed.
			until := trafficClient.now().Add(time.Minute)

			var failure *Error

			if errors.As(err, &failure) && failure.RetryAfter > time.Minute {
				until = trafficClient.now().Add(failure.RetryAfter)
			}

			trafficClient.nextAttempt = until
		}

		trafficClient.flight = nil

		close(done)

		trafficClient.mu.Unlock()

		rows := 0

		if snapshot != nil {
			rows = len(snapshot.links)
		}

		slog.Info(
			"traffic.refresh",
			"source",
			"lta",
			"success",
			err == nil,
			"latency_ms",
			time.Since(started).Milliseconds(),
			"links",
			rows,
		)

		return snapshot, false, err
	}
}

type numeric float64

// UnmarshalJSON accepts LTA numeric fields encoded either as numbers or numeric strings.
func (n *numeric) UnmarshalJSON(raw []byte) error {
	var v any

	unmarshalError := json.Unmarshal(raw, &v)

	if unmarshalError != nil {
		return unmarshalError
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

// valid checks the bounds and identifiers needed to use a speed-band road link.
func (link rawLink) valid() bool {
	if link.ID == "" || len(link.ID) > 100 || link.Band < 1 || link.Band > 8 || link.Minimum == nil || link.Maximum == nil || link.StartLat == nil || link.StartLon == nil || link.EndLat == nil || link.EndLon == nil {
		return false
	}

	if *link.Minimum < 0 || *link.Minimum > 150 || *link.Maximum < *link.Minimum || *link.Maximum > 200 {
		return false
	}

	for _, p := range []Point{
		{
			float64(*link.StartLat),
			float64(*link.StartLon),
		},
		{
			float64(*link.EndLat),
			float64(*link.EndLon),
		},
	} {
		if p.Latitude < 1 || p.Latitude > 1.6 || p.Longitude < 103.5 || p.Longitude > 104.6 {
			return false
		}
	}

	return *link.StartLat != *link.EndLat || *link.StartLon != *link.EndLon
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

// wait spaces request starts across all workers while allowing cancellation.
func (pacer *pagePacer) wait(ctx context.Context) error {
	for {
		contextError := ctx.Err()

		if contextError != nil {
			return contextError
		}

		pacer.mu.Lock()

		now := time.Now()

		if !pacer.next.After(now) {
			pacer.next = now.Add(pacer.interval)

			pacer.mu.Unlock()

			return nil
		}

		delay := pacer.next.Sub(now)

		pacer.mu.Unlock()

		timer := time.NewTimer(delay)

		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()

		case <-timer.C:
		}
	}
}

// fetchPage retries an eligible transient page failure within the bounded page retry budget.
func (trafficClient *LTA) fetchPage(ctx context.Context, page int, pacer *pagePacer) (speedPage, error) {
	for attempt := 0; attempt < 2; attempt++ {
		result, err := trafficClient.fetchPageOnce(ctx, page, pacer)

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

// fetchPageOnce paces one HTTP request and validates its status, timestamps and response body.
func (trafficClient *LTA) fetchPageOnce(ctx context.Context, page int, pacer *pagePacer) (speedPage, error) {
	waitError := pacer.wait(ctx)

	if waitError != nil {
		return speedPage{}, waitError
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s?$skip=%d", trafficClient.endpoint, page*500), nil)

	if err != nil {
		return speedPage{}, trafficFailure()
	}

	req.Header.Set("AccountKey", trafficClient.key)

	req.Header.Set("Accept", "application/json")

	response, err := trafficClient.client.Do(req)

	if err != nil {
		if ctx.Err() != nil {
			return speedPage{}, ctx.Err()
		}

		slog.Warn("traffic.page.failed", "offset", page*500, "reason", "transport")

		return speedPage{}, &Error{
			Status:  502,
			Message: "LTA traffic is unavailable.",
		}
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
			failure.RetryAfter = retryAfter(value, trafficClient.now())
		}

		if response.StatusCode == 401 || response.StatusCode == 403 {
			failure.Message = "LTA access failed. Check the backend API account key."
		}

		if response.StatusCode == 429 {
			failure.Status = http.StatusTooManyRequests

			failure.Message = "LTA traffic is temporarily rate limited."

			failure.RetryAfter = retryAfter(response.Header.Get("Retry-After"), trafficClient.now())
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

	return speedPage{
		body.Updated,
		*body.Value,
	}, nil
}

// fetch gathers pages with bounded workers and publishes links in page order after validation.
func (trafficClient *LTA) fetch(parent context.Context) (*speedSnapshot, error) {
	ctx, cancel := context.WithCancel(parent)

	var workers sync.WaitGroup
	// Wait for canceled requests before releasing the shared refresh flight.
	defer func() {
		cancel()

		workers.Wait()
	}()

	pacer := &pagePacer{interval: trafficClient.pageInterval}

	type timedLink struct {
		link     speedLink
		observed time.Time
	}

	// Keep the newest observation per road link as each page is consumed.
	rowsByID := map[string]timedLink{}

	pages := 0
	rows := 0

	consume := func(page speedPage) {
		pages++

		rows += len(page.rows)

		if len(page.rows) > 0 {
			revision, parseErr := time.ParseInLocation("2006-01-02 15:04:05", page.updated, singapore)

			now := trafficClient.now()
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

					rowsByID[row.ID] = timedLink{
						speedLink{
							id: row.ID,
							start: Point{
								Latitude:  float64(*row.StartLat),
								Longitude: float64(*row.StartLon),
							},
							end: Point{
								Latitude:  float64(*row.EndLat),
								Longitude: float64(*row.EndLon),
							},
							minimum: float64(*row.Minimum),
							maximum: float64(*row.Maximum),
						},
						revision,
					}
				}
			}
		}
	}

	// Recheck freshness at publication time and cap expiry by the oldest retained source timestamp.
	finish := func() (*speedSnapshot, error) {
		fetched := trafficClient.now()

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

		sort.Slice(links, func(i, j int) bool {
			return links[i].id < links[j].id
		})

		expiry := fetched.Add(5 * time.Minute)

		if sourceExpiry := observed.Add(10 * time.Minute); sourceExpiry.Before(expiry) {
			expiry = sourceExpiry
		}

		slog.Info(
			"traffic.collection",
			"pages",
			pages,
			"rows",
			rows,
			"fresh_links",
			len(links),
			"max_concurrent_requests",
			trafficClient.pageWorkers,
			"request_interval_ms",
			trafficClient.pageInterval.Milliseconds(),
		)

		return &speedSnapshot{
			links:      links,
			observedAt: observed.UTC(),
			fetchedAt:  fetched.UTC(),
			expiresAt:  expiry.UTC(),
		}, nil
	}
	// Fetch page zero first: small datasets need exactly one request.
	first, err := trafficClient.fetchPage(ctx, 0, pacer)

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

	concurrency := trafficClient.pageWorkers

	if concurrency < 1 {
		concurrency = 1
	}

	if concurrency > 4 {
		concurrency = 4
	}

	results := make(chan result, concurrency)

	pending := map[int]result{}

	next := 1
	dispatch := 1

	for next < 300 {
		// Bound both active requests and out-of-order pages to this window.
		for dispatch < 300 && dispatch < next+concurrency {
			index := dispatch

			dispatch++

			workers.Add(1)

			go func() {
				defer workers.Done()

				page, err := trafficClient.fetchPage(ctx, index, pacer)

				select {
				case results <- result{
					index,
					page,
					err,
				}:

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

// trafficFailure returns the sanitized public error used when LTA traffic cannot be collected.
func trafficFailure() *Error {
	return &Error{Message: "LTA traffic is unavailable."}
}

// Enrich applies available fresh traffic data while keeping the baseline route on failure.
func (trafficClient *LTA) Enrich(ctx context.Context, route Route) Route {
	baseline := 0.0

	if route.DurationSeconds != nil {
		baseline = *route.DurationSeconds
	}

	if route.Source != "onemap" || baseline <= 0 {
		return route
	}

	info := &TrafficEstimate{
		Source:          "lta",
		Status:          "unavailable",
		BaselineSeconds: baseline,
		Message:         "LTA traffic is unavailable; showing OneMap's baseline time.",
	}

	route.Traffic = info

	if len(route.Geometry) < 2 {
		info.Status = "no_geometry"

		info.Message = "Route geometry is unavailable; showing OneMap's baseline time."

		return route
	}

	started := time.Now()

	trafficClient.mu.Lock()

	snapshot := trafficClient.snapshot

	if snapshot != nil && !trafficClient.now().Before(snapshot.expiresAt) {
		snapshot = nil
	}

	refreshing := trafficClient.flight != nil

	failure := trafficClient.lastError

	trafficClient.mu.Unlock()

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

	slog.Info(
		"traffic.estimate",
		"success",
		info.Status == "applied",
		"status",
		info.Status,
		"cache_hit",
		cached,
		"coverage",
		info.Coverage,
		"matched_links",
		info.MatchedLinks,
		"latency_ms",
		time.Since(started).Milliseconds(),
	)

	return route
}

// Run owns the refresh lifetime. Calling comparisons does not start additional
// downloads; closing the server cancels in-flight HTTP and pacing waits.
func (trafficClient *LTA) Run(ctx context.Context) {
	for ctx.Err() == nil {
		snapshot, _, err := trafficClient.loadSnapshot(ctx, true)

		delay := time.Minute

		if err == nil && snapshot != nil {
			delay = snapshot.expiresAt.Sub(trafficClient.now()) - 120*time.Second

			if delay < time.Minute {
				delay = time.Minute
			}
		}

		trafficClient.mu.Lock()

		if until := trafficClient.nextAttempt.Sub(trafficClient.now()); until > delay {
			delay = until
		}

		trafficClient.mu.Unlock()

		timer := time.NewTimer(delay)

		select {
		case <-ctx.Done():
			timer.Stop()
			return

		case <-timer.C:
		}
	}
}
