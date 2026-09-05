package httpserver

import (
	"errors"
	"strings"
	"time"
)

const instantLayout = "2006-01-02T15:04:05.000000Z"

// ErrInvalidInstant reports a rejected API instant value.
var ErrInvalidInstant = errors.New("invalid instant")

// FormatInstant renders an instant as RFC 3339 UTC with the Z suffix and
// exactly six fractional digits.
func FormatInstant(value time.Time) string { return value.UTC().Format(instantLayout) }

// FormatOptionalInstant renders an optional instant. Absent instants, nil or
// zero, return nil and serialize as JSON null.
func FormatOptionalInstant(value *time.Time) *string {
	if value == nil || value.IsZero() {
		return nil
	}
	formatted := FormatInstant(*value)
	return &formatted
}

// ParseInstant parses a strict RFC 3339 instant. It accepts up to nine
// fractional digits and rejects values with a non-zero numeric UTC offset.
// The result is normalized to UTC.
func ParseInstant(value string) (time.Time, error) {
	if fractionalDigits(value) > 9 {
		return time.Time{}, ErrInvalidInstant
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, ErrInvalidInstant
	}
	if _, offset := parsed.Zone(); offset != 0 {
		return time.Time{}, ErrInvalidInstant
	}
	return parsed.UTC(), nil
}

func fractionalDigits(value string) int {
	start := strings.IndexByte(value, '.')
	if start < 0 {
		return 0
	}
	digits := 0
	for _, character := range value[start+1:] {
		if character < '0' || character > '9' {
			break
		}
		digits++
	}
	return digits
}
