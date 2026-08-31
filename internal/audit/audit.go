// Package audit writes content-free audit records in the caller's transaction.
package audit

import (
	"context"
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
)

var actions = map[Action]struct{}{ActionOperatorAdministratorBootstrapped: {}, ActionAuthSessionLoggedIn: {}, ActionAuthSessionLoggedOut: {}, ActionAuthPasswordChanged: {}, ActionAuthSessionFailed: {}, ActionAuthSessionThrottled: {}, ActionAuthPasswordRecoveryRequested: {}, ActionAuthPasswordReset: {}, ActionAuthPasswordRecoveryDeliveryFailed: {}, ActionAuthPasswordRecoveryTimedOut: {}, ActionAuthPasswordRecoveryThrottled: {}, ActionAuthPasswordResetThrottled: {}, ActionAuthPasswordResetFailed: {}, ActionAuthPasswordRecoveryAmbiguous: {}, ActionAuthMFAEnrolled: {}, ActionAuthMFADisabled: {}, ActionAuthMFAReplaced: {}, ActionAuthMFARecoveryCodeUsed: {}, ActionAuthMFAChallengeCreated: {}, ActionAuthMFAChallengeVerified: {}, ActionAuthMFAChallengeFailed: {}, ActionAuthMFAChallengeReplayed: {}, ActionAuthMFAChallengeThrottled: {}, ActionAuthMFAEnrollmentFailed: {}, ActionAuthMFAEnrollmentThrottled: {}, ActionOperatorAdministratorMFAReset: {}}

// Write records one registered action in the same transaction as its mutation.
func Write(ctx context.Context, query miSQLite.Querier, action Action, actorID, subjectID string) error {
	if _, ok := actions[action]; !ok {
		return fmt.Errorf("unregistered audit action %q", action)
	}
	id, err := id()
	if err != nil {
		return err
	}
	_, err = query.ExecContext(ctx, `INSERT INTO audit_events
		(id, action, actor_user_id, subject_user_id, created_at, metadata) VALUES (?, ?, ?, ?, ?, '{}')`,
		id, string(action), nullable(actorID), nullable(subjectID), instant(time.Now()))
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
