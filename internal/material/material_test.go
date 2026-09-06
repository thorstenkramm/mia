package material

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/thorstenkramm/mia/internal/course"
	"github.com/thorstenkramm/mia/internal/jobs"
	"github.com/thorstenkramm/mia/internal/lifecycle"
	"github.com/thorstenkramm/mia/internal/provider/mistral"
	"github.com/thorstenkramm/mia/internal/provider/openai"
	miSQLite "github.com/thorstenkramm/mia/internal/sqlite"
	"github.com/thorstenkramm/mia/internal/user"
)

type summaryFake struct{ brief Brief }

func (fake summaryFake) Structured(context.Context, string, []string, map[string]any) (openai.Result, error) {
	encoded, err := json.Marshal(fake.brief)
	return openai.Result{JSON: encoded, InputTokens: 10, OutputTokens: 5}, err
}

type ocrFake struct{ result mistral.Result }

func (fake ocrFake) OCR(context.Context, string, []byte) (mistral.Result, error) {
	return fake.result, nil
}

func TestLinkBriefApprovalCorrectionAndVisibility(t *testing.T) {
	service, database, _, supervisor, student, courseID := materialFixture(t, nil)
	brief := validBrief("Grounded summary")
	created, err := service.Create(context.Background(), CreateInput{CourseID: courseID, ActorID: supervisor,
		Scope: "course-wide", Name: "Reference video", Kind: "youtube", Format: "link",
		ExternalURL: "https://www.youtube.com/watch?v=abc", Brief: &brief})
	if err != nil {
		t.Fatal(err)
	}
	ready, err := service.Finalize(context.Background(), created.ID, supervisor)
	if err != nil || ready.State != "ready" {
		t.Fatalf("Finalize link = %#v, %v", ready, err)
	}
	approved, err := service.SetApproval(context.Background(), created.ID, supervisor, true)
	if err != nil || !approved.Approved {
		t.Fatalf("SetApproval = %#v, %v", approved, err)
	}
	if _, err := service.Get(context.Background(), created.ID, student); err != nil {
		t.Fatalf("student cannot see approved link: %v", err)
	}
	unchanged, err := service.CorrectBrief(context.Background(), created.ID, supervisor, brief)
	if err != nil || !unchanged.Approved {
		t.Fatalf("unchanged correction revoked approval: %#v, %v", unchanged, err)
	}
	brief.Summary = "Corrected grounded summary"
	corrected, err := service.CorrectBrief(context.Background(), created.ID, supervisor, brief)
	if err != nil || corrected.Approved || corrected.BriefSource != "supervisor" {
		t.Fatalf("changed correction = %#v, %v", corrected, err)
	}
	if _, err := service.Get(context.Background(), created.ID, student); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked material student error = %v", err)
	}
	for _, action := range []string{"material.brief.corrected", "material.approval.revoked"} {
		var count int
		if err := database.QueryRow("SELECT COUNT(*) FROM audit_events WHERE action = ? AND course_id = ?", action, courseID).
			Scan(&count); err != nil || count != 1 {
			t.Fatalf("audit action %s count = %d, %v", action, count, err)
		}
	}
	var jobsCount int
	if err := database.QueryRow("SELECT COUNT(*) FROM jobs WHERE subject_id = ?", created.ID).Scan(&jobsCount); err != nil || jobsCount != 0 {
		t.Fatalf("link jobs = %d, %v", jobsCount, err)
	}
}

func TestUploadRejectsUnsafeFilenames(t *testing.T) {
	service, _, _, supervisor, _, courseID := materialFixture(t, nil)
	created, err := service.Create(context.Background(), CreateInput{CourseID: courseID, ActorID: supervisor,
		Scope: "course-wide", Name: "Filename validation", Kind: "worksheet", Format: "text"})
	if err != nil {
		t.Fatal(err)
	}
	tests := []string{
		"not-nfc-e\u0301.txt",
		"invalid-\xff.txt",
		"control-\u0001.txt",
		"misleading-\u202ereadme.txt",
		strings.Repeat("a", 256) + ".txt",
		strings.Repeat("界", 342) + ".txt",
	}
	for _, filename := range tests {
		if _, err := service.Upload(context.Background(), created.ID, supervisor, filename, []byte("safe")); !errors.Is(err, ErrInvalid) {
			t.Errorf("Upload filename %q error = %v", filename, err)
		}
	}
}

