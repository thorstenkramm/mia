package httpserver

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/labstack/echo/v5"
)

// Code identifies a registered, stable MIA domain error.
type Code string

const (
	CodeCSRFInvalid      Code = "csrf_invalid"
	CodeInternalError    Code = "internal_error"
	CodeMethodNotAllowed Code = "method_not_allowed"
	CodeNotFound         Code = "not_found"
	CodeRateLimited      Code = "rate_limited"
	CodeNotAcceptable    Code = "not_acceptable"
	CodeMalformedRequest Code = "malformed_request"
	// CodeRequestBodyTooLarge and CodeRequestMediaTypeUnsupported are the
	// neutral protocol codes shared by every JSON:API route.
	CodeRequestBodyTooLarge         Code = "request_too_large"
	CodeRequestMediaTypeUnsupported Code = "unsupported_media_type"
	CodeInvalidCredentials          Code = "auth_invalid_credentials"
	CodeInvalidRequest              Code = "auth_invalid_request"
	CodeInvalidPassword             Code = "auth_invalid_password"
	CodeUnauthenticated             Code = "auth_unauthenticated"
	CodePasswordChangeRequired      Code = "auth_password_change_required"
	CodeLoginThrottled              Code = "auth_login_throttled"
	CodeInvalidResetToken           Code = "auth_invalid_reset_token"
	CodeMFARequired                 Code = "auth_mfa_required"
	CodeInvalidMFACode              Code = "auth_invalid_mfa_code"
	CodeMFAStepUsed                 Code = "auth_mfa_step_used"
	CodeInvalidRecoveryCode         Code = "auth_invalid_recovery_code"
	CodeMFAChallengeExpired         Code = "auth_mfa_challenge_expired"
	CodeMFAProofRequired            Code = "auth_mfa_proof_required"
	CodeMFAUnavailable              Code = "auth_mfa_unavailable"

	CodeInvitationNotFound         Code = "invitation_not_found"
	CodeInvitationInvalid          Code = "invitation_invalid"
	CodeInvitationEmailRegistered  Code = "invitation_email_registered"
	CodeInvitationAlreadyAccepted  Code = "invitation_already_accepted"
	CodeInvitationRoleUnauthorized Code = "invitation_role_unauthorized"

	CodeUserRoleUnauthorized       Code = "user_role_unauthorized"
	CodeUserNotFound               Code = "user_not_found"
	CodeUsernameTaken              Code = "username_taken"
	CodeUserProfileInvalid         Code = "user_profile_invalid"
	CodeUserProfileUnauthorized    Code = "user_profile_unauthorized"
	CodeUserMobileUnavailable      Code = "user_mobile_unavailable"
	CodeUserMobileChallengeInvalid Code = "user_mobile_challenge_invalid"
	CodeUserMobileCodeInvalid      Code = "user_mobile_code_invalid"
	CodeUserAvatarInvalid          Code = "user_avatar_invalid"

	CodeInvitationListUnauthorized Code = "invitation_list_unauthorized"

	CodeCourseNotFound              Code = "course_not_found"
	CodeCourseUnauthorized          Code = "course_unauthorized"
	CodeCourseInvalid               Code = "course_invalid"
	CodeCourseNameTaken             Code = "course_name_taken"
	CodeCourseSupervisorInvalid     Code = "course_supervisor_invalid"
	CodeCourseLastSupervisor        Code = "course_last_supervisor"
	CodeCourseActivationUnavailable Code = "course_activation_unavailable"
	CodeCourseInvalidState          Code = "course_invalid_state"
	CodeCourseLogoInvalid           Code = "course_logo_invalid"
	CodeCourseStudentNotFound       Code = "course_student_not_found"
	CodeCourseStudentInvalid        Code = "course_student_invalid"

	CodeMaterialNotFound      Code = "material_not_found"
	CodeMaterialInvalid       Code = "material_invalid"
	CodeMaterialNameTaken     Code = "material_name_taken"
	CodeMaterialInvalidState  Code = "material_invalid_state"
	CodeJobNotFound           Code = "job_not_found"
	CodeJobUnauthorized       Code = "job_unauthorized"
	CodeJobInvalid            Code = "job_invalid"
	CodeTutoringNotFound      Code = "tutoring_not_found"
	CodeTutoringInvalid       Code = "tutoring_invalid"
	CodeTutoringConflict      Code = "tutoring_request_conflict"
	CodeTutoringActiveSession Code = "tutoring_active_session"
	CodeTutoringBusy          Code = "tutoring_work_busy"
	CodeTutoringInvalidState  Code = "tutoring_invalid_state"
)

type definition struct {
	status        int
	title, detail string
}

