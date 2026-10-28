DROP TRIGGER IF EXISTS geofence_events_no_truncate ON geofence_events;
DROP TRIGGER IF EXISTS geofence_events_no_update_delete ON geofence_events;
DROP TABLE IF EXISTS geofence_events;
DROP FUNCTION IF EXISTS geofence_events_append_only();

DROP TABLE IF EXISTS geofences;
