package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/thorstenkramm/mia/internal/httpserver"
	miSQLite "github.com/thorstenkramm/mia/internal/sqlite"
)

var (
	ErrNotFound     = errors.New("job not found")
	ErrUnauthorized = errors.New("job oversight unauthorized")
	ErrInvalidList  = errors.New("invalid job list")
)

type AdministratorCheck func(context.Context, miSQLite.Querier, string) (bool, error)

type Record struct {
	ID, Type, SubjectType, SubjectID, CourseID, OwnerUserID, State, FailureCode string
	AttemptCount, ProviderInputUnits, ProviderOutputUnits                       int64
	AvailableAt, CreatedAt                                                      time.Time
	StartedAt, FinishedAt                                                       *time.Time
}

type Records struct {
	Items   []Record
	HasMore bool
}

type Oversight struct {
	database *sql.DB
	admin    AdministratorCheck
}

func NewOversight(database *sql.DB, admin AdministratorCheck) *Oversight {
	return &Oversight{database: database, admin: admin}
}

func (oversight *Oversight) List(ctx context.Context, actorID string, limit, offset int) (Records, error) {
	if limit < 1 || limit > 100 || offset < 0 || offset > 10_000 {
		return Records{}, ErrInvalidList
	}
	allowed, err := oversight.admin(ctx, oversight.database, actorID)
	if err != nil {
		return Records{}, err
	}
	if !allowed {
		return Records{}, ErrUnauthorized
	}
	return list(ctx, oversight.database, "1 = 1", nil, limit, offset, true)
}

func (oversight *Oversight) Get(ctx context.Context, actorID, jobID string) (Record, error) {
	allowed, err := oversight.admin(ctx, oversight.database, actorID)
	if err != nil {
		return Record{}, err
	}
	if !allowed {
		return Record{}, ErrUnauthorized
	}
	items, err := list(ctx, oversight.database, "id = ?", []any{jobID}, 1, 0, true)
	if err != nil {
		return Record{}, err
	}
	if len(items.Items) == 0 {
		return Record{}, ErrNotFound
	}
	return items.Items[0], nil
}

// ListSubject returns safe subject-scoped records after the owning feature has
// already authorized the actor. Provider failure details and usage are omitted.
func (oversight *Oversight) ListSubject(ctx context.Context, subjectType, subjectID string,
	limit, offset int) (Records, error) {
	if limit < 1 || limit > 100 || offset < 0 || offset > 10_000 {
		return Records{}, ErrInvalidList
	}
	return list(ctx, oversight.database, "subject_type = ? AND subject_id = ?", []any{subjectType, subjectID},
		limit, offset, false)
}

// ListSubjectIDs returns safe records for an owner-supplied, already authorized
// set of subject IDs. The jobs owner never queries feature-owned subject tables.
func (oversight *Oversight) ListSubjectIDs(ctx context.Context, subjectIDs []string,
	limit, offset int) (Records, error) {
	if len(subjectIDs) == 0 || len(subjectIDs) > 201 || limit < 1 || limit > 100 || offset < 0 || offset > 10_000 {
		return Records{}, ErrInvalidList
	}
	placeholders := make([]string, len(subjectIDs))
	args := make([]any, len(subjectIDs))
	for index, id := range subjectIDs {
		placeholders[index], args[index] = "?", id
	}
	return list(ctx, oversight.database, "subject_id IN ("+strings.Join(placeholders, ",")+")", args,
		limit, offset, false)
}

