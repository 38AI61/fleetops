CREATE TABLE positions (
  msg_id      UUID NOT NULL,
  fleet_id    BIGINT NOT NULL,
  vehicle_id  BIGINT NOT NULL REFERENCES vehicles(id),
  geom        geography(Point, 4326) NOT NULL,
  speed_kmh   REAL NOT NULL,
  heading_deg REAL NOT NULL,
  accuracy_m  REAL NOT NULL,
  device_ts   TIMESTAMPTZ NOT NULL,
  server_ts   TIMESTAMPTZ NOT NULL,
  PRIMARY KEY (server_ts, msg_id)
) PARTITION BY RANGE (server_ts);

CREATE TABLE vehicles_latest (
  vehicle_id   BIGINT PRIMARY KEY REFERENCES vehicles(id),
  fleet_id     BIGINT NOT NULL,
  geom         geography(Point, 4326) NOT NULL,
  speed_kmh    REAL,
  heading_deg  REAL,
  device_ts    TIMESTAMPTZ NOT NULL,
  server_ts    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX vehicles_latest_geom_gix ON vehicles_latest USING GIST (geom);
CREATE INDEX vehicles_latest_fleet_idx ON vehicles_latest (fleet_id);
