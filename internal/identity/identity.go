// Package identity validates and normalizes shared identity values.
package identity

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"
	_ "time/tzdata"
	"unicode"
	"unicode/utf8"

	_ "embed"
	"golang.org/x/crypto/argon2"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"golang.org/x/text/unicode/norm"
)

var (
	ErrInvalidUsername = errors.New("invalid username")
	ErrInvalidEmail    = errors.New("invalid email")
	ErrInvalidText     = errors.New("invalid text")
	ErrInvalidPassword = errors.New("invalid password")
)

// SecLists snapshot: danielmiessler/SecLists, commit
// f025490a4bc7bd1d6cd36c3b834631acd615ff28 (2026-08-30T11:13:41Z),
// Passwords/Common-Credentials/xato-net-10-million-passwords-100000.txt.
// Upstream is distributed under the MIT License.
//
//go:embed xato-net-10-million-passwords-100000.txt
var commonPasswordsFile string

var commonPasswords = makePasswordSet(commonPasswordsFile)

// Password validates the exact submitted password and returns an Argon2id PHC hash.
func Password(value string) (string, error) {
	if !validPassword(value) {
		return "", ErrInvalidPassword
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	derived := argon2.IDKey([]byte(value), salt, 2, 19456, 1, 32)
	return fmt.Sprintf("$argon2id$v=19$m=19456,t=2,p=1$%s$%s", base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(derived)), nil
}

// VerifyPassword compares the exact submitted password against a validated PHC hash.
func VerifyPassword(value, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" || parts[3] != "m=19456,t=2,p=1" {
		return false, errors.New("malformed stored password hash")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) != 16 {
		return false, errors.New("malformed stored password hash")
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(expected) != 32 {
		return false, errors.New("malformed stored password hash")
	}
	actual := argon2.IDKey([]byte(value), salt, 2, 19456, 1, 32)
	return subtle.ConstantTimeCompare(actual, expected) == 1, nil
}

// DummyPasswordWork equalizes rejected-login work without retaining a password.
func DummyPasswordWork(value string) {
	argon2.IDKey([]byte(value), []byte("mia-login-dummy!"), 2, 19456, 1, 32)
}

func validPassword(value string) bool {
	return utf8.ValidString(value) && len(value) <= 512 && utf8.RuneCountInString(value) >= 12 &&
		utf8.RuneCountInString(value) <= 128 && !commonPasswords[value]
}

func makePasswordSet(value string) map[string]bool {
	set := make(map[string]bool, 100_000)
	for _, password := range strings.Split(strings.TrimSuffix(value, "\n"), "\n") {
		set[password] = true
	}
	return set
}

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
	if err != nil || tag == language.Und || strings.HasPrefix(strings.ToLower(value), "x-") {
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
	if value == "" || value == "Local" || strings.Contains(value, "+") || strings.HasPrefix(value, "-") {
		return "", errors.New("invalid time zone")
	}
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
