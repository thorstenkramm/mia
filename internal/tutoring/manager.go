package tutoring

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/thorstenkramm/mia/internal/course"
	"github.com/thorstenkramm/mia/internal/material"
	"github.com/thorstenkramm/mia/internal/provider/openai"
	"github.com/thorstenkramm/mia/internal/user"
	"github.com/tiktoken-go/tokenizer"
)

const defaultTutorInstructions = `You are MIA, a supportive educational tutor. Use only the supplied course context and
authorized material tools, and retrieve material only after the student's learning intent is clear. Ask clarifying
questions, try alternative explanations, examples, and guided exercises.
Do not suggest mentoring unless supplied context explicitly says that mentoring is available; no model output can grant
that permission. If a message suggests immediate danger, self-harm, abuse, or another safeguarding concern, respond
supportively and direct the student to a trusted person or appropriate local emergency service. Never claim that anyone
was notified or is monitoring the conversation. MIA is not an emergency service.`

type streamClient interface {
	Stream(context.Context, openai.ChatRequest, func(string) error, openai.ToolExecutor) (openai.StreamResult, error)
}

type streamMetadata interface {
	ProviderModel() (string, string)
}

type managedResponse struct {
	mu          sync.Mutex
	response    Response
	cancel      context.CancelFunc
	subscribers map[*Subscription]struct{}
	stale       bool
	lastPersist time.Time
	persisted   int
}

type materialDelivery struct {
	manager  *Manager
	response Response
	pending  map[string]bool
}

// Manager is safe for concurrent dispatch, interruption, subscription, and shutdown.
type Manager struct {
	database  *sql.DB
	service   *Service
	client    streamClient
	dataDir   string
	logger    *slog.Logger
	mu        sync.Mutex
	responses map[string]*managedResponse
	stopping  bool
	wg        sync.WaitGroup
}

// Subscription is one bounded SSE subscriber. It is safe for one reader and concurrent closure.
type Subscription struct {
	manager *managedResponse
	events  chan Event
	mu      sync.Mutex
	bytes   int
	closed  bool
}

func NewManager(database *sql.DB, service *Service, client streamClient, dataDir string,
	logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	return &Manager{database: database, service: service, client: client, dataDir: dataDir, logger: logger,
		responses: make(map[string]*managedResponse)}
}

func EnsureInstructions(dataDir string) error {
	paths := map[string]string{
		filepath.Join(dataDir, "llm-instructions", "init.md"):                             defaultTutorInstructions,
		filepath.Join(dataDir, "llm-instructions", "jobs", "tutoring-session-summary.md"): "Summarize the complete tutoring session with grounded strengths, weaknesses, and suggested next steps.",
	}
	for path, content := range paths {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return fmt.Errorf("create tutoring instruction directory: %w", err)
		}
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("create tutoring instruction file: %w", err)
		}
		if _, err := file.WriteString(content + "\n"); err != nil {
			return errors.Join(err, file.Close())
		}
		if err := file.Sync(); err != nil {
			return errors.Join(err, file.Close())
		}
		if err := file.Close(); err != nil {
			return err
		}
	}
	return nil
}

// Recover safely fails uncertain provider work and resumes never-started responses.
func (manager *Manager) Recover(ctx context.Context) error {
	if _, err := manager.database.ExecContext(ctx, `UPDATE tutor_responses SET state = 'failed',
		failure_code = 'worker_restarted', finished_at = ? WHERE state = 'generating'`, instant(time.Now())); err != nil {
		return fmt.Errorf("fail stranded tutor responses: %w", err)
	}
	rows, err := manager.database.QueryContext(ctx, `SELECT id FROM tutor_responses WHERE state = 'queued'
		ORDER BY created_at, id`)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return errors.Join(err, rows.Close())
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return errors.Join(err, rows.Close())
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, id := range ids {
		manager.Dispatch(id)
	}
	return nil
}

func (manager *Manager) Dispatch(responseID string) {
	manager.mu.Lock()
	if manager.stopping {
		manager.mu.Unlock()
		return
	}
	manager.wg.Add(1)
	manager.mu.Unlock()
	go func() {
		defer manager.wg.Done()
		manager.run(responseID)
	}()
}

