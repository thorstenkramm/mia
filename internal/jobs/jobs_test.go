package jobs

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	miSQLite "github.com/thorstenkramm/mia/internal/sqlite"
)

type retryHandler struct {
	executions, commits, terminal int
}

func (handler *retryHandler) Execute(context.Context, Job) (Result, error) {
	handler.executions++
	if handler.executions < 3 {
		return Result{}, &Failure{Code: "temporary", Retryable: true, ProviderInputUnits: 2}
	}
	return Result{Value: "ok", ProviderInputUnits: 3, ProviderOutputUnits: 1}, nil
}

func (handler *retryHandler) Commit(context.Context, miSQLite.Querier, Job, Result) (func(bool) error, error) {
	handler.commits++
	return nil, nil
}

func (handler *retryHandler) TerminalFailure(context.Context, miSQLite.Querier, Job, string) error {
	handler.terminal++
	return nil
}

func TestTransientFailureRetriesThreeAttemptsAndAccumulatesUsage(t *testing.T) {
	database, courseID := jobsDatabase(t)
	handler := &retryHandler{}
	worker := New(database, nil)
	worker.Register("material-summary", handler)
	if _, err := Enqueue(context.Background(), database, "material-summary", "material", "mat_test", courseID, ""); err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= 3; attempt++ {
		if _, err := database.Exec("UPDATE jobs SET available_at = ? WHERE state = 'queued'", instant(time.Now())); err != nil {
			t.Fatal(err)
		}
		job, found, err := worker.claim(context.Background())
		if err != nil || !found {
			t.Fatalf("claim %d = %#v, %v, %v", attempt, job, found, err)
		}
		worker.run(context.Background(), job)
	}
	var state string
	var attempts, input, output int
	if err := database.QueryRow(`SELECT state, attempt_count, provider_input_units, provider_output_units FROM jobs`).
		Scan(&state, &attempts, &input, &output); err != nil {
		t.Fatal(err)
	}
	if state != "succeeded" || attempts != 3 || input != 7 || output != 1 || handler.commits != 1 ||
		handler.terminal != 0 {
		t.Fatalf("job state=%s attempts=%d usage=%d/%d handler=%#v", state, attempts, input, output, handler)
	}
}

func TestStaleLeaseCannotCommit(t *testing.T) {
	database, courseID := jobsDatabase(t)
	handler := &retryHandler{executions: 2}
	worker := New(database, nil)
	worker.Register("material-summary", handler)
	if _, err := Enqueue(context.Background(), database, "material-summary", "material", "mat_test", courseID, ""); err != nil {
		t.Fatal(err)
	}
	job, found, err := worker.claim(context.Background())
	if err != nil || !found {
		t.Fatal(err)
	}
	if _, err := database.Exec("UPDATE jobs SET lease_token = ? WHERE id = ?", "replacement", job.ID); err != nil {
		t.Fatal(err)
	}
	err = worker.finishSuccess(context.Background(), job, handler, Result{Value: "ok"})
	if !errors.Is(err, ErrStaleLease) || handler.commits != 0 {
		t.Fatalf("stale commit error=%v commits=%d", err, handler.commits)
	}
}

