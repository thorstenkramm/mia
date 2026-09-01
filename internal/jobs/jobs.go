// Package jobs owns MIA's durable leased background-work queue.
package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	miSQLite "github.com/thorstenkramm/mia/internal/sqlite"
)

const (
	leaseDuration = 2 * time.Minute
	renewInterval = 30 * time.Second
	idlePoll      = time.Second
	maxAttempts   = 3
)

var ErrStaleLease = errors.New("job lease is stale")

type Job struct {
	ID, Type, SubjectType, SubjectID, CourseID string
	OwnerUserID, LeaseToken                    string
	Attempt                                    int
}

type Result struct {
	Value               any
	ProviderInputUnits  int64
	ProviderOutputUnits int64
}

// Handler executes one registered type. Commit runs in the same transaction as
// the lease-guarded job transition. Its returned finalizer receives true only
// after that transaction commits, allowing reversible file publication.
type Handler interface {
	Execute(context.Context, Job) (Result, error)
	Commit(context.Context, miSQLite.Querier, Job, Result) (func(bool) error, error)
	TerminalFailure(context.Context, miSQLite.Querier, Job, string) error
}

type Failure struct {
	Code                                    string
	Retryable                               bool
	RetryAfter                              time.Duration
	ProviderInputUnits, ProviderOutputUnits int64
	Cause                                   error
}

func (failure *Failure) Error() string {
	if failure.Cause != nil {
		return failure.Cause.Error()
	}
	return failure.Code
}

func (failure *Failure) Unwrap() error { return failure.Cause }

// Worker is safe for concurrent Start, Stop, and queue inspection. Handler
// registration must finish before Start.
type Worker struct {
	database  *sql.DB
	logger    *slog.Logger
	handlers  map[string]Handler
	mu        sync.Mutex
	claimMu   sync.Mutex
	done      chan struct{}
	stop      chan struct{}
	stopOnce  sync.Once
	runCancel context.CancelFunc
}

func New(database *sql.DB, logger *slog.Logger) *Worker {
	if logger == nil {
		logger = slog.Default()
	}
	return &Worker{database: database, logger: logger, handlers: make(map[string]Handler)}
}

func (worker *Worker) Register(jobType string, handler Handler) {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	if worker.done != nil || handler == nil || worker.handlers[jobType] != nil {
		panic("invalid job handler registration")
	}
	worker.handlers[jobType] = handler
}

func Enqueue(ctx context.Context, query miSQLite.Querier, jobType, subjectType, subjectID, courseID, ownerID string) (string, error) {
	id := "job_" + uuid.NewString()
	now := instant(time.Now())
	_, err := query.ExecContext(ctx, `INSERT INTO jobs
		(id, type, subject_type, subject_id, course_id, owner_user_id, available_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, id, jobType, subjectType, subjectID, courseID, nullable(ownerID), now, now)
	if err != nil {
		return "", fmt.Errorf("enqueue job: %w", err)
	}
	return id, nil
}

func (worker *Worker) Recover(ctx context.Context) error {
	return miSQLite.WithTx(ctx, worker.database, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT id, type, subject_type, subject_id, course_id,
			COALESCE(owner_user_id, ''), attempt_count, COALESCE(lease_token, '') FROM jobs
			WHERE state = 'running'`)
		if err != nil {
			return fmt.Errorf("list expired job leases: %w", err)
		}
		var abandoned []Job
		for rows.Next() {
			var job Job
			if err := rows.Scan(&job.ID, &job.Type, &job.SubjectType, &job.SubjectID, &job.CourseID,
				&job.OwnerUserID, &job.Attempt, &job.LeaseToken); err != nil {
				return errors.Join(fmt.Errorf("scan expired job lease: %w", err), rows.Close())
			}
			abandoned = append(abandoned, job)
		}
		if err := rows.Err(); err != nil {
			return errors.Join(fmt.Errorf("iterate expired job leases: %w", err), rows.Close())
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("close expired job leases: %w", err)
		}
		for _, job := range abandoned {
			if job.Attempt < maxAttempts {
				if _, err := tx.ExecContext(ctx, `UPDATE jobs SET state = 'queued', lease_token = NULL,
					lease_expires_at = NULL, available_at = ?, failure_code = 'worker_restarted'
					WHERE id = ? AND state = 'running' AND lease_token = ?`, instant(time.Now()), job.ID,
					job.LeaseToken); err != nil {
					return fmt.Errorf("requeue abandoned job: %w", err)
				}
				continue
			}
			if err := worker.failTerminal(ctx, tx, job, "worker_restarted"); err != nil {
				return err
			}
		}
		return nil
	})
}

