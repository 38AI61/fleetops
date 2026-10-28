package geofence

import (
	"math"
	"testing"
)

// destination returns the point dist metres from (lng, lat) along bearing on a
// sphere of radius R. Three radii approximate the WGS84 spheroid's extremes.
func destination(lng, lat, dist, bearingDeg, R float64) (float64, float64) {
	rad := math.Pi / 180
	delta := dist / R
	theta := bearingDeg * rad
	phi1 := lat * rad
	lambda1 := lng * rad
	phi2 := math.Asin(math.Sin(phi1)*math.Cos(delta) + math.Cos(phi1)*math.Sin(delta)*math.Cos(theta))
	lambda2 := lambda1 + math.Atan2(math.Sin(theta)*math.Sin(delta)*math.Cos(phi1),
		math.Cos(delta)-math.Sin(phi1)*math.Sin(phi2))
	lambda2 = math.Mod(lambda2+3*math.Pi, 2*math.Pi) - math.Pi
	return lambda2 / rad, phi2 / rad
}

// TestCircleBboxCovers checks GF-262: every point on the circle, at every
// 45 degree bearing, lies inside the box. Radii run from 1 m to 100 km, and
// centres at 0, 52.5 and 70 degrees latitude plus one at the antimeridian.
func TestCircleBboxCovers(t *testing.T) {
	radii := map[string]float64{"1m": 1, "500m": 500, "100km": 100000}
	radiusR := map[string]float64{"mean": 6371008.8, "polar": 6356752.3, "equatorial": 6378137}
	centres := []struct {
		name     string
		lng, lat float64
	}{
		{"equator", 13.4, 0},
		{"berlin", 13.4, 52.5},
		{"high-lat", 13.4, 70},
		{"antimeridian", 179.95, 0},
	}
	for _, c := range centres {
		for rName, r := range radii {
			minLng, minLat, maxLng, maxLat := CircleBBox(c.lng, c.lat, r)
			for sName, R := range radiusR {
				for bearing := 0.0; bearing < 360; bearing += 45 {
					lng, lat := destination(c.lng, c.lat, r, bearing, R)
					if lng < minLng || lng > maxLng || lat < minLat || lat > maxLat {
						t.Errorf("%s r=%s R=%s bearing %.0f: point (%.9f, %.9f) outside box [%.9f %.9f %.9f %.9f]",
							c.name, rName, sName, bearing, lng, lat, minLng, minLat, maxLng, maxLat)
					}
				}
			}
		}
	}
}

// TestCircleBboxClampsRanges checks that a box never leaves valid lng/lat.
func TestCircleBboxClampsRanges(t *testing.T) {
	minLng, minLat, maxLng, maxLat := CircleBBox(0, 89.999, 100000)
	if minLat < -90 || maxLat > 90 || minLng < -180 || maxLng > 180 {
		t.Fatalf("box out of range: %v %v %v %v", minLng, minLat, maxLng, maxLat)
	}
	if minLng != -180 || maxLng != 180 {
		t.Fatalf("polar circle must span all longitudes, got %v %v", minLng, maxLng)
	}
}
