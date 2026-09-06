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
	ActionAuthMFADeliveryFailed              Action = "auth.mfa.delivery_failed"
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

	ActionUserUserRoleGranted                Action = "user.user.role_granted"
	ActionUserUserRoleGrantDenied            Action = "user.user.role_grant_denied"
	ActionUserProfileUpdated                 Action = "user.profile.updated"
	ActionUserMobileChallengeCreated         Action = "user.mobile_challenge.created"
	ActionUserMobileChallengeResendAttempted Action = "user.mobile_challenge.resend_attempted"
	ActionUserMobileChallengeFailed          Action = "user.mobile_challenge.failed"
	ActionUserMobileDeliveryFailed           Action = "user.mobile_delivery.failed"
	ActionUserMobileChanged                  Action = "user.mobile.changed"
	ActionUserMobileRemoved                  Action = "user.mobile.removed"
	ActionUserAvatarUpdated                  Action = "user.avatar.updated"
	ActionUserAvatarRemoved                  Action = "user.avatar.removed"

	ActionCourseCourseCreated        Action = "course.course.created"
	ActionCourseCourseUpdated        Action = "course.course.updated"
	ActionCourseCourseActivated      Action = "course.course.activated"
	ActionCourseCourseDeactivated    Action = "course.course.deactivated"
	ActionCourseCourseDeleted        Action = "course.course.deleted"
	ActionCourseSupervisorAssigned   Action = "course.supervisor.assigned"
	ActionCourseSupervisorRemoved    Action = "course.supervisor.removed"
	ActionCourseLogoUpdated          Action = "course.logo.updated"
	ActionCourseLogoRemoved          Action = "course.logo.removed"
	ActionCourseMutationDenied       Action = "course.mutation.denied"
	ActionCourseStudentProvisioned   Action = "course.student.provisioned"
	ActionCourseStudentAdded         Action = "course.student.added"
	ActionCourseStudentRemoved       Action = "course.student.removed"
	ActionUserTemporaryPasswordSet   Action = "user.password.temporary_set"
	ActionUserStudentBanned          Action = "user.student.banned"
	ActionUserStudentUnbanned        Action = "user.student.unbanned"
	ActionUserStudentTTSVoiceUpdated Action = "user.student_tts_voice.updated"

	ActionMaterialCreated                Action = "material.material.created"
	ActionMaterialFileUploaded           Action = "material.file.uploaded"
	ActionMaterialFileDeleted            Action = "material.file.deleted"
	ActionMaterialFinalized              Action = "material.material.finalized"
	ActionMaterialBriefCorrected         Action = "material.brief.corrected"
	ActionMaterialApproved               Action = "material.approval.granted"
	ActionMaterialApprovalRevoked        Action = "material.approval.revoked"
	ActionMaterialDeleted                Action = "material.material.deleted"
	ActionMaterialProcessingFailed       Action = "material.processing.failed"
	ActionMaterialMutationDenied         Action = "material.mutation.denied"
	ActionTutoringSummaryCorrected       Action = "tutoring.summary.corrected"
	ActionTutoringSummaryFailed          Action = "tutoring.summary.failed"
	ActionMentoringCourseMentorAssigned  Action = "mentoring.course_mentor.assigned"
	ActionMentoringCourseMentorRemoved   Action = "mentoring.course_mentor.removed"
	ActionMentoringStudentMentorAssigned Action = "mentoring.student_mentor.assigned"
	ActionMentoringStudentMentorRemoved  Action = "mentoring.student_mentor.removed"
	ActionMentoringSessionRequested      Action = "mentoring.session.requested"
	ActionMentoringSessionAssigned       Action = "mentoring.session.assigned"
	ActionMentoringSessionTriaged        Action = "mentoring.session.triaged"
	ActionMentoringSessionResponded      Action = "mentoring.session.responded"
	ActionMentoringSessionScheduled      Action = "mentoring.session.scheduled"
	ActionMentoringSessionRescheduled    Action = "mentoring.session.rescheduled"
	ActionMentoringSessionUpdated        Action = "mentoring.session.updated"
	ActionMentoringSessionCancelled      Action = "mentoring.session.cancelled"
	ActionMentoringSessionCompleted      Action = "mentoring.session.completed"
	ActionMentoringMutationDenied        Action = "mentoring.mutation.denied"
	ActionUserMentoringPermissionUpdated Action = "user.mentoring_permission.updated"
	ActionSpeechGenerationFailed         Action = "speech.generation.failed"
	ActionSpeechMutationDenied           Action = "speech.mutation.denied"
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
	ActionAuthMFADeliveryFailed:                 {},
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
	ActionUserProfileUpdated:                    {},
	ActionUserMobileChallengeCreated:            {},
	ActionUserMobileChallengeResendAttempted:    {},
	ActionUserMobileChallengeFailed:             {},
	ActionUserMobileDeliveryFailed:              {},
	ActionUserMobileChanged:                     {},
	ActionUserMobileRemoved:                     {},
	ActionUserAvatarUpdated:                     {},
	ActionUserAvatarRemoved:                     {},
	ActionCourseCourseCreated:                   {},
	ActionCourseCourseUpdated:                   {},
	ActionCourseCourseActivated:                 {},
	ActionCourseCourseDeactivated:               {},
	ActionCourseCourseDeleted:                   {},
	ActionCourseSupervisorAssigned:              {},
	ActionCourseSupervisorRemoved:               {},
	ActionCourseLogoUpdated:                     {},
	ActionCourseLogoRemoved:                     {},
	ActionCourseMutationDenied:                  {},
	ActionCourseStudentProvisioned:              {},
	ActionCourseStudentAdded:                    {},
	ActionCourseStudentRemoved:                  {},
	ActionUserTemporaryPasswordSet:              {},
	ActionUserStudentBanned:                     {},
	ActionUserStudentUnbanned:                   {},
	ActionMaterialCreated:                       {},
	ActionMaterialFileUploaded:                  {},
	ActionMaterialFileDeleted:                   {},
	ActionMaterialFinalized:                     {},
	ActionMaterialBriefCorrected:                {},
	ActionMaterialApproved:                      {},
	ActionMaterialApprovalRevoked:               {},
	ActionMaterialDeleted:                       {},
	ActionMaterialProcessingFailed:              {},
	ActionMaterialMutationDenied:                {},
	ActionTutoringSummaryCorrected:              {},
	ActionTutoringSummaryFailed:                 {},
	ActionMentoringCourseMentorAssigned:         {},
	ActionMentoringCourseMentorRemoved:          {},
	ActionMentoringStudentMentorAssigned:        {},
	ActionMentoringStudentMentorRemoved:         {},
	ActionMentoringSessionRequested:             {},
	ActionMentoringSessionAssigned:              {},
	ActionMentoringSessionTriaged:               {},
	ActionMentoringSessionResponded:             {},
	ActionMentoringSessionScheduled:             {},
	ActionMentoringSessionRescheduled:           {},
	ActionMentoringSessionUpdated:               {},
	ActionMentoringSessionCancelled:             {},
	ActionMentoringSessionCompleted:             {},
	ActionMentoringMutationDenied:               {},
	ActionUserMentoringPermissionUpdated:        {},
	ActionUserStudentTTSVoiceUpdated:            {},
	ActionSpeechGenerationFailed:                {},
	ActionSpeechMutationDenied:                  {},
}

