package material

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/thorstenkramm/mia/internal/audit"
	"github.com/thorstenkramm/mia/internal/filepublish"
	"github.com/thorstenkramm/mia/internal/jobs"
	"github.com/thorstenkramm/mia/internal/provider/mistral"
	"github.com/thorstenkramm/mia/internal/provider/openai"
	miSQLite "github.com/thorstenkramm/mia/internal/sqlite"
	"github.com/tiktoken-go/tokenizer"
)

const defaultMaterialInstructions = `Create a concise, strictly grounded material brief from only the supplied source.
Do not invent learning goals, educational level, locations, or content. Use chapter and section labels rather than OCR
page positions. Record ambiguity, unreadable content, and incomplete source in warnings.`

type ExtractionHandler struct{ service *Service }
type SummaryHandler struct{ service *Service }

func NewExtractionHandler(service *Service) *ExtractionHandler {
	return &ExtractionHandler{service: service}
}
func NewSummaryHandler(service *Service) *SummaryHandler { return &SummaryHandler{service: service} }

func EnsureInstructions(dataDir string) error {
	for _, kind := range []string{"text-book", "exam", "worksheet", "website", "youtube"} {
		path := filepath.Join(dataDir, "llm-instructions", "jobs", "material", kind+".md")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return fmt.Errorf("create material instruction directory: %w", err)
		}
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("create material instruction file: %w", err)
		}
		if _, err := file.WriteString(defaultMaterialInstructions + "\n"); err != nil {
			return errors.Join(fmt.Errorf("write material instruction file: %w", err), file.Close())
		}
		if err := file.Sync(); err != nil {
			return errors.Join(fmt.Errorf("sync material instruction file: %w", err), file.Close())
		}
		if err := file.Close(); err != nil {
			return fmt.Errorf("close material instruction file: %w", err)
		}
	}
	return nil
}

func (handler *ExtractionHandler) Execute(ctx context.Context, job jobs.Job) (jobs.Result, error) {
	file, err := loadFile(ctx, handler.service.database, job.SubjectID)
	if err != nil {
		if errors.Is(err, ErrFileNotFound) {
			return jobs.Result{}, jobs.ErrStaleLease
		}
		return jobs.Result{}, err
	}
	var format, materialState string
	if err := handler.service.database.QueryRowContext(ctx, `SELECT format, state FROM materials WHERE id = ?`,
		file.MaterialID).Scan(&format, &materialState); err != nil || materialState != "processing" {
		return jobs.Result{}, jobs.ErrStaleLease
	}
	path, err := handler.service.sourcePath(file.MaterialID, file.ID)
	if err != nil {
		return jobs.Result{}, err
	}
	source, err := os.ReadFile(path)
	if err != nil {
		return jobs.Result{}, &jobs.Failure{Code: "material_source_missing", Cause: err}
	}
	var segments []Segment
	var inputUnits int64
	if format == "pdf" || format == "jpeg" || format == "png" {
		if handler.service.ocr == nil {
			return jobs.Result{}, &jobs.Failure{Code: "mistral_unavailable", Retryable: true}
		}
		if format == "pdf" {
			segments, inputUnits, err = handler.ocrPDF(ctx, source, file.PageCount)
		} else {
			var result mistral.Result
			result, err = handler.service.ocr.OCR(ctx, file.MediaType, source)
			if err == nil {
				segments, err = ocrSegments(result)
				inputUnits = result.InputUnits
			}
		}
		if err != nil {
			var providerError *mistral.Error
			if errors.As(err, &providerError) {
				failure := providerFailure(err)
				var jobFailure *jobs.Failure
				if errors.As(failure, &jobFailure) {
					jobFailure.ProviderInputUnits += inputUnits
				}
				return jobs.Result{}, failure
			}
			return jobs.Result{}, &jobs.Failure{Code: "material_ocr_invalid", Cause: err,
				ProviderInputUnits: inputUnits}
		}
	} else {
		segments, err = localExtract(format, source)
		if err != nil {
			return jobs.Result{}, &jobs.Failure{Code: "material_extraction_invalid", Cause: err}
		}
	}
	content, err := encodeSegments(segments)
	if err != nil {
		return jobs.Result{}, &jobs.Failure{Code: "material_content_invalid", Cause: err,
			ProviderInputUnits: inputUnits}
	}
	return jobs.Result{Value: extractionResult{Content: content, Input: inputUnits}, ProviderInputUnits: inputUnits}, nil
}

