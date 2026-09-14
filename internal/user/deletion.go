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
	database       *sql.DB
	dataDir        string
	lifecycle      *lifecycle.Registry
	logger         *slog.Logger
	soleSupervisor SoleSupervisorCheck
}

// SoleSupervisorCheck is supplied by the course table owner.
type SoleSupervisorCheck func(context.Context, miSQLite.Querier, string) (bool, error)

// SetSoleSupervisorCheck installs the course-owned account deletion guard reader.
func (service *DeletionService) SetSoleSupervisorCheck(check SoleSupervisorCheck) {
	service.soleSupervisor = check
}

// ListAccounts reads one coherent administration page in a transaction snapshot.
func (service *DeletionService) ListAccounts(ctx context.Context, actorID string,
	filter AccountFilter) (AdministrationList, error) {
	var result AdministrationList
	err := miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		var loadErr error
		result, loadErr = ListAdministrationAccounts(ctx, tx, actorID, filter, service.soleSupervisor)
		return loadErr
	})
	return result, err
}

// GetAccount reads one coherent administration target and effect validator.
func (service *DeletionService) GetAccount(ctx context.Context, actorID, accountID string) (AdministrationAccount, error) {
	var account AdministrationAccount
	err := miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		var loadErr error
		account, loadErr = GetAdministrationAccount(ctx, tx, actorID, accountID, service.soleSupervisor)
		return loadErr
	})
	return account, err
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
	return service.delete(ctx, actorID, accountID, nil)
}

// DeleteReviewed requires the effect-complete validator returned by account detail.
func (service *DeletionService) DeleteReviewed(ctx context.Context, actorID, accountID, expectedETag string) error {
	return service.delete(ctx, actorID, accountID, &expectedETag)
}

func (service *DeletionService) delete(ctx context.Context, actorID, accountID string, expectedETag *string) error {
	err := miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		administrator, err := HasRole(ctx, tx, actorID, Administrator)
		if err != nil {
			return err
		}
		if !administrator {
			return ErrDeletionUnauthorized
		}
		snapshot, err := loadAdministrationSnapshot(ctx, tx, accountID, actorID, service.soleSupervisor)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrDeletionNotFound
		}
		if err != nil {
			return err
		}
		if expectedETag != nil {
			if *expectedETag == "" {
				return ErrAccountPreconditionRequired
			}
			if *expectedETag != snapshot.ETag {
				return ErrAccountPreconditionFailed
			}
		}
		roles := make(map[Role]bool, len(snapshot.Roles))
		for _, role := range snapshot.Roles {
			roles[role] = true
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
