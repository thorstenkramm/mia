package invitation

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/thorstenkramm/mia/internal/httpserver"
	"github.com/thorstenkramm/mia/internal/provider/smtp"
)

// invitationMailer sends invitation emails.
type invitationMailer interface {
	SendInvitation(context.Context, string, string, string) error
}

// DeliveryManager owns bounded invitation-email delivery work. Close stops new
// work and waits for accepted deliveries to record their terminal effects.
type DeliveryManager struct {
	service *Service
	mailer  invitationMailer
	logger  *slog.Logger
	mu      sync.Mutex
	closed  bool
	jobs    chan delivery
	done    chan struct{}
	wg      sync.WaitGroup
	workers sync.WaitGroup
}

type delivery struct {
	invitationID, email, role, link string
	generation                      int
	context                         context.Context
}

// NewDeliveryManager creates the async invitation email delivery manager.
func NewDeliveryManager(service *Service, _ *sql.DB, mailer invitationMailer, logger *slog.Logger) *DeliveryManager {
	if logger == nil {
		logger = slog.Default()
	}
	manager := &DeliveryManager{service: service, mailer: mailer, logger: logger, jobs: make(chan delivery, 16), done: make(chan struct{})}
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
	attemptedAt := time.Now().UTC()
	err := manager.mailer.SendInvitation(ctx, email, role, link)
	if err == nil {
		if markErr := manager.service.MarkSentIfGeneration(ctx, invitationID, generation, attemptedAt); markErr != nil {
			manager.logger.Error("mark invitation sent", "invitation_id", invitationID, "error", markErr)
		}
		return
	}
	if errors.Is(err, smtp.ErrTimeout) {
		manager.logger.Warn("invitation email timed out", "invitation_id", invitationID)
		if markErr := manager.service.MarkAmbiguousIfGeneration(
			ctx, invitationID, httpserver.StableCode(httpserver.CodeInvitationDeliveryTimeout), generation, attemptedAt,
		); markErr != nil {
			manager.logger.Error("record invitation delivery timeout", "invitation_id", invitationID, "error", markErr)
		}
		return
	}
	if errors.Is(err, smtp.ErrRejected) {
		// Rejected is audited via MarkFaultyIfGeneration which writes its own audit record.
		if markErr := manager.service.MarkFaultyIfGeneration(
			ctx, invitationID, httpserver.StableCode(httpserver.CodeInvitationDeliveryRejected), generation, attemptedAt,
		); markErr != nil {
			manager.logger.Error("mark invitation faulty", "invitation_id", invitationID, "error", markErr)
		}
		return
	}
	// All other errors (including ErrAmbiguous and any unclassified errors) are treated as ambiguous.
	// This is the safest default - we don't know if the message was delivered or not.
	manager.logger.Warn("invitation email delivery outcome ambiguous", "invitation_id", invitationID, "error", err)
	if markErr := manager.service.MarkAmbiguousIfGeneration(
		ctx, invitationID, httpserver.StableCode(httpserver.CodeInvitationDeliveryAmbiguous), generation, attemptedAt,
	); markErr != nil {
		manager.logger.Error("record ambiguous invitation delivery", "invitation_id", invitationID, "error", markErr)
	}
}
