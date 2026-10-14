package routing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
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
	now           func() time.Time
}

func NewLTA(key string) (*LTA, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, errors.New("LTA_ACCOUNT_KEY is required for LTA traffic")
	}
	return &LTA{client: &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, endpoint: speedBandsURL, key: key, pageInterval: 200 * time.Millisecond, now: time.Now}, nil
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
func (l *LTA) fetch(ctx context.Context) (*speedSnapshot, error) {
	type timedLink struct {
		link     speedLink
		observed time.Time
	}
	rowsByID := map[string]timedLink{}
	for page := 0; page < 300; page++ {
		if page > 0 {
			timer := time.NewTimer(l.pageInterval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s?$skip=%d", l.endpoint, page*500), nil)
		if err != nil {
			return nil, trafficFailure()
		}
		req.Header.Set("AccountKey", l.key)
		req.Header.Set("Accept", "application/json")
		response, err := l.client.Do(req)
		if err != nil {
			slog.Warn("traffic.page.failed", "offset", page*500, "reason", "transport")
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, trafficFailure()
		}
		var body struct {
			Updated string             `json:"lastUpdatedTime"`
			Value   *[]json.RawMessage `json:"value"`
		}
		if response.StatusCode != 200 {
			slog.Warn("traffic.page.failed", "offset", page*500, "reason", "http_status", "status", response.StatusCode)
			failure := trafficFailure()
			if response.StatusCode == 401 || response.StatusCode == 403 {
				failure.Message = "LTA access failed. Check the backend API account key."
			}
			if response.StatusCode == 429 {
				failure.Message = "LTA traffic is temporarily rate limited."
				failure.RetryAfter = retryAfter(response.Header.Get("Retry-After"), l.now())
			}
			response.Body.Close()
			return nil, failure
		}
		limited := io.LimitReader(response.Body, (2<<20)+1)
		data, readErr := io.ReadAll(limited)
		response.Body.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if readErr != nil || len(data) > 2<<20 || json.Unmarshal(data, &body) != nil || body.Value == nil || len(*body.Value) > 500 {
			slog.Warn("traffic.page.failed", "offset", page*500, "reason", "invalid_payload", "bytes", len(data))
			return nil, trafficFailure()
		}
		if len(*body.Value) > 0 {
			revision, parseErr := time.ParseInLocation("2006-01-02 15:04:05", body.Updated, singapore)
			now := l.now()
			// Page timestamps differ in the live feed. Skip old/invalid pages;
			// check each retained observation again at publication time.
			if parseErr == nil && !revision.After(now.Add(time.Minute)) && now.Sub(revision) <= 10*time.Minute {
				for _, raw := range *body.Value {
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
		if len(*body.Value) < 500 {
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
			return &speedSnapshot{links, observed.UTC(), fetched.UTC(), expiry.UTC()}, nil
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
