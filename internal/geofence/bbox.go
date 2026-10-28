package geofence

import "math"

// minMetresPerDegreeLat is the shortest WGS84 meridian degree, at the equator
// (110574 m). Dividing by the smallest degree length gives the widest latitude
// span, so the box cannot be too small on the spheroid.
const minMetresPerDegreeLat = 110574.0

// bboxMargin covers the gap between a sphere and the spheroid for geodesic arcs (GF-262).
const bboxMargin = 1.01

// cosFloor is the cosine below which the longitude span is taken as the full
// range: a circle that reaches this close to a pole can wrap all longitudes.
const cosFloor = 0.01

// CircleBBox returns a lng/lat box that contains every point within radiusM
// metres of (lng, lat) (GF-262). It is a prefilter bound, so it errs wide: a
// box that is too large costs a candidate check, and one that is too small
// drops a fence.
func CircleBBox(lng, lat, radiusM float64) (minLng, minLat, maxLng, maxLat float64) {
	dLat := radiusM / minMetresPerDegreeLat * bboxMargin
	minLat = math.Max(lat-dLat, -90)
	maxLat = math.Min(lat+dLat, 90)

	// Longitude extent grows toward the pole. The cosine at the poleward edge
	// is the smallest in the box, so it gives the widest span.
	pole := math.Max(math.Abs(minLat), math.Abs(maxLat))
	cosPole := math.Cos(pole * math.Pi / 180)
	if cosPole < cosFloor {
		return -180, minLat, 180, maxLat
	}
	dLng := dLat / cosPole
	if lng-dLng < -180 || lng+dLng > 180 {
		// The circle crosses the antimeridian. A clamped box would miss the far side.
		return -180, minLat, 180, maxLat
	}
	return lng - dLng, minLat, lng + dLng, maxLat
}
