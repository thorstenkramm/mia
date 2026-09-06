package material

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/thorstenkramm/mia/internal/jobs"
	miSQLite "github.com/thorstenkramm/mia/internal/sqlite"
)

type scanner interface{ Scan(...any) error }

func closeRows(rows *sql.Rows, err error) error {
	return errors.Join(err, rows.Close())
}

func loadScoped(ctx context.Context, query miSQLite.Querier, materialID, actorID string, manage bool) (Material, error) {
	condition := `(
		EXISTS(SELECT 1 FROM course_supervisors cs WHERE cs.course_id = m.course_id AND cs.supervisor_user_id = ?)
		OR NOT ? AND m.scope = 'student-private' AND m.owner_user_id = ?
		OR NOT ? AND m.scope = 'course-wide' AND m.state = 'ready' AND m.is_approved = 1
			AND EXISTS(SELECT 1 FROM course_students st WHERE st.course_id = m.course_id AND st.student_user_id = ?))`
	row := query.QueryRowContext(ctx, `SELECT m.id, m.course_id, COALESCE(m.owner_user_id, ''), m.scope, m.name,
		m.kind, m.format, m.state, COALESCE(m.external_url, ''), m.brief_json, COALESCE(m.brief_source, ''),
		m.brief_updated_at, COALESCE(m.brief_updated_by, ''), m.is_approved, m.approved_at,
		COALESCE(m.approved_by, ''), COALESCE(m.failure_code, ''), m.created_at, m.updated_at
		FROM materials m WHERE m.id = ? AND `+condition, materialID, actorID, manage, actorID, manage, actorID)
	material, err := scanMaterial(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Material{}, ErrNotFound
	}
	return material, err
}

func loadScopedForDelete(ctx context.Context, query miSQLite.Querier, materialID, actorID string) (Material, error) {
	row := query.QueryRowContext(ctx, `SELECT m.id, m.course_id, COALESCE(m.owner_user_id, ''), m.scope, m.name,
		m.kind, m.format, m.state, COALESCE(m.external_url, ''), m.brief_json, COALESCE(m.brief_source, ''),
		m.brief_updated_at, COALESCE(m.brief_updated_by, ''), m.is_approved, m.approved_at,
		COALESCE(m.approved_by, ''), COALESCE(m.failure_code, ''), m.created_at, m.updated_at
		FROM materials m WHERE m.id = ? AND ((m.scope = 'course-wide' AND EXISTS(SELECT 1 FROM course_supervisors cs
		WHERE cs.course_id = m.course_id AND cs.supervisor_user_id = ?)) OR
		(m.scope = 'student-private' AND m.owner_user_id = ?))`, materialID, actorID, actorID)
	material, err := scanMaterial(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Material{}, ErrNotFound
	}
	return material, err
}

func scanMaterial(row scanner) (Material, error) {
	var material Material
	var brief sql.NullString
	var briefUpdated, approvedAt, updatedAt sql.NullString
	var approved int
	var createdAt string
	err := row.Scan(&material.ID, &material.CourseID, &material.OwnerUserID, &material.Scope, &material.Name,
		&material.Kind, &material.Format, &material.State, &material.ExternalURL, &brief, &material.BriefSource,
		&briefUpdated, &material.BriefUpdatedBy, &approved, &approvedAt, &material.ApprovedBy, &material.FailureCode,
		&createdAt, &updatedAt)
	if err != nil {
		return Material{}, err
	}
	material.Approved = approved != 0
	if brief.Valid {
		parsed, err := decodeBrief([]byte(brief.String))
		if err != nil {
			return Material{}, fmt.Errorf("decode stored material brief: %w", err)
		}
		material.Brief = cloneBrief(parsed)
	}
	var parseErr error
	material.CreatedAt, parseErr = parseInstant(createdAt)
	if parseErr != nil {
		return Material{}, parseErr
	}
	if material.BriefUpdatedAt, parseErr = parseOptionalInstant(briefUpdated); parseErr != nil {
		return Material{}, parseErr
	}
	if material.ApprovedAt, parseErr = parseOptionalInstant(approvedAt); parseErr != nil {
		return Material{}, parseErr
	}
	material.UpdatedAt, parseErr = parseOptionalInstant(updatedAt)
	return material, parseErr
}

func scanMaterials(rows *sql.Rows) ([]Material, error) {
	var materials []Material
	for rows.Next() {
		material, err := scanMaterial(rows)
		if err != nil {
			return nil, closeRows(rows, fmt.Errorf("scan material: %w", err))
		}
		materials = append(materials, material)
	}
	if err := rows.Err(); err != nil {
		return nil, closeRows(rows, fmt.Errorf("iterate materials: %w", err))
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return materials, nil
}

func authorizeCreate(ctx context.Context, query miSQLite.Querier, courseID, actorID, scope string) error {
	var allowed int
	if scope == "course-wide" {
		if err := query.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM course_supervisors
			WHERE course_id = ? AND supervisor_user_id = ?)`, courseID, actorID).Scan(&allowed); err != nil {
			return fmt.Errorf("authorize course-wide material: %w", err)
		}
	} else {
		if err := query.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM course_students
			WHERE course_id = ? AND student_user_id = ?)`, courseID, actorID).Scan(&allowed); err != nil {
			return fmt.Errorf("authorize private material: %w", err)
		}
	}
	if allowed == 0 {
		return ErrNotFound
	}
	return nil
}

