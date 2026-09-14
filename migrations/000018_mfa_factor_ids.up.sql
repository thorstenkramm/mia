CREATE TABLE mfa_factors_with_ids (
    user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    id TEXT NOT NULL UNIQUE,
    method TEXT NOT NULL CHECK (method IN ('totp', 'sms')),
    totp_secret BLOB,
    sms_destination TEXT,
    last_totp_step INTEGER,
    created_at TEXT NOT NULL,
    CHECK ((method = 'totp' AND totp_secret IS NOT NULL AND sms_destination IS NULL)
        OR (method = 'sms' AND totp_secret IS NULL AND sms_destination IS NOT NULL))
);

INSERT INTO mfa_factors_with_ids
    (user_id, id, method, totp_secret, sms_destination, last_totp_step, created_at)
SELECT user_id,
    'mff_' || lower(hex(randomblob(4))) || '-' || lower(hex(randomblob(2))) || '-4' ||
        substr(lower(hex(randomblob(2))), 2) || '-' ||
        substr('89ab', abs(random() % 4) + 1, 1) || substr(lower(hex(randomblob(2))), 2) || '-' ||
        lower(hex(randomblob(6))),
    method, totp_secret, sms_destination, last_totp_step, created_at
FROM mfa_factors;

DROP TABLE mfa_factors;
ALTER TABLE mfa_factors_with_ids RENAME TO mfa_factors;
