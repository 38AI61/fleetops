CREATE TABLE admin_audit (
  id        BIGSERIAL PRIMARY KEY,
  fleet_id  BIGINT NOT NULL REFERENCES fleets(id),
  actor_id  BIGINT NOT NULL REFERENCES users(id),
  action    TEXT NOT NULL,
  target    TEXT NOT NULL,
  at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX admin_audit_fleet_id_at_idx ON admin_audit (fleet_id, at);

CREATE FUNCTION admin_audit_append_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  RAISE EXCEPTION 'admin_audit is append-only' USING ERRCODE = 'check_violation';
END;
$$;

CREATE TRIGGER admin_audit_no_update_delete
  BEFORE UPDATE OR DELETE ON admin_audit
  FOR EACH ROW EXECUTE FUNCTION admin_audit_append_only();

CREATE TRIGGER admin_audit_no_truncate
  BEFORE TRUNCATE ON admin_audit
  FOR EACH STATEMENT EXECUTE FUNCTION admin_audit_append_only();
