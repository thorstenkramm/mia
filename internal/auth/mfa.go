package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" // RFC 6238 requires SHA-1 for MIA TOTP factors.
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/thorstenkramm/mia/internal/audit"
	"github.com/thorstenkramm/mia/internal/provider/sms"
	miSQLite "github.com/thorstenkramm/mia/internal/sqlite"
	"github.com/thorstenkramm/mia/internal/user"
)

const (
	totpSecretLength   = 20
	recoveryCodeCount  = 10
	recoveryCodeLength = 16
)

var crockfordEncoding = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

var errInvalidMFAValue = errors.New("invalid MFA value")

var (
	ErrMFAResetNotFound    = errors.New("MFA reset target not found")
	ErrMFAResetUnavailable = errors.New("MFA reset unavailable")
)

type FactorState struct {
	ID, Method string
}

type EnrollmentState struct {
	ID, Method, ReplacesFactorID string
	ExpiresAt                    time.Time
}

type MFAActionState struct {
	Enroll, Continue, Cancel, Disable, Replace, Verify, SMSResend bool
}

type State struct {
	Active  *FactorState
	Pending *EnrollmentState
	Actions MFAActionState
}

// StateForAccount returns live factor and enrollment metadata without returning
// secret-bearing values. It reads an SMS destination only to evaluate its
// durable resend gate.
func StateForAccount(ctx context.Context, query miSQLite.Querier, accountID string, now time.Time,
	smsAvailable bool,
) (State, error) {
	var state State
	var factorID, factorMethod, enrollmentID, enrollmentMethod sql.NullString
	var pendingDestination, expires sql.NullString
	err := query.QueryRowContext(ctx, `SELECT factor.id, factor.method, enrollment.id, enrollment.method,
		enrollment.sms_destination, enrollment.expires_at FROM (SELECT 1) singleton
		LEFT JOIN mfa_factors factor ON factor.user_id = ?
		LEFT JOIN mfa_enrollments enrollment ON enrollment.user_id = ? AND enrollment.expires_at > ?`,
		accountID, accountID, instant(now)).Scan(&factorID, &factorMethod, &enrollmentID, &enrollmentMethod,
		&pendingDestination, &expires)
	if err != nil {
		return State{}, fmt.Errorf("load MFA state: %w", err)
	}
	if factorID.Valid != factorMethod.Valid || enrollmentID.Valid != enrollmentMethod.Valid ||
		enrollmentID.Valid != expires.Valid {
		return State{}, errors.New("persisted MFA state is incomplete")
	}
	if factorID.Valid && factorMethod.Valid {
		state.Active = &FactorState{ID: factorID.String, Method: factorMethod.String}
	}
	if enrollmentID.Valid && enrollmentMethod.Valid && expires.Valid {
		pending := EnrollmentState{ID: enrollmentID.String, Method: enrollmentMethod.String}
		pending.ExpiresAt, err = time.Parse("2006-01-02T15:04:05.000000Z", expires.String)
		if err != nil {
			return State{}, fmt.Errorf("parse MFA enrollment expiry: %w", err)
		}
		if state.Active != nil {
			pending.ReplacesFactorID = state.Active.ID
		}
		state.Pending = &pending
	}
	state.Actions.Enroll = state.Active == nil && state.Pending == nil
	state.Actions.Continue = state.Pending != nil
	state.Actions.Cancel = state.Pending != nil
	state.Actions.Disable = state.Active != nil
	state.Actions.Replace = state.Active != nil && state.Pending == nil
	state.Actions.Verify = state.Pending != nil
	if smsAvailable && state.Pending != nil && state.Pending.Method == "sms" {
		state.Actions.SMSResend, err = sms.CanSend(ctx, query, accountID, pendingDestination.String, now)
		if err != nil {
			return State{}, fmt.Errorf("check MFA enrollment resend eligibility: %w", err)
		}
	}
	return state, nil
}

type StudentResetAuthorizer func(context.Context, miSQLite.Querier, string, string) (bool, error)

type ResetResult struct {
	ID, AccountClass, SessionEffect string
}

