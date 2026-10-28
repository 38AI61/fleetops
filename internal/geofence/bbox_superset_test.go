package geofence

import (
	"context"
	"testing"

	"pgregory.net/rapid"

	"fleetops/internal/testinfra"
)

// TestCircleBBoxSupersetProperty: for random circles with centre latitude in
// [-70, 70] and radius 1 m to 100 km, every point that ST_DWithin accepts lies in
// CircleBBox. The truth is PostGIS on the spheroid, not a Go formula. Points are
// drawn from a bearing and distance around the radius, so many fall near the edge.
func TestCircleBBoxSupersetProperty(t *testing.T) {
	db := testinfra.NewMigratedDB(t)
	ctx := context.Background()
	const q = `
WITH c AS (SELECT ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography AS g),
     p AS (SELECT ST_Project(c.g, $3::float8, radians($4::float8))::geography AS g FROM c)
SELECT ST_X(p.g::geometry), ST_Y(p.g::geometry), ST_DWithin(c.g, p.g, $5)
FROM c, p`

	rapid.Check(t, func(rt *rapid.T) {
		lng := rapid.Float64Range(-180, 180).Draw(rt, "lng")
		lat := rapid.Float64Range(-70, 70).Draw(rt, "lat")
		radius := rapid.Float64Range(1, 100000).Draw(rt, "radius")
		bearing := rapid.Float64Range(0, 360).Draw(rt, "bearing")
		// Distance from 0.9 to 1.1 of the radius covers both sides of the edge.
		dist := radius * rapid.Float64Range(0.9, 1.1).Draw(rt, "dist")

		var pLng, pLat float64
		var inside bool
		if err := db.QueryRowContext(ctx, q, lng, lat, dist, bearing, radius).Scan(&pLng, &pLat, &inside); err != nil {
			rt.Fatalf("query: %v", err)
		}
		if !inside {
			return
		}
		minLng, minLat, maxLng, maxLat := CircleBBox(lng, lat, radius)
		if pLng < minLng || pLng > maxLng || pLat < minLat || pLat > maxLat {
			rt.Fatalf("ST_DWithin true for (%v, %v) but outside bbox [%v %v, %v %v]; centre (%v, %v) radius %v",
				pLng, pLat, minLng, minLat, maxLng, maxLat, lng, lat, radius)
		}
	})
}
