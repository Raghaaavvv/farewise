package fare

import (
	"context"
	"testing"
	"time"
)

type slowProvider struct{}

func (slowProvider) ID() string   { return "slow" }
func (slowProvider) Name() string { return "Slow demo" }
func (slowProvider) Estimate(ctx context.Context, trip Trip) (Quote, error) {
	<-ctx.Done()
	return Quote{}, ctx.Err()
}

func TestTimeoutKeepsOtherProviderResult(t *testing.T) {
	trip, err := Validate(Request{"changi-t3", "orchard", 1, "standard", "normal"})
	if err != nil {
		t.Fatal(err)
	}
	fast := DemoProvider{"fast", "Fast demo", 500, 100, 2, 0}
	service := NewService([]Provider{slowProvider{}, fast}, 10*time.Millisecond)
	comparison := service.Compare(context.Background(), trip)
	if len(comparison.Quotes) != 2 || comparison.Quotes[0].ProviderID != "fast" || comparison.Quotes[1].Status != "unavailable" {
		t.Fatal("timeout discarded a healthy result")
	}
}
