package speech

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	miSQLite "github.com/thorstenkramm/mia/internal/sqlite"
	"github.com/thorstenkramm/mia/internal/user"
)

type fakeSynthesizer struct {
	calls atomic.Int32
	mu    sync.Mutex
	gate  chan struct{}
	data  []byte
	err   error
}

func (fake *fakeSynthesizer) Available() bool { return true }
func (fake *fakeSynthesizer) Generate(ctx context.Context, _, _ string) ([]byte, error) {
	fake.calls.Add(1)
	fake.mu.Lock()
	gate, data, err := fake.gate, append([]byte(nil), fake.data...), fake.err
	fake.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, context.Cause(ctx)
		}
	}
	return data, err
}

func (fake *fakeSynthesizer) result(data []byte, err error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.data, fake.err = append([]byte(nil), data...), err
}

type speechFixture struct {
	database            *sql.DB
	directory, student  string
	response, otherUser string
}

func newSpeechFixture(t *testing.T) speechFixture {
	t.Helper()
	directory := t.TempDir()
	require.NoError(t, os.Chmod(directory, 0o700))
	database, err := miSQLite.Open(directory)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	ctx := context.Background()
	student, err := user.Create(ctx, database, user.CreateInput{Username: "speech.student", PasswordHash: "hash",
		Language: "en", Country: "US", TimeZone: "UTC", Roles: []user.Role{user.Student}})
	require.NoError(t, err)
	other, err := user.Create(ctx, database, user.CreateInput{Username: "other.student", PasswordHash: "hash",
		Language: "en", Country: "US", TimeZone: "UTC", Roles: []user.Role{user.Student}})
	require.NoError(t, err)
	_, err = database.Exec("UPDATE users SET tts_voice = 'voice-1' WHERE id = ?", student.ID)
	require.NoError(t, err)
	now := instant(time.Now())
	courseID := "cou_" + uuid.NewString()
	_, err = database.Exec(`INSERT INTO courses (id, name, name_normalized, language, created_at)
		VALUES (?, 'Speech', 'speech', 'en', ?)`, courseID, now)
	require.NoError(t, err)
	_, err = database.Exec(`INSERT INTO course_students (id, course_id, student_user_id, joined_at)
		VALUES (?, ?, ?, ?)`, "cst_"+uuid.NewString(), courseID, student.ID, now)
	require.NoError(t, err)
	sessionID, messageID, responseID := "ts_"+uuid.NewString(), "msg_"+uuid.NewString(), "rsp_"+uuid.NewString()
	_, err = database.Exec(`INSERT INTO tutoring_sessions
		(id, course_id, student_user_id, state, client_request_id, request_digest, started_at, last_activity_at,
		 completed_at, completed_by) VALUES (?, ?, ?, 'completed', ?, zeroblob(32), ?, ?, ?, ?)`,
		sessionID, courseID, student.ID, uuid.NewString(), now, now, now, student.ID)
	require.NoError(t, err)
	_, err = database.Exec(`INSERT INTO student_messages
		(id, tutoring_session_id, sequence, content, client_request_id, request_digest, created_at)
		VALUES (?, ?, 1, 'question', ?, zeroblob(32), ?)`, messageID, sessionID, uuid.NewString(), now)
	require.NoError(t, err)
	_, err = database.Exec(`INSERT INTO tutor_responses
		(id, session_id, student_message_id, attempt, state, content, created_at, started_at, finished_at)
		VALUES (?, ?, ?, 1, 'completed', 'answer', ?, ?, ?)`, responseID, sessionID, messageID, now, now, now)
	require.NoError(t, err)
	return speechFixture{database: database, directory: directory, student: student.ID,
		response: responseID, otherUser: other.ID}
}

