package tutoring

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/thorstenkramm/mia/internal/audit"
	"github.com/thorstenkramm/mia/internal/jobs"
	"github.com/thorstenkramm/mia/internal/provider/openai"
	miSQLite "github.com/thorstenkramm/mia/internal/sqlite"
	"github.com/tiktoken-go/tokenizer"
)

type structuredClient interface {
	Structured(context.Context, string, []string, map[string]any) (openai.Result, error)
}

type SessionSummaryHandler struct {
	service *Service
	client  structuredClient
	dataDir string
}

type sessionSummary struct {
	Summary  string `json:"summary"`
	FollowUp string `json:"follow_up"`
}

func NewSessionSummaryHandler(service *Service, client structuredClient, dataDir string) *SessionSummaryHandler {
	return &SessionSummaryHandler{service: service, client: client, dataDir: dataDir}
}

func (handler *SessionSummaryHandler) Execute(ctx context.Context, job jobs.Job) (jobs.Result, error) {
	if handler.client == nil {
		return jobs.Result{}, &jobs.Failure{Code: "openai_unavailable", Retryable: true}
	}
	var state string
	if err := handler.service.database.QueryRowContext(ctx, `SELECT state FROM tutoring_sessions WHERE id = ?`,
		job.SubjectID).Scan(&state); err != nil || state != "completed" {
		return jobs.Result{}, jobs.ErrStaleLease
	}
	rows, err := handler.service.database.QueryContext(ctx, `SELECT m.sequence, m.content, r.attempt, r.content, r.state
		FROM student_messages m JOIN tutor_responses r ON r.student_message_id = m.id WHERE m.tutoring_session_id = ?
		ORDER BY m.sequence, r.attempt`, job.SubjectID)
	if err != nil {
		return jobs.Result{}, err
	}
	var units []string
	lastSequence := 0
	for rows.Next() {
		var sequence, attempt int
		var message, response, responseState string
		if err := rows.Scan(&sequence, &message, &attempt, &response, &responseState); err != nil {
			return jobs.Result{}, errors.Join(err, rows.Close())
		}
		if sequence != lastSequence {
			units = append(units, fmt.Sprintf("Student message %d:\n%s", sequence, message))
			lastSequence = sequence
		}
		if response != "" {
			units = append(units, fmt.Sprintf("Tutor response %d.%d (%s):\n%s", sequence, attempt,
				responseState, response))
		}
	}
	if err := rows.Err(); err != nil {
		return jobs.Result{}, errors.Join(err, rows.Close())
	}
	if err := rows.Close(); err != nil {
		return jobs.Result{}, err
	}
	materialRows, err := handler.service.database.QueryContext(ctx, `SELECT DISTINCT m.name, m.kind
		FROM material_retrievals used JOIN materials m ON m.id = used.material_id
		WHERE used.tutoring_session_id = ? ORDER BY m.name_normalized, m.id`, job.SubjectID)
	if err != nil {
		return jobs.Result{}, err
	}
	for materialRows.Next() {
		var name, kind string
		if err := materialRows.Scan(&name, &kind); err != nil {
			return jobs.Result{}, errors.Join(err, materialRows.Close())
		}
		units = append(units, "Material used: "+name+" ("+kind+")")
	}
	if err := materialRows.Err(); err != nil {
		return jobs.Result{}, errors.Join(err, materialRows.Close())
	}
	if err := materialRows.Close(); err != nil {
		return jobs.Result{}, err
	}
	if len(units) == 0 {
		return jobs.Result{}, &jobs.Failure{Code: "summary_input_invalid"}
	}
	instructions, err := os.ReadFile(filepath.Join(handler.dataDir, "llm-instructions", "jobs",
		"tutoring-session-summary.md"))
	if err != nil {
		return jobs.Result{}, err
	}
	value, input, output, err := handler.summarize(ctx, string(instructions), units)
	if err != nil {
		return jobs.Result{}, err
	}
	return jobs.Result{Value: value, ProviderInputUnits: input, ProviderOutputUnits: output}, nil
}

func (handler *SessionSummaryHandler) summarize(ctx context.Context, instructions string,
	units []string) (sessionSummary, int64, int64, error) {
	codec, err := tokenizer.Get(tokenizer.O200kBase)
	if err != nil {
		return sessionSummary{}, 0, 0, err
	}
	current := units
	var inputTotal, outputTotal int64
	for round := 0; round <= 2; round++ {
		chunks, err := summaryChunks(codec, current)
		if err != nil || len(chunks) > 64 {
			return sessionSummary{}, inputTotal, outputTotal, &jobs.Failure{Code: "summary_input_too_large",
				Cause: err, ProviderInputUnits: inputTotal, ProviderOutputUnits: outputTotal}
		}
		var outputs []string
		var final sessionSummary
		for _, chunk := range chunks {
			result, err := handler.client.Structured(ctx, instructions, []string{chunk}, sessionSummarySchema())
			if err != nil {
				return sessionSummary{}, inputTotal, outputTotal, summaryProviderFailure(err, inputTotal, outputTotal)
			}
			inputTotal += result.InputTokens
			outputTotal += result.OutputTokens
			if err := decodeSessionSummary(result.JSON, &final); err != nil || !validGeneratedSummary(final) {
				return sessionSummary{}, inputTotal, outputTotal, &jobs.Failure{Code: "session_summary_invalid",
					Cause: err, ProviderInputUnits: inputTotal, ProviderOutputUnits: outputTotal}
			}
			outputs = append(outputs, string(result.JSON))
		}
		if len(outputs) == 1 {
			return final, inputTotal, outputTotal, nil
		}
		current = outputs
		instructions = "Combine the ordered partial session summaries without losing strengths, weaknesses, or next steps."
	}
	return sessionSummary{}, inputTotal, outputTotal, &jobs.Failure{Code: "summary_input_too_large",
		ProviderInputUnits: inputTotal, ProviderOutputUnits: outputTotal}
}

