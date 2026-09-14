package material

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/thorstenkramm/mia/internal/audit"
	"github.com/thorstenkramm/mia/internal/filepublish"
	"github.com/thorstenkramm/mia/internal/identity"
	"github.com/thorstenkramm/mia/internal/jobs"
	"github.com/thorstenkramm/mia/internal/provider/mistral"
	"github.com/thorstenkramm/mia/internal/provider/openai"
	miSQLite "github.com/thorstenkramm/mia/internal/sqlite"
)

type OCRClient interface {
	OCR(context.Context, string, []byte) (mistral.Result, error)
}

type SummaryClient interface {
	Structured(context.Context, string, []string, map[string]any) (openai.Result, error)
}

type SelectedCheck func(context.Context, miSQLite.Querier, string) (bool, error)

// Service is safe for concurrent use after construction.
type Service struct {
	database *sql.DB
	dataDir  string
	limits   Limits
	ocr      OCRClient
	summary  SummaryClient
	selected SelectedCheck
	logger   *slog.Logger
}

func NewService(database *sql.DB, dataDir string, limits Limits, ocr OCRClient, summary SummaryClient,
	selected SelectedCheck, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{database: database, dataDir: dataDir, limits: limits, ocr: ocr, summary: summary,
		selected: selected, logger: logger}
}

func (service *Service) AuditMutationDenied(ctx context.Context, actorID, outcome string) {
	if err := miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		return audit.WriteWithMetadata(ctx, tx, audit.ActionMaterialMutationDenied, actorID, "",
			audit.Metadata{OutcomeCode: outcome})
	}); err != nil {
		service.logger.WarnContext(ctx, "audit denied material mutation", "error", err)
	}
}

func (service *Service) Create(ctx context.Context, input CreateInput) (Material, error) {
	name, err := validateName(input.Name)
	linkKind := input.Kind == "website" || input.Kind == "youtube"
	if err != nil || !validScope(input.Scope) || !validKind(input.Kind) || !validFormat(input.Format) ||
		(input.Format == "link") != linkKind || input.Format == "link" && input.Scope != "course-wide" ||
		input.Format != "link" && input.ExternalURL != "" {
		return Material{}, ErrInvalid
	}
	if input.Format == "link" {
		input.ExternalURL, err = validateURL(input.ExternalURL, input.Kind)
		if err != nil || input.Brief == nil {
			return Material{}, ErrInvalid
		}
		normalized, normalizeErr := normalizeBrief(*input.Brief)
		if normalizeErr != nil || validateBrief(normalized) != nil {
			return Material{}, ErrInvalid
		}
		input.Brief = &normalized
	} else if input.Brief != nil {
		return Material{}, ErrInvalid
	}
	materialID := "mat_" + uuid.NewString()
	now := time.Now()
	ownerID := ""
	if input.Scope == "student-private" {
		ownerID = input.ActorID
	}
	err = miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		if err := authorizeCreate(ctx, tx, input.CourseID, input.ActorID, input.Scope); err != nil {
			return err
		}
		var briefJSON any
		var briefSource any
		if input.Brief != nil {
			encoded, marshalErr := json.Marshal(input.Brief)
			if marshalErr != nil {
				return fmt.Errorf("encode material brief: %w", marshalErr)
			}
			briefJSON, briefSource = string(encoded), "supervisor"
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO materials
			(id, course_id, owner_user_id, scope, name, name_normalized, kind, format, external_url,
			 brief_json, brief_source, brief_updated_at, brief_updated_by, created_at, created_by)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, materialID, input.CourseID, nullable(ownerID),
			input.Scope, name, identity.NameKey(name), input.Kind, input.Format, nullable(input.ExternalURL), briefJSON,
			briefSource, nullableInstant(input.Brief != nil, now), nullableWhen(input.Brief != nil, input.ActorID),
			instant(now), input.ActorID)
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE constraint failed: materials.course_id, materials.name_normalized") {
				return ErrNameTaken
			}
			return fmt.Errorf("insert material: %w", err)
		}
		return audit.WriteWithMetadata(ctx, tx, audit.ActionMaterialCreated, input.ActorID, "",
			audit.Metadata{CourseID: input.CourseID, MaterialID: materialID})
	})
	if err != nil {
		return Material{}, err
	}
	return service.Get(ctx, materialID, input.ActorID)
}