func TestConcurrentRequestsShareGenerationAndCache(t *testing.T) {
	fixture := newSpeechFixture(t)
	provider := &fakeSynthesizer{gate: make(chan struct{}), data: []byte("ID3audio")}
	service := NewService(fixture.database, fixture.directory, provider, 30, nil)
	t.Cleanup(func() { require.NoError(t, service.Stop(context.Background())) })

	var values [2]Speech
	var errs [2]error
	var wait sync.WaitGroup
	for index := range values {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			values[index], errs[index] = service.Request(context.Background(), fixture.response, fixture.student)
		}(index)
	}
	wait.Wait()
	require.NoError(t, errs[0])
	require.NoError(t, errs[1])
	assert.Equal(t, values[0].ID, values[1].ID)
	require.Eventually(t, func() bool { return provider.calls.Load() == 1 }, time.Second, 10*time.Millisecond)
	close(provider.gate)
	require.Eventually(t, func() bool {
		value, err := service.Get(context.Background(), fixture.response, fixture.student)
		return err == nil && value.State == "available"
	}, time.Second, 10*time.Millisecond)
	audio, err := service.ReadAudio(context.Background(), fixture.response, fixture.student)
	require.NoError(t, err)
	assert.Equal(t, []byte("ID3audio"), audio.Data)
	var expiryBefore, expiryAfter string
	require.NoError(t, fixture.database.QueryRow("SELECT expires_at FROM generated_speech WHERE id = ?", values[0].ID).
		Scan(&expiryBefore))
	_, err = service.ReadAudio(context.Background(), fixture.response, fixture.student)
	require.NoError(t, err)
	require.NoError(t, fixture.database.QueryRow("SELECT expires_at FROM generated_speech WHERE id = ?", values[0].ID).
		Scan(&expiryAfter))
	assert.Equal(t, expiryBefore, expiryAfter)
}

func TestRequestEligibilityProviderVoiceAndOwnership(t *testing.T) {
	fixture := newSpeechFixture(t)
	configured := NewService(fixture.database, fixture.directory, &fakeSynthesizer{data: []byte("ID3audio")}, 30, nil)
	t.Cleanup(func() { require.NoError(t, configured.Stop(context.Background())) })
	_, err := configured.Request(context.Background(), fixture.response, fixture.otherUser)
	assert.ErrorIs(t, err, ErrNotFound)
	var denials int
	require.NoError(t, fixture.database.QueryRow(`SELECT COUNT(*) FROM audit_events
		WHERE action = 'speech.mutation.denied' AND actor_user_id = ?`, fixture.otherUser).Scan(&denials))
	assert.Equal(t, 1, denials)
	_, err = fixture.database.Exec("UPDATE users SET tts_voice = NULL WHERE id = ?", fixture.student)
	require.NoError(t, err)
	_, err = configured.Request(context.Background(), fixture.response, fixture.student)
	assert.ErrorIs(t, err, ErrVoice)
	unavailable := NewService(fixture.database, fixture.directory, nil, 30, nil)
	_, err = unavailable.Request(context.Background(), fixture.response, fixture.student)
	assert.ErrorIs(t, err, ErrUnavailable)
}

func TestFailedGenerationRetriesOnlyOnExplicitRequest(t *testing.T) {
	fixture := newSpeechFixture(t)
	provider := &fakeSynthesizer{err: errors.New("outage")}
	service := NewService(fixture.database, fixture.directory, provider, 30, nil)
	t.Cleanup(func() { require.NoError(t, service.Stop(context.Background())) })
	first, err := service.Request(context.Background(), fixture.response, fixture.student)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		value, getErr := service.Get(context.Background(), fixture.response, fixture.student)
		return getErr == nil && value.State == "failed"
	}, time.Second, 10*time.Millisecond)
	assert.Equal(t, int32(1), provider.calls.Load())
	time.Sleep(30 * time.Millisecond)
	assert.Equal(t, int32(1), provider.calls.Load())
	provider.result([]byte("ID3retry"), nil)
	retried, err := service.Request(context.Background(), fixture.response, fixture.student)
	require.NoError(t, err)
	assert.Equal(t, first.ID, retried.ID)
	require.Eventually(t, func() bool { return provider.calls.Load() == 2 }, time.Second, 10*time.Millisecond)
}

