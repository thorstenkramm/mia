// Package invitation owns staff invitation and acceptance flows.
package invitation

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/thorstenkramm/mia/internal/audit"
	"github.com/thorstenkramm/mia/internal/identity"
	miSQLite "github.com/thorstenkramm/mia/internal/sqlite"
	"github.com/thorstenkramm/mia/internal/user"
)

// Status is the invitation lifecycle state.
type Status string

const (
	StatusPending  Status = "pending"
	StatusAccepted Status = "accepted"
	StatusRevoked  Status = "revoked"
	StatusFaulty   Status = "faulty"
)

// Role is the staff role granted by invitation acceptance.
type Role string

const (
	RoleAdministrator Role = "administrator"
	RoleSupervisor    Role = "supervisor"
	RoleMentor        Role = "mentor"
)

var (
	ErrInvitationNotFound         = errors.New("invitation not found")
	ErrInvitationInvalid          = errors.New("invitation invalid")
	ErrInvitationEmailExists      = errors.New("invitation email already registered")
	ErrInvitationPendingExists    = errors.New("pending invitation exists for email")
	ErrInvitationNotPending       = errors.New("invitation is not pending")
	ErrInvitationUnauthorized     = errors.New("unauthorized to manage invitation")
	ErrInvitationRoleInvalid      = errors.New("invalid invitation role for actor")
	ErrInvitationListUnauthorized = errors.New("unauthorized to list invitations")
)

// Invitation is the persisted invitation record.
type Invitation struct {
	ID              string
	Email           string
	EmailNormalized string
	Role            Role
	Status          Status
	TokenGeneration int
	FailureCode     *string
	InviterID       *string
	AcceptedBy      *string
	RevokedBy       *string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	SentAt          *time.Time
	AcceptedAt      *time.Time
	RevokedAt       *time.Time
	FaultAt         *time.Time
}

// CreateInput contains validated fields for a new invitation.
type CreateInput struct {
	Email     string
	Role      Role
	InviterID string
}

// AcceptInput contains validated fields for invitation acceptance.
type AcceptInput struct {
	Token        string
	Username     string
	Password     string
	Language     string
	Country      string
	TimeZone     string
	PasswordHash string
}

// Service owns invitation persistence and business rules.
type Service struct {
	database *sql.DB
	logger   *slog.Logger
}

// NewService creates the invitation service.
func NewService(database *sql.DB, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{database: database, logger: logger}
}

// Create inserts a new pending invitation after authorization checks.
func (s *Service) Create(ctx context.Context, input CreateInput) (Invitation, string, error) {
	email, emailKey, err := identity.Email(input.Email)
	if err != nil {
		return Invitation{}, "", err
	}
	if input.Role != RoleAdministrator && input.Role != RoleSupervisor && input.Role != RoleMentor {
		return Invitation{}, "", ErrInvitationRoleInvalid
	}
	token := newToken()
	digest := tokenDigest(token)
	id := invitationID()
	now := time.Now().UTC()
	var invitation Invitation
	err = miSQLite.WithTx(ctx, s.database, func(tx *sql.Tx) error {
		authorized, err := canCreateInvitation(ctx, tx, input.InviterID, input.Role)
		if err != nil {
			return err
		}
		if !authorized {
			return ErrInvitationRoleInvalid
		}
		existsUser, err := user.EmailExists(ctx, tx, emailKey)
		if err != nil {
			return err
		}
		if existsUser {
			return ErrInvitationEmailExists
		}
		var existsPending int
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM invitations WHERE email_normalized = ? AND status = 'pending')", emailKey).Scan(&existsPending); err != nil {
			return fmt.Errorf("check pending invitation: %w", err)
		}
		if existsPending != 0 {
			return ErrInvitationPendingExists
		}
		nowStr := instant(now)
		_, err = tx.ExecContext(ctx, `INSERT INTO invitations
			(id, email, email_normalized, role, token_digest, token_generation, status, inviter_id, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, 1, 'pending', ?, ?, ?)`,
			id, email, emailKey, string(input.Role), digest[:], input.InviterID, nowStr, nowStr)
		if err != nil {
			return fmt.Errorf("insert invitation: %w", err)
		}
		if err := audit.WriteWithMetadata(ctx, tx, audit.ActionInvitationInvitationCreated, input.InviterID, "", audit.Metadata{InvitationID: id, Role: string(input.Role)}); err != nil {
			return err
		}
		inviterID := input.InviterID
		invitation = Invitation{
			ID:              id,
			Email:           email,
			EmailNormalized: emailKey,
			Role:            input.Role,
			Status:          StatusPending,
			TokenGeneration: 1,
			InviterID:       &inviterID,
			CreatedAt:       now,
			UpdatedAt:       now,
		}
		return nil
	})
	if err != nil {
		return Invitation{}, "", err
	}
	return invitation, token, nil
}

