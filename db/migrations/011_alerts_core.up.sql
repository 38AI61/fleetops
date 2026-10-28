-- Per-fleet alert rules (D-08). Three fixed kinds; parameters are configurable, kinds are not.
CREATE TABLE alert_rules (
  id         BIGSERIAL PRIMARY KEY,
  fleet_id   BIGINT NOT NULL REFERENCES fleets(id),
  kind       TEXT NOT NULL CHECK (kind IN ('device_silent','eta_slip','geofence_exit_outside_schedule')),
  params     JSONB NOT NULL DEFAULT '{}',
  enabled    BOOLEAN NOT NULL DEFAULT true,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (fleet_id, kind)
);

-- Alerts (D-09). Append-only except the one-time ack transition (GF-295).
CREATE TABLE alerts (
  id        BIGSERIAL PRIMARY KEY,
  fleet_id  BIGINT NOT NULL REFERENCES fleets(id),
  kind      TEXT NOT NULL CHECK (kind IN ('device_silent','eta_slip','geofence_exit_outside_schedule')),
  severity  TEXT NOT NULL CHECK (severity IN ('warning','critical')),
  payload   JSONB NOT NULL,
  raised_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  acked_by  BIGINT NULL REFERENCES users(id) ON DELETE RESTRICT,
  acked_at  TIMESTAMPTZ NULL,
  CONSTRAINT alerts_ack_pair CHECK ((acked_by IS NULL) = (acked_at IS NULL))
);

-- Backfill: one row per existing fleet per kind (D-08, GF-290, GF-297).
INSERT INTO alert_rules (fleet_id, kind, params, enabled)
SELECT f.id, k.kind, k.params, k.enabled
FROM fleets f
CROSS JOIN (VALUES
  ('device_silent', '{"threshold_s": 600}'::jsonb, true),
  ('eta_slip', '{}'::jsonb, false),
  ('geofence_exit_outside_schedule', '{}'::jsonb, true)
) AS k(kind, params, enabled);

-- Guard (GF-295, copies 010_geofences_events.up.sql:21-35). One UPDATE passes:
-- the ack pair goes from NULL to a value and no other column changes.
CREATE FUNCTION alerts_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'UPDATE'
     AND OLD.acked_by IS NULL AND OLD.acked_at IS NULL
     AND NEW.acked_by IS NOT NULL AND NEW.acked_at IS NOT NULL
     AND NEW.id IS NOT DISTINCT FROM OLD.id
     AND NEW.fleet_id IS NOT DISTINCT FROM OLD.fleet_id
     AND NEW.kind IS NOT DISTINCT FROM OLD.kind
     AND NEW.severity IS NOT DISTINCT FROM OLD.severity
     AND NEW.payload IS NOT DISTINCT FROM OLD.payload
     AND NEW.raised_at IS NOT DISTINCT FROM OLD.raised_at THEN
    RETURN NEW;
  END IF;
  RAISE EXCEPTION 'alerts is append-only except one-time ack' USING ERRCODE = 'check_violation';
END;
$$;

CREATE TRIGGER alerts_guard
  BEFORE UPDATE OR DELETE ON alerts
  FOR EACH ROW EXECUTE FUNCTION alerts_guard();

CREATE TRIGGER alerts_no_truncate
  BEFORE TRUNCATE ON alerts
  FOR EACH STATEMENT EXECUTE FUNCTION alerts_guard();
