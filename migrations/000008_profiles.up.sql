ALTER TABLE users ADD COLUMN name TEXT;
ALTER TABLE users ADD COLUMN nickname TEXT;
ALTER TABLE users ADD COLUMN tts_voice TEXT;
ALTER TABLE users ADD COLUMN updated_at TEXT;
ALTER TABLE users ADD COLUMN updated_by TEXT REFERENCES users(id) ON DELETE SET NULL;

CREATE TABLE mobile_verification_challenges (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    requested_by TEXT REFERENCES users(id) ON DELETE SET NULL,
    pending_mobile TEXT NOT NULL,
    code TEXT NOT NULL CHECK (length(code) = 6),
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    last_sent_at TEXT NOT NULL,
    failed_attempts INTEGER NOT NULL DEFAULT 0 CHECK (failed_attempts >= 0 AND failed_attempts <= 5),
    consumed_at TEXT,
    invalidated_at TEXT
);
CREATE INDEX mobile_verification_challenges_user_idx
    ON mobile_verification_challenges(user_id, consumed_at, invalidated_at, expires_at);
