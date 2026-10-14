// Validate quote bundles and copy mutable fields before sharing or caching them.
package fare

import (
	"strings"
	"time"
)

// Bounds match JavaScript's safe-integer range; zero is a legitimate explicit
// fare, while nil means that no price was supplied.
func validFare(fareRange *FareRange) bool {
	return fareRange != nil && fareRange.MinFare >= 0 && fareRange.MaxFare >= fareRange.MinFare && fareRange.MaxFare <= 1<<53-1 &&
		fareRange.IsEstimateRange == (fareRange.MinFare != fareRange.MaxFare)
}

// validProduct accepts only the product categories supported by the quote contract.
func validProduct(product ProductType) bool {
	switch product {
	case ProductStandard, ProductPremium, ProductPool, ProductTaxi:
		return true
	}

	return false
}

// validQuote checks whether a product is complete enough to be cached as an available fare.
func validQuote(quote Quote) bool {
	return quote.Status == "available" && validFare(quote.Fare) && quote.ETAMinutes != nil && *quote.ETAMinutes >= 0 &&
		*quote.ETAMinutes <= 1<<53-1 && strings.TrimSpace(quote.ProviderID) != "" && strings.TrimSpace(quote.ProviderName) != "" &&
		strings.TrimSpace(quote.ProductID) != "" && strings.TrimSpace(quote.ServiceName) != "" && quote.Currency == "SGD" &&
		quote.Capacity >= 1 && quote.Capacity <= 6 && validSource(quote.Source) && validProduct(quote.ProductType) &&
		!quote.FetchedAt.IsZero() && quote.ExpiresAt.After(quote.FetchedAt)
}

// validSource accepts the supported provenance labels without implying that a fare is live.
func validSource(source string) bool {
	return source == "simulated" || source == "model" || source == "live" || source == "unknown"
}

// cloneQuotes copies the slice and its pointer fields so callers cannot mutate shared quotes.
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
