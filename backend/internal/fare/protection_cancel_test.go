package fare

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTP429LongRetryAfterIsNotCappedToBackoff(t *testing.T) {
	for _, header := range []string{"60", time.Now().Add(time.Hour).UTC().Format(http.TimeFormat)} {
		t.Run(header, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Retry-After", header)
				w.WriteHeader(http.StatusTooManyRequests)
			}))
			defer server.Close()
			p := protectedForTest(t, testHTTPProvider(server), fastPolicy())
			ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
			defer cancel()
			_, err := p.Estimate(ctx, sampleTrip(t))
			if !errors.Is(err, context.DeadlineExceeded) || calls.Load() != 1 {
				t.Fatalf("long Retry-After must outlast the caller, without early retry: calls=%d, err=%v", calls.Load(), err)
			}
		})
	}
}

func TestFlightCapacityAndLeaderCancellation(t *testing.T) {
	policy := fastPolicy()
	policy.MaxInFlight = 1
	var calls atomic.Int32
	started := make(chan struct{}, 2)
	p := protectedForTest(t, functionProvider{"cancel", func(ctx context.Context, _ Trip) (Quote, error) {
		calls.Add(1)
		started <- struct{}{}
		<-ctx.Done()
		return Quote{}, ctx.Err()
	}}, policy)
	trip := sampleTrip(t)
	leader, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { _, err := p.Estimate(leader, trip); result <- err }()
	<-started
	other := trip
	other.Passengers++
	if _, err := p.Estimate(context.Background(), other); !errors.Is(err, ErrProviderBusy) {
		t.Fatalf("distinct trip should be rejected when the queue is full: %v", err)
	}
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("leader did not cancel: %v", err)
	}
	ctx, stop := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer stop()
	_, err := p.Estimate(ctx, trip)
	if !errors.Is(err, context.DeadlineExceeded) || calls.Load() != 2 {
		t.Fatal("canceled flight must free its slot and must not be cached")
	}
}

func TestEachRetryConsumesAToken(t *testing.T) {
	var calls atomic.Int32
	policy := fastPolicy()
	policy.RequestsPerSecond, policy.Burst = 1, 1
	p := protectedForTest(t, functionProvider{"retry", func(context.Context, Trip) (Quote, error) {
		calls.Add(1)
		return Quote{}, &ProviderHTTPError{StatusCode: 503}
	}}, policy)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := p.Estimate(ctx, sampleTrip(t))
	if !errors.Is(err, context.DeadlineExceeded) || calls.Load() != 1 {
		t.Fatalf("retry bypassed the depleted token bucket: calls=%d, err=%v", calls.Load(), err)
	}
}