func (service *Service) Get(ctx context.Context, materialID, actorID string) (Material, error) {
	return loadScoped(ctx, service.database, materialID, actorID, false)
}

func (service *Service) List(ctx context.Context, courseID, actorID string, input ListInput) (ListResult, error) {
	if input.Limit < 1 || input.Limit > 100 || input.Offset < 0 || input.Offset > 10_000 {
		return ListResult{}, ErrInvalid
	}
	var courseVisible int
	if err := service.database.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM courses c WHERE c.id = ? AND
		(EXISTS(SELECT 1 FROM course_supervisors cs WHERE cs.course_id = c.id AND cs.supervisor_user_id = ?)
		 OR EXISTS(SELECT 1 FROM course_students st WHERE st.course_id = c.id AND st.student_user_id = ?)))`,
		courseID, actorID, actorID).Scan(&courseVisible); err != nil {
		return ListResult{}, fmt.Errorf("authorize material list: %w", err)
	}
	if courseVisible == 0 {
		return ListResult{}, ErrNotFound
	}
	rows, err := service.database.QueryContext(ctx, `SELECT m.id, m.course_id, COALESCE(m.owner_user_id, ''),
		m.scope, m.name, m.kind, m.format, m.state, COALESCE(m.external_url, ''), m.brief_json,
		COALESCE(m.brief_source, ''), m.brief_updated_at, COALESCE(m.brief_updated_by, ''), m.is_approved,
		m.approved_at, COALESCE(m.approved_by, ''), COALESCE(m.failure_code, ''), m.created_at, m.updated_at
		FROM materials m WHERE m.course_id = ? AND (
		EXISTS(SELECT 1 FROM course_supervisors cs WHERE cs.course_id = m.course_id AND cs.supervisor_user_id = ?)
		OR m.scope = 'student-private' AND m.owner_user_id = ?
		OR m.scope = 'course-wide' AND m.state = 'ready' AND m.is_approved = 1
			AND EXISTS(SELECT 1 FROM course_students st WHERE st.course_id = m.course_id AND st.student_user_id = ?))
		ORDER BY m.created_at DESC, m.id DESC LIMIT ? OFFSET ?`, courseID, actorID, actorID, actorID,
		input.Limit+1, input.Offset)
	if err != nil {
		return ListResult{}, fmt.Errorf("list materials: %w", err)
	}
	materials, err := scanMaterials(rows)
	if err != nil {
		return ListResult{}, err
	}
	hasMore := len(materials) > input.Limit
	if hasMore {
		materials = materials[:input.Limit]
	}
	return ListResult{Materials: materials, HasMore: hasMore}, nil
}

func (service *Service) Upload(ctx context.Context, materialID, actorID, filename string, source []byte) (File, error) {
	filename = filepath.Base(strings.TrimSpace(filename))
	if validateFilename(filename) != nil {
		return File{}, ErrInvalid
	}
	material, err := loadScopedForDelete(ctx, service.database, materialID, actorID)
	if err != nil {
		return File{}, err
	}
	var files, pages int
	var bytesTotal int64
	err = service.database.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM material_files f WHERE f.material_id = m.id),
		COALESCE((SELECT SUM(f.size_bytes) FROM material_files f WHERE f.material_id = m.id), 0),
		COALESCE((SELECT SUM(f.page_count) FROM material_files f WHERE f.material_id = m.id), 0)
		FROM materials m WHERE m.id = ?`, materialID).Scan(&files, &bytesTotal, &pages)
	if errors.Is(err, sql.ErrNoRows) {
		return File{}, ErrNotFound
	}
	if err != nil {
		return File{}, fmt.Errorf("load material upload limits: %w", err)
	}
	mediaType, pageCount, normalized, err := validateSource(material.Format, source, service.limits)
	if err != nil || files+1 > service.limits.MaxFiles || bytesTotal+int64(len(normalized)) > service.limits.MaxMaterialBytes ||
		pages+pageCount > service.limits.MaxPages {
		return File{}, ErrInvalid
	}
	file := File{ID: "mf_" + uuid.NewString(), MaterialID: materialID, OriginalFilename: filename,
		MediaType: mediaType, SizeBytes: int64(len(normalized)), PageCount: pageCount, State: "draft", CreatedAt: time.Now()}
	path, err := service.sourcePath(materialID, file.ID)
	if err != nil {
		return File{}, err
	}
	change, err := filepublish.Replace(path, normalized)
	if err != nil {
		return File{}, err
	}
	err = miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		current, err := loadScopedForDelete(ctx, tx, materialID, actorID)
		if err != nil {
			return err
		}
		if current.Format == "link" || current.State != "draft" && current.State != "failed" {
			return ErrInvalidState
		}
		if current.State == "failed" {
			if err := resetFailed(ctx, tx, materialID); err != nil {
				return err
			}
		}
		var currentFiles, currentPages int
		var currentBytes int64
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(size_bytes), 0),
			COALESCE(SUM(page_count), 0) FROM material_files WHERE material_id = ?`, materialID).
			Scan(&currentFiles, &currentBytes, &currentPages); err != nil {
			return fmt.Errorf("recheck material upload limits: %w", err)
		}
		if currentFiles+1 > service.limits.MaxFiles || currentBytes+file.SizeBytes > service.limits.MaxMaterialBytes ||
			currentPages+file.PageCount > service.limits.MaxPages {
			return ErrInvalid
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO material_files
			(id, material_id, original_filename, media_type, size_bytes, page_count, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, file.ID, materialID, filename, mediaType, file.SizeBytes, pageCount,
			instant(file.CreatedAt))
		if err != nil {
			return fmt.Errorf("insert material file: %w", err)
		}
		return audit.WriteWithMetadata(ctx, tx, audit.ActionMaterialFileUploaded, actorID, "",
			audit.Metadata{CourseID: current.CourseID, MaterialID: materialID})
	})
	if err != nil {
		return File{}, errors.Join(err, change.Finish(false))
	}
	if material.State == "failed" {
		service.removeContentFiles(materialID)
	}
	return file, change.Finish(true)
}

func (service *Service) Finalize(ctx context.Context, materialID, actorID string) (Material, error) {
	err := miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		material, err := loadScopedForDelete(ctx, tx, materialID, actorID)
		if err != nil {
			return err
		}
		if material.State != "draft" && material.State != "failed" {
			return ErrInvalidState
		}
		if material.Format == "link" {
			if material.Scope != "course-wide" || material.Brief == nil || validateBrief(*material.Brief) != nil {
				return ErrInvalid
			}
			_, err := tx.ExecContext(ctx, `UPDATE materials SET state = 'ready', failure_code = NULL, updated_at = ?
				WHERE id = ? AND state IN ('draft', 'failed')`, instant(time.Now()), materialID)
			if err != nil {
				return err
			}
			return audit.WriteWithMetadata(ctx, tx, audit.ActionMaterialFinalized, actorID, "",
				audit.Metadata{CourseID: material.CourseID, MaterialID: materialID})
		}
		rows, err := tx.QueryContext(ctx, `SELECT id FROM material_files WHERE material_id = ?
			ORDER BY created_at, id`, materialID)
		if err != nil {
			return fmt.Errorf("list files for finalization: %w", err)
		}
		fileIDs, err := miSQLite.ScanStrings(rows)
		if err != nil {
			return fmt.Errorf("collect finalization files: %w", err)
		}
		if len(fileIDs) == 0 {
			return ErrInvalid
		}
		if err := resetFailed(ctx, tx, materialID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE materials SET state = 'processing', failure_code = NULL,
			updated_at = ? WHERE id = ?`, instant(time.Now()), materialID); err != nil {
			return fmt.Errorf("freeze material: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE material_files SET state = 'processing', failure_code = NULL
			WHERE material_id = ?`, materialID); err != nil {
			return fmt.Errorf("freeze material files: %w", err)
		}
		for _, fileID := range fileIDs {
			if _, err := jobs.Enqueue(ctx, tx, "material-extraction", "material-file", fileID, material.CourseID,
				material.OwnerUserID); err != nil {
				return err
			}
		}
		return audit.WriteWithMetadata(ctx, tx, audit.ActionMaterialFinalized, actorID, "",
			audit.Metadata{CourseID: material.CourseID, MaterialID: materialID})
	})
	if err != nil {
		return Material{}, err
	}
	return service.Get(ctx, materialID, actorID)
}

func (service *Service) CorrectBrief(ctx context.Context, materialID, actorID string, brief Brief) (Material, error) {
	brief, err := normalizeBrief(brief)
	if err != nil || validateBrief(brief) != nil {
		return Material{}, ErrInvalid
	}
	encoded, err := json.Marshal(brief)
	if err != nil {
		return Material{}, fmt.Errorf("encode corrected brief: %w", err)
	}
	err = miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		material, err := loadScoped(ctx, tx, materialID, actorID, true)
		if err != nil {
			return err
		}
		if material.Scope != "course-wide" || material.State != "ready" || material.Format == "link" && material.Brief == nil {
			return ErrInvalidState
		}
		current, err := json.Marshal(material.Brief)
		if err != nil {
			return fmt.Errorf("encode stored brief: %w", err)
		}
		if string(current) == string(encoded) {
			return nil
		}
		now := instant(time.Now())
		_, err = tx.ExecContext(ctx, `UPDATE materials SET brief_json = ?, brief_source = 'supervisor',
			brief_updated_at = ?, brief_updated_by = ?, is_approved = 0, approved_at = NULL, approved_by = NULL,
			updated_at = ? WHERE id = ?`, string(encoded), now, actorID, now, materialID)
		if err != nil {
			return fmt.Errorf("correct material brief: %w", err)
		}
		metadata := audit.Metadata{CourseID: material.CourseID, MaterialID: materialID}
		if err := audit.WriteWithMetadata(ctx, tx, audit.ActionMaterialBriefCorrected, actorID, "", metadata); err != nil {
			return err
		}
		if material.Approved {
			return audit.WriteWithMetadata(ctx, tx, audit.ActionMaterialApprovalRevoked, actorID, "", metadata)
		}
		return nil
	})
	if err != nil {
		return Material{}, err
	}
	return service.Get(ctx, materialID, actorID)
}

func (service *Service) SetApproval(ctx context.Context, materialID, actorID string, approved bool) (Material, error) {
	err := miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		material, err := loadScoped(ctx, tx, materialID, actorID, true)
		if err != nil {
			return err
		}
		if material.Scope != "course-wide" || material.State != "ready" || material.Brief == nil {
			return ErrInvalidState
		}
		if material.Approved == approved {
			return nil
		}
		var approvedAt, approvedBy any
		if approved {
			approvedAt, approvedBy = instant(time.Now()), actorID
		}
		_, err = tx.ExecContext(ctx, `UPDATE materials SET is_approved = ?, approved_at = ?, approved_by = ?,
			updated_at = ? WHERE id = ?`, approved, approvedAt, approvedBy, instant(time.Now()), materialID)
		if err != nil {
			return fmt.Errorf("set material approval: %w", err)
		}
		action := audit.ActionMaterialApprovalRevoked
		if approved {
			action = audit.ActionMaterialApproved
		}
		return audit.WriteWithMetadata(ctx, tx, action, actorID, "",
			audit.Metadata{CourseID: material.CourseID, MaterialID: materialID})
	})
	if err != nil {
		return Material{}, err
	}
	return service.Get(ctx, materialID, actorID)
}

func (service *Service) Delete(ctx context.Context, materialID, actorID string) error {
	var directory string
	err := miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		material, err := loadScopedForDelete(ctx, tx, materialID, actorID)
		if err != nil {
			return err
		}
		if service.selected != nil {
			selected, err := service.selected(ctx, tx, materialID)
			if err != nil {
				return err
			}
			if selected {
				return ErrFileSelected
			}
		}
		subjects, err := materialSubjectIDs(ctx, tx, materialID)
		if err != nil {
			return err
		}
		if err := jobs.DeleteSubjects(ctx, tx, subjects); err != nil {
			return err
		}
		directory = filepath.Join(service.dataDir, "materials", materialID)
		if _, err := tx.ExecContext(ctx, "DELETE FROM materials WHERE id = ?", materialID); err != nil {
			return fmt.Errorf("delete material: %w", err)
		}
		return audit.WriteWithMetadata(ctx, tx, audit.ActionMaterialDeleted, actorID, "",
			audit.Metadata{CourseID: material.CourseID, MaterialID: materialID})
	})
	if err == nil && directory != "" {
		if removeErr := os.RemoveAll(directory); removeErr != nil {
			service.logger.WarnContext(ctx, "remove deleted material files", "material_id", materialID, "error", removeErr)
		}
	}
	return err
}

func (service *Service) OpenFile(ctx context.Context, fileID, actorID string, content bool) (*os.File, File, error) {
	file, material, err := service.loadFileScoped(ctx, service.database, fileID, actorID)
	if err != nil {
		return nil, File{}, err
	}
	path, err := service.sourcePath(material.ID, file.ID)
	if content {
		if file.State != "processed" || material.State != "ready" {
			return nil, File{}, ErrFileNotFound
		}
		path, err = service.contentPath(material.ID, file.ID)
	}
	if err != nil {
		return nil, File{}, err
	}
	opened, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, File{}, ErrFileNotFound
	}
	if err != nil {
		return nil, File{}, fmt.Errorf("open material file: %w", err)
	}
	info, err := opened.Stat()
	if err != nil {
		return nil, File{}, errors.Join(fmt.Errorf("stat material file: %w", err), opened.Close())
	}
	if !info.Mode().IsRegular() {
		return nil, File{}, errors.Join(ErrFileNotFound, opened.Close())
	}
	return opened, file, nil
}

func (service *Service) Files(ctx context.Context, materialID, actorID string) ([]File, error) {
	if _, err := loadScoped(ctx, service.database, materialID, actorID, false); err != nil {
		return nil, err
	}
	rows, err := service.database.QueryContext(ctx, `SELECT id, material_id, original_filename, media_type,
		size_bytes, page_count, state, COALESCE(failure_code, ''), created_at FROM material_files
		WHERE material_id = ? ORDER BY created_at, id`, materialID)
	if err != nil {
		return nil, fmt.Errorf("list material files: %w", err)
	}
	var files []File
	for rows.Next() {
		var file File
		var created string
		if err := rows.Scan(&file.ID, &file.MaterialID, &file.OriginalFilename, &file.MediaType, &file.SizeBytes,
			&file.PageCount, &file.State, &file.FailureCode, &created); err != nil {
			return nil, closeRows(rows, fmt.Errorf("scan material file: %w", err))
		}
		file.CreatedAt, err = parseInstant(created)
		if err != nil {
			return nil, closeRows(rows, err)
		}
		files = append(files, file)
	}
	if err := rows.Err(); err != nil {
		return nil, closeRows(rows, fmt.Errorf("iterate material files: %w", err))
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return files, nil
}

func (service *Service) GetFile(ctx context.Context, fileID, actorID string) (File, error) {
	file, _, err := service.loadFileScoped(ctx, service.database, fileID, actorID)
	return file, err
}

func (service *Service) RequireAssignedSupervisor(ctx context.Context, materialID, actorID string) error {
	var allowed int
	err := service.database.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM materials m
		JOIN course_supervisors cs ON cs.course_id = m.course_id WHERE m.id = ? AND cs.supervisor_user_id = ?)`,
		materialID, actorID).Scan(&allowed)
	if err != nil {
		return fmt.Errorf("authorize material job oversight: %w", err)
	}
	if allowed == 0 {
		return ErrNotFound
	}
	return nil
}