// Get returns an invitation by ID with authorization checks.
// Uses authorization-scoped fetch to avoid loading sensitive data before authorization.
func (s *Service) Get(ctx context.Context, id, actorID string) (Invitation, error) {
	var inv Invitation
	err := miSQLite.WithTx(ctx, s.database, func(tx *sql.Tx) error {
		var err error
		inv, err = loadInvitationScoped(ctx, tx, id, actorID, scopeView)
		return err
	})
	return inv, err
}

// ListInput contains pagination parameters for invitation listing.
type ListInput struct {
	Offset   int
	PageSize int
}

// ListResult contains paginated invitations with navigation info.
type ListResult struct {
	Invitations []Invitation
	HasMore     bool
}

const maxPageSize = 100

// List returns invitations visible to the actor with pagination.
func (s *Service) List(ctx context.Context, actorID string, input ListInput) (ListResult, error) {
	pageSize := input.PageSize
	if pageSize <= 0 || pageSize > maxPageSize {
		pageSize = maxPageSize
	}
	var result ListResult
	err := miSQLite.WithTx(ctx, s.database, func(tx *sql.Tx) error {
		isAdmin, err := hasRole(ctx, tx, actorID, user.Administrator)
		if err != nil {
			return err
		}
		isSupervisor, err := hasRole(ctx, tx, actorID, user.Supervisor)
		if err != nil {
			return err
		}
		// Only administrators and supervisors can list invitations.
		if !isAdmin && !isSupervisor {
			return ErrInvitationListUnauthorized
		}
		// Fetch one extra row to determine if there are more results.
		fetchLimit := pageSize + 1
		var query string
		var args []any
		if isAdmin {
			// Administrators can view all invitations.
			query = `SELECT id, email, email_normalized, role, token_generation, status, failure_code, inviter_id, created_at, updated_at
				FROM invitations ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`
			args = []any{fetchLimit, input.Offset}
		} else {
			// Supervisors see only their own mentor invitations.
			query = `SELECT id, email, email_normalized, role, token_generation, status, failure_code, inviter_id, created_at, updated_at
				FROM invitations WHERE role = 'mentor' AND inviter_id = ? ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`
			args = []any{actorID, fetchLimit, input.Offset}
		}
		rows, err := tx.QueryContext(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("list invitations: %w", err)
		}
		invitations, closeErr := scanInvitations(rows)
		if closeErr != nil {
			return closeErr
		}
		result.Invitations = invitations
		// Check if we fetched more than requested, indicating there are more results.
		if len(result.Invitations) > pageSize {
			result.HasMore = true
			result.Invitations = result.Invitations[:pageSize]
		}
		return nil
	})
	return result, err
}

