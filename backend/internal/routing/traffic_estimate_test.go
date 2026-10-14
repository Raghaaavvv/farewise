package routing

import (
	"context"
	"math"
	"testing"
	"time"
)

func eastRoute() Route {
	a, b := Point{1.3, 103.8}, Point{1.3, 103.81}
	size := length(sub(project(b), project(a)))
	baseline := 120.0
	return Route{Source: "onemap", DistanceMeters: size, DurationSeconds: &baseline, Geometry: []Point{a, b}}
}
func snapshot(links ...speedLink) *speedSnapshot {
	now := time.Now().UTC()
	return &speedSnapshot{links, now.Add(-time.Minute), now, now.Add(5 * time.Minute)}
}
func TestTrafficMatchesDirectionAndReplacesOnlyCoveredBaseline(t *testing.T) {
	route := eastRoute()
	link := speedLink{"east", route.Geometry[0], Point{1.3, 103.805}, 10, 19}
	result := estimateTraffic(context.Background(), route, snapshot(link))
	if result.Status != "applied" || math.Abs(result.Coverage-.5) > .02 || result.MatchedLinks != 1 {
		t.Fatalf("incorrect match: %+v", result)
	}
	expected := 120*(1-result.Coverage) + result.MatchedMeters/14.5*3.6
	if math.Abs(*result.EstimatedSeconds-expected) > .001 || *result.MinSeconds > *result.EstimatedSeconds || *result.MaxSeconds < *result.EstimatedSeconds {
		t.Fatal("did not replace matched baseline time")
	}
	// Repeating the same road observation cannot count coverage twice.
	duplicate := estimateTraffic(context.Background(), route, snapshot(link, link))
	if duplicate.Coverage != result.Coverage || *duplicate.EstimatedSeconds != *result.EstimatedSeconds {
		t.Fatal("double-counted road link")
	}
}
func TestTrafficRejectsOppositeCrossingDistantAndSparseRoads(t *testing.T) {
	route := eastRoute()
	for _, link := range []speedLink{
		{"reverse", route.Geometry[1], route.Geometry[0], 10, 19},
		{"cross", Point{1.299, 103.805}, Point{1.301, 103.805}, 10, 19},
		{"far", Point{1.301, 103.8}, Point{1.301, 103.81}, 10, 19},
		{"sparse", route.Geometry[0], Point{1.3, 103.801}, 10, 19},
	} {
		result := estimateTraffic(context.Background(), route, snapshot(link))
		if result.Status != "insufficient_coverage" || result.EstimatedSeconds != nil {
			t.Fatalf("invented traffic duration for %s", link.id)
		}
	}
}
func TestTrafficLowSpeedBoundsMissingGeometryAndExpiredData(t *testing.T) {
	route := eastRoute()
	link := speedLink{"crawl", route.Geometry[0], route.Geometry[1], 0, 9}
	result := estimateTraffic(context.Background(), route, snapshot(link))
	if result.Status != "applied" || math.IsInf(*result.MaxSeconds, 0) || *result.EstimatedSeconds <= 120 {
		t.Fatal("invalid crawl speed estimate")
	}
	expired := snapshot(link)
	expired.expiresAt = time.Now().Add(-time.Second)
	if estimateTraffic(context.Background(), route, expired).Status != "unavailable" {
		t.Fatal("used expired observations")
	}
	route.Geometry = nil
	if estimateTraffic(context.Background(), route, snapshot(link)).Status != "no_geometry" {
		t.Fatal("used missing geometry")
	}
}
func TestPolylineDecoding(t *testing.T) {
	points, err := decodePolyline("_p~iF~ps|U_ulLnnqC_mqNvxq`@")
	if err != nil || len(points) != 3 || points[0] != (Point{38.5, -120.2}) || points[2] != (Point{43.252, -126.453}) {
		t.Fatal("incorrect polyline decode", points, err)
	}
	for _, value := range []string{"", "?", "~", "!!!!", "~~~~~~~~~~~~"} {
		if _, err := decodePolyline(value); err == nil {
			t.Fatal("accepted malformed polyline")
		}
	}
}
