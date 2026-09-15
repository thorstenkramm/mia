package mentoring

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/labstack/echo/v5"
	"github.com/thorstenkramm/mia/internal/httpserver"
)

type mentorAssignmentRequest struct {
	Data struct {
		Type          string `json:"type"`
		ID            string `json:"id,omitempty"`
		Relationships struct {
			Mentor struct {
				Data struct {
					Type string `json:"type"`
					ID   string `json:"id"`
				} `json:"data"`
			} `json:"mentor"`
		} `json:"relationships"`
	} `json:"data"`
}

type sessionRequest struct {
	Data struct {
		Type       string `json:"type"`
		ID         string `json:"id,omitempty"`
		Attributes struct {
			Topic               json.RawMessage `json:"topic"`
			Response            json.RawMessage `json:"response"`
			ProposedFor         json.RawMessage `json:"proposed_for"`
			ScheduledFor        json.RawMessage `json:"scheduled_for"`
			MeetingInstructions json.RawMessage `json:"meeting_instructions"`
			MeetingURL          json.RawMessage `json:"meeting_url"`
			ClosureReason       json.RawMessage `json:"closure_reason"`
		} `json:"attributes"`
		Relationships struct {
			Mentor *struct {
				Data *struct {
					Type string `json:"type"`
					ID   string `json:"id"`
				} `json:"data"`
			} `json:"mentor"`
		} `json:"relationships"`
	} `json:"data"`
}

func Register(server *httpserver.Server, service *Service) {
	server.AuthenticatedGET("/api/v1/courses/:course_id/mentors", listCourseMentorsHandler(service))
	server.AuthenticatedPOST("/api/v1/courses/:course_id/mentors", assignCourseMentorHandler(service))
	server.AuthenticatedGET("/api/v1/courses/:course_id/mentors/:user_id", reviewCourseMentorRemovalHandler(service))
	server.AuthenticatedDELETE("/api/v1/courses/:course_id/mentors/:user_id", removeCourseMentorHandler(service))
	server.AuthenticatedGET("/api/v1/courses/:course_id/students/:student_id/mentors",
		listStudentMentorsHandler(service))
	server.AuthenticatedPOST("/api/v1/courses/:course_id/students/:student_id/mentors",
		assignStudentMentorHandler(service))
	server.AuthenticatedGET("/api/v1/courses/:course_id/students/:student_id/mentors/:user_id",
		reviewStudentMentorRemovalHandler(service))
	server.AuthenticatedDELETE("/api/v1/courses/:course_id/students/:student_id/mentors/:user_id",
		removeStudentMentorHandler(service))
	server.AuthenticatedGET("/api/v1/courses/:course_id/mentor-students/:student_id",
		mentorStudentProfileHandler(service))
	server.AuthenticatedRoute(http.MethodGet, "/api/v1/courses/:course_id/mentor-students/:student_id/avatar",
		httpserver.RepresentationBinary, mentorStudentAvatarHandler(service))
	server.AuthenticatedGET("/api/v1/courses/:course_id/mentoring-sessions", listSessionsHandler(service))
	server.AuthenticatedGET("/api/v1/courses/:course_id/mentoring-request-eligibility",
		requestEligibilityHandler(service))
	server.AuthenticatedPOST("/api/v1/courses/:course_id/mentoring-sessions", createSessionHandler(service))
	server.AuthenticatedGET("/api/v1/mentoring-sessions/:id", getSessionHandler(service))
	server.AuthenticatedGET("/api/v1/mentoring-sessions/:id/completion-eligibility",
		completionEligibilityHandler(service))
	server.AuthenticatedPATCH("/api/v1/mentoring-sessions/:id", updateSessionHandler(service))
}

func assignCourseMentorHandler(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := httpserver.AuthenticatedUser(c)
		if err != nil {
			return err
		}
		mentorID, err := decodeMentorAssignment(c, "course-mentor-assignments")
		if err != nil {
			if errors.Is(err, ErrInvalid) {
				return denyMutation(c, service, actorID, err)
			}
			return err
		}
		if err := service.AssignCourseMentor(c.Request().Context(), c.Param("course_id"), mentorID, actorID); err != nil {
			return mutationError(c, service, actorID, err)
		}
		return c.NoContent(http.StatusNoContent)
	}
}