// Delete revokes a pending invitation or physically deletes a faulty one.
// Uses authorization-scoped fetch to avoid loading sensitive data before authorization.
func (s *Service) Delete(ctx context.Context, id, actorID string) error {
	return miSQLite.WithTx(ctx, s.database, func(tx *sql.Tx) error {
		inv, err := loadInvitationScoped(ctx, tx, id, actorID, scopeManage)
		if err != nil {
			return err
		}
		if inv.Status == StatusAccepted || inv.Status == StatusRevoked {
			return ErrInvitationNotPending
		}
		if inv.Status == StatusFaulty {
			if _, err := tx.ExecContext(ctx, "DELETE FROM invitations WHERE id = ?", id); err != nil {
				return fmt.Errorf("delete faulty invitation: %w", err)
			}
			return audit.WriteWithMetadata(ctx, tx, audit.ActionInvitationInvitationFaultyDeleted, actorID, "", audit.Metadata{InvitationID: id})
		}
		now := time.Now()
		nowStr := instant(now)
		_, err = tx.ExecContext(ctx, "UPDATE invitations SET status = 'revoked', token_digest = NULL, updated_at = ?, revoked_at = ?, revoked_by = ? WHERE id = ?",
			nowStr, nowStr, actorID, id)
		if err != nil {
			return fmt.Errorf("revoke invitation: %w", err)
		}
		return audit.WriteWithMetadata(ctx, tx, audit.ActionInvitationInvitationRevoked, actorID, "", audit.Metadata{InvitationID: id})
	})
}

// Resend rotates the token for a pending invitation and returns the new token.
// Uses authorization-scoped fetch to avoid loading sensitive data before authorization.
func (s *Service) Resend(ctx context.Context, id, actorID string) (Invitation, string, error) {
	token := newToken()
	digest := tokenDigest(token)
	var inv Invitation
	err := miSQLite.WithTx(ctx, s.database, func(tx *sql.Tx) error {
		var err error
		inv, err = loadInvitationScoped(ctx, tx, id, actorID, scopeManage)
		if err != nil {
			return err
		}
		if inv.Status != StatusPending {
			return ErrInvitationNotPending
		}
		now := time.Now().UTC()
		newGeneration := inv.TokenGeneration + 1
		_, err = tx.ExecContext(ctx, "UPDATE invitations SET token_digest = ?, token_generation = ?, updated_at = ? WHERE id = ?",
			digest[:], newGeneration, instant(now), id)
		if err != nil {
			return fmt.Errorf("rotate invitation token: %w", err)
		}
		inv.UpdatedAt = now
		inv.TokenGeneration = newGeneration
		return audit.WriteWithMetadata(ctx, tx, audit.ActionInvitationInvitationResent, actorID, "", audit.Metadata{InvitationID: inv.ID})
	})
	return inv, token, err
}

// Preview returns the role for a valid pending invitation token without revealing other state.
func (s *Service) Preview(ctx context.Context, token string) (Role, error) {
	if !validToken(token) {
		return "", ErrInvitationInvalid
	}
	digest := tokenDigest(token)
	var role string
	err := s.database.QueryRowContext(ctx,
		"SELECT role FROM invitations WHERE token_digest = ? AND status = 'pending'", digest[:]).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrInvitationInvalid
	}
	if err != nil {
		return "", fmt.Errorf("preview invitation: %w", err)
	}
	return Role(role), nil
}

// IsPending checks if a token corresponds to a pending invitation without revealing other state.
// Returns ErrInvitationInvalid for invalid format, non-existent, or non-pending tokens.
func (s *Service) IsPending(ctx context.Context, token string) error {
	if !validToken(token) {
		return ErrInvitationInvalid
	}
	digest := tokenDigest(token)
	var exists int
	err := s.database.QueryRowContext(ctx,
		"SELECT 1 FROM invitations WHERE token_digest = ? AND status = 'pending'", digest[:]).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrInvitationInvalid
	}
	if err != nil {
		return fmt.Errorf("check invitation pending: %w", err)
	}
	return nil
}

