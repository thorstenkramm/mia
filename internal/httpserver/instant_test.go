package httpserver

import (
	"errors"
	"testing"
	"time"
)

func TestFormatInstantNormalizesToSixDigitUTC(t *testing.T) {
	value := time.Date(2026, 1, 2, 3, 4, 5, 123456789, time.FixedZone("CET", 3600))
	if got := FormatInstant(value); got != "2026-01-02T02:04:05.123456Z" {
		t.Fatalf("formatted instant = %q", got)
	}
	whole := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if got := FormatInstant(whole); got != "2026-01-02T03:04:05.000000Z" {
		t.Fatalf("whole-second instant = %q", got)
	}
}

func TestFormatOptionalInstant(t *testing.T) {
	if FormatOptionalInstant(nil) != nil {
		t.Fatal("nil instant did not serialize as absent")
	}
	zero := time.Time{}
	if FormatOptionalInstant(&zero) != nil {
		t.Fatal("zero instant did not serialize as absent")
	}
	value := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	got := FormatOptionalInstant(&value)
	if got == nil || *got != "2026-01-02T03:04:05.000000Z" {
		t.Fatalf("optional instant = %v", got)
	}
}

func TestParseInstant(t *testing.T) {
	cases := []struct {
		name, value string
		want        string
		wantErr     bool
	}{
		{name: "z suffix", value: "2026-01-02T03:04:05Z", want: "2026-01-02T03:04:05.000000Z"},
		{name: "six fractional digits", value: "2026-01-02T03:04:05.123456Z", want: "2026-01-02T03:04:05.123456Z"},
		{name: "nine fractional digits", value: "2026-01-02T03:04:05.123456789Z", want: "2026-01-02T03:04:05.123456Z"},
		{name: "zero numeric offset", value: "2026-01-02T03:04:05+00:00", want: "2026-01-02T03:04:05.000000Z"},
		{name: "ten fractional digits", value: "2026-01-02T03:04:05.1234567891Z", wantErr: true},
		{name: "positive offset", value: "2026-01-02T05:04:05+02:00", wantErr: true},
		{name: "negative offset", value: "2026-01-02T01:04:05-02:00", wantErr: true},
		{name: "lowercase z", value: "2026-01-02T03:04:05z", wantErr: true},
		{name: "missing offset", value: "2026-01-02T03:04:05", wantErr: true},
		{name: "space separator", value: "2026-01-02 03:04:05Z", wantErr: true},
		{name: "empty", value: "", wantErr: true},
		{name: "garbage", value: "not-an-instant", wantErr: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			parsed, err := ParseInstant(testCase.value)
			if testCase.wantErr {
				if !errors.Is(err, ErrInvalidInstant) {
					t.Fatalf("error = %v, want ErrInvalidInstant", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("error = %v", err)
			}
			if got := FormatInstant(parsed); got != testCase.want {
				t.Fatalf("parsed instant = %q, want %q", got, testCase.want)
			}
		})
	}
}
