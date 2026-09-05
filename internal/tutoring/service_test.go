package tutoring

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/thorstenkramm/mia/internal/jobs"
	"github.com/thorstenkramm/mia/internal/material"
	"github.com/thorstenkramm/mia/internal/provider/openai"
	miSQLite "github.com/thorstenkramm/mia/internal/sqlite"
	"github.com/thorstenkramm/mia/internal/user"
)

type tutoringFixture struct {
	database                 *sql.DB
	directory                string
	service                  *Service
	student, supervisor      string
	course, secondCourse     string
	materialID, materialFile string
}

func newTutoringFixture(t *testing.T) tutoringFixture {
	t.Helper()
	directory := t.TempDir()
	require.NoError(t, os.Chmod(directory, 0o700))
	database, err := miSQLite.Open(directory)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	ctx := context.Background()
	student, err := user.Create(ctx, database, user.CreateInput{Username: "student.one", PasswordHash: "hash",
		Language: "en", Country: "US", TimeZone: "UTC", Roles: []user.Role{user.Student}})
	require.NoError(t, err)
	supervisor, err := user.Create(ctx, database, user.CreateInput{Username: "teacher.one", Email: "teacher@example.org",
		EmailVerified: true, PasswordHash: "hash", Language: "en", Country: "US", TimeZone: "UTC",
		Roles: []user.Role{user.Supervisor, user.Student}})
	require.NoError(t, err)
	now := instant(time.Now())
	courseID := "cou_" + uuid.NewString()
	secondCourseID := "cou_" + uuid.NewString()
	for index, id := range []string{courseID, secondCourseID} {
		_, err = database.Exec(`INSERT INTO courses (id, name, name_normalized, description, curriculum, learning_goals,
			llm_instructions, language, is_active, created_at) VALUES (?, ?, ?, 'Description', 'Curriculum', 'Goals',
			'Instructions', 'en', 1, ?)`, id, "Course "+string(rune('A'+index)), "course-"+string(rune('a'+index)), now)
		require.NoError(t, err)
		_, err = database.Exec(`INSERT INTO course_students (id, course_id, student_user_id, joined_at)
			VALUES (?, ?, ?, ?)`, "cst_"+uuid.NewString(), id, student.ID, now)
		require.NoError(t, err)
		_, err = database.Exec(`INSERT INTO course_supervisors (course_id, supervisor_user_id, assigned_at)
			VALUES (?, ?, ?)`, id, supervisor.ID, now)
		require.NoError(t, err)
	}
	materialID := "mat_" + uuid.NewString()
	fileID := "mf_" + uuid.NewString()
	brief := `{"version":1,"summary":"Vocabulary","subjects":["English"],"learning_goals":[],"sections":[],"warnings":[],"educational_level":null}`
	_, err = database.Exec(`INSERT INTO materials (id, course_id, scope, name, name_normalized, kind, format, state,
		brief_json, brief_source, is_approved, created_at) VALUES (?, ?, 'course-wide', 'Book', 'book', 'text-book',
		'text', 'ready', ?, 'generated', 1, ?)`, materialID, courseID, brief, now)
	require.NoError(t, err)
	_, err = database.Exec(`INSERT INTO material_files (id, material_id, original_filename, media_type, size_bytes,
		page_count, state, created_at) VALUES (?, ?, 'book.txt', 'text/plain', 20, 0, 'processed', ?)`, fileID,
		materialID, now)
	require.NoError(t, err)
	content, err := json.Marshal(material.Segment{Version: 1, Sequence: 1, Text: "alpha alpha beta vocabulary"})
	require.NoError(t, err)
	path := filepath.Join(directory, "materials", materialID, "files", fileID, "content.jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, append(content, '\n'), 0o600))
	materialService := material.NewService(database, directory, material.Limits{}, nil, nil, nil, nil)
	service := NewService(database, materialService, nil)
	return tutoringFixture{database: database, directory: directory, service: service, student: student.ID,
		supervisor: supervisor.ID, course: courseID, secondCourse: secondCourseID, materialID: materialID,
		materialFile: fileID}
}

func TestSessionLifecycleIdempotencyQueueAndReview(t *testing.T) {
	fixture := newTutoringFixture(t)
	ctx := context.Background()
	requestID := uuid.NewString()
	session, replay, err := fixture.service.Start(ctx, StartInput{CourseID: fixture.course, StudentID: fixture.student,
		RequestID: requestID, SelectedMaterialIDs: []string{fixture.materialID}})
	require.NoError(t, err)
	assert.False(t, replay)
	replayed, replay, err := fixture.service.Start(ctx, StartInput{CourseID: fixture.course, StudentID: fixture.student,
		RequestID: requestID, SelectedMaterialIDs: []string{fixture.materialID}})
	require.NoError(t, err)
	assert.True(t, replay)
	assert.Equal(t, session.ID, replayed.ID)
	_, _, err = fixture.service.Start(ctx, StartInput{CourseID: fixture.secondCourse, StudentID: fixture.student,
		RequestID: uuid.NewString()})
	assert.ErrorIs(t, err, ErrActiveSession)
	_, _, err = fixture.service.Start(ctx, StartInput{CourseID: fixture.course, StudentID: fixture.student,
		RequestID: requestID})
	assert.ErrorIs(t, err, ErrConflict)

	messageID := uuid.NewString()
	first, err := fixture.service.Submit(ctx, SubmitInput{SessionID: session.ID, StudentID: fixture.student,
		RequestID: messageID, Content: "Help me with alpha."})
	require.NoError(t, err)
	replayedMessage, err := fixture.service.Submit(ctx, SubmitInput{SessionID: session.ID, StudentID: fixture.student,
		RequestID: messageID, Content: "Help me with alpha."})
	require.NoError(t, err)
	assert.True(t, replayedMessage.Replay)
	_, err = fixture.service.Submit(ctx, SubmitInput{SessionID: session.ID, StudentID: fixture.student,
		RequestID: messageID, Content: "Different"})
	assert.ErrorIs(t, err, ErrConflict)
	require.NoError(t, fixture.database.QueryRow(`UPDATE tutor_responses SET state = 'generating', started_at = ?
		WHERE id = ? RETURNING state`, instant(time.Now()), first.Response.ID).Scan(new(string)))
	second, err := fixture.service.Submit(ctx, SubmitInput{SessionID: session.ID, StudentID: fixture.student,
		RequestID: uuid.NewString(), Content: "A queued question"})
	require.NoError(t, err)
	assert.Equal(t, "queued", second.Response.State)
	_, err = fixture.service.Submit(ctx, SubmitInput{SessionID: session.ID, StudentID: fixture.student,
		RequestID: uuid.NewString(), Content: "Too much work"})
	assert.ErrorIs(t, err, ErrWorkBusy)
	_, _, err = fixture.service.Messages(ctx, session.ID, fixture.supervisor, ListInput{Limit: 25})
	assert.Error(t, err)
	_, err = fixture.service.Complete(ctx, session.ID, fixture.supervisor)
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = fixture.database.Exec(`UPDATE tutor_responses SET state = 'completed', finished_at = ?
		WHERE session_id = ?`, instant(time.Now()), session.ID)
	require.NoError(t, err)
	completed, err := fixture.service.Complete(ctx, session.ID, fixture.student)
	require.NoError(t, err)
	assert.Equal(t, "completed", completed.State)
	messages, _, err := fixture.service.Messages(ctx, session.ID, fixture.supervisor, ListInput{Limit: 25})
	require.NoError(t, err)
	assert.Len(t, messages, 2)
	corrected, err := fixture.service.CorrectSummary(ctx, session.ID, fixture.supervisor, "Good progress", "Review beta")
	require.NoError(t, err)
	assert.Equal(t, "supervisor", corrected.SummarySource)
	var audits int
	require.NoError(t, fixture.database.QueryRow(`SELECT COUNT(*) FROM audit_events
		WHERE action = 'tutoring.summary.corrected'`).Scan(&audits))
	assert.Equal(t, 1, audits)
	summaryHandler := NewSessionSummaryHandler(fixture.service, nil, fixture.directory)
	_, err = summaryHandler.Commit(ctx, fixture.database, jobs.Job{SubjectID: session.ID},
		jobs.Result{Value: sessionSummary{Summary: "Generated overwrite", FollowUp: "Wrong"}})
	assert.ErrorIs(t, err, jobs.ErrStaleLease)
	stillCorrected, err := fixture.service.Get(ctx, session.ID, fixture.supervisor)
	require.NoError(t, err)
	assert.Equal(t, "Good progress", stillCorrected.Summary)
	_, _, err = fixture.service.Start(ctx, StartInput{CourseID: fixture.course, StudentID: fixture.student,
		RequestID: requestID, SelectedMaterialIDs: []string{fixture.materialID}})
	assert.ErrorIs(t, err, ErrConflict)
}

func TestConcurrentMessageSubmissionsKeepOneQueuedResponse(t *testing.T) {
	fixture := newTutoringFixture(t)
	ctx := context.Background()
	session, _, err := fixture.service.Start(ctx, StartInput{CourseID: fixture.course, StudentID: fixture.student,
		RequestID: uuid.NewString()})
	require.NoError(t, err)
	initial, err := fixture.service.Submit(ctx, SubmitInput{SessionID: session.ID, StudentID: fixture.student,
		RequestID: uuid.NewString(), Content: "Initial message"})
	require.NoError(t, err)
	_, err = fixture.database.Exec(`UPDATE tutor_responses SET state = 'generating', started_at = ? WHERE id = ?`,
		instant(time.Now()), initial.Response.ID)
	require.NoError(t, err)

	start := make(chan struct{})
	ready := sync.WaitGroup{}
	ready.Add(2)
	errorsBySubmission := make(chan error, 2)
	for range 2 {
		go func() {
			ready.Done()
			<-start
			_, submitErr := fixture.service.Submit(ctx, SubmitInput{SessionID: session.ID, StudentID: fixture.student,
				RequestID: uuid.NewString(), Content: "Concurrent message"})
			errorsBySubmission <- submitErr
		}()
	}
	ready.Wait()
	close(start)

	assertOneSuccessAndOneBusy(t, errorsBySubmission)
	var queued, messages int
	require.NoError(t, fixture.database.QueryRow(`SELECT COUNT(*) FROM tutor_responses
		WHERE session_id = ? AND state = 'queued'`, session.ID).Scan(&queued))
	require.NoError(t, fixture.database.QueryRow(`SELECT COUNT(*) FROM student_messages
		WHERE tutoring_session_id = ?`, session.ID).Scan(&messages))
	assert.Equal(t, 1, queued)
	assert.Equal(t, 2, messages)
}

func TestConcurrentResponseRetriesKeepOneQueuedResponse(t *testing.T) {
	fixture := newTutoringFixture(t)
	ctx := context.Background()
	session, _, err := fixture.service.Start(ctx, StartInput{CourseID: fixture.course, StudentID: fixture.student,
		RequestID: uuid.NewString()})
	require.NoError(t, err)
	initial, err := fixture.service.Submit(ctx, SubmitInput{SessionID: session.ID, StudentID: fixture.student,
		RequestID: uuid.NewString(), Content: "Retry concurrently"})
	require.NoError(t, err)
	_, err = fixture.database.Exec(`UPDATE tutor_responses SET state = 'failed', finished_at = ? WHERE id = ?`,
		instant(time.Now()), initial.Response.ID)
	require.NoError(t, err)

	start := make(chan struct{})
	ready := sync.WaitGroup{}
	ready.Add(2)
	errorsByRetry := make(chan error, 2)
	for range 2 {
		go func() {
			ready.Done()
			<-start
			_, retryErr := fixture.service.Retry(ctx, initial.Message.ID, fixture.student)
			errorsByRetry <- retryErr
		}()
	}
	ready.Wait()
	close(start)

	assertOneSuccessAndOneBusy(t, errorsByRetry)
	var queued, attempts int
	require.NoError(t, fixture.database.QueryRow(`SELECT COUNT(*) FROM tutor_responses
		WHERE session_id = ? AND state = 'queued'`, session.ID).Scan(&queued))
	require.NoError(t, fixture.database.QueryRow(`SELECT COUNT(*) FROM tutor_responses
		WHERE student_message_id = ?`, initial.Message.ID).Scan(&attempts))
	assert.Equal(t, 1, queued)
	assert.Equal(t, 2, attempts)
}

func assertOneSuccessAndOneBusy(t *testing.T, results <-chan error) {
	t.Helper()
	var successes, busy int
	for range 2 {
		err := <-results
		if err == nil {
			successes++
		} else if errors.Is(err, ErrWorkBusy) {
			busy++
		} else {
			assert.NoError(t, err)
		}
	}
	assert.Equal(t, 1, successes)
	assert.Equal(t, 1, busy)
}

func TestStudentMessageLimits(t *testing.T) {
	tests := []struct {
		name    string
		content string
		valid   bool
	}{
		{name: "single code point", content: "a", valid: true},
		{name: "eight thousand ASCII code points", content: strings.Repeat("a", 8_000), valid: true},
		{name: "eight thousand four-byte code points", content: strings.Repeat("😀", 8_000), valid: true},
		{name: "over eight thousand code points", content: strings.Repeat("a", 8_001)},
		{name: "over thirty-two KiB", content: strings.Repeat("😀", 8_193)},
		{name: "invalid UTF-8", content: string([]byte{0xff})},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.valid, validMessage(test.content))
		})
	}
}

