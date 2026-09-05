package course

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"

	"github.com/labstack/echo/v5"
	"github.com/thorstenkramm/mia/internal/httpserver"
	"github.com/thorstenkramm/mia/internal/imagefile"
	"github.com/thorstenkramm/mia/internal/user"
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
	server.AuthenticatedRoute(http.MethodGet, "/api/v1/courses/:id/logo", httpserver.RepresentationBinary, logoHandler(service))
	server.AuthenticatedRoute(http.MethodPut, "/api/v1/courses/:id/logo", httpserver.RepresentationImageUpload, putLogoHandler(service))
	server.AuthenticatedDELETE("/api/v1/courses/:id/logo", deleteLogoHandler(service))
	server.AuthenticatedGET("/api/v1/courses/:id/students", listStudentsHandler(service))
	server.AuthenticatedPOST("/api/v1/courses/:id/students", addStudentHandler(service))
	server.AuthenticatedDELETE("/api/v1/courses/:id/students/:user_id", removeStudentHandler(service))
	server.AuthenticatedPOST("/api/v1/users/:id/temporary-passwords", temporaryPasswordHandler(service))
	server.AuthenticatedPOST("/api/v1/users/:id/bans", banStudentHandler(service))
	server.AuthenticatedDELETE("/api/v1/users/:id/bans", unbanStudentHandler(service))
	server.AuthenticatedPATCH("/api/v1/users/:id", updateStudentHandler(service))
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

type studentRequest struct {
	Data struct {
		Type       string `json:"type"`
		ID         string `json:"id"`
		Attributes struct {
			Mode              json.RawMessage `json:"mode"`
			Username          json.RawMessage `json:"username"`
			TemporaryPassword json.RawMessage `json:"temporary_password"`
			PreferredLanguage json.RawMessage `json:"preferred_language"`
			Country           json.RawMessage `json:"country"`
			TimeZone          json.RawMessage `json:"time_zone"`
		} `json:"attributes"`
	} `json:"data"`
}

type temporaryPasswordRequest struct {
	Data struct {
		Type       string `json:"type"`
		ID         string `json:"id"`
		Attributes struct {
			Password string `json:"password"`
		} `json:"attributes"`
	} `json:"data"`
}

type studentUpdateRequest struct {
	Data struct {
		Type       string `json:"type"`
		ID         string `json:"id"`
		Attributes struct {
			MentoringRequestsAllowed *bool `json:"mentoring_requests_allowed"`
		} `json:"attributes"`
	} `json:"data"`
}

