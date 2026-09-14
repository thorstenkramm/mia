package user

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v5"
	"github.com/thorstenkramm/mia/internal/httpserver"
	"github.com/thorstenkramm/mia/internal/imagefile"
	"github.com/thorstenkramm/mia/internal/provider/sms"
)

// RegisterProfileRoutes attaches the current-user profile vertical slice.
func RegisterProfileRoutes(server *httpserver.Server, service *Service) {
	server.AuthenticatedGET("/api/v1/users/me", getProfile(service))
	server.AuthenticatedPATCH("/api/v1/users/me", patchProfile(service))
	server.AuthenticatedGET("/api/v1/users/me/mobile-change-challenges", getMobileChallenge(service))
	server.AuthenticatedPOST("/api/v1/users/me/mobile-change-challenges", startMobileChallenge(server, service))
	server.AuthenticatedPOST("/api/v1/users/me/mobile-change-challenges/:id/verifications",
		verifyMobileChallenge(server, service))
	server.AuthenticatedPOST("/api/v1/users/me/mobile-change-challenges/:id/resends",
		resendMobileChallenge(server, service))
	server.AuthenticatedDELETE("/api/v1/users/me/mobile", removeMobile(service))
	server.AuthenticatedRoute(http.MethodGet, "/api/v1/users/me/avatar", httpserver.RepresentationBinary, getAvatar(service))
	server.AuthenticatedRoute(http.MethodPut, "/api/v1/users/me/avatar", httpserver.RepresentationImageUpload, putAvatar(service))
	server.AuthenticatedDELETE("/api/v1/users/me/avatar", deleteAvatar(service))
}

func getMobileChallenge(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		accountID, err := currentUser(c)
		if err != nil {
			return err
		}
		state, err := service.MobileState(c.Request().Context(), accountID)
		if err != nil {
			return profileError(err)
		}
		return mobileStateResponse(c, http.StatusOK, state)
	}
}

func getProfile(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		accountID, err := currentUser(c)
		if err != nil {
			return err
		}
		profile, err := service.GetSelf(c.Request().Context(), accountID)
		if err != nil {
			return err
		}
		return profileResponse(c, profile, service.avatarExists(accountID))
	}
}

type profilePatchRequest struct {
	Data struct {
		Type       string `json:"type"`
		ID         string `json:"id"`
		Attributes struct {
			Name              json.RawMessage `json:"name"`
			Nickname          json.RawMessage `json:"nickname"`
			PreferredLanguage json.RawMessage `json:"preferred_language"`
			Country           json.RawMessage `json:"country"`
			TimeZone          json.RawMessage `json:"time_zone"`
			TTSVoice          json.RawMessage `json:"tts_voice"`
		} `json:"attributes"`
	} `json:"data"`
}

func patchProfile(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		accountID, err := currentUser(c)
		if err != nil {
			return err
		}
		var request profilePatchRequest
		if err := httpserver.DecodeJSONAPI(c, &request); err != nil {
			return err
		}
		// PATCH identity: the document must carry the exact resource identity of
		// the authenticated account; a missing or mismatched ID is invalid.
		if request.Data.Type != "users" || request.Data.ID != accountID {
			return httpserver.NewError(httpserver.CodeUserProfileInvalid)
		}
		changes, err := profileChanges(request)
		if err != nil {
			return httpserver.NewError(httpserver.CodeUserProfileInvalid)
		}
		profile, err := service.UpdateSelf(c.Request().Context(), accountID, changes)
		if err != nil {
			return profileError(err)
		}
		return profileResponse(c, profile, service.avatarExists(accountID))
	}
}

func profileChanges(request profilePatchRequest) (ProfileChanges, error) {
	var changes ProfileChanges
	var err error
	changes.Name, err = optionalString(request.Data.Attributes.Name)
	if err != nil {
		return ProfileChanges{}, err
	}
	changes.Nickname, err = optionalString(request.Data.Attributes.Nickname)
	if err != nil {
		return ProfileChanges{}, err
	}
	changes.TTSVoice, err = optionalString(request.Data.Attributes.TTSVoice)
	if err != nil {
		return ProfileChanges{}, err
	}
	changes.PreferredLanguage, err = requiredString(request.Data.Attributes.PreferredLanguage)
	if err != nil {
		return ProfileChanges{}, err
	}
	changes.Country, err = requiredString(request.Data.Attributes.Country)
	if err != nil {
		return ProfileChanges{}, err
	}
	changes.TimeZone, err = requiredString(request.Data.Attributes.TimeZone)
	return changes, err
}