func TestSubscriptionDisconnectDoesNotCancelGeneration(t *testing.T) {
	fixture := newTutoringFixture(t)
	require.NoError(t, EnsureInstructions(fixture.directory))
	ctx := context.Background()
	session, _, err := fixture.service.Start(ctx, StartInput{CourseID: fixture.course, StudentID: fixture.student,
		RequestID: uuid.NewString()})
	require.NoError(t, err)
	result, err := fixture.service.Submit(ctx, SubmitInput{SessionID: session.ID, StudentID: fixture.student,
		RequestID: uuid.NewString(), Content: "Stream"})
	require.NoError(t, err)
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	manager := NewManager(fixture.database, fixture.service, &scriptedStream{mu: started, block: release},
		fixture.directory, nil)
	fixture.service.SetManager(manager)
	manager.Dispatch(result.Response.ID)
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("generation did not start")
	}
	require.Eventually(t, func() bool {
		manager.mu.Lock()
		current := manager.responses[result.Response.ID]
		manager.mu.Unlock()
		if current == nil {
			return false
		}
		current.mu.Lock()
		defer current.mu.Unlock()
		return current.response.Content == "partial"
	}, time.Second, 10*time.Millisecond)
	snapshot, subscription, err := manager.Subscribe(ctx, result.Response.ID, fixture.student)
	require.NoError(t, err)
	assert.Equal(t, "partial", snapshot.Content)
	require.NotNil(t, subscription)
	subscription.Close()
	require.Eventually(t, func() bool {
		response, loadErr := loadResponse(ctx, fixture.database, result.Response.ID)
		return loadErr == nil && response.State == "generating" && response.Content == "partial"
	}, 2*time.Second, 25*time.Millisecond)
	close(release)
	require.Eventually(t, func() bool {
		response, loadErr := loadResponse(ctx, fixture.database, result.Response.ID)
		return loadErr == nil && response.State == "completed" && response.Content == "partial"
	}, 3*time.Second, 10*time.Millisecond)
}

