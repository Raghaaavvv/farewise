package fare

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

type Service struct {
	providers []Provider
	timeout   time.Duration
	slots     chan struct{}
	logger    *slog.Logger
}

type ServiceOption func(*Service)

func WithMaxConcurrentProviders(limit int) ServiceOption {
	if limit < 1 {
		panic("provider concurrency limit must be positive")
	}
	return func(s *Service) { s.slots = make(chan struct{}, limit) }
}

func WithServiceLogger(logger *slog.Logger) ServiceOption {
	return func(s *Service) {
		if logger != nil {
			s.logger = logger
		}
	}
}

func NewService(providers []Provider, timeout time.Duration, options ...ServiceOption) *Service {
	s := &Service{providers: append([]Provider(nil), providers...), timeout: timeout,
		slots: make(chan struct{}, 4), logger: slog.Default()}
	for _, option := range options {
		option(s)
	}
	return s
}

func Validate(request Request) (Trip, error) {
	pickup, err := resolveLocation(request.PickupID, request.PickupCoordinates)
	if err != nil {
		return Trip{}, err
	}
	dropoff, err := resolveLocation(request.DropoffID, request.DropoffCoordinates)
	if err != nil {
		return Trip{}, err
	}
	if pickup.Latitude == dropoff.Latitude && pickup.Longitude == dropoff.Longitude {
		return Trip{}, errors.New("pickup and destination must be different")
	}
	if request.Passengers < 1 || request.Passengers > 6 {
		return Trip{}, errors.New("passengers must be between 1 and 6")
	}
	if request.RideType != "standard" && request.RideType != "six" {
		return Trip{}, errors.New("choose standard or six for rideType")
	}
	if request.Passengers > 4 && request.RideType != "six" {
		return Trip{}, errors.New("choose a 6-seater for more than 4 passengers")
	}
	switch request.Scenario {
	case "normal", "busy", "rain", "outage":
	default:
		return Trip{}, errors.New("unknown demo scenario")
	}
	// Straight-line distance times an illustrative road factor. Not real routing.
	distance := math.Round(haversine(pickup, dropoff)*1.25*10) / 10
	return Trip{pickup, dropoff, request.Passengers, request.RideType, request.Scenario, distance}, nil
}

func haversine(a, b Location) float64 {
	rad := math.Pi / 180
	dlat, dlon := (b.Latitude-a.Latitude)*rad, (b.Longitude-a.Longitude)*rad
	x := math.Sin(dlat/2)*math.Sin(dlat/2) + math.Cos(a.Latitude*rad)*math.Cos(b.Latitude*rad)*math.Sin(dlon/2)*math.Sin(dlon/2)
	// Rounding near antipodal points can otherwise make a square root negative.
	x = math.Max(0, math.Min(1, x))
	return 6371 * 2 * math.Atan2(math.Sqrt(x), math.Sqrt(1-x))
}

func (s *Service) Compare(ctx context.Context, trip Trip) Comparison {
	now := time.Now().UTC()
	// One total budget includes worker queueing and the shared semaphore wait.
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	comparison := Comparison{
		Mode: "demo", Notice: "All fares and pickup times are simulated. No provider has been contacted.",
		Trip: trip, Quotes: make([]Quote, 0, len(s.providers)), CollectedAt: now,
		ExpiresAt: now.Add(30 * time.Second),
	}
	jobs := make(chan Provider, len(s.providers))
	results := make(chan []Quote, len(s.providers))
	for _, provider := range s.providers {
		jobs <- provider
	}
	close(jobs)
	var workers sync.WaitGroup
	for i := 0; i < min(len(s.providers), cap(s.slots)); i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for provider := range jobs {
				results <- s.callProvider(ctx, provider, trip)
			}
		}()
	}
	// Direct calls keep permits until the adapter actually exits. No detached
	// goroutine hides an adapter that ignores context; honoring it is required.
	workers.Wait()
	close(results)
	finishedAt := time.Now()
	var oldestFetchedAt, earliestExpiry time.Time
	for quotes := range results {
		for i := range quotes {
			quote := &quotes[i]
			// A short-lived product can expire while another adapter is running.
			if quote.Status != "unavailable" && !quote.ExpiresAt.After(finishedAt) {
				quote.Status = "expired"
			}
			if quote.Fare == nil || quote.Status == "unavailable" {
				continue
			}
			if oldestFetchedAt.IsZero() || quote.FetchedAt.Before(oldestFetchedAt) {
				oldestFetchedAt = quote.FetchedAt
			}
			if earliestExpiry.IsZero() || quote.ExpiresAt.Before(earliestExpiry) {
				earliestExpiry = quote.ExpiresAt
			}
		}
		comparison.Quotes = append(comparison.Quotes, quotes...)
	}
	if !oldestFetchedAt.IsZero() {
		comparison.CollectedAt = oldestFetchedAt
		comparison.ExpiresAt = earliestExpiry
	}
	sort.Slice(comparison.Quotes, func(i, j int) bool {
		a, b := comparison.Quotes[i], comparison.Quotes[j]
		aPriced, bPriced := a.Fare != nil && a.Status != "expired", b.Fare != nil && b.Status != "expired"
		if aPriced != bPriced {
			return aPriced
		}
		if aPriced && a.Fare.MinFare != b.Fare.MinFare {
			return a.Fare.MinFare < b.Fare.MinFare
		}
		if a.ProviderID != b.ProviderID {
			return a.ProviderID < b.ProviderID
		}
		return a.ProductID < b.ProductID
	})
	return comparison
}

