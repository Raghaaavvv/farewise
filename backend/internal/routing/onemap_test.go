// Use a fake OneMap server to check response validation, rate limits and request cancellation.
package routing

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testRouter(t *testing.T, handler http.HandlerFunc) (*OneMap, *httptest.Server) {
	t.Helper()

	server := httptest.NewServer(handler)

	t.Cleanup(server.Close)

	router, err := NewOneMap("test-only-token")

	if err != nil {
		t.Fatal(err)
	}

	router.endpoint, router.client = server.URL, server.Client()

	return router, server
}

func TestOneMapDrivingRouteContract(t *testing.T) {
	router, _ := testRouter(t, func(writer http.ResponseWriter, httpRequest *http.Request) {
		if httpRequest.Method != "GET" || httpRequest.URL.Query().Get("routeType") != "drive" || httpRequest.URL.Query().Get("start") != "1.357,103.987" || httpRequest.URL.Query().Get("end") != "1.304,103.832" || httpRequest.Header.Get("Authorization") != "test-only-token" {
			t.Errorf("incorrect routing request")
		}

		fmt.Fprint(writer, `{"status":0,"route_summary":{"total_distance":23456,"total_time":1234.5},"route_geometry":"unused"}`)
	})

	route, err := router.DrivingRoute(context.Background(), Point{
		Latitude:  1.357,
		Longitude: 103.987,
	}, Point{
		Latitude:  1.304,
		Longitude: 103.832,
	})

	if err != nil {
		t.Fatal(err)
	}

	if route.Source != "onemap" || route.DistanceMeters != 23456 || route.DurationSeconds == nil || *route.DurationSeconds != 1234.5 || route.TrafficAware {
		t.Fatalf("incorrect result: %+v", route)
	}
}

func TestOneMapInvalidResponsesAndErrors(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   int
	}{
		{
			"missing status",
			200,
			`{"route_summary":{"total_distance":10,"total_time":5}}`,
			502,
		},
		{
			"missing distance",
			200,
			`{"status":0,"route_summary":{"total_time":5}}`,
			502,
		},
		{
			"missing time",
			200,
			`{"status":0,"route_summary":{"total_distance":10}}`,
			502,
		},
		{
			"negative distance",
			200,
			`{"status":0,"route_summary":{"total_distance":-1,"total_time":5}}`,
			502,
		},
		{
			"negative time",
			200,
			`{"status":0,"route_summary":{"total_distance":10,"total_time":-1}}`,
			502,
		},
		{
			"string distance",
			200,
			`{"status":0,"route_summary":{"total_distance":"10","total_time":5}}`,
			502,
		},
		{
			"no route",
			200,
			`{"status":207}`,
			422,
		},
		{
			"not JSON",
			200,
			`<html>secret error</html>`,
			502,
		},
		{
			"extra JSON",
			200,
			`{"status":0,"route_summary":{"total_distance":10,"total_time":5}} {}`,
			502,
		},
		{
			"expired token",
			401,
			`secret token and coordinates`,
			503,
		},
		{
			"denied token",
			403,
			`secret`,
			503,
		},
		{
			"outside Singapore",
			400,
			`secret`,
			422,
		},
		{
			"missing route",
			404,
			`secret`,
			422,
		},
		{
			"upstream unavailable",
			500,
			`secret`,
			502,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router, _ := testRouter(t, func(writer http.ResponseWriter, httpRequest *http.Request) {
				writer.WriteHeader(tc.status)

				fmt.Fprint(writer, tc.body)
			})

			_, err := router.DrivingRoute(context.Background(), Point{
				Latitude:  1.3,
				Longitude: 103.8,
			}, Point{
				Latitude:  1.4,
				Longitude: 103.9,
			})

			var failure *Error

			if !errors.As(err, &failure) || failure.Status != tc.want || strings.Contains(err.Error(), "secret") {
				t.Fatalf("unexpected failure: %v", err)
			}
		})
	}
}

