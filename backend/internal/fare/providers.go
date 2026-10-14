// Simulated provider adapters exercise the comparison flow without contacting ride services.
package fare

import (
	"context"
	"errors"
	"hash/fnv"
	"math"
	"time"
)

// Provider is the seam for a live or model adapter. Implementations must honor
// ctx, use context-aware HTTP requests, and return stable IDs and timestamps
// per product. A nonempty slice with an error may contain usable partial results.
// Demo implementations never contact real providers or use trained models.
type Provider interface {
	ID() string
	Name() string
	Estimate(context.Context, Trip) ([]Quote, error)
}

type DemoProvider struct {
	ProviderID   string
	ProviderName string
	BaseCents    int
	CentsPerKM   int
	BaseETA      int
	Latency      time.Duration
}

// ID returns the stable identifier of this provider adapter.
func (provider DemoProvider) ID() string {
	return provider.ProviderID
}

// Name returns the provider name displayed with its quotes.
func (provider DemoProvider) Name() string {
	return provider.ProviderName
}

// DemoProviders returns four adapters with invented pricing and response timings.
func DemoProviders() []Provider {
	return []Provider{
		DemoProvider{
			ProviderID:   "grab",
			ProviderName: "Grab",
			BaseCents:    590,
			CentsPerKM:   106,
			BaseETA:      4,
			Latency:      180 * time.Millisecond,
		},
		DemoProvider{
			ProviderID:   "gojek",
			ProviderName: "Gojek",
			BaseCents:    510,
			CentsPerKM:   99,
			BaseETA:      6,
			Latency:      260 * time.Millisecond,
		},
		DemoProvider{
			ProviderID:   "zig",
			ProviderName: "CDG Zig",
			BaseCents:    620,
			CentsPerKM:   97,
			BaseETA:      3,
			Latency:      140 * time.Millisecond,
		},
		DemoProvider{
			ProviderID:   "ryde",
			ProviderName: "Ryde",
			BaseCents:    450,
			CentsPerKM:   94,
			BaseETA:      8,
			Latency:      310 * time.Millisecond,
		},
	}
}

// Estimate waits with cancellation support, then builds simulated products for this trip.
func (provider DemoProvider) Estimate(ctx context.Context, trip Trip) ([]Quote, error) {
	contextError := ctx.Err()

	if contextError != nil {
		return nil, contextError
	}

	timer := time.NewTimer(provider.Latency)

	defer timer.Stop()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()

	case <-timer.C:
	}
	// A zero-delay timer and cancellation can become ready together.
	cancellationAfterTimerError := ctx.Err()

	if cancellationAfterTimerError != nil {
		return nil, cancellationAfterTimerError
	}

	if trip.Scenario == "outage" && provider.ID() == "ryde" {
		return nil, errors.New("simulated provider failure")
	}

	capacity := 4
	product := "Standard · demo"

	if trip.RideType == "six" {
		capacity = 6
		product = "6-seater · demo"
	}

	quote := Quote{
		ProviderID:      provider.ID(),
		ProviderName:    provider.Name(),
		ServiceName:     product,
		ProductID:       trip.RideType,
		ProductType:     ProductStandard,
		Status:          "available",
		Currency:        "SGD",
		Source:          "simulated",
		Capacity:        capacity,
		FetchedAt:       time.Now().UTC(),
		Message:         "Illustrative quote. Not offered by the provider.",
		FeesDescription: "Demo total includes an invented airport fee when applicable. No real fees, ERP, discounts or availability are checked.",
	}
	// A stable route-based offset produces variety without claiming live refreshes.
	hash := fnv.New32a()

	_, _ = hash.Write([]byte(trip.Pickup.ID + trip.Dropoff.ID + provider.ID()))

	// The base, distance rate and route offset are invented demo inputs.
	amount := provider.BaseCents + int(math.Round(trip.DistanceKM*float64(provider.CentsPerKM))) + int(hash.Sum32()%180)

	if trip.Pickup.ID == "changi-t3" || trip.Pickup.ID == "changi-t1" || trip.Pickup.ID == "jewel" {
		amount += 500
	}

	if capacity == 6 {
		amount = amount * 145 / 100
	}

	eta := provider.BaseETA

	// Demo scenarios deliberately change price and pickup time; no live demand is measured.
	switch trip.Scenario {
	case "busy":
		amount = amount * (125 + int(hash.Sum32()%20)) / 100
		eta += 3

	case "rain":
		amount = amount * (145 + int(hash.Sum32()%25)) / 100
		eta += 6
	}

	// Preserve the demo convention of rounding to the nearest ten cents.
	amount = (amount + 5) / 10 * 10

	quote.Fare = &FareRange{
		MinFare: amount,
		MaxFare: amount,
	}

	quote.ETAMinutes = &eta
	// Deliberately different lifetimes exercise per-product expiry in the demo.
	lifetimes := map[string]time.Duration{
		"grab":  90 * time.Second,
		"gojek": 2 * time.Minute,
		"zig":   75 * time.Second,
		"ryde":  time.Minute,
	}

	ttl := lifetimes[provider.ID()]

	if ttl == 0 {
		ttl = 2 * time.Minute
	}

	quote.ExpiresAt = quote.FetchedAt.Add(ttl)

	quotes := []Quote{quote}

	// Add provider-specific tiers to exercise ranges, expiry and partial responses.
	switch provider.ID() {
	case "grab":
		premium := quote
		premium.ProductID = trip.RideType + "-premium"
		premium.ProductType = ProductPremium
		premium.ServiceName = "Premium · demo"
		premium.Fare = &FareRange{
			MinFare:         amount * 140 / 100,
			MaxFare:         amount * 160 / 100,
			IsEstimateRange: true,
		}
		premium.ExpiresAt = quote.FetchedAt.Add(45 * time.Second)
		quotes = append(quotes, premium)

	case "zig":
		taxi := quote
		taxi.ProductID = trip.RideType + "-taxi"
		taxi.ProductType = ProductTaxi
		taxi.ServiceName = "Metered taxi · demo"
		taxi.Fare = &FareRange{
			MinFare:         amount * 90 / 100,
			MaxFare:         amount * 120 / 100,
			IsEstimateRange: true,
		}
		taxi.ExpiresAt = quote.FetchedAt.Add(50 * time.Second)
		quotes = append(quotes, taxi)

	case "ryde":
		if trip.RideType == "standard" && trip.Passengers <= 2 {
			pool := quote

			pool.ProductID = "pool"
			pool.ProductType = ProductPool
			pool.ServiceName = "Shared ride · demo"
			pool.Capacity = 2

			pool.Fare = &FareRange{
				MinFare:         amount * 70 / 100,
				MaxFare:         amount * 85 / 100,
				IsEstimateRange: true,
			}

			pool.ExpiresAt = quote.FetchedAt.Add(40 * time.Second)

			quotes = append(quotes, pool)
		}

	case "gojek":
		if trip.Scenario == "rain" {
			quotes[0].ETAMinutes = nil
			quotes[0].Status = "partial"

			quotes[0].Message = "Demo fare is available, but the pickup estimate is missing."
		}
	}

	return quotes, nil
}
