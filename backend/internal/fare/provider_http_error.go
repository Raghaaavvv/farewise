// Classify upstream HTTP failures and interpret their requested retry delay.
package fare

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ProviderHTTPError is the contract for a future HTTP adapter: close response
// bodies and return the status and Retry-After header, without credentials or
// sensitive upstream response bodies in the error. Only read-only estimates
// may be retried. Unclassified errors are not automatically retried.
type ProviderHTTPError struct {
	StatusCode int
	RetryAfter string
}

// Error returns the sanitized message exposed by this error type.
func (providerError *ProviderHTTPError) Error() string {
	return fmt.Sprintf("provider HTTP status %d", providerError.StatusCode)
}

// Retryable identifies the HTTP status codes eligible for a bounded estimate retry.
func (providerError *ProviderHTTPError) Retryable() bool {
	switch providerError.StatusCode {
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true

	default:
		return false
	}
}

// ParseRetryAfter accepts HTTP's nonnegative integer seconds or HTTP-date.
// Invalid headers use ordinary backoff; past dates allow immediate retry.
func ParseRetryAfter(value string, now time.Time) (time.Duration, bool) {
	value = strings.TrimSpace(value)

	if value == "" {
		return 0, false
	}

	digits := true

	for _, char := range value {
		if char < '0' || char > '9' {
			digits = false

			break
		}
	}

	if digits {
		seconds, err := strconv.ParseUint(value, 10, 64)

		if err != nil || seconds > uint64(math.MaxInt64/int64(time.Second)) {
			return time.Duration(math.MaxInt64), true
		}

		return time.Duration(seconds) * time.Second, true
	}

	if date, err := http.ParseTime(value); err == nil {
		return maxDuration(0, date.Sub(now)), true
	}

	return 0, false
}

func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}

	return b
}
