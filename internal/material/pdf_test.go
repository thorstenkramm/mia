package material

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
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

// syntheticPDF builds a minimal, non-copyrighted PDF with numbered pages, the given document-information entries, and
// an optional comment of padding bytes. Cross-reference offsets are computed from the written bytes.
func syntheticPDF(pages int, info string, padding int) []byte {
	return syntheticPDFCatalog(pages, info, padding, "")
}

func syntheticPDFCatalog(pages int, info string, padding int, catalog string, extraObjects ...string) []byte {
	var objects []string
	objects = append(objects, "<< /Type /Catalog /Pages 2 0 R "+catalog+" >>")
	kids := make([]string, 0, pages)
	for page := range pages {
		kids = append(kids, fmt.Sprintf("%d 0 R", page+3))
	}
	objects = append(objects, fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), pages))
	for page := range pages {
		objects = append(objects, fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] "+
			"/Resources << /Font << /F1 << /Type /Font /Subtype /Type1 /BaseFont /Helvetica >> >> >> "+
			"/Contents %d 0 R >>", pages+page+3))
	}
	for page := range pages {
		content := fmt.Sprintf("BT /F1 12 Tf 72 720 Td (Page %d text) Tj ET\n", page+1)
		objects = append(objects, fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(content), content))
	}
	objects = append(objects, extraObjects...)
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
type pageOCRFake struct {
	chunks []int
	calls  int
}

func (fake *pageOCRFake) OCR(_ context.Context, mediaType string, source []byte) (mistral.Result, error) {
	fake.calls++
	if mediaType != "application/pdf" {
		return mistral.Result{}, fmt.Errorf("unexpected media type %q", mediaType)
	}
	document, err := api.ReadAndValidate(bytes.NewReader(source), pdfConfiguration())
	if err != nil {
		return mistral.Result{}, err
	}
	pages := document.PageCount
	fake.chunks = append(fake.chunks, pages)
	if pages < 1 || pages > 25 {
		return mistral.Result{}, fmt.Errorf("unexpected chunk size %d", pages)
	}
	catalog, err := document.Catalog()
	if err != nil {
		return mistral.Result{}, err
	}
	if catalog["Metadata"] != nil || document.Title != "" || document.Author != "" {
		return mistral.Result{}, errors.New("source document metadata forwarded")
	}
	result := mistral.Result{InputUnits: int64(pages)}
	for index := range pages {
		page, _, _, err := document.PageDict(index+1, false)
		if err != nil {
			return mistral.Result{}, err
		}
		content, err := document.PageContent(page, index+1)
		if err != nil {
			return mistral.Result{}, err
		}
		_, text, found := strings.Cut(string(content), "(Page ")
		text, _, closed := strings.Cut(text, ") Tj")
		if !found || !closed {
			return mistral.Result{}, errors.New("page content missing")
		}
		result.Pages = append(result.Pages, mistral.Page{Index: index, Markdown: "Page " + text})
	}
	return result, nil
}

func TestPDFChunksWithNamedDestinationsPreserveContent(t *testing.T) {
	// Separate destination leaves exercise pdfcpu's removal of unused destination object graphs.
	// Reusing the first chunk's context loses source objects needed by the second chunk.
	source := syntheticPDFCatalog(27, "/Title 123", 0,
		"/Names << /Dests << /Kids [57 0 R 58 0 R] >> >>",
		"<< /Limits [(first) (first)] /Names [(first) [3 0 R /Fit]] >>",
		"<< /Limits [(last) (last)] /Names [(last) [29 0 R /Fit]] >>")
	ocr := &pageOCRFake{}
	handler := NewExtractionHandler(&Service{ocr: ocr})
	segments, usage, err := handler.ocrPDF(context.Background(), source, 27)
	if err != nil {
		t.Fatal(err)
	}
	if usage != 27 || len(segments) != 27 || len(ocr.chunks) != 2 || ocr.chunks[0] != 25 || ocr.chunks[1] != 2 {
		t.Fatalf("chunks=%v, segments=%d, usage=%d", ocr.chunks, len(segments), usage)
	}
	for index, segment := range segments {
		if segment.Sequence != index+1 || segment.Text != fmt.Sprintf("Page %d text", index+1) {
			t.Fatalf("page %d = %#v", index+1, segment)
		}
	}
}

