// Package mapview serves GET /map/vehicles: the caller's fleet's latest vehicle
// positions inside a bounding box, with the D-27 display status (P8).
package mapview

import (
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// ErrBBox is every bbox rejection (GF-127). It never carries the offending value.
var ErrBBox = errors.New("mapview: invalid bbox")

// BBox is a validated viewport in GeoJSON and MapLibre order (GF-127).
type BBox struct {
	MinLng, MinLat, MaxLng, MaxLat float64
}

// decimalPattern accepts plain decimal numbers only: no exponent, hex, or
// underscores, which strconv.ParseFloat would otherwise accept.
var decimalPattern = regexp.MustCompile(`^[+-]?(\d+(\.\d*)?|\.\d+)$`)

// ParseBBox validates the bbox query values. vals holds every occurrence of
// the parameter, so a missing or duplicated parameter has len != 1 (GF-127).
func ParseBBox(vals []string) (BBox, error) {
	if len(vals) != 1 {
		return BBox{}, ErrBBox
	}
	parts := strings.Split(vals[0], ",")
	if len(parts) != 4 {
		return BBox{}, ErrBBox
	}
	var n [4]float64
	for i, p := range parts {
		if !decimalPattern.MatchString(p) {
			return BBox{}, ErrBBox
		}
		f, err := strconv.ParseFloat(p, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return BBox{}, ErrBBox
		}
		n[i] = f
	}
	b := BBox{MinLng: n[0], MinLat: n[1], MaxLng: n[2], MaxLat: n[3]}
	// Strict inequalities reject zero-area and inverted boxes, including
	// antimeridian-crossing ones (GF-127).
	if !(-180 <= b.MinLng && b.MinLng < b.MaxLng && b.MaxLng <= 180) {
		return BBox{}, ErrBBox
	}
	if !(-90 <= b.MinLat && b.MinLat < b.MaxLat && b.MaxLat <= 90) {
		return BBox{}, ErrBBox
	}
	return b, nil
}
