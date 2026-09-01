// Package course owns courses, supervisor assignments, and course lifecycle operations.
package course

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"golang.org/x/text/unicode/norm"

	"github.com/thorstenkramm/mia/internal/audit"
	"github.com/thorstenkramm/mia/internal/filepublish"
	"github.com/thorstenkramm/mia/internal/identity"
	"github.com/thorstenkramm/mia/internal/lifecycle"
	miSQLite "github.com/thorstenkramm/mia/internal/sqlite"
	"github.com/thorstenkramm/mia/internal/user"
)

var (
	ErrNotFound              = errors.New("course not found")
	ErrUnauthorized          = errors.New("course action unauthorized")
	ErrInvalid               = errors.New("invalid course")
	ErrNameTaken             = errors.New("course name taken")
	ErrSupervisorInvalid     = errors.New("course supervisor invalid")
	ErrLastSupervisor        = errors.New("course requires a supervisor")
	ErrActivationUnavailable = errors.New("course activation requirements not met")
	ErrInvalidState          = errors.New("course state does not allow action")
	ErrActiveSession         = errors.New("course has an active tutoring session")
	ErrLogoNotFound          = errors.New("course logo not found")
)

const (
	maxNameRunes        = 200
	maxNameBytes        = 800
	maxDescriptionRunes = 4_000
	maxDescriptionBytes = 16 << 10
	maxContextRunes     = 16_000
	maxContextBytes     = 64 << 10
)

// MaterialReadiness is the material owner's transaction-aware activation gate.
type MaterialReadiness func(context.Context, miSQLite.Querier, string) (bool, error)

// ActiveSessionCheck is the tutoring owner's transaction-aware course-deletion gate.
type ActiveSessionCheck func(context.Context, miSQLite.Querier, string) (bool, error)

type OptionalString struct {
	Set   bool
	Value *string
}

type Fields struct {
	Name, Description, Curriculum, LearningGoals, Instructions, Language OptionalString
}

type CreateInput struct {
	Fields
	SupervisorIDs []string
	ActorID       string
}

type ListInput struct {
	Limit, Offset int
}

type ListResult struct {
	Courses []Course
	HasMore bool
}

type Course struct {
	ID, Name                               string
	Description, Curriculum, LearningGoals *string
	Instructions, Language                 *string
	Active                                 bool
	HasLogo                                bool
	CreatedAt                              time.Time
	ActivatedAt, DeactivatedAt, UpdatedAt  *time.Time
	SupervisorIDs                          []string
}

// Service is safe for concurrent use after construction.
type Service struct {
	database       *sql.DB
	dataDir        string
	lifecycle      *lifecycle.Registry
	materialReady  MaterialReadiness
	activeSessions ActiveSessionCheck
	logger         *slog.Logger
	logoMu         sync.RWMutex
}

func NewService(database *sql.DB, dataDir string, registry *lifecycle.Registry, readiness MaterialReadiness,
	activeSessions ActiveSessionCheck, logger *slog.Logger) *Service {
	if registry == nil {
		registry = &lifecycle.Registry{}
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{database: database, dataDir: dataDir, lifecycle: registry, materialReady: readiness,
		activeSessions: activeSessions, logger: logger}
}

func (service *Service) AuditMutationDenied(ctx context.Context, actorID, outcome string) {
	err := miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		return audit.WriteWithMetadata(ctx, tx, audit.ActionCourseMutationDenied, actorID, "",
			audit.Metadata{OutcomeCode: outcome})
	})
	if err != nil {
		service.logger.WarnContext(ctx, "audit denied course mutation", "error", err)
	}
}