func TestPDFExtractionRejectsPersistedPageCountMismatch(t *testing.T) {
	service, database, _, supervisor, _, courseID := materialFixture(t, nil)
	ocr := &pageOCRFake{}
	service.ocr = ocr
	ctx := context.Background()
	created, err := service.Create(ctx, CreateInput{CourseID: courseID, ActorID: supervisor,
		Scope: "course-wide", Name: "Mismatched count", Kind: "worksheet", Format: "pdf"})
	if err != nil {
		t.Fatal(err)
	}
	file, err := service.Upload(ctx, created.ID, supervisor, "source.pdf", syntheticPDF(2, "/Title 123", 0))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Finalize(ctx, created.ID, supervisor); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, "UPDATE material_files SET page_count = 1 WHERE id = ?", file.ID); err != nil {
		t.Fatal(err)
	}
	result, err := NewExtractionHandler(service).Execute(ctx, jobs.Job{SubjectID: file.ID})
	var failure *jobs.Failure
	if !errors.As(err, &failure) || failure.Code != "material_ocr_invalid" ||
		failure.ProviderInputUnits != 0 || failure.ProviderOutputUnits != 0 {
		t.Fatalf("extraction failure = %#v, %v", failure, err)
	}
	if ocr.calls != 0 || result.Value != nil || result.ProviderInputUnits != 0 || result.ProviderOutputUnits != 0 {
		t.Fatalf("unexpected OCR calls=%d or partial result=%#v", ocr.calls, result)
	}
	if _, err := os.Stat(serviceContentPath(t, service, created.ID, file.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial content exists or stat failed: %v", err)
	}
}

func TestOriginalPDFTrimDoesNotForwardInfo(t *testing.T) {
	source := syntheticPDF(2, "/Title (Source title) /Author (Source author)", 0)
	input, err := api.ReadAndValidate(bytes.NewReader(source), pdfConfiguration())
	if err != nil {
		t.Fatal(err)
	}
	if input.Title != "Source title" || input.Author != "Source author" || input.Producer != "MIA synthetic test" {
		t.Fatal("fixture metadata not recognized")
	}
	var chunk bytes.Buffer
	if err := api.Trim(bytes.NewReader(source), &chunk, []string{"1-2"}, pdfConfiguration()); err != nil {
		t.Fatal(err)
	}
	output, err := api.ReadAndValidate(bytes.NewReader(chunk.Bytes()), pdfConfiguration())
	if err != nil {
		t.Fatal(err)
	}
	if output.Title != "" || output.Author != "" || output.Producer == input.Producer ||
		bytes.Contains(chunk.Bytes(), []byte("Source title")) || bytes.Contains(chunk.Bytes(), []byte("Source author")) {
		t.Fatal("original Trim forwarded source Info metadata")
	}
}

func TestPDFWithNonconformingInfoDatesUploadsAndProcesses(t *testing.T) {
	tests := []struct {
		name, info, catalog string
		pages               int
	}{
		{name: "dates", info: nonconformingInfoDates, pages: 2},
		{name: "numeric title", info: "/Title 123 /Author (Private source author)", pages: 2},
		{name: "invalid trapped", info: "/Trapped /Invalid", pages: 2},
		{name: "combined multi chunk", info: "/Title 123 /Trapped /Invalid /CreationDate (bad) /ModDate (bad)", pages: 27},
		{name: "invalid catalog metadata", catalog: "/Metadata 123", pages: 2},
		// The invalid XMP stream is also page content: detachment must not delete the shared object.
		{name: "shared XMP stream", catalog: "/Metadata 5 0 R", pages: 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			testPDFProcessing(t, syntheticPDFCatalog(test.pages, test.info, 0, test.catalog), test.pages)
		})
	}
}

