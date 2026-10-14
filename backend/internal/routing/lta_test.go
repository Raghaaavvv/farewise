package routing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testLTA(t *testing.T, handler http.HandlerFunc) *LTA {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := NewLTA("test-only-account-key")
	if err != nil {
		t.Fatal(err)
	}
	client.client = server.Client()
	client.endpoint = server.URL
	client.pageInterval = 0
	return client
}
func testRow(id string) map[string]any {
	return map[string]any{"LinkID": id, "SpeedBand": 4, "MinimumSpeed": "30", "MaximumSpeed": "39", "StartLat": "1.3", "StartLon": "103.8", "EndLat": "1.3", "EndLon": "103.81"}
}
func writePage(w http.ResponseWriter, revision string, rows []map[string]any) {
	json.NewEncoder(w).Encode(map[string]any{"lastUpdatedTime": revision, "value": rows})
}
func revisionNow() string { return time.Now().In(singapore).Format("2006-01-02 15:04:05") }

func TestLTAPaginationCacheAndCoalescing(t *testing.T) {
	var calls atomic.Int32
	revision := revisionNow()
	client := testLTA(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("AccountKey") != "test-only-account-key" || r.Header.Get("Accept") != "application/json" {
			t.Error("wrong authentication")
		}
		switch r.URL.Query().Get("$skip") {
		case "0":
			rows := make([]map[string]any, 500)
			for i := range rows {
				rows[i] = testRow(fmt.Sprint(i))
			}
			writePage(w, revision, rows)
		case "500":
			writePage(w, revision, []map[string]any{testRow("last")})
		default:
			t.Error("incorrect pagination")
		}
	})
	var wait sync.WaitGroup
	for i := 0; i < 12; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			s, _, err := client.load(context.Background())
			if err != nil || len(s.links) != 501 {
				t.Errorf("incomplete snapshot: %v", err)
			}
		}()
	}
	wait.Wait()
	if calls.Load() != 2 {
		t.Fatalf("refetched shared snapshot %d times", calls.Load())
	}
	s, cached, err := client.load(context.Background())
	if err != nil || !cached || len(s.links) != 501 {
		t.Fatal("cache miss")
	}
	// Expiring a snapshot triggers another complete collection, not page accumulation.
	client.mu.Lock()
	client.snapshot.expiresAt = time.Now().Add(-time.Second)
	client.mu.Unlock()
	_, _, err = client.load(context.Background())
	if err != nil || calls.Load() != 4 {
		t.Fatal("expired snapshot was reused")
	}
}
func TestLTARejectsIncompleteStaleAndMalformedSnapshots(t *testing.T) {
	for _, kind := range []string{"stale", "missing value", "bad numbers", "empty"} {
		t.Run(kind, func(t *testing.T) {
			revision := revisionNow()
			client := testLTA(t, func(w http.ResponseWriter, r *http.Request) {
				switch kind {
				case "stale":
					writePage(w, time.Now().Add(-11*time.Minute).In(singapore).Format("2006-01-02 15:04:05"), []map[string]any{testRow("old")})
				case "missing value":
					fmt.Fprint(w, `{"lastUpdatedTime":"invalid"}`)
				case "bad numbers":
					row := testRow("bad")
					row["MinimumSpeed"] = "NaN"
					writePage(w, revision, []map[string]any{row})
				case "empty":
					writePage(w, revision, []map[string]any{})
				}
			})
			s, _, err := client.load(context.Background())
			if err == nil || s != nil || client.snapshot != nil {
				t.Fatal("accepted incomplete/invalid snapshot")
			}
		})
	}
}
func TestLTARetryAfterAndErrorsAreSanitized(t *testing.T) {
	for _, status := range []int{401, 403, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls atomic.Int32
			client := testLTA(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Retry-After", "120")
				w.WriteHeader(status)
				fmt.Fprint(w, "secret account key request URL")
			})
			_, _, err := client.load(context.Background())
			if err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatal("unsafe upstream error")
			}
			_, _, err = client.load(context.Background())
			if err == nil || calls.Load() != 1 {
				t.Fatal("failure cooldown did not protect upstream")
			}
			if status == 429 && time.Until(client.nextAttempt) < 119*time.Second {
				t.Fatal("Retry-After ignored")
			}
		})
	}
}
func TestLTACancellationReachesHTTPServer(t *testing.T) {
	started, canceled := make(chan struct{}), make(chan struct{})
	client := testLTA(t, func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done(); close(canceled) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { _, _, err := client.load(ctx); result <- err }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("request did not start")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("lost cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("request did not cancel")
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("HTTP server did not observe cancellation")
	}
}
func TestLTARejectsRedirectWithoutForwardingKey(t *testing.T) {
	var leaked atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Store(true) }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer source.Close()
	client, _ := NewLTA("test-only-account-key")
	client.endpoint = source.URL
	_, _, err := client.load(context.Background())
	if err == nil || leaked.Load() {
		t.Fatal("followed credential redirect")
	}
}