func TestAuthorizedRetrievalAndActiveSelection(t *testing.T) {
	fixture := newTutoringFixture(t)
	require.NoError(t, EnsureInstructions(fixture.directory))
	ctx := context.Background()
	hits, err := fixture.service.materials.SearchForTutor(ctx, fixture.course, fixture.student, []string{"ALPHA"}, 8)
	require.NoError(t, err)
	require.Len(t, hits, 1)
	assert.Equal(t, fixture.materialID, hits[0].MaterialID)
	excerpt, err := fixture.service.materials.ExcerptForTutor(ctx, fixture.course, fixture.student, fixture.materialID,
		fixture.materialFile, 1)
	require.NoError(t, err)
	assert.Contains(t, excerpt.Text, "alpha")
	_, err = fixture.service.materials.SearchForTutor(ctx, fixture.course, fixture.supervisor, []string{"alpha"}, 8)
	require.NoError(t, err)
	session, _, err := fixture.service.Start(ctx, StartInput{CourseID: fixture.course, StudentID: fixture.student,
		RequestID: uuid.NewString(), SelectedMaterialIDs: []string{fixture.materialID}})
	require.NoError(t, err)
	selected, err := fixture.service.MaterialSelectedByActive(ctx, fixture.database, fixture.materialID)
	require.NoError(t, err)
	assert.True(t, selected)
	_, err = fixture.database.Exec(`UPDATE materials SET is_approved = 0 WHERE id = ?`, fixture.materialID)
	require.NoError(t, err)
	_, err = fixture.service.materials.ExcerptForTutor(ctx, fixture.course, fixture.student, fixture.materialID,
		fixture.materialFile, 1)
	assert.ErrorIs(t, err, material.ErrNotFound)
	message, err := fixture.service.Submit(ctx, SubmitInput{SessionID: session.ID, StudentID: fixture.student,
		RequestID: uuid.NewString(), Content: "Continue without the revoked source"})
	require.NoError(t, err)
	request, used, err := NewManager(fixture.database, fixture.service, nil, fixture.directory, nil).
		buildRequest(ctx, message.Response)
	require.NoError(t, err)
	assert.Empty(t, used)
	assert.NotContains(t, request.Instructions, fixture.materialID)
	assert.NotEmpty(t, session.ID)
}