func optionalString(raw json.RawMessage) (OptionalString, error) {
	set, value, err := httpserver.OptionalStringAttribute(raw)
	if err != nil {
		return OptionalString{}, err
	}
	return OptionalString{Set: set, Value: value}, nil
}

func requiredString(raw json.RawMessage) (*string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil || value == "" {
		return nil, errors.New("required profile string is invalid")
	}
	return &value, nil
}

type mobileStartRequest struct {
	Data struct {
		Type       string `json:"type"`
		ID         string `json:"id"`
		Attributes struct {
			Mobile string `json:"mobile"`
		} `json:"attributes"`
	} `json:"data"`
}

type mobileVerificationRequest struct {
	Data struct {
		Type       string `json:"type"`
		ID         string `json:"id"`
		Attributes struct {
			Code string `json:"code"`
		} `json:"attributes"`
	} `json:"data"`
}

func startMobileChallenge(server *httpserver.Server, service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		accountID, err := currentUser(c)
		if err != nil {
			return err
		}
		if result := server.CheckMFA(c, accountID); !result.Allowed {
			return rateLimited(c, result)
		}
		var request mobileStartRequest
		if err := httpserver.DecodeJSONAPI(c, &request); err != nil {
			return err
		}
		if request.Data.Type != "mobile-change-challenges" || request.Data.ID != "" ||
			request.Data.Attributes.Mobile == "" {
			return httpserver.NewError(httpserver.CodeUserProfileInvalid)
		}
		state, err := service.StartMobileChallengeState(c.Request().Context(), accountID, request.Data.Attributes.Mobile)
		if err != nil {
			return profileErrorWithRetry(c, err)
		}
		return mobileStateResponse(c, http.StatusCreated, state)
	}
}

func verifyMobileChallenge(server *httpserver.Server, service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		accountID, err := currentUser(c)
		if err != nil {
			return err
		}
		if result := server.CheckMFA(c, accountID); !result.Allowed {
			return rateLimited(c, result)
		}
		var request mobileVerificationRequest
		if err := httpserver.DecodeJSONAPI(c, &request); err != nil {
			return err
		}
		if request.Data.Type != "mobile-change-verifications" || request.Data.ID != "" ||
			request.Data.Attributes.Code == "" {
			return httpserver.NewError(httpserver.CodeUserProfileInvalid)
		}
		state, err := service.VerifyMobileChallengeState(c.Request().Context(), accountID, c.Param("id"),
			request.Data.Attributes.Code)
		if err != nil {
			return profileError(err)
		}
		return mobileStateResponse(c, http.StatusOK, state)
	}
}

func resendMobileChallenge(server *httpserver.Server, service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		accountID, err := currentUser(c)
		if err != nil {
			return err
		}
		if result := server.CheckMFA(c, accountID); !result.Allowed {
			return rateLimited(c, result)
		}
		state, err := service.ResendMobileChallengeState(c.Request().Context(), accountID, c.Param("id"))
		if err != nil {
			return profileErrorWithRetry(c, err)
		}
		return mobileStateResponse(c, http.StatusOK, state)
	}
}

func removeMobile(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		accountID, err := currentUser(c)
		if err != nil {
			return err
		}
		if err := service.RemoveMobile(c.Request().Context(), accountID); err != nil {
			return profileError(err)
		}
		return c.NoContent(http.StatusNoContent)
	}
}

func putAvatar(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		accountID, err := currentUser(c)
		if err != nil {
			return err
		}
		if err := service.requireStaff(c.Request().Context(), accountID); err != nil {
			return profileError(err)
		}
		mediaType, parameters, err := mime.ParseMediaType(c.Request().Header.Get(echo.HeaderContentType))
		if err != nil || len(parameters) != 0 || mediaType != "image/jpeg" && mediaType != "image/png" {
			return httpserver.NewError(httpserver.CodeUserAvatarInvalid)
		}
		if c.Request().ContentLength > imagefile.MaxSourceBytes {
			return httpserver.NewError(httpserver.CodeUserAvatarInvalid)
		}
		c.Request().Body = http.MaxBytesReader(c.Response(), c.Request().Body, imagefile.MaxSourceBytes)
		source, err := io.ReadAll(c.Request().Body)
		if err != nil || len(source) == 0 || mediaType == "image/jpeg" && !bytes.HasPrefix(source, []byte{0xff, 0xd8, 0xff}) ||
			mediaType == "image/png" && !bytes.HasPrefix(source, []byte("\x89PNG\r\n\x1a\n")) {
			return httpserver.NewError(httpserver.CodeUserAvatarInvalid)
		}
		pngData, err := imagefile.Normalize(bytes.NewReader(source))
		if err != nil {
			return httpserver.NewError(httpserver.CodeUserAvatarInvalid)
		}
		if err := service.PutAvatar(c.Request().Context(), accountID, pngData); err != nil {
			return profileError(err)
		}
		return c.NoContent(http.StatusNoContent)
	}
}

