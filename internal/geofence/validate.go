package geofence

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Kinds of fence (GF-261).
const (
	KindPolygon = "polygon"
	KindCircle  = "circle"
)

// ErrValidation marks a body the server refuses (GF-264, GF-265). The HTTP
// layer answers 400 validation_failed and returns no reason (apierr has none).
var ErrValidation = errors.New("geofence: validation failed")

// Sentinels for the store. The HTTP layer maps each to one apierr code.
var (
	ErrNotFound = errors.New("geofence: not found")
	ErrConflict = errors.New("geofence: conflict")
)

const (
	maxNameRunes     = 100
	maxPolygonPoints = 1000
	minPolygonPoints = 4
	maxRadiusM       = 100000
	maxDwellMin      = 1440
	maxTzLen         = 64
)

// hhmm is the GF-265 time shape: 24-hour HH:MM.
var hhmm = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

// Schedule is the stored and returned schedule (GF-265). Field order is the
// canonical JSON order. Days are sorted and distinct.
type Schedule struct {
	Days []int  `json:"days"`
	From string `json:"from"`
	To   string `json:"to"`
	Tz   string `json:"tz"`
}

// Input is a validated fence, ready for the store. Coordinates are [lng, lat].
type Input struct {
	Name     string
	Kind     string
	Ring     [][2]float64 // polygon: closed exterior ring, as posted
	Centre   [2]float64   // circle: centre point
	RadiusM  float64      // circle: radius in metres
	DwellMin *int
	Schedule *Schedule
}

// featureIn mirrors the posted Feature. The decoder rejects unknown keys at
// every level (GF-263). id and bbox are accepted and ignored.
type featureIn struct {
	Type       string          `json:"type"`
	ID         json.RawMessage `json:"id"`
	BBox       json.RawMessage `json:"bbox"`
	Geometry   *geometryIn     `json:"geometry"`
	Properties *propertiesIn   `json:"properties"`
}

type geometryIn struct {
	Type        string          `json:"type"`
	Coordinates json.RawMessage `json:"coordinates"`
}

type propertiesIn struct {
	Name     *string         `json:"name"`
	Kind     *string         `json:"kind"`
	RadiusM  *float64        `json:"radius_m"`
	DwellMin *int            `json:"dwell_min"`
	Schedule json.RawMessage `json:"schedule"`
}

type scheduleIn struct {
	Days []int   `json:"days"`
	From *string `json:"from"`
	To   *string `json:"to"`
	Tz   *string `json:"tz"`
}

// Parse decodes and validates one posted Feature. Every failure wraps ErrValidation.
func Parse(r io.Reader) (Input, error) {
	var f featureIn
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return Input{}, bad("body")
	}
	if f.Type != "Feature" || f.Geometry == nil || f.Properties == nil {
		return Input{}, bad("feature")
	}
	p := f.Properties
	if p.Name == nil || p.Kind == nil {
		return Input{}, bad("name or kind")
	}
	in := Input{Kind: *p.Kind}
	name := strings.TrimSpace(*p.Name)
	if n := utf8.RuneCountInString(name); n < 1 || n > maxNameRunes {
		return Input{}, bad("name")
	}
	in.Name = name

	if p.DwellMin != nil && (*p.DwellMin < 1 || *p.DwellMin > maxDwellMin) {
		return Input{}, bad("dwell_min")
	}
	in.DwellMin = p.DwellMin

	sched, err := parseSchedule(p.Schedule)
	if err != nil {
		return Input{}, err
	}
	in.Schedule = sched

	switch in.Kind {
	case KindPolygon:
		if p.RadiusM != nil {
			return Input{}, bad("radius_m on polygon")
		}
		if f.Geometry.Type != "Polygon" {
			return Input{}, bad("geometry kind")
		}
		ring, err := parsePolygon(f.Geometry.Coordinates)
		if err != nil {
			return Input{}, err
		}
		in.Ring = ring
	case KindCircle:
		if p.RadiusM == nil {
			return Input{}, bad("radius_m missing")
		}
		if r := *p.RadiusM; !(r >= 1 && r <= maxRadiusM) {
			return Input{}, bad("radius_m")
		}
		if f.Geometry.Type != "Point" {
			return Input{}, bad("geometry kind")
		}
		var c []float64
		if err := json.Unmarshal(f.Geometry.Coordinates, &c); err != nil {
			return Input{}, bad("point")
		}
		pos, err := position(c)
		if err != nil {
			return Input{}, err
		}
		in.Centre = pos
		in.RadiusM = *p.RadiusM
	default:
		return Input{}, bad("kind")
	}
	return in, nil
}

