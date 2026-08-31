package invitation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/thorstenkramm/mia/internal/audit"
	"github.com/thorstenkramm/mia/internal/provider/smtp"
	miSQLite "github.com/thorstenkramm/mia/internal/sqlite"
)

// invitationMailer sends invitation emails.
type invitationMailer interface {
	SendInvitation(context.Context, string, string, string) error
}

// DeliveryManager owns bounded invitation-email delivery work. Close stops new
// work and waits for accepted deliveries to record their terminal effects.
type DeliveryManager struct {
	service  *Service
	database *sql.DB
	mailer   invitationMailer
	logger   *slog.Logger
	mu       sync.Mutex
	closed   bool
	jobs     chan delivery
	done     chan struct{}
	wg       sync.WaitGroup
	workers  sync.WaitGroup
}

type delivery struct {
	invitationID, email, role, link string
	generation                      int
	context                         context.Context
}

// NewDeliveryManager creates the async invitation email delivery manager.
func NewDeliveryManager(service *Service, database *sql.DB, mailer invitationMailer, logger *slog.Logger) *DeliveryManager {
	if logger == nil {
		logger = slog.Default()
	}
	manager := &DeliveryManager{service: service, database: database, mailer: mailer, logger: logger, jobs: make(chan delivery, 16), done: make(chan struct{})}
	for range 4 {
		manager.workers.Add(1)
		go func() {
			defer manager.workers.Done()
			for job := range manager.jobs {
				manager.deliver(context.WithoutCancel(job.context), job.invitationID, job.email, job.role, job.link, job.generation)
			}
		}()
	}
	return manager
}

// Admit blocks while the bounded queue is full so an accepted request is never
// silently dropped. It reports false when:
// - shutdown has stopped admission
// - the context is canceled while waiting for queue space
// The invitation remains valid even if delivery admission fails; it can be resent manually.
func (manager *DeliveryManager) Admit(ctx context.Context, invitationID, email, role, link string, generation int) bool {
	// Check if context is already canceled before entering select.
	// This prevents a pre-canceled context from racing into the channel send.
	if ctx.Err() != nil {
		return false
	}
	manager.mu.Lock()
	if manager.closed {
		manager.mu.Unlock()
		return false
	}
	manager.wg.Add(1)
	defer manager.wg.Done()
	manager.mu.Unlock()
	select {
	case manager.jobs <- delivery{invitationID, email, role, link, generation, ctx}:
		return true
	case <-manager.done:
		return false
	case <-ctx.Done():
		return false
	}
}

// Close stops admission and waits for in-flight work.
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

func (manager *DeliveryManager) deliver(ctx context.Context, invitationID, email, role, link string, generation int) {
	err := manager.mailer.SendInvitation(ctx, email, role, link)
	if err == nil {
		// Success: update sent_at and audit delivery (Finding 2).
		if markErr := manager.service.MarkSentIfGeneration(ctx, invitationID, generation); markErr != nil {
			manager.logger.Error("mark invitation sent", "invitation_id", invitationID, "error", markErr)
		}
		return
	}
	if errors.Is(err, smtp.ErrTimeout) {
		manager.logger.Warn("invitation email timed out", "invitation_id", invitationID)
		manager.auditDeliveryOutcomeIfGeneration(ctx, audit.ActionInvitationInvitationDeliveryTimeout, invitationID, "timeout", generation)
		return
	}
	if errors.Is(err, smtp.ErrRejected) {
		// Rejected is audited via MarkFaultyIfGeneration which writes its own audit record.
		if markErr := manager.service.MarkFaultyIfGeneration(ctx, invitationID, "smtp_rejected", generation); markErr != nil {
			manager.logger.Error("mark invitation faulty", "invitation_id", invitationID, "error", markErr)
		}
		return
	}
	// All other errors (including ErrAmbiguous and any unclassified errors) are treated as ambiguous.
	// This is the safest default - we don't know if the message was delivered or not.
	manager.logger.Warn("invitation email delivery outcome ambiguous", "invitation_id", invitationID, "error", err)
	manager.auditDeliveryOutcomeIfGeneration(ctx, audit.ActionInvitationInvitationDeliveryAmbiguous, invitationID, "ambiguous", generation)
}

// auditDeliveryOutcomeIfGeneration audits a delivery outcome only if the invitation's
// current generation matches AND status is still pending, preventing stale deliveries
// from auditing against newer tokens or terminal invitations.
func (manager *DeliveryManager) auditDeliveryOutcomeIfGeneration(ctx context.Context, action audit.Action, invitationID, outcomeCode string, generation int) {
	if manager.database == nil {
		return
	}
	if err := miSQLite.WithTx(ctx, manager.database, func(tx *sql.Tx) error {
		// Check if the invitation still has the same generation AND is still pending before auditing.
		// This prevents stale deliveries from auditing against terminal invitations (accepted/revoked/faulty).
		var currentGeneration int
		var status string
		err := tx.QueryRowContext(ctx, "SELECT token_generation, status FROM invitations WHERE id = ?", invitationID).Scan(&currentGeneration, &status)
		if errors.Is(err, sql.ErrNoRows) {
			// Invitation was deleted - skip audit.
			return nil
		}
		if err != nil {
			return fmt.Errorf("check invitation generation: %w", err)
		}
		if currentGeneration != generation {
			// Stale delivery - a resend has occurred, skip audit for this old generation.
			return nil
		}
		if status != "pending" {
			// Invitation became terminal during delivery - skip audit.
			return nil
		}
		return audit.WriteWithMetadata(ctx, tx, action, "", "", audit.Metadata{InvitationID: invitationID, OutcomeCode: outcomeCode})
	}); err != nil {
		manager.logger.Error("audit delivery outcome", "action", action, "error", err)
	}
}