func (manager *Manager) run(responseID string) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	response, err := loadResponse(ctx, manager.database, responseID)
	if err != nil || response.State != "queued" {
		return
	}
	var provider, model string
	if metadata, ok := manager.client.(streamMetadata); ok {
		provider, model = metadata.ProviderModel()
	}
	now := time.Now()
	result, err := manager.database.ExecContext(ctx, `UPDATE tutor_responses SET state = 'generating', started_at = ?,
		provider = ?, model = ?
		WHERE id = ? AND state = 'queued' AND NOT EXISTS(SELECT 1 FROM tutor_responses other
		WHERE other.session_id = tutor_responses.session_id AND other.state = 'generating')`, instant(now),
		nullable(provider), nullable(model), responseID)
	if err != nil {
		manager.logger.Error("claim tutor response", "response_id", responseID, "error", err)
		return
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		return
	}
	response.State, response.StartedAt = "generating", &now
	current := manager.managed(response)
	current.mu.Lock()
	current.response.State, current.response.StartedAt, current.cancel, current.lastPersist = "generating", &now, cancel, now
	manager.broadcastLocked(current, Event{Type: "started", State: "generating"})
	current.mu.Unlock()
	request, initiallyUsed, err := manager.buildRequest(ctx, response)
	if err != nil {
		manager.fail(current, "tutor_input_invalid", err)
		return
	}
	delivery := &materialDelivery{manager: manager, response: response, pending: make(map[string]bool)}
	for _, materialID := range initiallyUsed {
		delivery.queue(materialID)
	}
	request.OnRequestAccepted = delivery.accepted
	executor := manager.toolExecutor(response, delivery.queue)
	if manager.client == nil {
		manager.fail(current, "openai_unavailable", errors.New("OpenAI tutor client is not configured"))
		return
	}
	persistStop := make(chan struct{})
	persistDone := make(chan struct{})
	go manager.persistPeriodically(ctx, current, persistStop, persistDone)
	usage, err := manager.client.Stream(ctx, request, func(text string) error {
		return manager.appendDelta(current, text)
	}, executor)
	close(persistStop)
	<-persistDone
	if err != nil {
		current.mu.Lock()
		interrupted := current.response.State == "interrupted" || current.stale
		current.mu.Unlock()
		if !interrupted {
			manager.mu.Lock()
			stopping := manager.stopping
			manager.mu.Unlock()
			code := "provider_failure"
			if stopping {
				code = "server_shutdown"
			}
			manager.failWithUsage(current, code, usage, err)
		}
		return
	}
	manager.finish(current, "completed", "", usage)
}

func (manager *Manager) persistPeriodically(ctx context.Context, current *managedResponse, stop <-chan struct{},
	done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-ticker.C:
			current.mu.Lock()
			if !current.stale && current.response.State == "generating" &&
				len(current.response.Content) > current.persisted {
				if err := manager.persistLocked(current); err != nil {
					manager.logger.Error("persist tutor response", "response_id", current.response.ID, "error", err)
					current.stale = true
					manager.closeSubscribersLocked(current)
					if current.cancel != nil {
						current.cancel()
					}
				}
			}
			current.mu.Unlock()
		}
	}
}

func (manager *Manager) appendDelta(current *managedResponse, text string) error {
	if text == "" {
		return nil
	}
	current.mu.Lock()
	defer current.mu.Unlock()
	if current.stale || current.response.State != "generating" {
		return context.Canceled
	}
	current.response.Content += text
	manager.broadcastLocked(current, Event{Type: "delta", Text: text})
	if len(current.response.Content)-current.persisted >= 16<<10 || time.Since(current.lastPersist) >= time.Second {
		return manager.persistLocked(current)
	}
	return nil
}

func (manager *Manager) persistLocked(current *managedResponse) error {
	result, err := manager.database.Exec(`UPDATE tutor_responses SET content = ? WHERE id = ? AND state = 'generating'`,
		current.response.Content, current.response.ID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		current.stale = true
		manager.closeSubscribersLocked(current)
		return context.Canceled
	}
	current.persisted, current.lastPersist = len(current.response.Content), time.Now()
	return nil
}

