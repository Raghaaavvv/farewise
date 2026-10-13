package fare

import (
	"context"
	"errors"
	"hash/fnv"
	"math"
	"time"
)

// Provider is the seam for a future permitted live integration. The demo
// implementations never make network requests or use real pricing algorithms.
type Provider interface {
	ID() string
	Name() string
	Estimate(context.Context, Trip) (Quote, error)
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

func (p DemoProvider) Estimate(ctx context.Context, trip Trip) (Quote, error) {
	timer := time.NewTimer(p.Latency)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return Quote{}, ctx.Err()
	case <-timer.C:
	}
	if trip.Scenario == "outage" && p.ID() == "ryde" {
		return Quote{}, errors.New("simulated provider failure")
	}
	capacity, product := 4, "Standard · demo"
	if trip.RideType == "six" {
		capacity, product = 6, "6-seater · demo"
	}
	quote := Quote{
		ProviderID: p.ID(), ProviderName: p.Name(), ServiceName: product,
		Status: "available", Currency: "SGD", Source: "simulated",
		Capacity: capacity, CollectedAt: time.Now().UTC(),
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
	quote.AmountCents, quote.ETAMinutes = &amount, &eta
	return quote, nil
}
