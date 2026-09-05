package material

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	miSQLite "github.com/thorstenkramm/mia/internal/sqlite"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// TutorMaterial is the bounded material identity and brief supplied to tutoring.
type TutorMaterial struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Kind            string `json:"kind"`
	Scope           string `json:"scope"`
	Format          string `json:"format"`
	Brief           *Brief `json:"brief"`
	CompleteContent string `json:"-"`
}

// SearchHit identifies a deterministic segment candidate without exposing its content.
type SearchHit struct {
	MaterialID   string  `json:"material_id"`
	MaterialName string  `json:"material_name"`
	MaterialKind string  `json:"material_kind"`
	FileID       string  `json:"file_id"`
	Sequence     int     `json:"sequence"`
	ChapterLabel *string `json:"chapter_label"`
	SectionLabel *string `json:"section_label"`
	MatchedTerms int     `json:"matched_term_count"`
	Frequency    int     `json:"match_frequency"`
}

// TutorExcerpt is authorized content returned to the tutor model.
type TutorExcerpt struct {
	SearchHit
	Text              string    `json:"text"`
	IncludedSequences []int     `json:"-"`
	IncludedSegments  []Segment `json:"-"`
}

// SelectForTutor validates selected material in the caller's session-creation transaction.
func (service *Service) SelectForTutor(ctx context.Context, query miSQLite.Querier, courseID, studentID string,
	ids []string) ([]TutorMaterial, error) {
	seen := make(map[string]bool, len(ids))
	result := make([]TutorMaterial, 0, len(ids))
	for _, id := range ids {
		if id == "" || seen[id] {
			return nil, ErrNotFound
		}
		seen[id] = true
		value, err := service.loadTutorMaterial(ctx, query, id, courseID, studentID)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, nil
}

// AvailableSelectionsForTutor returns only selections still authorized for an existing session.
// Revocation makes content unavailable without preventing that session from continuing.
func (service *Service) AvailableSelectionsForTutor(ctx context.Context, query miSQLite.Querier, courseID,
	studentID string, ids []string) ([]TutorMaterial, error) {
	result := make([]TutorMaterial, 0, len(ids))
	for _, id := range ids {
		value, err := service.loadTutorMaterial(ctx, query, id, courseID, studentID)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, nil
}

func (service *Service) loadTutorMaterial(ctx context.Context, query miSQLite.Querier, id, courseID,
	studentID string) (TutorMaterial, error) {
	row := query.QueryRowContext(ctx, `SELECT m.id, m.name, m.kind, m.scope, m.format, m.brief_json
		FROM materials m WHERE m.id = ? AND m.course_id = ? AND m.state = 'ready' AND
		((m.scope = 'course-wide' AND m.is_approved = 1) OR
		 (m.scope = 'student-private' AND m.owner_user_id = ?))`, id, courseID, studentID)
	var value TutorMaterial
	var brief sql.NullString
	if err := row.Scan(&value.ID, &value.Name, &value.Kind, &value.Scope, &value.Format, &brief); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TutorMaterial{}, ErrNotFound
		}
		return TutorMaterial{}, fmt.Errorf("load tutor material: %w", err)
	}
	if brief.Valid {
		decoded, err := decodeBrief([]byte(brief.String))
		if err != nil {
			return TutorMaterial{}, fmt.Errorf("decode tutor material brief: %w", err)
		}
		value.Brief = cloneBrief(decoded)
	}
	if value.Format != "link" {
		content, complete, err := service.smallMaterialContent(ctx, query, id)
		if err != nil {
			return TutorMaterial{}, err
		}
		if complete {
			value.CompleteContent = content
		}
	}
	return value, nil
}

func (service *Service) smallMaterialContent(ctx context.Context, query miSQLite.Querier,
	materialID string) (string, bool, error) {
	rows, err := query.QueryContext(ctx, `SELECT id FROM material_files WHERE material_id = ? AND state = 'processed'
		ORDER BY created_at, id`, materialID)
	if err != nil {
		return "", false, err
	}
	var builder strings.Builder
	for rows.Next() {
		var fileID string
		if err := rows.Scan(&fileID); err != nil {
			return "", false, closeRows(rows, err)
		}
		err = service.scanSegments(materialID, fileID, func(segment Segment) error {
			if builder.Len() > 0 {
				builder.WriteString("\n\n")
			}
			builder.WriteString(segment.Text)
			if builder.Len() > 32<<10 || utf8.RuneCountInString(builder.String()) > 8_000 {
				return errStopSegmentScan
			}
			return nil
		})
		if errors.Is(err, errStopSegmentScan) {
			return "", false, closeRows(rows, nil)
		}
		if err != nil {
			return "", false, closeRows(rows, err)
		}
	}
	if err := rows.Err(); err != nil {
		return "", false, closeRows(rows, err)
	}
	if err := rows.Close(); err != nil {
		return "", false, err
	}
	return builder.String(), builder.Len() > 0, nil
}

