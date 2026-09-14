ALTER TABLE invitations ADD COLUMN version INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1);
ALTER TABLE invitations ADD COLUMN delivery_state TEXT NOT NULL DEFAULT 'queued'
    CHECK (delivery_state IN ('queued', 'delivered', 'ambiguous', 'failed'));
ALTER TABLE invitations ADD COLUMN delivery_code TEXT;
ALTER TABLE invitations ADD COLUMN delivery_attempted_at TEXT;

UPDATE invitations
SET delivery_state = CASE
        WHEN status = 'faulty' THEN 'failed'
        WHEN sent_at IS NOT NULL THEN 'delivered'
        ELSE 'queued'
    END,
    delivery_code = NULL,
    delivery_attempted_at = CASE
        WHEN status = 'faulty' THEN fault_at
        WHEN sent_at IS NOT NULL THEN sent_at
        ELSE NULL
    END;

CREATE TRIGGER prevent_terminal_invitation_delivery_change
BEFORE UPDATE OF version, delivery_state, delivery_code, delivery_attempted_at ON invitations
WHEN OLD.status IN ('accepted', 'revoked', 'faulty')
    AND (
        NEW.version != OLD.version
        OR NEW.delivery_state != OLD.delivery_state
        OR NEW.delivery_code IS NOT OLD.delivery_code
        OR NEW.delivery_attempted_at IS NOT OLD.delivery_attempted_at
    )
BEGIN
    SELECT RAISE(ABORT, 'cannot modify terminal invitation delivery state');
END;
