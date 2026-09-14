package user

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/thorstenkramm/mia/internal/audit"
	"github.com/thorstenkramm/mia/internal/filepublish"
	"github.com/thorstenkramm/mia/internal/identity"
	"github.com/thorstenkramm/mia/internal/provider/sms"
	miSQLite "github.com/thorstenkramm/mia/internal/sqlite"
)

var (
	ErrProfileNotStaff       = errors.New("profile self-edit requires staff")
	ErrProfileInvalid        = errors.New("invalid profile value")
	ErrMobileUnavailable     = errors.New("mobile verification unavailable")
	ErrMobileChallenge       = errors.New("mobile verification challenge invalid")
	ErrMobileCode            = errors.New("mobile verification code invalid")
	ErrAvatarNotFound        = errors.New("avatar not found")
	ErrPendingSMSInvalidator = errors.New("pending SMS invalidator is not configured")
)

// PendingSMSInvalidator is auth's narrow table-owner operation used inside user transactions.
type PendingSMSInvalidator func(context.Context, miSQLite.Querier, string) error

// Service owns profile, mobile, and avatar operations. It is safe for concurrent use.
type Service struct {
	database             *sql.DB
	dataDir              string
	sender               sms.Sender
	invalidatePendingSMS PendingSMSInvalidator
	now                  func() time.Time
	mobileMu             sync.Mutex
	avatarMu             sync.RWMutex
}

func NewService(database *sql.DB, dataDir string, sender sms.Sender, invalidator PendingSMSInvalidator) *Service {
	return NewServiceWithClock(database, dataDir, sender, invalidator, time.Now)
}

// NewServiceWithClock constructs a profile service with an injected lifecycle clock.
func NewServiceWithClock(database *sql.DB, dataDir string, sender sms.Sender, invalidator PendingSMSInvalidator,
	now func() time.Time,
) *Service {
	return &Service{database: database, dataDir: dataDir, sender: sender, invalidatePendingSMS: invalidator, now: now}
}

type Profile struct {
	ID, Username, PreferredLanguage, Country, TimeZone string
	Email, Name, Nickname, Mobile, TTSVoice            *string
	Staff                                              bool
}

// MinimalProfile is the assignment-scoped identity visible to a mentor.
type MinimalProfile struct {
	ID, Username   string
	Name, Nickname *string
}

// LoadTTSVoice returns the account's current explicitly selected ElevenLabs voice.
func LoadTTSVoice(ctx context.Context, query miSQLite.Querier, accountID string) (*string, error) {
	var voice sql.NullString
	if err := query.QueryRowContext(ctx, "SELECT tts_voice FROM users WHERE id = ?", accountID).Scan(&voice); err != nil {
		return nil, fmt.Errorf("load text-to-speech voice: %w", err)
	}
	return nullString(voice), nil
}

// LoadMinimalProfile returns only the identity fields permitted in a mentor projection.
func LoadMinimalProfile(ctx context.Context, query miSQLite.Querier, accountID string) (MinimalProfile, error) {
	var profile MinimalProfile
	var name, nickname sql.NullString
	err := query.QueryRowContext(ctx, `SELECT id, username, name, nickname FROM users WHERE id = ?`, accountID).
		Scan(&profile.ID, &profile.Username, &name, &nickname)
	if err != nil {
		return MinimalProfile{}, fmt.Errorf("load minimal profile: %w", err)
	}
	profile.Name, profile.Nickname = nullString(name), nullString(nickname)
	return profile, nil
}

type OptionalString struct {
	Set   bool
	Value *string
}

type ProfileChanges struct {
	Name, Nickname, TTSVoice             OptionalString
	PreferredLanguage, Country, TimeZone *string
}

func (changes ProfileChanges) Empty() bool {
	return !changes.Name.Set && !changes.Nickname.Set && !changes.TTSVoice.Set && changes.PreferredLanguage == nil &&
		changes.Country == nil && changes.TimeZone == nil
}