func (manager *Manager) finish(current *managedResponse, state, code string, usage openai.StreamResult) {
	current.mu.Lock()
	if current.stale || current.response.State != "generating" {
		current.mu.Unlock()
		return
	}
	result, err := manager.database.Exec(`UPDATE tutor_responses SET content = ?, state = ?, failure_code = ?,
		input_units = input_units + ?, output_units = output_units + ?, finished_at = ?
		WHERE id = ? AND state = 'generating'`, current.response.Content, state, nullable(code), usage.InputTokens,
		usage.OutputTokens, instant(time.Now()), current.response.ID)
	if err != nil {
		manager.logger.Error("commit tutor response", "response_id", current.response.ID, "error", err)
		current.response.State, current.response.FailureCode = "failed", "storage_failure"
		manager.broadcastLocked(current, Event{Type: "failed", State: "failed", Code: "storage_failure"})
		manager.closeSubscribersLocked(current)
		current.mu.Unlock()
		return
	}
	count, err := result.RowsAffected()
	if err != nil || count == 0 {
		current.stale = true
		manager.closeSubscribersLocked(current)
		current.mu.Unlock()
		return
	}
	current.response.State, current.response.FailureCode = state, code
	manager.broadcastLocked(current, Event{Type: state, State: state, Code: code})
	manager.closeSubscribersLocked(current)
	current.mu.Unlock()
	manager.release(current)
	go manager.dispatchNext(current.response.SessionID)
}

func (manager *Manager) fail(current *managedResponse, code string, err error) {
	manager.failWithUsage(current, code, openai.StreamResult{}, err)
}

func (manager *Manager) failWithUsage(current *managedResponse, code string, usage openai.StreamResult, err error) {
	manager.logger.Warn("tutor response failed", "response_id", current.response.ID, "code", code, "error", err)
	manager.finish(current, "failed", code, usage)
}

func (manager *Manager) dispatchNext(sessionID string) {
	manager.mu.Lock()
	stopping := manager.stopping
	manager.mu.Unlock()
	if stopping {
		return
	}
	var id string
	err := manager.database.QueryRow(`SELECT id FROM tutor_responses WHERE session_id = ? AND state = 'queued'
		ORDER BY created_at, id LIMIT 1`, sessionID).Scan(&id)
	if err == nil {
		manager.Dispatch(id)
	}
}

func (manager *Manager) managed(response Response) *managedResponse {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if current := manager.responses[response.ID]; current != nil {
		return current
	}
	current := &managedResponse{response: response, subscribers: make(map[*Subscription]struct{}),
		persisted: len(response.Content), lastPersist: time.Now()}
	manager.responses[response.ID] = current
	return current
}

func (manager *Manager) Interrupt(responseID string) error {
	manager.mu.Lock()
	current := manager.responses[responseID]
	manager.mu.Unlock()
	if current == nil {
		return ErrInvalidState
	}
	current.mu.Lock()
	if current.response.State != "generating" {
		current.mu.Unlock()
		return ErrInvalidState
	}
	result, err := manager.database.Exec(`UPDATE tutor_responses SET content = ?, state = 'interrupted', finished_at = ?
		WHERE id = ? AND state = 'generating'`, current.response.Content, instant(time.Now()), responseID)
	if err != nil {
		current.mu.Unlock()
		return err
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		current.mu.Unlock()
		return ErrInvalidState
	}
	current.response.State = "interrupted"
	if current.cancel != nil {
		current.cancel()
	}
	manager.broadcastLocked(current, Event{Type: "interrupted", State: "interrupted"})
	manager.closeSubscribersLocked(current)
	current.mu.Unlock()
	manager.release(current)
	go manager.dispatchNext(current.response.SessionID)
	return nil
}

func (manager *Manager) queuedInterrupted(responseID string) {
	manager.mu.Lock()
	current := manager.responses[responseID]
	manager.mu.Unlock()
	if current == nil {
		return
	}
	current.mu.Lock()
	if current.response.State == "queued" {
		current.response.State = "interrupted"
		manager.broadcastLocked(current, Event{Type: "interrupted", State: "interrupted"})
		manager.closeSubscribersLocked(current)
	}
	current.mu.Unlock()
	manager.release(current)
}

func (manager *Manager) release(current *managedResponse) {
	manager.mu.Lock()
	if manager.responses[current.response.ID] == current {
		delete(manager.responses, current.response.ID)
	}
	manager.mu.Unlock()
}

