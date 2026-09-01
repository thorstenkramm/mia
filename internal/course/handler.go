package course

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/labstack/echo/v5"
	"github.com/thorstenkramm/mia/internal/httpserver"
	"github.com/thorstenkramm/mia/internal/imagefile"
)

func Register(server *httpserver.Server, service *Service) {
	server.AuthenticatedGET("/api/v1/courses", listHandler(service))
	server.AuthenticatedPOST("/api/v1/courses", createHandler(service))
	server.AuthenticatedGET("/api/v1/courses/:id", getHandler(service))
	server.AuthenticatedPATCH("/api/v1/courses/:id", updateHandler(service))
	server.AuthenticatedDELETE("/api/v1/courses/:id", deleteHandler(service))
	server.AuthenticatedPOST("/api/v1/courses/:id/activations", activateHandler(service))
	server.AuthenticatedPOST("/api/v1/courses/:id/deactivations", deactivateHandler(service))
	server.AuthenticatedGET("/api/v1/courses/:id/supervisors", supervisorsHandler(service))
	server.AuthenticatedPOST("/api/v1/courses/:id/supervisors", assignSupervisorHandler(service))
	server.AuthenticatedDELETE("/api/v1/courses/:id/supervisors/:user_id", removeSupervisorHandler(service))
	server.AuthenticatedGET("/api/v1/courses/:id/logo", logoHandler(service))
	server.AuthenticatedPUT("/api/v1/courses/:id/logo", putLogoHandler(service))
	server.AuthenticatedDELETE("/api/v1/courses/:id/logo", deleteLogoHandler(service))
}

type attributesRequest struct {
	Name                json.RawMessage `json:"name"`
	Description         json.RawMessage `json:"description"`
	Curriculum          json.RawMessage `json:"curriculum"`
	LearningGoals       json.RawMessage `json:"learning_goals"`
	AITutorInstructions json.RawMessage `json:"ai_tutor_instructions"`
	Language            json.RawMessage `json:"language"`
}

type courseRequest struct {
	Data struct {
		Type          string            `json:"type"`
		ID            string            `json:"id,omitempty"`
		Attributes    attributesRequest `json:"attributes"`
		Relationships struct {
			Supervisors struct {
				Data []struct {
					Type string `json:"type"`
					ID   string `json:"id"`
				} `json:"data"`
			} `json:"supervisors"`
		} `json:"relationships"`
	} `json:"data"`
}

type supervisorRequest struct {
	Data struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	} `json:"data"`
}

func createHandler(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := actor(c)
		if err != nil {
			return err
		}
		var request courseRequest
		if err := decode(c, &request); err != nil || request.Data.Type != "courses" || request.Data.ID != "" {
			return denyMutation(c, service, actorID, "course_invalid", httpserver.CodeCourseInvalid)
		}
		fields, err := requestFields(request.Data.Attributes)
		if err != nil || !fields.Name.Set {
			return denyMutation(c, service, actorID, "course_invalid", httpserver.CodeCourseInvalid)
		}
		supervisors := make([]string, len(request.Data.Relationships.Supervisors.Data))
		for index, relationship := range request.Data.Relationships.Supervisors.Data {
			if relationship.Type != "users" || relationship.ID == "" {
				return denyMutation(c, service, actorID, "course_supervisor_invalid",
					httpserver.CodeCourseSupervisorInvalid)
			}
			supervisors[index] = relationship.ID
		}
		created, err := service.Create(c.Request().Context(), CreateInput{Fields: fields,
			SupervisorIDs: supervisors, ActorID: actorID})
		if err != nil {
			return mutationError(c, service, actorID, err)
		}
		return courseResponse(c, http.StatusCreated, created)
	}
}

func listHandler(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := actor(c)
		if err != nil {
			return err
		}
		input, err := listInput(c)
		if err != nil {
			return httpserver.NewError(httpserver.CodeCourseInvalid)
		}
		result, err := service.List(c.Request().Context(), actorID, input)
		if err != nil {
			return err
		}
		data := make([]map[string]any, 0, len(result.Courses))
		for _, value := range result.Courses {
			data = append(data, courseResource(value))
		}
		return jsonAPI(c, http.StatusOK, map[string]any{"data": data,
			"meta": map[string]any{"has_more": result.HasMore}})
	}
}

