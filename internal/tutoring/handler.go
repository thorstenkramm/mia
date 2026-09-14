package tutoring

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/thorstenkramm/mia/internal/httpserver"
)

type sessionCreateRequest struct {
	Data struct {
		Type       string `json:"type"`
		ID         string `json:"id,omitempty"`
		Attributes struct {
			ClientRequestID string `json:"client_request_id"`
		} `json:"attributes"`
		Relationships struct {
			Materials *struct {
				Data []struct {
					Type string `json:"type"`
					ID   string `json:"id"`
				} `json:"data"`
			} `json:"materials"`
		} `json:"relationships"`
	} `json:"data"`
}

type sessionSummaryRequest struct {
	Data struct {
		Type       string `json:"type"`
		ID         string `json:"id"`
		Attributes struct {
			Summary  *string `json:"summary"`
			FollowUp *string `json:"follow_up"`
		} `json:"attributes"`
	} `json:"data"`
}

type messageRequest struct {
	Data struct {
		Type       string `json:"type"`
		ID         string `json:"id,omitempty"`
		Attributes struct {
			ClientRequestID string `json:"client_request_id"`
			Content         string `json:"content"`
		} `json:"attributes"`
	} `json:"data"`
}

type responseRetryRequest struct {
	Data struct {
		Type       string `json:"type"`
		ID         string `json:"id,omitempty"`
		Attributes struct {
			ClientRequestID string `json:"client_request_id"`
		} `json:"attributes"`
	} `json:"data"`
}

func Register(server *httpserver.Server, service *Service, manager *Manager) {
	server.AuthenticatedGET("/api/v1/users/me/active-tutoring-session", discoverActiveSessionHandler(service))
	server.AuthenticatedGET("/api/v1/courses/:course_id/tutoring-sessions", listSessionsHandler(service))
	server.AuthenticatedPOST("/api/v1/courses/:course_id/tutoring-sessions", startSessionHandler(service))
	server.AuthenticatedGET("/api/v1/tutoring-sessions/:id", getSessionHandler(service))
	server.AuthenticatedGET("/api/v1/tutoring-sessions/:id/current-work", currentWorkHandler(service))
	server.AuthenticatedPATCH("/api/v1/tutoring-sessions/:id", correctSummaryHandler(service))
	server.AuthenticatedPOST("/api/v1/tutoring-sessions/:id/completion", completeSessionHandler(service))
	server.AuthenticatedPOST("/api/v1/tutoring-sessions/:id/summary-generations", regenerateSummaryHandler(service))
	server.AuthenticatedGET("/api/v1/tutoring-sessions/:id/messages", listMessagesHandler(service))
	server.AuthenticatedGET("/api/v1/tutoring-sessions/:id/materials", listMaterialsHandler(service))
	server.AuthenticatedPOST("/api/v1/tutoring-sessions/:id/messages", submitMessageHandler(service))
	server.AuthenticatedPOST("/api/v1/student-messages/:id/response-retries", retryResponseHandler(service))
	server.AuthenticatedPOST("/api/v1/tutor-responses/:id/interruptions", interruptResponseHandler(service))
	server.AuthenticatedRoute(http.MethodGet, "/api/v1/tutor-responses/:id/events", httpserver.RepresentationSSE,
		eventsHandler(manager))
}

func currentWorkHandler(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := httpserver.AuthenticatedUser(c)
		if err != nil {
			return err
		}
		query, err := workQuery(c)
		if err != nil {
			return tutoringError(err)
		}
		work, err := service.CurrentWork(c.Request().Context(), c.Param("id"), actorID, query)
		if err != nil {
			return tutoringError(err)
		}
		return currentWorkResponse(c, http.StatusOK, work)
	}
}

func workQuery(c *echo.Context) (WorkQuery, error) {
	values, err := url.ParseQuery(c.Request().URL.RawQuery)
	if err != nil {
		return WorkQuery{}, ErrInvalid
	}
	for key, entries := range values {
		if (key != "message_request_id" && key != "response_id") || len(entries) != 1 || entries[0] == "" {
			return WorkQuery{}, ErrInvalid
		}
	}
	return WorkQuery{MessageRequestID: values.Get("message_request_id"), ResponseID: values.Get("response_id")}, nil
}

