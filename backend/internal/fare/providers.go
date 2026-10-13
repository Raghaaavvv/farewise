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
	ProviderID, ProviderName       string
	BaseCents, CentsPerKM, BaseETA int
	Latency                        time.Duration
}

func (p DemoProvider) ID() string   { return p.ProviderID }
func (p DemoProvider) Name() string { return p.ProviderName }

func DemoProviders() []Provider {
	return []Provider{
		DemoProvider{"grab", "Grab", 590, 106, 4, 180 * time.Millisecond},
		DemoProvider{"gojek", "Gojek", 510, 99, 6, 260 * time.Millisecond},
		DemoProvider{"zig", "CDG Zig", 620, 97, 3, 140 * time.Millisecond},
		DemoProvider{"ryde", "Ryde", 450, 94, 8, 310 * time.Millisecond},
	}
}

func (p DemoProvider) Estimate(ctx context.Context, trip Trip) ([]Quote, error) {
	timer := time.NewTimer(p.Latency)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
	}
	if trip.Scenario == "outage" && p.ID() == "ryde" {
		return nil, errors.New("simulated provider failure")
	}
	capacity, product := 4, "Standard · demo"
	if trip.RideType == "six" {
		capacity, product = 6, "6-seater · demo"
	}
	quote := Quote{
		ProviderID: p.ID(), ProviderName: p.Name(), ServiceName: product,
		ProductID: trip.RideType, ProductType: ProductStandard,
		Status: "available", Currency: "SGD", Source: "simulated",
		Capacity: capacity, FetchedAt: time.Now().UTC(),
		Message:         "Illustrative quote. Not offered by the provider.",
		FeesDescription: "Demo total includes an invented airport fee when applicable. No real fees, ERP, discounts or availability are checked.",
	}
	// A stable route-based offset produces variety without claiming live refreshes.
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(trip.Pickup.ID + trip.Dropoff.ID + p.ID()))
	amount := p.BaseCents + int(math.Round(trip.DistanceKM*float64(p.CentsPerKM))) + int(hash.Sum32()%180)
	if trip.Pickup.ID == "changi-t3" || trip.Pickup.ID == "changi-t1" || trip.Pickup.ID == "jewel" {
		amount += 500
	}
	if capacity == 6 {
		amount = amount * 145 / 100
	}
	eta := p.BaseETA
	switch trip.Scenario {
	case "busy":
		amount = amount * (125 + int(hash.Sum32()%20)) / 100
		eta += 3
	case "rain":
		amount = amount * (145 + int(hash.Sum32()%25)) / 100
		eta += 6
	}
	amount = (amount + 5) / 10 * 10
	quote.Fare, quote.ETAMinutes = &FareRange{MinFare: amount, MaxFare: amount}, &eta
	// Deliberately different lifetimes exercise per-product expiry in the demo.
	lifetimes := map[string]time.Duration{"grab": 90 * time.Second, "gojek": 2 * time.Minute, "zig": 75 * time.Second, "ryde": time.Minute}
	ttl := lifetimes[p.ID()]
	if ttl == 0 {
		ttl = 2 * time.Minute
	}
	quote.ExpiresAt = quote.FetchedAt.Add(ttl)
	quotes := []Quote{quote}
	switch p.ID() {
	case "grab":
		premium := quote
		premium.ProductID, premium.ProductType, premium.ServiceName = trip.RideType+"-premium", ProductPremium, "Premium · demo"
		premium.Fare = &FareRange{amount * 140 / 100, amount * 160 / 100, true}
		premium.ExpiresAt = quote.FetchedAt.Add(45 * time.Second)
		quotes = append(quotes, premium)
	case "zig":
		taxi := quote
		taxi.ProductID, taxi.ProductType, taxi.ServiceName = trip.RideType+"-taxi", ProductTaxi, "Metered taxi · demo"
		taxi.Fare = &FareRange{amount * 90 / 100, amount * 120 / 100, true}
		taxi.ExpiresAt = quote.FetchedAt.Add(50 * time.Second)
		quotes = append(quotes, taxi)
	case "ryde":
		if trip.RideType == "standard" && trip.Passengers <= 2 {
			pool := quote
			pool.ProductID, pool.ProductType, pool.ServiceName, pool.Capacity = "pool", ProductPool, "Shared ride · demo", 2
			pool.Fare = &FareRange{amount * 70 / 100, amount * 85 / 100, true}
			pool.ExpiresAt = quote.FetchedAt.Add(40 * time.Second)
			quotes = append(quotes, pool)
		}
	case "gojek":
		if trip.Scenario == "rain" {
			quotes[0].ETAMinutes, quotes[0].Status = nil, "partial"
			quotes[0].Message = "Demo fare is available, but the pickup estimate is missing."
		}
	}
	return quotes, nil
}