func (worker *Worker) Start(parent context.Context) {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	if worker.done != nil {
		panic("job worker already started")
	}
	ctx := context.WithoutCancel(parent)
	worker.done = make(chan struct{})
	worker.stop = make(chan struct{})
	go worker.loop(ctx, worker.done)
	go func() {
		select {
		case <-parent.Done():
			worker.requestStop()
			timer := time.NewTimer(30 * time.Second)
			defer timer.Stop()
			select {
			case <-worker.done:
			case <-timer.C:
				worker.mu.Lock()
				cancel := worker.runCancel
				worker.mu.Unlock()
				if cancel != nil {
					cancel()
				}
			}
		case <-worker.done:
		}
	}()
}

func (worker *Worker) Stop(ctx context.Context) error {
	worker.mu.Lock()
	done := worker.done
	worker.mu.Unlock()
	if done == nil {
		return nil
	}
	worker.requestStop()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		worker.mu.Lock()
		cancel := worker.runCancel
		worker.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		<-done
		return ctx.Err()
	}
}

// BeginShutdown immediately prevents the worker from claiming another job while
// allowing the current job to finish until Stop's deadline expires.
func (worker *Worker) BeginShutdown() {
	worker.mu.Lock()
	started := worker.stop != nil
	worker.mu.Unlock()
	if started {
		worker.requestStop()
	}
}

func (worker *Worker) requestStop() {
	worker.claimMu.Lock()
	defer worker.claimMu.Unlock()
	worker.stopOnce.Do(func() { close(worker.stop) })
}

func (worker *Worker) loop(ctx context.Context, done chan<- struct{}) {
	defer close(done)
	for {
		job, found, err := worker.claimUnlessStopping(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			worker.logger.ErrorContext(ctx, "claim background job", "error", err)
		}
		if err != nil || !found {
			select {
			case <-worker.stop:
				return
			case <-time.After(idlePoll):
				continue
			}
		}
		worker.run(ctx, job)
	}
}

func (worker *Worker) claimUnlessStopping(ctx context.Context) (Job, bool, error) {
	worker.claimMu.Lock()
	defer worker.claimMu.Unlock()
	select {
	case <-worker.stop:
		return Job{}, false, nil
	default:
		return worker.claim(ctx)
	}
}

func (worker *Worker) claim(ctx context.Context) (Job, bool, error) {
	var job Job
	err := miSQLite.WithTx(ctx, worker.database, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `SELECT id, type, subject_type, subject_id, course_id,
			COALESCE(owner_user_id, ''), attempt_count FROM jobs WHERE state = 'queued' AND available_at <= ?
			ORDER BY available_at, created_at, id LIMIT 1`, instant(time.Now())).Scan(&job.ID, &job.Type,
			&job.SubjectType, &job.SubjectID, &job.CourseID, &job.OwnerUserID, &job.Attempt)
		if errors.Is(err, sql.ErrNoRows) {
			return sql.ErrNoRows
		}
		if err != nil {
			return fmt.Errorf("select due job: %w", err)
		}
		job.LeaseToken = uuid.NewString()
		job.Attempt++
		now := time.Now()
		result, err := tx.ExecContext(ctx, `UPDATE jobs SET state = 'running', attempt_count = ?, lease_token = ?,
			lease_expires_at = ?, started_at = COALESCE(started_at, ?) WHERE id = ? AND state = 'queued'`, job.Attempt,
			job.LeaseToken, instant(now.Add(leaseDuration)), instant(now), job.ID)
		if err != nil {
			return fmt.Errorf("claim due job: %w", err)
		}
		count, err := result.RowsAffected()
		if err != nil || count != 1 {
			if err != nil {
				return fmt.Errorf("count claimed job: %w", err)
			}
			return ErrStaleLease
		}
		return nil
	})
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, ErrStaleLease) {
		return Job{}, false, nil
	}
	return job, err == nil, err
}

