CREATE TABLE jobs (
  id              BIGSERIAL PRIMARY KEY,
  fleet_id        BIGINT NOT NULL REFERENCES fleets(id),
  state           TEXT NOT NULL DEFAULT 'created',
  vehicle_id      BIGINT REFERENCES vehicles(id),
  driver_id       BIGINT REFERENCES drivers(id),
  pickup          geography(Point, 4326) NOT NULL,
  dropoff         geography(Point, 4326) NOT NULL,
  window_start    TIMESTAMPTZ,
  window_end      TIMESTAMPTZ,
  eta_minutes     INT,
  proof_photo     TEXT,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  delivered_at    TIMESTAMPTZ,
  notes           TEXT,
  size_class      TEXT,
  weight_kg       NUMERIC(10,2),
  delivery_geom   geography(Point, 4326),
  failure_reason  TEXT,
  CONSTRAINT jobs_state_vocab CHECK (state IN ('created','assigned','accepted','en_route','picked_up','delivered','failed','cancelled')),
  CONSTRAINT jobs_assignment_consistent CHECK (state IN ('created','cancelled') OR (vehicle_id IS NOT NULL AND driver_id IS NOT NULL)),
  CONSTRAINT jobs_window_order CHECK (window_start IS NULL OR window_end IS NULL OR window_end > window_start),
  CONSTRAINT jobs_eta_range CHECK (eta_minutes IS NULL OR eta_minutes BETWEEN 0 AND 1440),
  CONSTRAINT jobs_weight_positive CHECK (weight_kg IS NULL OR weight_kg > 0),
  CONSTRAINT jobs_size_class CHECK (size_class IS NULL OR size_class IN ('small','medium','large')),
  CONSTRAINT jobs_notes_len CHECK (notes IS NULL OR char_length(notes) <= 1000)
);
CREATE INDEX jobs_fleet_state_idx ON jobs (fleet_id, state);
CREATE INDEX jobs_fleet_window_idx ON jobs (fleet_id, window_start);

-- D-23 edges. internal/jobs/transitions.go is the Go source of this table.
CREATE TABLE job_transitions (
  from_state TEXT NOT NULL CHECK (from_state IN ('created','assigned','accepted','en_route','picked_up','delivered','failed','cancelled')),
  to_state   TEXT NOT NULL CHECK (to_state   IN ('created','assigned','accepted','en_route','picked_up','delivered','failed','cancelled')),
  PRIMARY KEY (from_state, to_state)
);
INSERT INTO job_transitions (from_state, to_state) VALUES
  ('created',   'assigned'),
  ('assigned',  'assigned'),
  ('assigned',  'accepted'),
  ('accepted',  'en_route'),
  ('en_route',  'picked_up'),
  ('picked_up', 'delivered'),
  ('accepted',  'failed'),
  ('en_route',  'failed'),
  ('picked_up', 'failed'),
  ('created',   'cancelled'),
  ('assigned',  'cancelled'),
  ('accepted',  'cancelled');

CREATE TABLE job_events (
  id          BIGSERIAL PRIMARY KEY,
  job_id      BIGINT NOT NULL REFERENCES jobs(id),
  from_state  TEXT NULL CHECK (from_state IS NULL OR from_state IN ('created','assigned','accepted','en_route','picked_up','delivered','failed','cancelled')),
  to_state    TEXT NOT NULL CHECK (to_state IN ('created','assigned','accepted','en_route','picked_up','delivered','failed','cancelled')),
  actor_id    BIGINT NOT NULL REFERENCES users(id),
  at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX job_events_job_id_idx ON job_events (job_id, id);

CREATE FUNCTION job_events_append_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'job_events is append-only' USING ERRCODE = 'check_violation';
END;
$$;

CREATE TRIGGER job_events_no_update_delete
  BEFORE UPDATE OR DELETE ON job_events
  FOR EACH ROW EXECUTE FUNCTION job_events_append_only();

CREATE TRIGGER job_events_no_truncate
  BEFORE TRUNCATE ON job_events
  FOR EACH STATEMENT EXECUTE FUNCTION job_events_append_only();

-- BEFORE UPDATE guard (GF-171). FL409 = illegal transition (HTTP 409).
-- FL428 = event-writing change without app.actor_id (HTTP 500, programming error).
CREATE FUNCTION jobs_transition_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  actor BIGINT;
  assign_changed BOOLEAN;
BEGIN
  IF NEW.fleet_id <> OLD.fleet_id THEN
    RAISE EXCEPTION 'jobs.fleet_id is immutable' USING ERRCODE = 'check_violation';
  END IF;

  assign_changed := NEW.vehicle_id IS DISTINCT FROM OLD.vehicle_id
                 OR NEW.driver_id IS DISTINCT FROM OLD.driver_id;

  IF NEW.state <> OLD.state THEN
    IF NOT EXISTS (SELECT 1 FROM job_transitions
                   WHERE from_state = OLD.state AND to_state = NEW.state) THEN
      RAISE EXCEPTION 'illegal job transition % -> %', OLD.state, NEW.state USING ERRCODE = 'FL409';
    END IF;
    IF assign_changed AND NOT (OLD.state = 'created' AND NEW.state = 'assigned') THEN
      RAISE EXCEPTION 'assignment may change only on created -> assigned' USING ERRCODE = 'FL409';
    END IF;
  ELSIF assign_changed THEN
    IF NOT (OLD.state = 'assigned' AND EXISTS (SELECT 1 FROM job_transitions
                                               WHERE from_state = 'assigned' AND to_state = 'assigned')) THEN
      RAISE EXCEPTION 'assignment may change only while assigned (reassign)' USING ERRCODE = 'FL409';
    END IF;
  ELSE
    RETURN NEW;
  END IF;

  actor := NULLIF(current_setting('app.actor_id', true), '')::BIGINT;
  IF actor IS NULL THEN
    RAISE EXCEPTION 'app.actor_id is not set' USING ERRCODE = 'FL428';
  END IF;
  INSERT INTO job_events (job_id, from_state, to_state, actor_id)
    VALUES (OLD.id, OLD.state, NEW.state, actor);
  RETURN NEW;
END;
$$;

CREATE TRIGGER jobs_transition_guard
  BEFORE UPDATE ON jobs
  FOR EACH ROW EXECUTE FUNCTION jobs_transition_guard();

-- AFTER INSERT creation event (GF-171). Creation is not a transition.
CREATE FUNCTION jobs_record_create() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  actor BIGINT;
BEGIN
  actor := NULLIF(current_setting('app.actor_id', true), '')::BIGINT;
  IF actor IS NULL THEN
    RAISE EXCEPTION 'app.actor_id is not set' USING ERRCODE = 'FL428';
  END IF;
  INSERT INTO job_events (job_id, from_state, to_state, actor_id)
    VALUES (NEW.id, NULL, NEW.state, actor);
  RETURN NULL;
END;
$$;

CREATE TRIGGER jobs_record_create
  AFTER INSERT ON jobs
  FOR EACH ROW EXECUTE FUNCTION jobs_record_create();
