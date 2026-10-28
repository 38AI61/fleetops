package geofence

// qCandidates is GF-284: one statement per flush. The observed fixes are unnested,
// each is joined to its vehicle's fleet, and the bbox prefilter runs through
// geofences_bbox_gix. The exact predicate runs on prefilter hits only. A fence
// with no row for a fix is outside that fix.
const qCandidates = `
WITH obs AS (
  SELECT u.vehicle_id, u.msg_id, u.lng, u.lat, u.server_ts
  FROM unnest($1::bigint[], $2::text[]::uuid[], $3::float8[], $4::float8[], $5::text[]::timestamptz[])
       AS u(vehicle_id, msg_id, lng, lat, server_ts)
)
SELECT o.vehicle_id, o.msg_id::text, g.id, g.dwell_min,
       CASE WHEN g.kind = 'polygon' THEN ST_Covers(g.geom, p.geog)
            ELSE ST_DWithin(g.geom, p.geog, g.radius_m) END AS inside
FROM obs o
JOIN vehicles v ON v.id = o.vehicle_id
CROSS JOIN LATERAL (
  SELECT g.id, g.kind, g.geom, g.radius_m, g.dwell_min
  FROM geofences g
  WHERE g.fleet_id = v.fleet_id
    AND g.bbox && ST_SetSRID(ST_MakePoint(o.lng, o.lat), 4326)
) g
CROSS JOIN LATERAL (
  SELECT ST_SetSRID(ST_MakePoint(o.lng, o.lat), 4326)::geography AS geog
) p`

// qRebuild is the GF-280 rebuild. It is served by geofence_events_fleet_at_idx,
// which G1 proves with EXPLAIN.
const qRebuild = `
SELECT geofence_id, kind, occurred_at
FROM geofence_events
WHERE fleet_id = $1 AND vehicle_id = $2
  AND occurred_at >= now() - interval '24 hours'
ORDER BY occurred_at, id`

// qInsertEvents appends the emitted events of one flush (GF-286 fields).
const qInsertEvents = `
INSERT INTO geofence_events (fleet_id, geofence_id, vehicle_id, kind, occurred_at)
SELECT u.fleet_id, u.geofence_id, u.vehicle_id, u.kind, u.occurred_at
FROM unnest($1::bigint[], $2::bigint[], $3::bigint[], $4::text[], $5::text[]::timestamptz[])
     AS u(fleet_id, geofence_id, vehicle_id, kind, occurred_at)`
