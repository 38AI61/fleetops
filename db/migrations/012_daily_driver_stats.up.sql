-- Daily driver rollup (D-32, GF-319). One row per fleet, driver and fleet-local day.
-- The natural key is the upsert target for the idempotent rollup (GF-323).
CREATE TABLE daily_driver_stats (
  fleet_id       BIGINT NOT NULL REFERENCES fleets(id),
  driver_id      BIGINT NOT NULL REFERENCES drivers(id),
  day            DATE NOT NULL,
  jobs_completed INTEGER NOT NULL,
  on_time_count  INTEGER NOT NULL,
  on_time_denom  INTEGER NOT NULL,
  distance_m     DOUBLE PRECISION NOT NULL,
  idle_s         INTEGER NOT NULL,
  PRIMARY KEY (fleet_id, driver_id, day),
  CONSTRAINT daily_driver_stats_bounds CHECK (
    on_time_count >= 0 AND on_time_count <= on_time_denom AND on_time_denom >= 0
    AND jobs_completed >= 0 AND distance_m >= 0 AND idle_s >= 0)
);

-- Serves GET /reports/daily?date= without a driver, which the PK prefix cannot (GF-319).
CREATE INDEX daily_driver_stats_fleet_day_idx ON daily_driver_stats (fleet_id, day);
