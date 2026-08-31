// Package user owns account, role, and account-security persistence.
package user

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/thorstenkramm/mia/internal/identity"
	miSQLite "github.com/thorstenkramm/mia/internal/sqlite"
)

var ErrAdministratorExists = errors.New("administrator already exists")

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

// MFAProfile contains the account data auth needs for MFA flows. user owns
// these account-record reads even when auth owns the MFA tables.
type MFAProfile struct {
	Username, PasswordHash string
	MustChangePassword     bool
	VerifiedMobile         string
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
		(id, username, username_key, email, email_key, email_verified_at, password_hash, preferred_language, country, time_zone, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, input.Username, usernameKey, nullable(email), nullable(emailKey), verified,
		input.PasswordHash, language, country, timeZone, now)
	if err != nil {
		return Account{}, fmt.Errorf("create user: %w", err)
	}
	for _, role := range roles {
		if _, err := query.ExecContext(ctx, "INSERT INTO user_roles (user_id, role, granted_at) VALUES (?, ?, ?)", id, role, now); err != nil {
			return Account{}, fmt.Errorf("grant initial role: %w", err)
		}
	}
	return Account{ID: id, PasswordHash: input.PasswordHash, SecurityGeneration: 1}, nil
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
func GrantRole(ctx context.Context, query miSQLite.Querier, userID string, role Role, grantedBy string) error {
	if role != Administrator && role != Supervisor && role != Mentor {
		return errors.New("invalid role")
	}
	if grantedBy == "" {
		return errors.New("role grant actor is required")
	}
	var authorized int
	requiredRole := Supervisor
	if role == Administrator || role == Supervisor {
		requiredRole = Administrator
	}
	if err := query.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM user_roles WHERE user_id = ? AND role = ?)", grantedBy, requiredRole).Scan(&authorized); err != nil {
		return fmt.Errorf("load role grant actor: %w", err)
	}
	if authorized == 0 {
		return errors.New("role grant actor is not authorized")
	}
	var verified any
	var banned int
	var staff int
	if err := query.QueryRowContext(ctx, "SELECT email_verified_at, is_banned FROM users WHERE id = ?", userID).Scan(&verified, &banned); err != nil {
		return fmt.Errorf("load role recipient: %w", err)
	}
	if err := query.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM user_roles WHERE user_id = ? AND role IN ('administrator', 'supervisor', 'mentor'))", userID).Scan(&staff); err != nil {
		return fmt.Errorf("check role recipient staff status: %w", err)
	}
	if verified == nil || banned != 0 || staff == 0 {
		return errors.New("role recipient does not meet staff role requirements")
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
