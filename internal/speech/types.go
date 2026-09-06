// Package speech owns generated-speech cache records and private MP3 files.
package speech

import (
	"errors"
	"time"
)

var (
	ErrNotFound    = errors.New("generated speech not found")
	ErrUnavailable = errors.New("text-to-speech unavailable")
	ErrVoice       = errors.New("text-to-speech voice is not configured")
	ErrInvalid     = errors.New("invalid generated speech state")
)

type Speech struct {
	ID, ResponseID, VoiceID, State, FailureCode string
	GeneratedAt, ExpiresAt                      *time.Time
}

type Audio struct {
	Speech Speech
	Data   []byte
}
