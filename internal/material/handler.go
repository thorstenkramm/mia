package material

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/labstack/echo/v5"
	"github.com/thorstenkramm/mia/internal/httpserver"
	"github.com/thorstenkramm/mia/internal/jobs"
)

type request struct {
	Data struct {
		Type       string `json:"type"`
		ID         string `json:"id,omitempty"`
		Attributes struct {
			Name, Scope, Kind, Format string
			ExternalURL               *string         `json:"external_url"`
			Brief                     json.RawMessage `json:"brief"`
		} `json:"attributes"`
	} `json:"data"`
}

func Register(server *httpserver.Server, service *Service, oversight *jobs.Oversight) {
	server.AuthenticatedGET("/api/v1/courses/:course_id/materials", listHandler(service))
	server.AuthenticatedPOST("/api/v1/courses/:course_id/materials", createHandler(service))
	server.AuthenticatedGET("/api/v1/materials/:id", getHandler(service))
	server.AuthenticatedPATCH("/api/v1/materials/:id", updateHandler(service))
	server.AuthenticatedDELETE("/api/v1/materials/:id", deleteHandler(service))
	server.AuthenticatedPOST("/api/v1/materials/:id/finalizations", finalizeHandler(service))
	server.AuthenticatedPOST("/api/v1/materials/:id/approvals", approvalHandler(service, true))
	server.AuthenticatedDELETE("/api/v1/materials/:id/approvals", approvalHandler(service, false))
	server.AuthenticatedGET("/api/v1/materials/:id/files", filesHandler(service))
	server.AuthenticatedPOST("/api/v1/materials/:id/files", uploadHandler(service))
	server.AuthenticatedGET("/api/v1/material-files/:id", fileHandler(service))
	server.AuthenticatedDELETE("/api/v1/material-files/:id", deleteFileHandler(service))
	server.AuthenticatedGET("/api/v1/material-files/:id/download", downloadHandler(service, false))
	server.AuthenticatedGET("/api/v1/material-files/:id/content", downloadHandler(service, true))
	server.AuthenticatedGET("/api/v1/materials/:id/jobs", materialJobsHandler(service, oversight))
}

func createHandler(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := actor(c)
		if err != nil {
			return err
		}
		var body request
		if err := decode(c, &body); err != nil || body.Data.Type != "materials" || body.Data.ID != "" {
			return httpserver.NewError(httpserver.CodeMaterialInvalid)
		}
		var brief *Brief
		if len(body.Data.Attributes.Brief) != 0 && !bytes.Equal(body.Data.Attributes.Brief, []byte("null")) {
			parsed, err := decodeBrief(body.Data.Attributes.Brief)
			if err != nil {
				return httpserver.NewError(httpserver.CodeMaterialInvalid)
			}
			brief = &parsed
		}
		externalURL := ""
		if body.Data.Attributes.ExternalURL != nil {
			externalURL = *body.Data.Attributes.ExternalURL
		}
		created, err := service.Create(c.Request().Context(), CreateInput{CourseID: c.Param("course_id"),
			ActorID: actorID, Scope: body.Data.Attributes.Scope, Name: body.Data.Attributes.Name,
			Kind: body.Data.Attributes.Kind, Format: body.Data.Attributes.Format, ExternalURL: externalURL, Brief: brief})
		if err != nil {
			return mutationError(c, service, actorID, err)
		}
		return materialResponse(c, http.StatusCreated, created)
	}
}

func listHandler(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := actor(c)
		if err != nil {
			return err
		}
		limit, offset, err := pagination(c)
		if err != nil {
			return httpserver.NewError(httpserver.CodeMaterialInvalid)
		}
		result, err := service.List(c.Request().Context(), c.Param("course_id"), actorID,
			ListInput{Limit: limit, Offset: offset})
		if err != nil {
			return materialError(err)
		}
		data := make([]map[string]any, len(result.Materials))
		for index, value := range result.Materials {
			data[index] = materialResource(value)
		}
		return jsonAPI(c, http.StatusOK, map[string]any{"data": data, "meta": map[string]bool{"has_more": result.HasMore}})
	}
}