func assignStudentMentorHandler(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := httpserver.AuthenticatedUser(c)
		if err != nil {
			return err
		}
		mentorID, err := decodeMentorAssignment(c, "student-mentor-assignments")
		if err != nil {
			if errors.Is(err, ErrInvalid) {
				return denyMutation(c, service, actorID, err)
			}
			return err
		}
		if err := service.AssignStudentMentor(c.Request().Context(), c.Param("course_id"), c.Param("student_id"),
			mentorID, actorID); err != nil {
			return mutationError(c, service, actorID, err)
		}
		return c.NoContent(http.StatusNoContent)
	}
}

func decodeMentorAssignment(c *echo.Context, resourceType string) (string, error) {
	var body mentorAssignmentRequest
	if err := httpserver.DecodeJSONAPI(c, &body); err != nil {
		return "", err
	}
	mentor := body.Data.Relationships.Mentor.Data
	if body.Data.Type != resourceType || body.Data.ID != "" || mentor.Type != "users" || mentor.ID == "" {
		return "", ErrInvalid
	}
	return mentor.ID, nil
}

func removeCourseMentorHandler(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := httpserver.AuthenticatedUser(c)
		if err != nil {
			return err
		}
		if err := service.RemoveCourseMentor(c.Request().Context(), c.Param("course_id"), c.Param("user_id"), actorID,
			removalIfMatch(c)); err != nil {
			return mutationError(c, service, actorID, err)
		}
		return c.NoContent(http.StatusNoContent)
	}
}

func removeStudentMentorHandler(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := httpserver.AuthenticatedUser(c)
		if err != nil {
			return err
		}
		if err := service.RemoveStudentMentor(c.Request().Context(), c.Param("course_id"), c.Param("student_id"),
			c.Param("user_id"), actorID, removalIfMatch(c)); err != nil {
			return mutationError(c, service, actorID, err)
		}
		return c.NoContent(http.StatusNoContent)
	}
}

// removalIfMatch preserves missing-header semantics while ensuring multiple validators can never match one review.
func removalIfMatch(c *echo.Context) string {
	values := c.Request().Header.Values("If-Match")
	if len(values) == 0 {
		return ""
	}
	if len(values) != 1 || strings.Contains(values[0], ",") {
		return strings.Join(values, ",")
	}
	return values[0]
}

func reviewCourseMentorRemovalHandler(service *Service) echo.HandlerFunc {
	return removalReviewHandler(func(c *echo.Context, actorID string) (RemovalReview, error) {
		return service.ReviewCourseMentorRemoval(c.Request().Context(), c.Param("course_id"), c.Param("user_id"), actorID)
	})
}

func reviewStudentMentorRemovalHandler(service *Service) echo.HandlerFunc {
	return removalReviewHandler(func(c *echo.Context, actorID string) (RemovalReview, error) {
		return service.ReviewStudentMentorRemoval(c.Request().Context(), c.Param("course_id"), c.Param("student_id"),
			c.Param("user_id"), actorID)
	})
}

func removalReviewHandler(review func(*echo.Context, string) (RemovalReview, error)) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := httpserver.AuthenticatedUser(c)
		if err != nil {
			return err
		}
		value, err := review(c, actorID)
		if err != nil {
			return mentoringError(err)
		}
		c.Response().Header().Set("ETag", value.ETag)
		return httpserver.JSONAPI(c, http.StatusOK, map[string]any{"data": removalReviewResource(value)})
	}
}

func listCourseMentorsHandler(service *Service) echo.HandlerFunc {
	return assignmentListHandler(func(c *echo.Context, input ListInput, actorID string) (AssignmentListResult, error) {
		return service.ListCourseMentors(c.Request().Context(), c.Param("course_id"), actorID, input)
	})
}

func listStudentMentorsHandler(service *Service) echo.HandlerFunc {
	return assignmentListHandler(func(c *echo.Context, input ListInput, actorID string) (AssignmentListResult, error) {
		return service.ListStudentMentors(c.Request().Context(), c.Param("course_id"), c.Param("student_id"), actorID,
			input)
	})
}

func assignmentListHandler(list func(*echo.Context, ListInput, string) (AssignmentListResult, error)) echo.HandlerFunc {
	return httpserver.CollectionHandler(mentoringError, ErrInvalid,
		func(c *echo.Context, page httpserver.Page, actorID string) ([]Assignment, bool, error) {
			result, err := list(c, ListInput{Limit: page.Limit, Offset: page.Offset}, actorID)
			return result.Assignments, result.HasMore, err
		},
		func(value Assignment) map[string]any {
			return map[string]any{"type": "users", "id": value.MentorID}
		})
}

