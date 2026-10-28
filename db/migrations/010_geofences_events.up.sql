CREATE TABLE geofences (
  id         BIGSERIAL PRIMARY KEY,
  fleet_id   BIGINT NOT NULL REFERENCES fleets(id),
  name       TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
  kind       TEXT NOT NULL CHECK (kind IN ('polygon','circle')),
  geom       geography(Geometry,4326) NOT NULL,
  radius_m   DOUBLE PRECISION,
  bbox       geometry(Polygon,4326) NOT NULL,
  dwell_min  INTEGER CHECK (dwell_min BETWEEN 1 AND 1440),
  schedule   JSONB,
  CONSTRAINT geofences_shape CHECK (
    (kind = 'polygon' AND radius_m IS NULL AND GeometryType(geom::geometry) = 'POLYGON')
    OR (kind = 'circle' AND radius_m IS NOT NULL AND radius_m BETWEEN 1 AND 100000
        AND GeometryType(geom::geometry) = 'POINT')
  )
);
-- Case-insensitive unique name per fleet (GF-265). The store maps 23505 to 409.
CREATE UNIQUE INDEX geofences_fleet_name_uq ON geofences (fleet_id, lower(name));
-- P18's bbox prefilter uses this index (GF-270). P17 runs no prefilter in production.
CREATE INDEX geofences_bbox_gix ON geofences USING GIST (bbox);

CREATE TABLE geofence_events (
  id          BIGSERIAL PRIMARY KEY,
  fleet_id    BIGINT NOT NULL REFERENCES fleets(id),
  geofence_id BIGINT NOT NULL REFERENCES geofences(id) ON DELETE RESTRICT,
  vehicle_id  BIGINT NOT NULL REFERENCES vehicles(id),
  kind        TEXT NOT NULL CHECK (kind IN ('enter','exit','dwell')),
  occurred_at TIMESTAMPTZ NOT NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX geofence_events_fleet_at_idx ON geofence_events (fleet_id, occurred_at);

-- Append-only (R-7, GF-266). Copies 009_jobs_core.up.sql:61-74.
CREATE FUNCTION geofence_events_append_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'geofence_events is append-only' USING ERRCODE = 'check_violation';
END;
$$;

CREATE TRIGGER geofence_events_no_update_delete
  BEFORE UPDATE OR DELETE ON geofence_events
  FOR EACH ROW EXECUTE FUNCTION geofence_events_append_only();

CREATE TRIGGER geofence_events_no_truncate
  BEFORE TRUNCATE ON geofence_events
  FOR EACH STATEMENT EXECUTE FUNCTION geofence_events_append_only();