func getHandler(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := actor(c)
		if err != nil {
			return err
		}
		value, err := service.Get(c.Request().Context(), c.Param("id"), actorID)
		if err != nil {
			return materialError(err)
		}
		return materialResponse(c, http.StatusOK, value)
	}
}

func updateHandler(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := actor(c)
		if err != nil {
			return err
		}
		var body request
		if err := decode(c, &body); err != nil || body.Data.Type != "materials" || body.Data.ID != "" &&
			body.Data.ID != c.Param("id") || body.Data.Attributes.Name != "" || body.Data.Attributes.Scope != "" ||
			body.Data.Attributes.Kind != "" || body.Data.Attributes.Format != "" || body.Data.Attributes.ExternalURL != nil ||
			len(body.Data.Attributes.Brief) == 0 {
			return httpserver.NewError(httpserver.CodeMaterialInvalid)
		}
		brief, err := decodeBrief(body.Data.Attributes.Brief)
		if err != nil {
			return httpserver.NewError(httpserver.CodeMaterialInvalid)
		}
		updated, err := service.CorrectBrief(c.Request().Context(), c.Param("id"), actorID, brief)
		if err != nil {
			return mutationError(c, service, actorID, err)
		}
		return materialResponse(c, http.StatusOK, updated)
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

func finalizeHandler(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := actor(c)
		if err != nil {
			return err
		}
		value, err := service.Finalize(c.Request().Context(), c.Param("id"), actorID)
		if err != nil {
			return mutationError(c, service, actorID, err)
		}
		return materialResponse(c, http.StatusOK, value)
	}
}

func approvalHandler(service *Service, approved bool) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := actor(c)
		if err != nil {
			return err
		}
		value, err := service.SetApproval(c.Request().Context(), c.Param("id"), actorID, approved)
		if err != nil {
			return mutationError(c, service, actorID, err)
		}
		return materialResponse(c, http.StatusOK, value)
	}
}

func filesHandler(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := actor(c)
		if err != nil {
			return err
		}
		files, err := service.Files(c.Request().Context(), c.Param("id"), actorID)
		if err != nil {
			return materialError(err)
		}
		data := make([]map[string]any, len(files))
		for index, file := range files {
			data[index] = fileResource(file)
		}
		return jsonAPI(c, http.StatusOK, map[string]any{"data": data})
	}
}

func uploadHandler(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := actor(c)
		if err != nil {
			return err
		}
		mediaType, parameters, err := mime.ParseMediaType(c.Request().Header.Get(echo.HeaderContentType))
		if err != nil || mediaType != "multipart/form-data" || parameters["boundary"] == "" {
			return httpserver.NewError(httpserver.CodeMaterialInvalid)
		}
		maximum := service.limits.MaxFileBytes + (1 << 20)
		c.Request().Body = http.MaxBytesReader(c.Response(), c.Request().Body, maximum)
		reader, err := c.Request().MultipartReader()
		if err != nil {
			return httpserver.NewError(httpserver.CodeMaterialInvalid)
		}
		part, err := reader.NextPart()
		if err != nil || part.FormName() != "file" || part.FileName() == "" {
			return httpserver.NewError(httpserver.CodeMaterialInvalid)
		}
		source, err := io.ReadAll(io.LimitReader(part, service.limits.MaxFileBytes+1))
		if closeErr := part.Close(); err == nil {
			err = closeErr
		}
		if err != nil || int64(len(source)) > service.limits.MaxFileBytes {
			return httpserver.NewError(httpserver.CodeMaterialInvalid)
		}
		if extra, err := reader.NextPart(); err != io.EOF || extra != nil {
			if extra != nil {
				if closeErr := extra.Close(); closeErr != nil {
					return httpserver.NewError(httpserver.CodeMaterialInvalid)
				}
			}
			return httpserver.NewError(httpserver.CodeMaterialInvalid)
		}
		file, err := service.Upload(c.Request().Context(), c.Param("id"), actorID, part.FileName(), source)
		if err != nil {
			return mutationError(c, service, actorID, err)
		}
		return jsonAPI(c, http.StatusCreated, map[string]any{"data": fileResource(file)})
	}
}

