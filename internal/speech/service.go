package speech

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/thorstenkramm/mia/internal/audit"
	"github.com/thorstenkramm/mia/internal/filepublish"
	"github.com/thorstenkramm/mia/internal/httpserver"
	"github.com/thorstenkramm/mia/internal/provider/elevenlabs"
	miSQLite "github.com/thorstenkramm/mia/internal/sqlite"
	"github.com/thorstenkramm/mia/internal/tutoring"
	"github.com/thorstenkramm/mia/internal/user"
)

const (
	storedInstant = "2006-01-02T15:04:05.000000Z"
	maximumMP3    = 25 << 20
)

// Service is safe for concurrent use. One database transition wins each cache
// identity, so concurrent requests share the winning generation goroutine.
type Service struct {
	database  *sql.DB
	dataDir   string
	provider  elevenlabs.Synthesizer
	retention time.Duration
	logger    *slog.Logger
	ctx       context.Context
	cancel    context.CancelFunc
	mu        sync.Mutex
	requestMu sync.Mutex
	stopping  bool
	workers   sync.WaitGroup
}

func NewService(database *sql.DB, dataDir string, provider elevenlabs.Synthesizer, retentionDays int,
	logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{database: database, dataDir: dataDir, provider: provider,
		retention: time.Duration(retentionDays) * 24 * time.Hour, logger: logger, ctx: ctx, cancel: cancel}
}

func (service *Service) available() bool {
	return service.provider != nil && service.provider.Available()
}

func (service *Service) Request(ctx context.Context, responseID, studentID string) (Speech, error) {
	if !service.available() {
		return Speech{}, ErrUnavailable
	}
	service.mu.Lock()
	stopping := service.stopping
	service.mu.Unlock()
	if stopping {
		return Speech{}, ErrUnavailable
	}
	source, voice, hash, err := service.source(ctx, responseID, studentID)
	if err != nil {
		if errors.Is(err, ErrNotFound) || errors.Is(err, ErrVoice) {
			auditErr := miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
				return audit.WriteWithMetadata(ctx, tx, audit.ActionSpeechMutationDenied, studentID, "",
					audit.Metadata{TutorResponseID: responseID, OutcomeCode: denialCode(err)})
			})
			return Speech{}, errors.Join(err, auditErr)
		}
		return Speech{}, err
	}
	service.requestMu.Lock()
	defer service.requestMu.Unlock()
	var result Speech
	var dispatch bool
	var expiredID string
	err = miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		current, loadErr := loadVariant(ctx, tx, source.ID, hash[:], voice)
		if loadErr == nil {
			if current.State == "available" && current.ExpiresAt != nil && !current.ExpiresAt.After(time.Now()) {
				if _, err := tx.ExecContext(ctx, "DELETE FROM generated_speech WHERE id = ?", current.ID); err != nil {
					return fmt.Errorf("delete expired generated speech: %w", err)
				}
				expiredID = current.ID
			} else if current.State == "failed" {
				updated, err := tx.ExecContext(ctx, `UPDATE generated_speech SET state = 'generating', generated_at = NULL,
					expires_at = NULL, failure_code = NULL WHERE id = ? AND state = 'failed'`, current.ID)
				if err != nil {
					return fmt.Errorf("retry generated speech: %w", err)
				}
				count, err := updated.RowsAffected()
				if err != nil {
					return err
				}
				if count == 1 {
					current.State, current.FailureCode, current.GeneratedAt, current.ExpiresAt = "generating", "", nil, nil
					dispatch = true
				}
				result = current
				return nil
			} else {
				result = current
				return nil
			}
		} else if !errors.Is(loadErr, sql.ErrNoRows) {
			return loadErr
		}
		result = Speech{ID: "sp_" + uuid.NewString(), ResponseID: source.ID, VoiceID: voice,
			State: "generating"}
		_, err := tx.ExecContext(ctx, `INSERT INTO generated_speech
			(id, tutor_response_id, voice_id, source_content_hash, state) VALUES (?, ?, ?, ?, 'generating')`,
			result.ID, result.ResponseID, result.VoiceID, hash[:])
		if err != nil {
			return fmt.Errorf("insert generated speech: %w", err)
		}
		dispatch = true
		return nil
	})
	if err != nil {
		return Speech{}, err
	}
	if expiredID != "" {
		service.removeFile(ctx, expiredID)
	}
	if dispatch {
		service.dispatch(result.ID, source.Content, voice)
	}
	return result, nil
}

