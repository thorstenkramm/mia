ALTER TABLE jobs ADD COLUMN state_changed_at TEXT;

UPDATE jobs
SET state_changed_at = CASE state
    WHEN 'queued' THEN CASE WHEN attempt_count = 0 THEN created_at END
    WHEN 'running' THEN CASE WHEN attempt_count = 1 THEN started_at END
    WHEN 'succeeded' THEN COALESCE(finished_at, created_at)
    WHEN 'failed' THEN COALESCE(finished_at, created_at)
    WHEN 'cancelled' THEN COALESCE(finished_at, created_at)
END;