func TestLTAPageTimestampsAndStaleRows(t *testing.T) {
	fresh := revisionNow()
	older := time.Now().Add(-time.Minute).In(singapore).Format("2006-01-02 15:04:05")
	stale := time.Now().Add(-11 * time.Minute).In(singapore).Format("2006-01-02 15:04:05")
	client := testLTA(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("$skip") {
		case "0":
			rows := make([]map[string]any, 500)
			for i := range rows {
				rows[i] = testRow(fmt.Sprint(i))
			}
			writePage(w, fresh, rows)
		case "500":
			rows := make([]map[string]any, 500)
			for i := range rows {
				rows[i] = testRow("stale" + fmt.Sprint(i))
			}
			writePage(w, stale, rows)
		case "1000":
			writePage(w, older, []map[string]any{testRow("older-fresh")})
		}
	})
	result, _, err := client.load(context.Background())
	if err != nil || len(result.links) != 501 || result.observedAt.In(singapore).Format("2006-01-02 15:04:05") != older {
		t.Fatal("failed to normalize page freshness", err)
	}
}
func TestLTABaselineWhileWorkerRefreshesAndShutdownCancels(t *testing.T) {
	started, canceled := make(chan struct{}), make(chan struct{})
	client := testLTA(t, func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done(); close(canceled) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { client.Run(ctx); close(done) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	startedAt := time.Now()
	route := client.Enrich(context.Background(), eastRoute())
	if time.Since(startedAt) > 100*time.Millisecond || route.TrafficAware || route.Traffic.Status != "unavailable" || *route.DurationSeconds != 120 {
		t.Fatal("comparison blocked on cold collection")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop")
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("worker HTTP was not canceled")
	}
}
func TestLTAPublishesAdjustedDurationAndRetainsBaselineOnFailure(t *testing.T) {
	client := testLTA(t, func(w http.ResponseWriter, r *http.Request) {
		writePage(w, revisionNow(), []map[string]any{testRow("east")})
	})
	if _, _, err := client.load(context.Background()); err != nil {
		t.Fatal(err)
	}
	route := client.Enrich(context.Background(), eastRoute())
	if !route.TrafficAware || route.Traffic.Status != "applied" || route.Traffic.BaselineSeconds != 120 || *route.DurationSeconds == 120 {
		t.Fatal("did not apply traffic result")
	}
	original := eastRoute()
	original.Geometry = nil
	route = client.Enrich(context.Background(), original)
	if route.TrafficAware || *route.DurationSeconds != 120 || route.Traffic.Status != "no_geometry" {
		t.Fatal("lost honest baseline fallback")
	}
}

func TestLTARefreshKeepsPreviousFreshSnapshotUsable(t *testing.T) {
	started := make(chan struct{})
	var calls atomic.Int32
	client := testLTA(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			writePage(w, revisionNow(), []map[string]any{testRow("east")})
			return
		}
		close(started)
		<-r.Context().Done()
	})
	if _, _, err := client.load(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, _, err := client.loadSnapshot(ctx, true); done <- err }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("forced refresh reused cache")
	}
	route := client.Enrich(context.Background(), eastRoute())
	if !route.TrafficAware {
		t.Fatal("refresh discarded previous valid snapshot")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("refresh did not cancel")
	}
	if !client.Enrich(context.Background(), eastRoute()).TrafficAware {
		t.Fatal("failed refresh discarded valid snapshot")
	}
}