func getAvatar(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		accountID, err := currentUser(c)
		if err != nil {
			return err
		}
		data, err := service.Avatar(c.Request().Context(), accountID)
		if err != nil {
			return profileError(err)
		}
		return httpserver.PrivateAvatarPNG(c, data)
	}
}

func deleteAvatar(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		accountID, err := currentUser(c)
		if err != nil {
			return err
		}
		if err := service.DeleteAvatar(c.Request().Context(), accountID); err != nil {
			return profileError(err)
		}
		return c.NoContent(http.StatusNoContent)
	}
}

func profileResponse(c *echo.Context, profile Profile, avatar bool) error {
	attributes := map[string]any{
		"username": profile.Username, "email": profile.Email, "name": profile.Name, "nickname": profile.Nickname,
		"preferred_language": profile.PreferredLanguage, "country": profile.Country, "time_zone": profile.TimeZone,
		"tts_voice": profile.TTSVoice, "has_verified_mobile": profile.Mobile != nil, "avatar_url": nil,
	}
	if avatar {
		attributes["avatar_url"] = "/api/v1/users/me/avatar"
	}
	return resource(c, http.StatusOK, "users", profile.ID, attributes)
}

func mobileStateResponse(c *echo.Context, status int, state MobileState) error {
	attributes := map[string]any{
		"state":          state.Lifecycle,
		"challenge_id":   state.ChallengeID,
		"expires_at":     httpserver.FormatOptionalInstant(state.ExpiresAt),
		"resend_state":   state.ResendState,
		"next_resend_at": httpserver.FormatOptionalInstant(state.NextResendAt),
	}
	return resource(c, status, "mobile-verification-states", state.AccountID, attributes)
}

func resource(c *echo.Context, status int, resourceType, id string, attributes any) error {
	return httpserver.Resource(c, status, resourceType, id, attributes)
}

func currentUser(c *echo.Context) (string, error) {
	return httpserver.AuthenticatedUser(c)
}

func profileError(err error) error {
	switch {
	case errors.Is(err, ErrProfileNotStaff):
		return httpserver.NewError(httpserver.CodeUserProfileUnauthorized)
	case errors.Is(err, ErrProfileInvalid):
		return httpserver.NewError(httpserver.CodeUserProfileInvalid)
	case errors.Is(err, ErrMobileUnavailable):
		return httpserver.NewError(httpserver.CodeUserMobileUnavailable)
	case errors.Is(err, sms.ErrRateLimited):
		return httpserver.NewError(httpserver.CodeRateLimited)
	case errors.Is(err, ErrMobileChallenge):
		return httpserver.NewError(httpserver.CodeUserMobileChallengeInvalid)
	case errors.Is(err, ErrMobileCode):
		return httpserver.NewError(httpserver.CodeUserMobileCodeInvalid)
	case errors.Is(err, ErrAvatarNotFound):
		return httpserver.NewError(httpserver.CodeNotFound)
	default:
		return err
	}
}

func profileErrorWithRetry(c *echo.Context, err error) error {
	var limit *sms.LimitError
	if !errors.As(err, &limit) {
		return profileError(err)
	}
	retry := limit.RetryAfter
	if retry > 0 {
		c.Response().Header().Set("Retry-After", strconv.Itoa(max(1, int(retry.Seconds()+.999))))
	}
	if limit.Eligibility.Reason == sms.LimitCooldown {
		return httpserver.NewError(httpserver.CodeUserMobileResendCooldown)
	}
	return httpserver.NewError(httpserver.CodeRateLimited)
}

func rateLimited(c *echo.Context, result httpserver.Result) error {
	if result.RetryAfter > 0 {
		c.Response().Header().Set("Retry-After", strconv.Itoa(max(1, int(result.RetryAfter.Seconds()+.999))))
	}
	return httpserver.NewError(httpserver.CodeRateLimited)
}
