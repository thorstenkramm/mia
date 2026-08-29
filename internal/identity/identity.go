// Package identity validates and normalizes shared identity values.
package identity

import (
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"golang.org/x/text/unicode/norm"
)

var (
	ErrInvalidUsername = errors.New("invalid username")
	ErrInvalidEmail    = errors.New("invalid email")
	ErrInvalidText     = errors.New("invalid text")
)

// Username validates a username and returns its ASCII-lowercase uniqueness key.
func Username(value string) (string, error) {
	if len(value) < 3 || len(value) > 32 || !isASCIIAlphaNum(value[0]) || !isASCIIAlphaNum(value[len(value)-1]) {
		return "", ErrInvalidUsername
	}
	for _, char := range value {
		if char > unicode.MaxASCII || (!isASCIIAlphaNum(byte(char)) && char != '.' && char != '_' && char != '-') {
			return "", ErrInvalidUsername
		}
	}
	return strings.ToLower(value), nil
}

// Email validates an email address and returns its trimmed display value and
// ASCII-lowercase uniqueness key.
func Email(value string) (string, string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 254 || !isASCII(value) || strings.ContainsAny(value, "()[]\\\"") {
		return "", "", ErrInvalidEmail
	}
	address, err := mail.ParseAddress(value)
	if err != nil || address.Address != value {
		return "", "", ErrInvalidEmail
	}
	parts := strings.Split(value, "@")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.HasPrefix(parts[1], "[") ||
		!strings.Contains(parts[1], ".") {
		return "", "", ErrInvalidEmail
	}
	return value, strings.ToLower(value), nil
}

// Language validates and canonicalizes a BCP 47 language tag.
func Language(value string) (string, error) {
	tag, err := language.Parse(value)
	if err != nil || tag == language.Und {
		return "", fmt.Errorf("invalid language: %w", err)
	}
	return tag.String(), nil
}

// Country validates an ISO 3166-1 alpha-2 country code.
func Country(value string) (string, error) {
	value = strings.ToUpper(value)
	region, err := language.ParseRegion(value)
	if len(value) != 2 || !isASCIIAlpha(value[0]) || !isASCIIAlpha(value[1]) || err != nil || !region.IsCountry() {
		return "", errors.New("invalid country")
	}
	return value, nil
}

// TimeZone validates an IANA time-zone name.
func TimeZone(value string) (string, error) {
	if _, err := time.LoadLocation(value); err != nil {
		return "", fmt.Errorf("invalid time zone: %w", err)
	}
	return value, nil
}

// E164 validates a strict E.164 mobile number.
func E164(value string) (string, error) {
	if len(value) < 2 || len(value) > 16 || value[0] != '+' || value[1] == '0' {
		return "", errors.New("invalid E.164 number")
	}
	for _, char := range value[1:] {
		if char < '0' || char > '9' {
			return "", errors.New("invalid E.164 number")
		}
	}
	return value, nil
}

// Text normalizes optional profile and descriptive text. Empty normalized text
// is represented by a nil pointer.
func Text(value string, maxRunes, maxBytes int) (*string, error) {
	if !utf8.ValidString(value) {
		return nil, ErrInvalidText
	}
	value = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\r", "\n"))
	if value == "" {
		return nil, nil
	}
	if utf8.RuneCountInString(value) > maxRunes || len(value) > maxBytes {
		return nil, ErrInvalidText
	}
	for _, char := range value {
		if char == 0 || (unicode.IsControl(char) && char != '\t' && char != '\n') {
			return nil, ErrInvalidText
		}
	}
	return &value, nil
}

// NameKey returns the NFC-normalized Unicode-case-folded uniqueness key used by
// course and material names.
func NameKey(value string) string {
	return norm.NFC.String(cases.Fold().String(norm.NFC.String(value)))
}

func isASCII(value string) bool {
	for _, char := range value {
		if char > unicode.MaxASCII {
			return false
		}
	}
	return true
}

func isASCIIAlpha(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
}

func isASCIIAlphaNum(value byte) bool {
	return isASCIIAlpha(value) || value >= '0' && value <= '9'
}