func TestTutorExcerptEnforcesCodePointAndByteBounds(t *testing.T) {
	fixture := newTutoringFixture(t)
	segment, err := json.Marshal(material.Segment{Version: 1, Sequence: 1, Text: strings.Repeat("界", 6_000)})
	require.NoError(t, err)
	target, err := json.Marshal(material.Segment{Version: 1, Sequence: 2, Text: "target"})
	require.NoError(t, err)
	path := filepath.Join(fixture.directory, "materials", fixture.materialID, "files", fixture.materialFile,
		"content.jsonl")
	content := append(append(append([]byte(nil), segment...), '\n'), target...)
	require.NoError(t, os.WriteFile(path, append(content, '\n'), 0o600))
	excerpt, err := fixture.service.materials.ExcerptForTutor(context.Background(), fixture.course, fixture.student,
		fixture.materialID, fixture.materialFile, 1)
	require.NoError(t, err)
	assert.LessOrEqual(t, utf8.RuneCountInString(excerpt.Text), 4_000)
	assert.LessOrEqual(t, len(excerpt.Text), 16<<10)
	targetExcerpt, err := fixture.service.materials.ExcerptForTutor(context.Background(), fixture.course,
		fixture.student, fixture.materialID, fixture.materialFile, 2)
	require.NoError(t, err)
	assert.Equal(t, "target", targetExcerpt.Text)
}

