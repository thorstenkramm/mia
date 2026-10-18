package material

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/thorstenkramm/mia/internal/jobs"
	"github.com/thorstenkramm/mia/internal/provider/mistral"
)

const nonconformingInfoDates = "/CreationDate (D:20250809135053Z) /ModDate (D:20250809135053Z)"

// syntheticPDF builds a minimal, non-copyrighted PDF with blank pages, the given document-information entries, and
// an optional comment of padding bytes. Cross-reference offsets are computed from the written bytes.
func syntheticPDF(pages int, info string, padding int) []byte {
	var objects []string
	objects = append(objects, "<< /Type /Catalog /Pages 2 0 R >>")
	kids := make([]string, 0, pages)
	for page := range pages {
		kids = append(kids, fmt.Sprintf("%d 0 R", page+3))
	}
	objects = append(objects, fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), pages))
	for range pages {
		objects = append(objects, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << >> >>")
	}
	objects = append(objects, fmt.Sprintf("<< /Producer (MIA synthetic test) %s >>", info))
	var output bytes.Buffer
	output.WriteString("%PDF-1.4\n")
	if padding > 0 {
		output.WriteString("%" + strings.Repeat("x", padding) + "\n")
	}
	offsets := make([]int, 0, len(objects))
	for index, object := range objects {
		offsets = append(offsets, output.Len())
		fmt.Fprintf(&output, "%d 0 obj\n%s\nendobj\n", index+1, object)
	}
	xref := output.Len()
	fmt.Fprintf(&output, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, offset := range offsets {
		fmt.Fprintf(&output, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&output, "trailer\n<< /Size %d /Root 1 0 R /Info %d 0 R >>\nstartxref\n%d\n%%%%EOF\n",
		len(objects)+1, len(objects), xref)
	return output.Bytes()
}

// pageOCRFake returns one Markdown page per page of the received PDF chunk.
type pageOCRFake struct{}

func (pageOCRFake) OCR(_ context.Context, mediaType string, source []byte) (mistral.Result, error) {
	if mediaType != "application/pdf" {
		return mistral.Result{}, fmt.Errorf("unexpected media type %q", mediaType)
	}
	pages, err := api.PageCount(bytes.NewReader(source), pdfConfiguration())
	if err != nil {
		return mistral.Result{}, err
	}
	result := mistral.Result{InputUnits: int64(pages)}
	for index := range pages {
		result.Pages = append(result.Pages, mistral.Page{Index: index, Markdown: fmt.Sprintf("Page %d text", index+1)})
	}
	return result, nil
}

func TestPDFWithNonconformingInfoDatesUploadsAndProcesses(t *testing.T) {
	source := syntheticPDF(2, nonconformingInfoDates, 0)
	strict := pdfConfiguration()
	strict.ValidationMode = model.ValidationStrict
	if err := api.Validate(bytes.NewReader(source), strict); err == nil {
		t.Fatal("fixture does not exercise strict metadata validation")
	}
	service, database, _, supervisor, _, courseID := materialFixture(t,
		summaryFake{brief: validBrief("Generated source-grounded summary")})
	service.ocr = pageOCRFake{}
	created, err := service.Create(context.Background(), CreateInput{CourseID: courseID, ActorID: supervisor,
		Scope: "course-wide", Name: "Scanned book", Kind: "text-book", Format: "pdf"})
	if err != nil {
		t.Fatal(err)
	}
	file, err := service.Upload(context.Background(), created.ID, supervisor, "book.pdf", source)
	if err != nil || file.PageCount != 2 || file.MediaType != "application/pdf" {
		t.Fatalf("Upload = %#v, %v", file, err)
	}
	worker := jobs.New(database, nil)
	worker.Register("material-extraction", NewExtractionHandler(service))
	worker.Register("material-summary", NewSummaryHandler(service))
	ctx, cancel := context.WithCancel(context.Background())
	worker.Start(ctx)
	if _, err := service.Finalize(ctx, created.ID, supervisor); err != nil {
		cancel()
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		current, getErr := service.Get(ctx, created.ID, supervisor)
		if getErr == nil && (current.State == "ready" || current.State == "failed") {
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
	if err != nil || processed.State != "ready" {
		t.Fatalf("processed material = %#v, %v", processed, err)
	}
	content, err := os.ReadFile(serviceContentPath(t, service, created.ID, file.ID))
	if err != nil {
		t.Fatal(err)
	}
	segments, err := decodeSegments(content)
	if err != nil || len(segments) != 2 || segments[0].Text != "Page 1 text" || segments[1].Text != "Page 2 text" {
		t.Fatalf("content segments = %#v, %v", segments, err)
	}
}

func TestPDFUploadRejections(t *testing.T) {
	valid := syntheticPDF(1, "", 0)
	corruptXref := bytes.Replace(valid, []byte("xref\n0 "), []byte("xref\nx "), 1)
	if bytes.Equal(corruptXref, valid) {
		t.Fatal("corrupt xref fixture was not modified")
	}
	tests := []struct {
		name   string
		source []byte
	}{
		{name: "unparseable", source: []byte("%PDF-1.4\nnot a document at all\n")},
		{name: "corrupt xref subsection header", source: corruptXref},
		{name: "zero pages", source: syntheticPDF(0, "", 0)},
		{name: "file size above limit", source: syntheticPDF(1, "", 1<<20)},
		{name: "pages above limit", source: syntheticPDF(11, "", 0)},
		{name: "missing signature", source: append([]byte("\n"), valid...)},
	}
	for _, marker := range []string{"/Encrypt", "/EmbeddedFile", "/JavaScript", "/JS ", "/Launch"} {
		tests = append(tests, struct {
			name   string
			source []byte
		}{name: "hazard " + marker, source: syntheticPDF(1, "/MIAProbe ["+marker+" ]", 0)})
	}
	service, _, _, supervisor, _, courseID := materialFixture(t, nil)
	created, err := service.Create(context.Background(), CreateInput{CourseID: courseID, ActorID: supervisor,
		Scope: "course-wide", Name: "Rejected PDFs", Kind: "worksheet", Format: "pdf"})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := service.Upload(context.Background(), created.ID, supervisor, "source.pdf",
				test.source); !errors.Is(err, ErrInvalid) {
				t.Fatalf("Upload error = %v", err)
			}
		})
	}
	for name, source := range map[string][]byte{"oversized": syntheticPDF(1, "", 1<<20),
		"too many pages": syntheticPDF(11, "", 0)} {
		if _, _, _, err := validateSource("pdf", source, Limits{MaxFileBytes: 2 << 20}); err != nil {
			t.Fatalf("%s fixture is not otherwise valid: %v", name, err)
		}
	}
	probe := syntheticPDF(1, "/MIAProbe [/Safe ]", 0)
	if file, err := service.Upload(context.Background(), created.ID, supervisor, "probe.pdf", probe); err != nil ||
		file.PageCount != 1 {
		t.Fatalf("hazard-free probe Upload = %#v, %v", file, err)
	}
}

func TestPDFValidationDoesNotUseUserConfigDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	if _, pages, _, err := validateSource("pdf", syntheticPDF(2, nonconformingInfoDates, 0),
		Limits{MaxFileBytes: 1 << 20}); err != nil || pages != 2 {
		t.Fatalf("validateSource pages = %d, %v", pages, err)
	}
	if model.ConfigPath != "disable" {
		t.Fatalf("pdfcpu configuration directory is enabled: %q", model.ConfigPath)
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("user configuration directory was written: %v", entries)
	}
}