func TestReconcileExpiryStrandingIntegrityAndOrphans(t *testing.T) {
	fixture := newSpeechFixture(t)
	service := NewService(fixture.database, fixture.directory, &fakeSynthesizer{}, 30, nil)
	cache := filepath.Join(fixture.directory, "tts-cache")
	require.NoError(t, os.MkdirAll(cache, 0o700))
	now := time.Now()
	insertSpeech := func(id, state string, generated, expires any, failure any) {
		_, err := fixture.database.Exec(`INSERT INTO generated_speech
			(id, tutor_response_id, voice_id, source_content_hash, state, generated_at, expires_at, failure_code)
			VALUES (?, ?, ?, randomblob(32), ?, ?, ?, ?)`, id, fixture.response, id, state, generated, expires, failure)
		require.NoError(t, err)
	}
	expiredID, strandedID := "sp_"+uuid.NewString(), "sp_"+uuid.NewString()
	insertSpeech(expiredID, "available", instant(now.Add(-48*time.Hour)), instant(now.Add(-24*time.Hour)), nil)
	insertSpeech(strandedID, "generating", nil, nil, nil)
	require.NoError(t, os.WriteFile(filepath.Join(cache, expiredID+".mp3"), []byte("ID3old"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(cache, strandedID+".mp3"), []byte("ID3partial"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(cache, "orphan.mp3"), []byte("ID3orphan"), 0o600))
	require.NoError(t, service.RecoverState(context.Background()))
	require.NoError(t, service.ValidateFiles(context.Background()))
	assert.FileExists(t, filepath.Join(cache, "orphan.mp3"))
	require.NoError(t, service.ReconcileOrphans(context.Background()))
	assert.NoFileExists(t, filepath.Join(cache, expiredID+".mp3"))
	assert.NoFileExists(t, filepath.Join(cache, strandedID+".mp3"))
	assert.NoFileExists(t, filepath.Join(cache, "orphan.mp3"))
	var state, code string
	require.NoError(t, fixture.database.QueryRow("SELECT state, failure_code FROM generated_speech WHERE id = ?",
		strandedID).Scan(&state, &code))
	assert.Equal(t, "failed", state)
	assert.Equal(t, "speech_restart_interrupted", code)

	missingID := "sp_" + uuid.NewString()
	insertSpeech(missingID, "available", instant(now), instant(now.Add(time.Hour)), nil)
	err := service.Reconcile(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), missingID)
}

func TestLifecycleDeletionRemovesRowsThenOrphanedFiles(t *testing.T) {
	fixture := newSpeechFixture(t)
	service := NewService(fixture.database, fixture.directory,
		&fakeSynthesizer{data: []byte("ID3audio")}, 30, nil)
	t.Cleanup(func() { require.NoError(t, service.Stop(context.Background())) })
	created, err := service.Request(context.Background(), fixture.response, fixture.student)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		value, getErr := service.Get(context.Background(), fixture.response, fixture.student)
		return getErr == nil && value.State == "available"
	}, time.Second, 10*time.Millisecond)
	path := filepath.Join(fixture.directory, "tts-cache", created.ID+".mp3")
	assert.FileExists(t, path)
	require.NoError(t, miSQLite.WithTx(context.Background(), fixture.database, func(tx *sql.Tx) error {
		return service.DeleteAccountData(context.Background(), tx, fixture.student)
	}))
	require.NoError(t, service.CleanupAccountData(context.Background(), fixture.student))
	assert.NoFileExists(t, path)
	var count int
	require.NoError(t, fixture.database.QueryRow("SELECT COUNT(*) FROM generated_speech WHERE id = ?", created.ID).
		Scan(&count))
	assert.Zero(t, count)
}

func TestExpiredSpeechIsDeletedAndRegeneratedWithNewIdentity(t *testing.T) {
	fixture := newSpeechFixture(t)
	provider := &fakeSynthesizer{data: []byte("ID3audio")}
	service := NewService(fixture.database, fixture.directory, provider, 30, nil)
	t.Cleanup(func() { require.NoError(t, service.Stop(context.Background())) })
	first, err := service.Request(context.Background(), fixture.response, fixture.student)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		value, getErr := service.Get(context.Background(), fixture.response, fixture.student)
		return getErr == nil && value.State == "available"
	}, time.Second, 10*time.Millisecond)
	oldPath := filepath.Join(fixture.directory, "tts-cache", first.ID+".mp3")
	then := time.Now().Add(-48 * time.Hour)
	_, err = fixture.database.Exec("UPDATE generated_speech SET generated_at = ?, expires_at = ? WHERE id = ?",
		instant(then), instant(then.Add(24*time.Hour)), first.ID)
	require.NoError(t, err)
	second, err := service.Request(context.Background(), fixture.response, fixture.student)
	require.NoError(t, err)
	assert.NotEqual(t, first.ID, second.ID)
	assert.NoFileExists(t, oldPath)
	require.Eventually(t, func() bool { return provider.calls.Load() == 2 }, time.Second, 10*time.Millisecond)
}
