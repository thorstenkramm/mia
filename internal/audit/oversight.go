package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/thorstenkramm/mia/internal/httpserver"
	miSQLite "github.com/thorstenkramm/mia/internal/sqlite"
)

var (
	ErrUnauthorized = errors.New("audit oversight unauthorized")
	ErrInvalidList  = errors.New("invalid audit event list")
)

type AdministratorCheck func(context.Context, miSQLite.Querier, string) (bool, error)

type Event struct {
	ID, Action, ActorUserID, ActorFingerprint, SubjectUserID string
	SubjectType, SubjectFingerprint, CourseID                string
	CreatedAt                                                time.Time
	Metadata                                                 map[string]any
}

type Events struct {
	Items   []Event
	HasMore bool
}

// Oversight exposes content-free audit records to administrators. It is safe
// for concurrent use after construction.
type Oversight struct {
	admin    AdministratorCheck
	database *sql.DB
}

func NewOversight(database *sql.DB, admin AdministratorCheck) *Oversight {
	if admin == nil {
		panic("nil audit administrator check")
	}
	return &Oversight{admin: admin, database: database}
}

func (oversight *Oversight) List(ctx context.Context, actorID string, limit, offset int) (Events, error) {
	if limit < 1 || limit > 100 || offset < 0 || offset > 10_000 {
		return Events{}, ErrInvalidList
	}
	allowed, err := oversight.admin(ctx, oversight.database, actorID)
	if err != nil {
		return Events{}, err
	}
	if !allowed {
		return Events{}, ErrUnauthorized
	}
	args := []any{limit + 1, offset}
	rows, err := oversight.database.QueryContext(ctx, `SELECT id, action, COALESCE(actor_user_id, ''),
		COALESCE(actor_fingerprint, ''), COALESCE(subject_user_id, ''), COALESCE(subject_type, ''),
		COALESCE(subject_fingerprint, ''), COALESCE(course_id, ''), metadata, created_at
		FROM audit_events ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return Events{}, fmt.Errorf("list audit events: %w", err)
	}
	items := make([]Event, 0, limit+1)
	for rows.Next() {
		var event Event
		var metadata sql.NullString
		var createdAt string
		if err := rows.Scan(&event.ID, &event.Action, &event.ActorUserID, &event.ActorFingerprint,
			&event.SubjectUserID, &event.SubjectType, &event.SubjectFingerprint, &event.CourseID,
			&metadata, &createdAt); err != nil {
			return Events{}, errors.Join(fmt.Errorf("scan audit event: %w", err), rows.Close())
		}
		if event.CreatedAt, err = time.Parse("2006-01-02T15:04:05.000000Z", createdAt); err != nil {
			return Events{}, errors.Join(fmt.Errorf("parse audit event instant: %w", err), rows.Close())
		}
		if metadata.Valid {
			if err := json.Unmarshal([]byte(metadata.String), &event.Metadata); err != nil {
				return Events{}, errors.Join(fmt.Errorf("parse audit event metadata: %w", err), rows.Close())
			}
		}
		items = append(items, event)
	}
	if err := rows.Err(); err != nil {
		return Events{}, errors.Join(fmt.Errorf("iterate audit events: %w", err), rows.Close())
	}
	if err := rows.Close(); err != nil {
		return Events{}, fmt.Errorf("close audit event rows: %w", err)
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	return Events{Items: items, HasMore: hasMore}, nil
}

// Register attaches administrator audit-log oversight.
func Register(server *httpserver.Server, oversight *Oversight) {
	server.AuthenticatedGET("/api/v1/audit-events", listHandler(oversight))
}

func listHandler(oversight *Oversight) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := auditActor(c)
		if err != nil {
			return err
		}
		page, err := httpserver.ParsePagination(c.QueryParams())
		if err != nil {
			return httpserver.NewError(httpserver.CodeAuditInvalid)
		}
		events, err := oversight.List(c.Request().Context(), actorID, page.Limit, page.Offset)
		if err != nil {
			if errors.Is(err, ErrUnauthorized) {
				return httpserver.NewError(httpserver.CodeAuditUnauthorized)
			}
			if errors.Is(err, ErrInvalidList) {
				return httpserver.NewError(httpserver.CodeAuditInvalid)
			}
			return err
		}
		data := make([]map[string]any, 0, len(events.Items))
		for _, event := range events.Items {
			data = append(data, eventResource(event))
		}
		document := map[string]any{"data": data, "meta": map[string]any{"has_more": events.HasMore}}
		if links := httpserver.CollectionLinks(c.Request().URL.Path, page, events.HasMore); links != nil {
			document["links"] = links
		}
		return httpserver.JSONAPI(c, http.StatusOK, document)
	}
}

func auditActor(c *echo.Context) (string, error) {
	return httpserver.AuthenticatedUser(c)
}

func eventResource(event Event) map[string]any {
	return map[string]any{"type": "audit-events", "id": event.ID, "attributes": map[string]any{
		"action": event.Action, "actor_user_id": nullableString(event.ActorUserID),
		"actor_fingerprint": nullableString(event.ActorFingerprint),
		"subject_user_id":   nullableString(event.SubjectUserID), "subject_type": nullableString(event.SubjectType),
		"subject_fingerprint": nullableString(event.SubjectFingerprint), "course_id": nullableString(event.CourseID),
		"metadata": event.Metadata, "created_at": httpserver.FormatInstant(event.CreatedAt),
	}}
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