func (service *Service) Get(ctx context.Context, responseID, studentID string) (Speech, error) {
	if !service.available() {
		return Speech{}, ErrUnavailable
	}
	source, voice, hash, err := service.source(ctx, responseID, studentID)
	if err != nil {
		return Speech{}, err
	}
	value, err := loadVariant(ctx, service.database, source.ID, hash[:], voice)
	if errors.Is(err, sql.ErrNoRows) {
		return Speech{}, ErrNotFound
	}
	if err != nil {
		return Speech{}, err
	}
	if value.State == "available" && value.ExpiresAt != nil && !value.ExpiresAt.After(time.Now()) {
		if err := service.expire(ctx, value.ID); err != nil {
			return Speech{}, err
		}
		return Speech{}, ErrNotFound
	}
	return value, nil
}

func (service *Service) ReadAudio(ctx context.Context, responseID, studentID string) (Audio, error) {
	value, err := service.Get(ctx, responseID, studentID)
	if err != nil {
		return Audio{}, err
	}
	if value.State != "available" {
		return Audio{}, ErrInvalid
	}
	file, err := os.Open(service.path(value.ID))
	if err != nil {
		return Audio{}, fmt.Errorf("open generated speech: %w", err)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maximumMP3+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		return Audio{}, errors.Join(readErr, closeErr)
	}
	if len(data) > maximumMP3 || !validMP3(data) {
		return Audio{}, errors.New("stored generated speech is invalid")
	}
	return Audio{Speech: value, Data: data}, nil
}

func (service *Service) source(ctx context.Context, responseID, studentID string) (
	tutoring.CompletedResponse, string, [32]byte, error,
) {
	source, err := tutoring.LoadCompletedResponseForOwner(ctx, service.database, responseID, studentID)
	if err != nil {
		if errors.Is(err, tutoring.ErrNotFound) {
			return tutoring.CompletedResponse{}, "", [32]byte{}, ErrNotFound
		}
		return tutoring.CompletedResponse{}, "", [32]byte{}, err
	}
	voice, err := user.LoadTTSVoice(ctx, service.database, studentID)
	if err != nil {
		return tutoring.CompletedResponse{}, "", [32]byte{}, err
	}
	if voice == nil || !validVoice(*voice) {
		return tutoring.CompletedResponse{}, "", [32]byte{}, ErrVoice
	}
	return source, *voice, sha256.Sum256([]byte(source.Content)), nil
}

func validVoice(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for index := range len(value) {
		if value[index] < 0x20 || value[index] > 0x7e {
			return false
		}
	}
	return true
}

func denialCode(err error) string {
	if errors.Is(err, ErrVoice) {
		return httpserver.StableCode(httpserver.CodeSpeechVoiceRequired)
	}
	return httpserver.StableCode(httpserver.CodeSpeechNotFound)
}

func (service *Service) dispatch(id, text, voice string) {
	service.mu.Lock()
	if service.stopping {
		service.mu.Unlock()
		if err := service.fail(context.Background(), id,
			httpserver.StableCode(httpserver.CodeSpeechShutdown)); err != nil {
			service.logger.Error("fail speech during shutdown", "speech_id", id, "error", err)
		}
		service.removeFile(context.Background(), id)
		return
	}
	service.workers.Add(1)
	service.mu.Unlock()
	go func() {
		defer service.workers.Done()
		data, err := service.provider.Generate(service.ctx, text, voice)
		if err != nil {
			if failErr := service.fail(context.Background(), id,
				httpserver.StableCode(httpserver.CodeSpeechProviderFailed)); failErr != nil {
				service.logger.Error("record speech provider failure", "speech_id", id, "error", failErr)
			}
			service.removeFile(context.Background(), id)
			return
		}
		if err := service.publish(context.Background(), id, data); err != nil {
			service.logger.Error("publish generated speech", "speech_id", id, "error", err)
			service.removeFile(context.Background(), id)
		}
	}()
}

func (service *Service) publish(ctx context.Context, id string, data []byte) error {
	change, err := filepublish.Replace(service.path(id), data)
	if err != nil {
		return errors.Join(err, service.fail(ctx, id,
			httpserver.StableCode(httpserver.CodeSpeechPublicationFailed)))
	}
	commit := false
	defer func() {
		if finishErr := change.Finish(commit); finishErr != nil {
			service.logger.Error("finish generated speech publication", "speech_id", id, "error", finishErr)
		}
	}()
	now := time.Now()
	err = miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE generated_speech SET state = 'available', generated_at = ?,
			expires_at = ?, failure_code = NULL WHERE id = ? AND state = 'generating'`, instant(now),
			instant(now.Add(service.retention)), id)
		if err != nil {
			return fmt.Errorf("mark generated speech available: %w", err)
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count != 1 {
			return ErrNotFound
		}
		return nil
	})
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			err = errors.Join(err, service.fail(ctx, id,
				httpserver.StableCode(httpserver.CodeSpeechCommitFailed)))
		}
		return err
	}
	commit = true
	return nil
}

func (service *Service) fail(ctx context.Context, id, code string) error {
	return miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE generated_speech SET state = 'failed', generated_at = NULL,
			expires_at = NULL, failure_code = ? WHERE id = ? AND state = 'generating'`, code, id)
		if err != nil {
			return fmt.Errorf("mark generated speech failed: %w", err)
		}
		count, err := result.RowsAffected()
		if err != nil || count == 0 {
			return err
		}
		return audit.WriteWithMetadata(ctx, tx, audit.ActionSpeechGenerationFailed, "", "",
			audit.Metadata{SpeechID: id, OutcomeCode: code})
	})
}

