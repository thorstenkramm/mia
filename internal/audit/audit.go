// Package audit writes content-free audit records in the caller's transaction.
package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	miSQLite "github.com/thorstenkramm/mia/internal/sqlite"
)

// Action is a centrally validated content-free audit action identifier.
type Action string

const (
	ActionOperatorAdministratorBootstrapped  Action = "operator.administrator.bootstrapped"
	ActionAuthSessionLoggedIn                Action = "auth.session.logged_in"
	ActionAuthSessionLoggedOut               Action = "auth.session.logged_out"
	ActionAuthPasswordChanged                Action = "auth.password.changed"
	ActionAuthSessionFailed                  Action = "auth.session.failed"
	ActionAuthSessionThrottled               Action = "auth.session.throttled"
	ActionAuthPasswordRecoveryRequested      Action = "auth.password_recovery.requested"
	ActionAuthPasswordReset                  Action = "auth.password.reset"
	ActionAuthPasswordRecoveryDeliveryFailed Action = "auth.password_recovery.delivery_failed"
	ActionAuthPasswordRecoveryTimedOut       Action = "auth.password_recovery.timed_out"
	ActionAuthPasswordRecoveryThrottled      Action = "auth.password_recovery.throttled"
	ActionAuthPasswordResetThrottled         Action = "auth.password_reset.throttled"
	ActionAuthPasswordResetFailed            Action = "auth.password_reset.failed"
	ActionAuthPasswordRecoveryAmbiguous      Action = "auth.password_recovery.ambiguous"
	ActionAuthMFAEnrolled                    Action = "auth.mfa.enrolled"
	ActionAuthMFADisabled                    Action = "auth.mfa.disabled"
	ActionAuthMFAReplaced                    Action = "auth.mfa.replaced"
	ActionAuthMFARecoveryCodeUsed            Action = "auth.mfa.recovery_code.used"
	ActionAuthMFAChallengeCreated            Action = "auth.mfa.challenge.created"
	ActionAuthMFAChallengeVerified           Action = "auth.mfa.challenge.verified"
	ActionAuthMFAChallengeFailed             Action = "auth.mfa.challenge.failed"
	ActionAuthMFAChallengeReplayed           Action = "auth.mfa.challenge.replayed"
	ActionAuthMFAChallengeThrottled          Action = "auth.mfa.challenge.throttled"
	ActionAuthMFAEnrollmentFailed            Action = "auth.mfa.enrollment.failed"
	ActionAuthMFAEnrollmentThrottled         Action = "auth.mfa.enrollment.throttled"
	ActionOperatorAdministratorMFAReset      Action = "operator.administrator.mfa_reset"

	ActionInvitationInvitationCreated           Action = "invitation.invitation.created"
	ActionInvitationInvitationCreationDenied    Action = "invitation.invitation.creation_denied"
	ActionInvitationInvitationAccepted          Action = "invitation.invitation.accepted"
	ActionInvitationInvitationRevoked           Action = "invitation.invitation.revoked"
	ActionInvitationInvitationRevocationDenied  Action = "invitation.invitation.revocation_denied"
	ActionInvitationInvitationResent            Action = "invitation.invitation.resent"
	ActionInvitationInvitationResendDenied      Action = "invitation.invitation.resend_denied"
	ActionInvitationInvitationMarkedFaulty      Action = "invitation.invitation.marked_faulty"
	ActionInvitationInvitationFaultyDeleted     Action = "invitation.invitation.faulty_deleted"
	ActionInvitationInvitationDeliveryTimeout   Action = "invitation.invitation.delivery_timeout"
	ActionInvitationInvitationDeliveryAmbiguous Action = "invitation.invitation.delivery_ambiguous"
	ActionInvitationInvitationDelivered         Action = "invitation.invitation.delivered"

	ActionUserUserRoleGranted     Action = "user.user.role_granted"
	ActionUserUserRoleGrantDenied Action = "user.user.role_grant_denied"
)

