package httpserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"unicode/utf8"

	"github.com/labstack/echo/v5"
)

// DefaultRequestBodyLimit bounds JSON:API request documents.
const DefaultRequestBodyLimit = 1 << 20

// DecodeJSONAPI decodes one bounded JSON:API request document into
// destination, rejecting unknown fields. It classifies protocol failures with
// registry errors: a request media type other than exactly
// application/vnd.api+json without parameters maps to 415, a body exceeding
// the bound maps to 413, and malformed JSON, malformed UTF-8, malformed
// Unicode surrogate escapes, duplicate object members at any depth, or
// trailing input map to 400. Callers validate resource-level data and select
// their documented 422 errors.
func DecodeJSONAPI(c *echo.Context, destination any) error {
	mediaType, parameters, err := mime.ParseMediaType(c.Request().Header.Get(echo.HeaderContentType))
	if err != nil || mediaType != jsonAPI || len(parameters) != 0 {
		return NewError(CodeRequestMediaTypeUnsupported)
	}
	if c.Request().ContentLength > DefaultRequestBodyLimit {
		return NewError(CodeRequestBodyTooLarge)
	}
	c.Request().Body = http.MaxBytesReader(c.Response(), c.Request().Body, DefaultRequestBodyLimit)
	body, err := io.ReadAll(c.Request().Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return NewError(CodeRequestBodyTooLarge)
		}
		return NewError(CodeMalformedRequest)
	}
	if !validJSONUnicode(body) {
		return NewError(CodeMalformedRequest)
	}
	if err := validateUniqueObjectMembers(body); err != nil {
		return NewError(CodeMalformedRequest)
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return NewError(CodeMalformedRequest)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return NewError(CodeMalformedRequest)
	}
	return nil
}

// validateUniqueObjectMembers rejects ambiguous last-wins JSON while allowing
// the same member name in distinct objects.
func validateUniqueObjectMembers(body []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := validateJSONValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON input")
	}
	return nil
}

func validateJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		members := make(map[string]struct{})
		for decoder.More() {
			nameToken, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := nameToken.(string)
			if !ok {
				return errors.New("object member name is not a string")
			}
			if _, duplicate := members[name]; duplicate {
				return errors.New("duplicate object member")
			}
			members[name] = struct{}{}
			if err := validateJSONValue(decoder); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	case '[':
		for decoder.More() {
			if err := validateJSONValue(decoder); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	default:
		return errors.New("unexpected JSON delimiter")
	}
}

// validJSONUnicode rejects malformed UTF-8 and unpaired UTF-16 surrogate
// escapes before encoding/json can replace them with U+FFFD.
func validJSONUnicode(body []byte) bool {
	if !utf8.Valid(body) {
		return false
	}
	inString := false
	for index := 0; index < len(body); index++ {
		if !inString {
			if body[index] == '"' {
				inString = true
			}
			continue
		}
		if body[index] == '"' {
			inString = false
			continue
		}
		if body[index] != '\\' {
			continue
		}
		index++
		if index >= len(body) {
			return false
		}
		if body[index] != 'u' {
			continue
		}
		if index+4 >= len(body) {
			return false
		}
		value, ok := unicodeEscape(body[index+1 : index+5])
		if !ok {
			return false
		}
		index += 4
		if value >= 0xD800 && value <= 0xDBFF {
			if index+6 >= len(body) || body[index+1] != '\\' || body[index+2] != 'u' {
				return false
			}
			low, ok := unicodeEscape(body[index+3 : index+7])
			if !ok || low < 0xDC00 || low > 0xDFFF {
				return false
			}
			index += 6
		} else if value >= 0xDC00 && value <= 0xDFFF {
			return false
		}
	}
	return !inString
}

func unicodeEscape(value []byte) (rune, bool) {
	if len(value) != 4 {
		return 0, false
	}
	var result rune
	for _, character := range value {
		result <<= 4
		switch {
		case character >= '0' && character <= '9':
			result += rune(character - '0')
		case character >= 'a' && character <= 'f':
			result += rune(character-'a') + 10
		case character >= 'A' && character <= 'F':
			result += rune(character-'A') + 10
		default:
			return 0, false
		}
	}
	return result, true
}
