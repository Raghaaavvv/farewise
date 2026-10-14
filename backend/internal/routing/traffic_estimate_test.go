// Check geometric road matching, fallback conditions and encoded route geometry.
package routing

import (
	"context"
	"math"
	"testing"
	"time"
)

func eastRoute() Route {
	a := Point{
		Latitude:  1.3,
		Longitude: 103.8,
	}
	b := Point{
		Latitude:  1.3,
		Longitude: 103.81,
	}

	size := length(sub(project(b), project(a)))

	baseline := 120.0

	return Route{
		Source:          "onemap",
		DistanceMeters:  size,
		DurationSeconds: &baseline,
		Geometry: []Point{
			a,
			b,
		},
	}
}

func snapshot(links ...speedLink) *speedSnapshot {
	now := time.Now().UTC()

	return &speedSnapshot{
		links:      links,
		observedAt: now.Add(-time.Minute),
		fetchedAt:  now,
		expiresAt:  now.Add(5 * time.Minute),
	}
}

func TestTrafficMatchesDirectionAndReplacesOnlyCoveredBaseline(t *testing.T) {
	route := eastRoute()

	link := speedLink{
		id:    "east",
		start: route.Geometry[0],
		end: Point{
			Latitude:  1.3,
			Longitude: 103.805,
		},
		minimum: 10,
		maximum: 19,
	}

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
		{
			"reverse",
			route.Geometry[1],
			route.Geometry[0],
			10,
			19,
		},
		{
			"cross",
			Point{
				Latitude:  1.299,
				Longitude: 103.805,
			},
			Point{
				Latitude:  1.301,
				Longitude: 103.805,
			},
			10,
			19,
		},
		{
			"far",
			Point{
				Latitude:  1.301,
				Longitude: 103.8,
			},
			Point{
				Latitude:  1.301,
				Longitude: 103.81,
			},
			10,
			19,
		},
		{
			"sparse",
			route.Geometry[0],
			Point{
				Latitude:  1.3,
				Longitude: 103.801,
			},
			10,
			19,
		},
	} {
		result := estimateTraffic(context.Background(), route, snapshot(link))

		if result.Status != "insufficient_coverage" || result.EstimatedSeconds != nil {
			t.Fatalf("invented traffic duration for %s", link.id)
		}
	}
}

func TestTrafficLowSpeedBoundsMissingGeometryAndExpiredData(t *testing.T) {
	route := eastRoute()

	link := speedLink{
		id:      "crawl",
		start:   route.Geometry[0],
		end:     route.Geometry[1],
		minimum: 0,
		maximum: 9,
	}

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

	if err != nil || len(points) != 3 || points[0] != (Point{
		Latitude:  38.5,
		Longitude: -120.2,
	}) || points[2] != (Point{
		Latitude:  43.252,
		Longitude: -126.453,
	}) {
		t.Fatal("incorrect polyline decode", points, err)
	}

	for _, value := range []string{
		"",
		"?",
		"~",
		"!!!!",
		"~~~~~~~~~~~~",
	} {
		_, decodePolylineError := decodePolyline(value)

		if decodePolylineError == nil {
			t.Fatal("accepted malformed polyline")
		}
	}
}