// GetSelf applies self-visibility and returns the current account profile.
func (service *Service) GetSelf(ctx context.Context, accountID string) (Profile, error) {
	var profile Profile
	var email, name, nickname, mobile, voice sql.NullString
	var staff int
	err := service.database.QueryRowContext(ctx, `SELECT u.id, u.username, u.email, u.preferred_language,
		u.country, u.time_zone, u.name, u.nickname, u.mobile, u.tts_voice,
		EXISTS(SELECT 1 FROM user_roles r WHERE r.user_id = u.id AND r.role IN ('administrator', 'supervisor', 'mentor'))
		FROM users u WHERE u.id = ?`, accountID).Scan(&profile.ID, &profile.Username, &email,
		&profile.PreferredLanguage, &profile.Country, &profile.TimeZone, &name, &nickname, &mobile, &voice, &staff)
	if err != nil {
		return Profile{}, fmt.Errorf("load own profile: %w", err)
	}
	profile.Email, profile.Name, profile.Nickname, profile.Mobile, profile.TTSVoice = nullString(email), nullString(name),
		nullString(nickname), nullString(mobile), nullString(voice)
	profile.Staff = staff != 0
	return profile, nil
}

// UpdateSelf validates all supplied fields before atomically updating a staff profile and its audit record.
func (service *Service) UpdateSelf(ctx context.Context, accountID string, changes ProfileChanges) (Profile, error) {
	if changes.Empty() {
		return Profile{}, ErrProfileInvalid
	}
	name, err := normalizeOptional(changes.Name, 100, 400)
	if err != nil {
		return Profile{}, ErrProfileInvalid
	}
	nickname, err := normalizeOptional(changes.Nickname, 24, 96)
	if err != nil {
		return Profile{}, ErrProfileInvalid
	}
	voice, err := normalizeVoice(changes.TTSVoice)
	if err != nil {
		return Profile{}, ErrProfileInvalid
	}
	var language, country, zone *string
	if changes.PreferredLanguage != nil {
		value, validateErr := identity.Language(*changes.PreferredLanguage)
		if validateErr != nil {
			return Profile{}, ErrProfileInvalid
		}
		language = &value
	}
	if changes.Country != nil {
		value, validateErr := identity.Country(*changes.Country)
		if validateErr != nil {
			return Profile{}, ErrProfileInvalid
		}
		country = &value
	}
	if changes.TimeZone != nil {
		value, validateErr := identity.TimeZone(*changes.TimeZone)
		if validateErr != nil {
			return Profile{}, ErrProfileInvalid
		}
		zone = &value
	}
	err = miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		var staff int
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM user_roles WHERE user_id = ?
			AND role IN ('administrator', 'supervisor', 'mentor'))`, accountID).Scan(&staff); err != nil {
			return err
		}
		if staff == 0 {
			return ErrProfileNotStaff
		}
		result, err := tx.ExecContext(ctx, `UPDATE users SET
			name = CASE WHEN ? THEN ? ELSE name END,
			nickname = CASE WHEN ? THEN ? ELSE nickname END,
			tts_voice = CASE WHEN ? THEN ? ELSE tts_voice END,
			preferred_language = COALESCE(?, preferred_language), country = COALESCE(?, country),
			time_zone = COALESCE(?, time_zone), updated_at = ?, updated_by = ? WHERE id = ?`,
			changes.Name.Set, nullablePointer(name), changes.Nickname.Set, nullablePointer(nickname),
			changes.TTSVoice.Set, nullablePointer(voice), nullablePointer(language), nullablePointer(country),
			nullablePointer(zone), instant(time.Now()), accountID, accountID)
		if err != nil {
			return fmt.Errorf("update own profile: %w", err)
		}
		rows, err := result.RowsAffected()
		if err != nil || rows != 1 {
			return fmt.Errorf("count own profile update: %w", err)
		}
		return audit.Write(ctx, tx, audit.ActionUserProfileUpdated, accountID, accountID)
	})
	if err != nil {
		return Profile{}, err
	}
	return service.GetSelf(ctx, accountID)
}

// SetStudentTTSVoice validates and stores the supervisor-managed voice field.
// The caller owns student-only and shared-course authorization.
func SetStudentTTSVoice(ctx context.Context, query miSQLite.Querier, studentID, actorID string, voice *string) error {
	normalized, err := normalizeVoice(OptionalString{Set: true, Value: voice})
	if err != nil {
		return ErrProfileInvalid
	}
	result, err := query.ExecContext(ctx, `UPDATE users SET tts_voice = ?, updated_at = ?, updated_by = ? WHERE id = ?`,
		nullablePointer(normalized), instant(time.Now()), actorID, studentID)
	if err != nil {
		return fmt.Errorf("set student TTS voice: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count student TTS voice update: %w", err)
	}
	if rows != 1 {
		return ErrProfileInvalid
	}
	return nil
}

type MobileState struct {
	AccountID, Lifecycle, ResendState string
	ChallengeID                       *string
	ExpiresAt, NextResendAt           *time.Time
}

// MobileState returns the current account's latest secret-free mobile-verification lifecycle.
func (service *Service) MobileState(ctx context.Context, accountID string) (MobileState, error) {
	service.mobileMu.Lock()
	defer service.mobileMu.Unlock()
	return service.mobileState(ctx, accountID, service.now())
}

func (service *Service) mobileState(ctx context.Context, accountID string, now time.Time) (MobileState, error) {
	var state MobileState
	err := miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		var loadErr error
		state, loadErr = service.loadMobileState(ctx, tx, accountID, now)
		return loadErr
	})
	return state, err
}

func (service *Service) loadMobileState(ctx context.Context, query miSQLite.Querier, accountID string,
	now time.Time,
) (MobileState, error) {
	if err := requireStaff(ctx, query, accountID); err != nil {
		return MobileState{}, err
	}
	state := MobileState{AccountID: accountID, Lifecycle: "absent", ResendState: "not_available"}
	var id, destination, expires string
	var consumed, invalidated sql.NullString
	err := query.QueryRowContext(ctx, `SELECT id, pending_mobile, expires_at, consumed_at, invalidated_at
		FROM mobile_verification_challenges WHERE user_id = ? ORDER BY created_at DESC, rowid DESC LIMIT 1`, accountID).
		Scan(&id, &destination, &expires, &consumed, &invalidated)
	if errors.Is(err, sql.ErrNoRows) {
		return state, nil
	}
	if err != nil {
		return MobileState{}, fmt.Errorf("load mobile verification state: %w", err)
	}
	expiresAt, err := time.Parse("2006-01-02T15:04:05.000000Z", expires)
	if err != nil {
		return MobileState{}, fmt.Errorf("parse mobile verification expiry: %w", err)
	}
	state.ChallengeID, state.ExpiresAt = &id, &expiresAt
	switch {
	case consumed.Valid:
		state.Lifecycle = "completed"
	case invalidated.Valid:
		state.Lifecycle = "invalidated"
	case !expiresAt.After(now):
		state.Lifecycle = "expired"
	case !sms.Available(service.sender):
		state.Lifecycle, state.ResendState = "unavailable", "unavailable"
	default:
		state.Lifecycle = "active"
		eligibility, checkErr := sms.Check(ctx, query, accountID, destination, now)
		if checkErr != nil {
			return MobileState{}, fmt.Errorf("check mobile resend eligibility: %w", checkErr)
		}
		if eligibility.Allowed {
			state.ResendState = "eligible"
		} else {
			state.ResendState = string(eligibility.Reason)
			state.NextResendAt = &eligibility.RetryAt
		}
	}
	return state, nil
}

// StartMobileChallenge replaces the account's pending challenge before one provider attempt.
func (service *Service) StartMobileChallenge(ctx context.Context, accountID, destination string) (string, error) {
	state, err := service.StartMobileChallengeState(ctx, accountID, destination)
	if err != nil {
		return "", err
	}
	if state.ChallengeID == nil {
		return "", errors.New("started mobile challenge has no identity")
	}
	return *state.ChallengeID, nil
}

// StartMobileChallengeState starts a challenge and returns its authoritative lifecycle.
func (service *Service) StartMobileChallengeState(ctx context.Context, accountID, destination string) (MobileState, error) {
	service.mobileMu.Lock()
	defer service.mobileMu.Unlock()
	if !sms.Available(service.sender) {
		return MobileState{}, ErrMobileUnavailable
	}
	destination, err := identity.E164(destination)
	if err != nil {
		return MobileState{}, ErrProfileInvalid
	}
	code, err := newMobileCode()
	if err != nil {
		return MobileState{}, err
	}
	id := "mvc_" + uuid.NewString()
	now := service.now()
	err = miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		if err := requireStaff(ctx, tx, accountID); err != nil {
			return err
		}
		if _, err := sms.Reserve(ctx, tx, accountID, destination, now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE mobile_verification_challenges SET invalidated_at = ?
			WHERE user_id = ? AND consumed_at IS NULL AND invalidated_at IS NULL`, instant(now), accountID); err != nil {
			return fmt.Errorf("invalidate earlier mobile challenge: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO mobile_verification_challenges
			(id, user_id, requested_by, pending_mobile, code, created_at, expires_at, last_sent_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, id, accountID, accountID, destination, code, instant(now),
			instant(now.Add(30*time.Minute)), instant(now)); err != nil {
			return fmt.Errorf("create mobile challenge: %w", err)
		}
		return audit.Write(ctx, tx, audit.ActionUserMobileChallengeCreated, accountID, accountID)
	})
	if err != nil {
		return MobileState{}, err
	}
	return service.sendMobileCodeAndState(ctx, accountID, destination, code)
}

