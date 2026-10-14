package fare

import (
	"errors"
	"math"
)

// CurrentLocation validates coordinates supplied after browser permission.
// The server cannot access browser GPS or display its permission prompt.
// This endpoint does not persist or reverse-geocode coordinates. A subsequent
// comparison sends its endpoints to OneMap when road routing is configured.
func CurrentLocation(coordinates Coordinates) (Location, error) {
	if coordinates.Latitude == nil || coordinates.Longitude == nil {
		return Location{}, errors.New("latitude and longitude are required")
	}
	lat, lon := *coordinates.Latitude, *coordinates.Longitude
	if math.IsNaN(lat) || math.IsInf(lat, 0) || lat < -90 || lat > 90 {
		return Location{}, errors.New("latitude must be between -90 and 90")
	}
	if math.IsNaN(lon) || math.IsInf(lon, 0) || lon < -180 || lon > 180 {
		return Location{}, errors.New("longitude must be between -180 and 180")
	}
	if accuracy := coordinates.AccuracyMeters; accuracy != nil && (math.IsNaN(*accuracy) || math.IsInf(*accuracy, 0) || *accuracy < 0) {
		return Location{}, errors.New("accuracy must be a non-negative number of meters")
	}
	return Location{ID: "current-location", Name: "Current location", Detail: "Device coordinates · no address lookup", Latitude: lat, Longitude: lon}, nil
}

func resolveLocation(id string, coordinates *Coordinates) (Location, error) {
	if id == "current-location" {
		if coordinates == nil {
			return Location{}, errors.New("current location requires device coordinates")
		}
		return CurrentLocation(*coordinates)
	}
	if coordinates != nil {
		return Location{}, errors.New("coordinates must use the current-location ID")
	}
	location, found := FindLocation(id)
	if !found {
		return Location{}, errors.New("choose a sample location or use current location")
	}
	return location, nil
}
