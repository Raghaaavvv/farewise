// Validate trips and aggregate provider quotes with bounded concurrency and cancellation.
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

	"farewise/backend/internal/routing"
)

type Service struct {
	providers []Provider
	timeout   time.Duration
	slots     chan struct{}
	logger    *slog.Logger
}

type ServiceOption func(*Service)

// WithMaxConcurrentProviders sets the shared maximum number of active provider calls.
func WithMaxConcurrentProviders(limit int) ServiceOption {
	if limit < 1 {
		panic("provider concurrency limit must be positive")
	}

	return func(service *Service) {
		service.slots = make(chan struct{}, limit)
	}
}

// WithServiceLogger replaces the service logger when a non-nil logger is supplied.
func WithServiceLogger(logger *slog.Logger) ServiceOption {
	return func(service *Service) {
		if logger != nil {
			service.logger = logger
		}
	}
}

// NewService copies the provider list and applies the optional concurrency and logging settings.
func NewService(providers []Provider, timeout time.Duration, options ...ServiceOption) *Service {
	s := &Service{
		providers: append([]Provider(nil), providers...),
		timeout:   timeout,
		slots:     make(chan struct{}, 4),
		logger:    slog.Default(),
	}

	for _, option := range options {
		option(s)
	}

	return s
}

// Validate resolves both endpoints and rejects unsupported trip combinations.
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

	return Trip{
		Pickup:     pickup,
		Dropoff:    dropoff,
		Passengers: request.Passengers,
		RideType:   request.RideType,
		Scenario:   request.Scenario,
		DistanceKM: distance,
		Route: routing.Route{
			Source:         "approximate",
			DistanceMeters: distance * 1000,
		},
	}, nil
}

// haversine calculates straight-line distance in kilometers from two latitude/longitude pairs.
func haversine(origin, destination Location) float64 {
	// Trigonometric functions expect radians; location coordinates are in degrees.
	radiansPerDegree := math.Pi / 180
	latitudeDelta := (destination.Latitude - origin.Latitude) * radiansPerDegree
	longitudeDelta := (destination.Longitude - origin.Longitude) * radiansPerDegree

	// Keep the haversine formula explicit so both coordinate contributions are visible.
	haversineTerm := math.Sin(latitudeDelta/2)*math.Sin(latitudeDelta/2) +
		math.Cos(origin.Latitude*radiansPerDegree)*math.Cos(destination.Latitude*radiansPerDegree)*
			math.Sin(longitudeDelta/2)*math.Sin(longitudeDelta/2)

	// Rounding near antipodal points can otherwise make a square root negative.
	haversineTerm = math.Max(0, math.Min(1, haversineTerm))

	return 6371 * 2 * math.Atan2(math.Sqrt(haversineTerm), math.Sqrt(1-haversineTerm))
}