func (manager *Manager) Subscribe(ctx context.Context, responseID, actorID string) (Event, *Subscription, error) {
	response, err := loadResponseScopedOwner(ctx, manager.database, responseID, actorID)
	if err != nil {
		return Event{}, nil, err
	}
	manager.mu.Lock()
	current := manager.responses[responseID]
	if current == nil {
		response, err = loadResponseScopedOwner(ctx, manager.database, responseID, actorID)
		if err == nil && (response.State == "queued" || response.State == "generating") {
			current = &managedResponse{response: response, subscribers: make(map[*Subscription]struct{}),
				persisted: len(response.Content), lastPersist: time.Now()}
			manager.responses[response.ID] = current
		}
	}
	manager.mu.Unlock()
	if err != nil {
		return Event{}, nil, err
	}
	if current == nil {
		return Event{Type: "snapshot", Content: response.Content, State: response.State,
			Code: response.FailureCode}, nil, nil
	}
	current.mu.Lock()
	defer current.mu.Unlock()
	snapshot := Event{Type: "snapshot", Content: current.response.Content, State: current.response.State,
		Code: current.response.FailureCode}
	if current.response.State != "queued" && current.response.State != "generating" {
		return snapshot, nil, nil
	}
	subscription := &Subscription{manager: current, events: make(chan Event, 64)}
	current.subscribers[subscription] = struct{}{}
	return snapshot, subscription, nil
}

func (subscription *Subscription) Next(ctx context.Context) (Event, bool) {
	select {
	case <-ctx.Done():
		return Event{}, false
	case event, ok := <-subscription.events:
		if ok {
			subscription.mu.Lock()
			subscription.bytes -= eventSize(event)
			subscription.mu.Unlock()
		}
		return event, ok
	}
}

func (subscription *Subscription) Close() {
	current := subscription.manager
	current.mu.Lock()
	defer current.mu.Unlock()
	if _, exists := current.subscribers[subscription]; exists {
		delete(current.subscribers, subscription)
		subscription.close()
	}
}

func (subscription *Subscription) close() {
	subscription.mu.Lock()
	defer subscription.mu.Unlock()
	if !subscription.closed {
		subscription.closed = true
		close(subscription.events)
	}
}

func (manager *Manager) broadcastLocked(current *managedResponse, event Event) {
	for subscriber := range current.subscribers {
		size := eventSize(event)
		subscriber.mu.Lock()
		if subscriber.closed || subscriber.bytes+size > 256<<10 {
			subscriber.mu.Unlock()
			delete(current.subscribers, subscriber)
			subscriber.close()
			continue
		}
		select {
		case subscriber.events <- event:
			subscriber.bytes += size
			subscriber.mu.Unlock()
		default:
			subscriber.mu.Unlock()
			delete(current.subscribers, subscriber)
			subscriber.close()
		}
	}
}

func (manager *Manager) closeSubscribersLocked(current *managedResponse) {
	for subscriber := range current.subscribers {
		delete(current.subscribers, subscriber)
		subscriber.close()
	}
}

func eventSize(event Event) int {
	return len(event.Type) + len(event.Content) + len(event.State) + len(event.Text) + len(event.Code) + 64
}

func (manager *Manager) Stop(ctx context.Context) error {
	manager.mu.Lock()
	manager.stopping = true
	manager.mu.Unlock()
	done := make(chan struct{})
	go func() { manager.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		manager.mu.Lock()
		responses := make([]*managedResponse, 0, len(manager.responses))
		for _, response := range manager.responses {
			responses = append(responses, response)
		}
		manager.mu.Unlock()
		for _, response := range responses {
			manager.failForShutdown(response)
		}
		return ctx.Err()
	}
}

func (manager *Manager) failForShutdown(current *managedResponse) {
	current.mu.Lock()
	if current.response.State != "generating" {
		current.mu.Unlock()
		return
	}
	if current.cancel != nil {
		current.cancel()
	}
	result, err := manager.database.Exec(`UPDATE tutor_responses SET content = ?, state = 'failed',
		failure_code = 'server_shutdown', finished_at = ? WHERE id = ? AND state = 'generating'`,
		current.response.Content, instant(time.Now()), current.response.ID)
	if err != nil {
		manager.logger.Error("fail tutor response during shutdown", "response_id", current.response.ID, "error", err)
		current.mu.Unlock()
		return
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		current.mu.Unlock()
		return
	}
	current.response.State, current.response.FailureCode = "failed", "server_shutdown"
	manager.broadcastLocked(current, Event{Type: "failed", State: "failed", Code: "server_shutdown"})
	manager.closeSubscribersLocked(current)
	current.mu.Unlock()
	manager.release(current)
}