func TestCreateRejectsInvalidLinkKindFormatAndScopeCombinations(t *testing.T) {
	service, _, _, supervisor, student, courseID := materialFixture(t, nil)
	brief := validBrief("Grounded summary")
	tests := []struct {
		name  string
		input CreateInput
	}{
		{
			name: "website with file-backed format",
			input: CreateInput{CourseID: courseID, ActorID: supervisor, Scope: "course-wide", Name: "Website PDF",
				Kind: "website", Format: "pdf"},
		},
		{
			name: "youtube with file-backed format",
			input: CreateInput{CourseID: courseID, ActorID: supervisor, Scope: "course-wide", Name: "Video notes",
				Kind: "youtube", Format: "text"},
		},
		{
			name: "student-private website link",
			input: CreateInput{CourseID: courseID, ActorID: student, Scope: "student-private", Name: "Private website",
				Kind: "website", Format: "link", ExternalURL: "https://example.com", Brief: &brief},
		},
		{
			name: "file-backed kind with link format",
			input: CreateInput{CourseID: courseID, ActorID: supervisor, Scope: "course-wide", Name: "Linked worksheet",
				Kind: "worksheet", Format: "link", ExternalURL: "https://example.com", Brief: &brief},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := service.Create(context.Background(), test.input); !errors.Is(err, ErrInvalid) {
				t.Fatalf("Create error = %v", err)
			}
		})
	}
}