// ResendMobileChallenge sends the same live code without extending its state.
func (service *Service) ResendMobileChallenge(ctx context.Context, accountID, challengeID string) error {
	_, err := service.ResendMobileChallengeState(ctx, accountID, challengeID)
	return err
}

// ResendMobileChallengeState resends and returns the unchanged authoritative lifecycle.
func (service *Service) ResendMobileChallengeState(ctx context.Context, accountID, challengeID string) (MobileState, error) {
	service.mobileMu.Lock()
	defer service.mobileMu.Unlock()
	if !sms.Available(service.sender) {
		return MobileState{}, ErrMobileUnavailable
	}
	var destination, code string
	now := service.now()
	err := miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		if err := requireStaff(ctx, tx, accountID); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT pending_mobile, code FROM mobile_verification_challenges
			WHERE id = ? AND user_id = ? AND consumed_at IS NULL AND invalidated_at IS NULL AND expires_at > ?`,
			challengeID, accountID, instant(now)).Scan(&destination, &code); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrMobileChallenge
			}
			return err
		}
		if _, err := sms.Reserve(ctx, tx, accountID, destination, now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE mobile_verification_challenges SET last_sent_at = ? WHERE id = ?",
			instant(now), challengeID); err != nil {
			return err
		}
		return audit.Write(ctx, tx, audit.ActionUserMobileChallengeResendAttempted, accountID, accountID)
	})
	if err != nil {
		return MobileState{}, err
	}
	return service.sendMobileCodeAndState(ctx, accountID, destination, code)
}

func (service *Service) sendMobileCodeAndState(ctx context.Context, accountID, destination, code string) (MobileState, error) {
	if err := service.sender.Send(ctx, destination, code); err != nil {
		if auditErr := service.auditMobileDeliveryFailure(ctx, accountID); auditErr != nil {
			return MobileState{}, errors.Join(ErrMobileUnavailable, auditErr)
		}
		return MobileState{}, ErrMobileUnavailable
	}
	return service.mobileState(ctx, accountID, service.now())
}

// VerifyMobileChallenge consumes one challenge and changes the verified profile mobile atomically.
func (service *Service) VerifyMobileChallenge(ctx context.Context, accountID, challengeID, submitted string) error {
	_, err := service.VerifyMobileChallengeState(ctx, accountID, challengeID, submitted)
	return err
}

// VerifyMobileChallengeState verifies and returns the resulting authoritative lifecycle.
func (service *Service) VerifyMobileChallengeState(ctx context.Context, accountID, challengeID,
	submitted string,
) (MobileState, error) {
	service.mobileMu.Lock()
	defer service.mobileMu.Unlock()
	if len(submitted) != 6 {
		return MobileState{}, service.mobileFailure(ctx, accountID, challengeID)
	}
	now := service.now()
	resultErr := error(nil)
	err := miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		if err := requireStaff(ctx, tx, accountID); err != nil {
			return err
		}
		var destination, code string
		err := tx.QueryRowContext(ctx, `SELECT pending_mobile, code FROM mobile_verification_challenges
			WHERE id = ? AND user_id = ? AND consumed_at IS NULL AND invalidated_at IS NULL AND expires_at > ?`,
			challengeID, accountID, instant(now)).Scan(&destination, &code)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrMobileChallenge
		}
		if err != nil {
			return err
		}
		if subtle.ConstantTimeCompare([]byte(code), []byte(submitted)) != 1 {
			if err := recordMobileFailure(ctx, tx, accountID, challengeID, now); err != nil {
				return err
			}
			resultErr = ErrMobileCode
			return nil
		}
		result, err := tx.ExecContext(ctx, `UPDATE mobile_verification_challenges SET consumed_at = ?
			WHERE id = ? AND user_id = ? AND consumed_at IS NULL AND invalidated_at IS NULL AND expires_at > ?`,
			instant(now), challengeID, accountID, instant(now))
		if err != nil {
			return err
		}
		rows, err := result.RowsAffected()
		if err != nil || rows != 1 {
			return ErrMobileChallenge
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET mobile = ?, mobile_verified_at = ?, updated_at = ?, updated_by = ?
			WHERE id = ?`, destination, instant(now), instant(now), accountID, accountID); err != nil {
			return fmt.Errorf("set verified mobile: %w", err)
		}
		if err := service.invalidateSMS(ctx, tx, accountID); err != nil {
			return err
		}
		return audit.Write(ctx, tx, audit.ActionUserMobileChanged, accountID, accountID)
	})
	if err != nil {
		return MobileState{}, err
	}
	if resultErr != nil {
		return MobileState{}, resultErr
	}
	return service.mobileState(ctx, accountID, now)
}

