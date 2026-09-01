package material

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/thorstenkramm/mia/internal/provider/mistral"
)

func localExtract(format string, source []byte) ([]Segment, error) {
	var text string
	var err error
	switch format {
	case "text", "markdown":
		text = string(source)
	case "docx":
		text, err = extractDOCX(source)
	default:
		return nil, ErrInvalid
	}
	if err != nil {
		return nil, err
	}
	return splitText(text), nil
}

func ocrSegments(result mistral.Result) ([]Segment, error) {
	sort.Slice(result.Pages, func(left, right int) bool { return result.Pages[left].Index < result.Pages[right].Index })
	segments := make([]Segment, 0, len(result.Pages))
	for _, page := range result.Pages {
		text := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(page.Markdown, "\r\n", "\n"), "\r", "\n"))
		if text == "" || !utf8.ValidString(text) {
			return nil, ErrInvalid
		}
		segments = append(segments, Segment{Version: 1, Sequence: len(segments) + 1, Text: text})
	}
	if len(segments) == 0 {
		return nil, ErrInvalid
	}
	return segments, nil
}

func splitText(text string) []Segment {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	paragraphs := strings.Split(text, "\n\n")
	segments := make([]Segment, 0, len(paragraphs))
	for _, paragraph := range paragraphs {
		paragraph = strings.TrimSpace(paragraph)
		if paragraph == "" {
			continue
		}
		segments = append(segments, Segment{Version: 1, Sequence: len(segments) + 1, Text: paragraph})
	}
	return segments
}

func encodeSegments(segments []Segment) ([]byte, error) {
	if len(segments) == 0 {
		return nil, ErrInvalid
	}
	var output bytes.Buffer
	writer := bufio.NewWriter(&output)
	decodedBytes := 0
	for index, segment := range segments {
		if segment.Version != 1 || segment.Sequence != index+1 || invalidText(segment.Text, 1, maxContentBytes, maxContentBytes) {
			return nil, ErrInvalid
		}
		decodedBytes += len(segment.Text)
		if decodedBytes > maxContentBytes {
			return nil, ErrInvalid
		}
		encoded, err := json.Marshal(segment)
		if err != nil {
			return nil, fmt.Errorf("encode content segment: %w", err)
		}
		if output.Len()+len(encoded)+1 > maxContentBytes {
			return nil, ErrInvalid
		}
		if _, err := writer.Write(encoded); err != nil {
			return nil, fmt.Errorf("write content segment: %w", err)
		}
		if err := writer.WriteByte('\n'); err != nil {
			return nil, fmt.Errorf("terminate content segment: %w", err)
		}
	}
	if err := writer.Flush(); err != nil {
		return nil, fmt.Errorf("flush content segments: %w", err)
	}
	return output.Bytes(), nil
}

func decodeSegments(source []byte) ([]Segment, error) {
	if len(source) > maxContentBytes {
		return nil, ErrInvalid
	}
	scanner := bufio.NewScanner(bytes.NewReader(source))
	scanner.Buffer(make([]byte, 64<<10), maxContentBytes)
	var segments []Segment
	for scanner.Scan() {
		var segment Segment
		decoder := json.NewDecoder(bytes.NewReader(scanner.Bytes()))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&segment); err != nil {
			return nil, ErrInvalid
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) || segment.Version != 1 ||
			segment.Sequence != len(segments)+1 || invalidText(segment.Text, 1, maxContentBytes, maxContentBytes) {
			return nil, ErrInvalid
		}
		segments = append(segments, segment)
	}
	if err := scanner.Err(); err != nil || len(segments) == 0 {
		return nil, ErrInvalid
	}
	return segments, nil
}

func extractDOCX(source []byte) (string, error) {
	archive, err := zip.NewReader(bytes.NewReader(source), int64(len(source)))
	if err != nil {
		return "", ErrInvalid
	}
	allowed := func(name string) bool {
		return name == "word/document.xml" || strings.HasPrefix(name, "word/header") ||
			strings.HasPrefix(name, "word/footer") || name == "word/footnotes.xml" || name == "word/endnotes.xml"
	}
	files := append([]*zip.File(nil), archive.File...)
	sort.Slice(files, func(left, right int) bool {
		if files[left].Name == "word/document.xml" {
			return true
		}
		return files[left].Name < files[right].Name
	})
	var output strings.Builder
	for _, entry := range files {
		if !allowed(entry.Name) {
			continue
		}
		reader, err := entry.Open()
		if err != nil {
			return "", ErrInvalid
		}
		text, parseErr := extractWordXML(io.LimitReader(reader, maxDOCXEntry+1))
		closeErr := reader.Close()
		if parseErr != nil || closeErr != nil {
			return "", ErrInvalid
		}
		if text != "" {
			output.WriteString(text)
			output.WriteString("\n\n")
		}
		if output.Len() > maxContentBytes {
			return "", ErrInvalid
		}
	}
	return strings.TrimSpace(output.String()), nil
}

func extractWordXML(reader io.Reader) (string, error) {
	decoder := xml.NewDecoder(reader)
	decoder.Strict = true
	var output strings.Builder
	deletedDepth := 0
	runHidden := false
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
		switch value := token.(type) {
		case xml.StartElement:
			switch value.Name.Local {
			case "del":
				deletedDepth++
			case "r":
				runHidden = false
			case "vanish":
				runHidden = true
			case "tab":
				if deletedDepth == 0 && !runHidden {
					output.WriteByte('\t')
				}
			case "br":
				if deletedDepth == 0 && !runHidden {
					output.WriteByte('\n')
				}
			}
		case xml.EndElement:
			switch value.Name.Local {
			case "del":
				deletedDepth = max(0, deletedDepth-1)
			case "r":
				runHidden = false
			case "p", "tr":
				if deletedDepth == 0 {
					output.WriteByte('\n')
				}
			}
		case xml.CharData:
			if deletedDepth == 0 && !runHidden {
				output.Write(value)
			}
		}
	}
	return strings.TrimSpace(output.String()), nil
}