func (s *Service) callProvider(ctx context.Context, p Provider, trip Trip) []Quote {
	start := time.Now()
	var quotes []Quote
	var err error
	select {
	case <-ctx.Done():
		err = ctx.Err()
	case s.slots <- struct{}{}:
		if err = ctx.Err(); err == nil {
			quotes, err = p.Estimate(ctx, trip)
		}
		<-s.slots
	}
	result, invalid := normalizeQuotes(p, trip, quotes, err, time.Now().UTC())
	logProvider(ctx, s.logger, "provider.result", p.ID(), "not_applicable", start, result, err, "invalid_products", invalid)
	return result
}

func normalizeQuotes(p Provider, trip Trip, quotes []Quote, err error, now time.Time) ([]Quote, int) {
	result := make([]Quote, 0, len(quotes))
	seen := make(map[string]bool)
	invalid := 0
	for _, q := range cloneQuotes(quotes) {
		if (q.ProviderID != "" && q.ProviderID != p.ID()) || strings.TrimSpace(q.ProductID) == "" ||
			strings.TrimSpace(q.ServiceName) == "" || !validProduct(q.ProductType) || q.Currency != "SGD" ||
			q.Capacity < 1 || q.Capacity > 6 || q.FetchedAt.IsZero() || q.FetchedAt.After(now.Add(time.Second)) ||
			!q.ExpiresAt.After(q.FetchedAt) || seen[q.ProductID] ||
			!validSource(q.Source) {
			invalid++
			continue
		}
		q.ProviderID, q.ProviderName = p.ID(), p.Name()
		if q.Fare != nil {
			q.Fare.IsEstimateRange = q.Fare.MinFare != q.Fare.MaxFare
			if !validFare(q.Fare) {
				q.Fare = nil
				invalid++
			}
		}
		if q.ETAMinutes != nil && (*q.ETAMinutes < 0 || *q.ETAMinutes > 1<<53-1) {
			q.ETAMinutes = nil
			invalid++
		}
		if q.Status == "unavailable" || q.Capacity < trip.Passengers {
			q.Status, q.Fare, q.ETAMinutes = "unavailable", nil, nil
		} else if !q.ExpiresAt.After(now) {
			q.Status = "expired"
		} else if q.Fare == nil && q.ETAMinutes == nil {
			q.Status = "unavailable"
		} else if q.Fare == nil || q.ETAMinutes == nil {
			q.Status = "partial"
		} else {
			q.Status = "available"
		}
		seen[q.ProductID] = true
		result = append(result, q)
	}
	if len(result) == 0 {
		message := "This provider has no usable quote. Please try again later."
		if errors.Is(err, context.DeadlineExceeded) {
			message = "This provider could not complete within the time limit."
		}
		if errors.Is(err, context.Canceled) {
			message = "This comparison was canceled."
		}
		capacity := 4
		if trip.RideType == "six" {
			capacity = 6
		}
		result = append(result, Quote{ProviderID: p.ID(), ProviderName: p.Name(), ProductID: "unavailable", ProductType: ProductStandard,
			ServiceName: "No quote available", Status: "unavailable", Currency: "SGD", Source: "unknown", Capacity: capacity,
			FetchedAt: now, ExpiresAt: now.Add(30 * time.Second), Message: message})
	}
	return result, invalid
}