func (service *Service) mobileFailure(ctx context.Context, accountID, challengeID string) error {
	err := miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		if err := requireStaff(ctx, tx, accountID); err != nil {
			return err
		}
		return recordMobileFailure(ctx, tx, accountID, challengeID, service.now())
	})
	if err != nil {
		return err
	}
	return ErrMobileCode
}

func recordMobileFailure(ctx context.Context, tx *sql.Tx, accountID, challengeID string, now time.Time) error {
	result, err := tx.ExecContext(ctx, `UPDATE mobile_verification_challenges SET failed_attempts = failed_attempts + 1,
		invalidated_at = CASE WHEN failed_attempts + 1 >= 5 THEN ? ELSE invalidated_at END
		WHERE id = ? AND user_id = ? AND consumed_at IS NULL AND invalidated_at IS NULL AND expires_at > ?
		AND failed_attempts < 5`, instant(now), challengeID, accountID, instant(now))
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return ErrMobileChallenge
	}
	if err := audit.Write(ctx, tx, audit.ActionUserMobileChallengeFailed, accountID, accountID); err != nil {
		return err
	}
	return nil
}

// RemoveMobile clears a verified mobile and pending mobile/SMS state without touching an active factor.
func (service *Service) RemoveMobile(ctx context.Context, accountID string) error {
	service.mobileMu.Lock()
	defer service.mobileMu.Unlock()
	now := service.now()
	return miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		if err := requireStaff(ctx, tx, accountID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET mobile = NULL, mobile_verified_at = NULL,
			updated_at = ?, updated_by = ? WHERE id = ?`, instant(now), accountID, accountID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE mobile_verification_challenges SET invalidated_at = ?
			WHERE user_id = ? AND consumed_at IS NULL AND invalidated_at IS NULL`, instant(now), accountID); err != nil {
			return err
		}
		if err := service.invalidateSMS(ctx, tx, accountID); err != nil {
			return err
		}
		return audit.Write(ctx, tx, audit.ActionUserMobileRemoved, accountID, accountID)
	})
}