func listInput(c *echo.Context) (ListInput, error) {
	const defaultLimit = 25
	params := c.QueryParams()
	for key := range params {
		if key != "page[limit]" && key != "page[offset]" {
			return ListInput{}, ErrInvalid
		}
	}
	input := ListInput{Limit: defaultLimit}
	for key, destination := range map[string]*int{"page[limit]": &input.Limit, "page[offset]": &input.Offset} {
		values, exists := params[key]
		if !exists {
			continue
		}
		if len(values) != 1 || values[0] == "" {
			return ListInput{}, ErrInvalid
		}
		value, err := strconv.Atoi(values[0])
		if err != nil {
			return ListInput{}, ErrInvalid
		}
		*destination = value
	}
	if input.Limit < 1 || input.Limit > 100 || input.Offset < 0 || input.Offset > 10_000 {
		return ListInput{}, ErrInvalid
	}
	return input, nil
}

func getHandler(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := actor(c)
		if err != nil {
			return err
		}
		value, err := service.Get(c.Request().Context(), c.Param("id"), actorID)
		if err != nil {
			return courseError(err)
		}
		return courseResponse(c, http.StatusOK, value)
	}
}

func updateHandler(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := actor(c)
		if err != nil {
			return err
		}
		var request courseRequest
		if err := decode(c, &request); err != nil || request.Data.Type != "courses" ||
			request.Data.ID != "" && request.Data.ID != c.Param("id") ||
			len(request.Data.Relationships.Supervisors.Data) != 0 {
			return denyMutation(c, service, actorID, "course_invalid", httpserver.CodeCourseInvalid)
		}
		fields, err := requestFields(request.Data.Attributes)
		if err != nil {
			return denyMutation(c, service, actorID, "course_invalid", httpserver.CodeCourseInvalid)
		}
		value, err := service.Update(c.Request().Context(), c.Param("id"), actorID, fields)
		if err != nil {
			return mutationError(c, service, actorID, err)
		}
		return courseResponse(c, http.StatusOK, value)
	}
}

func activateHandler(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := actor(c)
		if err != nil {
			return err
		}
		value, err := service.Activate(c.Request().Context(), c.Param("id"), actorID)
		if err != nil {
			return mutationError(c, service, actorID, err)
		}
		return courseResponse(c, http.StatusOK, value)
	}
}

func deactivateHandler(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := actor(c)
		if err != nil {
			return err
		}
		value, err := service.Deactivate(c.Request().Context(), c.Param("id"), actorID)
		if err != nil {
			return mutationError(c, service, actorID, err)
		}
		return courseResponse(c, http.StatusOK, value)
	}
}

func deleteHandler(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := actor(c)
		if err != nil {
			return err
		}
		if err := service.Delete(c.Request().Context(), c.Param("id"), actorID); err != nil {
			return mutationError(c, service, actorID, err)
		}
		return c.NoContent(http.StatusNoContent)
	}
}

func supervisorsHandler(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := actor(c)
		if err != nil {
			return err
		}
		ids, err := service.Supervisors(c.Request().Context(), c.Param("id"), actorID)
		if err != nil {
			return courseError(err)
		}
		data := make([]map[string]string, len(ids))
		for index, id := range ids {
			data[index] = map[string]string{"type": "users", "id": id}
		}
		return jsonAPI(c, http.StatusOK, map[string]any{"data": data})
	}
}

func assignSupervisorHandler(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := actor(c)
		if err != nil {
			return err
		}
		var request supervisorRequest
		if err := decode(c, &request); err != nil || request.Data.Type != "users" || request.Data.ID == "" {
			return denyMutation(c, service, actorID, "course_supervisor_invalid",
				httpserver.CodeCourseSupervisorInvalid)
		}
		if err := service.AssignSupervisor(c.Request().Context(), c.Param("id"), request.Data.ID, actorID); err != nil {
			return mutationError(c, service, actorID, err)
		}
		return c.NoContent(http.StatusNoContent)
	}
}

func removeSupervisorHandler(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := actor(c)
		if err != nil {
			return err
		}
		if err := service.RemoveSupervisor(c.Request().Context(), c.Param("id"), c.Param("user_id"), actorID); err != nil {
			return mutationError(c, service, actorID, err)
		}
		return c.NoContent(http.StatusNoContent)
	}
}

