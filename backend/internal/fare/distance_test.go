package fare

import (
	"math"
	"testing"
)

func TestHaversineDistance(t *testing.T) {
	origin := Location{Latitude: 0, Longitude: 0}
	oneDegreeEast := Location{Latitude: 0, Longitude: 1}
	if distance := haversine(origin, origin); distance != 0 {
		t.Fatalf("identical points: want zero, got %f km", distance)
	}
	// One degree on the equator is about 111.195 km for a 6371 km sphere.
	if distance := haversine(origin, oneDegreeEast); math.Abs(distance-111.195) > 0.001 {
		t.Fatalf("equatorial distance: want approximately 111.195 km, got %f", distance)
	}
	if math.Abs(haversine(origin, oneDegreeEast)-haversine(oneDegreeEast, origin)) > 1e-9 {
		t.Fatal("distance should be symmetric")
	}
}
