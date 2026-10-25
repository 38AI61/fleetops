CREATE TABLE vehicles (
  id               BIGSERIAL PRIMARY KEY,
  fleet_id         BIGINT NOT NULL REFERENCES fleets(id),
  plate            TEXT NOT NULL,
  model            TEXT NULL,
  status           TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','maintenance','retired')),
  paired_device_id BIGINT NULL,
  UNIQUE (fleet_id, plate)
);
CREATE INDEX vehicles_fleet_id_idx ON vehicles (fleet_id);

CREATE TABLE drivers (
  id              BIGSERIAL PRIMARY KEY,
  fleet_id        BIGINT NOT NULL REFERENCES fleets(id),
  name            TEXT NOT NULL,
  license_no_hash BYTEA NOT NULL,
  phone           TEXT NULL,
  shift_start     TIME NULL,
  shift_end       TIME NULL,
  UNIQUE (fleet_id, license_no_hash)
);
CREATE INDEX drivers_fleet_id_idx ON drivers (fleet_id);

CREATE TABLE devices (
  id           BIGSERIAL PRIMARY KEY,
  fleet_id     BIGINT NOT NULL REFERENCES fleets(id),
  kind         TEXT NOT NULL CHECK (kind IN ('pwa','tracker')),
  api_key_hash BYTEA NOT NULL,
  last_seen_at TIMESTAMPTZ NULL,
  revoked_at   TIMESTAMPTZ NULL
);
CREATE INDEX devices_fleet_id_idx ON devices (fleet_id);

ALTER TABLE users ADD CONSTRAINT users_driver_id_fkey FOREIGN KEY (driver_id) REFERENCES drivers(id);
