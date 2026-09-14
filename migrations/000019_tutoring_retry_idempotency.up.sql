ALTER TABLE tutor_responses ADD COLUMN retry_request_id TEXT;
ALTER TABLE tutor_responses ADD COLUMN retry_request_digest BLOB
    CHECK (retry_request_digest IS NULL OR length(retry_request_digest) = 32);

CREATE UNIQUE INDEX tutor_responses_retry_request_idx
    ON tutor_responses(session_id, retry_request_id)
    WHERE retry_request_id IS NOT NULL;

CREATE TRIGGER tutor_responses_retry_request_pair_insert
BEFORE INSERT ON tutor_responses
WHEN (NEW.retry_request_id IS NULL) != (NEW.retry_request_digest IS NULL)
BEGIN
    SELECT RAISE(ABORT, 'retry request identity must be complete');
END;

CREATE TRIGGER tutor_responses_retry_request_pair_update
BEFORE UPDATE OF retry_request_id, retry_request_digest ON tutor_responses
WHEN (NEW.retry_request_id IS NULL) != (NEW.retry_request_digest IS NULL)
BEGIN
    SELECT RAISE(ABORT, 'retry request identity must be complete');
END;
