-- Finding 1: Add attribution columns and constraints for invitation lifecycle

-- Add accepted_by column with foreign key reference
-- Deleting the accepting user deletes their accepted invitation per product contract.
ALTER TABLE invitations ADD COLUMN accepted_by TEXT REFERENCES users(id) ON DELETE CASCADE;

-- Add revoked_by column with foreign key reference
ALTER TABLE invitations ADD COLUMN revoked_by TEXT REFERENCES users(id) ON DELETE SET NULL;

-- SQLite doesn't support adding CHECK constraints via ALTER TABLE,
-- so we create triggers to enforce lifecycle invariants.
--
-- Lifecycle rules:
-- 1. On INSERT: require appropriate fields based on status, forbid fields from other states
-- 2. On UPDATE to terminal state: require new timestamp and attribution at transition time
-- 3. Allow NULL attribution on terminal states after user deletion (ON DELETE SET NULL)
-- 4. Forbid all fields belonging to other lifecycle states

-- Enforce status-field consistency on INSERT:
-- - pending: accepted_at, revoked_at, fault_at, accepted_by, revoked_by, failure_code must be NULL
-- - accepted: accepted_at and accepted_by must be NOT NULL; revoked_at, revoked_by, fault_at, failure_code must be NULL
-- - revoked: revoked_at and revoked_by must be NOT NULL; accepted_at, accepted_by, fault_at, failure_code must be NULL
-- - faulty: fault_at and failure_code must be NOT NULL; accepted_at, accepted_by, revoked_at, revoked_by must be NULL
CREATE TRIGGER check_invitation_lifecycle_insert
BEFORE INSERT ON invitations
BEGIN
    SELECT CASE
        -- pending: no terminal fields allowed
        WHEN NEW.status = 'pending' AND (
            NEW.accepted_at IS NOT NULL OR NEW.accepted_by IS NOT NULL OR
            NEW.revoked_at IS NOT NULL OR NEW.revoked_by IS NOT NULL OR
            NEW.fault_at IS NOT NULL OR NEW.failure_code IS NOT NULL)
        THEN RAISE(ABORT, 'pending invitation must not have accepted_at, revoked_at, fault_at, or attribution fields')
        -- accepted: must have accepted_at and accepted_by, no other terminal fields
        WHEN NEW.status = 'accepted' AND (NEW.accepted_at IS NULL OR NEW.accepted_by IS NULL)
        THEN RAISE(ABORT, 'accepted invitation must have accepted_at and accepted_by')
        WHEN NEW.status = 'accepted' AND (
            NEW.revoked_at IS NOT NULL OR NEW.revoked_by IS NOT NULL OR
            NEW.fault_at IS NOT NULL OR NEW.failure_code IS NOT NULL)
        THEN RAISE(ABORT, 'accepted invitation must not have revoked or faulty fields')
        -- revoked: must have revoked_at and revoked_by, no other terminal fields
        WHEN NEW.status = 'revoked' AND (NEW.revoked_at IS NULL OR NEW.revoked_by IS NULL)
        THEN RAISE(ABORT, 'revoked invitation must have revoked_at and revoked_by')
        WHEN NEW.status = 'revoked' AND (
            NEW.accepted_at IS NOT NULL OR NEW.accepted_by IS NOT NULL OR
            NEW.fault_at IS NOT NULL OR NEW.failure_code IS NOT NULL)
        THEN RAISE(ABORT, 'revoked invitation must not have accepted or faulty fields')
        -- faulty: must have fault_at and failure_code, no other terminal fields
        WHEN NEW.status = 'faulty' AND (NEW.fault_at IS NULL OR NEW.failure_code IS NULL)
        THEN RAISE(ABORT, 'faulty invitation must have fault_at and failure_code')
        WHEN NEW.status = 'faulty' AND (
            NEW.accepted_at IS NOT NULL OR NEW.accepted_by IS NOT NULL OR
            NEW.revoked_at IS NOT NULL OR NEW.revoked_by IS NOT NULL)
        THEN RAISE(ABORT, 'faulty invitation must not have accepted or revoked fields')
    END;
END;

-- Enforce status-field consistency on UPDATE:
-- On transition to terminal state, require timestamp and attribution
-- Allow NULL attribution after user deletion (when status hasn't changed)
-- Forbid all fields belonging to other lifecycle states
CREATE TRIGGER check_invitation_lifecycle_update
BEFORE UPDATE ON invitations
BEGIN
    SELECT CASE
        -- pending: no terminal fields allowed
        WHEN NEW.status = 'pending' AND (
            NEW.accepted_at IS NOT NULL OR NEW.accepted_by IS NOT NULL OR
            NEW.revoked_at IS NOT NULL OR NEW.revoked_by IS NOT NULL OR
            NEW.fault_at IS NOT NULL OR NEW.failure_code IS NOT NULL)
        THEN RAISE(ABORT, 'pending invitation must not have accepted_at, revoked_at, fault_at, or attribution fields')
        -- accepted: must have accepted_at; accepted_by required only at transition
        WHEN NEW.status = 'accepted' AND NEW.accepted_at IS NULL
        THEN RAISE(ABORT, 'accepted invitation must have accepted_at')
        WHEN NEW.status = 'accepted' AND OLD.status != 'accepted' AND NEW.accepted_by IS NULL
        THEN RAISE(ABORT, 'accepted invitation must have accepted_by at transition')
        WHEN NEW.status = 'accepted' AND (
            NEW.revoked_at IS NOT NULL OR NEW.revoked_by IS NOT NULL OR
            NEW.fault_at IS NOT NULL OR NEW.failure_code IS NOT NULL)
        THEN RAISE(ABORT, 'accepted invitation must not have revoked or faulty fields')
        -- revoked: must have revoked_at; revoked_by required only at transition
        WHEN NEW.status = 'revoked' AND NEW.revoked_at IS NULL
        THEN RAISE(ABORT, 'revoked invitation must have revoked_at')
        WHEN NEW.status = 'revoked' AND OLD.status != 'revoked' AND NEW.revoked_by IS NULL
        THEN RAISE(ABORT, 'revoked invitation must have revoked_by at transition')
        WHEN NEW.status = 'revoked' AND (
            NEW.accepted_at IS NOT NULL OR NEW.accepted_by IS NOT NULL OR
            NEW.fault_at IS NOT NULL OR NEW.failure_code IS NOT NULL)
        THEN RAISE(ABORT, 'revoked invitation must not have accepted or faulty fields')
        -- faulty: must have fault_at and failure_code, no other terminal fields
        WHEN NEW.status = 'faulty' AND (NEW.fault_at IS NULL OR NEW.failure_code IS NULL)
        THEN RAISE(ABORT, 'faulty invitation must have fault_at and failure_code')
        WHEN NEW.status = 'faulty' AND (
            NEW.accepted_at IS NOT NULL OR NEW.accepted_by IS NOT NULL OR
            NEW.revoked_at IS NOT NULL OR NEW.revoked_by IS NOT NULL)
        THEN RAISE(ABORT, 'faulty invitation must not have accepted or revoked fields')
    END;
END;

-- Create trigger to ensure token_generation starts at 1 and remains positive
CREATE TRIGGER check_token_generation_positive
BEFORE INSERT ON invitations
WHEN NEW.token_generation < 1
BEGIN
    SELECT RAISE(ABORT, 'token_generation must be at least 1');
END;

CREATE TRIGGER check_token_generation_positive_update
BEFORE UPDATE ON invitations
WHEN NEW.token_generation < 1
BEGIN
    SELECT RAISE(ABORT, 'token_generation must be at least 1');
END;