// BeginShutdown immediately prevents new queued work from starting.
func (manager *Manager) BeginShutdown() {
	manager.mu.Lock()
	manager.stopping = true
	manager.mu.Unlock()
}

func (manager *Manager) buildRequest(ctx context.Context, response Response) (openai.ChatRequest, []string, error) {
	var courseID, studentID string
	if err := manager.database.QueryRowContext(ctx, `SELECT course_id, student_user_id FROM tutoring_sessions
		WHERE id = ? AND state = 'active'`, response.SessionID).Scan(&courseID, &studentID); err != nil {
		return openai.ChatRequest{}, nil, err
	}
	courseContext, err := course.LoadTutorContext(ctx, manager.database, courseID, studentID)
	if err != nil {
		return openai.ChatRequest{}, nil, err
	}
	profile, err := user.LoadTutorProfile(ctx, manager.database, studentID)
	if err != nil {
		return openai.ChatRequest{}, nil, err
	}
	base, err := os.ReadFile(filepath.Join(manager.dataDir, "llm-instructions", "init.md"))
	if err != nil {
		return openai.ChatRequest{}, nil, err
	}
	selectedIDs, err := manager.selectedIDs(ctx, response.SessionID)
	if err != nil {
		return openai.ChatRequest{}, nil, err
	}
	selected, err := manager.service.materials.AvailableSelectionsForTutor(ctx, manager.database, courseID, studentID,
		selectedIDs)
	if err != nil {
		return openai.ChatRequest{}, nil, err
	}
	codec, err := tokenizer.Get(tokenizer.O200kBase)
	if err != nil {
		return openai.ChatRequest{}, nil, err
	}
	var instructions strings.Builder
	instructions.Write(base)
	instructions.WriteString("\n\nCourse brief:\n")
	encodedCourse, err := json.Marshal(courseContext)
	if err != nil {
		return openai.ChatRequest{}, nil, fmt.Errorf("encode tutor course context: %w", err)
	}
	instructions.Write(encodedCourse)
	instructions.WriteString("\n\nStudent brief:\n")
	encodedProfile, err := json.Marshal(profile)
	if err != nil {
		return openai.ChatRequest{}, nil, fmt.Errorf("encode tutor student context: %w", err)
	}
	instructions.Write(encodedProfile)
	for _, value := range selected {
		identity := struct {
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Kind  string          `json:"kind"`
			Scope string          `json:"scope"`
			Brief *material.Brief `json:"brief"`
		}{ID: value.ID, Name: value.Name, Kind: value.Kind, Scope: value.Scope, Brief: value.Brief}
		encoded, err := json.Marshal(identity)
		if err != nil {
			return openai.ChatRequest{}, nil, fmt.Errorf("encode selected material context: %w", err)
		}
		instructions.WriteString("\n\nSelected material:\n")
		instructions.Write(encoded)
	}
	var previousSummary, previousFollowUp string
	err = manager.database.QueryRowContext(ctx, `SELECT COALESCE(summary, ''), COALESCE(follow_up, '')
		FROM tutoring_sessions WHERE course_id = ? AND student_user_id = ? AND state = 'completed' AND summary IS NOT NULL
		ORDER BY completed_at DESC, id DESC LIMIT 1`, courseID, studentID).Scan(&previousSummary, &previousFollowUp)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return openai.ChatRequest{}, nil, fmt.Errorf("load previous tutoring summary: %w", err)
	}
	if previousSummary != "" {
		instructions.WriteString("\n\nPrevious session summary:\n")
		instructions.WriteString(previousSummary)
		instructions.WriteString("\nFollow-up:\n")
		instructions.WriteString(previousFollowUp)
	}
	messages, err := manager.conversation(ctx, response)
	if err != nil {
		return openai.ChatRequest{}, nil, err
	}
	if len(messages) == 0 {
		return openai.ChatRequest{}, nil, ErrInvalid
	}
	instructionText := instructions.String()
	count, err := codec.Count(instructionText)
	if err != nil {
		return openai.ChatRequest{}, nil, err
	}
	currentCount, err := codec.Count(messages[len(messages)-1].Content)
	if err != nil || count+currentCount > 32_000 {
		return openai.ChatRequest{}, nil, ErrInvalid
	}
	var used []string
	for _, value := range selected {
		if value.CompleteContent == "" {
			continue
		}
		addition := "\n\nSelected material content (" + value.ID + "):\n" + value.CompleteContent
		additionCount, countErr := codec.Count(addition)
		if countErr != nil {
			return openai.ChatRequest{}, nil, countErr
		}
		if count+currentCount+additionCount <= 32_000 {
			instructionText += addition
			count += additionCount
			used = append(used, value.ID)
		}
	}
	currentMessage := messages[len(messages)-1]
	var priorTurns [][]openai.ChatMessage
	budget := count + currentCount
	for end := len(messages) - 1; end > 0; {
		start := end - 1
		for start > 0 && messages[start].Role != "user" {
			start--
		}
		turnTokens := 0
		for index := start; index < end; index++ {
			messageTokens, countErr := codec.Count(messages[index].Content)
			if countErr != nil {
				return openai.ChatRequest{}, nil, countErr
			}
			turnTokens += messageTokens
		}
		if budget+turnTokens <= 32_000 {
			priorTurns = append(priorTurns, append([]openai.ChatMessage(nil), messages[start:end]...))
			budget += turnTokens
		}
		end = start
	}
	kept := make([]openai.ChatMessage, 0, len(messages))
	for index := len(priorTurns) - 1; index >= 0; index-- {
		kept = append(kept, priorTurns[index]...)
	}
	kept = append(kept, currentMessage)
	return openai.ChatRequest{Instructions: instructionText, Messages: kept, Tools: tutorTools()}, used, nil
}

