package fare

import (
	"strings"
	"time"
)

// Bounds match JavaScript's safe-integer range; zero is a legitimate explicit
// fare, while nil means that no price was supplied.
func validFare(f *FareRange) bool {
	return f != nil && f.MinFare >= 0 && f.MaxFare >= f.MinFare && f.MaxFare <= 1<<53-1 &&
		f.IsEstimateRange == (f.MinFare != f.MaxFare)
}

func validProduct(product ProductType) bool {
	switch product {
	case ProductStandard, ProductPremium, ProductPool, ProductTaxi:
		return true
	}
	return false
}

func validQuote(q Quote) bool {
	return q.Status == "available" && validFare(q.Fare) && q.ETAMinutes != nil && *q.ETAMinutes >= 0 &&
		*q.ETAMinutes <= 1<<53-1 && strings.TrimSpace(q.ProviderID) != "" && strings.TrimSpace(q.ProviderName) != "" &&
		strings.TrimSpace(q.ProductID) != "" && strings.TrimSpace(q.ServiceName) != "" && q.Currency == "SGD" &&
		q.Capacity >= 1 && q.Capacity <= 6 && validSource(q.Source) && validProduct(q.ProductType) &&
		!q.FetchedAt.IsZero() && q.ExpiresAt.After(q.FetchedAt)
}

func validSource(source string) bool {
	return source == "simulated" || source == "model" || source == "live" || source == "unknown"
}

func cloneQuotes(quotes []Quote) []Quote {
	if quotes == nil {
		return nil
	}
	result := make([]Quote, len(quotes))
	for i, q := range quotes {
		if q.Fare != nil {
			value := *q.Fare
			q.Fare = &value
		}
		if q.ETAMinutes != nil {
			value := *q.ETAMinutes
			q.ETAMinutes = &value
		}
		result[i] = q
	}
	return result
}

// Cache complete, fresh bundles only, never beyond any product's own expiry.
func cacheDeadline(quotes []Quote, now time.Time, ttl time.Duration) (time.Time, bool) {
	if len(quotes) == 0 {
		return time.Time{}, false
	}
	deadline := now.Add(ttl)
	for _, quote := range quotes {
		if !validQuote(quote) || quote.FetchedAt.After(now.Add(time.Second)) || !quote.ExpiresAt.After(now) {
			return time.Time{}, false
		}
		if quote.ExpiresAt.Before(deadline) {
			deadline = quote.ExpiresAt
		}
	}
	return deadline, true
}
