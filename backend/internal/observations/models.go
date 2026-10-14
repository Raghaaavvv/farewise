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

func ValidID(id string) bool { return idPattern.MatchString(id) }
func text(value string, max int) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) <= max && !strings.ContainsRune(value, 0)
}
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

func (r *CaptureRequest) Validate() error {
	r.PickupLabel = strings.TrimSpace(r.PickupLabel)
	r.DropoffLabel = strings.TrimSpace(r.DropoffLabel)
	if !ValidID(r.ID) || !text(r.PickupLabel, 120) || !text(r.DropoffLabel, 120) || !Zone(r.PickupZone) || !Zone(r.DropoffZone) {
		return errors.New("send a valid capture ID, labels up to 120 characters and area types")
	}
	_, err := fare.Validate(r.Trip)
	return err
}
func Fingerprint(value any) [32]byte { body, _ := json.Marshal(value); return sha256.Sum256(body) }

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

func (r *Fields) Validate(now time.Time) error {
	r.Product = strings.TrimSpace(r.Product)
	r.Notes = strings.TrimSpace(r.Notes)
	r.QuotedAt = r.QuotedAt.UTC().Truncate(time.Microsecond)
	if r.Provider != "grab" && r.Provider != "gojek" && r.Provider != "zig" && r.Provider != "ryde" {
		return errors.New("choose Grab, Gojek, Zig or Ryde")
	}
	if r.Product == "" || !text(r.Product, 80) {
		return errors.New("enter the exact provider product, up to 80 characters")
	}
	if r.PriceCents == nil || *r.PriceCents < 0 || *r.PriceCents > 1000000 {
		return errors.New("enter a fare between $0 and $10,000 in integer cents")
	}
	if r.FareKind != "quoted" && r.FareKind != "paid" {
		return errors.New("choose a displayed quote or a paid fare")
	}
	if r.QuotedAt.IsZero() || r.QuotedAt.Before(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)) || r.QuotedAt.After(now.Add(2*time.Minute)) {
		return errors.New("enter when you checked the fare; it cannot be in the future")
	}
	if r.Weather != "unknown" && r.Weather != "dry" && r.Weather != "rain" {
		return errors.New("choose unknown, dry or rain for weather")
	}
	if r.Promotion != "unknown" && r.Promotion != "none" && r.Promotion != "applied" {
		return errors.New("choose unknown, none or applied for promotion")
	}
	if r.IncludedFeesCents != nil && (*r.IncludedFeesCents < 0 || *r.IncludedFeesCents > *r.PriceCents) {
		return errors.New("included fees must be between zero and the total fare")
	}
	if !text(r.Notes, 1000) {
		return errors.New("notes must be at most 1,000 characters")
	}
	return nil
}

type SaveRequest struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	Fields
}

func (r *SaveRequest) Validate(now time.Time) error {
	if !ValidID(r.ID) || !ValidID(r.SessionID) {
		return errors.New("send valid save and session IDs")
	}
	return r.Fields.Validate(now)
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

func (r *Record) Derive() {
	gap := r.QuotedAt.Sub(r.Snapshot.CapturedAt)
	r.ContextAligned = gap >= -time.Minute && gap <= 5*time.Minute
	traffic := r.Snapshot.Trip.Route.Traffic
	r.TrafficFreshAtQuote = traffic != nil && r.Snapshot.Trip.Route.TrafficAware && traffic.ObservedAt != nil && traffic.ExpiresAt != nil && !traffic.ObservedAt.After(r.QuotedAt) && r.QuotedAt.Before(*traffic.ExpiresAt)
	r.TrainingEligible = r.FareKind == "quoted" && r.ContextAligned && r.Snapshot.Trip.Route.Source == "onemap"
	local := r.QuotedAt.In(singapore)
	r.SingaporeWeekday = (int(local.Weekday())+6)%7 + 1
	switch hour := local.Hour(); {
	case hour >= 6 && hour < 10:
		r.TimeSlot = "morning"
	case hour >= 10 && hour < 16:
		r.TimeSlot = "midday"
	case hour >= 16 && hour < 20:
		r.TimeSlot = "evening"
	default:
		r.TimeSlot = "night"
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
	Provider, Search string
	Limit, Offset    int
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
