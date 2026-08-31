package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/thorstenkramm/mia/internal/audit"
	"github.com/thorstenkramm/mia/internal/provider/smtp"
	miSQLite "github.com/thorstenkramm/mia/internal/sqlite"
)

// DeliveryManager owns bounded recovery-email delivery work. Close stops new
// work and waits for accepted deliveries to record their terminal effects.
type DeliveryManager struct {
	database *sql.DB
	mailer   recoveryMailer
	logger   *slog.Logger
	mu       sync.Mutex
	closed   bool
	jobs     chan delivery
	done     chan struct{}
	wg       sync.WaitGroup // in-flight Admit calls
	workers  sync.WaitGroup
}

type delivery struct {
	accountID, email, link, challengeID string
	context                             context.Context
}

func NewDeliveryManager(database *sql.DB, mailer recoveryMailer, logger *slog.Logger) *DeliveryManager {
	if logger == nil {
		logger = slog.Default()
	}
	manager := &DeliveryManager{database: database, mailer: mailer, logger: logger, jobs: make(chan delivery, 16), done: make(chan struct{})}
	for range 4 {
		manager.workers.Add(1)
		go func() {
			defer manager.workers.Done()
			for job := range manager.jobs {
				manager.deliver(context.WithoutCancel(job.context), job.accountID, job.email, job.link, job.challengeID)
			}
		}()
	}
	return manager
}

// Admit blocks while the bounded queue is full so an accepted request is never
// silently dropped. It reports false only once shutdown has stopped admission.
func (manager *DeliveryManager) Admit(ctx context.Context, accountID, email, link, challengeID string) bool {
	manager.mu.Lock()
	if manager.closed {
		manager.mu.Unlock()
		return false
	}
	manager.wg.Add(1)
	defer manager.wg.Done()
	manager.mu.Unlock()
	select {
	case manager.jobs <- delivery{accountID, email, link, challengeID, ctx}:
		return true
	case <-manager.done:
		return false
	}
}

func (manager *DeliveryManager) Close() {
	manager.mu.Lock()
	if manager.closed {
		manager.mu.Unlock()
		return
	}
	manager.closed = true
	close(manager.done)
	manager.mu.Unlock()
	manager.wg.Wait()
	close(manager.jobs)
	manager.workers.Wait()
}

func (manager *DeliveryManager) deliver(ctx context.Context, accountID, email, link, challengeID string) {
	err := manager.mailer.SendPasswordRecovery(ctx, email, link)
	if err == nil {
		return
	}
	action := audit.ActionAuthPasswordRecoveryAmbiguous
	if errors.Is(err, smtp.ErrTimeout) {
		action = audit.ActionAuthPasswordRecoveryTimedOut
		manager.logger.Warn("password recovery email timed out")
	} else if errors.Is(err, smtp.ErrRejected) {
		action = audit.ActionAuthPasswordRecoveryDeliveryFailed
	} else {
		manager.logger.Warn("password recovery email delivery outcome ambiguous")
	}
	if errors.Is(err, smtp.ErrRejected) {
		err = miSQLite.WithTx(ctx, manager.database, func(tx *sql.Tx) error {
			if _, updateErr := tx.ExecContext(ctx, "UPDATE password_reset_challenges SET consumed_at = ? WHERE id = ? AND consumed_at IS NULL", instant(time.Now()), challengeID); updateErr != nil {
				return fmt.Errorf("invalidate failed password reset challenge: %w", updateErr)
			}
			return audit.Write(ctx, tx, action, "", accountID)
		})
	} else {
		err = writeAudit(ctx, manager.database, action, accountID)
	}
	if err != nil {
		manager.logger.Error("record password recovery delivery outcome", "error", err)
	}
}