func TestConcurrentStartsKeepOneActiveSession(t *testing.T) {
	fixture := newTutoringFixture(t)
	ctx := context.Background()
	start := make(chan struct{})
	errorsByRequest := make(chan error, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, _, err := fixture.service.Start(ctx, StartInput{CourseID: fixture.course, StudentID: fixture.student,
				RequestID: uuid.NewString()})
			errorsByRequest <- err
		}()
	}
	close(start)
	wait.Wait()
	close(errorsByRequest)
	var succeeded, conflicted int
	for err := range errorsByRequest {
		if err == nil {
			succeeded++
		} else if errors.Is(err, ErrActiveSession) {
			conflicted++
		} else {
			t.Fatalf("unexpected concurrent start error: %v", err)
		}
	}
	assert.Equal(t, 1, succeeded)
	assert.Equal(t, 1, conflicted)
	var active int
	require.NoError(t, fixture.database.QueryRow(`SELECT COUNT(*) FROM tutoring_sessions
		WHERE student_user_id = ? AND state = 'active'`, fixture.student).Scan(&active))
	assert.Equal(t, 1, active)
}

func TestToolRetrievalRecordsOnlyDeliveredNonOverlappingMaterial(t *testing.T) {
	fixture := newTutoringFixture(t)
	ctx := context.Background()
	session, _, err := fixture.service.Start(ctx, StartInput{CourseID: fixture.course, StudentID: fixture.student,
		RequestID: uuid.NewString()})
	require.NoError(t, err)
	message, err := fixture.service.Submit(ctx, SubmitInput{SessionID: session.ID, StudentID: fixture.student,
		RequestID: uuid.NewString(), Content: "Find alpha"})
	require.NoError(t, err)
	manager := NewManager(fixture.database, fixture.service, nil, fixture.directory, nil)
	delivery := &materialDelivery{manager: manager, response: message.Response, pending: make(map[string]bool)}
	execute := manager.toolExecutor(message.Response, delivery.queue)
	search, err := execute(ctx, openai.ToolCall{Name: "search_material", Arguments: `{"terms":["ALPHA"]}`})
	require.NoError(t, err)
	assert.Contains(t, search, fixture.materialID)
	var used int
	require.NoError(t, fixture.database.QueryRow(`SELECT COUNT(*) FROM material_retrievals`).Scan(&used))
	assert.Zero(t, used)
	arguments := `{"material_id":"` + fixture.materialID + `","file_id":"` + fixture.materialFile + `","sequence":1}`
	excerpt, err := execute(ctx, openai.ToolCall{Name: "get_excerpt", Arguments: arguments})
	require.NoError(t, err)
	assert.Contains(t, excerpt, "alpha")
	require.NoError(t, delivery.accepted(ctx))
	require.NoError(t, fixture.database.QueryRow(`SELECT COUNT(*) FROM material_retrievals`).Scan(&used))
	assert.Equal(t, 1, used)
	materials, _, err := fixture.service.Materials(ctx, session.ID, fixture.student, ListInput{Limit: 25})
	require.NoError(t, err)
	require.Len(t, materials, 1)
	assert.Equal(t, fixture.materialID, materials[0].ID)
	_, _, err = fixture.service.Materials(ctx, session.ID, fixture.supervisor, ListInput{Limit: 25})
	assert.ErrorIs(t, err, ErrNotFound)
	duplicate, err := execute(ctx, openai.ToolCall{Name: "get_excerpt", Arguments: arguments})
	require.NoError(t, err)
	assert.JSONEq(t, `{"duplicate":true}`, duplicate)
	require.NoError(t, fixture.database.QueryRow(`SELECT COUNT(*) FROM material_retrievals`).Scan(&used))
	assert.Equal(t, 1, used)
	_, err = execute(ctx, openai.ToolCall{Name: "search_material", Arguments: `{"terms":["alpha"],"extra":true}`})
	assert.ErrorIs(t, err, ErrInvalid)
}