// Metadata contains typed, content-free audit metadata.
// Only identifiers, scheduling instants, and outcome codes are allowed; no tokens, emails, or other sensitive data.
type Metadata struct {
	InvitationID        string `json:"invitation_id,omitempty"`
	MaterialID          string `json:"material_id,omitempty"`
	CourseID            string `json:"-"`
	OutcomeCode         string `json:"outcome_code,omitempty"`
	Role                string `json:"role,omitempty"`
	MentoringSessionID  string `json:"mentoring_session_id,omitempty"`
	MentorID            string `json:"mentor_id,omitempty"`
	PreviousScheduledAt string `json:"previous_scheduled_at,omitempty"`
	NewScheduledAt      string `json:"new_scheduled_at,omitempty"`
	SpeechID            string `json:"speech_id,omitempty"`
	TutorResponseID     string `json:"tutor_response_id,omitempty"`
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
		(id, action, actor_user_id, subject_user_id, created_at, metadata, course_id) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, string(action), nullable(actorID), nullable(subjectID), instant(time.Now()), string(metaJSON), nullable(meta.CourseID))
	if err != nil {
		return fmt.Errorf("write audit event: %w", err)
	}
	return nil
}

// ReplaceCourseHistoryWithDeletion removes all course-scoped audit history and
// writes the sole retained de-identified deletion event in the caller's transaction.
func ReplaceCourseHistoryWithDeletion(ctx context.Context, query miSQLite.Querier, courseID, actorID string) error {
	if _, err := query.ExecContext(ctx, "DELETE FROM audit_events WHERE course_id = ?", courseID); err != nil {
		return fmt.Errorf("delete course audit history: %w", err)
	}
	eventID, err := id()
	if err != nil {
		return err
	}
	fingerprint, err := NewDeletionFingerprint()
	if err != nil {
		return err
	}
	_, err = query.ExecContext(ctx, `INSERT INTO audit_events
		(id, action, actor_user_id, subject_user_id, created_at, metadata, course_id, subject_type, subject_fingerprint)
		VALUES (?, ?, ?, NULL, ?, NULL, NULL, 'course', ?)`, eventID, string(ActionCourseCourseDeleted),
		nullable(actorID), instant(time.Now()), fingerprint)
	if err != nil {
		return fmt.Errorf("write de-identified course deletion audit event: %w", err)
	}
	return nil
}

// ReplaceStudentCourseHistoryWithRemoval deletes course-scoped audit history
// identifying a removed student and writes the one retained content-free event.
func ReplaceStudentCourseHistoryWithRemoval(
	ctx context.Context,
	query miSQLite.Querier,
	courseID string,
	studentID string,
	actorID string,
) error {
	if _, err := query.ExecContext(ctx, `DELETE FROM audit_events WHERE course_id = ?
		AND (subject_user_id = ? OR actor_user_id = ?)`, courseID, studentID, studentID); err != nil {
		return fmt.Errorf("delete removed student course audit history: %w", err)
	}
	return WriteWithMetadata(ctx, query, ActionCourseStudentRemoved, actorID, studentID,
		Metadata{CourseID: courseID})
}

// NewDeletionFingerprint returns an opaque UUID v4 with no retained mapping to
// the identity being deleted. One deletion operation reuses its returned value
// for every retained reference to that identity.
func NewDeletionFingerprint() (string, error) {
	value, err := uuid.NewRandom()
	if err != nil {
		return "", fmt.Errorf("generate deletion fingerprint: %w", err)
	}
	return value.String(), nil
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