func decodeSessionSummary(source []byte, destination *sessionSummary) error {
	decoder := json.NewDecoder(bytes.NewReader(source))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("session summary has trailing JSON")
	}
	return nil
}

func (handler *SessionSummaryHandler) Commit(ctx context.Context, query miSQLite.Querier, job jobs.Job,
	result jobs.Result) (func(bool) error, error) {
	value, ok := result.Value.(sessionSummary)
	if !ok || !validGeneratedSummary(value) {
		return nil, errors.New("invalid session summary result")
	}
	resultSQL, err := query.ExecContext(ctx, `UPDATE tutoring_sessions SET summary = ?, follow_up = ?,
		summary_source = 'generated', summary_updated_at = ?, summary_updated_by = NULL
		WHERE id = ? AND state = 'completed' AND summary IS NULL AND
		(summary_source IS NULL OR summary_source = 'generated')`, value.Summary, value.FollowUp, instant(time.Now()), job.SubjectID)
	// The guarded-commit tail and TerminalFailure signature below are fixed by
	// the jobs.Handler contract and match internal/material/jobs.go for that
	// reason. The duplication marker lives at that counterpart.
	if err != nil {
		return nil, err
	}
	if err := jobs.RequireCommitted(resultSQL); err != nil {
		return nil, err
	}
	return nil, nil
}

func (handler *SessionSummaryHandler) TerminalFailure(ctx context.Context, query miSQLite.Querier, job jobs.Job,
	code string) error {
	var courseID string
	if err := query.QueryRowContext(ctx, "SELECT course_id FROM tutoring_sessions WHERE id = ?", job.SubjectID).
		Scan(&courseID); errors.Is(err, sql.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	return audit.WriteWithMetadata(ctx, query, audit.ActionTutoringSummaryFailed, "", "",
		audit.Metadata{CourseID: courseID, OutcomeCode: code})
}

type summaryCounter interface{ Count(string) (int, error) }

func summaryChunks(codec summaryCounter, units []string) ([]string, error) {
	const limit = 24_000
	var chunks []string
	var current strings.Builder
	for _, unit := range units {
		parts := []string{unit}
		count, err := codec.Count(unit)
		if err != nil {
			return nil, err
		}
		if count > limit {
			parts, err = splitSummaryUnit(codec, unit, limit)
			if err != nil {
				return nil, err
			}
		}
		for _, part := range parts {
			candidate := current.String()
			if candidate != "" {
				candidate += "\n\n"
			}
			candidate += part
			count, err := codec.Count(candidate)
			if err != nil {
				return nil, err
			}
			if count > limit && current.Len() > 0 {
				chunks = append(chunks, current.String())
				current.Reset()
			}
			if current.Len() > 0 {
				current.WriteString("\n\n")
			}
			current.WriteString(part)
		}
	}
	if current.Len() > 0 {
		chunks = append(chunks, current.String())
	}
	if len(chunks) == 0 {
		return nil, errors.New("session summary has no input")
	}
	return chunks, nil
}

func splitSummaryUnit(codec summaryCounter, unit string, limit int) ([]string, error) {
	runes := []rune(unit)
	var result []string
	for start := 0; start < len(runes); {
		low, high, best := start+1, len(runes), start
		for low <= high {
			middle := low + (high-low)/2
			count, err := codec.Count(string(runes[start:middle]))
			if err != nil {
				return nil, err
			}
			if count <= limit {
				best, low = middle, middle+1
			} else {
				high = middle - 1
			}
		}
		if best == start {
			return nil, errors.New("cannot split session summary unit")
		}
		result = append(result, string(runes[start:best]))
		start = best
	}
	return result, nil
}

func validGeneratedSummary(value sessionSummary) bool {
	return value.Summary != "" && utf8.ValidString(value.Summary) && utf8.ValidString(value.FollowUp) &&
		utf8.RuneCountInString(value.Summary) <= 16_000 && len(value.Summary) <= 64<<10 &&
		utf8.RuneCountInString(value.FollowUp) <= 16_000 && len(value.FollowUp) <= 64<<10
}

func summaryProviderFailure(err error, input, output int64) error {
	var providerError *openai.Error
	if errors.As(err, &providerError) {
		return &jobs.Failure{Code: providerError.Code, Retryable: providerError.Retryable,
			RetryAfter: providerError.RetryAfter, Cause: err, ProviderInputUnits: input,
			ProviderOutputUnits: output}
	}
	return err
}

func sessionSummarySchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false,
		"properties": map[string]any{
			"summary":   map[string]any{"type": "string", "minLength": 1, "maxLength": 16000},
			"follow_up": map[string]any{"type": "string", "maxLength": 16000},
		}, "required": []string{"summary", "follow_up"}}
}
