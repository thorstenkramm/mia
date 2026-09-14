// Package user owns account, role, and account-security persistence.
package user

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/thorstenkramm/mia/internal/identity"
	miSQLite "github.com/thorstenkramm/mia/internal/sqlite"
)

var (
	ErrAdministratorExists     = errors.New("administrator already exists")
	ErrUsernameTaken           = errors.New("username already taken")
	ErrRoleInvalid             = errors.New("invalid role")
	ErrRoleActorRequired       = errors.New("role grant actor is required")
	ErrRoleActorUnauthorized   = errors.New("role grant actor is not authorized")
	ErrRoleRecipientIneligible = errors.New("role recipient does not meet staff role requirements")
	ErrStudentIneligible       = errors.New("student account is ineligible")
)

type Role string

const (
	Administrator Role = "administrator"
	Supervisor    Role = "supervisor"
	Mentor        Role = "mentor"
	Student       Role = "student"
)

type CreateInput struct {
	Username, Email, PasswordHash, Language, Country, TimeZone string
	EmailVerified                                              bool
	MustChangePassword                                         bool
	Roles                                                      []Role
}

type Account struct {
	ID, Username       string
	Email              string
	PasswordHash       string
	SecurityGeneration int64
	MustChangePassword bool
	Banned             bool
}

// StudentOnlyState returns whether an account is student-only and its ban state.
func StudentOnlyState(ctx context.Context, query miSQLite.Querier, userID string) (bool, bool, error) {
	var student, staff, banned int
	err := query.QueryRowContext(ctx, `SELECT
		EXISTS(SELECT 1 FROM user_roles WHERE user_id = users.id AND role = 'student'),
		EXISTS(SELECT 1 FROM user_roles WHERE user_id = users.id AND role IN ('administrator','supervisor','mentor')),
		is_banned FROM users WHERE id = ?`, userID).Scan(&student, &staff, &banned)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, fmt.Errorf("load student-only state: %w", err)
	}
	return student != 0 && staff == 0, banned != 0, nil
}

// Roles returns the account's permanent roles in stable product order.
func Roles(ctx context.Context, query miSQLite.Querier, userID string) ([]Role, error) {
	rows, err := query.QueryContext(ctx, `SELECT role FROM user_roles WHERE user_id = ?
		ORDER BY CASE role WHEN 'administrator' THEN 1 WHEN 'supervisor' THEN 2 WHEN 'mentor' THEN 3 ELSE 4 END`, userID)
	if err != nil {
		return nil, fmt.Errorf("list user roles: %w", err)
	}
	roles := make([]Role, 0, 4)
	for rows.Next() {
		var role Role
		if err := rows.Scan(&role); err != nil {
			return nil, errors.Join(fmt.Errorf("scan user role: %w", err), rows.Close())
		}
		roles = append(roles, role)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.Join(fmt.Errorf("iterate user roles: %w", err), rows.Close())
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close user roles: %w", err)
	}
	return roles, nil
}

// MFAProfile contains the account data auth needs for MFA flows. user owns
// these account-record reads even when auth owns the MFA tables.
type MFAProfile struct {
	Username, PasswordHash string
	MustChangePassword     bool
	VerifiedMobile         string
}

// TutorProfile contains the user-owned student fields supplied to tutoring context.
type TutorProfile struct {
	Nickname     string `json:"nickname"`
	Language     string `json:"language"`
	Country      string `json:"country"`
	Instructions string `json:"instructions"`
	YearOfBirth  *int   `json:"year_of_birth"`
}

// MentoringRequestsAllowed returns the user-owned gate for new mentoring work.
func MentoringRequestsAllowed(ctx context.Context, query miSQLite.Querier, id string) (bool, error) {
	var allowed int
	if err := query.QueryRowContext(ctx, "SELECT mentoring_requests_allowed FROM users WHERE id = ?", id).
		Scan(&allowed); err != nil {
		return false, fmt.Errorf("load mentoring request permission: %w", err)
	}
	return allowed != 0, nil
}

// SetMentoringRequestsAllowed updates the user-owned gate in the caller's transaction.
func SetMentoringRequestsAllowed(ctx context.Context, query miSQLite.Querier, id string, allowed bool) error {
	result, err := query.ExecContext(ctx, "UPDATE users SET mentoring_requests_allowed = ? WHERE id = ?", allowed, id)
	if err != nil {
		return fmt.Errorf("update mentoring request permission: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count mentoring request permission update: %w", err)
	}
	if count != 1 {
		return sql.ErrNoRows
	}
	return nil
}