// Accept consumes a pending invitation and creates the user account.
func (s *Service) Accept(ctx context.Context, input AcceptInput) (user.Account, error) {
	if !validToken(input.Token) {
		return user.Account{}, ErrInvitationInvalid
	}
	digest := tokenDigest(input.Token)
	var account user.Account
	err := miSQLite.WithTx(ctx, s.database, func(tx *sql.Tx) error {
		var id, email, role string
		err := tx.QueryRowContext(ctx,
			"SELECT id, email, role FROM invitations WHERE token_digest = ? AND status = 'pending'", digest[:]).
			Scan(&id, &email, &role)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInvitationInvalid
		}
		if err != nil {
			return fmt.Errorf("load invitation for acceptance: %w", err)
		}
		_, emailKey, err := identity.Email(email)
		if err != nil {
			return fmt.Errorf("normalize invitation email: %w", err)
		}
		emailExists, err := user.EmailExists(ctx, tx, emailKey)
		if err != nil {
			return err
		}
		if emailExists {
			return ErrInvitationEmailExists
		}
		var roles []user.Role
		switch Role(role) {
		case RoleAdministrator:
			roles = []user.Role{user.Administrator}
		case RoleSupervisor:
			roles = []user.Role{user.Supervisor}
		case RoleMentor:
			roles = []user.Role{user.Mentor}
		default:
			return fmt.Errorf("invalid invitation role: %s", role)
		}
		account, err = user.Create(ctx, tx, user.CreateInput{
			Username:      input.Username,
			Email:         email,
			PasswordHash:  input.PasswordHash,
			Language:      input.Language,
			Country:       input.Country,
			TimeZone:      input.TimeZone,
			EmailVerified: true,
			Roles:         roles,
		})
		if err != nil {
			return err
		}
		now := time.Now()
		nowStr := instant(now)
		result, err := tx.ExecContext(ctx,
			"UPDATE invitations SET status = 'accepted', token_digest = NULL, updated_at = ?, accepted_at = ?, accepted_by = ? WHERE id = ? AND status = 'pending'",
			nowStr, nowStr, account.ID, id)
		if err != nil {
			return fmt.Errorf("mark invitation accepted: %w", err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("count invitation acceptance: %w", err)
		}
		if rows != 1 {
			return ErrInvitationInvalid
		}
		return audit.WriteWithMetadata(ctx, tx, audit.ActionInvitationInvitationAccepted, account.ID, account.ID, audit.Metadata{InvitationID: id, Role: role})
	})
	return account, err
}