func (service *Service) avatarPath(accountID string) (string, error) {
	parsed, err := uuid.Parse(stringAfterPrefix(accountID, "u_"))
	if err != nil || parsed.Version() != 4 || accountID != "u_"+parsed.String() {
		return "", errors.New("invalid account ID for avatar path")
	}
	return filepath.Join(service.dataDir, "users", accountID, "avatar.png"), nil
}

func (service *Service) avatarExists(accountID string) bool {
	service.avatarMu.RLock()
	defer service.avatarMu.RUnlock()
	path, err := service.avatarPath(accountID)
	if err != nil {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// HasAvatar reports whether the account currently has a normalized avatar file.
func (service *Service) HasAvatar(accountID string) bool { return service.avatarExists(accountID) }

func (service *Service) PutAvatar(ctx context.Context, accountID string, pngData []byte) error {
	service.avatarMu.Lock()
	defer service.avatarMu.Unlock()
	if err := service.requireStaff(ctx, accountID); err != nil {
		return err
	}
	// jscpd:ignore-start
	// Avatar replacement and removal have distinct file publication and audit actions.
	path, err := service.avatarPath(accountID)
	if err != nil {
		return err
	}
	change, err := filepublish.Replace(path, pngData)
	// jscpd:ignore-end
	if err != nil {
		return err
	}
	err = miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		if err := requireStaff(ctx, tx, accountID); err != nil {
			return err
		}
		return audit.Write(ctx, tx, audit.ActionUserAvatarUpdated, accountID, accountID)
	})
	if err != nil {
		return errors.Join(err, change.Finish(false))
	}
	return change.Finish(true)
}