// Compare runs a bounded worker pool, collects partial results and orders usable fares first.
func (service *Service) Compare(ctx context.Context, trip Trip) Comparison {
	now := time.Now().UTC()
	// One total budget includes worker queueing and the shared semaphore wait.
	ctx, cancel := context.WithTimeout(ctx, service.timeout)

	defer cancel()

	comparison := Comparison{
		Mode:        "demo",
		Notice:      "All fares and pickup times are simulated. No provider has been contacted.",
		Trip:        trip,
		Quotes:      make([]Quote, 0, len(service.providers)),
		CollectedAt: now,
		ExpiresAt:   now.Add(30 * time.Second),
	}

	// Queue each provider once. Workers share the service-wide semaphore across requests.
	jobs := make(chan Provider, len(service.providers))

	results := make(chan []Quote, len(service.providers))

	for _, provider := range service.providers {
		jobs <- provider
	}

	close(jobs)

	var workers sync.WaitGroup

	for i := 0; i < min(len(service.providers), cap(service.slots)); i++ {
		workers.Add(1)

		go func() {
			defer workers.Done()

			for provider := range jobs {
				results <- service.callProvider(ctx, provider, trip)
			}
		}()
	}
	// Direct calls keep permits until the adapter actually exits. No detached
	// goroutine hides an adapter that ignores context; honoring it is required.
	workers.Wait()

	close(results)

	// Evaluate expiry after all workers finish, since a short-lived product may already be stale.
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

	// Show current priced products first, then break ties with stable provider/product IDs.
	sort.Slice(comparison.Quotes, func(i, j int) bool {
		a, b := comparison.Quotes[i], comparison.Quotes[j]

		aPriced := a.Fare != nil && a.Status != "expired"
		bPriced := b.Fare != nil && b.Status != "expired"

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

// callProvider acquires a shared slot, calls the adapter and normalizes its response.
func (service *Service) callProvider(ctx context.Context, provider Provider, trip Trip) []Quote {
	start := time.Now()

	var quotes []Quote

	var err error

	select {
	case <-ctx.Done():
		err = ctx.Err()

	case service.slots <- struct{}{}:
		err = ctx.Err()

		if err == nil {
			quotes, err = provider.Estimate(ctx, trip)
		}
		<-service.slots
	}

	result, invalid := normalizeQuotes(provider, trip, quotes, err, time.Now().UTC())

	logProvider(
		ctx,
		service.logger,
		"provider.result",
		provider.ID(),
		"not_applicable",
		start,
		result,
		err,
		"invalid_products",
		invalid,
	)

	return result
}

// normalizeQuotes rejects malformed products and gives each remaining quote a consistent status.
func normalizeQuotes(provider Provider, trip Trip, quotes []Quote, err error, now time.Time) ([]Quote, int) {
	result := make([]Quote, 0, len(quotes))

	seen := make(map[string]bool)

	invalid := 0

	for _, quote := range cloneQuotes(quotes) {
		if (quote.ProviderID != "" && quote.ProviderID != provider.ID()) || strings.TrimSpace(quote.ProductID) == "" ||
			strings.TrimSpace(quote.ServiceName) == "" || !validProduct(quote.ProductType) || quote.Currency != "SGD" ||
			quote.Capacity < 1 || quote.Capacity > 6 || quote.FetchedAt.IsZero() || quote.FetchedAt.After(now.Add(time.Second)) ||
			!quote.ExpiresAt.After(quote.FetchedAt) || seen[quote.ProductID] ||
			!validSource(quote.Source) {
			invalid++

			continue
		}

		quote.ProviderID = provider.ID()
		quote.ProviderName = provider.Name()

		if quote.Fare != nil {
			quote.Fare.IsEstimateRange = quote.Fare.MinFare != quote.Fare.MaxFare

			if !validFare(quote.Fare) {
				quote.Fare = nil

				invalid++
			}
		}

		if quote.ETAMinutes != nil && (*quote.ETAMinutes < 0 || *quote.ETAMinutes > 1<<53-1) {
			quote.ETAMinutes = nil

			invalid++
		}

		if quote.Status == "unavailable" || quote.Capacity < trip.Passengers {
			quote.Status = "unavailable"
			quote.Fare = nil
			quote.ETAMinutes = nil
		} else if !quote.ExpiresAt.After(now) {
			quote.Status = "expired"
		} else if quote.Fare == nil && quote.ETAMinutes == nil {
			quote.Status = "unavailable"
		} else if quote.Fare == nil || quote.ETAMinutes == nil {
			quote.Status = "partial"
		} else {
			quote.Status = "available"
		}

		seen[quote.ProductID] = true

		result = append(result, quote)
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

		result = append(result, Quote{
			ProviderID:   provider.ID(),
			ProviderName: provider.Name(),
			ProductID:    "unavailable",
			ProductType:  ProductStandard,
			ServiceName:  "No quote available",
			Status:       "unavailable",
			Currency:     "SGD",
			Source:       "unknown",
			Capacity:     capacity,
			FetchedAt:    now,
			ExpiresAt:    now.Add(30 * time.Second),
			Message:      message,
		})
	}

	return result, invalid
}