// MarkFaulty marks an invitation as faulty after definite SMTP rejection.
func (s *Service) MarkFaulty(ctx context.Context, id, failureCode string) error {
	return miSQLite.WithTx(ctx, s.database, func(tx *sql.Tx) error {
		now := time.Now()
		nowStr := instant(now)
		result, err := tx.ExecContext(ctx,
			"UPDATE invitations SET status = 'faulty', token_digest = NULL, failure_code = ?, updated_at = ?, fault_at = ? WHERE id = ? AND status = 'pending'",
			failureCode, nowStr, nowStr, id)
		if err != nil {
			return fmt.Errorf("mark invitation faulty: %w", err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("count faulty update: %w", err)
		}
		if rows != 1 {
			return nil
		}
		return audit.WriteWithMetadata(ctx, tx, audit.ActionInvitationInvitationMarkedFaulty, "", "", audit.Metadata{InvitationID: id, OutcomeCode: failureCode})
	})
}

// MarkFaultyIfGeneration marks an invitation as faulty only if its generation matches.
// This prevents stale delivery workers from marking a resent invitation faulty.
func (s *Service) MarkFaultyIfGeneration(ctx context.Context, id, failureCode string, generation int) error {
	return miSQLite.WithTx(ctx, s.database, func(tx *sql.Tx) error {
		now := time.Now()
		nowStr := instant(now)
		result, err := tx.ExecContext(ctx,
			`UPDATE invitations SET status = 'faulty', token_digest = NULL, failure_code = ?, updated_at = ?, fault_at = ?
			WHERE id = ? AND status = 'pending' AND token_generation = ?`,
			failureCode, nowStr, nowStr, id, generation)
		if err != nil {
			return fmt.Errorf("mark invitation faulty: %w", err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("count faulty update: %w", err)
		}
		if rows != 1 {
			// Generation mismatch or already terminal - no action needed
			return nil
		}
		return audit.WriteWithMetadata(ctx, tx, audit.ActionInvitationInvitationMarkedFaulty, "", "", audit.Metadata{InvitationID: id, OutcomeCode: failureCode})
	})
}

// MarkSentIfGeneration updates sent_at for a pending invitation only if generation matches.
// Used by the delivery manager to record successful delivery.
func (s *Service) MarkSentIfGeneration(ctx context.Context, id string, generation int) error {
	return miSQLite.WithTx(ctx, s.database, func(tx *sql.Tx) error {
		now := time.Now()
		nowStr := instant(now)
		result, err := tx.ExecContext(ctx,
			`UPDATE invitations SET sent_at = ?, updated_at = ?
			WHERE id = ? AND status = 'pending' AND token_generation = ?`,
			nowStr, nowStr, id, generation)
		if err != nil {
			return fmt.Errorf("mark invitation sent: %w", err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("count sent update: %w", err)
		}
		if rows != 1 {
			// Generation mismatch or already terminal - no action needed
			return nil
		}
		return audit.WriteWithMetadata(ctx, tx, audit.ActionInvitationInvitationDelivered, "", "", audit.Metadata{InvitationID: id})
	})
}

// scopeType defines the authorization scope for invitation fetches.
type scopeType int

const (
	// scopeView is for read-only access (Get).
	scopeView scopeType = iota
	// scopeManage is for mutation access (Delete, Resend).
	scopeManage
)

// loadInvitationScoped fetches an invitation with authorization in the WHERE clause.
// Returns ErrInvitationNotFound for both missing AND out-of-scope invitations,
// avoiding existence disclosure per AD-4.
func loadInvitationScoped(ctx context.Context, tx *sql.Tx, id, actorID string, scope scopeType) (Invitation, error) {
	// Build authorization-scoped query based on scope type.
	// Administrator can always access. Inviter can always access their own.
	// For view scope: same as manage scope (we allow any authorized user to view).
	// For manage scope: only inviter or administrator.
	//
	// The query includes actor role check and inviter ownership in WHERE clause.
	var query string
	switch scope {
	case scopeView:
		// View scope: administrator sees all, inviter sees own, supervisor sees mentor invitations from others
		query = `SELECT i.id, i.email, i.email_normalized, i.role, i.token_generation, i.status, i.failure_code, i.inviter_id, i.created_at, i.updated_at
			FROM invitations i
			WHERE i.id = ?
			AND (
				i.inviter_id = ?
				OR EXISTS(SELECT 1 FROM user_roles r WHERE r.user_id = ? AND r.role = 'administrator')
			)`
	case scopeManage:
		// Manage scope: only inviter or administrator
		query = `SELECT i.id, i.email, i.email_normalized, i.role, i.token_generation, i.status, i.failure_code, i.inviter_id, i.created_at, i.updated_at
			FROM invitations i
			WHERE i.id = ?
			AND (
				i.inviter_id = ?
				OR EXISTS(SELECT 1 FROM user_roles r WHERE r.user_id = ? AND r.role = 'administrator')
			)`
	}
	row := tx.QueryRowContext(ctx, query, id, actorID, actorID)
	return scanInvitationRow(row)
}

// scanInvitations scans all rows into a slice and properly closes the rows.
func scanInvitations(rows *sql.Rows) (invitations []Invitation, err error) {
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("close invitation rows: %w", closeErr)
		}
	}()
	for rows.Next() {
		inv, scanErr := scanInvitation(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		invitations = append(invitations, inv)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("iterate invitations: %w", rowsErr)
	}
	return invitations, nil
}

func scanInvitation(rows *sql.Rows) (Invitation, error) {
	return scanInvitationValues(rows)
}

type invitationRow interface {
	Scan(...any) error
}

func scanInvitationValues(row invitationRow) (Invitation, error) {
	var inv Invitation
	var roleStr, statusStr string
	var failureCode, inviterID sql.NullString
	var createdAt, updatedAt string
	if err := row.Scan(&inv.ID, &inv.Email, &inv.EmailNormalized, &roleStr, &inv.TokenGeneration, &statusStr, &failureCode, &inviterID, &createdAt, &updatedAt); err != nil {
		return Invitation{}, fmt.Errorf("scan invitation: %w", err)
	}
	inv.Role = Role(roleStr)
	inv.Status = Status(statusStr)
	if failureCode.Valid {
		inv.FailureCode = &failureCode.String
	}
	if inviterID.Valid {
		inv.InviterID = &inviterID.String
	}
	var err error
	inv.CreatedAt, err = time.Parse("2006-01-02T15:04:05.000000Z", createdAt)
	if err != nil {
		return Invitation{}, fmt.Errorf("parse invitation created_at: %w", err)
	}
	inv.UpdatedAt, err = time.Parse("2006-01-02T15:04:05.000000Z", updatedAt)
	if err != nil {
		return Invitation{}, fmt.Errorf("parse invitation updated_at: %w", err)
	}
	return inv, nil
}

func scanInvitationRow(row *sql.Row) (Invitation, error) {
	inv, err := scanInvitationValues(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Invitation{}, ErrInvitationNotFound
		}
		return Invitation{}, err
	}
	return inv, nil
}

func canCreateInvitation(ctx context.Context, tx *sql.Tx, actorID string, role Role) (bool, error) {
	if role == RoleAdministrator || role == RoleSupervisor {
		return hasRole(ctx, tx, actorID, user.Administrator)
	}
	if role == RoleMentor {
		// Only supervisors can create mentor invitations per AGENTS.md:
		// "Any supervisor may invite a mentor."
		return hasRole(ctx, tx, actorID, user.Supervisor)
	}
	return false, nil
}

func hasRole(ctx context.Context, tx *sql.Tx, userID string, role user.Role) (bool, error) {
	return user.HasRole(ctx, tx, userID, role)
}

func newToken() string {
	return strings.ToLower(uuid.NewString())
}

func tokenDigest(token string) [32]byte {
	return sha256.Sum256([]byte(token))
}

func validToken(token string) bool {
	parsed, err := uuid.Parse(token)
	if err != nil || parsed.Version() != 4 || parsed.String() != token {
		return false
	}
	return strings.ToLower(token) == token
}

func invitationID() string {
	return "inv_" + uuid.NewString()
}

func instant(value time.Time) string {
	return value.UTC().Format("2006-01-02T15:04:05.000000Z")
}

// AuditCreationDenied records a denied invitation creation without revealing sensitive data.
// Fire-and-forget: audit failure does not change the API response for denied mutations.
func (s *Service) AuditCreationDenied(ctx context.Context, actorID, role string) {
	err := miSQLite.WithTx(ctx, s.database, func(tx *sql.Tx) error {
		return audit.WriteWithMetadata(ctx, tx, audit.ActionInvitationInvitationCreationDenied, actorID, "", audit.Metadata{Role: role})
	})
	if err != nil {
		s.logger.ErrorContext(ctx, "audit write failed", "action", audit.ActionInvitationInvitationCreationDenied, "error", err)
	}
}

// AuditRevocationDenied records a denied invitation revocation without revealing existence.
// Fire-and-forget: audit failure does not change the API response for denied mutations.
func (s *Service) AuditRevocationDenied(ctx context.Context, actorID string) {
	err := miSQLite.WithTx(ctx, s.database, func(tx *sql.Tx) error {
		return audit.Write(ctx, tx, audit.ActionInvitationInvitationRevocationDenied, actorID, "")
	})
	if err != nil {
		s.logger.ErrorContext(ctx, "audit write failed", "action", audit.ActionInvitationInvitationRevocationDenied, "error", err)
	}
}

// AuditResendDenied records a denied invitation resend without revealing existence.
// Fire-and-forget: audit failure does not change the API response for denied mutations.
func (s *Service) AuditResendDenied(ctx context.Context, actorID string) {
	err := miSQLite.WithTx(ctx, s.database, func(tx *sql.Tx) error {
		return audit.Write(ctx, tx, audit.ActionInvitationInvitationResendDenied, actorID, "")
	})
	if err != nil {
		s.logger.ErrorContext(ctx, "audit write failed", "action", audit.ActionInvitationInvitationResendDenied, "error", err)
	}
}