func fileHandler(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := actor(c)
		if err != nil {
			return err
		}
		file, err := service.GetFile(c.Request().Context(), c.Param("id"), actorID)
		if err != nil {
			return materialError(err)
		}
		return jsonAPI(c, http.StatusOK, map[string]any{"data": fileResource(file)})
	}
}

func deleteFileHandler(service *Service) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := actor(c)
		if err != nil {
			return err
		}
		if err := service.DeleteFile(c.Request().Context(), c.Param("id"), actorID); err != nil {
			return mutationError(c, service, actorID, err)
		}
		return c.NoContent(http.StatusNoContent)
	}
}

func downloadHandler(service *Service, content bool) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := actor(c)
		if err != nil {
			return err
		}
		opened, file, err := service.OpenFile(c.Request().Context(), c.Param("id"), actorID, content)
		if err != nil {
			return materialError(err)
		}
		defer func() {
			if closeErr := opened.Close(); closeErr != nil {
				service.logger.ErrorContext(c.Request().Context(), "close material download", "error", closeErr)
			}
		}()
		header := c.Response().Header()
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("Cache-Control", "private, no-store")
		if content {
			header.Set("Content-Disposition", `attachment; filename="content.jsonl"`)
			return c.Stream(http.StatusOK, "application/x-ndjson", opened)
		}
		header.Set("Content-Disposition", contentDisposition(file.OriginalFilename))
		return c.Stream(http.StatusOK, file.MediaType, opened)
	}
}

func materialJobsHandler(service *Service, oversight *jobs.Oversight) echo.HandlerFunc {
	return func(c *echo.Context) error {
		actorID, err := actor(c)
		if err != nil {
			return err
		}
		if err := service.RequireAssignedSupervisor(c.Request().Context(), c.Param("id"), actorID); err != nil {
			return materialError(err)
		}
		limit, offset, err := pagination(c)
		if err != nil {
			return httpserver.NewError(httpserver.CodeJobInvalid)
		}
		files, err := service.Files(c.Request().Context(), c.Param("id"), actorID)
		if err != nil {
			return materialError(err)
		}
		subjects := []string{c.Param("id")}
		for _, file := range files {
			subjects = append(subjects, file.ID)
		}
		records, err := oversight.ListSubjectIDs(c.Request().Context(), subjects, limit, offset)
		if err != nil {
			return httpserver.NewError(httpserver.CodeJobInvalid)
		}
		data := make([]map[string]any, len(records.Items))
		for index, record := range records.Items {
			data[index] = safeJobResource(record)
		}
		return jsonAPI(c, http.StatusOK, map[string]any{"data": data,
			"meta": map[string]bool{"has_more": records.HasMore}})
	}
}

func decode(c *echo.Context, destination any) error {
	mediaType, parameters, err := mime.ParseMediaType(c.Request().Header.Get(echo.HeaderContentType))
	if err != nil || mediaType != "application/vnd.api+json" || len(parameters) != 0 || c.Request().ContentLength > 1<<20 {
		return ErrInvalid
	}
	c.Request().Body = http.MaxBytesReader(c.Response(), c.Request().Body, 1<<20)
	body, err := io.ReadAll(c.Request().Body)
	if err != nil || !utf8.Valid(body) {
		return ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return ErrInvalid
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ErrInvalid
	}
	return nil
}

func pagination(c *echo.Context) (int, int, error) {
	limit, offset := 25, 0
	for key, values := range c.QueryParams() {
		if key != "page[limit]" && key != "page[offset]" || len(values) != 1 {
			return 0, 0, ErrInvalid
		}
		value, err := strconv.Atoi(values[0])
		if err != nil {
			return 0, 0, ErrInvalid
		}
		if key == "page[limit]" {
			limit = value
		} else {
			offset = value
		}
	}
	if limit < 1 || limit > 100 || offset < 0 || offset > 10_000 {
		return 0, 0, ErrInvalid
	}
	return limit, offset, nil
}

