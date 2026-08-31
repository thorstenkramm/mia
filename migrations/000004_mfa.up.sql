CREATE TABLE mfa_factors (
    user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    method TEXT NOT NULL CHECK (method IN ('totp', 'sms')),
    totp_secret BLOB,
    sms_destination TEXT,
    last_totp_step INTEGER,
    created_at TEXT NOT NULL,
    CHECK ((method = 'totp' AND totp_secret IS NOT NULL AND sms_destination IS NULL)
        OR (method = 'sms' AND totp_secret IS NULL AND sms_destination IS NOT NULL))
);
ALTER TABLE users ADD COLUMN mobile TEXT;
ALTER TABLE users ADD COLUMN mobile_verified_at TEXT;
CREATE TABLE mfa_enrollments (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    method TEXT NOT NULL CHECK (method IN ('totp', 'sms')),
    totp_secret BLOB,
    sms_destination TEXT,
    sms_code TEXT,
    proof_digest BLOB,
    expires_at TEXT NOT NULL,
    failures INTEGER NOT NULL DEFAULT 0 CHECK (failures >= 0 AND failures <= 5),
    created_at TEXT NOT NULL,
    CHECK ((method = 'totp' AND totp_secret IS NOT NULL AND sms_destination IS NULL AND sms_code IS NULL)
        OR (method = 'sms' AND totp_secret IS NULL AND sms_destination IS NOT NULL AND sms_code IS NOT NULL))
);
CREATE UNIQUE INDEX mfa_enrollments_user_idx ON mfa_enrollments(user_id);
CREATE TABLE mfa_challenges (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    method TEXT NOT NULL CHECK (method IN ('totp', 'sms')),
    sms_code TEXT,
    expires_at TEXT NOT NULL,
    failures INTEGER NOT NULL DEFAULT 0 CHECK (failures >= 0 AND failures <= 5),
    consumed_at TEXT,
    created_at TEXT NOT NULL,
    CHECK ((method = 'totp' AND sms_code IS NULL) OR (method = 'sms' AND sms_code IS NOT NULL))
);
CREATE INDEX mfa_challenges_user_idx ON mfa_challenges(user_id, consumed_at, expires_at);
CREATE TABLE mfa_management_proofs (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_digest BLOB NOT NULL UNIQUE,
    expires_at TEXT NOT NULL,
    consumed_at TEXT,
    created_at TEXT NOT NULL
);
CREATE INDEX mfa_management_proofs_user_idx ON mfa_management_proofs(user_id, consumed_at, expires_at);
CREATE TABLE mfa_recovery_codes (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    digest BLOB NOT NULL UNIQUE,
    consumed_at TEXT,
    created_at TEXT NOT NULL
);
CREATE INDEX mfa_recovery_codes_user_idx ON mfa_recovery_codes(user_id, consumed_at);
CREATE TABLE sms_delivery_attempts (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    destination TEXT NOT NULL,
    created_at TEXT NOT NULL
);
CREATE INDEX sms_delivery_attempts_account_destination_idx
    ON sms_delivery_attempts(user_id, destination, created_at);
