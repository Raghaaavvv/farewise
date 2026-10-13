package fare

import (
	"context"
	"errors"
	"math"
	"sort"
	"time"
)

type Service struct {
	providers []Provider
	timeout   time.Duration
}

func NewService(providers []Provider, timeout time.Duration) *Service {
	return &Service{providers: providers, timeout: timeout}
}

func Validate(request Request) (Trip, error) {
	pickup, found := FindLocation(request.PickupID)
	if !found {
		return Trip{}, errors.New("choose a pickup from the demo locations")
	}
	dropoff, found := FindLocation(request.DropoffID)
	if !found {
		return Trip{}, errors.New("choose a destination from the demo locations")
	}
	if pickup.ID == dropoff.ID {
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
	return 6371 * 2 * math.Atan2(math.Sqrt(x), math.Sqrt(1-x))
}

func (s *Service) Compare(ctx context.Context, trip Trip) Comparison {
	now := time.Now().UTC()
	comparison := Comparison{
		Mode: "demo", Notice: "All fares and pickup times are simulated. No provider has been contacted.",
		Trip: trip, Quotes: make([]Quote, 0, len(s.providers)), CollectedAt: now,
		ExpiresAt: now.Add(2 * time.Minute),
	}
	// Query adapters concurrently; each result has an independent deadline.
	results := make(chan Quote, len(s.providers))
	for _, provider := range s.providers {
		go func(p Provider) {
			deadline, cancel := context.WithTimeout(ctx, s.timeout)
			defer cancel()
			// Buffered channel also accommodates an adapter that responds after
			// its deadline. Real adapters must still use context-aware HTTP calls.
			type result struct {
				quote Quote
				err   error
			}
			ch := make(chan result, 1)
			go func() { q, err := p.Estimate(deadline, trip); ch <- result{q, err} }()
			var quote Quote
			var err error
			select {
			case r := <-ch:
				quote, err = r.quote, r.err
			case <-deadline.Done():
				err = deadline.Err()
			}
			if err != nil || quote.AmountCents == nil || quote.ETAMinutes == nil || *quote.AmountCents <= 0 || *quote.ETAMinutes < 0 {
				message := "This provider is unavailable in the demo scenario."
				if errors.Is(err, context.DeadlineExceeded) {
					message = "This provider did not respond in time."
				}
				quote = Quote{ProviderID: p.ID(), ProviderName: p.Name(), Status: "unavailable", Currency: "SGD", Source: "simulated", CollectedAt: time.Now().UTC(), Message: message}
			}
			results <- quote
		}(provider)
	}
	for range s.providers {
		comparison.Quotes = append(comparison.Quotes, <-results)
	}
	sort.Slice(comparison.Quotes, func(i, j int) bool {
		a, b := comparison.Quotes[i], comparison.Quotes[j]
		if a.AmountCents == nil {
			return b.AmountCents == nil && a.ProviderID < b.ProviderID
		}
		if b.AmountCents == nil {
			return true
		}
		if *a.AmountCents == *b.AmountCents {
			return a.ProviderID < b.ProviderID
		}
		return *a.AmountCents < *b.AmountCents
	})
	return comparison
}