func discoverActiveSessionHandler(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := httpserver.AuthenticatedUser(c)
		if err != nil {
			return err
		}
		value, err := service.DiscoverActive(c.Request().Context(), actorID)
		if err != nil {
			return tutoringError(err)
		}
		var data any
		if value != nil {
			data = activeSessionResource(*value)
		}
		return httpserver.JSONAPI(c, http.StatusOK, map[string]any{"data": data})
	}
}

func startSessionHandler(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := httpserver.AuthenticatedUser(c)
		if err != nil {
			return err
		}
		var body sessionCreateRequest
		if err := httpserver.DecodeJSONAPI(c, &body); err != nil {
			return err
		}
		if body.Data.Type != "tutoring-sessions" || body.Data.ID != "" {
			return tutoringError(ErrInvalid)
		}
		var materialIDs []string
		if body.Data.Relationships.Materials != nil {
			materialIDs = make([]string, 0, len(body.Data.Relationships.Materials.Data))
			for _, identifier := range body.Data.Relationships.Materials.Data {
				if identifier.Type != "materials" || identifier.ID == "" {
					return tutoringError(ErrInvalid)
				}
				materialIDs = append(materialIDs, identifier.ID)
			}
		}
		value, replay, err := service.Start(c.Request().Context(), StartInput{CourseID: c.Param("course_id"),
			StudentID: actorID, RequestID: body.Data.Attributes.ClientRequestID,
			SelectedMaterialIDs: materialIDs})
		if err != nil {
			return tutoringError(err)
		}
		status := http.StatusCreated
		if replay {
			status = http.StatusOK
		}
		return sessionResponse(c, status, value)
	}
}

// jscpd:ignore-start
// Tutoring and mentoring session reads retain separate authorization, existence
// hiding, and error translation. Unifying them would move those decisions out of
// the packages that own them for two unrelated resources. This single marker
// covers the pair with internal/mentoring/handler.go.
func getSessionHandler(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := httpserver.AuthenticatedUser(c)
		if err != nil {
			return err
		}
		value, err := service.Get(c.Request().Context(), c.Param("id"), actorID)
		if err != nil {
			return tutoringError(err)
		}
		return sessionResponse(c, http.StatusOK, value)
	}
}

// jscpd:ignore-end

func listSessionsHandler(service *Service) echo.HandlerFunc {
	return httpserver.CollectionHandler(tutoringError, ErrInvalid,
		func(c *echo.Context, page httpserver.Page, actorID string) ([]Session, bool, error) {
			result, err := service.List(c.Request().Context(), c.Param("course_id"), actorID,
				ListInput{Limit: page.Limit, Offset: page.Offset})
			return result.Sessions, result.HasMore, err
		}, sessionResource)
}

func correctSummaryHandler(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := httpserver.AuthenticatedUser(c)
		if err != nil {
			return err
		}
		var body sessionSummaryRequest
		if err := httpserver.DecodeJSONAPI(c, &body); err != nil {
			return err
		}
		if body.Data.Type != "tutoring-sessions" || body.Data.ID != c.Param("id") ||
			body.Data.Attributes.Summary == nil || body.Data.Attributes.FollowUp == nil {
			return tutoringError(ErrInvalid)
		}
		value, err := service.CorrectSummary(c.Request().Context(), c.Param("id"), actorID,
			*body.Data.Attributes.Summary, *body.Data.Attributes.FollowUp)
		if err != nil {
			return tutoringError(err)
		}
		return sessionResponse(c, http.StatusOK, value)
	}
}

func completeSessionHandler(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := httpserver.AuthenticatedUser(c)
		if err != nil {
			return err
		}
		value, err := service.Complete(c.Request().Context(), c.Param("id"), actorID)
		if err != nil {
			return tutoringError(err)
		}
		return sessionResponseWithCurrentWork(c, http.StatusOK, value)
	}
}

func regenerateSummaryHandler(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := httpserver.AuthenticatedUser(c)
		if err != nil {
			return err
		}
		if err := service.RegenerateSummary(c.Request().Context(), c.Param("id"), actorID); err != nil {
			return tutoringError(err)
		}
		return c.NoContent(http.StatusNoContent)
	}
}

