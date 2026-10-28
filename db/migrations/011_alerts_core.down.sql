DROP TRIGGER IF EXISTS alerts_no_truncate ON alerts;
DROP TRIGGER IF EXISTS alerts_guard ON alerts;
DROP TABLE IF EXISTS alerts;
DROP FUNCTION IF EXISTS alerts_guard();

DROP TABLE IF EXISTS alert_rules;