func (service *Service) DeleteFile(ctx context.Context, fileID, actorID string) error {
	var directory string
	var resetMaterial string
	err := miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		file, err := loadFile(ctx, tx, fileID)
		if err != nil {
			return err
		}
		material, err := loadScopedForDelete(ctx, tx, file.MaterialID, actorID)
		if err != nil {
			return ErrFileNotFound
		}
		if material.State != "draft" && material.State != "failed" {
			return ErrInvalidState
		}
		if material.State == "failed" {
			if err := resetFailed(ctx, tx, material.ID); err != nil {
				return err
			}
			resetMaterial = material.ID
		}
		if err := jobs.DeleteSubjects(ctx, tx, []string{fileID}); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM material_files WHERE id = ?", fileID); err != nil {
			return fmt.Errorf("delete material file: %w", err)
		}
		directory = filepath.Join(service.dataDir, "materials", material.ID, "files", fileID)
		return audit.WriteWithMetadata(ctx, tx, audit.ActionMaterialFileDeleted, actorID, "",
			audit.Metadata{CourseID: material.CourseID, MaterialID: material.ID})
	})
	if err != nil {
		return err
	}
	if resetMaterial != "" {
		service.removeContentFiles(resetMaterial)
	}
	if err := os.RemoveAll(directory); err != nil {
		service.logger.WarnContext(ctx, "remove material file directory", "file_id", fileID, "error", err)
	}
	return nil
}