// LoadTutorProfile returns the bounded profile fields used by the tutor.
func LoadTutorProfile(ctx context.Context, query miSQLite.Querier, id string) (TutorProfile, error) {
	var profile TutorProfile
	var nickname, instructions sql.NullString
	var year sql.NullInt64
	err := query.QueryRowContext(ctx, `SELECT nickname, year_of_birth, preferred_language, country,
		llm_instructions FROM users WHERE id = ?`, id).
		Scan(&nickname, &year, &profile.Language, &profile.Country, &instructions)
	if err != nil {
		return TutorProfile{}, fmt.Errorf("load tutor profile: %w", err)
	}
	if nickname.Valid {
		profile.Nickname = nickname.String
	}
	if instructions.Valid {
		profile.Instructions = instructions.String
	}
	if year.Valid {
		value := int(year.Int64)
		profile.YearOfBirth = &value
	}
	return profile, nil
}

// LoadMFAProfile returns the account security and verified-mobile state used by MFA.
func LoadMFAProfile(ctx context.Context, query miSQLite.Querier, id string) (MFAProfile, error) {
	var profile MFAProfile
	var gate int
	var mobile any
	err := query.QueryRowContext(ctx, `SELECT username, password_hash, must_change_password,
		CASE WHEN mobile_verified_at IS NOT NULL THEN mobile END FROM users WHERE id = ?`, id).
		Scan(&profile.Username, &profile.PasswordHash, &gate, &mobile)
	if err != nil {
		return MFAProfile{}, fmt.Errorf("load MFA profile: %w", err)
	}
	profile.MustChangePassword = gate != 0
	if mobile != nil {
		value, ok := mobile.(string)
		if !ok {
			return MFAProfile{}, errors.New("verified mobile has an invalid stored type")
		}
		profile.VerifiedMobile = value
	}
	return profile, nil
}

// SoleAdministrator returns the only administrator when precisely one exists.
func SoleAdministrator(ctx context.Context, query miSQLite.Querier) (Account, error) {
	rows, err := query.QueryContext(ctx, `SELECT u.id, u.username FROM users u JOIN user_roles r ON r.user_id = u.id
		WHERE r.role = 'administrator' ORDER BY u.id`)
	if err != nil {
		return Account{}, fmt.Errorf("list administrators: %w", err)
	}
	var accounts []Account
	for rows.Next() {
		var account Account
		if err := rows.Scan(&account.ID, &account.Username); err != nil {
			return Account{}, fmt.Errorf("scan administrator: %w", err)
		}
		accounts = append(accounts, account)
	}
	if err := rows.Err(); err != nil {
		return Account{}, fmt.Errorf("iterate administrators: %w", err)
	}
	if err := rows.Close(); err != nil {
		return Account{}, fmt.Errorf("close administrator rows: %w", err)
	}
	if len(accounts) != 1 {
		return Account{}, errors.New("exactly one administrator is required")
	}
	return accounts[0], nil
}

// RequirePasswordChangeAfterMFAReset invalidates browser cookies and enables the password gate.
func RequirePasswordChangeAfterMFAReset(ctx context.Context, query miSQLite.Querier, id string) error {
	result, err := query.ExecContext(ctx, "UPDATE users SET must_change_password = 1, security_generation = security_generation + 1 WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("require password change after MFA reset: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count MFA reset user update: %w", err)
	}
	if rows != 1 {
		return errors.New("account missing while resetting MFA")
	}
	return nil
}