func (service *Service) loadFileScoped(ctx context.Context, query miSQLite.Querier, fileID, actorID string) (File, Material, error) {
	var materialID string
	if err := query.QueryRowContext(ctx, "SELECT material_id FROM material_files WHERE id = ?", fileID).Scan(&materialID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return File{}, Material{}, ErrFileNotFound
		}
		return File{}, Material{}, err
	}
	material, err := loadScoped(ctx, query, materialID, actorID, false)
	if err != nil {
		return File{}, Material{}, ErrFileNotFound
	}
	file, err := loadFile(ctx, query, fileID)
	return file, material, err
}

func loadFile(ctx context.Context, query miSQLite.Querier, fileID string) (File, error) {
	var file File
	var createdAt string
	err := query.QueryRowContext(ctx, `SELECT id, material_id, original_filename, media_type, size_bytes,
		page_count, state, COALESCE(failure_code, ''), created_at FROM material_files WHERE id = ?`, fileID).
		Scan(&file.ID, &file.MaterialID, &file.OriginalFilename, &file.MediaType, &file.SizeBytes, &file.PageCount,
			&file.State, &file.FailureCode, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return File{}, ErrFileNotFound
	}
	if err != nil {
		return File{}, err
	}
	file.CreatedAt, err = parseInstant(createdAt)
	return file, err
}

func resetFailed(ctx context.Context, query miSQLite.Querier, materialID string) error {
	subjects, err := materialSubjectIDs(ctx, query, materialID)
	if err != nil {
		return err
	}
	if err := jobs.CancelSubjects(ctx, query, subjects, ""); err != nil {
		return fmt.Errorf("cancel failed material jobs: %w", err)
	}
	if _, err := query.ExecContext(ctx, `UPDATE material_files SET state = 'draft', failure_code = NULL
		WHERE material_id = ? AND state != 'draft'`, materialID); err != nil {
		return fmt.Errorf("reset material files: %w", err)
	}
	if _, err := query.ExecContext(ctx, `UPDATE materials SET state = 'draft', failure_code = NULL,
		brief_json = CASE WHEN brief_source = 'supervisor' THEN brief_json ELSE NULL END,
		brief_source = CASE WHEN brief_source = 'supervisor' THEN brief_source ELSE NULL END WHERE id = ? AND state = 'failed'`,
		materialID); err != nil {
		return fmt.Errorf("reset failed material: %w", err)
	}
	return nil
}

func materialSubjectIDs(ctx context.Context, query miSQLite.Querier, materialID string) ([]string, error) {
	rows, err := query.QueryContext(ctx, "SELECT id FROM material_files WHERE material_id = ?", materialID)
	if err != nil {
		return nil, err
	}
	subjects := []string{materialID}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, closeRows(rows, err)
		}
		subjects = append(subjects, id)
	}
	if err := rows.Err(); err != nil {
		return nil, closeRows(rows, err)
	}
	return subjects, rows.Close()
}

func (service *Service) sourcePath(materialID, fileID string) (string, error) {
	if !validPrefixedUUID(materialID, "mat_") || !validPrefixedUUID(fileID, "mf_") {
		return "", ErrFileNotFound
	}
	return filepath.Join(service.dataDir, "materials", materialID, "files", fileID, "file"), nil
}

func (service *Service) contentPath(materialID, fileID string) (string, error) {
	path, err := service.sourcePath(materialID, fileID)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(path), "content.jsonl"), nil
}

func validPrefixedUUID(value, prefix string) bool {
	parsed, err := uuid.Parse(strings.TrimPrefix(value, prefix))
	return err == nil && parsed.Version() == 4 && value == prefix+parsed.String()
}

func (service *Service) removeContentFiles(materialID string) {
	directory := filepath.Join(service.dataDir, "materials", materialID, "files")
	entries, err := os.ReadDir(directory)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if err := os.Remove(filepath.Join(directory, entry.Name(), "content.jsonl")); err != nil && !errors.Is(err, os.ErrNotExist) {
			service.logger.Warn("remove material content", "material_id", materialID, "error", err)
		}
	}
}