func (handler *ExtractionHandler) ocrPDF(ctx context.Context, source []byte, pageCount int) ([]Segment, int64, error) {
	const pagesPerChunk = 25
	var all []Segment
	var inputUnits int64
	configuration := model.NewDefaultConfiguration()
	for first := 1; first <= pageCount; first += pagesPerChunk {
		last := min(first+pagesPerChunk-1, pageCount)
		var chunk bytes.Buffer
		if err := api.Trim(bytes.NewReader(source), &chunk, []string{fmt.Sprintf("%d-%d", first, last)},
			configuration); err != nil {
			return nil, inputUnits, err
		}
		result, err := handler.service.ocr.OCR(ctx, "application/pdf", chunk.Bytes())
		if err != nil {
			return nil, inputUnits, err
		}
		inputUnits += result.InputUnits
		segments, err := ocrSegments(result)
		if err != nil {
			return nil, inputUnits, err
		}
		for _, segment := range segments {
			segment.Sequence = len(all) + 1
			all = append(all, segment)
		}
	}
	return all, inputUnits, nil
}

func (handler *ExtractionHandler) Commit(ctx context.Context, query miSQLite.Querier, job jobs.Job,
	result jobs.Result) (func(bool) error, error) {
	extraction, ok := result.Value.(extractionResult)
	if !ok {
		return nil, errors.New("invalid extraction result type")
	}
	var materialID string
	err := query.QueryRowContext(ctx, `SELECT f.material_id FROM material_files f JOIN materials m ON m.id = f.material_id
		WHERE f.id = ? AND f.state = 'processing' AND m.state = 'processing'`, job.SubjectID).Scan(&materialID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, jobs.ErrStaleLease
	}
	if err != nil {
		return nil, err
	}
	rows, err := query.QueryContext(ctx, `SELECT id FROM material_files WHERE material_id = ?
		AND state = 'processed'`, materialID)
	if err != nil {
		return nil, err
	}
	totalContent := int64(len(extraction.Content))
	currentSegments, err := decodeSegments(extraction.Content)
	if err != nil {
		return nil, closeRows(rows, err)
	}
	var totalText int64
	for _, segment := range currentSegments {
		totalText += int64(len(segment.Text))
	}
	for rows.Next() {
		var processedID string
		if err := rows.Scan(&processedID); err != nil {
			return nil, closeRows(rows, err)
		}
		processedPath, err := handler.service.contentPath(materialID, processedID)
		if err != nil {
			return nil, closeRows(rows, err)
		}
		processedContent, err := os.ReadFile(processedPath)
		if err != nil {
			return nil, closeRows(rows, err)
		}
		totalContent += int64(len(processedContent))
		segments, err := decodeSegments(processedContent)
		if err != nil {
			return nil, closeRows(rows, err)
		}
		for _, segment := range segments {
			totalText += int64(len(segment.Text))
		}
	}
	if err := rows.Err(); err != nil {
		return nil, closeRows(rows, err)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if totalContent > maxContentBytes || totalText > maxContentBytes {
		return nil, &jobs.Failure{Code: "material_content_too_large"}
	}
	path, err := handler.service.contentPath(materialID, job.SubjectID)
	if err != nil {
		return nil, err
	}
	change, err := filepublish.Replace(path, extraction.Content)
	if err != nil {
		return nil, err
	}
	finish := func(commit bool) error { return change.Finish(commit) }
	resultSQL, err := query.ExecContext(ctx, `UPDATE material_files SET state = 'processed', failure_code = NULL
		WHERE id = ? AND state = 'processing' AND material_id = ?`, job.SubjectID, materialID)
	if err != nil {
		return finish, err
	}
	if err := jobs.RequireCommitted(resultSQL); err != nil {
		return finish, err
	}
	var remaining int
	if err := query.QueryRowContext(ctx, `SELECT COUNT(*) FROM material_files
		WHERE material_id = ? AND state != 'processed'`, materialID).Scan(&remaining); err != nil {
		return finish, err
	}
	if remaining == 0 {
		var courseID, ownerID string
		if err := query.QueryRowContext(ctx, `SELECT course_id, COALESCE(owner_user_id, '') FROM materials
			WHERE id = ? AND state = 'processing'`, materialID).Scan(&courseID, &ownerID); err != nil {
			return finish, err
		}
		if _, err := jobs.Enqueue(ctx, query, "material-summary", "material", materialID, courseID, ownerID); err != nil {
			return finish, err
		}
	}
	return finish, nil
}

func (handler *ExtractionHandler) TerminalFailure(ctx context.Context, query miSQLite.Querier, job jobs.Job, code string) error {
	var materialID, courseID string
	if err := query.QueryRowContext(ctx, `SELECT f.material_id, m.course_id FROM material_files f
		JOIN materials m ON m.id = f.material_id WHERE f.id = ?`, job.SubjectID).Scan(&materialID, &courseID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	if _, err := query.ExecContext(ctx, `UPDATE material_files SET state = 'failed', failure_code = ?
		WHERE id = ? AND state = 'processing'`, code, job.SubjectID); err != nil {
		return err
	}
	if _, err := query.ExecContext(ctx, `UPDATE materials SET state = 'failed', failure_code = ?, updated_at = ?
		WHERE id = ? AND state = 'processing'`, code, instant(time.Now()), materialID); err != nil {
		return err
	}
	subjects, err := materialSubjectIDs(ctx, query, materialID)
	if err != nil {
		return err
	}
	if err := jobs.CancelSubjects(ctx, query, subjects, job.ID); err != nil {
		return err
	}
	return audit.WriteWithMetadata(ctx, query, audit.ActionMaterialProcessingFailed, "", "",
		audit.Metadata{CourseID: courseID, MaterialID: materialID, OutcomeCode: code})
}

func (handler *SummaryHandler) Execute(ctx context.Context, job jobs.Job) (jobs.Result, error) {
	if handler.service.summary == nil {
		return jobs.Result{}, &jobs.Failure{Code: "openai_unavailable", Retryable: true}
	}
	var kind, state string
	if err := handler.service.database.QueryRowContext(ctx, `SELECT kind, state FROM materials WHERE id = ?`,
		job.SubjectID).Scan(&kind, &state); err != nil || state != "processing" {
		return jobs.Result{}, jobs.ErrStaleLease
	}
	rows, err := handler.service.database.QueryContext(ctx, `SELECT f.original_filename, f.id FROM material_files f
		WHERE f.material_id = ? AND f.state = 'processed' ORDER BY f.created_at, f.id`, job.SubjectID)
	if err != nil {
		return jobs.Result{}, err
	}
	var units []string
	for rows.Next() {
		var name, fileID string
		if err := rows.Scan(&name, &fileID); err != nil {
			return jobs.Result{}, closeRows(rows, err)
		}
		path, err := handler.service.contentPath(job.SubjectID, fileID)
		if err != nil {
			return jobs.Result{}, closeRows(rows, err)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return jobs.Result{}, closeRows(rows, &jobs.Failure{Code: "material_content_missing", Cause: err})
		}
		segments, err := decodeSegments(content)
		if err != nil {
			return jobs.Result{}, closeRows(rows, &jobs.Failure{Code: "material_content_invalid", Cause: err})
		}
		for _, segment := range segments {
			units = append(units, "Source file: "+name+"\n"+segment.Text)
		}
	}
	if err := rows.Err(); err != nil {
		return jobs.Result{}, closeRows(rows, err)
	}
	if err := rows.Close(); err != nil {
		return jobs.Result{}, err
	}
	instructions, err := os.ReadFile(filepath.Join(handler.service.dataDir, "llm-instructions", "jobs", "material",
		kind+".md"))
	if err != nil {
		return jobs.Result{}, err
	}
	brief, input, output, err := handler.summarize(ctx, string(instructions), units)
	if err != nil {
		return jobs.Result{}, err
	}
	return jobs.Result{Value: summaryResult{Brief: brief, Input: input, Output: output},
		ProviderInputUnits: input, ProviderOutputUnits: output}, nil
}

func (handler *SummaryHandler) summarize(ctx context.Context, instructions string, units []string) (Brief, int64, int64, error) {
	codec, err := tokenizer.Get(tokenizer.O200kBase)
	if err != nil {
		return Brief{}, 0, 0, err
	}
	current := units
	var inputTotal, outputTotal int64
	for round := 0; round <= 2; round++ {
		chunks, err := tokenChunks(codec, current)
		if err != nil || len(chunks) > 64 {
			return Brief{}, inputTotal, outputTotal, &jobs.Failure{Code: "summary_input_too_large", Cause: err,
				ProviderInputUnits: inputTotal, ProviderOutputUnits: outputTotal}
		}
		var outputs []string
		var final Brief
		for _, chunk := range chunks {
			response, err := handler.service.summary.Structured(ctx, instructions, []string{chunk}, briefSchema())
			if err != nil {
				failure := providerFailure(err)
				var jobFailure *jobs.Failure
				if errors.As(failure, &jobFailure) {
					jobFailure.ProviderInputUnits += inputTotal
					jobFailure.ProviderOutputUnits += outputTotal
				}
				return Brief{}, inputTotal, outputTotal, failure
			}
			inputTotal += response.InputTokens
			outputTotal += response.OutputTokens
			brief, err := decodeBrief(response.JSON)
			if err != nil {
				return Brief{}, inputTotal, outputTotal, &jobs.Failure{Code: "material_brief_invalid", Cause: err,
					ProviderInputUnits: inputTotal, ProviderOutputUnits: outputTotal}
			}
			final = brief
			outputs = append(outputs, string(response.JSON))
		}
		if len(outputs) == 1 {
			return final, inputTotal, outputTotal, nil
		}
		current = outputs
		instructions = defaultMaterialInstructions + "\nCombine the ordered partial briefs without losing coverage."
	}
	return Brief{}, inputTotal, outputTotal, &jobs.Failure{Code: "summary_input_too_large",
		ProviderInputUnits: inputTotal, ProviderOutputUnits: outputTotal}
}

func (handler *SummaryHandler) Commit(ctx context.Context, query miSQLite.Querier, job jobs.Job,
	result jobs.Result) (func(bool) error, error) {
	summary, ok := result.Value.(summaryResult)
	if !ok || validateBrief(summary.Brief) != nil {
		return nil, errors.New("invalid summary result type")
	}
	encoded, err := json.Marshal(summary.Brief)
	if err != nil {
		return nil, fmt.Errorf("encode generated brief: %w", err)
	}
	resultSQL, err := query.ExecContext(ctx, `UPDATE materials SET brief_json = ?, brief_source = 'generated',
		brief_updated_at = ?, brief_updated_by = NULL, state = 'ready', failure_code = NULL, updated_at = ?
		WHERE id = ? AND state = 'processing' AND (brief_source IS NULL OR brief_source = 'generated')
		AND NOT EXISTS(SELECT 1 FROM material_files WHERE material_id = materials.id AND state != 'processed')`,
		string(encoded), instant(time.Now()), instant(time.Now()), job.SubjectID)
	// jscpd:ignore-start
	// The guarded-commit tail and the following TerminalFailure signature are
	// fixed by the jobs.Handler contract. internal/tutoring/summary.go implements
	// the same contract for tutoring sessions, so neither shape can differ. This
	// single marker covers both sides of that pair.
	if err != nil {
		return nil, err
	}
	if err := jobs.RequireCommitted(resultSQL); err != nil {
		return nil, err
	}
	return nil, nil
}

func (handler *SummaryHandler) TerminalFailure(ctx context.Context, query miSQLite.Querier, job jobs.Job, code string) error {
	var courseID string
	// jscpd:ignore-end
	if err := query.QueryRowContext(ctx, "SELECT course_id FROM materials WHERE id = ?", job.SubjectID).Scan(&courseID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	if _, err := query.ExecContext(ctx, `UPDATE materials SET state = 'failed', failure_code = ?, updated_at = ?
		WHERE id = ? AND state = 'processing'`, code, instant(time.Now()), job.SubjectID); err != nil {
		return err
	}
	return audit.WriteWithMetadata(ctx, query, audit.ActionMaterialProcessingFailed, "", "",
		audit.Metadata{CourseID: courseID, MaterialID: job.SubjectID, OutcomeCode: code})
}

type tokenCounter interface{ Count(string) (int, error) }

func tokenChunks(codec tokenCounter, units []string) ([]string, error) {
	const maximum = 24_000
	var chunks []string
	var current strings.Builder
	for _, unit := range units {
		count, err := codec.Count(unit)
		if err != nil {
			return nil, err
		}
		if count > maximum {
			parts, err := splitTokenUnit(codec, unit, maximum)
			if err != nil {
				return nil, err
			}
			for _, part := range parts {
				if current.Len() > 0 {
					chunks = append(chunks, current.String())
					current.Reset()
				}
				chunks = append(chunks, part)
			}
			continue
		}
		candidate := current.String()
		if candidate != "" {
			candidate += "\n\n"
		}
		candidate += unit
		candidateCount, err := codec.Count(candidate)
		if err != nil {
			return nil, err
		}
		if candidateCount > maximum && current.Len() > 0 {
			chunks = append(chunks, current.String())
			current.Reset()
		}
		if current.Len() > 0 {
			current.WriteString("\n\n")
		}
		current.WriteString(unit)
	}
	if current.Len() > 0 {
		chunks = append(chunks, current.String())
	}
	if len(chunks) == 0 {
		return nil, errors.New("summary has no input")
	}
	return chunks, nil
}

func splitTokenUnit(codec tokenCounter, unit string, maximum int) ([]string, error) {
	runes := []rune(unit)
	var parts []string
	for start := 0; start < len(runes); {
		low, high := start+1, len(runes)
		best := start
		for low <= high {
			middle := low + (high-low)/2
			count, err := codec.Count(string(runes[start:middle]))
			if err != nil {
				return nil, err
			}
			if count <= maximum {
				best, low = middle, middle+1
			} else {
				high = middle - 1
			}
		}
		if best == start {
			return nil, errors.New("cannot split oversized summary unit")
		}
		parts = append(parts, string(runes[start:best]))
		start = best
	}
	return parts, nil
}

func providerFailure(err error) error {
	var mistralError *mistral.Error
	if errors.As(err, &mistralError) {
		return &jobs.Failure{Code: mistralError.Code, Retryable: mistralError.Retryable,
			RetryAfter: mistralError.RetryAfter, Cause: err}
	}
	var openAIError *openai.Error
	if errors.As(err, &openAIError) {
		return &jobs.Failure{Code: openAIError.Code, Retryable: openAIError.Retryable,
			RetryAfter: openAIError.RetryAfter, Cause: err}
	}
	return err
}

func briefSchema() map[string]any {
	stringArray := func(maxItems, maxLength int) map[string]any {
		return map[string]any{"type": "array", "maxItems": maxItems,
			"items": map[string]any{"type": "string", "minLength": 1, "maxLength": maxLength}}
	}
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"properties": map[string]any{
			"version":  map[string]any{"type": "integer", "const": 1},
			"summary":  map[string]any{"type": "string", "minLength": 1, "maxLength": 4000},
			"subjects": stringArray(20, 100), "learning_goals": stringArray(30, 300),
			"sections": map[string]any{"type": "array", "maxItems": 500, "items": map[string]any{
				"type": "object", "additionalProperties": false,
				"properties": map[string]any{"sequence": map[string]any{"type": "integer", "minimum": 1},
					"label":        map[string]any{"type": []string{"string", "null"}, "maxLength": 100},
					"title":        map[string]any{"type": "string", "minLength": 1, "maxLength": 200},
					"description":  map[string]any{"type": "string", "minLength": 1, "maxLength": 500},
					"search_terms": stringArray(10, 50)},
				"required": []string{"sequence", "label", "title", "description", "search_terms"}}},
			"warnings":          stringArray(20, 500),
			"educational_level": map[string]any{"type": []string{"string", "null"}, "maxLength": 100},
		},
		"required": []string{"version", "summary", "subjects", "learning_goals", "sections", "warnings",
			"educational_level"},
	}
}