// AuthorizeStaffMFAReset applies the different-administrator rule while making
// missing, non-staff, and self-targeted accounts indistinguishable.
func AuthorizeStaffMFAReset(ctx context.Context, query miSQLite.Querier, actorID, targetID string) (bool, error) {
	var allowed int
	err := query.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users target
		WHERE target.id = ? AND target.id <> ?
		AND EXISTS(SELECT 1 FROM user_roles target_role WHERE target_role.user_id = target.id
			AND target_role.role IN ('administrator','supervisor','mentor'))
		AND EXISTS(SELECT 1 FROM user_roles actor_role WHERE actor_role.user_id = ?
			AND actor_role.role = 'administrator'))`, targetID, actorID, actorID).Scan(&allowed)
	if err != nil {
		return false, fmt.Errorf("authorize staff MFA reset: %w", err)
	}
	return allowed != 0, nil
}

// FindStaffForRecovery returns an eligible staff account for a complete username.
func FindStaffForRecovery(ctx context.Context, query miSQLite.Querier, username string) (Account, error) {
	key, err := identity.Username(username)
	if err != nil {
		return Account{}, err
	}
	var account Account
	var banned, staff int
	err = query.QueryRowContext(ctx, `SELECT u.id, u.email, u.is_banned,
		EXISTS(SELECT 1 FROM user_roles r WHERE r.user_id = u.id AND r.role IN ('administrator', 'supervisor', 'mentor'))
		FROM users u WHERE u.username_key = ? AND u.email_verified_at IS NOT NULL`, key).
		Scan(&account.ID, &account.Email, &banned, &staff)
	if err != nil {
		return Account{}, fmt.Errorf("find recovery account: %w", err)
	}
	account.Banned = banned != 0
	if staff == 0 || account.Banned || account.Email == "" {
		return Account{}, sql.ErrNoRows
	}
	return account, nil
}

// IsEligibleForRecovery checks staff recovery eligibility without exposing state to callers.
func IsEligibleForRecovery(ctx context.Context, query miSQLite.Querier, id string) (bool, error) {
	var eligible int
	err := query.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users u WHERE u.id = ? AND u.is_banned = 0
		AND u.email_verified_at IS NOT NULL AND EXISTS(SELECT 1 FROM user_roles r WHERE r.user_id = u.id
		AND r.role IN ('administrator', 'supervisor', 'mentor')))`, id).Scan(&eligible)
	if err != nil {
		return false, fmt.Errorf("check recovery eligibility: %w", err)
	}
	return eligible != 0, nil
}