func createHandler(service *Service) echo.HandlerFunc {
	// jscpd:ignore-start
	// Create and PATCH validate distinct JSON:API identity and mutation contracts.
	return func(c *echo.Context) error {
		actorID, err := actor(c)
		if err != nil {
			return err
		}
		var request courseRequest
		if err := httpserver.DecodeJSONAPI(c, &request); err != nil {
			return err
		}
		if request.Data.Type != "courses" || request.Data.ID != "" {
			return denyMutation(c, service, actorID, "course_invalid", httpserver.CodeCourseInvalid)
		}
		// jscpd:ignore-end
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
	// jscpd:ignore-start
	// Course and material collections apply resource-specific scope and error policy.
	return func(c *echo.Context) error {
		actorID, err := actor(c)
		if err != nil {
			return err
		}
		page, err := httpserver.ParsePagination(c.QueryParams())
		if err != nil {
			return httpserver.NewError(httpserver.CodeCourseInvalid)
		}
		// jscpd:ignore-end
		result, err := service.List(c.Request().Context(), actorID, ListInput{Limit: page.Limit, Offset: page.Offset})
		if err != nil {
			return err
		}
		data := make([]map[string]any, 0, len(result.Courses))
		for _, value := range result.Courses {
			data = append(data, courseResource(value))
		}
		return collection(c, data, page, result.HasMore)
	}
}

func getHandler(service *Service) echo.HandlerFunc {
	// jscpd:ignore-start
	// Course and material reads retain separate authorization and domain-error mapping.
	return func(c *echo.Context) error {
		actorID, err := actor(c)
		if err != nil {
			return err
		}
		value, err := service.Get(c.Request().Context(), c.Param("id"), actorID)
		if err != nil {
			return courseError(err)
		}
		// jscpd:ignore-end
		return courseResponse(c, http.StatusOK, value)
	}
}

func updateHandler(service *Service) echo.HandlerFunc {
	// jscpd:ignore-start
	// PATCH validation is separate for course fields and material briefs.
	return func(c *echo.Context) error {
		actorID, err := actor(c)
		if err != nil {
			return err
		}
		var request courseRequest
		if err := httpserver.DecodeJSONAPI(c, &request); err != nil {
			return err
		}
		// jscpd:ignore-end
		// PATCH identity: the document must carry a non-empty resource ID
		// exactly matching the path resource ID.
		if request.Data.Type != "courses" ||
			request.Data.ID == "" || request.Data.ID != c.Param("id") ||
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
	// jscpd:ignore-start
	// Course deletion and material deletion have distinct lifecycle rules.
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
	// jscpd:ignore-end
}

func supervisorsHandler(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := actor(c)
		if err != nil {
			return err
		}
		page, err := httpserver.ParsePagination(c.QueryParams())
		if err != nil {
			return httpserver.NewError(httpserver.CodeCourseInvalid)
		}
		ids, hasMore, err := service.Supervisors(c.Request().Context(), c.Param("id"), actorID,
			ListInput{Limit: page.Limit, Offset: page.Offset})
		if err != nil {
			return courseError(err)
		}
		data := make([]map[string]any, 0, len(ids))
		for _, id := range ids {
			data = append(data, map[string]any{"type": "users", "id": id})
		}
		return collection(c, data, page, hasMore)
	}
}

func assignSupervisorHandler(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := actor(c)
		if err != nil {
			return err
		}
		var request supervisorRequest
		if err := httpserver.DecodeJSONAPI(c, &request); err != nil {
			return err
		}
		if request.Data.Type != "users" || request.Data.ID == "" {
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

func listStudentsHandler(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := actor(c)
		if err != nil {
			return err
		}
		page, err := httpserver.ParsePagination(c.QueryParams())
		if err != nil {
			return httpserver.NewError(httpserver.CodeCourseStudentInvalid)
		}
		result, err := service.ListStudents(c.Request().Context(), c.Param("id"), actorID,
			ListInput{Limit: page.Limit, Offset: page.Offset})
		if err != nil {
			return courseError(err)
		}
		data := make([]map[string]any, 0, len(result.Memberships))
		for _, membership := range result.Memberships {
			data = append(data, membershipResource(membership))
		}
		return collection(c, data, page, result.HasMore)
	}
}

func addStudentHandler(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := actor(c)
		if err != nil {
			return err
		}
		var request studentRequest
		if err := httpserver.DecodeJSONAPI(c, &request); err != nil {
			return err
		}
		if request.Data.Type != "course-students" || request.Data.ID != "" {
			return denyMutation(c, service, actorID, "course_student_invalid",
				httpserver.CodeCourseStudentInvalid)
		}
		mode, modeSet := rawString(request.Data.Attributes.Mode)
		username, usernameSet := rawString(request.Data.Attributes.Username)
		if !modeSet || !usernameSet || username == "" {
			return denyMutation(c, service, actorID, "course_student_invalid",
				httpserver.CodeCourseStudentInvalid)
		}
		var membership Membership
		switch mode {
		case "existing":
			if anySet(request.Data.Attributes.TemporaryPassword, request.Data.Attributes.PreferredLanguage,
				request.Data.Attributes.Country, request.Data.Attributes.TimeZone) {
				return denyMutation(c, service, actorID, "course_student_invalid",
					httpserver.CodeCourseStudentInvalid)
			}
			membership, err = service.AddStudent(c.Request().Context(), c.Param("id"), username, actorID)
		case "provision":
			password, passwordSet := rawString(request.Data.Attributes.TemporaryPassword)
			language, languageSet := rawString(request.Data.Attributes.PreferredLanguage)
			country, countrySet := rawString(request.Data.Attributes.Country)
			timeZone, timeZoneSet := rawString(request.Data.Attributes.TimeZone)
			if !passwordSet || !languageSet || !countrySet || !timeZoneSet {
				return denyMutation(c, service, actorID, "course_student_invalid",
					httpserver.CodeCourseStudentInvalid)
			}
			membership, err = service.ProvisionStudent(c.Request().Context(), c.Param("id"), ProvisionStudentInput{
				Username: username, Password: password, Language: language, Country: country, TimeZone: timeZone,
				ActorID: actorID,
			})
		default:
			return denyMutation(c, service, actorID, "course_student_invalid",
				httpserver.CodeCourseStudentInvalid)
		}
		if err != nil {
			return mutationError(c, service, actorID, err)
		}
		return jsonAPI(c, http.StatusCreated, map[string]any{"data": membershipResource(membership)})
	}
}

func removeStudentHandler(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := actor(c)
		if err != nil {
			return err
		}
		if err := service.RemoveStudent(c.Request().Context(), c.Param("id"), c.Param("user_id"), actorID); err != nil {
			return mutationError(c, service, actorID, err)
		}
		return c.NoContent(http.StatusNoContent)
	}
}

