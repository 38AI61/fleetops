CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE users (
  id            BIGSERIAL PRIMARY KEY,
  fleet_id      BIGINT NOT NULL REFERENCES fleets(id),
  email         CITEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  role          TEXT NOT NULL CHECK (role IN ('admin','manager','dispatcher','driver')),
  driver_id     BIGINT NULL,
  disabled_at   TIMESTAMPTZ NULL,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX users_fleet_id_idx ON users (fleet_id);

CREATE TABLE refresh_tokens (
  id          BIGSERIAL PRIMARY KEY,
  fleet_id    BIGINT NOT NULL REFERENCES fleets(id),
  user_id     BIGINT NOT NULL REFERENCES users(id),
  family_id   UUID NOT NULL,
  token_hash  BYTEA NOT NULL UNIQUE,
  issued_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at  TIMESTAMPTZ NOT NULL,
  rotated_at  TIMESTAMPTZ NULL,
  revoked_at  TIMESTAMPTZ NULL
);
CREATE INDEX refresh_tokens_user_id_idx ON refresh_tokens (user_id);
CREATE INDEX refresh_tokens_family_id_idx ON refresh_tokens (family_id);