func (service *Service) Stop(ctx context.Context) error {
	service.BeginShutdown()
	done := make(chan struct{})
	go func() {
		service.workers.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("stop speech generation: %w", context.Cause(ctx))
	}
}

// BeginShutdown prevents new generation and cancels provider work.
func (service *Service) BeginShutdown() {
	service.mu.Lock()
	if !service.stopping {
		service.stopping = true
		service.cancel()
	}
	service.mu.Unlock()
}

// RecoverState removes expired speech and fails uncertain generation without contacting ElevenLabs.
func (service *Service) RecoverState(ctx context.Context) error {
	if err := os.MkdirAll(service.cacheDir(), 0o700); err != nil {
		return fmt.Errorf("create speech cache: %w", err)
	}
	if err := os.Chmod(service.cacheDir(), 0o700); err != nil {
		return fmt.Errorf("secure speech cache: %w", err)
	}
	if err := service.cleanupExpired(ctx); err != nil {
		return err
	}
	return service.failStranded(ctx)
}

// ValidateFiles enforces available-file integrity without mutating cache orphans.
func (service *Service) ValidateFiles(ctx context.Context) error {
	return service.validateAvailable(ctx)
}

// ReconcileOrphans removes speech files without database rows after all
// required-file validation has succeeded.
func (service *Service) ReconcileOrphans(ctx context.Context) error {
	return service.reconcileOrphans(ctx)
}

// Reconcile executes the complete speech-specific startup order.
func (service *Service) Reconcile(ctx context.Context) error {
	if err := service.RecoverState(ctx); err != nil {
		return err
	}
	if err := service.ValidateFiles(ctx); err != nil {
		return err
	}
	return service.ReconcileOrphans(ctx)
}

func (service *Service) cleanupExpired(ctx context.Context) error {
	rows, err := service.database.QueryContext(ctx, `SELECT id FROM generated_speech
		WHERE state = 'available' AND expires_at <= ?`, instant(time.Now()))
	if err != nil {
		return fmt.Errorf("list expired speech: %w", err)
	}
	ids, err := scanIDs(rows)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := service.expire(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

func (service *Service) expire(ctx context.Context, id string) error {
	if _, err := service.database.ExecContext(ctx, `DELETE FROM generated_speech WHERE id = ?
		AND state = 'available' AND expires_at <= ?`, id, instant(time.Now())); err != nil {
		return fmt.Errorf("delete expired speech: %w", err)
	}
	service.removeFile(ctx, id)
	return nil
}

func (service *Service) failStranded(ctx context.Context) error {
	rows, err := service.database.QueryContext(ctx, "SELECT id FROM generated_speech WHERE state = 'generating'")
	if err != nil {
		return fmt.Errorf("list stranded speech: %w", err)
	}
	ids, err := scanIDs(rows)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := service.fail(ctx, id,
			httpserver.StableCode(httpserver.CodeSpeechRestartInterrupted)); err != nil {
			return err
		}
		service.removeFile(ctx, id)
	}
	return nil
}

func (service *Service) validateAvailable(ctx context.Context) error {
	rows, err := service.database.QueryContext(ctx, "SELECT id FROM generated_speech WHERE state = 'available'")
	if err != nil {
		return fmt.Errorf("list available speech: %w", err)
	}
	ids, err := scanIDs(rows)
	if err != nil {
		return err
	}
	for _, id := range ids {
		info, err := os.Lstat(service.path(id))
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("available generated speech file is missing or invalid: %s", id)
		}
	}
	return nil
}

