package routing

import "errors"

// OneMap's encoded route geometry uses Google polyline precision 5.
func decodePolyline(encoded string) ([]Point, error) {
	if len(encoded) > 200000 {
		return nil, errors.New("route geometry too large")
	}
	var points []Point
	var lat, lon int64
	for index := 0; index < len(encoded); {
		values := [2]int64{}
		for axis := range values {
			var value int64
			var shift uint
			for {
				if index >= len(encoded) || shift > 30 {
					return nil, errors.New("invalid route geometry")
				}
				b := int64(encoded[index]) - 63
				index++
				if b < 0 || b > 63 {
					return nil, errors.New("invalid route geometry")
				}
				value |= (b & 31) << shift
				shift += 5
				if b < 32 {
					break
				}
			}
			if value&1 != 0 {
				values[axis] = ^(value >> 1)
			} else {
				values[axis] = value >> 1
			}
		}
		lat += values[0]
		lon += values[1]
		p := Point{float64(lat) / 1e5, float64(lon) / 1e5}
		if p.Latitude < -90 || p.Latitude > 90 || p.Longitude < -180 || p.Longitude > 180 {
			return nil, errors.New("invalid route geometry")
		}
		points = append(points, p)
		if len(points) > 20000 {
			return nil, errors.New("route geometry too large")
		}
	}
	if len(points) < 2 {
		return nil, errors.New("missing route geometry")
	}
	return points, nil
}