// ValidateFiles enforces startup integrity for database-referenced material files.
func (service *Service) ValidateFiles(ctx context.Context) error {
	rows, err := service.database.QueryContext(ctx, `SELECT f.id, f.material_id, f.state FROM material_files f
		JOIN materials m ON m.id = f.material_id ORDER BY f.material_id, f.id`)
	if err != nil {
		return fmt.Errorf("list material files for integrity check: %w", err)
	}
	for rows.Next() {
		var fileID, materialID, state string
		if err := rows.Scan(&fileID, &materialID, &state); err != nil {
			return closeRows(rows, fmt.Errorf("scan material integrity row: %w", err))
		}
		source, err := service.sourcePath(materialID, fileID)
		if err != nil {
			return closeRows(rows, err)
		}
		if info, err := os.Stat(source); err != nil || !info.Mode().IsRegular() {
			return closeRows(rows, errors.New("database-referenced material source is missing"))
		}
		content, err := service.contentPath(materialID, fileID)
		if err != nil {
			return closeRows(rows, err)
		}
		if state == "processed" {
			data, err := os.ReadFile(content)
			if err != nil {
				return closeRows(rows, errors.New("database-referenced material content is missing"))
			}
			if _, err := decodeSegments(data); err != nil {
				return closeRows(rows, fmt.Errorf("database-referenced material content is invalid: %w", err))
			}
		} else {
			if err := os.Remove(content); err != nil && !errors.Is(err, os.ErrNotExist) {
				return closeRows(rows, fmt.Errorf("remove incomplete material content: %w", err))
			}
		}
	}
	if err := rows.Err(); err != nil {
		return closeRows(rows, fmt.Errorf("iterate material integrity rows: %w", err))
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close material integrity rows: %w", err)
	}
	return nil
}

// ReconcileOrphans removes material directories without owning rows after all
// required-file validation has succeeded.
func (service *Service) ReconcileOrphans(ctx context.Context) error {
	knownMaterials := make(map[string]bool)
	knownFiles := make(map[string]map[string]bool)
	fileRows, err := service.database.QueryContext(ctx, "SELECT id, material_id FROM material_files")
	if err != nil {
		return fmt.Errorf("list material files for reconciliation: %w", err)
	}
	for fileRows.Next() {
		var fileID, materialID string
		if err := fileRows.Scan(&fileID, &materialID); err != nil {
			return closeRows(fileRows, err)
		}
		if knownFiles[materialID] == nil {
			knownFiles[materialID] = make(map[string]bool)
		}
		knownFiles[materialID][fileID] = true
	}
	if err := fileRows.Err(); err != nil {
		return closeRows(fileRows, err)
	}
	if err := fileRows.Close(); err != nil {
		return err
	}
	materialRows, err := service.database.QueryContext(ctx, "SELECT id FROM materials")
	if err != nil {
		return fmt.Errorf("list materials for reconciliation: %w", err)
	}
	for materialRows.Next() {
		var id string
		if err := materialRows.Scan(&id); err != nil {
			return closeRows(materialRows, err)
		}
		knownMaterials[id] = true
	}
	if err := materialRows.Err(); err != nil {
		return closeRows(materialRows, err)
	}
	if err := materialRows.Close(); err != nil {
		return err
	}
	root := filepath.Join(service.dataDir, "materials")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return fmt.Errorf("create materials directory: %w", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("read materials directory: %w", err)
	}
	for _, entry := range entries {
		if !knownMaterials[entry.Name()] {
			if err := os.RemoveAll(filepath.Join(root, entry.Name())); err != nil {
				return fmt.Errorf("remove orphan material directory: %w", err)
			}
			continue
		}
		filesRoot := filepath.Join(root, entry.Name(), "files")
		fileEntries, err := os.ReadDir(filesRoot)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("read material file directories: %w", err)
		}
		for _, fileEntry := range fileEntries {
			if !knownFiles[entry.Name()][fileEntry.Name()] {
				if err := os.RemoveAll(filepath.Join(filesRoot, fileEntry.Name())); err != nil {
					return fmt.Errorf("remove orphan material file directory: %w", err)
				}
			}
		}
	}
	return nil
}

// Reconcile validates required material files before removing filesystem orphans.
func (service *Service) Reconcile(ctx context.Context) error {
	if err := service.ValidateFiles(ctx); err != nil {
		return err
	}
	return service.ReconcileOrphans(ctx)
}

func validScope(value string) bool { return value == "course-wide" || value == "student-private" }
func validKind(value string) bool {
	return value == "text-book" || value == "exam" || value == "worksheet" || value == "website" || value == "youtube"
}
func validFormat(value string) bool {
	return value == "pdf" || value == "jpeg" || value == "png" || value == "text" || value == "markdown" ||
		value == "docx" || value == "link"
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableWhen(condition bool, value string) any {
	if !condition {
		return nil
	}
	return value
}

func nullableInstant(condition bool, value time.Time) any {
	if !condition {
		return nil
	}
	return instant(value)
}

func parseOptionalInstant(value sql.NullString) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}
	parsed, err := parseInstant(value.String)
	return &parsed, err
}

func parseInstant(value string) (time.Time, error) {
	parsed, err := time.Parse("2006-01-02T15:04:05.000000Z", value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse stored material instant: %w", err)
	}
	return parsed, nil
}

func instant(value time.Time) string { return value.UTC().Format("2006-01-02T15:04:05.000000Z") }