func submitMessageHandler(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := httpserver.AuthenticatedUser(c)
		if err != nil {
			return err
		}
		var body messageRequest
		if err := httpserver.DecodeJSONAPI(c, &body); err != nil {
			return err
		}
		if body.Data.Type != "student-messages" || body.Data.ID != "" {
			return tutoringError(ErrInvalid)
		}
		result, err := service.Submit(c.Request().Context(), SubmitInput{SessionID: c.Param("id"), StudentID: actorID,
			RequestID: body.Data.Attributes.ClientRequestID, Content: body.Data.Attributes.Content})
		if err != nil {
			return tutoringError(err)
		}
		status := http.StatusCreated
		if result.Replay {
			status = http.StatusOK
		}
		return messageResponse(c, status, result)
	}
}

func listMessagesHandler(service *Service) echo.HandlerFunc {
	return httpserver.CollectionHandler(tutoringError, ErrInvalid,
		func(c *echo.Context, page httpserver.Page, actorID string) ([]MessageResult, bool, error) {
			return service.Messages(c.Request().Context(), c.Param("id"), actorID,
				ListInput{Limit: page.Limit, Offset: page.Offset})
		}, transcriptResource)
}

func listMaterialsHandler(service *Service) echo.HandlerFunc {
	return httpserver.CollectionHandler(tutoringError, ErrInvalid,
		func(c *echo.Context, page httpserver.Page, actorID string) ([]UsedMaterial, bool, error) {
			return service.Materials(c.Request().Context(), c.Param("id"), actorID,
				ListInput{Limit: page.Limit, Offset: page.Offset})
		},
		func(value UsedMaterial) map[string]any {
			return map[string]any{"type": "materials", "id": value.ID, "attributes": map[string]any{
				"name": value.Name, "kind": value.Kind, "scope": value.Scope}}
		})
}

func retryResponseHandler(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := httpserver.AuthenticatedUser(c)
		if err != nil {
			return err
		}
		var body responseRetryRequest
		if err := httpserver.DecodeJSONAPI(c, &body); err != nil {
			return err
		}
		if body.Data.Type != "tutor-response-retries" || body.Data.ID != "" {
			return tutoringError(ErrInvalid)
		}
		result, err := service.Retry(c.Request().Context(), RetryInput{MessageID: c.Param("id"), StudentID: actorID,
			RequestID: body.Data.Attributes.ClientRequestID})
		if err != nil {
			return tutoringError(err)
		}
		status := http.StatusCreated
		if result.Replay {
			status = http.StatusOK
		}
		return responseResponse(c, status, result.Response)
	}
}

func interruptResponseHandler(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := httpserver.AuthenticatedUser(c)
		if err != nil {
			return err
		}
		work, err := service.InterruptAndCurrentWork(c.Request().Context(), c.Param("id"), actorID)
		if err != nil {
			return tutoringError(err)
		}
		return currentWorkResponse(c, http.StatusOK, work)
	}
}

func eventsHandler(manager *Manager) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := httpserver.AuthenticatedUser(c)
		if err != nil {
			return err
		}
		snapshot, subscription, err := manager.Subscribe(c.Request().Context(), c.Param("id"), actorID)
		if err != nil {
			return tutoringError(err)
		}
		if subscription != nil {
			defer subscription.Close()
		}
		response := c.Response()
		response.Header().Set(echo.HeaderContentType, "text/event-stream")
		response.Header().Set("X-Accel-Buffering", "no")
		response.WriteHeader(http.StatusOK)
		if err := writeSSE(response, snapshot); err != nil {
			return nil
		}
		if subscription == nil {
			return nil
		}
		heartbeat := time.NewTicker(15 * time.Second)
		defer heartbeat.Stop()
		for {
			eventCtx, cancel := contextWithCancelOnHeartbeat(c.Request().Context(), heartbeat.C)
			event, ok := subscription.Next(eventCtx)
			heartbeatEvent := errors.Is(context.Cause(eventCtx), errHeartbeat)
			cancel()
			if heartbeatEvent {
				if err := writeSSEComment(response); err != nil {
					return nil
				}
				continue
			}
			if !ok {
				return nil
			}
			if err := writeSSE(response, event); err != nil {
				return nil
			}
		}
	}
}