func putLogoHandler(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := actor(c)
		if err != nil {
			return err
		}
		mediaType, parameters, err := mime.ParseMediaType(c.Request().Header.Get(echo.HeaderContentType))
		if err != nil || len(parameters) != 0 || mediaType != "image/jpeg" && mediaType != "image/png" ||
			c.Request().ContentLength > imagefile.MaxSourceBytes {
			return denyMutation(c, service, actorID, "course_logo_invalid", httpserver.CodeCourseLogoInvalid)
		}
		c.Request().Body = http.MaxBytesReader(c.Response(), c.Request().Body, imagefile.MaxSourceBytes)
		source, err := io.ReadAll(c.Request().Body)
		if err != nil || len(source) == 0 || mediaType == "image/jpeg" && !bytes.HasPrefix(source, []byte{0xff, 0xd8, 0xff}) ||
			mediaType == "image/png" && !bytes.HasPrefix(source, []byte("\x89PNG\r\n\x1a\n")) {
			return denyMutation(c, service, actorID, "course_logo_invalid", httpserver.CodeCourseLogoInvalid)
		}
		pngData, err := imagefile.Normalize(bytes.NewReader(source))
		if err != nil {
			return denyMutation(c, service, actorID, "course_logo_invalid", httpserver.CodeCourseLogoInvalid)
		}
		if err := service.PutLogo(c.Request().Context(), c.Param("id"), actorID, pngData); err != nil {
			return mutationError(c, service, actorID, err)
		}
		return c.NoContent(http.StatusNoContent)
	}
}

func logoHandler(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := actor(c)
		if err != nil {
			return err
		}
		data, etag, err := service.Logo(c.Request().Context(), c.Param("id"), actorID)
		if err != nil {
			return courseError(err)
		}
		header := c.Response().Header()
		header.Set(echo.HeaderContentType, "image/png")
		header.Set("Cache-Control", "private, no-cache")
		header.Set("Content-Disposition", `inline; filename="course-logo.png"`)
		header.Set("ETag", etag)
		header.Set("X-Content-Type-Options", "nosniff")
		if c.Request().Header.Get("If-None-Match") == etag {
			return c.NoContent(http.StatusNotModified)
		}
		return c.Blob(http.StatusOK, "image/png", data)
	}
}

func deleteLogoHandler(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := actor(c)
		if err != nil {
			return err
		}
		if err := service.DeleteLogo(c.Request().Context(), c.Param("id"), actorID); err != nil {
			return mutationError(c, service, actorID, err)
		}
		return c.NoContent(http.StatusNoContent)
	}
}

func requestFields(attributes attributesRequest) (Fields, error) {
	var fields Fields
	var err error
	fields.Name, err = optional(attributes.Name)
	if err != nil {
		return Fields{}, err
	}
	fields.Description, err = optional(attributes.Description)
	if err != nil {
		return Fields{}, err
	}
	fields.Curriculum, err = optional(attributes.Curriculum)
	if err != nil {
		return Fields{}, err
	}
	fields.LearningGoals, err = optional(attributes.LearningGoals)
	if err != nil {
		return Fields{}, err
	}
	fields.Instructions, err = optional(attributes.AITutorInstructions)
	if err != nil {
		return Fields{}, err
	}
	fields.Language, err = optional(attributes.Language)
	return fields, err
}

func optional(raw json.RawMessage) (OptionalString, error) {
	if len(raw) == 0 {
		return OptionalString{}, nil
	}
	if bytes.Equal(raw, []byte("null")) {
		return OptionalString{Set: true}, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return OptionalString{}, err
	}
	return OptionalString{Set: true, Value: &value}, nil
}

func decode(c *echo.Context, destination any) error {
	const maximumBody = 1 << 20
	mediaType, parameters, err := mime.ParseMediaType(c.Request().Header.Get(echo.HeaderContentType))
	if err != nil || mediaType != "application/vnd.api+json" || len(parameters) != 0 ||
		c.Request().ContentLength > maximumBody {
		return errors.New("invalid course request")
	}
	c.Request().Body = http.MaxBytesReader(c.Response(), c.Request().Body, maximumBody)
	body, err := io.ReadAll(c.Request().Body)
	if err != nil || !utf8.Valid(body) {
		return errors.New("invalid course request")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("invalid trailing course input")
	}
	return nil
}

