-- Finding 1: Enforce token_digest is exactly 32 bytes (64 hex characters when stored as BLOB)
-- and prevent transitions from terminal states.

-- SQLite CHECK constraints can validate BLOB length.
-- token_digest is stored as a 32-byte BLOB (SHA-256 hash).
-- Add trigger to validate token_digest length on INSERT and UPDATE.

CREATE TRIGGER check_token_digest_length_insert
BEFORE INSERT ON invitations
WHEN NEW.token_digest IS NOT NULL AND length(NEW.token_digest) != 32
BEGIN
    SELECT RAISE(ABORT, 'token_digest must be exactly 32 bytes');
END;

CREATE TRIGGER check_token_digest_length_update
BEFORE UPDATE ON invitations
WHEN NEW.token_digest IS NOT NULL AND length(NEW.token_digest) != 32
BEGIN
    SELECT RAISE(ABORT, 'token_digest must be exactly 32 bytes');
END;

-- Prevent transitions FROM terminal states (accepted, revoked, faulty).
-- Terminal states can only have their attribution columns set to NULL via ON DELETE SET NULL,
-- which doesn't change the status. Any other modification is forbidden.
--
-- The trigger allows:
-- 1. Setting inviter_id, accepted_by, or revoked_by to NULL (ON DELETE SET NULL)
-- 2. Updating updated_at (which accompanies cascades)
--
-- The trigger forbids:
-- 1. Any status change from a terminal state
-- 2. Changes to immutable fields (id, email, role, timestamps, etc.)
-- 3. Setting attribution columns to non-NULL values different from current
CREATE TRIGGER prevent_terminal_state_transition
BEFORE UPDATE ON invitations
WHEN OLD.status IN ('accepted', 'revoked', 'faulty')
    AND (
        -- Status change from terminal is always forbidden
        NEW.status != OLD.status
        -- Immutable fields must not change
        OR NEW.id != OLD.id
        OR NEW.email != OLD.email
        OR NEW.email_normalized != OLD.email_normalized
        OR NEW.role != OLD.role
        -- token_digest must stay NULL in terminal states
        OR NEW.token_digest IS NOT NULL
        OR NEW.token_generation != OLD.token_generation
        -- failure_code is immutable once set (NULL safe comparison)
        OR (NEW.failure_code IS NOT NULL AND OLD.failure_code IS NOT NULL AND NEW.failure_code != OLD.failure_code)
        OR (NEW.failure_code IS NOT NULL AND OLD.failure_code IS NULL)
        OR (NEW.failure_code IS NULL AND OLD.failure_code IS NOT NULL)
        OR NEW.created_at != OLD.created_at
        -- Timestamps are immutable once set (NULL safe comparison)
        OR (NEW.sent_at IS NOT NULL AND OLD.sent_at IS NOT NULL AND NEW.sent_at != OLD.sent_at)
        OR (NEW.sent_at IS NOT NULL AND OLD.sent_at IS NULL)
        OR (NEW.sent_at IS NULL AND OLD.sent_at IS NOT NULL)
        OR (NEW.accepted_at IS NOT NULL AND OLD.accepted_at IS NOT NULL AND NEW.accepted_at != OLD.accepted_at)
        OR (NEW.accepted_at IS NOT NULL AND OLD.accepted_at IS NULL)
        OR (NEW.accepted_at IS NULL AND OLD.accepted_at IS NOT NULL)
        OR (NEW.revoked_at IS NOT NULL AND OLD.revoked_at IS NOT NULL AND NEW.revoked_at != OLD.revoked_at)
        OR (NEW.revoked_at IS NOT NULL AND OLD.revoked_at IS NULL)
        OR (NEW.revoked_at IS NULL AND OLD.revoked_at IS NOT NULL)
        OR (NEW.fault_at IS NOT NULL AND OLD.fault_at IS NOT NULL AND NEW.fault_at != OLD.fault_at)
        OR (NEW.fault_at IS NOT NULL AND OLD.fault_at IS NULL)
        OR (NEW.fault_at IS NULL AND OLD.fault_at IS NOT NULL)
        -- Attribution can only go from non-NULL to NULL (ON DELETE SET NULL), not to different values
        -- inviter_id: allow NULL, forbid change to different non-NULL
        OR (NEW.inviter_id IS NOT NULL AND OLD.inviter_id IS NOT NULL AND NEW.inviter_id != OLD.inviter_id)
        OR (NEW.inviter_id IS NOT NULL AND OLD.inviter_id IS NULL)
        -- accepted_by: allow NULL, forbid change to different non-NULL
        OR (NEW.accepted_by IS NOT NULL AND OLD.accepted_by IS NOT NULL AND NEW.accepted_by != OLD.accepted_by)
        OR (NEW.accepted_by IS NOT NULL AND OLD.accepted_by IS NULL)
        -- revoked_by: allow NULL, forbid change to different non-NULL
        OR (NEW.revoked_by IS NOT NULL AND OLD.revoked_by IS NOT NULL AND NEW.revoked_by != OLD.revoked_by)
        OR (NEW.revoked_by IS NOT NULL AND OLD.revoked_by IS NULL)
    )
BEGIN
    SELECT RAISE(ABORT, 'cannot modify terminal invitation state');
END;