func TestDuplicateDispatchCallsProviderOnce(t *testing.T) {
	fixture := newTutoringFixture(t)
	require.NoError(t, EnsureInstructions(fixture.directory))
	ctx := context.Background()
	session, _, err := fixture.service.Start(ctx, StartInput{CourseID: fixture.course, StudentID: fixture.student,
		RequestID: uuid.NewString()})
	require.NoError(t, err)
	result, err := fixture.service.Submit(ctx, SubmitInput{SessionID: session.ID, StudentID: fixture.student,
		RequestID: uuid.NewString(), Content: "Only once"})
	require.NoError(t, err)
	stream := &countingStream{}
	manager := NewManager(fixture.database, fixture.service, stream, fixture.directory, nil)
	manager.Dispatch(result.Response.ID)
	manager.Dispatch(result.Response.ID)
	require.Eventually(t, func() bool {
		response, loadErr := loadResponse(ctx, fixture.database, result.Response.ID)
		return loadErr == nil && response.State == "completed"
	}, 3*time.Second, 10*time.Millisecond)
	assert.Equal(t, int32(1), stream.calls.Load())
}

type countingStream struct{ calls atomic.Int32 }

func (fake *countingStream) Stream(_ context.Context, _ openai.ChatRequest, delta func(string) error,
	_ openai.ToolExecutor) (openai.StreamResult, error) {
	fake.calls.Add(1)
	return openai.StreamResult{}, delta("once")
}

func TestMessagesReturnRetryHistoryWithoutDuplicateMessages(t *testing.T) {
	fixture := newTutoringFixture(t)
	require.NoError(t, EnsureInstructions(fixture.directory))
	ctx := context.Background()
	session, _, err := fixture.service.Start(ctx, StartInput{CourseID: fixture.course, StudentID: fixture.student,
		RequestID: uuid.NewString()})
	require.NoError(t, err)
	result, err := fixture.service.Submit(ctx, SubmitInput{SessionID: session.ID, StudentID: fixture.student,
		RequestID: uuid.NewString(), Content: "Retry this"})
	require.NoError(t, err)
	_, err = fixture.database.Exec(`UPDATE tutor_responses SET state = 'failed', finished_at = ? WHERE id = ?`,
		instant(time.Now()), result.Response.ID)
	require.NoError(t, err)
	retry, err := fixture.service.Retry(ctx, result.Message.ID, fixture.student)
	require.NoError(t, err)
	values, _, err := fixture.service.Messages(ctx, session.ID, fixture.student, ListInput{Limit: 25})
	require.NoError(t, err)
	require.Len(t, values, 2)
	assert.Equal(t, result.Response.ID, values[0].Response.ID)
	assert.Equal(t, retry.ID, values[1].Response.ID)
	assert.Equal(t, result.Response.ID, values[1].Response.RetryOfID)
	manager := NewManager(fixture.database, fixture.service, nil, fixture.directory, nil)
	request, _, err := manager.buildRequest(ctx, retry)
	require.NoError(t, err)
	require.NotEmpty(t, request.Messages)
	assert.Equal(t, "user", request.Messages[len(request.Messages)-1].Role)
	assert.Equal(t, "Retry this", request.Messages[len(request.Messages)-1].Content)
}

type scriptedStream struct {
	mu    chan struct{}
	fail  bool
	block <-chan struct{}
}

