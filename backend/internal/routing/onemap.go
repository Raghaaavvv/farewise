// Package routing fetches road distances independently of fare providers.
package routing

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Point struct{ Latitude, Longitude float64 }

type Route struct {
	Source          string   `json:"source"`
	DistanceMeters  float64  `json:"distanceMeters"`
	DurationSeconds *float64 `json:"durationSeconds"`
	TrafficAware    bool     `json:"trafficAware"`
	// Geometry is used only inside the backend; never returned or persisted.
	Geometry []Point          `json:"-"`
	Traffic  *TrafficEstimate `json:"traffic,omitempty"`
}

type Router interface {
	Source() string
	DrivingRoute(context.Context, Point, Point) (Route, error)
}

// Error messages deliberately omit request URLs, coordinates and credentials.
type Error struct {
	Status     int
	Message    string
	RetryAfter time.Duration
}

func (e *Error) Error() string { return e.Message }

// OneMap performs no automatic retries: a failed route must not silently turn
// into an approximate distance. Requests are paced at two/second with at most
// two in flight; 429 Retry-After applies to subsequent calls on this instance.
type OneMap struct {
	client          *http.Client
	endpoint, token string
	slots           chan struct{}
	mu              sync.Mutex
	next, cooldown  time.Time
}

func NewOneMap(token string) (*OneMap, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("ONEMAP_TOKEN is required when FAREWISE_ROUTING_PROVIDER=onemap")
	}
	return &OneMap{client: &http.Client{Timeout: 3 * time.Second,
		// Never forward the token to a redirected host.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		endpoint: "https://www.onemap.gov.sg/api/public/routingsvc/route",
		token:    token, slots: make(chan struct{}, 2)}, nil
}

func (*OneMap) Source() string { return "onemap" }

func (o *OneMap) wait(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		o.mu.Lock()
		now := time.Now()
		ready := o.next
		if o.cooldown.After(ready) {
			ready = o.cooldown
		}
		if !ready.After(now) {
			o.next = now.Add(500 * time.Millisecond)
			o.mu.Unlock()
			return nil
		}
		delay := ready.Sub(now)
		o.mu.Unlock()
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (o *OneMap) DrivingRoute(ctx context.Context, origin, destination Point) (Route, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	select {
	case o.slots <- struct{}{}:
		defer func() { <-o.slots }()
	case <-ctx.Done():
		return Route{}, ctx.Err()
	}
	if err := o.wait(ctx); err != nil {
		return Route{}, err
	}
	query := url.Values{"start": {pointString(origin)}, "end": {pointString(destination)}, "routeType": {"drive"}}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, o.endpoint+"?"+query.Encode(), nil)
	if err != nil {
		return Route{}, &Error{Status: 502, Message: "Could not prepare the driving route request."}
	}
	request.Header.Set("Authorization", o.token)
	request.Header.Set("Accept", "application/json")
	response, err := o.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return Route{}, ctx.Err()
		}
		return Route{}, &Error{Status: 502, Message: "OneMap is unreachable. Try the route again."}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		failure := &Error{Status: 502, Message: "OneMap could not calculate this driving route. Try again."}
		switch response.StatusCode {
		case 401, 403:
			failure.Status = 503
			failure.Message = "OneMap access failed. Check or renew the backend ONEMAP_TOKEN."
		case 400, 404:
			failure.Status = 422
			failure.Message = "OneMap could not find a driving route. Choose road-accessible locations in Singapore."
		case 429:
			failure.Status = 503
			failure.Message = "OneMap's request limit was reached. Wait before comparing again."
			failure.RetryAfter = retryAfter(response.Header.Get("Retry-After"), time.Now())
			o.mu.Lock()
			if until := time.Now().Add(failure.RetryAfter); until.After(o.cooldown) {
				o.cooldown = until
			}
			o.mu.Unlock()
		}
		return Route{}, failure
	}
	var body struct {
		Geometry string `json:"route_geometry"`
		Status   *int   `json:"status"`
		Summary  *struct {
			Distance *float64 `json:"total_distance"`
			Duration *float64 `json:"total_time"`
		} `json:"route_summary"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 2<<20))
	if err := decoder.Decode(&body); err != nil {
		if ctx.Err() != nil {
			return Route{}, ctx.Err()
		}
		return Route{}, invalidResponse()
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		if ctx.Err() != nil {
			return Route{}, ctx.Err()
		}
		return Route{}, invalidResponse()
	}
	if err := ctx.Err(); err != nil {
		return Route{}, err
	}
	if body.Status == nil {
		return Route{}, invalidResponse()
	}
	if *body.Status != 0 {
		return Route{}, &Error{Status: 422, Message: "OneMap could not find a driving route for these locations."}
	}
	if body.Summary == nil || !validValue(body.Summary.Distance) || !validValue(body.Summary.Duration) {
		return Route{}, invalidResponse()
	}
	geometry, _ := decodePolyline(body.Geometry)
	return Route{Source: "onemap", DistanceMeters: *body.Summary.Distance, DurationSeconds: body.Summary.Duration, Geometry: geometry}, nil
}

func pointString(p Point) string {
	return strconv.FormatFloat(p.Latitude, 'f', -1, 64) + "," + strconv.FormatFloat(p.Longitude, 'f', -1, 64)
}
func validValue(v *float64) bool {
	return v != nil && !math.IsNaN(*v) && !math.IsInf(*v, 0) && *v >= 0 && *v <= 9007199254740991
}
func invalidResponse() error {
	return &Error{Status: 502, Message: "OneMap returned an invalid route. Try again."}
}
func retryAfter(value string, now time.Time) time.Duration {
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds >= 0 {
		// Saturate extremely large values rather than overflowing to a negative wait.
		if seconds > int64((time.Duration(1<<63-1))/time.Second) {
			return time.Duration(1<<63 - 1)
		}
		return time.Duration(seconds) * time.Second
	}
	if date, err := http.ParseTime(value); err == nil && date.After(now) {
		return date.Sub(now)
	}
	return time.Second
}