// sessionMutationHandler authenticates the actor and decodes the session request
// body. Each mutation keeps its own attribute invariants and audits its own
// denial, because create and update accept different attributes.
func sessionMutationHandler(mutate func(*echo.Context, sessionRequest, string) error) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := httpserver.AuthenticatedUser(c)
		if err != nil {
			return err
		}
		var body sessionRequest
		if err := httpserver.DecodeJSONAPI(c, &body); err != nil {
			return err
		}
		return mutate(c, body, actorID)
	}
}

func createSessionHandler(service *Service) echo.HandlerFunc {
	return sessionMutationHandler(func(c *echo.Context, body sessionRequest, actorID string) error {
		topic, topicSet := rawString(body.Data.Attributes.Topic)
		proposed, err := rawTime(body.Data.Attributes.ProposedFor)
		if err != nil || body.Data.Type != "mentoring-sessions" || body.Data.ID != "" || !topicSet ||
			anyRaw(body.Data.Attributes.Response, body.Data.Attributes.ScheduledFor,
				body.Data.Attributes.MeetingInstructions, body.Data.Attributes.MeetingURL,
				body.Data.Attributes.ClosureReason) || body.Data.Relationships.Mentor != nil {
			return denyMutation(c, service, actorID, ErrInvalid)
		}
		value, err := service.Create(c.Request().Context(), CreateInput{CourseID: c.Param("course_id"),
			StudentID: actorID, Topic: topic, ProposedFor: proposed.Value})
		if err != nil {
			return mutationError(c, service, actorID, err)
		}
		return sessionResponse(c, http.StatusCreated, value)
	})
}

func requestEligibilityHandler(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := httpserver.AuthenticatedUser(c)
		if err != nil {
			return err
		}
		value, err := service.RequestEligibility(c.Request().Context(), c.Param("course_id"), actorID)
		if err != nil {
			return mentoringError(err)
		}
		return httpserver.Resource(c, http.StatusOK, "mentoring-request-eligibilities", value.ID, map[string]any{
			"state": value.State, "checked_at": httpserver.FormatInstant(value.CheckedAt),
		})
	}
}

// Mentoring session reads keep mentoring-specific authorization, existence
// hiding, and error translation, which is why this matches the tutoring session
// read. The duplication marker lives at internal/tutoring/handler.go.
func getSessionHandler(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := httpserver.AuthenticatedUser(c)
		if err != nil {
			return err
		}
		value, err := service.Get(c.Request().Context(), c.Param("id"), actorID)
		if err != nil {
			return mentoringError(err)
		}
		return sessionResponse(c, http.StatusOK, value)
	}
}

func completionEligibilityHandler(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := httpserver.AuthenticatedUser(c)
		if err != nil {
			return err
		}
		value, err := service.CompletionEligibility(c.Request().Context(), c.Param("id"), actorID)
		if err != nil {
			return mentoringError(err)
		}
		return httpserver.JSONAPI(c, http.StatusOK, map[string]any{"data": completionEligibilityResource(value)})
	}
}

func listSessionsHandler(service *Service) echo.HandlerFunc {
	return httpserver.CollectionHandler(mentoringError, ErrInvalid,
		func(c *echo.Context, page httpserver.Page, actorID string) ([]Session, bool, error) {
			result, err := service.List(c.Request().Context(), c.Param("course_id"), actorID,
				ListInput{Limit: page.Limit, Offset: page.Offset})
			return result.Sessions, result.HasMore, err
		}, sessionResource)
}

func updateSessionHandler(service *Service) echo.HandlerFunc {
	return sessionMutationHandler(func(c *echo.Context, body sessionRequest, actorID string) error {
		if body.Data.Type != "mentoring-sessions" || body.Data.ID != c.Param("id") ||
			anyRaw(body.Data.Attributes.Topic, body.Data.Attributes.ProposedFor) {
			return denyMutation(c, service, actorID, ErrInvalid)
		}
		input, err := updateInput(body, actorID)
		if err != nil {
			return denyMutation(c, service, actorID, ErrInvalid)
		}
		value, err := service.Update(c.Request().Context(), c.Param("id"), input)
		if err != nil {
			return mutationError(c, service, actorID, err)
		}
		return sessionResponse(c, http.StatusOK, value)
	})
}

