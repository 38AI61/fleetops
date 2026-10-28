package geofence

import (
	"encoding/json"
)

// Feature is the GeoJSON Feature the API returns (GF-263). Top-level id and
// bbox are added by the server; a client's id and bbox are never read.
type Feature struct {
	Type       string          `json:"type"`
	ID         int64           `json:"id"`
	BBox       []float64       `json:"bbox"`
	Geometry   json.RawMessage `json:"geometry"`
	Properties Properties      `json:"properties"`
}

// Properties are the fence fields. Optional fields are omitted when unset.
type Properties struct {
	Name     string    `json:"name"`
	Kind     string    `json:"kind"`
	RadiusM  *float64  `json:"radius_m,omitempty"`
	DwellMin *int      `json:"dwell_min,omitempty"`
	Schedule *Schedule `json:"schedule,omitempty"`
}

// Collection is the GET /geofences FeatureCollection (GF-263).
type Collection struct {
	Type     string    `json:"type"`
	Features []Feature `json:"features"`
}
