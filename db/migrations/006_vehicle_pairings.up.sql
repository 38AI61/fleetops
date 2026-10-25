CREATE TABLE vehicle_pairings (
  id         BIGSERIAL PRIMARY KEY,
  fleet_id   BIGINT NOT NULL REFERENCES fleets(id),
  vehicle_id BIGINT NOT NULL REFERENCES vehicles(id),
  driver_id  BIGINT NULL REFERENCES drivers(id),
  device_id  BIGINT NOT NULL REFERENCES devices(id),
  started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  ended_at   TIMESTAMPTZ NULL,
  CHECK (ended_at IS NULL OR ended_at >= started_at)
);
CREATE UNIQUE INDEX vehicle_pairings_open_vehicle_key ON vehicle_pairings (vehicle_id) WHERE (ended_at IS NULL);
CREATE UNIQUE INDEX vehicle_pairings_open_device_key ON vehicle_pairings (device_id) WHERE (ended_at IS NULL);
CREATE INDEX vehicle_pairings_vehicle_id_started_at_idx ON vehicle_pairings (vehicle_id, started_at);

ALTER TABLE vehicles ADD CONSTRAINT vehicles_paired_device_fkey FOREIGN KEY (paired_device_id) REFERENCES devices(id);