func updateInput(body sessionRequest, actorID string) (UpdateInput, error) {
	result := UpdateInput{ActorID: actorID}
	var err error
	if len(body.Data.Attributes.Response) != 0 {
		response, ok := rawString(body.Data.Attributes.Response)
		if !ok {
			return result, ErrInvalid
		}
		result.Response = OptionalString{Set: true, Value: &response}
	}
	result.MeetingInstructions, err = rawOptionalString(body.Data.Attributes.MeetingInstructions)
	if err != nil {
		return result, err
	}
	result.MeetingURL, err = rawOptionalString(body.Data.Attributes.MeetingURL)
	if err != nil {
		return result, err
	}
	if len(body.Data.Attributes.ScheduledFor) != 0 {
		result.ScheduledFor, err = rawRequiredTime(body.Data.Attributes.ScheduledFor)
		if err != nil {
			return result, err
		}
	}
	if len(body.Data.Attributes.ClosureReason) != 0 {
		var ok bool
		result.ClosureReason, ok = rawString(body.Data.Attributes.ClosureReason)
		if !ok {
			return result, ErrInvalid
		}
	}
	if body.Data.Relationships.Mentor != nil {
		result.MentorID.Set = true
		if body.Data.Relationships.Mentor.Data == nil || body.Data.Relationships.Mentor.Data.Type != "users" ||
			body.Data.Relationships.Mentor.Data.ID == "" {
			return result, ErrInvalid
		}
		result.MentorID.Value = &body.Data.Relationships.Mentor.Data.ID
	}
	return result, nil
}

func rawOptionalString(raw json.RawMessage) (OptionalString, error) {
	set, value, err := httpserver.OptionalStringAttribute(raw)
	if err != nil {
		return OptionalString{}, err
	}
	return OptionalString{Set: set, Value: value}, nil
}

func rawString(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "", false
	}
	var value string
	return value, json.Unmarshal(raw, &value) == nil
}

func rawTime(raw json.RawMessage) (OptionalTime, error) {
	if len(raw) == 0 {
		return OptionalTime{}, nil
	}
	if bytes.Equal(raw, []byte("null")) {
		return OptionalTime{Set: true}, nil
	}
	value, ok := rawString(raw)
	if !ok {
		return OptionalTime{}, ErrInvalid
	}
	parsed, err := httpserver.ParseInstant(value)
	if err != nil {
		return OptionalTime{}, err
	}
	return OptionalTime{Set: true, Value: &parsed}, nil
}

func rawRequiredTime(raw json.RawMessage) (OptionalTime, error) {
	if bytes.Equal(raw, []byte("null")) {
		return OptionalTime{}, ErrInvalid
	}
	value, err := rawTime(raw)
	if err != nil || !value.Set || value.Value == nil {
		return OptionalTime{}, ErrInvalid
	}
	return value, nil
}

func anyRaw(values ...json.RawMessage) bool {
	for _, value := range values {
		if len(value) != 0 {
			return true
		}
	}
	return false
}

func sessionResponse(c *echo.Context, status int, value Session) error {
	return httpserver.JSONAPI(c, status, map[string]any{"data": sessionResource(value)})
}

func sessionResource(value Session) map[string]any {
	state := "requested"
	if value.ScheduledFor != nil {
		state = "scheduled"
	}
	if value.ClosedAt != nil {
		state = value.ClosureReason
	}
	var mentor any
	if value.MentorID != "" {
		mentor = map[string]string{"type": "users", "id": value.MentorID}
	}
	return map[string]any{"type": "mentoring-sessions", "id": value.ID, "attributes": map[string]any{
		"topic": value.Topic, "response": nullableJSON(value.Response), "responded_at": httpserver.FormatOptionalInstant(value.RespondedAt),
		"responded_by": nullableJSON(value.RespondedBy), "proposed_for": httpserver.FormatOptionalInstant(value.ProposedFor),
		"scheduled_for": httpserver.FormatOptionalInstant(value.ScheduledFor), "meeting_instructions": nullableJSON(value.MeetingInstructions),
		"meeting_url": nullableJSON(value.MeetingURL), "state": state, "created_at": httpserver.FormatInstant(value.CreatedAt),
		"closed_at": httpserver.FormatOptionalInstant(value.ClosedAt), "closed_by": nullableJSON(value.ClosedBy),
		"closure_reason":         nullableJSON(value.ClosureReason),
		"completion_eligibility": completionEligibilityAttributes(value.CompletionEligibility)}, "relationships": map[string]any{
		"course":  map[string]any{"data": map[string]string{"type": "courses", "id": value.CourseID}},
		"student": map[string]any{"data": map[string]string{"type": "users", "id": value.StudentID}},
		"mentor":  map[string]any{"data": mentor}}}
}

