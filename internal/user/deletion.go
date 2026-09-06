package user

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/thorstenkramm/mia/internal/audit"
	"github.com/thorstenkramm/mia/internal/lifecycle"
	miSQLite "github.com/thorstenkramm/mia/internal/sqlite"
)

var (
	ErrDeletionUnauthorized = errors.New("account deletion unauthorized")
	ErrDeletionNotFound     = errors.New("account deletion target not found")
	ErrLastAdministrator    = errors.New("last administrator cannot be deleted")
	ErrSoleSupervisor       = errors.New("sole course supervisor cannot be deleted")
)

// DeletionService owns account deletion transactions. It is safe for concurrent use.
type DeletionService struct {
	database  *sql.DB
	dataDir   string
	lifecycle *lifecycle.Registry
	logger    *slog.Logger
}

func NewDeletionService(database *sql.DB, dataDir string, registry *lifecycle.Registry,
	logger *slog.Logger) *DeletionService {
	if registry == nil {
		registry = &lifecycle.Registry{}
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &DeletionService{database: database, dataDir: dataDir, lifecycle: registry, logger: logger}
}

// Delete removes one account and every registered account-scoped dependency in
// one transaction. It intentionally does not coordinate with asynchronous work.
func (service *DeletionService) Delete(ctx context.Context, actorID, accountID string) error {
	err := miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		administrator, err := HasRole(ctx, tx, actorID, Administrator)
		if err != nil {
			return err
		}
		if !administrator {
			return ErrDeletionUnauthorized
		}
		roles, err := deletionRoles(ctx, tx, accountID)
		if err != nil {
			return err
		}
		if roles[Administrator] {
			var count int
			if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM user_roles WHERE role = 'administrator'").Scan(&count); err != nil {
				return fmt.Errorf("count administrators for deletion: %w", err)
			}
			if count <= 1 {
				return ErrLastAdministrator
			}
		}
		staff := roles[Administrator] || roles[Supervisor] || roles[Mentor]
		if staff && actorID == accountID {
			return ErrDeletionUnauthorized
		}
		if err := service.lifecycle.DeleteAccountData(ctx, tx, accountID); err != nil {
			if errors.Is(err, lifecycle.ErrAccountDeletionBlocked) {
				return ErrSoleSupervisor
			}
			return fmt.Errorf("delete registered account data: %w", err)
		}
		if err := audit.ReplaceAccountHistoryWithDeletion(ctx, tx, accountID, actorID); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, "DELETE FROM users WHERE id = ?", accountID)
		if err != nil {
			return fmt.Errorf("delete account: %w", err)
		}
		count, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("count deleted accounts: %w", err)
		}
		if count != 1 {
			return ErrDeletionNotFound
		}
		return nil
	})
	if err != nil {
		return err
	}
	if err := service.lifecycle.CleanupAccountData(ctx, accountID); err != nil {
		service.logger.WarnContext(ctx, "clean deleted account files", "account_id", accountID, "error", err)
	}
	if err := os.RemoveAll(filepath.Join(service.dataDir, "users", accountID)); err != nil {
		service.logger.WarnContext(ctx, "remove deleted account files", "account_id", accountID, "error", err)
	}
	return nil
}

func deletionRoles(ctx context.Context, query miSQLite.Querier, accountID string) (map[Role]bool, error) {
	rows, err := query.QueryContext(ctx, `SELECT role FROM user_roles
		WHERE user_id = ? AND role IN ('administrator', 'supervisor', 'mentor', 'student')`, accountID)
	if err != nil {
		return nil, fmt.Errorf("load deletion target roles: %w", err)
	}
	roles := make(map[Role]bool, 4)
	for rows.Next() {
		var role Role
		if err := rows.Scan(&role); err != nil {
			return nil, errors.Join(fmt.Errorf("scan deletion target role: %w", err), rows.Close())
		}
		roles[role] = true
	}
	if err := rows.Err(); err != nil {
		return nil, errors.Join(fmt.Errorf("iterate deletion target roles: %w", err), rows.Close())
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close deletion target roles: %w", err)
	}
	if len(roles) == 0 {
		return nil, ErrDeletionNotFound
	}
	return roles, nil
}

// AuditDeletionDenied records a valid denied mutation without retaining the
// requested target identity, preserving hidden-resource behavior.
func (service *DeletionService) AuditDeletionDenied(ctx context.Context, actorID, outcome string) {
	err := miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		return audit.WriteWithMetadata(ctx, tx, audit.ActionUserAccountDeletionDenied, actorID, "",
			audit.Metadata{OutcomeCode: outcome})
	})
	if err != nil {
		service.logger.WarnContext(ctx, "audit denied account deletion", "error", err)
	}
}