func MaterialReady(ctx context.Context, query miSQLite.Querier, courseID string) (bool, error) {
	var ready int
	err := query.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM materials WHERE course_id = ?
		AND scope = 'course-wide' AND state = 'ready' AND is_approved = 1 AND format != 'link'
		AND EXISTS(SELECT 1 FROM material_files WHERE material_id = materials.id AND state = 'processed'))`,
		courseID).Scan(&ready)
	return ready != 0, err
}

func (service *Service) DeleteCourseData(ctx context.Context, query miSQLite.Querier, courseID string) error {
	_, err := query.ExecContext(ctx, "DELETE FROM materials WHERE course_id = ?", courseID)
	return err
}

func (service *Service) DeleteStudentCourseData(ctx context.Context, query miSQLite.Querier, courseID, studentID string) error {
	return deleteMaterials(ctx, query, "course_id = ? AND owner_user_id = ?", courseID, studentID)
}

func (service *Service) CourseDeletionImpact(ctx context.Context, query miSQLite.Querier,
	courseID string) ([]string, error) {
	return materialDeletionImpact(ctx, query, "course_id = ?", courseID)
}

func (service *Service) StudentCourseDeletionImpact(ctx context.Context, query miSQLite.Querier, courseID,
	studentID string) ([]string, error) {
	return materialDeletionImpact(ctx, query, "course_id = ? AND owner_user_id = ?", courseID, studentID)
}

func materialDeletionImpact(ctx context.Context, query miSQLite.Querier, condition string, args ...any) ([]string, error) {
	rows, err := query.QueryContext(ctx, "SELECT id FROM materials WHERE "+condition+" ORDER BY id", args...)
	if err != nil {
		return nil, fmt.Errorf("list deletion-impact materials: %w", err)
	}
	materialIDs, err := miSQLite.ScanStrings(rows)
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(materialIDs))
	var subjects []string
	for _, materialID := range materialIDs {
		result = append(result, "material:"+materialID)
		ids, impactErr := materialSubjectIDs(ctx, query, materialID)
		if impactErr != nil {
			return nil, impactErr
		}
		subjects = append(subjects, ids...)
		for _, id := range ids[1:] {
			result = append(result, "file:"+id)
		}
	}
	jobImpact, err := jobs.DeletionImpact(ctx, query, subjects)
	if err != nil {
		return nil, err
	}
	return append(result, jobImpact...), nil
}

func (service *Service) DeleteAccountData(ctx context.Context, query miSQLite.Querier, accountID string) error {
	return deleteMaterials(ctx, query, "owner_user_id = ?", accountID)
}

// deleteMaterials removes the matching materials after discarding the job
// subjects owned by each material and its files.
func deleteMaterials(ctx context.Context, query miSQLite.Querier, condition string, args ...any) error {
	rows, err := query.QueryContext(ctx, "SELECT id FROM materials WHERE "+condition, args...)
	if err != nil {
		return err
	}
	materialIDs, err := miSQLite.ScanStrings(rows)
	if err != nil {
		return err
	}
	for _, materialID := range materialIDs {
		subjects, err := materialSubjectIDs(ctx, query, materialID)
		if err != nil {
			return err
		}
		if err := jobs.DeleteSubjects(ctx, query, subjects); err != nil {
			return err
		}
	}
	_, err = query.ExecContext(ctx, "DELETE FROM materials WHERE "+condition, args...)
	return err
}

func (service *Service) CleanupCourseData(ctx context.Context, _ string) error {
	return service.Reconcile(ctx)
}

func (service *Service) CleanupStudentCourseData(ctx context.Context, _, _ string) error {
	return service.Reconcile(ctx)
}

func (service *Service) CleanupAccountData(ctx context.Context, _ string) error {
	return service.Reconcile(ctx)
}