func testPDFProcessing(t *testing.T, source []byte, pages int) {
	t.Helper()
	strict := pdfConfiguration()
	strict.ValidationMode = model.ValidationStrict
	if err := api.Validate(bytes.NewReader(source), strict); err == nil {
		t.Fatal("fixture does not exercise strict metadata validation")
	}
	service, database, _, supervisor, _, courseID := materialFixture(t,
		summaryFake{brief: validBrief("Generated source-grounded summary")})
	ocr := &pageOCRFake{}
	service.ocr = ocr
	service.limits.MaxPages = 100
	created, err := service.Create(context.Background(), CreateInput{CourseID: courseID, ActorID: supervisor,
		Scope: "course-wide", Name: "Scanned book", Kind: "text-book", Format: "pdf"})
	if err != nil {
		t.Fatal(err)
	}
	file, err := service.Upload(context.Background(), created.ID, supervisor, "book.pdf", source)
	if err != nil || file.PageCount != pages || file.MediaType != "application/pdf" {
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
	remaining := pages
	for _, count := range ocr.chunks {
		if count != min(25, remaining) {
			t.Fatalf("chunk sizes = %v for %d pages", ocr.chunks, pages)
		}
		remaining -= count
	}
	if remaining != 0 {
		t.Fatalf("missing %d pages from OCR chunks", remaining)
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
	if err != nil || len(segments) != pages {
		t.Fatalf("content segments = %#v, %v", segments, err)
	}
	for index, segment := range segments {
		if segment.Sequence != index+1 || segment.Text != fmt.Sprintf("Page %d text", index+1) {
			t.Fatalf("page %d = %#v", index+1, segment)
		}
	}
	download, _, err := service.OpenFile(context.Background(), file.ID, supervisor, false)
	if err != nil {
		t.Fatal(err)
	}
	retained, readErr := io.ReadAll(download)
	closeErr := download.Close()
	if readErr != nil || closeErr != nil || !bytes.Equal(retained, source) {
		t.Fatalf("source changed after processing: read=%v, close=%v", readErr, closeErr)
	}
}

func TestPDFUploadRejections(t *testing.T) {
	valid := syntheticPDF(1, "", 0)
	var encrypted bytes.Buffer
	configuration := pdfConfiguration()
	configuration.OwnerPW = "synthetic-owner-password"
	configuration.EncryptUsingAES = true
	configuration.EncryptKeyLength = 256
	if err := api.Encrypt(bytes.NewReader(valid), &encrypted, configuration); err != nil {
		t.Fatal(err)
	}
	corruptXref := bytes.Replace(valid, []byte("xref\n0 "), []byte("xref\nx "), 1)
	if bytes.Equal(corruptXref, valid) {
		t.Fatal("corrupt xref fixture was not modified")
	}
	tests := []struct {
		name   string
		source []byte
	}{
		{name: "unparseable", source: []byte("%PDF-1.4\nnot a document at all\n")},
		{name: "encrypted", source: encrypted.Bytes()},
		{name: "corrupt xref subsection header", source: corruptXref},
		{name: "zero pages", source: syntheticPDF(0, "", 0)},
		{name: "file size above limit", source: syntheticPDF(1, "", 1<<20)},
		{name: "pages above limit", source: syntheticPDF(11, "", 0)},
		{name: "missing signature", source: append([]byte("\n"), valid...)},
		{name: "missing page tree", source: bytes.Replace(valid, []byte("/Pages 2 0 R"), []byte("/Bogus 2 0 R"), 1)},
	}
	for _, marker := range []string{"/Encrypt", "/EmbeddedFile", "/JavaScript", "/JS ", "/Launch"} {
		tests = append(tests, struct {
			name   string
			source []byte
		}{name: "hazard " + marker, source: syntheticPDF(1, "/MIAProbe ["+marker+" ]", 0)})
		tests = append(tests, struct {
			name   string
			source []byte
		}{name: "XMP hazard " + marker, source: syntheticPDFCatalog(1, "/Title 123", 0,
			"/Metadata << /MIAProbe ("+marker+") >>")})
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
	document, err := preparePDFChunks(syntheticPDF(2, "/Title 123", 0))
	if err != nil {
		t.Fatal(err)
	}
	var chunk bytes.Buffer
	if err := writePDFChunk(document, &chunk, 1, 2); err != nil {
		t.Fatal(err)
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

func TestPDFMetadataTolerancePreservesAggregateLimits(t *testing.T) {
	for _, limit := range []string{"files", "pages", "bytes"} {
		t.Run(limit, func(t *testing.T) {
			service, _, _, supervisor, _, courseID := materialFixture(t, nil)
			source := syntheticPDF(2, "/Title 123", 0)
			switch limit {
			case "files":
				service.limits.MaxFiles = 1
			case "pages":
				service.limits.MaxPages = 3
			case "bytes":
				service.limits.MaxMaterialBytes = int64(len(source) + 1)
			}
			created, err := service.Create(context.Background(), CreateInput{CourseID: courseID, ActorID: supervisor,
				Scope: "course-wide", Name: "Bounded PDFs", Kind: "worksheet", Format: "pdf"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.Upload(context.Background(), created.ID, supervisor, "first.pdf", source); err != nil {
				t.Fatal(err)
			}
			if _, err := service.Upload(context.Background(), created.ID, supervisor, "second.pdf", source); !errors.Is(err, ErrInvalid) {
				t.Fatalf("aggregate %s limit error = %v", limit, err)
			}
		})
	}
}