func TestPermanentFailureHasNoRetryAndRunsTerminalTransition(t *testing.T) {
	database, courseID := jobsDatabase(t)
	handler := &retryHandler{executions: -10}
	worker := New(database, nil)
	worker.Register("material-summary", handler)
	if _, err := Enqueue(context.Background(), database, "material-summary", "material", "mat_test", courseID, ""); err != nil {
		t.Fatal(err)
	}
	job, found, err := worker.claim(context.Background())
	if err != nil || !found {
		t.Fatal(err)
	}
	worker.finishFailure(context.Background(), job, &Failure{Code: "malformed", Retryable: false})
	var state string
	if err := database.QueryRow("SELECT state FROM jobs WHERE id = ?", job.ID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "failed" || handler.terminal != 1 {
		t.Fatalf("permanent state=%s terminal=%d", state, handler.terminal)
	}
}

func TestStartupRecoveryRequeuesRunningJobBeforeLeaseExpiry(t *testing.T) {
	database, courseID := jobsDatabase(t)
	worker := New(database, nil)
	worker.Register("material-summary", &retryHandler{})
	jobID, err := Enqueue(context.Background(), database, "material-summary", "material", "mat_test", courseID, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE jobs SET state = 'running', attempt_count = 1, lease_token = 'old',
		lease_expires_at = ? WHERE id = ?`, instant(time.Now().Add(time.Hour)), jobID); err != nil {
		t.Fatal(err)
	}
	if err := worker.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	var state, failureCode string
	if err := database.QueryRow("SELECT state, failure_code FROM jobs WHERE id = ?", jobID).
		Scan(&state, &failureCode); err != nil {
		t.Fatal(err)
	}
	if state != "queued" || failureCode != "worker_restarted" {
		t.Fatalf("recovered state=%s failure_code=%s", state, failureCode)
	}
}

func TestBeginShutdownBetweenPollAndClaimDoesNotLeaseQueuedJob(t *testing.T) {
	database, courseID := jobsDatabase(t)
	worker := New(database, nil)
	jobID, err := Enqueue(context.Background(), database, "material-summary", "material", "mat_test", courseID, "")
	if err != nil {
		t.Fatal(err)
	}
	worker.mu.Lock()
	worker.done = make(chan struct{})
	worker.stop = make(chan struct{})
	worker.mu.Unlock()

	// Reproduce shutdown beginning after a polling iteration decided to claim.
	select {
	case <-worker.stop:
		t.Fatal("worker unexpectedly stopping before shutdown")
	default:
	}
	worker.BeginShutdown()
	if job, found, err := worker.claimUnlessStopping(context.Background()); err != nil || found {
		t.Fatalf("claim after shutdown = %#v, %v, %v", job, found, err)
	}

	var state string
	var attempts int
	if err := database.QueryRow("SELECT state, attempt_count FROM jobs WHERE id = ?", jobID).
		Scan(&state, &attempts); err != nil {
		t.Fatal(err)
	}
	if state != "queued" || attempts != 0 {
		t.Fatalf("job state=%s attempts=%d", state, attempts)
	}
}

func TestOversightSeparatesAdministratorDiagnosticsFromSafeSubjectState(t *testing.T) {
	database, courseID := jobsDatabase(t)
	jobID, err := Enqueue(context.Background(), database, "material-summary", "material", "mat_test", courseID, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE jobs SET failure_code = 'provider_detail', provider_input_units = 17,
		provider_output_units = 5 WHERE id = ?`, jobID); err != nil {
		t.Fatal(err)
	}
	oversight := NewOversight(database, func(_ context.Context, _ miSQLite.Querier, actorID string) (bool, error) {
		return actorID == "admin", nil
	})
	if _, err := oversight.List(context.Background(), "supervisor", 25, 0); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("non-administrator list error = %v", err)
	}
	adminRecords, err := oversight.List(context.Background(), "admin", 25, 0)
	if err != nil || len(adminRecords.Items) != 1 || adminRecords.Items[0].FailureCode != "provider_detail" ||
		adminRecords.Items[0].ProviderInputUnits != 17 {
		t.Fatalf("administrator records = %#v, %v", adminRecords, err)
	}
	safeRecords, err := oversight.ListSubjectIDs(context.Background(), []string{"mat_test"}, 25, 0)
	if err != nil || len(safeRecords.Items) != 1 || safeRecords.Items[0].FailureCode != "" ||
		safeRecords.Items[0].ProviderInputUnits != 0 || safeRecords.Items[0].ProviderOutputUnits != 0 {
		t.Fatalf("safe subject records = %#v, %v", safeRecords, err)
	}
}

func jobsDatabase(t *testing.T) (*sql.DB, string) {
	t.Helper()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	database, err := miSQLite.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	now := instant(time.Now())
	if _, err := database.Exec(`INSERT INTO users
		(id, username, username_key, password_hash, preferred_language, country, time_zone, created_at)
		VALUES ('u_test', 'test', 'test', 'hash', 'en', 'US', 'UTC', ?)`, now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO courses(id, name, name_normalized, created_at, created_by)
		VALUES ('cou_test', 'Test', 'test', ?, 'u_test')`, now); err != nil {
		t.Fatal(err)
	}
	return database, "cou_test"
}