func (fake *scriptedStream) Stream(ctx context.Context, _ openai.ChatRequest, delta func(string) error,
	_ openai.ToolExecutor) (openai.StreamResult, error) {
	if fake.mu != nil {
		fake.mu <- struct{}{}
	}
	if err := delta("partial"); err != nil {
		return openai.StreamResult{}, err
	}
	if fake.block != nil {
		select {
		case <-ctx.Done():
			return openai.StreamResult{}, ctx.Err()
		case <-fake.block:
		}
	}
	if fake.fail {
		return openai.StreamResult{InputTokens: 2, OutputTokens: 1}, errors.New("provider failed")
	}
	return openai.StreamResult{InputTokens: 2, OutputTokens: 1}, nil
}

func TestManagerPreservesPartialFailureAndRecovery(t *testing.T) {
	fixture := newTutoringFixture(t)
	require.NoError(t, EnsureInstructions(fixture.directory))
	ctx := context.Background()
	session, _, err := fixture.service.Start(ctx, StartInput{CourseID: fixture.course, StudentID: fixture.student,
		RequestID: uuid.NewString()})
	require.NoError(t, err)
	result, err := fixture.service.Submit(ctx, SubmitInput{SessionID: session.ID, StudentID: fixture.student,
		RequestID: uuid.NewString(), Content: "Start"})
	require.NoError(t, err)
	manager := NewManager(fixture.database, fixture.service, &scriptedStream{fail: true}, fixture.directory, nil)
	fixture.service.SetManager(manager)
	manager.Dispatch(result.Response.ID)
	require.Eventually(t, func() bool {
		response, loadErr := loadResponse(ctx, fixture.database, result.Response.ID)
		return loadErr == nil && response.State == "failed" && response.Content == "partial"
	}, 3*time.Second, 10*time.Millisecond)
	retry, err := fixture.service.Retry(ctx, result.Message.ID, fixture.student)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		response, loadErr := loadResponse(ctx, fixture.database, retry.ID)
		return loadErr == nil && response.State == "failed"
	}, 3*time.Second, 10*time.Millisecond)

	_, err = fixture.database.Exec(`UPDATE tutor_responses SET state = 'generating', failure_code = NULL,
		finished_at = NULL WHERE id = ?`, retry.ID)
	require.NoError(t, err)
	recovery := NewManager(fixture.database, fixture.service, &scriptedStream{}, fixture.directory, nil)
	require.NoError(t, recovery.Recover(ctx))
	recovered, err := loadResponse(ctx, fixture.database, retry.ID)
	require.NoError(t, err)
	assert.Equal(t, "failed", recovered.State)
	assert.Equal(t, "worker_restarted", recovered.FailureCode)
}

func TestRecoveryFailsUncertainWorkAndResumesQueuedWork(t *testing.T) {
	fixture := newTutoringFixture(t)
	require.NoError(t, EnsureInstructions(fixture.directory))
	ctx := context.Background()
	session, _, err := fixture.service.Start(ctx, StartInput{CourseID: fixture.course, StudentID: fixture.student,
		RequestID: uuid.NewString()})
	require.NoError(t, err)
	first, err := fixture.service.Submit(ctx, SubmitInput{SessionID: session.ID, StudentID: fixture.student,
		RequestID: uuid.NewString(), Content: "uncertain"})
	require.NoError(t, err)
	_, err = fixture.database.Exec(`UPDATE tutor_responses SET state = 'generating', started_at = ? WHERE id = ?`,
		instant(time.Now()), first.Response.ID)
	require.NoError(t, err)
	second, err := fixture.service.Submit(ctx, SubmitInput{SessionID: session.ID, StudentID: fixture.student,
		RequestID: uuid.NewString(), Content: "never started"})
	require.NoError(t, err)
	stream := &countingStream{}
	manager := NewManager(fixture.database, fixture.service, stream, fixture.directory, nil)
	fixture.service.SetManager(manager)
	require.NoError(t, manager.Recover(ctx))
	require.Eventually(t, func() bool {
		response, loadErr := loadResponse(ctx, fixture.database, second.Response.ID)
		return loadErr == nil && response.State == "completed"
	}, 3*time.Second, 10*time.Millisecond)
	recovered, err := loadResponse(ctx, fixture.database, first.Response.ID)
	require.NoError(t, err)
	assert.Equal(t, "failed", recovered.State)
	assert.Equal(t, "worker_restarted", recovered.FailureCode)
	assert.Equal(t, int32(1), stream.calls.Load())
}

