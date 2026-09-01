package material

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/url"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/thorstenkramm/mia/internal/identity"
	"golang.org/x/text/unicode/norm"
)

const (
	maxContentBytes = 512 << 20
	maxBriefBytes   = 256 << 10
	maxDOCXEntries  = 10_000
	maxDOCXExpanded = 512 << 20
	maxDOCXEntry    = 100 << 20
)

func validateName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || utf8.RuneCountInString(value) > 200 || len(value) > 800 {
		return "", ErrInvalid
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return "", ErrInvalid
		}
	}
	return value, nil
}

func validateFilename(value string) error {
	if value == "" || value == "." || !utf8.ValidString(value) || !norm.NFC.IsNormalString(value) ||
		utf8.RuneCountInString(value) > 255 || len(value) > 1024 || strings.ContainsAny(value, "/\\") {
		return ErrInvalid
	}
	for _, char := range value {
		if unicode.IsControl(char) || isBidiControl(char) {
			return ErrInvalid
		}
	}
	return nil
}

func isBidiControl(char rune) bool {
	return char == '\u061c' || char == '\u200e' || char == '\u200f' || char >= '\u202a' && char <= '\u202e' ||
		char >= '\u2066' && char <= '\u2069'
}

func validateURL(raw, kind string) (string, error) {
	if raw == "" || len(raw) > 2048 || !isASCII(raw) {
		return "", ErrInvalid
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return "", ErrInvalid
	}
	if kind == "youtube" {
		host := strings.ToLower(parsed.Hostname())
		switch host {
		case "youtube.com", "www.youtube.com", "m.youtube.com", "youtu.be", "youtube-nocookie.com",
			"www.youtube-nocookie.com":
		default:
			return "", ErrInvalid
		}
	}
	return parsed.String(), nil
}

func validateBrief(brief Brief) error {
	if brief.Version != 1 || invalidText(brief.Summary, 1, 4_000, 16<<10) || len(brief.Subjects) < 1 ||
		len(brief.Subjects) > 20 || len(brief.LearningGoals) > 30 || len(brief.Sections) > 500 ||
		len(brief.Warnings) > 20 {
		return ErrInvalid
	}
	if brief.EducationalLevel != nil && invalidText(*brief.EducationalLevel, 1, 100, 400) {
		return ErrInvalid
	}
	for _, subject := range brief.Subjects {
		if invalidText(subject, 1, 100, 400) {
			return ErrInvalid
		}
	}
	for _, goal := range brief.LearningGoals {
		if invalidText(goal, 1, 300, 1200) {
			return ErrInvalid
		}
	}
	for index, section := range brief.Sections {
		if section.Sequence != index+1 || invalidText(section.Title, 1, 200, 800) ||
			invalidText(section.Description, 1, 500, 2000) || len(section.SearchTerms) > 10 {
			return ErrInvalid
		}
		if section.Label != nil && invalidText(*section.Label, 1, 100, 400) {
			return ErrInvalid
		}
		for _, term := range section.SearchTerms {
			if invalidText(term, 1, 50, 200) {
				return ErrInvalid
			}
		}
	}
	for _, warning := range brief.Warnings {
		if invalidText(warning, 1, 500, 2000) {
			return ErrInvalid
		}
	}
	encoded, err := json.Marshal(brief)
	if err != nil || len(encoded) > maxBriefBytes {
		return ErrInvalid
	}
	return nil
}

func decodeBrief(raw []byte) (Brief, error) {
	if len(raw) > maxBriefBytes || !utf8.Valid(raw) {
		return Brief{}, ErrInvalid
	}
	var brief Brief
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&brief); err != nil {
		return Brief{}, ErrInvalid
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Brief{}, ErrInvalid
	}
	brief, err := normalizeBrief(brief)
	if err != nil || validateBrief(brief) != nil {
		return Brief{}, ErrInvalid
	}
	return brief, nil
}

func normalizeBrief(brief Brief) (Brief, error) {
	var err error
	if brief.Summary, err = normalizeBriefText(brief.Summary, 4_000, 16<<10); err != nil {
		return Brief{}, err
	}
	for index := range brief.Subjects {
		if brief.Subjects[index], err = normalizeBriefText(brief.Subjects[index], 100, 400); err != nil {
			return Brief{}, err
		}
	}
	for index := range brief.LearningGoals {
		if brief.LearningGoals[index], err = normalizeBriefText(brief.LearningGoals[index], 300, 1200); err != nil {
			return Brief{}, err
		}
	}
	for index := range brief.Sections {
		section := &brief.Sections[index]
		if section.Title, err = normalizeBriefText(section.Title, 200, 800); err != nil {
			return Brief{}, err
		}
		if section.Description, err = normalizeBriefText(section.Description, 500, 2000); err != nil {
			return Brief{}, err
		}
		if section.Label != nil {
			value, normalizeErr := normalizeBriefText(*section.Label, 100, 400)
			if normalizeErr != nil {
				return Brief{}, normalizeErr
			}
			section.Label = &value
		}
		for term := range section.SearchTerms {
			if section.SearchTerms[term], err = normalizeBriefText(section.SearchTerms[term], 50, 200); err != nil {
				return Brief{}, err
			}
		}
	}
	for index := range brief.Warnings {
		if brief.Warnings[index], err = normalizeBriefText(brief.Warnings[index], 500, 2000); err != nil {
			return Brief{}, err
		}
	}
	if brief.EducationalLevel != nil {
		value, normalizeErr := normalizeBriefText(*brief.EducationalLevel, 100, 400)
		if normalizeErr != nil {
			return Brief{}, normalizeErr
		}
		brief.EducationalLevel = &value
	}
	return brief, nil
}

