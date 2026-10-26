DROP TRIGGER IF EXISTS job_events_no_truncate ON job_events;
DROP TRIGGER IF EXISTS job_events_no_update_delete ON job_events;
DROP TABLE IF EXISTS job_events;

DROP TRIGGER IF EXISTS jobs_record_create ON jobs;
DROP TRIGGER IF EXISTS jobs_transition_guard ON jobs;
DROP TABLE IF EXISTS jobs;

DROP TABLE IF EXISTS job_transitions;

DROP FUNCTION IF EXISTS jobs_record_create();
DROP FUNCTION IF EXISTS jobs_transition_guard();
DROP FUNCTION IF EXISTS job_events_append_only();
