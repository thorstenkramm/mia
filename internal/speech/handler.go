package speech

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/labstack/echo/v5"
	"github.com/thorstenkramm/mia/internal/httpserver"
	"github.com/thorstenkramm/mia/internal/tutoring"
)

func Register(server *httpserver.Server, service *Service) {
	server.AuthenticatedPOST("/api/v1/tutor-responses/:id/speech", requestHandler(service))
	server.AuthenticatedRoute(http.MethodGet, "/api/v1/tutor-responses/:id/speech",
		httpserver.RepresentationBinary, getHandler(service))
}

func requestHandler(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := httpserver.AuthenticatedUser(c)
		if err != nil {
			return err
		}
		value, err := service.Request(c.Request().Context(), c.Param("id"), actorID)
		if err != nil {
			return speechError(err)
		}
		status := http.StatusAccepted
		if value.State != "generating" {
			status = http.StatusOK
		}
		return resourceResponse(c, status, value)
	}
}

func getHandler(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := httpserver.AuthenticatedUser(c)
		if err != nil {
			return err
		}
		value, err := service.Get(c.Request().Context(), c.Param("id"), actorID)
		if err != nil {
			return speechError(err)
		}
		if value.State != "available" {
			return resourceResponse(c, http.StatusOK, value)
		}
		audio, err := service.ReadAudio(c.Request().Context(), c.Param("id"), actorID)
		if err != nil {
			return speechError(err)
		}
		header := c.Response().Header()
		header.Set("Cache-Control", "private, no-cache")
		header.Set("Content-Disposition", fmt.Sprintf(`inline; filename="%s.mp3"`, audio.Speech.ID))
		header.Set("X-Content-Type-Options", "nosniff")
		return c.Blob(http.StatusOK, "audio/mpeg", audio.Data)
	}
}

func resourceResponse(c *echo.Context, status int, value Speech) error {
	return httpserver.JSONAPI(c, status, map[string]any{"data": map[string]any{
		"type": "generated-speech", "id": value.ID, "attributes": map[string]any{
			"tutor_response_id": value.ResponseID, "state": value.State,
			"failure_code": nullable(value.FailureCode),
			"generated_at": httpserver.FormatOptionalInstant(value.GeneratedAt),
			"expires_at":   httpserver.FormatOptionalInstant(value.ExpiresAt),
		},
	}})
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func speechError(err error) error {
	switch {
	case errors.Is(err, ErrNotFound), errors.Is(err, tutoring.ErrNotFound):
		return httpserver.NewError(httpserver.CodeSpeechNotFound)
	case errors.Is(err, ErrUnavailable):
		return httpserver.NewError(httpserver.CodeSpeechUnavailable)
	case errors.Is(err, ErrVoice):
		return httpserver.NewError(httpserver.CodeSpeechVoiceRequired)
	case errors.Is(err, ErrInvalid):
		return httpserver.NewError(httpserver.CodeSpeechInvalidState)
	default:
		return err
	}
}
