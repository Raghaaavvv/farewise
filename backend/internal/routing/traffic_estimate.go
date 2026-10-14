package routing

import (
	"context"
	"math"
	"time"
)

// Times are heuristic band bounds, not a statistical confidence interval.
// Coverage reports how much of the chosen route has a directional geometric match.
type TrafficEstimate struct {
	Source           string     `json:"source"`
	Status           string     `json:"status"`
	BaselineSeconds  float64    `json:"baselineSeconds"`
	EstimatedSeconds *float64   `json:"estimatedSeconds"`
	MinSeconds       *float64   `json:"minSeconds"`
	MaxSeconds       *float64   `json:"maxSeconds"`
	Coverage         float64    `json:"coverage"`
	MatchedMeters    float64    `json:"matchedMeters"`
	MatchedLinks     int        `json:"matchedLinks"`
	ObservedAt       *time.Time `json:"observedAt"`
	FetchedAt        *time.Time `json:"fetchedAt"`
	ExpiresAt        *time.Time `json:"expiresAt"`
	Message          string     `json:"message"`
}
type vector struct{ x, y float64 }

func project(p Point) vector {
	return vector{(p.Longitude - 103.8) * 111195 * math.Cos(1.3*math.Pi/180), (p.Latitude - 1.3) * 111195}
}
func sub(a, b vector) vector  { return vector{a.x - b.x, a.y - b.y} }
func length(a vector) float64 { return math.Hypot(a.x, a.y) }
func dot(a, b vector) float64 { return a.x*b.x + a.y*b.y }

type gridCell struct{ x, y int }
type indexedLink struct {
	start, delta vector
	length       float64
	link         speedLink
}

func cell(p vector) gridCell { return gridCell{int(math.Floor(p.x / 100)), int(math.Floor(p.y / 100))} }

func estimateTraffic(ctx context.Context, route Route, snapshot *speedSnapshot) *TrafficEstimate {
	baseline := *route.DurationSeconds
	info := &TrafficEstimate{Source: "lta", Status: "insufficient_coverage", BaselineSeconds: baseline, ObservedAt: &snapshot.observedAt, FetchedAt: &snapshot.fetchedAt, ExpiresAt: &snapshot.expiresAt, Message: "Too little of this route matches fresh LTA speeds; showing OneMap's baseline time."}
	if !snapshot.expiresAt.After(time.Now()) {
		info.Status = "unavailable"
		info.Message = "LTA observations expired; showing OneMap's baseline time."
		return info
	}
	grid := map[gridCell][]int{}
	var indexed []indexedLink
	gridEntries := 0
	for i, link := range snapshot.links {
		if i%128 == 0 && ctx.Err() != nil {
			info.Status = "unavailable"
			info.Message = "Traffic matching was canceled; showing OneMap's baseline time."
			return info
		}
		a, b := project(link.start), project(link.end)
		d := sub(b, a)
		size := length(d)
		if size < 1 || size > 5000 {
			continue
		}
		low := cell(vector{math.Min(a.x, b.x) - 30, math.Min(a.y, b.y) - 30})
		high := cell(vector{math.Max(a.x, b.x) + 30, math.Max(a.y, b.y) + 30})
		if (high.x-low.x+1)*(high.y-low.y+1) > 1000 {
			continue
		}
		gridEntries += (high.x - low.x + 1) * (high.y - low.y + 1)
		if gridEntries > 2000000 {
			info.Status = "unavailable"
			info.Message = "Traffic matching exceeded its memory budget; showing OneMap's baseline time."
			return info
		}
		index := len(indexed)
		indexed = append(indexed, indexedLink{a, d, size, link})
		for x := low.x; x <= high.x; x++ {
			for y := low.y; y <= high.y; y++ {
				key := gridCell{x, y}
				grid[key] = append(grid[key], index)
			}
		}
	}
	total := 0.0
	for i := 1; i < len(route.Geometry); i++ {
		total += length(sub(project(route.Geometry[i]), project(route.Geometry[i-1])))
	}
	if total <= 0 || route.DistanceMeters <= 0 || total < route.DistanceMeters*.6 || total > route.DistanceMeters*1.6 {
		info.Status = "no_geometry"
		info.Message = "Route geometry could not be matched reliably; showing OneMap's baseline time."
		return info
	}
	matched, estimated, minTime, maxTime := 0.0, 0.0, 0.0, 0.0
	used := map[string]bool{}
	sampleCount := 0
	for i := 1; i < len(route.Geometry); i++ {
		if ctx.Err() != nil {
			info.Status = "unavailable"
			info.Message = "Traffic matching was canceled; showing OneMap's baseline time."
			return info
		}
		a, b := project(route.Geometry[i-1]), project(route.Geometry[i])
		direction := sub(b, a)
		size := length(direction)
		if size < .01 {
			continue
		}
		count := int(math.Ceil(size / 25))
		sampleCount += count
		if sampleCount > 20000 {
			info.Status = "no_geometry"
			info.Message = "Route exceeds the traffic matching limit; showing OneMap's baseline time."
			return info
		}
		for n := 0; n < count; n++ {
			fraction := (float64(n) + .5) / float64(count)
			mid := vector{a.x + direction.x*fraction, a.y + direction.y*fraction}
			best, distance := -1, 30.0
			for _, candidate := range grid[cell(mid)] {
				link := indexed[candidate]
				// Reject opposite-direction lanes and most crossing roads.
				if dot(direction, link.delta)/(size*link.length) < .85 {
					continue
				}
				progress := dot(sub(mid, link.start), link.delta) / (link.length * link.length)
				if progress < 0 || progress > 1 {
					continue
				}
				closest := vector{link.start.x + progress*link.delta.x, link.start.y + progress*link.delta.y}
				offset := length(sub(mid, closest))
				if offset < distance {
					best, distance = candidate, offset
				}
			}
			if best < 0 {
				continue
			}
			link := indexed[best].link
			meters := size / float64(count)
			matched += meters
			used[link.id] = true
			// A zero lower speed cannot yield a finite time. 2 km/h is an explicit
			// crawl-speed assumption; this is not a measured queue-delay model.
			low := math.Max(2, link.minimum)
			high := math.Max(low, link.maximum)
			typical := math.Max(low, (link.minimum+link.maximum)/2)
			scaled := meters * route.DistanceMeters / total
			estimated += scaled / typical * 3.6
			minTime += scaled / high * 3.6
			maxTime += scaled / low * 3.6
		}
	}
	info.Coverage = math.Min(1, matched/total)
	info.MatchedMeters = route.DistanceMeters * info.Coverage
	info.MatchedLinks = len(used)
	if info.Coverage < .2 {
		return info
	}
	uncovered := baseline * (1 - info.Coverage)
	estimated += uncovered
	minTime += uncovered
	maxTime += uncovered
	if !validValue(&estimated) || maxTime > 86400 {
		info.Status = "unavailable"
		info.Message = "Traffic time could not be estimated reliably; showing OneMap's baseline time."
		return info
	}
	info.Status = "applied"
	info.EstimatedSeconds = &estimated
	info.MinSeconds = &minTime
	info.MaxSeconds = &maxTime
	info.Message = "Approximate LTA speed-band estimate for matched roads; unmatched portions retain OneMap's baseline. This is not a validated ETA or a traffic-optimized route."
	return info
}
