// Check coordinate bounds, endpoint swapping and device/map trip validation.
package fare

import (
	"math"
	"testing"
)

func deviceCoordinates(lat, lon float64) *Coordinates {
	return &Coordinates{
		Latitude:  &lat,
		Longitude: &lon,
	}
}

func TestCurrentLocationValidation(t *testing.T) {
	for _, coordinates := range []*Coordinates{
		deviceCoordinates(1.357, 103.987),
		deviceCoordinates(0, 0),
	} {
		location, err := CurrentLocation(*coordinates)

		if err != nil || location.Latitude != *coordinates.Latitude || location.Longitude != *coordinates.Longitude || location.ID != "current-location" {
			t.Fatalf("device coordinates were not preserved: %+v, %v", location, err)
		}
	}

	negativeAccuracy := -1.0

	for _, coordinates := range []Coordinates{
		{},
		{Latitude: new(float64)},
		{Longitude: new(float64)},
		*deviceCoordinates(91, 104),
		*deviceCoordinates(1.3, -181),
		*deviceCoordinates(math.NaN(), 104),
		*deviceCoordinates(1.3, math.Inf(1)),
		{
			Latitude:       new(float64),
			Longitude:      new(float64),
			AccuracyMeters: &negativeAccuracy,
		},
	} {
		_, currentLocationError := CurrentLocation(coordinates)

		if currentLocationError == nil {
			t.Fatalf("accepted invalid coordinates: %+v", coordinates)
		}
	}
}

func TestDeviceLocationComparisonAndSwap(t *testing.T) {
	coordinates := deviceCoordinates(1.36, 103.989)

	request := Request{
		PickupID:          "current-location",
		PickupCoordinates: coordinates,
		DropoffID:         "orchard",
		Passengers:        1,
		RideType:          "standard",
		Scenario:          "normal",
	}

	trip, err := Validate(request)

	if err != nil || trip.Pickup.Latitude != 1.36 || trip.Pickup.Longitude != 103.989 || trip.DistanceKM <= 0 {
		t.Fatalf("invalid device pickup: %+v, %v", trip, err)
	}

	request.PickupID, request.DropoffID = request.DropoffID, request.PickupID

	request.PickupCoordinates, request.DropoffCoordinates = request.DropoffCoordinates, request.PickupCoordinates

	swapped, err := Validate(request)

	if err != nil || swapped.Dropoff.ID != "current-location" || math.Abs(swapped.DistanceKM-trip.DistanceKM) > 1e-9 {
		t.Fatalf("swap lost the coordinates: %+v, %v", swapped, err)
	}

	request.DropoffCoordinates = nil

	_, validateError := Validate(request)

	if validateError == nil {
		t.Fatal("current-location ID without coordinates was accepted")
	}

	request.DropoffID = "marina"

	request.DropoffCoordinates = coordinates

	_, conflictingLocationError := Validate(request)

	if conflictingLocationError == nil {
		t.Fatal("coordinates with a conflicting sample ID were accepted")
	}

	request.PickupID, request.PickupCoordinates = "current-location", deviceCoordinates(1.304, 103.832)

	request.DropoffID = "orchard"
	request.DropoffCoordinates = nil

	_, matchingCoordinatesError := Validate(request)

	if matchingCoordinatesError == nil {
		t.Fatal("identical coordinates with different IDs were accepted")
	}
}

func TestMapPointValidationAndSwap(t *testing.T) {
	coordinates := deviceCoordinates(1.303452345, 103.83512345)

	location, err := MapLocation(*coordinates)

	if err != nil || location.ID != "map-location" || location.Latitude != *coordinates.Latitude {
		t.Fatalf("lost map point: %+v %v", location, err)
	}

	request := Request{
		PickupID:           "changi-t3",
		DropoffID:          "map-location",
		DropoffCoordinates: coordinates,
		Passengers:         1,
		RideType:           "standard",
		Scenario:           "normal",
	}

	trip, err := Validate(request)

	if err != nil {
		t.Fatal(err)
	}

	request.PickupID, request.DropoffID = request.DropoffID, request.PickupID

	request.PickupCoordinates, request.DropoffCoordinates = request.DropoffCoordinates, request.PickupCoordinates

	swapped, err := Validate(request)

	if err != nil || swapped.Pickup.ID != "map-location" || swapped.Pickup.Latitude != trip.Dropoff.Latitude || swapped.DistanceKM != trip.DistanceKM {
		t.Fatal("swap lost map coordinates")
	}

	request.PickupCoordinates = nil

	_, validateError := Validate(request)

	if validateError == nil {
		t.Fatal("accepted map-location without coordinates")
	}

	for _, point := range []*Coordinates{
		deviceCoordinates(0, 0),
		deviceCoordinates(math.NaN(), 103.8),
		deviceCoordinates(1.3, 104.503),
		{},
	} {
		_, mapLocationError := MapLocation(*point)

		if mapLocationError == nil {
			t.Fatal("invalid map point was accepted")
		}
	}

	request = Request{
		PickupID:           "current-location",
		PickupCoordinates:  coordinates,
		DropoffID:          "map-location",
		DropoffCoordinates: coordinates,
		Passengers:         1,
		RideType:           "standard",
		Scenario:           "normal",
	}

	_, conflictingLocationError := Validate(request)

	if conflictingLocationError == nil {
		t.Fatal("same coordinates with different source IDs were accepted")
	}
}