func (service *Service) Create(ctx context.Context, input CreateInput) (Course, error) {
	fields, err := normalizeFields(input.Fields, true)
	if err != nil || len(input.SupervisorIDs) == 0 {
		return Course{}, ErrInvalid
	}
	supervisors := uniqueStrings(input.SupervisorIDs)
	if len(supervisors) != len(input.SupervisorIDs) {
		return Course{}, ErrSupervisorInvalid
	}
	id := "cou_" + uuid.NewString()
	now := time.Now()
	err = miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		administrator, err := user.HasRole(ctx, tx, input.ActorID, user.Administrator)
		if err != nil {
			return err
		}
		if !administrator {
			return ErrUnauthorized
		}
		for _, supervisorID := range supervisors {
			hasRole, err := user.HasRole(ctx, tx, supervisorID, user.Supervisor)
			if err != nil {
				return err
			}
			if !hasRole {
				return ErrSupervisorInvalid
			}
		}
		name := *fields.Name.Value
		_, err = tx.ExecContext(ctx, `INSERT INTO courses
			(id, name, name_normalized, description, curriculum, learning_goals, llm_instructions, language,
			 created_at, created_by) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, name, identity.NameKey(name),
			nullable(fields.Description.Value), nullable(fields.Curriculum.Value), nullable(fields.LearningGoals.Value),
			nullable(fields.Instructions.Value), nullable(fields.Language.Value), instant(now), input.ActorID)
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE constraint failed: courses.name_normalized") {
				return ErrNameTaken
			}
			return fmt.Errorf("insert course: %w", err)
		}
		for _, supervisorID := range supervisors {
			if _, err := tx.ExecContext(ctx, `INSERT INTO course_supervisors
				(course_id, supervisor_user_id, assigned_at, assigned_by) VALUES (?, ?, ?, ?)`, id, supervisorID,
				instant(now), input.ActorID); err != nil {
				return fmt.Errorf("assign initial course supervisor: %w", err)
			}
		}
		return audit.WriteWithMetadata(ctx, tx, audit.ActionCourseCourseCreated, input.ActorID, "",
			audit.Metadata{CourseID: id})
	})
	if err != nil {
		return Course{}, err
	}
	return service.Get(ctx, id, input.ActorID)
}

func (service *Service) Get(ctx context.Context, courseID, actorID string) (Course, error) {
	course, err := loadScoped(ctx, service.database, courseID, actorID, false)
	if err != nil {
		return Course{}, err
	}
	course.SupervisorIDs, err = supervisorIDs(ctx, service.database, courseID)
	if err == nil {
		course.HasLogo = service.logoExists(courseID)
	}
	return course, err
}

func (service *Service) Supervisors(ctx context.Context, courseID, actorID string) ([]string, error) {
	if _, err := loadScoped(ctx, service.database, courseID, actorID, false); err != nil {
		return nil, err
	}
	return supervisorIDs(ctx, service.database, courseID)
}

func (service *Service) List(ctx context.Context, actorID string, input ListInput) (ListResult, error) {
	if input.Limit < 1 || input.Limit > 100 || input.Offset < 0 || input.Offset > 10_000 {
		return ListResult{}, ErrInvalid
	}
	administrator, err := user.HasRole(ctx, service.database, actorID, user.Administrator)
	if err != nil {
		return ListResult{}, err
	}
	query := `SELECT c.id, c.name, c.description, c.curriculum, c.learning_goals, c.llm_instructions, c.language,
		c.is_active, c.created_at, c.activated_at, c.deactivated_at, c.updated_at FROM courses c
		WHERE EXISTS(SELECT 1 FROM course_supervisors cs WHERE cs.course_id = c.id AND cs.supervisor_user_id = ?)
		ORDER BY c.name_normalized, c.id LIMIT ? OFFSET ?`
	args := []any{actorID, input.Limit + 1, input.Offset}
	if administrator {
		query = `SELECT c.id, c.name, c.description, c.curriculum, c.learning_goals, c.llm_instructions, c.language,
			c.is_active, c.created_at, c.activated_at, c.deactivated_at, c.updated_at FROM courses c
			ORDER BY c.name_normalized, c.id LIMIT ? OFFSET ?`
		args = []any{input.Limit + 1, input.Offset}
	}
	rows, err := service.database.QueryContext(ctx, query, args...)
	if err != nil {
		return ListResult{}, fmt.Errorf("list courses: %w", err)
	}
	var courses []Course
	for rows.Next() {
		course, err := scanCourse(rows)
		if err != nil {
			return ListResult{}, errors.Join(err, closeRows(rows))
		}
		courses = append(courses, course)
	}
	if err := rows.Err(); err != nil {
		return ListResult{}, errors.Join(fmt.Errorf("iterate courses: %w", err), closeRows(rows))
	}
	if err := closeRows(rows); err != nil {
		return ListResult{}, err
	}
	hasMore := len(courses) > input.Limit
	if hasMore {
		courses = courses[:input.Limit]
	}
	for index := range courses {
		courses[index].SupervisorIDs, err = supervisorIDs(ctx, service.database, courses[index].ID)
		if err != nil {
			return ListResult{}, err
		}
		courses[index].HasLogo = service.logoExists(courses[index].ID)
	}
	return ListResult{Courses: courses, HasMore: hasMore}, nil
}

func (service *Service) Update(ctx context.Context, courseID, actorID string, changes Fields) (Course, error) {
	if fieldsEmpty(changes) {
		return Course{}, ErrInvalid
	}
	fields, err := normalizeFields(changes, false)
	if err != nil {
		return Course{}, ErrInvalid
	}
	err = miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		if fields.Name.Set {
			if err := requireAdministrator(ctx, tx, actorID); err != nil {
				return err
			}
		}
		supervisorFields := fields.Description.Set || fields.Curriculum.Set || fields.LearningGoals.Set ||
			fields.Instructions.Set || fields.Language.Set
		if supervisorFields {
			if _, err := loadScoped(ctx, tx, courseID, actorID, true); err != nil {
				return err
			}
		} else if _, err := loadScoped(ctx, tx, courseID, actorID, false); err != nil {
			return err
		}
		nameKey := any(nil)
		if fields.Name.Set {
			nameKey = identity.NameKey(*fields.Name.Value)
		}
		_, err := tx.ExecContext(ctx, `UPDATE courses SET
			name = CASE WHEN ? THEN ? ELSE name END,
			name_normalized = CASE WHEN ? THEN ? ELSE name_normalized END,
			description = CASE WHEN ? THEN ? ELSE description END,
			curriculum = CASE WHEN ? THEN ? ELSE curriculum END,
			learning_goals = CASE WHEN ? THEN ? ELSE learning_goals END,
			llm_instructions = CASE WHEN ? THEN ? ELSE llm_instructions END,
			language = CASE WHEN ? THEN ? ELSE language END, updated_at = ?, updated_by = ? WHERE id = ?`,
			fields.Name.Set, nullable(fields.Name.Value), fields.Name.Set, nameKey,
			fields.Description.Set, nullable(fields.Description.Value), fields.Curriculum.Set, nullable(fields.Curriculum.Value),
			fields.LearningGoals.Set, nullable(fields.LearningGoals.Value), fields.Instructions.Set, nullable(fields.Instructions.Value),
			fields.Language.Set, nullable(fields.Language.Value), instant(time.Now()), actorID, courseID)
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE constraint failed: courses.name_normalized") {
				return ErrNameTaken
			}
			return fmt.Errorf("update course: %w", err)
		}
		updated, err := loadScoped(ctx, tx, courseID, actorID, supervisorFields)
		if err != nil {
			return err
		}
		if updated.Active && (updated.LearningGoals == nil || updated.Instructions == nil || updated.Language == nil) {
			return ErrActivationUnavailable
		}
		return audit.WriteWithMetadata(ctx, tx, audit.ActionCourseCourseUpdated, actorID, "", audit.Metadata{CourseID: courseID})
	})
	if err != nil {
		return Course{}, err
	}
	return service.Get(ctx, courseID, actorID)
}

func (service *Service) Activate(ctx context.Context, courseID, actorID string) (Course, error) {
	err := miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		course, err := loadScoped(ctx, tx, courseID, actorID, true)
		if err != nil {
			return err
		}
		if course.Active {
			return ErrInvalidState
		}
		var supervisors int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM course_supervisors WHERE course_id = ?", courseID).
			Scan(&supervisors); err != nil {
			return fmt.Errorf("count activation supervisors: %w", err)
		}
		if supervisors == 0 {
			return ErrActivationUnavailable
		}
		if course.LearningGoals == nil || course.Instructions == nil || course.Language == nil ||
			strings.TrimSpace(*course.LearningGoals) == "" || strings.TrimSpace(*course.Instructions) == "" {
			return ErrActivationUnavailable
		}
		if _, err := identity.Language(*course.Language); err != nil {
			return ErrActivationUnavailable
		}
		ready := false
		if service.materialReady != nil {
			ready, err = service.materialReady(ctx, tx, courseID)
			if err != nil {
				return err
			}
		}
		if !ready {
			return ErrActivationUnavailable
		}
		now := instant(time.Now())
		if _, err := tx.ExecContext(ctx, `UPDATE courses SET is_active = 1, activated_at = ?, activated_by = ?,
			deactivated_at = NULL, deactivated_by = NULL, updated_at = ?, updated_by = ? WHERE id = ?`, now, actorID,
			now, actorID, courseID); err != nil {
			return fmt.Errorf("activate course: %w", err)
		}
		return audit.WriteWithMetadata(ctx, tx, audit.ActionCourseCourseActivated, actorID, "", audit.Metadata{CourseID: courseID})
	})
	if err != nil {
		return Course{}, err
	}
	return service.Get(ctx, courseID, actorID)
}

func (service *Service) Deactivate(ctx context.Context, courseID, actorID string) (Course, error) {
	err := miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		course, err := loadScoped(ctx, tx, courseID, actorID, true)
		if err != nil {
			return err
		}
		if !course.Active {
			return ErrInvalidState
		}
		now := instant(time.Now())
		if _, err := tx.ExecContext(ctx, `UPDATE courses SET is_active = 0, deactivated_at = ?, deactivated_by = ?,
			updated_at = ?, updated_by = ? WHERE id = ?`, now, actorID, now, actorID, courseID); err != nil {
			return fmt.Errorf("deactivate course: %w", err)
		}
		return audit.WriteWithMetadata(ctx, tx, audit.ActionCourseCourseDeactivated, actorID, "", audit.Metadata{CourseID: courseID})
	})
	if err != nil {
		return Course{}, err
	}
	return service.Get(ctx, courseID, actorID)
}

func (service *Service) AssignSupervisor(ctx context.Context, courseID, supervisorID, actorID string) error {
	return miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		if err := requireAdministrator(ctx, tx, actorID); err != nil {
			return err
		}
		if _, err := loadScoped(ctx, tx, courseID, actorID, false); err != nil {
			return err
		}
		hasRole, err := user.HasRole(ctx, tx, supervisorID, user.Supervisor)
		if err != nil {
			return err
		}
		if !hasRole {
			return ErrSupervisorInvalid
		}
		result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO course_supervisors
			(course_id, supervisor_user_id, assigned_at, assigned_by) VALUES (?, ?, ?, ?)`, courseID, supervisorID,
			instant(time.Now()), actorID)
		if err != nil {
			return fmt.Errorf("assign course supervisor: %w", err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("count supervisor assignment: %w", err)
		}
		if rows == 0 {
			return nil
		}
		return audit.WriteWithMetadata(ctx, tx, audit.ActionCourseSupervisorAssigned, actorID, supervisorID,
			audit.Metadata{CourseID: courseID})
	})
}