func (worker *Worker) run(parent context.Context, job Job) {
	handler := worker.handlers[job.Type]
	if handler == nil {
		worker.finishFailure(context.WithoutCancel(parent), job, &Failure{Code: "job_type_unknown"})
		return
	}
	ctx, cancel := context.WithCancel(parent)
	worker.mu.Lock()
	worker.runCancel = cancel
	worker.mu.Unlock()
	defer func() {
		cancel()
		worker.mu.Lock()
		worker.runCancel = nil
		worker.mu.Unlock()
	}()
	renewDone := make(chan struct{})
	go worker.renew(ctx, job, cancel, renewDone)
	result, err := handler.Execute(ctx, job)
	cancel()
	<-renewDone
	commitCtx, commitCancel := context.WithTimeout(context.WithoutCancel(parent), 30*time.Second)
	defer commitCancel()
	if err != nil {
		worker.finishFailure(commitCtx, job, classify(err))
		return
	}
	if err := worker.finishSuccess(commitCtx, job, handler, result); err != nil && !errors.Is(err, ErrStaleLease) {
		worker.logger.ErrorContext(commitCtx, "commit background job", "job_id", job.ID, "error", err)
	}
}

func (worker *Worker) renew(ctx context.Context, job Job, cancel context.CancelFunc, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(renewInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			result, err := worker.database.ExecContext(ctx, `UPDATE jobs SET lease_expires_at = ?
				WHERE id = ? AND state = 'running' AND lease_token = ?`, instant(time.Now().Add(leaseDuration)),
				job.ID, job.LeaseToken)
			if err != nil {
				worker.logger.ErrorContext(ctx, "renew background job lease", "job_id", job.ID, "error", err)
				cancel()
				return
			}
			count, err := result.RowsAffected()
			if err != nil || count != 1 {
				cancel()
				return
			}
		}
	}
}