func (manager *Manager) conversation(ctx context.Context, response Response) ([]openai.ChatMessage, error) {
	rows, err := manager.database.QueryContext(ctx, `SELECT m.id, m.content, r.id, r.content, r.state
		FROM student_messages m JOIN tutor_responses r ON r.student_message_id = m.id
		WHERE m.tutoring_session_id = ? AND (r.id = ? OR r.state IN ('completed', 'failed', 'interrupted'))
		ORDER BY m.sequence, r.attempt`, response.SessionID, response.ID)
	if err != nil {
		return nil, err
	}
	var messages []openai.ChatMessage
	lastMessage := ""
	currentUser := ""
	for rows.Next() {
		var messageID, userText, responseID, assistantText, state string
		if err := rows.Scan(&messageID, &userText, &responseID, &assistantText, &state); err != nil {
			return nil, errors.Join(err, rows.Close())
		}
		if messageID != lastMessage {
			messages = append(messages, openai.ChatMessage{Role: "user", Content: userText})
			lastMessage = messageID
		}
		if messageID == response.MessageID {
			currentUser = userText
		}
		if responseID != response.ID && assistantText != "" {
			messages = append(messages, openai.ChatMessage{Role: "assistant", Content: assistantText})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, errors.Join(err, rows.Close())
	}
	if currentUser != "" && len(messages) > 0 && messages[len(messages)-1].Role != "user" {
		messages = append(messages, openai.ChatMessage{Role: "user", Content: currentUser})
	}
	return messages, rows.Close()
}

func (manager *Manager) selectedIDs(ctx context.Context, sessionID string) ([]string, error) {
	rows, err := manager.database.QueryContext(ctx, `SELECT material_id FROM session_material_selections
		WHERE tutoring_session_id = ? ORDER BY material_id`, sessionID)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, errors.Join(err, rows.Close())
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.Join(err, rows.Close())
	}
	return ids, rows.Close()
}