func list(ctx context.Context, query miSQLite.Querier, condition string, args []any, limit, offset int,
	diagnostics bool) (recordsResult Records, returnErr error) {
	queryArgs := append(append([]any{}, args...), limit+1, offset)
	rows, err := query.QueryContext(ctx, `SELECT id, type, subject_type, subject_id, course_id,
		COALESCE(owner_user_id, ''), state, attempt_count, available_at, COALESCE(failure_code, ''),
		provider_input_units, provider_output_units, created_at, started_at, finished_at FROM jobs WHERE `+condition+
		` ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`, queryArgs...)
	if err != nil {
		return Records{}, fmt.Errorf("list jobs: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, rows.Close())
	}()
	var records []Record
	for rows.Next() {
		var record Record
		var available, created string
		var started, finished sql.NullString
		if err := rows.Scan(&record.ID, &record.Type, &record.SubjectType, &record.SubjectID, &record.CourseID,
			&record.OwnerUserID, &record.State, &record.AttemptCount, &available, &record.FailureCode,
			&record.ProviderInputUnits, &record.ProviderOutputUnits, &created, &started, &finished); err != nil {
			return Records{}, fmt.Errorf("scan job: %w", err)
		}
		var err error
		if record.AvailableAt, err = parseInstant(available); err != nil {
			return Records{}, err
		}
		if record.CreatedAt, err = parseInstant(created); err != nil {
			return Records{}, err
		}
		if record.StartedAt, err = optionalInstant(started); err != nil {
			return Records{}, err
		}
		if record.FinishedAt, err = optionalInstant(finished); err != nil {
			return Records{}, err
		}
		if !diagnostics {
			record.FailureCode = ""
			record.ProviderInputUnits, record.ProviderOutputUnits = 0, 0
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return Records{}, fmt.Errorf("iterate jobs: %w", err)
	}
	hasMore := len(records) > limit
	if hasMore {
		records = records[:limit]
	}
	return Records{Items: records, HasMore: hasMore}, nil
}

func Register(server *httpserver.Server, oversight *Oversight) {
	server.AuthenticatedGET("/api/v1/jobs", listHandler(oversight))
	server.AuthenticatedGET("/api/v1/jobs/:id", getHandler(oversight))
}

func listHandler(oversight *Oversight) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := actor(c)
		if err != nil {
			return err
		}
		page, err := httpserver.ParsePagination(c.QueryParams())
		if err != nil {
			return httpserver.NewError(httpserver.CodeJobInvalid)
		}
		records, err := oversight.List(c.Request().Context(), actorID, page.Limit, page.Offset)
		if err != nil {
			return jobError(err)
		}
		data := make([]map[string]any, 0, len(records.Items))
		for _, record := range records.Items {
			data = append(data, resource(record, true))
		}
		document := map[string]any{"data": data, "meta": map[string]any{"has_more": records.HasMore}}
		if links := httpserver.CollectionLinks(c.Request().URL.Path, page, records.HasMore); links != nil {
			document["links"] = links
		}
		return jsonAPI(c, 200, document)
	}
}

func getHandler(oversight *Oversight) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := actor(c)
		if err != nil {
			return err
		}
		record, err := oversight.Get(c.Request().Context(), actorID, c.Param("id"))
		if err != nil {
			return jobError(err)
		}
		return jsonAPI(c, 200, map[string]any{"data": resource(record, true)})
	}
}

func resource(record Record, diagnostics bool) map[string]any {
	attributes := map[string]any{"job_type": record.Type, "subject_type": record.SubjectType,
		"subject_id": record.SubjectID, "state": record.State, "attempt_count": record.AttemptCount,
		"available_at": httpserver.FormatInstant(record.AvailableAt),
		"created_at":   httpserver.FormatInstant(record.CreatedAt),
		"started_at":   httpserver.FormatOptionalInstant(record.StartedAt),
		"finished_at":  httpserver.FormatOptionalInstant(record.FinishedAt)}
	if diagnostics {
		attributes["failure_code"] = nullable(record.FailureCode)
		attributes["provider_input_units"] = record.ProviderInputUnits
		attributes["provider_output_units"] = record.ProviderOutputUnits
	}
	return map[string]any{"type": "jobs", "id": record.ID, "attributes": attributes,
		"relationships": map[string]any{"course": map[string]any{"data": map[string]string{
			"type": "courses", "id": record.CourseID}}}}
}

func actor(c *echo.Context) (string, error) {
	return httpserver.AuthenticatedUser(c)
}

func jsonAPI(c *echo.Context, status int, body any) error {
	return httpserver.JSONAPI(c, status, body)
}

func jobError(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return httpserver.NewError(httpserver.CodeJobNotFound)
	case errors.Is(err, ErrUnauthorized):
		return httpserver.NewError(httpserver.CodeJobUnauthorized)
	case errors.Is(err, ErrInvalidList):
		return httpserver.NewError(httpserver.CodeJobInvalid)
	default:
		return err
	}
}

func parseInstant(value string) (time.Time, error) {
	parsed, err := time.Parse("2006-01-02T15:04:05.000000Z", value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse stored job instant: %w", err)
	}
	return parsed, nil
}

func optionalInstant(value sql.NullString) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}
	parsed, err := parseInstant(value.String)
	return &parsed, err
}