func (worker *Worker) finishSuccess(ctx context.Context, job Job, handler Handler, result Result) (returnErr error) {
	var finalize func(bool) error
	err := miSQLite.WithTx(ctx, worker.database, func(tx *sql.Tx) error {
		if err := requireLease(ctx, tx, job); err != nil {
			return err
		}
		var err error
		finalize, err = handler.Commit(ctx, tx, job, result)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE jobs SET state = 'succeeded', lease_token = NULL,
			lease_expires_at = NULL, failure_code = NULL, finished_at = ?,
			provider_input_units = provider_input_units + ?, provider_output_units = provider_output_units + ?
			WHERE id = ? AND state = 'running' AND lease_token = ?`, instant(time.Now()), result.ProviderInputUnits,
			result.ProviderOutputUnits, job.ID, job.LeaseToken)
		return err
	})
	if finalize != nil {
		return errors.Join(err, finalize(err == nil))
	}
	return err
}

func (worker *Worker) finishFailure(ctx context.Context, job Job, failure *Failure) {
	err := miSQLite.WithTx(ctx, worker.database, func(tx *sql.Tx) error {
		if err := requireLease(ctx, tx, job); err != nil {
			return err
		}
		if failure.Retryable && job.Attempt < maxAttempts {
			delay := time.Minute
			if job.Attempt >= 2 {
				delay = 5 * time.Minute
			}
			if failure.RetryAfter > delay {
				delay = min(failure.RetryAfter, time.Hour)
			}
			_, err := tx.ExecContext(ctx, `UPDATE jobs SET state = 'queued', lease_token = NULL,
				lease_expires_at = NULL, available_at = ?, failure_code = ?,
				provider_input_units = provider_input_units + ?, provider_output_units = provider_output_units + ?
				WHERE id = ? AND lease_token = ?`, instant(time.Now().Add(delay)), failure.Code,
				failure.ProviderInputUnits, failure.ProviderOutputUnits, job.ID, job.LeaseToken)
			return err
		}
		return worker.failTerminalWithUsage(ctx, tx, job, failure.Code, failure.ProviderInputUnits,
			failure.ProviderOutputUnits)
	})
	if err != nil && !errors.Is(err, ErrStaleLease) {
		worker.logger.ErrorContext(ctx, "fail background job", "job_id", job.ID, "error", err)
	}
}

func (worker *Worker) failTerminal(ctx context.Context, tx miSQLite.Querier, job Job, code string) error {
	return worker.failTerminalWithUsage(ctx, tx, job, code, 0, 0)
}

func (worker *Worker) failTerminalWithUsage(
	ctx context.Context,
	tx miSQLite.Querier,
	job Job,
	code string,
	inputUnits int64,
	outputUnits int64,
) error {
	handler := worker.handlers[job.Type]
	if handler != nil {
		if err := handler.TerminalFailure(ctx, tx, job, code); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `UPDATE jobs SET state = 'failed', lease_token = NULL,
		lease_expires_at = NULL, failure_code = ?, finished_at = ?,
		provider_input_units = provider_input_units + ?, provider_output_units = provider_output_units + ?
		WHERE id = ? AND state = 'running'`, code, instant(time.Now()), inputUnits, outputUnits, job.ID)
	return err
}

func requireLease(ctx context.Context, query miSQLite.Querier, job Job) error {
	var current string
	err := query.QueryRowContext(ctx, `SELECT lease_token FROM jobs WHERE id = ? AND state = 'running'
		AND lease_token = ?`, job.ID, job.LeaseToken).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrStaleLease
	}
	return err
}

func classify(err error) *Failure {
	var failure *Failure
	if errors.As(err, &failure) {
		if failure.Code == "" {
			failure.Code = "provider_failure"
		}
		return failure
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return &Failure{Code: "provider_timeout", Retryable: true, Cause: err}
	}
	return &Failure{Code: "processing_failure", Cause: err}
}

func RetryAfter(value string) time.Duration {
	seconds, err := strconv.Atoi(value)
	if err != nil || seconds < 0 {
		return 0
	}
	return min(time.Duration(seconds)*time.Second, time.Hour)
}

func CancelBySubject(ctx context.Context, query miSQLite.Querier, subjectType, subjectID string) error {
	_, err := query.ExecContext(ctx, `UPDATE jobs SET state = 'cancelled', lease_token = NULL, lease_expires_at = NULL,
		finished_at = ? WHERE subject_type = ? AND subject_id = ? AND state IN ('queued', 'running')`, instant(time.Now()),
		subjectType, subjectID)
	return err
}

func DeleteBySubject(ctx context.Context, query miSQLite.Querier, subjectType, subjectID string) error {
	_, err := query.ExecContext(ctx, "DELETE FROM jobs WHERE subject_type = ? AND subject_id = ?", subjectType, subjectID)
	return err
}

func CancelSubjects(ctx context.Context, query miSQLite.Querier, subjectIDs []string, excludeJobID string) error {
	if len(subjectIDs) == 0 {
		return nil
	}
	placeholders := make([]string, len(subjectIDs))
	args := []any{instant(time.Now())}
	for index, id := range subjectIDs {
		placeholders[index] = "?"
		args = append(args, id)
	}
	condition := ""
	if excludeJobID != "" {
		condition = " AND id != ?"
		args = append(args, excludeJobID)
	}
	_, err := query.ExecContext(ctx, `UPDATE jobs SET state = 'cancelled', lease_token = NULL,
		lease_expires_at = NULL, finished_at = ? WHERE subject_id IN (`+strings.Join(placeholders, ",")+
		`) AND state IN ('queued', 'running')`+condition, args...)
	return err
}

func DeleteSubjects(ctx context.Context, query miSQLite.Querier, subjectIDs []string) error {
	if len(subjectIDs) == 0 {
		return nil
	}
	placeholders := make([]string, len(subjectIDs))
	args := make([]any, len(subjectIDs))
	for index, id := range subjectIDs {
		placeholders[index], args[index] = "?", id
	}
	_, err := query.ExecContext(ctx, "DELETE FROM jobs WHERE subject_id IN ("+strings.Join(placeholders, ",")+")",
		args...)
	return err
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func instant(value time.Time) string { return value.UTC().Format("2006-01-02T15:04:05.000000Z") }