var errorRegistry = map[Code]definition{
	CodeCSRFInvalid:                 {http.StatusForbidden, "Forbidden", "The request could not be completed."},
	CodeInternalError:               {http.StatusInternalServerError, "Internal Server Error", "The request could not be completed."},
	CodeMethodNotAllowed:            {http.StatusMethodNotAllowed, "Method Not Allowed", "The request method is not supported."},
	CodeNotFound:                    {http.StatusNotFound, "Not Found", "The requested resource was not found."},
	CodeRateLimited:                 {http.StatusTooManyRequests, "Too Many Requests", "The request could not be completed."},
	CodeNotAcceptable:               {http.StatusNotAcceptable, "Not Acceptable", "The request could not be completed."},
	CodeMalformedRequest:            {http.StatusBadRequest, "Malformed Request", "The request could not be completed."},
	CodeRequestBodyTooLarge:         {http.StatusRequestEntityTooLarge, "Request Too Large", "The request could not be completed."},
	CodeRequestMediaTypeUnsupported: {http.StatusUnsupportedMediaType, "Unsupported Media Type", "The request could not be completed."},
	CodeInvalidCredentials:          {http.StatusUnauthorized, "Unauthorized", "The request could not be completed."},
	CodeInvalidRequest:              {http.StatusUnprocessableEntity, "Invalid Request", "The request could not be completed."},
	CodeInvalidPassword:             {http.StatusUnprocessableEntity, "Invalid Password", "The request could not be completed."},
	CodeUnauthenticated:             {http.StatusUnauthorized, "Unauthorized", "The request could not be completed."},
	CodePasswordChangeRequired:      {http.StatusForbidden, "Forbidden", "The request could not be completed."},
	CodeLoginThrottled:              {http.StatusTooManyRequests, "Too Many Requests", "The request could not be completed."},
	CodeInvalidResetToken:           {http.StatusUnprocessableEntity, "Invalid Reset Token", "The request could not be completed."},
	CodeMFARequired:                 {http.StatusForbidden, "MFA Required", "The request could not be completed."},
	CodeInvalidMFACode:              {http.StatusUnprocessableEntity, "Invalid MFA Code", "The request could not be completed."},
	CodeMFAStepUsed:                 {http.StatusForbidden, "MFA Step Used", "The request could not be completed."},
	CodeInvalidRecoveryCode:         {http.StatusUnprocessableEntity, "Invalid Recovery Code", "The request could not be completed."},
	CodeMFAChallengeExpired:         {http.StatusUnprocessableEntity, "MFA Challenge Expired", "The request could not be completed."},
	CodeMFAProofRequired:            {http.StatusUnprocessableEntity, "MFA Proof Required", "The request could not be completed."},
	CodeMFAUnavailable:              {http.StatusUnprocessableEntity, "MFA Unavailable", "The request could not be completed."},

	CodeInvitationNotFound:         {http.StatusNotFound, "Not Found", "The requested resource was not found."},
	CodeInvitationInvalid:          {http.StatusUnprocessableEntity, "Invalid Invitation", "The request could not be completed."},
	CodeInvitationEmailRegistered:  {http.StatusConflict, "Email Already Registered", "The request could not be completed."},
	CodeInvitationAlreadyAccepted:  {http.StatusUnprocessableEntity, "Invitation Already Accepted", "The request could not be completed."},
	CodeInvitationRoleUnauthorized: {http.StatusForbidden, "Unauthorized", "The request could not be completed."},

	CodeUserRoleUnauthorized:       {http.StatusForbidden, "Unauthorized", "The request could not be completed."},
	CodeUserNotFound:               {http.StatusNotFound, "Not Found", "The requested resource was not found."},
	CodeUsernameTaken:              {http.StatusConflict, "Username Taken", "The request could not be completed."},
	CodeUserProfileInvalid:         {http.StatusUnprocessableEntity, "Invalid Profile", "The request could not be completed."},
	CodeUserProfileUnauthorized:    {http.StatusForbidden, "Unauthorized", "The request could not be completed."},
	CodeUserMobileUnavailable:      {http.StatusServiceUnavailable, "Mobile Verification Unavailable", "The request could not be completed."},
	CodeUserMobileChallengeInvalid: {http.StatusUnprocessableEntity, "Invalid Mobile Challenge", "The request could not be completed."},
	CodeUserMobileCodeInvalid:      {http.StatusUnprocessableEntity, "Invalid Mobile Code", "The request could not be completed."},
	CodeUserAvatarInvalid:          {http.StatusUnprocessableEntity, "Invalid Avatar", "The request could not be completed."},

	CodeInvitationListUnauthorized:  {http.StatusForbidden, "Unauthorized", "The request could not be completed."},
	CodeCourseNotFound:              {http.StatusNotFound, "Not Found", "The requested resource was not found."},
	CodeCourseUnauthorized:          {http.StatusForbidden, "Unauthorized", "The request could not be completed."},
	CodeCourseInvalid:               {http.StatusUnprocessableEntity, "Invalid Course", "The request could not be completed."},
	CodeCourseNameTaken:             {http.StatusConflict, "Course Name Taken", "The request could not be completed."},
	CodeCourseSupervisorInvalid:     {http.StatusUnprocessableEntity, "Invalid Supervisor", "The request could not be completed."},
	CodeCourseLastSupervisor:        {http.StatusConflict, "Supervisor Required", "The request could not be completed."},
	CodeCourseActivationUnavailable: {http.StatusConflict, "Activation Unavailable", "The request could not be completed."},
	CodeCourseInvalidState:          {http.StatusConflict, "Invalid Course State", "The request could not be completed."},
	CodeCourseLogoInvalid:           {http.StatusUnprocessableEntity, "Invalid Course Logo", "The request could not be completed."},
	CodeCourseStudentNotFound:       {http.StatusNotFound, "Not Found", "The requested resource was not found."},
	CodeCourseStudentInvalid:        {http.StatusUnprocessableEntity, "Invalid Student", "The request could not be completed."},
	CodeMaterialNotFound:            {http.StatusNotFound, "Not Found", "The requested resource was not found."},
	CodeMaterialInvalid:             {http.StatusUnprocessableEntity, "Invalid Material", "The request could not be completed."},
	CodeMaterialNameTaken:           {http.StatusConflict, "Material Name Taken", "The request could not be completed."},
	CodeMaterialInvalidState:        {http.StatusConflict, "Invalid Material State", "The request could not be completed."},
	CodeJobNotFound:                 {http.StatusNotFound, "Not Found", "The requested resource was not found."},
	CodeJobUnauthorized:             {http.StatusForbidden, "Unauthorized", "The request could not be completed."},
	CodeJobInvalid:                  {http.StatusUnprocessableEntity, "Invalid Job Query", "The request could not be completed."},
	CodeTutoringNotFound:            {http.StatusNotFound, "Not Found", "The requested resource was not found."},
	CodeTutoringInvalid:             {http.StatusUnprocessableEntity, "Invalid Tutoring Request", "The request could not be completed."},
	CodeTutoringConflict:            {http.StatusConflict, "Request ID Conflict", "The request could not be completed."},
	CodeTutoringActiveSession:       {http.StatusConflict, "Active Session Exists", "Finish the active tutoring session first."},
	CodeTutoringBusy:                {http.StatusConflict, "Tutor Busy", "Wait for a tutoring response slot to become available."},
	CodeTutoringInvalidState:        {http.StatusConflict, "Invalid Tutoring State", "The request could not be completed."},
}