func validateSource(format string, source []byte, limits Limits) (string, int, []byte, error) {
	if int64(len(source)) < 1 || int64(len(source)) > limits.MaxFileBytes {
		return "", 0, nil, ErrInvalid
	}
	switch format {
	case "pdf":
		if !bytes.HasPrefix(source, []byte("%PDF-")) || containsPDFHazard(source) {
			return "", 0, nil, ErrInvalid
		}
		reader := bytes.NewReader(source)
		configuration := model.NewDefaultConfiguration()
		configuration.ValidationMode = model.ValidationStrict
		if err := api.Validate(reader, configuration); err != nil {
			return "", 0, nil, ErrInvalid
		}
		pages, err := api.PageCount(bytes.NewReader(source), configuration)
		if err != nil || pages < 1 {
			return "", 0, nil, ErrInvalid
		}
		return "application/pdf", pages, source, nil
	case "jpeg", "png":
		expected := "image/jpeg"
		if format == "png" {
			expected = "image/png"
		}
		if format == "jpeg" && !bytes.HasPrefix(source, []byte{0xff, 0xd8, 0xff}) ||
			format == "png" && (!bytes.HasPrefix(source, []byte("\x89PNG\r\n\x1a\n")) || bytes.Contains(source, []byte("acTL"))) {
			return "", 0, nil, ErrInvalid
		}
		config, detected, err := image.DecodeConfig(bytes.NewReader(source))
		pixels := int64(config.Width) * int64(config.Height)
		if err != nil || detected != format || config.Width > 20_000 || config.Height > 20_000 ||
			pixels > int64(limits.MaxImageMegapixels)*1_000_000 {
			return "", 0, nil, ErrInvalid
		}
		return expected, 1, source, nil
	case "text", "markdown":
		if !utf8.Valid(source) || bytes.IndexByte(source, 0) >= 0 {
			return "", 0, nil, ErrInvalid
		}
		value := strings.ReplaceAll(strings.ReplaceAll(string(source), "\r\n", "\n"), "\r", "\n")
		for _, char := range value {
			if unicode.IsControl(char) && char != '\n' && char != '\t' {
				return "", 0, nil, ErrInvalid
			}
		}
		media := "text/plain; charset=utf-8"
		if format == "markdown" {
			media = "text/markdown; charset=utf-8"
		}
		return media, 0, []byte(value), nil
	case "docx":
		if err := validateDOCX(source); err != nil {
			return "", 0, nil, ErrInvalid
		}
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document", 0, source, nil
	default:
		return "", 0, nil, ErrInvalid
	}
}

func validateDOCX(source []byte) error {
	archive, err := zip.NewReader(bytes.NewReader(source), int64(len(source)))
	if err != nil || len(archive.File) > maxDOCXEntries {
		return ErrInvalid
	}
	var expanded int64
	hasDocument, hasContentTypes := false, false
	for _, entry := range archive.File {
		name := filepath.ToSlash(entry.Name)
		if strings.Contains(name, "../") || strings.HasPrefix(name, "/") || entry.UncompressedSize64 > maxDOCXEntry {
			return ErrInvalid
		}
		if strings.EqualFold(filepath.Base(name), "vbaProject.bin") {
			return ErrInvalid
		}
		expanded += int64(entry.UncompressedSize64)
		if expanded > maxDOCXExpanded || entry.CompressedSize64 > 0 && entry.UncompressedSize64/entry.CompressedSize64 > 100 {
			return ErrInvalid
		}
		if name == "word/document.xml" {
			hasDocument = true
		}
		if name == "[Content_Types].xml" {
			hasContentTypes = true
		}
	}
	if !hasDocument || !hasContentTypes {
		return ErrInvalid
	}
	return nil
}

func containsPDFHazard(source []byte) bool {
	for _, marker := range [][]byte{[]byte("/Encrypt"), []byte("/EmbeddedFile"), []byte("/JavaScript"),
		[]byte("/JS "), []byte("/Launch")} {
		if bytes.Contains(source, marker) {
			return true
		}
	}
	return false
}

func invalidText(value string, minimum, maximumRunes, maximumBytes int) bool {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) < minimum ||
		utf8.RuneCountInString(value) > maximumRunes || len(value) > maximumBytes {
		return true
	}
	for _, char := range value {
		if char == 0 || unicode.IsControl(char) && char != '\n' && char != '\t' {
			return true
		}
	}
	return false
}

func normalizeBriefText(value string, runes, bytes int) (string, error) {
	normalized, err := identity.Text(value, runes, bytes)
	if err != nil || normalized == nil {
		return "", fmt.Errorf("normalize brief text: %w", ErrInvalid)
	}
	return *normalized, nil
}

func isASCII(value string) bool {
	for _, char := range value {
		if char > unicode.MaxASCII {
			return false
		}
	}
	return true
}