var actions = map[Action]struct{}{
	ActionOperatorAdministratorBootstrapped:     {},
	ActionAuthSessionLoggedIn:                   {},
	ActionAuthSessionLoggedOut:                  {},
	ActionAuthPasswordChanged:                   {},
	ActionAuthSessionFailed:                     {},
	ActionAuthSessionThrottled:                  {},
	ActionAuthPasswordRecoveryRequested:         {},
	ActionAuthPasswordReset:                     {},
	ActionAuthPasswordRecoveryDeliveryFailed:    {},
	ActionAuthPasswordRecoveryTimedOut:          {},
	ActionAuthPasswordRecoveryThrottled:         {},
	ActionAuthPasswordResetThrottled:            {},
	ActionAuthPasswordResetFailed:               {},
	ActionAuthPasswordRecoveryAmbiguous:         {},
	ActionAuthMFAEnrolled:                       {},
	ActionAuthMFADisabled:                       {},
	ActionAuthMFAReplaced:                       {},
	ActionAuthMFARecoveryCodeUsed:               {},
	ActionAuthMFAChallengeCreated:               {},
	ActionAuthMFAChallengeVerified:              {},
	ActionAuthMFAChallengeFailed:                {},
	ActionAuthMFAChallengeReplayed:              {},
	ActionAuthMFAChallengeThrottled:             {},
	ActionAuthMFAEnrollmentFailed:               {},
	ActionAuthMFAEnrollmentThrottled:            {},
	ActionOperatorAdministratorMFAReset:         {},
	ActionInvitationInvitationCreated:           {},
	ActionInvitationInvitationCreationDenied:    {},
	ActionInvitationInvitationAccepted:          {},
	ActionInvitationInvitationRevoked:           {},
	ActionInvitationInvitationRevocationDenied:  {},
	ActionInvitationInvitationResent:            {},
	ActionInvitationInvitationResendDenied:      {},
	ActionInvitationInvitationMarkedFaulty:      {},
	ActionInvitationInvitationFaultyDeleted:     {},
	ActionInvitationInvitationDeliveryTimeout:   {},
	ActionInvitationInvitationDeliveryAmbiguous: {},
	ActionInvitationInvitationDelivered:         {},
	ActionUserUserRoleGranted:                   {},
	ActionUserUserRoleGrantDenied:               {},
}

// Metadata contains typed, content-free audit metadata.
// Only identifiers and outcome codes are allowed; no tokens, emails, or other sensitive data.
type Metadata struct {
	InvitationID string `json:"invitation_id,omitempty"`
	OutcomeCode  string `json:"outcome_code,omitempty"`
	Role         string `json:"role,omitempty"`
}

// Write records one registered action in the same transaction as its mutation.
func Write(ctx context.Context, query miSQLite.Querier, action Action, actorID, subjectID string) error {
	return WriteWithMetadata(ctx, query, action, actorID, subjectID, Metadata{})
}

// WriteWithMetadata records one registered action with typed metadata.
func WriteWithMetadata(ctx context.Context, query miSQLite.Querier, action Action, actorID, subjectID string, meta Metadata) error {
	if _, ok := actions[action]; !ok {
		return fmt.Errorf("unregistered audit action %q", action)
	}
	id, err := id()
	if err != nil {
		return err
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("marshal audit metadata: %w", err)
	}
	_, err = query.ExecContext(ctx, `INSERT INTO audit_events
		(id, action, actor_user_id, subject_user_id, created_at, metadata) VALUES (?, ?, ?, ?, ?, ?)`,
		id, string(action), nullable(actorID), nullable(subjectID), instant(time.Now()), string(metaJSON))
	if err != nil {
		return fmt.Errorf("write audit event: %w", err)
	}
	return nil
}

func id() (string, error) {
	return "aud_" + uuid.NewString(), nil
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func instant(value time.Time) string { return value.UTC().Format("2006-01-02T15:04:05.000000Z") }
