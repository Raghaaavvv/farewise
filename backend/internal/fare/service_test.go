// Verify that a timed-out provider does not discard another provider's usable quote.
package fare

import (
	"context"
	"testing"
	"time"
)

type slowProvider struct{}

func (slowProvider) ID() string {
	return "slow"
}

func (slowProvider) Name() string {
	return "Slow demo"
}

func (slowProvider) Estimate(ctx context.Context, trip Trip) ([]Quote, error) {
	<-ctx.Done()

	return nil, ctx.Err()
}

func TestTimeoutKeepsOtherProviderResult(t *testing.T) {
	trip, err := Validate(Request{
		PickupID:   "changi-t3",
		DropoffID:  "orchard",
		Passengers: 1,
		RideType:   "standard",
		Scenario:   "normal",
	})

	if err != nil {
		t.Fatal(err)
	}

	fast := DemoProvider{
		ProviderID:   "fast",
		ProviderName: "Fast demo",
		BaseCents:    500,
		CentsPerKM:   100,
		BaseETA:      2,
		Latency:      0,
	}

	service := NewService([]Provider{
		slowProvider{},
		fast,
	}, 10*time.Millisecond)

	comparison := service.Compare(context.Background(), trip)

	if len(comparison.Quotes) != 2 || comparison.Quotes[0].ProviderID != "fast" || comparison.Quotes[1].Status != "unavailable" {
		t.Fatal("timeout discarded a healthy result")
	}
}