func temporaryPasswordHandler(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := actor(c)
		if err != nil {
			return err
		}
		var request temporaryPasswordRequest
		if err := httpserver.DecodeJSONAPI(c, &request); err != nil {
			return err
		}
		if request.Data.Type != "temporary-passwords" || request.Data.ID != "" ||
			request.Data.Attributes.Password == "" {
			return denyMutation(c, service, actorID, "course_student_invalid",
				httpserver.CodeCourseStudentInvalid)
		}
		if err := service.SetTemporaryPassword(c.Request().Context(), c.Param("id"), actorID,
			request.Data.Attributes.Password); err != nil {
			return mutationError(c, service, actorID, err)
		}
		return c.NoContent(http.StatusNoContent)
	}
}

func banStudentHandler(service *Service) echo.HandlerFunc {
	return studentBanHandler(service, true)
}

func unbanStudentHandler(service *Service) echo.HandlerFunc {
	return studentBanHandler(service, false)
}

func updateStudentHandler(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := actor(c)
		if err != nil {
			return err
		}
		var request studentUpdateRequest
		if err := httpserver.DecodeJSONAPI(c, &request); err != nil {
			return err
		}
		if request.Data.Type != "users" || request.Data.ID != c.Param("id") ||
			request.Data.Attributes.MentoringRequestsAllowed == nil {
			return denyMutation(c, service, actorID, "course_student_invalid", httpserver.CodeCourseStudentInvalid)
		}
		if err := service.SetMentoringRequestsAllowed(c.Request().Context(), c.Param("id"), actorID,
			*request.Data.Attributes.MentoringRequestsAllowed); err != nil {
			return mutationError(c, service, actorID, err)
		}
		return c.NoContent(http.StatusNoContent)
	}
}

func studentBanHandler(service *Service, banned bool) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := actor(c)
		if err != nil {
			return err
		}
		if err := service.SetStudentBanned(c.Request().Context(), c.Param("id"), actorID, banned); err != nil {
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
	// jscpd:ignore-start
	// Course logos and staff avatars have separate authorization and cache ownership.
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
	// jscpd:ignore-end
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
	// jscpd:ignore-start
	// Course optional fields and profile fields use distinct domain types.
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
	// jscpd:ignore-end
}

func rawString(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false
	}
	return value, true
}

func anySet(values ...json.RawMessage) bool {
	for _, value := range values {
		if len(value) != 0 {
			return true
		}
	}
	return false
}

func actor(c *echo.Context) (string, error) {
	return httpserver.AuthenticatedUser(c)
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
		"language": value.Language, "state": state, "created_at": httpserver.FormatInstant(value.CreatedAt),
		"activated_at":   httpserver.FormatOptionalInstant(value.ActivatedAt),
		"deactivated_at": httpserver.FormatOptionalInstant(value.DeactivatedAt),
		"updated_at":     httpserver.FormatOptionalInstant(value.UpdatedAt), "logo_url": nil,
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

func membershipResource(value Membership) map[string]any {
	return map[string]any{
		"type": "course-students",
		"id":   value.ID,
		"attributes": map[string]any{
			"username":  value.Username,
			"joined_at": httpserver.FormatInstant(value.JoinedAt),
		},
		"relationships": map[string]any{
			"course":  map[string]any{"data": map[string]string{"type": "courses", "id": value.CourseID}},
			"student": map[string]any{"data": map[string]string{"type": "users", "id": value.StudentID}},
		},
	}
}

func jsonAPI(c *echo.Context, status int, document any) error {
	return httpserver.JSONAPI(c, status, document)
}

// collection writes one paginated JSON:API collection page with meta.has_more
// and the shared prev and next navigation links when those pages exist.
func collection(c *echo.Context, data []map[string]any, page httpserver.Page, hasMore bool) error {
	return httpserver.Collection(c, data, page, hasMore)
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
	case errors.Is(err, ErrStudentNotFound):
		return httpserver.NewError(httpserver.CodeCourseStudentNotFound)
	case errors.Is(err, ErrStudentInvalid):
		return httpserver.NewError(httpserver.CodeCourseStudentInvalid)
	case errors.Is(err, user.ErrUsernameTaken):
		return httpserver.NewError(httpserver.CodeUsernameTaken)
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
	case errors.Is(err, ErrStudentNotFound):
		return "course_student_not_found"
	case errors.Is(err, ErrStudentInvalid):
		return "course_student_invalid"
	case errors.Is(err, user.ErrUsernameTaken):
		return "username_taken"
	default:
		return ""
	}
}

// denyMutation audits a domain-level denied mutation. Protocol failures
// classified by the shared decoder (415, 413, 400) are not domain mutations
// and return without an audit event, matching the Phase 3a auth migration.
func denyMutation(c *echo.Context, service *Service, actorID, outcome string, code httpserver.Code) error {
	service.AuditMutationDenied(c.Request().Context(), actorID, outcome)
	return httpserver.NewError(code)
}
