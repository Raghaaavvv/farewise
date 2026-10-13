// Package fare owns the trip and quote types, independent of HTTP or the UI.
package fare

import "time"

type Location struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Detail    string  `json:"detail"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

// These are approximate demo points, not a geocoding API or pickup instructions.
var Locations = []Location{
	{"changi-t3", "Changi Airport · Terminal 3", "Airport pickup · illustrative point", 1.3570, 103.9870},
	{"changi-t1", "Changi Airport · Terminal 1", "Airport pickup · illustrative point", 1.3640, 103.9910},
	{"jewel", "Jewel Changi Airport", "Airport boulevard", 1.3600, 103.9890},
	{"orchard", "ION Orchard", "Orchard Road", 1.3040, 103.8320},
	{"tampines", "Tampines Mall", "Tampines Central", 1.3520, 103.9450},
	{"marina", "Marina Bay Sands", "Bayfront Avenue", 1.2830, 103.8600},
	{"jurong", "JEM · Jurong East", "Jurong Gateway Road", 1.3330, 103.7430},
	{"woodlands", "Causeway Point", "Woodlands Square", 1.4360, 103.7860},
	{"serangoon", "NEX · Serangoon", "Serangoon Central", 1.3500, 103.8720},
}

func FindLocation(id string) (Location, bool) {
	for _, location := range Locations {
		if location.ID == id {
			return location, true
		}
	}
	return Location{}, false
}

type Request struct {
	PickupID           string       `json:"pickupId"`
	DropoffID          string       `json:"dropoffId"`
	Passengers         int          `json:"passengers"`
	RideType           string       `json:"rideType"`
	Scenario           string       `json:"scenario"`
	PickupCoordinates  *Coordinates `json:"pickupCoordinates,omitempty"`
	DropoffCoordinates *Coordinates `json:"dropoffCoordinates,omitempty"`
}

// Pointers distinguish omitted coordinates from valid zero latitude/longitude.
type Coordinates struct {
	Latitude       *float64 `json:"latitude"`
	Longitude      *float64 `json:"longitude"`
	AccuracyMeters *float64 `json:"accuracyMeters,omitempty"`
}

type Trip struct {
	Pickup     Location `json:"pickup"`
	Dropoff    Location `json:"dropoff"`
	Passengers int      `json:"passengers"`
	RideType   string   `json:"rideType"`
	Scenario   string   `json:"scenario"`
	DistanceKM float64  `json:"distanceKm"`
}

type ProductType string

const (
	ProductStandard ProductType = "standard"
	ProductPremium  ProductType = "premium"
	ProductPool     ProductType = "pool"
	ProductTaxi     ProductType = "taxi"
)

// A point fare is represented by equal bounds, never by a separate field.
// IsEstimateRange describes the shape, not a calibrated confidence level.
type FareRange struct {
	MinFare         int  `json:"minFare"`
	MaxFare         int  `json:"maxFare"`
	IsEstimateRange bool `json:"isEstimateRange"`
}

type Quote struct {
	ProviderID      string      `json:"providerId"`
	ProviderName    string      `json:"providerName"`
	ProductID       string      `json:"productId"`
	ProductType     ProductType `json:"productType"`
	ServiceName     string      `json:"serviceName"`
	Status          string      `json:"status"`
	Currency        string      `json:"currency"`
	Fare            *FareRange  `json:"fare"`
	ETAMinutes      *int        `json:"etaMinutes"`
	Source          string      `json:"source"`
	FetchedAt       time.Time   `json:"fetchedAt"`
	ExpiresAt       time.Time   `json:"expiresAt"`
	Cached          bool        `json:"cached"`
	Capacity        int         `json:"capacity"`
	Message         string      `json:"message"`
	FeesDescription string      `json:"feesDescription"`
}

type Comparison struct {
	Mode        string    `json:"mode"`
	Notice      string    `json:"notice"`
	Trip        Trip      `json:"trip"`
	Quotes      []Quote   `json:"quotes"`
	CollectedAt time.Time `json:"collectedAt"`
	ExpiresAt   time.Time `json:"expiresAt"`
}