// parsePolygon checks GF-264: one exterior ring, 4 to 1000 positions, closed,
// in range, and no longitude span over 180 degrees. Validity is checked in the
// store, where PostGIS can judge it.
func parsePolygon(raw json.RawMessage) ([][2]float64, error) {
	var rings [][][]float64
	if err := json.Unmarshal(raw, &rings); err != nil {
		return nil, bad("polygon")
	}
	if len(rings) != 1 {
		return nil, bad("polygon rings")
	}
	src := rings[0]
	if len(src) < minPolygonPoints || len(src) > maxPolygonPoints {
		return nil, bad("polygon positions")
	}
	ring := make([][2]float64, len(src))
	minLng, maxLng := math.Inf(1), math.Inf(-1)
	for i, p := range src {
		pos, err := position(p)
		if err != nil {
			return nil, err
		}
		ring[i] = pos
		minLng = math.Min(minLng, pos[0])
		maxLng = math.Max(maxLng, pos[0])
	}
	if ring[0] != ring[len(ring)-1] {
		return nil, bad("ring not closed")
	}
	if maxLng-minLng > 180 {
		return nil, bad("longitude span")
	}
	return ring, nil
}

// position reads one [lng, lat] pair in range and finite.
func position(p []float64) ([2]float64, error) {
	if len(p) != 2 {
		return [2]float64{}, bad("position")
	}
	lng, lat := p[0], p[1]
	if math.IsNaN(lng) || math.IsInf(lng, 0) || math.IsNaN(lat) || math.IsInf(lat, 0) {
		return [2]float64{}, bad("position finite")
	}
	if lng < -180 || lng > 180 || lat < -90 || lat > 90 {
		return [2]float64{}, bad("position range")
	}
	return [2]float64{lng, lat}, nil
}

// parseSchedule validates GF-265. Absent or null is nil (always active). The
// result is canonical: days sorted, and the overnight window is allowed.
func parseSchedule(raw json.RawMessage) (*Schedule, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var s scheduleIn
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return nil, bad("schedule")
	}
	if len(s.Days) == 0 {
		return nil, bad("schedule days")
	}
	seen := map[int]bool{}
	for _, d := range s.Days {
		if d < 1 || d > 7 || seen[d] {
			return nil, bad("schedule days")
		}
		seen[d] = true
	}
	if s.From == nil || s.To == nil || s.Tz == nil {
		return nil, bad("schedule keys")
	}
	if !hhmm.MatchString(*s.From) || !hhmm.MatchString(*s.To) || *s.From == *s.To {
		return nil, bad("schedule time")
	}
	if n := len(*s.Tz); n < 1 || n > maxTzLen || *s.Tz == "Local" {
		return nil, bad("schedule tz")
	}
	if _, err := time.LoadLocation(*s.Tz); err != nil {
		return nil, bad("schedule tz")
	}
	days := append([]int(nil), s.Days...)
	sort.Ints(days)
	return &Schedule{Days: days, From: *s.From, To: *s.To, Tz: *s.Tz}, nil
}

// geometryJSON returns the GeoJSON geometry text the store writes, built from
// the validated values only.
func (in Input) geometryJSON() (string, error) {
	var g map[string]any
	if in.Kind == KindPolygon {
		ring := make([][]float64, len(in.Ring))
		for i, p := range in.Ring {
			ring[i] = []float64{p[0], p[1]}
		}
		g = map[string]any{"type": "Polygon", "coordinates": [][][]float64{ring}}
	} else {
		g = map[string]any{"type": "Point", "coordinates": []float64{in.Centre[0], in.Centre[1]}}
	}
	b, err := json.Marshal(g)
	return string(b), err
}

func bad(what string) error {
	return fmt.Errorf("%w: %s", ErrValidation, what)
}