// SearchForTutor scans only material authorized for this student and course.
func (service *Service) SearchForTutor(ctx context.Context, courseID, studentID string,
	terms []string, limit int) ([]SearchHit, error) {
	if limit < 1 || limit > 8 {
		return nil, ErrInvalid
	}
	normalized := normalizeTerms(terms)
	if len(normalized) == 0 {
		return nil, ErrInvalid
	}
	rows, err := service.database.QueryContext(ctx, `SELECT m.id, m.name, m.kind, f.id FROM materials m
		JOIN material_files f ON f.material_id = m.id WHERE m.course_id = ? AND m.state = 'ready'
		AND f.state = 'processed' AND ((m.scope = 'course-wide' AND m.is_approved = 1) OR
		(m.scope = 'student-private' AND m.owner_user_id = ?)) ORDER BY m.id, f.id`, courseID, studentID)
	if err != nil {
		return nil, fmt.Errorf("list tutor-search files: %w", err)
	}
	hits := make([]SearchHit, 0, limit)
	for rows.Next() {
		var materialID, name, kind, fileID string
		if err := rows.Scan(&materialID, &name, &kind, &fileID); err != nil {
			return nil, closeRows(rows, err)
		}
		err = service.scanSegments(materialID, fileID, func(segment Segment) error {
			matched, frequency := termScore(segment.Text, normalized)
			if matched > 0 {
				hits = append(hits, SearchHit{MaterialID: materialID, MaterialName: name, MaterialKind: kind,
					FileID: fileID, Sequence: segment.Sequence, ChapterLabel: segment.ChapterLabel,
					SectionLabel: segment.SectionLabel, MatchedTerms: matched, Frequency: frequency})
				sortSearchHits(hits)
				if len(hits) > limit {
					hits = hits[:limit]
				}
			}
			return nil
		})
		if err != nil {
			return nil, closeRows(rows, err)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, closeRows(rows, err)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	sortSearchHits(hits)
	return hits, nil
}

func sortSearchHits(hits []SearchHit) {
	sort.Slice(hits, func(left, right int) bool {
		if hits[left].MatchedTerms != hits[right].MatchedTerms {
			return hits[left].MatchedTerms > hits[right].MatchedTerms
		}
		if hits[left].Frequency != hits[right].Frequency {
			return hits[left].Frequency > hits[right].Frequency
		}
		if hits[left].MaterialID != hits[right].MaterialID {
			return hits[left].MaterialID < hits[right].MaterialID
		}
		if hits[left].FileID != hits[right].FileID {
			return hits[left].FileID < hits[right].FileID
		}
		return hits[left].Sequence < hits[right].Sequence
	})
}

// ExcerptForTutor reauthorizes one search location and returns at most one adjacent segment per side.
func (service *Service) ExcerptForTutor(ctx context.Context, courseID, studentID, materialID, fileID string,
	sequence int) (TutorExcerpt, error) {
	value, err := service.loadTutorMaterial(ctx, service.database, materialID, courseID, studentID)
	if err != nil || value.Format == "link" {
		return TutorExcerpt{}, ErrNotFound
	}
	var belongs int
	if err := service.database.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM material_files
		WHERE id = ? AND material_id = ? AND state = 'processed')`, fileID, materialID).Scan(&belongs); err != nil {
		return TutorExcerpt{}, err
	}
	if belongs == 0 {
		return TutorExcerpt{}, ErrNotFound
	}
	var segments []Segment
	err = service.scanSegments(materialID, fileID, func(segment Segment) error {
		if segment.Sequence >= sequence-1 && segment.Sequence <= sequence+1 {
			segments = append(segments, segment)
		}
		if segment.Sequence > sequence {
			return errStopSegmentScan
		}
		return nil
	})
	if err != nil && !errors.Is(err, errStopSegmentScan) {
		return TutorExcerpt{}, err
	}
	index := -1
	for current := range segments {
		if segments[current].Sequence == sequence {
			index = current
		}
	}
	if index == -1 {
		return TutorExcerpt{}, ErrNotFound
	}
	center := segments[index]
	center.Text = boundedText(center.Text, 4_000, 16<<10)
	includedSegments := []Segment{center}
	if index > 0 {
		candidate := append([]Segment{segments[index-1]}, includedSegments...)
		if excerptTextFits(candidate) {
			includedSegments = candidate
		}
	}
	if index+1 < len(segments) {
		candidate := append(append([]Segment(nil), includedSegments...), segments[index+1])
		if excerptTextFits(candidate) {
			includedSegments = candidate
		}
	}
	included := make([]int, 0, len(includedSegments))
	for _, item := range includedSegments {
		included = append(included, item.Sequence)
	}
	segment := segments[index]
	return TutorExcerpt{SearchHit: SearchHit{MaterialID: materialID, MaterialName: value.Name,
		MaterialKind: value.Kind, FileID: fileID, Sequence: sequence, ChapterLabel: segment.ChapterLabel,
		SectionLabel: segment.SectionLabel}, Text: joinSegmentText(includedSegments), IncludedSequences: included,
		IncludedSegments: includedSegments}, nil
}

func excerptTextFits(segments []Segment) bool {
	value := joinSegmentText(segments)
	return len(value) <= 16<<10 && utf8.RuneCountInString(value) <= 4_000
}

func joinSegmentText(segments []Segment) string {
	var builder strings.Builder
	for _, segment := range segments {
		if builder.Len() > 0 {
			builder.WriteString("\n\n")
		}
		builder.WriteString(segment.Text)
	}
	return builder.String()
}

var errStopSegmentScan = errors.New("stop material segment scan")

func (service *Service) scanSegments(materialID, fileID string, visit func(Segment) error) (returnErr error) {
	path, err := service.contentPath(materialID, fileID)
	if err != nil {
		return err
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open tutor material content: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, file.Close()) }()
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat tutor material content: %w", err)
	}
	if info.Size() < 1 || info.Size() > maxContentBytes {
		return ErrInvalid
	}
	scanner := bufio.NewScanner(io.LimitReader(file, maxContentBytes))
	scanner.Buffer(make([]byte, 64<<10), maxContentBytes)
	sequence := 0
	for scanner.Scan() {
		var segment Segment
		decoder := json.NewDecoder(bytes.NewReader(scanner.Bytes()))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&segment); err != nil {
			return ErrInvalid
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) || segment.Version != 1 ||
			segment.Sequence != sequence+1 || invalidText(segment.Text, 1, maxContentBytes, maxContentBytes) {
			return ErrInvalid
		}
		sequence = segment.Sequence
		if err := visit(segment); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil || sequence == 0 {
		return ErrInvalid
	}
	return nil
}

func normalizeTerms(values []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, value := range values {
		for _, term := range strings.FieldsFunc(normalizeSearchText(value), func(char rune) bool {
			return !unicode.IsLetter(char) && !unicode.IsNumber(char)
		}) {
			if term != "" && !seen[term] {
				seen[term] = true
				result = append(result, term)
			}
		}
	}
	sort.Strings(result)
	return result
}

func termScore(text string, terms []string) (int, int) {
	words := strings.FieldsFunc(normalizeSearchText(text), func(char rune) bool {
		return !unicode.IsLetter(char) && !unicode.IsNumber(char)
	})
	counts := make(map[string]int, len(words))
	for _, word := range words {
		counts[word]++
	}
	matched, frequency := 0, 0
	for _, term := range terms {
		if count := counts[term]; count > 0 {
			matched++
			frequency += count
		}
	}
	return matched, frequency
}

func normalizeSearchText(value string) string {
	return norm.NFC.String(cases.Fold().String(norm.NFC.String(value)))
}

func boundedText(value string, runes, bytes int) string {
	result := make([]rune, 0, min(utf8.RuneCountInString(value), runes))
	length := 0
	for _, char := range value {
		size := utf8.RuneLen(char)
		if len(result) == runes || length+size > bytes {
			break
		}
		result = append(result, char)
		length += size
	}
	return string(result)
}