func (service *Service) Avatar(ctx context.Context, accountID string) ([]byte, error) {
	service.avatarMu.RLock()
	defer service.avatarMu.RUnlock()
	if _, err := service.GetSelf(ctx, accountID); err != nil {
		return nil, err
	}
	path, err := service.avatarPath(accountID)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrAvatarNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read avatar: %w", err)
	}
	return data, nil
}

func (service *Service) DeleteAvatar(ctx context.Context, accountID string) error {
	service.avatarMu.Lock()
	defer service.avatarMu.Unlock()
	if err := service.requireStaff(ctx, accountID); err != nil {
		return err
	}
	path, err := service.avatarPath(accountID)
	if err != nil {
		return err
	}
	change, err := filepublish.Remove(path)
	if err != nil {
		return err
	}
	err = miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		if err := requireStaff(ctx, tx, accountID); err != nil {
			return err
		}
		return audit.Write(ctx, tx, audit.ActionUserAvatarRemoved, accountID, accountID)
	})
	if err != nil {
		return errors.Join(err, change.Finish(false))
	}
	return change.Finish(true)
}

func (service *Service) requireStaff(ctx context.Context, accountID string) error {
	return requireStaff(ctx, service.database, accountID)
}

func requireStaff(ctx context.Context, query miSQLite.Querier, accountID string) error {
	var staff int
	if err := query.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM user_roles WHERE user_id = ?
		AND role IN ('administrator', 'supervisor', 'mentor'))`, accountID).Scan(&staff); err != nil {
		return err
	}
	if staff == 0 {
		return ErrProfileNotStaff
	}
	return nil
}

func (service *Service) invalidateSMS(ctx context.Context, query miSQLite.Querier, accountID string) error {
	if service.invalidatePendingSMS == nil {
		return ErrPendingSMSInvalidator
	}
	return service.invalidatePendingSMS(ctx, query, accountID)
}

func (service *Service) auditMobileDeliveryFailure(ctx context.Context, accountID string) error {
	return miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		return audit.Write(ctx, tx, audit.ActionUserMobileDeliveryFailed, accountID, accountID)
	})
}

func normalizeOptional(value OptionalString, runes, bytes int) (*string, error) {
	if !value.Set || value.Value == nil {
		return value.Value, nil
	}
	return identity.Text(*value.Value, runes, bytes)
}

func normalizeVoice(value OptionalString) (*string, error) {
	if !value.Set || value.Value == nil {
		return value.Value, nil
	}
	if len(*value.Value) == 0 {
		return nil, nil
	}
	if len(*value.Value) > 128 {
		return nil, ErrProfileInvalid
	}
	for _, character := range *value.Value {
		if character < 0x20 || character > 0x7e {
			return nil, ErrProfileInvalid
		}
	}
	return value.Value, nil
}

func nullablePointer(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullString(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}

func newMobileCode() (string, error) {
	value, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", fmt.Errorf("generate mobile verification code: %w", err)
	}
	return fmt.Sprintf("%06d", value.Int64()), nil
}

func stringAfterPrefix(value, prefix string) string {
	if len(value) <= len(prefix) || value[:len(prefix)] != prefix {
		return ""
	}
	return value[len(prefix):]
}