func (manager *Manager) toolExecutor(response Response, delivered func(string)) openai.ToolExecutor {
	excerpts := 0
	seen := make(map[string]map[int]bool)
	return func(ctx context.Context, call openai.ToolCall) (string, error) {
		var courseID, studentID string
		if err := manager.database.QueryRowContext(ctx, `SELECT course_id, student_user_id FROM tutoring_sessions
			WHERE id = ? AND state = 'active'`, response.SessionID).Scan(&courseID, &studentID); err != nil {
			return "", ErrNotFound
		}
		switch call.Name {
		case "search_material":
			var arguments struct {
				Terms []string `json:"terms"`
			}
			if decodeToolArguments(call.Arguments, &arguments) != nil || len(arguments.Terms) < 1 ||
				len(arguments.Terms) > 16 {
				return "", ErrInvalid
			}
			for _, term := range arguments.Terms {
				if term == "" || !utf8.ValidString(term) || utf8.RuneCountInString(term) > 100 || len(term) > 400 {
					return "", ErrInvalid
				}
			}
			hits, err := manager.service.materials.SearchForTutor(ctx, courseID, studentID, arguments.Terms, 8)
			if err != nil {
				return "", err
			}
			encoded, err := json.Marshal(hits)
			return string(encoded), err
		case "get_excerpt":
			var arguments struct {
				MaterialID string `json:"material_id"`
				FileID     string `json:"file_id"`
				Sequence   int    `json:"sequence"`
			}
			if decodeToolArguments(call.Arguments, &arguments) != nil || excerpts >= 8 ||
				len(arguments.MaterialID) > 128 || len(arguments.FileID) > 128 || arguments.Sequence < 1 {
				return "", ErrInvalid
			}
			value, err := manager.service.materials.ExcerptForTutor(ctx, courseID, studentID, arguments.MaterialID,
				arguments.FileID, arguments.Sequence)
			if err != nil {
				return "", err
			}
			key := fmt.Sprintf("%s/%s", arguments.MaterialID, arguments.FileID)
			if seen[key] == nil {
				seen[key] = make(map[int]bool)
			}
			var novel []material.Segment
			for _, segment := range value.IncludedSegments {
				if !seen[key][segment.Sequence] {
					novel = append(novel, segment)
				}
			}
			if len(novel) == 0 {
				return `{"duplicate":true}`, nil
			}
			var text strings.Builder
			value.IncludedSequences = value.IncludedSequences[:0]
			for _, segment := range novel {
				if text.Len() > 0 {
					text.WriteString("\n\n")
				}
				text.WriteString(segment.Text)
				value.IncludedSequences = append(value.IncludedSequences, segment.Sequence)
				seen[key][segment.Sequence] = true
			}
			value.Text = text.String()
			value.Sequence = novel[0].Sequence
			value.ChapterLabel = novel[0].ChapterLabel
			value.SectionLabel = novel[0].SectionLabel
			excerpts++
			if delivered != nil {
				delivered(value.MaterialID)
			}
			encoded, err := json.Marshal(value)
			return string(encoded), err
		default:
			return "", ErrInvalid
		}
	}
}

func (delivery *materialDelivery) queue(materialID string) { delivery.pending[materialID] = true }

func (delivery *materialDelivery) accepted(ctx context.Context) error {
	for materialID := range delivery.pending {
		if err := delivery.manager.markUsed(ctx, delivery.response, materialID); err != nil {
			return err
		}
		delete(delivery.pending, materialID)
	}
	return nil
}

func decodeToolArguments(value string, destination any) error {
	if len(value) > 16<<10 || !utf8.ValidString(value) {
		return ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewBufferString(value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return ErrInvalid
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ErrInvalid
	}
	return nil
}

func (manager *Manager) markUsed(ctx context.Context, response Response, materialID string) error {
	_, err := manager.database.ExecContext(ctx, `INSERT OR IGNORE INTO material_retrievals
		(id, tutoring_session_id, tutor_response_id, material_id, created_at) VALUES (?, ?, ?, ?, ?)`,
		"ret_"+uuid.NewString(), response.SessionID, response.ID, materialID, instant(time.Now()))
	return err
}

func tutorTools() []openai.Tool {
	return []openai.Tool{
		{Name: "search_material", Description: "Search authorized course and student material by normalized terms.",
			Parameters: map[string]any{"type": "object", "additionalProperties": false,
				"properties": map[string]any{"terms": map[string]any{"type": "array", "minItems": 1,
					"maxItems": 16, "items": map[string]any{"type": "string", "maxLength": 100}}},
				"required": []string{"terms"}}},
		{Name: "get_excerpt", Description: "Read one authorized bounded excerpt from a search result.",
			Parameters: map[string]any{"type": "object", "additionalProperties": false,
				"properties": map[string]any{"material_id": map[string]any{"type": "string"},
					"file_id": map[string]any{"type": "string"}, "sequence": map[string]any{"type": "integer", "minimum": 1}},
				"required": []string{"material_id", "file_id", "sequence"}}},
	}
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}