// Error is a registered domain error translated centrally to a JSON:API response.
type Error struct{ code Code }

func (err *Error) Error() string { return string(err.code) }

// Code returns the registered stable code for programmatic branching, such as
// distinguishing classified decode failures.
func (err *Error) Code() Code { return err.code }

// NewError returns an error from the shared registry. Codes can only be added
// by extending the registry in this package.
func NewError(code Code) *Error {
	if _, ok := errorRegistry[code]; !ok {
		return &Error{code: CodeInternalError}
	}
	return &Error{code: code}
}

func errorHandler(c *echo.Context, err error) {
	code := CodeInternalError
	directStatus := 0
	var domain *Error
	if errors.As(err, &domain) {
		code = domain.code
	} else {
		var httpError *echo.HTTPError
		if errors.As(err, &httpError) {
			code, directStatus = frameworkError(httpError.Code)
		} else {
			var statusCoder echo.HTTPStatusCoder
			if errors.As(err, &statusCoder) {
				code, directStatus = frameworkError(statusCoder.StatusCode())
			}
		}
	}
	problem := errorRegistry[code]
	if !isAPIPath(c.Request().URL.Path) {
		if directStatus != 0 {
			if writeErr := c.NoContent(directStatus); writeErr != nil {
				c.Logger().Error("write HTTP error response", "error", writeErr)
			}
			return
		}
		if writeErr := c.NoContent(problem.status); writeErr != nil {
			c.Logger().Error("write HTTP error response", "error", writeErr)
		}
		return
	}
	c.Response().Header().Set(echo.HeaderContentType, jsonAPI)
	if writeErr := c.JSON(problem.status, map[string]any{"errors": []map[string]string{{"status": strconv.Itoa(problem.status), "code": string(code), "title": problem.title, "detail": problem.detail}}}); writeErr != nil {
		c.Logger().Error("write HTTP error response", "error", writeErr)
	}
}

func frameworkError(status int) (Code, int) {
	switch status {
	case http.StatusNotFound:
		return CodeNotFound, 0
	case http.StatusMethodNotAllowed:
		return CodeMethodNotAllowed, 0
	default:
		return CodeInternalError, status
	}
}

func isAPIPath(path string) bool { return path == "/api" || strings.HasPrefix(path, "/api/") }