func materialResource(value Material) map[string]any {
	attributes := map[string]any{"name": value.Name, "scope": value.Scope, "kind": value.Kind,
		"format": value.Format, "state": value.State, "external_url": nullable(value.ExternalURL),
		"brief": value.Brief, "brief_source": nullable(value.BriefSource), "brief_updated_at": formatTime(value.BriefUpdatedAt),
		"approved": value.Approved, "approved_at": formatTime(value.ApprovedAt), "failure_code": nullable(value.FailureCode),
		"created_at": instant(value.CreatedAt), "updated_at": formatTime(value.UpdatedAt)}
	return map[string]any{"type": "materials", "id": value.ID, "attributes": attributes,
		"relationships": map[string]any{"course": map[string]any{"data": map[string]string{
			"type": "courses", "id": value.CourseID}}}}
}

func fileResource(file File) map[string]any {
	return map[string]any{"type": "material-files", "id": file.ID,
		"attributes": map[string]any{"original_filename": file.OriginalFilename, "media_type": file.MediaType,
			"size_bytes": file.SizeBytes, "page_count": file.PageCount, "state": file.State,
			"failure_code": nullable(file.FailureCode), "created_at": instant(file.CreatedAt),
			"download_url": "/api/v1/material-files/" + file.ID + "/download",
			"content_url":  "/api/v1/material-files/" + file.ID + "/content"},
		"relationships": map[string]any{"material": map[string]any{"data": map[string]string{
			"type": "materials", "id": file.MaterialID}}}}
}

func safeJobResource(record jobs.Record) map[string]any {
	return map[string]any{"type": "jobs", "id": record.ID, "attributes": map[string]any{
		"job_type": record.Type, "subject_type": record.SubjectType, "subject_id": record.SubjectID,
		"state": record.State, "attempt_count": record.AttemptCount, "created_at": instant(record.CreatedAt),
		"started_at": formatTime(record.StartedAt), "finished_at": formatTime(record.FinishedAt)}}
}

func materialResponse(c *echo.Context, status int, material Material) error {
	return jsonAPI(c, status, map[string]any{"data": materialResource(material)})
}

func jsonAPI(c *echo.Context, status int, body any) error {
	c.Response().Header().Set(echo.HeaderContentType, "application/vnd.api+json")
	return c.JSON(status, body)
}

func actor(c *echo.Context) (string, error) {
	id, ok := c.Get("mia.auth.user_id").(string)
	if !ok || id == "" {
		return "", httpserver.NewError(httpserver.CodeUnauthenticated)
	}
	return id, nil
}

func materialError(err error) error {
	switch {
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrFileNotFound):
		return httpserver.NewError(httpserver.CodeMaterialNotFound)
	case errors.Is(err, ErrInvalid):
		return httpserver.NewError(httpserver.CodeMaterialInvalid)
	case errors.Is(err, ErrNameTaken):
		return httpserver.NewError(httpserver.CodeMaterialNameTaken)
	case errors.Is(err, ErrInvalidState), errors.Is(err, ErrFileSelected):
		return httpserver.NewError(httpserver.CodeMaterialInvalidState)
	default:
		return err
	}
}

func mutationError(c *echo.Context, service *Service, actorID string, err error) error {
	var outcome string
	switch {
	case errors.Is(err, ErrNotFound), errors.Is(err, ErrFileNotFound):
		outcome = "material_not_found"
	case errors.Is(err, ErrInvalid):
		outcome = "material_invalid"
	case errors.Is(err, ErrNameTaken):
		outcome = "material_name_taken"
	case errors.Is(err, ErrInvalidState), errors.Is(err, ErrFileSelected):
		outcome = "material_invalid_state"
	default:
		return err
	}
	service.AuditMutationDenied(c.Request().Context(), actorID, outcome)
	return materialError(err)
}

func contentDisposition(filename string) string {
	filename = strings.Map(func(char rune) rune {
		if char < 0x20 || char == 0x7f || char == '"' || char == '\\' {
			return '_'
		}
		return char
	}, filename)
	return mime.FormatMediaType("attachment", map[string]string{"filename": filename})
}

func formatTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return instant(*value)
}