func actor(c *echo.Context) (string, error) {
	id, ok := c.Get("mia.auth.user_id").(string)
	if !ok || id == "" {
		return "", httpserver.NewError(httpserver.CodeUnauthenticated)
	}
	return id, nil
}

func courseResponse(c *echo.Context, status int, value Course) error {
	return jsonAPI(c, status, map[string]any{"data": courseResource(value)})
}

func courseResource(value Course) map[string]any {
	state := "inactive"
	if value.Active {
		state = "active"
	}
	attributes := map[string]any{
		"name": value.Name, "description": value.Description, "curriculum": value.Curriculum,
		"learning_goals": value.LearningGoals, "ai_tutor_instructions": value.Instructions,
		"language": value.Language, "state": state, "created_at": instant(value.CreatedAt),
		"activated_at": formatTime(value.ActivatedAt), "deactivated_at": formatTime(value.DeactivatedAt),
		"updated_at": formatTime(value.UpdatedAt), "logo_url": nil,
	}
	if value.HasLogo {
		attributes["logo_url"] = "/api/v1/courses/" + value.ID + "/logo"
	}
	supervisors := make([]map[string]string, len(value.SupervisorIDs))
	for index, id := range value.SupervisorIDs {
		supervisors[index] = map[string]string{"type": "users", "id": id}
	}
	return map[string]any{"type": "courses", "id": value.ID, "attributes": attributes,
		"relationships": map[string]any{"supervisors": map[string]any{"data": supervisors}}}
}

func formatTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return instant(*value)
}

func jsonAPI(c *echo.Context, status int, document any) error {
	c.Response().Header().Set(echo.HeaderContentType, "application/vnd.api+json")
	return c.JSON(status, document)
}

func courseError(err error) error {
	switch {
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrLogoNotFound):
		return httpserver.NewError(httpserver.CodeCourseNotFound)
	case errors.Is(err, ErrUnauthorized):
		return httpserver.NewError(httpserver.CodeCourseUnauthorized)
	case errors.Is(err, ErrInvalid):
		return httpserver.NewError(httpserver.CodeCourseInvalid)
	case errors.Is(err, ErrNameTaken):
		return httpserver.NewError(httpserver.CodeCourseNameTaken)
	case errors.Is(err, ErrSupervisorInvalid):
		return httpserver.NewError(httpserver.CodeCourseSupervisorInvalid)
	case errors.Is(err, ErrLastSupervisor):
		return httpserver.NewError(httpserver.CodeCourseLastSupervisor)
	case errors.Is(err, ErrActivationUnavailable):
		return httpserver.NewError(httpserver.CodeCourseActivationUnavailable)
	case errors.Is(err, ErrInvalidState), errors.Is(err, ErrActiveSession):
		return httpserver.NewError(httpserver.CodeCourseInvalidState)
	default:
		return err
	}
}

func mutationError(c *echo.Context, service *Service, actorID string, err error) error {
	if outcome := mutationOutcome(err); outcome != "" {
		service.AuditMutationDenied(c.Request().Context(), actorID, outcome)
	}
	return courseError(err)
}

func mutationOutcome(err error) string {
	switch {
	case errors.Is(err, ErrUnauthorized):
		return "course_unauthorized"
	case errors.Is(err, ErrNotFound):
		return "course_not_found"
	case errors.Is(err, ErrInvalid):
		return "course_invalid"
	case errors.Is(err, ErrNameTaken):
		return "course_name_taken"
	case errors.Is(err, ErrSupervisorInvalid):
		return "course_supervisor_invalid"
	case errors.Is(err, ErrLastSupervisor):
		return "course_last_supervisor"
	case errors.Is(err, ErrActivationUnavailable):
		return "course_activation_unavailable"
	case errors.Is(err, ErrInvalidState):
		return "course_invalid_state"
	case errors.Is(err, ErrActiveSession):
		return "course_active_session"
	default:
		return ""
	}
}

func denyMutation(c *echo.Context, service *Service, actorID, outcome string, code httpserver.Code) error {
	service.AuditMutationDenied(c.Request().Context(), actorID, outcome)
	return httpserver.NewError(code)
}