func TestOneMapCancellationReachesHTTPServer(t *testing.T) {
	started, canceled := make(chan struct{}), make(chan struct{})

	router, _ := testRouter(t, func(writer http.ResponseWriter, httpRequest *http.Request) {
		close(started)

		<-httpRequest.Context().Done()

		close(canceled)
	})

	ctx, cancel := context.WithCancel(context.Background())

	defer cancel()

	result := make(chan error, 1)

	go func() {
		_, err := router.DrivingRoute(ctx, Point{
			Latitude:  1.3,
			Longitude: 103.8,
		}, Point{
			Latitude:  1.4,
			Longitude: 103.9,
		})

		result <- err
	}()

	select {
	case <-started:

	case <-time.After(time.Second):
		t.Fatal("HTTP call did not start")
	}

	cancel()

	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("lost cancellation: %v", err)
		}

	case <-time.After(time.Second):
		t.Fatal("call did not exit")
	}

	select {
	case <-canceled:

	case <-time.After(time.Second):
		t.Fatal("HTTP server did not observe cancellation")
	}

	if len(router.slots) != 0 {
		t.Fatal("leaked concurrency permit")
	}
}

func TestOneMap429AppliesSharedCooldown(t *testing.T) {
	var calls atomic.Int32

	router, _ := testRouter(t, func(writer http.ResponseWriter, httpRequest *http.Request) {
		calls.Add(1)

		writer.Header().Set("Retry-After", "2")

		writer.WriteHeader(429)
	})

	_, err := router.DrivingRoute(context.Background(), Point{
		Latitude:  1.3,
		Longitude: 103.8,
	}, Point{
		Latitude:  1.4,
		Longitude: 103.9,
	})

	var failure *Error

	if !errors.As(err, &failure) || failure.RetryAfter != 2*time.Second {
		t.Fatalf("lost Retry-After: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)

	defer cancel()

	_, err = router.DrivingRoute(ctx, Point{
		Latitude:  1.3,
		Longitude: 103.8,
	}, Point{
		Latitude:  1.4,
		Longitude: 103.9,
	})

	if !errors.Is(err, context.DeadlineExceeded) || calls.Load() != 1 {
		t.Fatalf("sent request during cooldown: calls=%d, err=%v", calls.Load(), err)
	}
}

func TestOneMapPacingAndCancellationBeforeRequest(t *testing.T) {
	var calls atomic.Int32

	router, _ := testRouter(t, func(writer http.ResponseWriter, httpRequest *http.Request) {
		calls.Add(1)

		fmt.Fprint(writer, `{"status":0,"route_summary":{"total_distance":0,"total_time":0}}`)
	})

	route, err := router.DrivingRoute(context.Background(), Point{
		Latitude:  1.3,
		Longitude: 103.8,
	}, Point{
		Latitude:  1.4,
		Longitude: 103.9,
	})

	if err != nil || route.DistanceMeters != 0 || *route.DurationSeconds != 0 {
		t.Fatalf("zero is a valid value: %+v %v", route, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)

	defer cancel()

	_, err = router.DrivingRoute(ctx, Point{
		Latitude:  1.3,
		Longitude: 103.8,
	}, Point{
		Latitude:  1.4,
		Longitude: 103.9,
	})

	if !errors.Is(err, context.DeadlineExceeded) || calls.Load() != 1 {
		t.Fatal("pacing did not respect context")
	}

	canceled, stop := context.WithCancel(context.Background())

	stop()

	_, err = router.DrivingRoute(canceled, Point{}, Point{})

	if !errors.Is(err, context.Canceled) || calls.Load() != 1 {
		t.Fatal("canceled request contacted OneMap")
	}
}

func TestRoutingTokenAndRetryAfter(t *testing.T) {
	_, newOneMapError := NewOneMap(" ")

	if newOneMapError == nil {
		t.Fatal("accepted missing token")
	}

	now := time.Now().UTC().Truncate(time.Second)

	if retryAfter(now.Add(5*time.Second).Format(http.TimeFormat), now) != 5*time.Second {
		t.Fatal("HTTP-date Retry-After not parsed")
	}

	for _, value := range []string{
		"-1",
		"bad",
		"9223372036854775807",
	} {
		if retryAfter(value, now) <= 0 {
			t.Fatal("overflow or invalid cooldown")
		}
	}
}

func TestOneMapConcurrencyLimitAndCanceledQueue(t *testing.T) {
	var calls, active, maximum atomic.Int32

	started := make(chan struct{}, 3)

	router, _ := testRouter(t, func(writer http.ResponseWriter, httpRequest *http.Request) {
		calls.Add(1)

		running := active.Add(1)

		for old := maximum.Load(); running > old; old = maximum.Load() {
			if maximum.CompareAndSwap(old, running) {
				break
			}
		}

		defer active.Add(-1)

		started <- struct{}{}

		<-httpRequest.Context().Done()
	})

	ctx, cancel := context.WithCancel(context.Background())

	defer cancel()

	done := make(chan error, 2)

	for i := 0; i < 2; i++ {
		go func() {
			_, err := router.DrivingRoute(ctx, Point{
				Latitude:  1.3,
				Longitude: 103.8,
			}, Point{
				Latitude:  1.4,
				Longitude: 103.9,
			})

			done <- err
		}()
	}

	for i := 0; i < 2; i++ {
		select {
		case <-started:

		case <-time.After(2 * time.Second):
			t.Fatal("requests did not enter HTTP server")
		}
	}

	queued, stop := context.WithTimeout(context.Background(), 50*time.Millisecond)

	defer stop()

	_, err := router.DrivingRoute(queued, Point{
		Latitude:  1.3,
		Longitude: 103.8,
	}, Point{
		Latitude:  1.4,
		Longitude: 103.9,
	})

	if !errors.Is(err, context.DeadlineExceeded) || calls.Load() != 2 || maximum.Load() != 2 {
		t.Fatal("concurrency limit or cancellation failed")
	}

	cancel()

	for i := 0; i < 2; i++ {
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("lost cancellation: %v", err)
			}

		case <-time.After(time.Second):
			t.Fatal("request did not exit")
		}
	}

	if len(router.slots) != 0 {
		t.Fatal("permits were not released")
	}
}

func TestOneMapDoesNotForwardTokenOnRedirect(t *testing.T) {
	var calls atomic.Int32

	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, httpRequest *http.Request) {
		calls.Add(1)
	}))

	defer target.Close()

	router, _ := testRouter(t, func(writer http.ResponseWriter, httpRequest *http.Request) {
		http.Redirect(writer, httpRequest, target.URL, http.StatusFound)
	})
	// Keep the production redirect policy; only the local endpoint is replaced.
	production, _ := NewOneMap("test-only-token")

	router.client = production.client

	_, err := router.DrivingRoute(context.Background(), Point{
		Latitude:  1.3,
		Longitude: 103.8,
	}, Point{
		Latitude:  1.4,
		Longitude: 103.9,
	})

	if err == nil || calls.Load() != 0 {
		t.Fatal("followed redirect with private token")
	}
}

func TestOneMapDeadlineWhileReadingResponse(t *testing.T) {
	started, canceled := make(chan struct{}), make(chan struct{})

	router, _ := testRouter(t, func(writer http.ResponseWriter, httpRequest *http.Request) {
		writer.Header().Set("Content-Type", "application/json")

		writer.WriteHeader(http.StatusOK)

		writer.(http.Flusher).Flush()

		close(started)

		<-httpRequest.Context().Done()

		close(canceled)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)

	defer cancel()

	_, err := router.DrivingRoute(ctx, Point{
		Latitude:  1.3,
		Longitude: 103.8,
	}, Point{
		Latitude:  1.4,
		Longitude: 103.9,
	})

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("response-body timeout became a parse error: %v", err)
	}

	select {
	case <-started:

	default:
		t.Fatal("response headers did not arrive")
	}

	select {
	case <-canceled:

	case <-time.After(time.Second):
		t.Fatal("response-body cancellation did not reach server")
	}

	if len(router.slots) != 0 {
		t.Fatal("leaked permit on response-body timeout")
	}
}
