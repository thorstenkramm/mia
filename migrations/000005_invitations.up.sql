CREATE TABLE invitations (
    id TEXT PRIMARY KEY,
    email TEXT NOT NULL,
    email_normalized TEXT NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('administrator', 'supervisor', 'mentor')),
    token_digest BLOB,
    token_generation INTEGER NOT NULL DEFAULT 1,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'accepted', 'revoked', 'faulty')),
    failure_code TEXT,
    inviter_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    sent_at TEXT,
    accepted_at TEXT,
    revoked_at TEXT,
    fault_at TEXT,
    CHECK (
        (status = 'pending' AND token_digest IS NOT NULL) OR
        (status IN ('accepted', 'revoked', 'faulty') AND token_digest IS NULL)
    )
);

CREATE UNIQUE INDEX idx_invitations_pending_email ON invitations(email_normalized) WHERE status = 'pending';
CREATE UNIQUE INDEX idx_invitations_pending_token ON invitations(token_digest) WHERE status = 'pending';
CREATE INDEX idx_invitations_inviter ON invitations(inviter_id);