func TestTextUploadProcessingFailureAtomicReadinessAndAuthorization(t *testing.T) {
	brief := validBrief("Generated source-grounded summary")
	service, database, dataDir, supervisor, student, courseID := materialFixture(t, summaryFake{brief: brief})
	created, err := service.Create(context.Background(), CreateInput{CourseID: courseID, ActorID: supervisor,
		Scope: "course-wide", Name: "Class notes", Kind: "worksheet", Format: "text"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Upload(context.Background(), created.ID, student, "bad.txt", []byte{0}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("out-of-scope invalid upload error = %v", err)
	}
	file, err := service.Upload(context.Background(), created.ID, supervisor, "notes.txt",
		[]byte("First grounded section.\n\nSecond grounded section."))
	if err != nil {
		t.Fatal(err)
	}
	worker := jobs.New(database, nil)
	worker.Register("material-extraction", NewExtractionHandler(service))
	worker.Register("material-summary", NewSummaryHandler(service))
	ctx, cancel := context.WithCancel(context.Background())
	worker.Start(ctx)
	if _, err := service.Finalize(ctx, created.ID, supervisor); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		current, getErr := service.Get(ctx, created.ID, supervisor)
		if getErr == nil && current.State == "ready" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
	defer stopCancel()
	if err := worker.Stop(stopCtx); err != nil {
		t.Fatal(err)
	}
	processed, err := service.Get(context.Background(), created.ID, supervisor)
	if err != nil || processed.State != "ready" || processed.Brief == nil {
		t.Fatalf("processed material = %#v, %v", processed, err)
	}
	contentFile, _, err := service.OpenFile(context.Background(), file.ID, supervisor, true)
	if err != nil {
		t.Fatal(err)
	}
	content, err := io.ReadAll(contentFile)
	if closeErr := contentFile.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	segments, err := decodeSegments(content)
	if err != nil || len(segments) != 2 {
		t.Fatalf("content segments = %#v, %v", segments, err)
	}
	ready, err := MaterialReady(context.Background(), database, courseID)
	if err != nil || ready {
		t.Fatalf("unapproved readiness = %v, %v", ready, err)
	}
	if _, err := service.SetApproval(context.Background(), created.ID, supervisor, true); err != nil {
		t.Fatal(err)
	}
	ready, err = MaterialReady(context.Background(), database, courseID)
	if err != nil || !ready {
		t.Fatalf("approved readiness = %v, %v", ready, err)
	}
	studentContent, _, err := service.OpenFile(context.Background(), file.ID, student, true)
	if err != nil {
		t.Fatalf("student approved content: %v", err)
	}
	if err := studentContent.Close(); err != nil {
		t.Fatal(err)
	}
	contentPath := serviceContentPath(t, service, created.ID, file.ID)
	if err := os.WriteFile(contentPath, []byte("malformed content is streamed without whole-file decoding\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/material-files/"+file.ID+"/content", nil)
	web := echo.New()
	requestContext := web.NewContext(request, recorder)
	requestContext.Set("mia.auth.user_id", student)
	requestContext.SetPathValues(echo.PathValues{{Name: "id", Value: file.ID}})
	if err := downloadHandler(service, true)(requestContext); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != "application/x-ndjson" ||
		recorder.Header().Get("X-Content-Type-Options") != "nosniff" ||
		!strings.HasPrefix(recorder.Header().Get("Content-Disposition"), "attachment;") {
		t.Fatalf("content response status=%d headers=%v", recorder.Code, recorder.Header())
	}
	if recorder.Body.String() != "malformed content is streamed without whole-file decoding\n" {
		t.Fatalf("streamed content = %q", recorder.Body.String())
	}
	if err := os.Remove(contentPath); err != nil {
		t.Fatal(err)
	}
	if err := service.Reconcile(context.Background()); err == nil {
		t.Fatal("Reconcile accepted missing database-referenced content")
	}
	_ = dataDir
}

func TestTerminalExtractionFailureFaultsWholeMaterial(t *testing.T) {
	service, database, _, supervisor, _, courseID := materialFixture(t, nil)
	created, err := service.Create(context.Background(), CreateInput{CourseID: courseID, ActorID: supervisor,
		Scope: "course-wide", Name: "Two files", Kind: "worksheet", Format: "text"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.Upload(context.Background(), created.ID, supervisor, "one.txt", []byte("one"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Upload(context.Background(), created.ID, supervisor, "two.txt", []byte("two")); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Finalize(context.Background(), created.ID, supervisor); err != nil {
		t.Fatal(err)
	}
	var jobID string
	if err := database.QueryRow("SELECT id FROM jobs WHERE subject_id = ?", first.ID).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	job := jobs.Job{ID: jobID, Type: "material-extraction", SubjectID: first.ID, CourseID: courseID}
	if err := miSQLite.WithTx(context.Background(), database, func(tx *sql.Tx) error {
		return NewExtractionHandler(service).TerminalFailure(context.Background(), tx, job, "invalid_source")
	}); err != nil {
		t.Fatal(err)
	}
	failed, err := service.Get(context.Background(), created.ID, supervisor)
	if err != nil || failed.State != "failed" {
		t.Fatalf("failed material = %#v, %v", failed, err)
	}
	var queued int
	if err := database.QueryRow(`SELECT COUNT(*) FROM jobs WHERE subject_id != ? AND state IN ('queued','running')`,
		first.ID).Scan(&queued); err != nil || queued != 0 {
		t.Fatalf("remaining active jobs = %d, %v", queued, err)
	}
}

func TestPostProviderValidationFailuresRetainUsage(t *testing.T) {
	service, _, _, supervisor, _, courseID := materialFixture(t, summaryFake{brief: Brief{}})
	service.ocr = ocrFake{result: mistral.Result{Pages: []mistral.Page{{Index: 0}}, InputUnits: 7}}
	created, err := service.Create(context.Background(), CreateInput{CourseID: courseID, ActorID: supervisor,
		Scope: "course-wide", Name: "Scanned notes", Kind: "worksheet", Format: "png"})
	if err != nil {
		t.Fatal(err)
	}
	var source bytes.Buffer
	imageData := image.NewRGBA(image.Rect(0, 0, 1, 1))
	imageData.Set(0, 0, color.White)
	if err := png.Encode(&source, imageData); err != nil {
		t.Fatal(err)
	}
	file, err := service.Upload(context.Background(), created.ID, supervisor, "notes.png", source.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Finalize(context.Background(), created.ID, supervisor); err != nil {
		t.Fatal(err)
	}
	_, err = NewExtractionHandler(service).Execute(context.Background(), jobs.Job{SubjectID: file.ID})
	var extractionFailure *jobs.Failure
	if !errors.As(err, &extractionFailure) || extractionFailure.Code != "material_ocr_invalid" ||
		extractionFailure.ProviderInputUnits != 7 {
		t.Fatalf("OCR validation failure = %#v, %v", extractionFailure, err)
	}
	_, input, output, err := NewSummaryHandler(service).summarize(context.Background(), "instructions", []string{"source"})
	var summaryFailure *jobs.Failure
	if !errors.As(err, &summaryFailure) || summaryFailure.Code != "material_brief_invalid" || input != 10 || output != 5 ||
		summaryFailure.ProviderInputUnits != 10 || summaryFailure.ProviderOutputUnits != 5 {
		t.Fatalf("summary validation failure usage=%d/%d failure=%#v error=%v", input, output, summaryFailure, err)
	}
}

func TestAccountDeletionRemovesOwnedMaterialJobSubjects(t *testing.T) {
	service, database, dataDir, _, student, courseID := materialFixture(t, nil)
	created, err := service.Create(context.Background(), CreateInput{CourseID: courseID, ActorID: student,
		Scope: "student-private", Name: "Deletion source", Kind: "worksheet", Format: "text"})
	if err != nil {
		t.Fatal(err)
	}
	file, err := service.Upload(context.Background(), created.ID, student, "source.txt", []byte("source"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Finalize(context.Background(), created.ID, student); err != nil {
		t.Fatal(err)
	}
	// Exercise subject-owned cleanup independently of the jobs.owner_user_id FK.
	if _, err := database.Exec("UPDATE jobs SET owner_user_id = NULL WHERE subject_id = ?", file.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := jobs.Enqueue(context.Background(), database, "material-summary", "material", created.ID,
		courseID, ""); err != nil {
		t.Fatal(err)
	}
	var administrator string
	if err := database.QueryRow("SELECT id FROM users WHERE username_key = 'admin'").Scan(&administrator); err != nil {
		t.Fatal(err)
	}
	registry := &lifecycle.Registry{}
	registry.RegisterAccount(service)
	deletion := user.NewDeletionService(database, dataDir, registry, nil)
	if err := deletion.Delete(context.Background(), administrator, student); err != nil {
		t.Fatal(err)
	}
	var materialCount, jobCount int
	if err := database.QueryRow("SELECT COUNT(*) FROM materials WHERE id = ?", created.ID).Scan(&materialCount); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow("SELECT COUNT(*) FROM jobs WHERE subject_id IN (?, ?)", created.ID, file.ID).
		Scan(&jobCount); err != nil {
		t.Fatal(err)
	}
	if materialCount != 0 || jobCount != 0 {
		t.Fatalf("account deletion retained material/jobs = %d/%d", materialCount, jobCount)
	}
}

func materialFixture(t *testing.T, summary SummaryClient) (*Service, *sql.DB, string, string, string, string) {
	t.Helper()
	dataDir := t.TempDir()
	if err := os.Chmod(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	database, err := miSQLite.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	create := func(username string, roles ...user.Role) string {
		var id string
		err := miSQLite.WithTx(context.Background(), database, func(tx *sql.Tx) error {
			account, err := user.Create(context.Background(), tx, user.CreateInput{Username: username,
				Email: username + "@example.com", EmailVerified: true, PasswordHash: "hash", Language: "en",
				Country: "US", TimeZone: "UTC", Roles: roles})
			id = account.ID
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	admin := create("admin", user.Administrator)
	supervisor := create("supervisor", user.Supervisor, user.Student)
	student := create("student", user.Student)
	courseService := course.NewService(database, dataDir, nil, func(context.Context, miSQLite.Querier, string) (bool, error) {
		return true, nil
	}, nil, nil, nil, nil)
	name := "Material course"
	goals, instructions, language := "goals", "instructions", "en"
	created, err := courseService.Create(context.Background(), course.CreateInput{ActorID: admin,
		SupervisorIDs: []string{supervisor}, Fields: course.Fields{Name: course.OptionalString{Set: true, Value: &name},
			LearningGoals: course.OptionalString{Set: true, Value: &goals},
			Instructions:  course.OptionalString{Set: true, Value: &instructions},
			Language:      course.OptionalString{Set: true, Value: &language}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO course_students(id, course_id, student_user_id, joined_at, added_by)
		VALUES (?, ?, ?, ?, ?)`, "cst_test", created.ID, student, instant(time.Now()), supervisor); err != nil {
		t.Fatal(err)
	}
	service := NewService(database, dataDir, Limits{MaxFileBytes: 1 << 20, MaxMaterialBytes: 2 << 20,
		MaxPages: 10, MaxFiles: 10, MaxImageMegapixels: 10}, nil, summary, nil, nil)
	if err := EnsureInstructions(dataDir); err != nil {
		t.Fatal(err)
	}
	return service, database, dataDir, supervisor, student, created.ID
}

func validBrief(summary string) Brief {
	return Brief{Version: 1, Summary: summary, Subjects: []string{"Subject"}, LearningGoals: []string{},
		Sections: []Section{{Sequence: 1, Title: "Section", Description: "Grounded description",
			SearchTerms: []string{"grounded"}}}, Warnings: []string{}}
}

func serviceContentPath(t *testing.T, service *Service, materialID, fileID string) string {
	t.Helper()
	path, err := service.contentPath(materialID, fileID)
	if err != nil {
		t.Fatal(err)
	}
	return path
}