func completionEligibilityResource(value CompletionEligibility) map[string]any {
	return map[string]any{"type": "mentoring-completion-eligibilities", "id": value.ID,
		"attributes": completionEligibilityAttributes(value)}
}

func completionEligibilityAttributes(value CompletionEligibility) map[string]any {
	return map[string]any{"state": value.State, "checked_at": httpserver.FormatInstant(value.CheckedAt),
		"recheck_after": httpserver.FormatOptionalInstant(value.RecheckAfter)}
}

func removalReviewResource(value RemovalReview) map[string]any {
	return map[string]any{"type": "mentor-removal-reviews", "id": value.ID, "attributes": map[string]any{
		"scope": value.Scope, "affected_open_work": value.AffectedOpenWork,
		"cleared_fields":                          []string{"mentor", "proposed_for", "scheduled_for", "meeting_instructions", "meeting_url"},
		"preserved_fields":                        []string{"topic", "response", "responded_at", "responded_by"},
		"direct_reassignment_preserves_open_work": true,
		"checked_at":                              httpserver.FormatInstant(value.CheckedAt),
	}}
}

func nullableJSON(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func mentorStudentProfileHandler(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		mentorID, err := httpserver.AuthenticatedUser(c)
		if err != nil {
			return err
		}
		profile, avatar, err := service.MentorStudentProfile(c.Request().Context(), c.Param("course_id"),
			c.Param("student_id"), mentorID)
		if err != nil {
			return mentoringError(err)
		}
		var avatarURL any
		if avatar {
			avatarURL = "/api/v1/courses/" + c.Param("course_id") + "/mentor-students/" + profile.ID + "/avatar"
		}
		return httpserver.Resource(c, http.StatusOK, "users", profile.ID, map[string]any{
			"username": profile.Username, "name": profile.Name, "nickname": profile.Nickname, "avatar_url": avatarURL,
		})
	}
}

func mentorStudentAvatarHandler(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		mentorID, err := httpserver.AuthenticatedUser(c)
		if err != nil {
			return err
		}
		data, err := service.MentorStudentAvatar(c.Request().Context(), c.Param("course_id"),
			c.Param("student_id"), mentorID)
		if err != nil {
			return mentoringError(err)
		}
		return httpserver.PrivateAvatarPNG(c, data)
	}
}

func mentoringError(err error) error {
	code := mentoringCode(err)
	if code == "" {
		return err
	}
	return httpserver.NewError(code)
}

func mutationError(c *echo.Context, service *Service, actorID string, err error) error {
	code := mentoringCode(err)
	if code == "" {
		return err
	}
	service.AuditMutationDenied(c.Request().Context(), actorID, string(code))
	return httpserver.NewError(code)
}

func denyMutation(c *echo.Context, service *Service, actorID string, err error) error {
	return mutationError(c, service, actorID, err)
}

func mentoringCode(err error) httpserver.Code {
	switch {
	case errors.Is(err, ErrNotFound):
		return httpserver.CodeMentoringNotFound
	case errors.Is(err, ErrInvalid):
		return httpserver.CodeMentoringInvalid
	case errors.Is(err, ErrInvalidState):
		return httpserver.CodeMentoringInvalidState
	case errors.Is(err, ErrUnavailable):
		return httpserver.CodeMentoringUnavailable
	case errors.Is(err, ErrStateUnavailable):
		return httpserver.CodeMentoringStateUnavailable
	case errors.Is(err, ErrPreconditionRequired):
		return httpserver.CodeMentoringPreconditionReq
	case errors.Is(err, ErrPreconditionFailed):
		return httpserver.CodeMentoringPreconditionStale
	default:
		return ""
	}
}
