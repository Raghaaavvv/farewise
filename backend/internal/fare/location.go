// Validate device and map coordinates before using them as trip endpoints.
package fare

import (
	"errors"
	"math"
)

// MapBounds is OneMap's documented viewing rectangle, not a country boundary.
const (
	MapSouth = 1.144
	MapNorth = 1.494
	MapWest  = 103.535
	MapEast  = 104.502
)

// MapLocation validates an explicitly picked point, without GPS permission or
// reverse geocoding. Routing still determines whether that point is drivable.
func MapLocation(coordinates Coordinates) (Location, error) {
	location, err := CurrentLocation(coordinates)

	if err != nil {
		return Location{}, err
	}

	if location.Latitude < MapSouth || location.Latitude > MapNorth || location.Longitude < MapWest || location.Longitude > MapEast {
		return Location{}, errors.New("choose a point within OneMap's Singapore map coverage")
	}

	location.ID = "map-location"
	location.Name = "Pinned location"
	location.Detail = "Map coordinates · no address lookup"

	return location, nil
}

// CurrentLocation validates coordinates supplied after browser permission.
// The server cannot access browser GPS or display its permission prompt.
// This endpoint does not persist or reverse-geocode coordinates. A subsequent
// comparison sends its endpoints to OneMap when road routing is configured.
func CurrentLocation(coordinates Coordinates) (Location, error) {
	if coordinates.Latitude == nil || coordinates.Longitude == nil {
		return Location{}, errors.New("latitude and longitude are required")
	}

	lat := *coordinates.Latitude
	lon := *coordinates.Longitude

	if math.IsNaN(lat) || math.IsInf(lat, 0) || lat < -90 || lat > 90 {
		return Location{}, errors.New("latitude must be between -90 and 90")
	}

	if math.IsNaN(lon) || math.IsInf(lon, 0) || lon < -180 || lon > 180 {
		return Location{}, errors.New("longitude must be between -180 and 180")
	}

	if accuracy := coordinates.AccuracyMeters; accuracy != nil && (math.IsNaN(*accuracy) || math.IsInf(*accuracy, 0) || *accuracy < 0) {
		return Location{}, errors.New("accuracy must be a non-negative number of meters")
	}

	return Location{
		ID:        "current-location",
		Name:      "Current location",
		Detail:    "Device coordinates · no address lookup",
		Latitude:  lat,
		Longitude: lon,
	}, nil
}

// resolveLocation chooses a preset point or validates the coordinates supplied for a custom point.
func resolveLocation(id string, coordinates *Coordinates) (Location, error) {
	if id == "map-location" {
		if coordinates == nil {
			return Location{}, errors.New("map location requires selected coordinates")
		}

		return MapLocation(*coordinates)
	}

	if id == "current-location" {
		if coordinates == nil {
			return Location{}, errors.New("current location requires device coordinates")
		}

		return CurrentLocation(*coordinates)
	}

	if coordinates != nil {
		return Location{}, errors.New("coordinates must use the current-location or map-location ID")
	}

	location, found := FindLocation(id)

	if !found {
		return Location{}, errors.New("choose a sample location, a map point or current location")
	}

	return location, nil
}