var errHeartbeat = errors.New("SSE heartbeat")

func contextWithCancelOnHeartbeat(parent context.Context, heartbeat <-chan time.Time) (context.Context,
	context.CancelFunc) {
	ctx, cancel := context.WithCancelCause(parent)
	go func() {
		select {
		case <-heartbeat:
			cancel(errHeartbeat)
		case <-ctx.Done():
		}
	}()
	return ctx, func() { cancel(context.Canceled) }
}

func writeSSE(response http.ResponseWriter, event Event) error {
	payload := map[string]string{}
	for key, value := range map[string]string{"content": event.Content, "state": event.State, "text": event.Text,
		"code": event.Code} {
		if value != "" {
			payload[key] = value
		}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	controller := http.NewResponseController(response)
	if err := controller.SetWriteDeadline(time.Now().Add(30 * time.Second)); err != nil &&
		!errors.Is(err, http.ErrNotSupported) {
		return err
	}
	if _, err := fmt.Fprintf(response, "event: %s\ndata: %s\n\n", event.Type, data); err != nil {
		return err
	}
	return controller.Flush()
}

func writeSSEComment(response http.ResponseWriter) error {
	controller := http.NewResponseController(response)
	if err := controller.SetWriteDeadline(time.Now().Add(30 * time.Second)); err != nil &&
		!errors.Is(err, http.ErrNotSupported) {
		return err
	}
	if _, err := fmt.Fprint(response, ": heartbeat\n\n"); err != nil {
		return err
	}
	return controller.Flush()
}

func sessionResponse(c *echo.Context, status int, value Session) error {
	return httpserver.JSONAPI(c, status, map[string]any{"data": sessionResource(value)})
}

func sessionResponseWithCurrentWork(c *echo.Context, status int, value Session) error {
	return httpserver.JSONAPI(c, status, map[string]any{"data": sessionResource(value), "links": map[string]string{
		"current_work": "/api/v1/tutoring-sessions/" + value.ID + "/current-work"}})
}

func sessionResource(value Session) map[string]any {
	var summary, followUp, summarySource any
	if value.SummarySource != "" {
		summary, followUp, summarySource = value.Summary, value.FollowUp, value.SummarySource
	}
	resource := map[string]any{"type": "tutoring-sessions", "id": value.ID, "attributes": map[string]any{
		"course_id": value.CourseID, "student_id": value.StudentID, "state": value.State,
		"summary": summary, "follow_up": followUp, "summary_source": summarySource,
		"summary_updated_by": nullableString(value.SummaryActor),
		"started_at":         httpserver.FormatInstant(value.StartedAt),
		"last_activity_at":   httpserver.FormatInstant(value.LastActivityAt),
		"completed_at":       httpserver.FormatOptionalInstant(value.CompletedAt),
		"summary_updated_at": httpserver.FormatOptionalInstant(value.SummaryUpdatedAt)}}
	if value.SelectedMaterialIDs != nil {
		identifiers := make([]map[string]string, 0, len(value.SelectedMaterialIDs))
		for _, id := range value.SelectedMaterialIDs {
			identifiers = append(identifiers, map[string]string{"type": "materials", "id": id})
		}
		resource["relationships"] = map[string]any{"materials": map[string]any{"data": identifiers}}
	}
	return resource
}

func activeSessionResource(value ActiveSession) map[string]any {
	return map[string]any{"type": "tutoring-sessions", "id": value.ID, "attributes": map[string]any{
		"course_id": value.CourseID, "course_name": value.CourseName, "state": value.State,
		"started_at":       httpserver.FormatInstant(value.StartedAt),
		"last_activity_at": httpserver.FormatInstant(value.LastActivityAt)}}
}

func messageResponse(c *echo.Context, status int, value MessageResult) error {
	return httpserver.JSONAPI(c, status, map[string]any{"data": messageResource(value), "links": map[string]string{
		"current_work": "/api/v1/tutoring-sessions/" + value.Message.SessionID +
			"/current-work?message_request_id=" + value.RequestID}})
}

func messageResource(value MessageResult) map[string]any {
	return map[string]any{"type": "student-messages", "id": value.Message.ID, "attributes": map[string]any{
		"session_id": value.Message.SessionID, "sequence": value.Message.Sequence, "content": value.Message.Content,
		"created_at": httpserver.FormatInstant(value.Message.CreatedAt), "response": responseResource(value.Response)}}
}

func transcriptResource(value MessageResult) map[string]any {
	return map[string]any{"type": "tutoring-turns", "id": value.Response.ID, "attributes": map[string]any{
		"student_message_id": value.Message.ID, "sequence": value.Message.Sequence,
		"student_content": value.Message.Content, "student_message_created_at": httpserver.FormatInstant(value.Message.CreatedAt),
		"response": responseResource(value.Response)}}
}

func responseResponse(c *echo.Context, status int, value Response) error {
	return httpserver.JSONAPI(c, status, map[string]any{"data": responseResource(value), "links": map[string]string{
		"current_work": "/api/v1/tutoring-sessions/" + value.SessionID + "/current-work?response_id=" + value.ID}})
}

func responseResource(value Response) map[string]any {
	return map[string]any{"type": "tutor-responses", "id": value.ID, "attributes": map[string]any{
		"session_id": value.SessionID, "message_id": value.MessageID, "retry_of_response_id": nullableString(value.RetryOfID),
		"attempt": value.Attempt, "state": value.State, "content": value.Content,
		"failure_code": nullableString(value.FailureCode), "created_at": httpserver.FormatInstant(value.CreatedAt),
		"started_at":  httpserver.FormatOptionalInstant(value.StartedAt),
		"finished_at": httpserver.FormatOptionalInstant(value.FinishedAt)}}
}

func currentWorkResponse(c *echo.Context, status int, value CurrentWork) error {
	return httpserver.JSONAPI(c, status, map[string]any{"data": currentWorkResource(value)})
}

func currentWorkResource(value CurrentWork) map[string]any {
	attributes := map[string]any{
		"session_state":            value.SessionState,
		"state":                    value.State,
		"remaining_queue_capacity": value.RemainingQueueCapacity,
		"generating_response":      responsePointerResource(value.Generating),
		"queued_work":              messagePointerResource(value.Queued),
		"latest_response":          responsePointerResource(value.LatestResponse),
		"reconciled_message":       messagePointerResource(value.ReconciledMessage),
		"reconciled_response":      responsePointerResource(value.ReconciledResponse),
		"actions": map[string]any{
			"submit":                value.Actions.Submit,
			"queue":                 value.Actions.Queue,
			"stop":                  value.Actions.Stop,
			"stop_response_ids":     value.Actions.StopResponseIDs,
			"retry":                 value.Actions.Retry,
			"retry_response_id":     nullableString(value.Actions.RetryResponseID),
			"reconnect":             value.Actions.Reconnect,
			"reconnect_response_id": nullableString(value.Actions.ReconnectResponseID),
			"finish":                value.Actions.Finish,
		},
	}
	return map[string]any{"type": "tutoring-work-states", "id": value.SessionID, "attributes": attributes}
}

func responsePointerResource(value *Response) any {
	if value == nil {
		return nil
	}
	return responseResource(*value)
}

func messagePointerResource(value *MessageResult) any {
	if value == nil {
		return nil
	}
	return messageResource(*value)
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func tutoringError(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return httpserver.NewError(httpserver.CodeTutoringNotFound)
	case errors.Is(err, ErrUnauthorized):
		return httpserver.NewError(httpserver.CodeTutoringUnauthorized)
	case errors.Is(err, ErrDiscoveryUnavailable):
		return httpserver.NewError(httpserver.CodeInternalError)
	case errors.Is(err, ErrInvalid):
		return httpserver.NewError(httpserver.CodeTutoringInvalid)
	case errors.Is(err, ErrConflict):
		return httpserver.NewError(httpserver.CodeTutoringConflict)
	case errors.Is(err, ErrActiveSession):
		return httpserver.NewError(httpserver.CodeTutoringActiveSession)
	case errors.Is(err, ErrWorkBusy):
		return httpserver.NewError(httpserver.CodeTutoringBusy)
	case errors.Is(err, ErrInvalidState):
		return httpserver.NewError(httpserver.CodeTutoringInvalidState)
	default:
		return err
	}
}
