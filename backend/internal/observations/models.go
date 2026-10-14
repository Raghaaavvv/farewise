// Package observations stores manually reported fares separately from simulated quotes.
package observations

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"farewise/backend/internal/fare"
)

var ErrNotFound = errors.New("observation or session not found")

var ErrConflict = errors.New("this record changed or this save ID was already used with different details")

var idPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// ValidID checks the UUIDv4 format used for capture and save identifiers.
func ValidID(id string) bool {
	return idPattern.MatchString(id)
}

// text rejects invalid UTF-8, embedded null characters and overlong user text.
func text(value string, max int) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) <= max && !strings.ContainsRune(value, 0)
}

// Zone accepts the manual area classifications supported by the logger.
func Zone(value string) bool {
	switch value {
	case "unknown", "residential", "office", "industrial", "retail", "airport", "mixed":
		return true
	}

	return false
}

type CaptureRequest struct {
	ID           string       `json:"id"`
	Trip         fare.Request `json:"trip"`
	PickupLabel  string       `json:"pickupLabel"`
	DropoffLabel string       `json:"dropoffLabel"`
	PickupZone   string       `json:"pickupZone"`
	DropoffZone  string       `json:"dropoffZone"`
}

// Validate trims the labels and checks the capture identifier, zones and trip.
func (request *CaptureRequest) Validate() error {
	request.PickupLabel = strings.TrimSpace(request.PickupLabel)

	request.DropoffLabel = strings.TrimSpace(request.DropoffLabel)

	if !ValidID(request.ID) || !text(request.PickupLabel, 120) || !text(request.DropoffLabel, 120) || !Zone(request.PickupZone) || !Zone(request.DropoffZone) {
		return errors.New("send a valid capture ID, labels up to 120 characters and area types")
	}

	_, err := fare.Validate(request.Trip)

	return err
}

// Fingerprint hashes a request representation to recognize a safe replay of the same save.
func Fingerprint(value any) [32]byte {
	body, _ := json.Marshal(value)

	return sha256.Sum256(body)
}

type Snapshot struct {
	ID             string       `json:"id"`
	CapturedAt     time.Time    `json:"capturedAt"`
	FeatureVersion string       `json:"featureVersion"`
	DemoVersion    string       `json:"demoVersion"`
	Trip           fare.Trip    `json:"trip"`
	DemoQuotes     []fare.Quote `json:"demoQuotes"`
	PickupZone     string       `json:"pickupZone"`
	DropoffZone    string       `json:"dropoffZone"`
}

type Fields struct {
	Provider          string    `json:"provider"`
	Product           string    `json:"product"`
	PriceCents        *int      `json:"priceCents"`
	FareKind          string    `json:"fareKind"`
	QuotedAt          time.Time `json:"quotedAt"`
	Weather           string    `json:"weather"`
	Promotion         string    `json:"promotion"`
	IncludedFeesCents *int      `json:"includedFeesCents"`
	Notes             string    `json:"notes"`
}

// Validate normalizes manual input and checks its price, time and category bounds.
func (request *Fields) Validate(now time.Time) error {
	request.Product = strings.TrimSpace(request.Product)

	request.Notes = strings.TrimSpace(request.Notes)

	request.QuotedAt = request.QuotedAt.UTC().Truncate(time.Microsecond)

	if request.Provider != "grab" && request.Provider != "gojek" && request.Provider != "zig" && request.Provider != "ryde" {
		return errors.New("choose Grab, Gojek, Zig or Ryde")
	}

	if request.Product == "" || !text(request.Product, 80) {
		return errors.New("enter the exact provider product, up to 80 characters")
	}

	if request.PriceCents == nil || *request.PriceCents < 0 || *request.PriceCents > 1000000 {
		return errors.New("enter a fare between $0 and $10,000 in integer cents")
	}

	if request.FareKind != "quoted" && request.FareKind != "paid" {
		return errors.New("choose a displayed quote or a paid fare")
	}

	if request.QuotedAt.IsZero() || request.QuotedAt.Before(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)) || request.QuotedAt.After(now.Add(2*time.Minute)) {
		return errors.New("enter when you checked the fare; it cannot be in the future")
	}

	if request.Weather != "unknown" && request.Weather != "dry" && request.Weather != "rain" {
		return errors.New("choose unknown, dry or rain for weather")
	}

	if request.Promotion != "unknown" && request.Promotion != "none" && request.Promotion != "applied" {
		return errors.New("choose unknown, none or applied for promotion")
	}

	if request.IncludedFeesCents != nil && (*request.IncludedFeesCents < 0 || *request.IncludedFeesCents > *request.PriceCents) {
		return errors.New("included fees must be between zero and the total fare")
	}

	if !text(request.Notes, 1000) {
		return errors.New("notes must be at most 1,000 characters")
	}

	return nil
}