func (service *Service) RemoveSupervisor(ctx context.Context, courseID, supervisorID, actorID string) error {
	return miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		if err := requireAdministrator(ctx, tx, actorID); err != nil {
			return err
		}
		if _, err := loadScoped(ctx, tx, courseID, actorID, false); err != nil {
			return err
		}
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM course_supervisors WHERE course_id = ?", courseID).Scan(&count); err != nil {
			return fmt.Errorf("count course supervisors: %w", err)
		}
		var assigned int
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM course_supervisors
			WHERE course_id = ? AND supervisor_user_id = ?)`, courseID, supervisorID).Scan(&assigned); err != nil {
			return fmt.Errorf("check course supervisor assignment: %w", err)
		}
		if assigned == 0 {
			return ErrNotFound
		}
		if count <= 1 {
			return ErrLastSupervisor
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM course_supervisors WHERE course_id = ? AND supervisor_user_id = ?`,
			courseID, supervisorID); err != nil {
			return fmt.Errorf("remove course supervisor: %w", err)
		}
		return audit.WriteWithMetadata(ctx, tx, audit.ActionCourseSupervisorRemoved, actorID, supervisorID,
			audit.Metadata{CourseID: courseID})
	})
}

func (service *Service) Delete(ctx context.Context, courseID, actorID string) error {
	err := miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		if err := requireAdministrator(ctx, tx, actorID); err != nil {
			return err
		}
		course, err := loadScoped(ctx, tx, courseID, actorID, false)
		if err != nil {
			return err
		}
		if course.Active {
			return ErrInvalidState
		}
		if service.activeSessions != nil {
			active, err := service.activeSessions(ctx, tx, courseID)
			if err != nil {
				return err
			}
			if active {
				return ErrActiveSession
			}
		}
		if err := service.lifecycle.DeleteCourseData(ctx, tx, courseID); err != nil {
			return fmt.Errorf("delete registered course data: %w", err)
		}
		if err := audit.ReplaceCourseHistoryWithDeletion(ctx, tx, courseID, actorID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM courses WHERE id = ?", courseID); err != nil {
			return fmt.Errorf("delete course: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	service.logoMu.Lock()
	defer service.logoMu.Unlock()
	path, pathErr := service.logoPath(courseID)
	if pathErr == nil {
		if removeErr := os.RemoveAll(filepath.Dir(path)); removeErr != nil {
			service.logger.WarnContext(ctx, "remove deleted course logo", "course_id", courseID, "error", removeErr)
		}
	}
	return nil
}

func loadScoped(ctx context.Context, query miSQLite.Querier, courseID, actorID string, supervisorOnly bool) (Course, error) {
	administrator := false
	var err error
	if !supervisorOnly {
		administrator, err = user.HasRole(ctx, query, actorID, user.Administrator)
		if err != nil {
			return Course{}, err
		}
	}
	condition := `EXISTS(SELECT 1 FROM course_supervisors cs
		WHERE cs.course_id = c.id AND cs.supervisor_user_id = ?)`
	args := []any{courseID, actorID}
	if administrator {
		condition = "1 = 1"
		args = []any{courseID}
	}
	row := query.QueryRowContext(ctx, `SELECT c.id, c.name, c.description, c.curriculum, c.learning_goals,
		c.llm_instructions, c.language, c.is_active, c.created_at, c.activated_at, c.deactivated_at, c.updated_at
		FROM courses c WHERE c.id = ? AND (`+condition+`)`, args...)
	course, err := scanCourse(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Course{}, ErrNotFound
	}
	return course, err
}

type scanner interface{ Scan(...any) error }

func scanCourse(row scanner) (Course, error) {
	var course Course
	var description, curriculum, goals, instructions, language sql.NullString
	var created string
	var activated, deactivated, updated sql.NullString
	var active int
	if err := row.Scan(&course.ID, &course.Name, &description, &curriculum, &goals, &instructions, &language, &active,
		&created, &activated, &deactivated, &updated); err != nil {
		return Course{}, err
	}
	var err error
	course.CreatedAt, err = parseInstant(created)
	if err != nil {
		return Course{}, err
	}
	course.Description, course.Curriculum, course.LearningGoals = stringPointer(description), stringPointer(curriculum), stringPointer(goals)
	course.Instructions, course.Language, course.Active = stringPointer(instructions), stringPointer(language), active != 0
	if course.ActivatedAt, err = timePointer(activated); err != nil {
		return Course{}, err
	}
	if course.DeactivatedAt, err = timePointer(deactivated); err != nil {
		return Course{}, err
	}
	if course.UpdatedAt, err = timePointer(updated); err != nil {
		return Course{}, err
	}
	return course, nil
}

func supervisorIDs(ctx context.Context, query miSQLite.Querier, courseID string) ([]string, error) {
	rows, err := query.QueryContext(ctx, `SELECT supervisor_user_id FROM course_supervisors
		WHERE course_id = ? ORDER BY supervisor_user_id`, courseID)
	if err != nil {
		return nil, fmt.Errorf("list course supervisors: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, errors.Join(fmt.Errorf("scan course supervisor: %w", err), closeRows(rows))
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.Join(fmt.Errorf("iterate course supervisors: %w", err), closeRows(rows))
	}
	if err := closeRows(rows); err != nil {
		return nil, err
	}
	return ids, nil
}

func closeRows(rows *sql.Rows) error {
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close course rows: %w", err)
	}
	return nil
}

func requireAdministrator(ctx context.Context, query miSQLite.Querier, actorID string) error {
	administrator, err := user.HasRole(ctx, query, actorID, user.Administrator)
	if err != nil {
		return err
	}
	if !administrator {
		return ErrUnauthorized
	}
	return nil
}

func normalizeFields(fields Fields, creation bool) (Fields, error) {
	result := fields
	if creation && (!fields.Name.Set || fields.Name.Value == nil) {
		return Fields{}, ErrInvalid
	}
	if fields.Name.Set {
		if fields.Name.Value == nil {
			return Fields{}, ErrInvalid
		}
		name := strings.TrimSpace(norm.NFC.String(*fields.Name.Value))
		if name == "" || utf8.RuneCountInString(name) > maxNameRunes || len(name) > maxNameBytes {
			return Fields{}, ErrInvalid
		}
		for _, char := range name {
			if unicode.IsControl(char) {
				return Fields{}, ErrInvalid
			}
		}
		result.Name.Value = &name
	}
	var err error
	result.Description, err = normalizeOptional(fields.Description, maxDescriptionRunes, maxDescriptionBytes)
	if err != nil {
		return Fields{}, err
	}
	for source, destination := range map[*OptionalString]*OptionalString{
		&fields.Curriculum: &result.Curriculum, &fields.LearningGoals: &result.LearningGoals,
		&fields.Instructions: &result.Instructions,
	} {
		*destination, err = normalizeOptional(*source, maxContextRunes, maxContextBytes)
		if err != nil {
			return Fields{}, err
		}
	}
	if fields.Language.Set && fields.Language.Value != nil {
		value, err := identity.Language(*fields.Language.Value)
		if err != nil {
			return Fields{}, err
		}
		result.Language.Value = &value
	}
	return result, nil
}

func normalizeOptional(field OptionalString, runes, bytes int) (OptionalString, error) {
	if !field.Set || field.Value == nil {
		return field, nil
	}
	value, err := identity.Text(*field.Value, runes, bytes)
	if err != nil {
		return OptionalString{}, err
	}
	return OptionalString{Set: true, Value: value}, nil
}

func fieldsEmpty(fields Fields) bool {
	return !fields.Name.Set && !fields.Description.Set && !fields.Curriculum.Set && !fields.LearningGoals.Set &&
		!fields.Instructions.Set && !fields.Language.Set
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func nullable(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func stringPointer(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}

func timePointer(value sql.NullString) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}
	parsed, err := parseInstant(value.String)
	return &parsed, err
}

func parseInstant(value string) (time.Time, error) {
	parsed, err := time.Parse("2006-01-02T15:04:05.000000Z", value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse stored course instant: %w", err)
	}
	return parsed, nil
}

func instant(value time.Time) string { return value.UTC().Format("2006-01-02T15:04:05.000000Z") }

func (service *Service) logoPath(courseID string) (string, error) {
	raw := strings.TrimPrefix(courseID, "cou_")
	parsed, err := uuid.Parse(raw)
	if err != nil || parsed.Version() != 4 || courseID != "cou_"+parsed.String() {
		return "", ErrNotFound
	}
	return filepath.Join(service.dataDir, "courses", courseID, "logo.png"), nil
}

func (service *Service) logoExists(courseID string) bool {
	service.logoMu.RLock()
	defer service.logoMu.RUnlock()
	path, err := service.logoPath(courseID)
	if err != nil {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func (service *Service) PutLogo(ctx context.Context, courseID, actorID string, data []byte) error {
	service.logoMu.Lock()
	defer service.logoMu.Unlock()
	if err := service.requireLogoMutation(ctx, courseID, actorID); err != nil {
		return err
	}
	path, err := service.logoPath(courseID)
	if err != nil {
		return err
	}
	change, err := filepublish.Replace(path, data)
	if err != nil {
		return err
	}
	err = miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		if err := requireLogoMutation(ctx, tx, courseID, actorID); err != nil {
			return err
		}
		return audit.WriteWithMetadata(ctx, tx, audit.ActionCourseLogoUpdated, actorID, "", audit.Metadata{CourseID: courseID})
	})
	if err != nil {
		return errors.Join(err, change.Finish(false))
	}
	return change.Finish(true)
}

func (service *Service) Logo(ctx context.Context, courseID, actorID string) ([]byte, string, error) {
	service.logoMu.RLock()
	defer service.logoMu.RUnlock()
	if _, err := loadScoped(ctx, service.database, courseID, actorID, false); err != nil {
		return nil, "", err
	}
	path, err := service.logoPath(courseID)
	if err != nil {
		return nil, "", err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, "", ErrLogoNotFound
	}
	if err != nil {
		return nil, "", fmt.Errorf("read course logo: %w", err)
	}
	digest := sha256.Sum256(data)
	return data, fmt.Sprintf("\"%x\"", digest), nil
}

func (service *Service) DeleteLogo(ctx context.Context, courseID, actorID string) error {
	service.logoMu.Lock()
	defer service.logoMu.Unlock()
	if err := service.requireLogoMutation(ctx, courseID, actorID); err != nil {
		return err
	}
	path, err := service.logoPath(courseID)
	if err != nil {
		return err
	}
	change, err := filepublish.Remove(path)
	if err != nil {
		return err
	}
	err = miSQLite.WithTx(ctx, service.database, func(tx *sql.Tx) error {
		if err := requireLogoMutation(ctx, tx, courseID, actorID); err != nil {
			return err
		}
		return audit.WriteWithMetadata(ctx, tx, audit.ActionCourseLogoRemoved, actorID, "", audit.Metadata{CourseID: courseID})
	})
	if err != nil {
		return errors.Join(err, change.Finish(false))
	}
	return change.Finish(true)
}

func (service *Service) requireLogoMutation(ctx context.Context, courseID, actorID string) error {
	return requireLogoMutation(ctx, service.database, courseID, actorID)
}

func requireLogoMutation(ctx context.Context, query miSQLite.Querier, courseID, actorID string) error {
	administrator, err := user.HasRole(ctx, query, actorID, user.Administrator)
	if err != nil {
		return err
	}
	var allowed int
	if administrator {
		err = query.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM courses WHERE id = ?)", courseID).Scan(&allowed)
	} else {
		err = query.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM course_supervisors
			WHERE course_id = ? AND supervisor_user_id = ?)`, courseID, actorID).Scan(&allowed)
	}
	if err != nil {
		return fmt.Errorf("authorize course logo mutation: %w", err)
	}
	if allowed == 0 {
		return ErrNotFound
	}
	return nil
}