// ResetLostFactor authorizes and atomically removes all auth-owned MFA state,
// applies the class-specific password gate, and writes a content-free audit.
func ResetLostFactor(ctx context.Context, database *sql.DB, actorID, targetID string,
	authorizeStudent StudentResetAuthorizer,
) (ResetResult, error) {
	result := ResetResult{ID: "mfr_" + uuid.NewString()}
	err := miSQLite.WithTx(ctx, database, func(tx *sql.Tx) error {
		staff, err := user.HasAnyRole(ctx, tx, targetID, user.Administrator, user.Supervisor, user.Mentor)
		if err != nil {
			return err
		}
		allowed := false
		if staff {
			allowed, err = user.AuthorizeStaffMFAReset(ctx, tx, actorID, targetID)
			result.AccountClass, result.SessionEffect = "staff", "invalidated"
		} else if authorizeStudent != nil {
			allowed, err = authorizeStudent(ctx, tx, targetID, actorID)
			result.AccountClass, result.SessionEffect = "student_only", "invalidated"
		}
		if err != nil {
			return err
		}
		if !allowed {
			return ErrMFAResetNotFound
		}
		deleted, err := tx.ExecContext(ctx, "DELETE FROM mfa_factors WHERE user_id = ?", targetID)
		if err != nil {
			return fmt.Errorf("remove reset MFA factor: %w", err)
		}
		rows, err := deleted.RowsAffected()
		if err != nil {
			return fmt.Errorf("count reset MFA factor: %w", err)
		}
		if rows != 1 {
			return ErrMFAResetUnavailable
		}
		for _, statement := range []string{"DELETE FROM mfa_enrollments WHERE user_id = ?", "DELETE FROM mfa_challenges WHERE user_id = ?", "DELETE FROM mfa_management_proofs WHERE user_id = ?", "DELETE FROM mfa_recovery_codes WHERE user_id = ?"} {
			if _, err := tx.ExecContext(ctx, statement, targetID); err != nil {
				return fmt.Errorf("clear reset MFA artifacts: %w", err)
			}
		}
		err = user.RequirePasswordChangeAfterMFAReset(ctx, tx, targetID)
		if err != nil {
			return err
		}
		return audit.Write(ctx, tx, audit.ActionAuthMFAReset, actorID, targetID)
	})
	return result, err
}

// newTOTPSecret returns the exact secret size required by the MIA TOTP profile.
func newTOTPSecret() ([]byte, error) {
	secret := make([]byte, totpSecretLength)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("generate TOTP secret: %w", err)
	}
	return secret, nil
}

// verifyTOTP checks a six digit RFC 6238 SHA-1 value in the allowed one-step window.
func verifyTOTP(secret []byte, code string, now time.Time) (int64, bool) {
	if len(secret) != totpSecretLength || len(code) != 6 {
		return 0, false
	}
	if _, err := strconv.Atoi(code); err != nil {
		return 0, false
	}
	step := now.UTC().Unix() / 30
	for candidate := step - 1; candidate <= step+1; candidate++ {
		if subtle.ConstantTimeCompare([]byte(totpCode(secret, candidate)), []byte(code)) == 1 {
			return candidate, true
		}
	}
	return 0, false
}

func totpCode(secret []byte, step int64) string {
	var message [8]byte
	binary.BigEndian.PutUint64(message[:], uint64(step))
	mac := hmac.New(sha1.New, secret)
	_, _ = mac.Write(message[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := (uint32(sum[offset])&0x7f)<<24 | uint32(sum[offset+1])<<16 | uint32(sum[offset+2])<<8 | uint32(sum[offset+3])
	return fmt.Sprintf("%06d", value%1_000_000)
}

func newRecoveryCodes() ([]string, [][32]byte, error) {
	codes := make([]string, recoveryCodeCount)
	digests := make([][32]byte, recoveryCodeCount)
	for index := range codes {
		bytes := make([]byte, 10)
		if _, err := rand.Read(bytes); err != nil {
			return nil, nil, fmt.Errorf("generate recovery code: %w", err)
		}
		codes[index] = crockfordEncoding.EncodeToString(bytes)
		digests[index] = sha256.Sum256([]byte(codes[index]))
	}
	return codes, digests, nil
}

func recoveryDigest(code string) ([32]byte, error) {
	if len(code) != recoveryCodeLength || strings.ToUpper(code) != code {
		return [32]byte{}, errInvalidMFAValue
	}
	for _, character := range code {
		if !strings.ContainsRune("0123456789ABCDEFGHJKMNPQRSTVWXYZ", character) {
			return [32]byte{}, errInvalidMFAValue
		}
	}
	return sha256.Sum256([]byte(code)), nil
}

// ResetMFA removes factor state and forces the next login through password replacement.
func ResetMFA(ctx context.Context, query miSQLite.Querier, accountID string) error {
	for _, statement := range []string{"DELETE FROM mfa_enrollments WHERE user_id = ?", "DELETE FROM mfa_challenges WHERE user_id = ?", "DELETE FROM mfa_management_proofs WHERE user_id = ?", "DELETE FROM mfa_recovery_codes WHERE user_id = ?", "DELETE FROM mfa_factors WHERE user_id = ?"} {
		if _, err := query.ExecContext(ctx, statement, accountID); err != nil {
			return fmt.Errorf("clear MFA state: %w", err)
		}
	}
	return user.RequirePasswordChangeAfterMFAReset(ctx, query, accountID)
}

// HasMFA reports whether an account has active MFA state that an offline reset can remove.
func HasMFA(ctx context.Context, query miSQLite.Querier, accountID string) (bool, error) {
	var exists int
	if err := query.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM mfa_factors WHERE user_id = ?)", accountID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check MFA factor: %w", err)
	}
	return exists != 0, nil
}

// InvalidatePendingSMS removes only auth-owned pending SMS enrollment or
// replacement state. It deliberately leaves the active factor destination unchanged.
func InvalidatePendingSMS(ctx context.Context, query miSQLite.Querier, accountID string) error {
	if _, err := query.ExecContext(ctx,
		"DELETE FROM mfa_enrollments WHERE user_id = ? AND method = 'sms'", accountID); err != nil {
		return fmt.Errorf("invalidate pending SMS enrollment: %w", err)
	}
	return nil
}