type SaveRequest struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	Fields
}

// Validate checks the save/session identifiers before validating the fare fields.
func (request *SaveRequest) Validate(now time.Time) error {
	if !ValidID(request.ID) || !ValidID(request.SessionID) {
		return errors.New("send valid save and session IDs")
	}

	return request.Fields.Validate(now)
}

type EditRequest struct {
	Revision int `json:"revision"`
	Fields
}

type Record struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	Fields
	Revision            int       `json:"revision"`
	CreatedAt           time.Time `json:"createdAt"`
	UpdatedAt           time.Time `json:"updatedAt"`
	Snapshot            Snapshot  `json:"snapshot"`
	ContextAligned      bool      `json:"contextAligned"`
	TrafficFreshAtQuote bool      `json:"trafficFreshAtQuote"`
	TrainingEligible    bool      `json:"trainingEligible"`
	SingaporeWeekday    int       `json:"singaporeWeekday"`
	TimeSlot            string    `json:"timeSlot"`
}

var singapore = time.FixedZone("SGT", 8*3600)

// Derive calculates collection-quality flags and Singapore time buckets from the saved snapshot.
func (record *Record) Derive() {
	// A fare and its conditions must be observed close together to pass the alignment check.
	gap := record.QuotedAt.Sub(record.Snapshot.CapturedAt)

	record.ContextAligned = gap >= -time.Minute && gap <= 5*time.Minute

	traffic := record.Snapshot.Trip.Route.Traffic

	record.TrafficFreshAtQuote = traffic != nil && record.Snapshot.Trip.Route.TrafficAware && traffic.ObservedAt != nil && traffic.ExpiresAt != nil && !traffic.ObservedAt.After(record.QuotedAt) && record.QuotedAt.Before(*traffic.ExpiresAt)

	// This is a collection-quality candidate flag, not a guarantee of useful model training data.
	record.TrainingEligible = record.FareKind == "quoted" && record.ContextAligned && record.Snapshot.Trip.Route.Source == "onemap"

	// Bucket observations in Singapore time even if the server uses another timezone.
	local := record.QuotedAt.In(singapore)

	record.SingaporeWeekday = (int(local.Weekday())+6)%7 + 1

	switch hour := local.Hour(); {
	case hour >= 6 && hour < 10:
		record.TimeSlot = "morning"

	case hour >= 10 && hour < 16:
		record.TimeSlot = "midday"

	case hour >= 16 && hour < 20:
		record.TimeSlot = "evening"

	default:
		record.TimeSlot = "night"
	}
}

type Group struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}

type Stats struct {
	Total      int     `json:"total"`
	Quotes     int     `json:"quotes"`
	Paid       int     `json:"paid"`
	Routes     int     `json:"routes"`
	ByProvider []Group `json:"byProvider"`
	ByTimeSlot []Group `json:"byTimeSlot"`
	ByWeekday  []Group `json:"byWeekday"`
}

type Filter struct {
	Provider string
	Search   string
	Limit    int
	Offset   int
}

type Page struct {
	Records []Record `json:"records"`
	Total   int      `json:"total"`
	Stats   Stats    `json:"stats"`
}

type Repository interface {
	FindSession(context.Context, string) (Snapshot, []byte, error)
	PutSession(context.Context, Snapshot, [32]byte) (Snapshot, error)
	Save(context.Context, SaveRequest) (Record, bool, error)
	Edit(context.Context, string, EditRequest) (Record, error)
	List(context.Context, Filter) (Page, error)
}