// Create inserts one account and all initial roles through one owning API.
func Create(ctx context.Context, query miSQLite.Querier, input CreateInput) (Account, error) {
	roles, staff, err := initialRoles(input.Roles)
	if err != nil {
		return Account{}, err
	}
	usernameKey, err := identity.Username(input.Username)
	if err != nil {
		return Account{}, err
	}
	var email, emailKey string
	if input.Email != "" {
		var err error
		email, emailKey, err = identity.Email(input.Email)
		if err != nil {
			return Account{}, err
		}
	} else if input.EmailVerified || staff {
		return Account{}, identity.ErrInvalidEmail
	}
	if staff && !input.EmailVerified {
		return Account{}, errors.New("staff account requires verified email")
	}
	language, err := identity.Language(input.Language)
	if err != nil {
		return Account{}, err
	}
	country, err := identity.Country(input.Country)
	if err != nil {
		return Account{}, err
	}
	timeZone, err := identity.TimeZone(input.TimeZone)
	if err != nil {
		return Account{}, err
	}
	id, err := userID()
	if err != nil {
		return Account{}, err
	}
	now := instant(time.Now())
	var verified any
	if input.EmailVerified {
		verified = now
	}
	_, err = query.ExecContext(ctx, `INSERT INTO users
		(id, username, username_key, email, email_key, email_verified_at, password_hash, preferred_language, country,
		 time_zone, must_change_password, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, input.Username, usernameKey, nullable(email), nullable(emailKey),
		verified, input.PasswordHash, language, country, timeZone, input.MustChangePassword, now)
	if err != nil {
		if isUsernameUniqueViolation(err) {
			return Account{}, ErrUsernameTaken
		}
		return Account{}, fmt.Errorf("create user: %w", err)
	}
	for _, role := range roles {
		if _, err := query.ExecContext(ctx, "INSERT INTO user_roles (user_id, role, granted_at) VALUES (?, ?, ?)", id, role, now); err != nil {
			return Account{}, fmt.Errorf("grant initial role: %w", err)
		}
	}
	return Account{ID: id, PasswordHash: input.PasswordHash, SecurityGeneration: 1,
		MustChangePassword: input.MustChangePassword}, nil
}

// SetTemporaryPassword changes a student-only account's password, enables the
// replacement gate, and invalidates every browser cookie through its generation.
func SetTemporaryPassword(ctx context.Context, query miSQLite.Querier, accountID, passwordHash string) error {
	result, err := query.ExecContext(ctx, `UPDATE users SET password_hash = ?, must_change_password = 1,
		security_generation = security_generation + 1 WHERE id = ?
		AND EXISTS(SELECT 1 FROM user_roles WHERE user_id = users.id AND role = 'student')
		AND NOT EXISTS(SELECT 1 FROM user_roles WHERE user_id = users.id
			AND role IN ('administrator', 'supervisor', 'mentor'))`, passwordHash, accountID)
	if err != nil {
		return fmt.Errorf("set temporary student password: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count temporary student password update: %w", err)
	}
	if rows != 1 {
		return ErrStudentIneligible
	}
	return nil
}

// SetBanned changes ban state only for a student-only account. The returned
// boolean reports whether the durable state changed.
func SetBanned(ctx context.Context, query miSQLite.Querier, accountID string, banned bool) (bool, error) {
	desired := 0
	if banned {
		desired = 1
	}
	var eligible, current int
	err := query.QueryRowContext(ctx, `SELECT
		EXISTS(SELECT 1 FROM user_roles WHERE user_id = users.id AND role = 'student')
		AND NOT EXISTS(SELECT 1 FROM user_roles WHERE user_id = users.id
			AND role IN ('administrator', 'supervisor', 'mentor')), is_banned
		FROM users WHERE id = ?`, accountID).Scan(&eligible, &current)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrStudentIneligible
	}
	if err != nil {
		return false, fmt.Errorf("load student ban state: %w", err)
	}
	if eligible == 0 {
		return false, ErrStudentIneligible
	}
	if current == desired {
		return false, nil
	}
	result, err := query.ExecContext(ctx, "UPDATE users SET is_banned = ? WHERE id = ? AND is_banned = ?",
		desired, accountID, current)
	if err != nil {
		return false, fmt.Errorf("change student ban state: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("count student ban update: %w", err)
	}
	if rows != 1 {
		return false, errors.New("student ban state changed concurrently")
	}
	return true, nil
}

func HasAdministrator(ctx context.Context, query miSQLite.Querier) (bool, error) {
	var exists int
	if err := query.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM user_roles WHERE role = 'administrator')").Scan(&exists); err != nil {
		return false, fmt.Errorf("check administrators: %w", err)
	}
	return exists != 0, nil
}

func FindForLogin(ctx context.Context, query miSQLite.Querier, username string) (Account, error) {
	key, err := identity.Username(username)
	if err != nil {
		return Account{}, err
	}
	var account Account
	var gate, banned int
	err = query.QueryRowContext(ctx, "SELECT id, password_hash, security_generation, must_change_password, is_banned FROM users WHERE username_key = ?", key).
		Scan(&account.ID, &account.PasswordHash, &account.SecurityGeneration, &gate, &banned)
	if err != nil {
		return Account{}, fmt.Errorf("find login account: %w", err)
	}
	account.MustChangePassword, account.Banned = gate != 0, banned != 0
	return account, nil
}

// LoadSecurityState reloads the cookie-authoritative account state on every request.
func LoadSecurityState(ctx context.Context, query miSQLite.Querier, id string) (Account, error) {
	var account Account
	var gate, banned int
	err := query.QueryRowContext(ctx, "SELECT id, security_generation, must_change_password, is_banned FROM users WHERE id = ?", id).
		Scan(&account.ID, &account.SecurityGeneration, &gate, &banned)
	if err != nil {
		return Account{}, fmt.Errorf("load account security state: %w", err)
	}
	account.MustChangePassword, account.Banned = gate != 0, banned != 0
	return account, nil
}

func ChangePassword(ctx context.Context, query miSQLite.Querier, userID, hash string) error {
	result, err := query.ExecContext(ctx, "UPDATE users SET password_hash = ?, must_change_password = 0 WHERE id = ?", hash, userID)
	if err != nil {
		return fmt.Errorf("change password: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count password change: %w", err)
	}
	if rows != 1 {
		return errors.New("account missing while changing password")
	}
	return nil
}

// GrantRole idempotently grants a permanent role while enforcing account-level invariants.
// Returns ErrRoleActorUnauthorized when the actor lacks the required role (403).
// Returns ErrRoleRecipientIneligible for both missing and ineligible targets (404) to avoid
// existence leaks per AD-4.
func GrantRole(ctx context.Context, query miSQLite.Querier, userID string, role Role, grantedBy string) error {
	if role != Administrator && role != Supervisor && role != Mentor {
		return ErrRoleInvalid
	}
	if grantedBy == "" {
		return ErrRoleActorRequired
	}
	requiredRole := string(Supervisor)
	if role == Administrator || role == Supervisor {
		requiredRole = string(Administrator)
	}
	// First check actor authorization independently - this determines 403 vs 404.
	var authorized int
	if err := query.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM user_roles WHERE user_id = ? AND role = ?)", grantedBy, requiredRole).Scan(&authorized); err != nil {
		return fmt.Errorf("check role grant actor: %w", err)
	}
	if authorized == 0 {
		return ErrRoleActorUnauthorized
	}
	// Now check target existence and eligibility in one scoped query.
	// Returns 1 if eligible, 0 if exists but ineligible, no rows if not found.
	// We return the same error for not-found and ineligible to avoid existence leak.
	var eligible int
	err := query.QueryRowContext(ctx, `
		SELECT CASE
			WHEN u.email_verified_at IS NOT NULL AND u.is_banned = 0
				AND EXISTS(SELECT 1 FROM user_roles r WHERE r.user_id = u.id AND r.role IN ('administrator', 'supervisor', 'mentor'))
			THEN 1 ELSE 0
		END AS eligible
		FROM users u
		WHERE u.id = ?`, userID).Scan(&eligible)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrRoleRecipientIneligible
	}
	if err != nil {
		return fmt.Errorf("check role grant recipient: %w", err)
	}
	if eligible == 0 {
		return ErrRoleRecipientIneligible
	}
	now := instant(time.Now())
	if _, err := query.ExecContext(ctx, "INSERT OR IGNORE INTO user_roles (user_id, role, granted_at, granted_by) VALUES (?, ?, ?, ?)", userID, role, now, nullable(grantedBy)); err != nil {
		return fmt.Errorf("grant role: %w", err)
	}
	if role == Supervisor {
		if _, err := query.ExecContext(ctx, "INSERT OR IGNORE INTO user_roles (user_id, role, granted_at, granted_by) VALUES (?, 'student', ?, ?)", userID, now, nullable(grantedBy)); err != nil {
			return fmt.Errorf("grant supervisor student role: %w", err)
		}
	}
	return nil
}

// initialRoles accepts only the account-creation shapes owned by this package.
// Supervisor creation always includes the student role in the same transaction.
func initialRoles(roles []Role) ([]Role, bool, error) {
	if len(roles) == 1 {
		switch roles[0] {
		case Student:
			return []Role{Student}, false, nil
		case Administrator, Mentor:
			return roles, true, nil
		case Supervisor:
			return []Role{Supervisor, Student}, true, nil
		}
	}
	if len(roles) == 2 && ((roles[0] == Supervisor && roles[1] == Student) || (roles[0] == Student && roles[1] == Supervisor)) {
		return []Role{Supervisor, Student}, true, nil
	}
	return nil, false, errors.New("unsupported initial account roles")
}

func userID() (string, error) {
	return "u_" + uuid.NewString(), nil
}

func instant(value time.Time) string { return value.UTC().Format("2006-01-02T15:04:05.000000Z") }
func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}

// EmailExists checks if a user with the given normalized email key exists.
func EmailExists(ctx context.Context, query miSQLite.Querier, emailKey string) (bool, error) {
	var exists int
	if err := query.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM users WHERE email_key = ?)", emailKey).Scan(&exists); err != nil {
		return false, fmt.Errorf("check email existence: %w", err)
	}
	return exists != 0, nil
}

// EmailKeyForDeletion returns the normalized email owned by an account so a
// registered lifecycle owner can remove records addressed to that identity.
func EmailKeyForDeletion(ctx context.Context, query miSQLite.Querier, accountID string) (string, error) {
	var emailKey sql.NullString
	if err := query.QueryRowContext(ctx, "SELECT email_key FROM users WHERE id = ?", accountID).Scan(&emailKey); err != nil {
		return "", fmt.Errorf("load account email key for deletion: %w", err)
	}
	if !emailKey.Valid {
		return "", nil
	}
	return emailKey.String, nil
}

// isUsernameUniqueViolation checks if the error is a SQLite UNIQUE constraint violation
// on the username_key column.
func isUsernameUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "UNIQUE constraint failed: users.username_key") ||
		strings.Contains(errStr, "UNIQUE constraint failed") && strings.Contains(errStr, "username_key")
}

// HasRole checks if a user has a specific role.
func HasRole(ctx context.Context, query miSQLite.Querier, userID string, role Role) (bool, error) {
	var exists int
	if err := query.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM user_roles WHERE user_id = ? AND role = ?)", userID, string(role)).Scan(&exists); err != nil {
		return false, fmt.Errorf("check role: %w", err)
	}
	return exists != 0, nil
}

// HasAnyRole checks if a user has any of the specified roles.
func HasAnyRole(ctx context.Context, query miSQLite.Querier, userID string, roles ...Role) (bool, error) {
	for _, role := range roles {
		has, err := HasRole(ctx, query, userID, role)
		if err != nil {
			return false, err
		}
		if has {
			return true, nil
		}
	}
	return false, nil
}