func TestShutdownCancelsAndFailsGeneratingResponseAfterDeadline(t *testing.T) {
	fixture := newTutoringFixture(t)
	require.NoError(t, EnsureInstructions(fixture.directory))
	ctx := context.Background()
	session, _, err := fixture.service.Start(ctx, StartInput{CourseID: fixture.course, StudentID: fixture.student,
		RequestID: uuid.NewString()})
	require.NoError(t, err)
	result, err := fixture.service.Submit(ctx, SubmitInput{SessionID: session.ID, StudentID: fixture.student,
		RequestID: uuid.NewString(), Content: "shutdown"})
	require.NoError(t, err)
	started := make(chan struct{}, 1)
	manager := NewManager(fixture.database, fixture.service, &scriptedStream{mu: started, block: make(chan struct{})},
		fixture.directory, nil)
	manager.Dispatch(result.Response.ID)
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("generation did not start")
	}
	shutdown, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	assert.ErrorIs(t, manager.Stop(shutdown), context.DeadlineExceeded)
	response, err := loadResponse(ctx, fixture.database, result.Response.ID)
	require.NoError(t, err)
	assert.Equal(t, "failed", response.State)
	assert.Equal(t, "server_shutdown", response.FailureCode)
}

func TestQueuedResponseCanBeInterruptedWithoutProviderCall(t *testing.T) {
	fixture := newTutoringFixture(t)
	ctx := context.Background()
	session, _, err := fixture.service.Start(ctx, StartInput{CourseID: fixture.course, StudentID: fixture.student,
		RequestID: uuid.NewString()})
	require.NoError(t, err)
	result, err := fixture.service.Submit(ctx, SubmitInput{SessionID: session.ID, StudentID: fixture.student,
		RequestID: uuid.NewString(), Content: "cancel queued"})
	require.NoError(t, err)
	manager := NewManager(fixture.database, fixture.service, nil, fixture.directory, nil)
	fixture.service.SetManager(manager)
	_, subscription, err := manager.Subscribe(ctx, result.Response.ID, fixture.student)
	require.NoError(t, err)
	require.NotNil(t, subscription)
	require.NoError(t, fixture.service.Interrupt(ctx, result.Response.ID, fixture.student))
	event, ok := subscription.Next(ctx)
	assert.True(t, ok)
	assert.Equal(t, "interrupted", event.Type)
	response, err := loadResponse(ctx, fixture.database, result.Response.ID)
	require.NoError(t, err)
	assert.Equal(t, "interrupted", response.State)
}

type summaryClient struct{ input string }

func (fake *summaryClient) Structured(_ context.Context, _ string, input []string,
	_ map[string]any) (openai.Result, error) {
	fake.input += strings.Join(input, "\n")
	return openai.Result{JSON: []byte(`{"summary":"Strength","follow_up":"Next step"}`), InputTokens: 4,
		OutputTokens: 2}, nil
}

func TestSummaryUsesCompleteRetainedTranscriptAndCommitsGuardedly(t *testing.T) {
	fixture := newTutoringFixture(t)
	require.NoError(t, EnsureInstructions(fixture.directory))
	ctx := context.Background()
	session, _, err := fixture.service.Start(ctx, StartInput{CourseID: fixture.course, StudentID: fixture.student,
		RequestID: uuid.NewString()})
	require.NoError(t, err)
	message, err := fixture.service.Submit(ctx, SubmitInput{SessionID: session.ID, StudentID: fixture.student,
		RequestID: uuid.NewString(), Content: "Explain alpha"})
	require.NoError(t, err)
	_, err = fixture.database.Exec(`UPDATE tutor_responses SET state = 'completed', content = 'Alpha explanation',
		finished_at = ? WHERE id = ?`, instant(time.Now()), message.Response.ID)
	require.NoError(t, err)
	_, err = fixture.service.Complete(ctx, session.ID, fixture.student)
	require.NoError(t, err)
	client := &summaryClient{}
	handler := NewSessionSummaryHandler(fixture.service, client, fixture.directory)
	result, err := handler.Execute(ctx, jobs.Job{SubjectID: session.ID})
	require.NoError(t, err)
	assert.Contains(t, client.input, "Explain alpha")
	assert.Contains(t, client.input, "Alpha explanation")
	_, err = handler.Commit(ctx, fixture.database, jobs.Job{SubjectID: session.ID}, result)
	require.NoError(t, err)
	completed, err := fixture.service.Get(ctx, session.ID, fixture.student)
	require.NoError(t, err)
	assert.Equal(t, "Strength", completed.Summary)
	assert.Equal(t, "generated", completed.SummarySource)
}