func (service *Service) reconcileOrphans(ctx context.Context) error {
	rows, err := service.database.QueryContext(ctx,
		"SELECT id FROM generated_speech WHERE state IN ('available', 'generating')")
	if err != nil {
		return fmt.Errorf("list retained speech: %w", err)
	}
	ids, err := scanIDs(rows)
	if err != nil {
		return err
	}
	retained := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		retained[id+".mp3"] = struct{}{}
	}
	entries, err := os.ReadDir(service.cacheDir())
	if err != nil {
		return fmt.Errorf("read speech cache: %w", err)
	}
	for _, entry := range entries {
		if _, ok := retained[entry.Name()]; ok {
			continue
		}
		if err := os.RemoveAll(filepath.Join(service.cacheDir(), entry.Name())); err != nil {
			service.logger.WarnContext(ctx, "remove orphaned speech cache entry", "name", entry.Name(), "error", err)
		}
	}
	return nil
}

func scanIDs(rows *sql.Rows) ([]string, error) {
	ids, err := miSQLite.ScanStrings(rows)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		if !validSpeechID(id) {
			return nil, errors.New("invalid persisted generated speech identifier")
		}
	}
	return ids, nil
}

func loadVariant(ctx context.Context, query miSQLite.Querier, responseID string, hash []byte,
	voice string) (Speech, error) {
	var value Speech
	var generated, expires sql.NullString
	err := query.QueryRowContext(ctx, `SELECT id, tutor_response_id, voice_id, state, COALESCE(failure_code, ''),
		generated_at, expires_at FROM generated_speech WHERE tutor_response_id = ? AND source_content_hash = ?
		AND voice_id = ?`, responseID, hash, voice).Scan(&value.ID, &value.ResponseID, &value.VoiceID, &value.State,
		&value.FailureCode, &generated, &expires)
	if err != nil {
		return Speech{}, err
	}
	if !validSpeechID(value.ID) {
		return Speech{}, errors.New("invalid persisted generated speech identifier")
	}
	value.GeneratedAt, err = parseOptional(generated)
	if err == nil {
		value.ExpiresAt, err = parseOptional(expires)
	}
	return value, err
}

func parseOptional(value sql.NullString) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}
	parsed, err := time.Parse(storedInstant, value.String)
	if err != nil {
		return nil, fmt.Errorf("parse generated speech instant: %w", err)
	}
	return &parsed, nil
}

func (service *Service) deleteResponses(ctx context.Context, query miSQLite.Querier, ids []string) error {
	for _, id := range ids {
		if _, err := query.ExecContext(ctx, "DELETE FROM generated_speech WHERE tutor_response_id = ?", id); err != nil {
			return fmt.Errorf("delete response speech: %w", err)
		}
	}
	return nil
}

func (service *Service) DeleteCourseData(ctx context.Context, query miSQLite.Querier, courseID string) error {
	ids, err := tutoring.ResponseIDsForCourse(ctx, query, courseID)
	if err != nil {
		return err
	}
	return service.deleteResponses(ctx, query, ids)
}

func (service *Service) DeleteStudentCourseData(ctx context.Context, query miSQLite.Querier, courseID,
	studentID string) error {
	ids, err := tutoring.ResponseIDsForStudentCourse(ctx, query, courseID, studentID)
	if err != nil {
		return err
	}
	return service.deleteResponses(ctx, query, ids)
}

func (service *Service) DeleteAccountData(ctx context.Context, query miSQLite.Querier, accountID string) error {
	ids, err := tutoring.ResponseIDsForAccount(ctx, query, accountID)
	if err != nil {
		return err
	}
	return service.deleteResponses(ctx, query, ids)
}

func (service *Service) CleanupCourseData(ctx context.Context, _ string) error {
	return service.reconcileOrphans(ctx)
}
func (service *Service) CleanupStudentCourseData(ctx context.Context, _, _ string) error {
	return service.reconcileOrphans(ctx)
}
func (service *Service) CleanupAccountData(ctx context.Context, _ string) error {
	return service.reconcileOrphans(ctx)
}

func (service *Service) removeFile(ctx context.Context, id string) {
	if err := os.Remove(service.path(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		service.logger.WarnContext(ctx, "remove generated speech file", "speech_id", id, "error", err)
	}
}

func (service *Service) cacheDir() string { return filepath.Join(service.dataDir, "tts-cache") }
func (service *Service) path(id string) string {
	return filepath.Join(service.cacheDir(), id+".mp3")
}
func instant(value time.Time) string { return value.UTC().Format(storedInstant) }

func validMP3(data []byte) bool {
	return len(data) >= 3 && string(data[:3]) == "ID3" ||
		len(data) >= 2 && data[0] == 0xff && data[1]&0xe0 == 0xe0 && data[1]&0x06 != 0
}

func validSpeechID(value string) bool {
	if !strings.HasPrefix(value, "sp_") {
		return false
	}
	id := strings.TrimPrefix(value, "sp_")
	parsed, err := uuid.Parse(id)
	return err == nil && parsed.Version() == 4 && parsed.Variant() == uuid.RFC4122 && parsed.String() == id
}
