package identity

import (
	"errors"
	"strings"
	"testing"
)

func TestPasswordPolicyAndVerification(t *testing.T) {
	hash, err := Password("twelve chars")
	if err != nil {
		t.Fatal(err)
	}
	valid, err := VerifyPassword("twelve chars", hash)
	if err != nil || !valid {
		t.Fatalf("verification = %v, %v", valid, err)
	}
	if _, err := Password("short"); err == nil {
		t.Fatal("short password accepted")
	}
	for _, password := range []string{"passwordpassword", strings.Repeat("a", 129), strings.Repeat("界", 129), strings.Repeat("a", 513)} {
		if _, err := Password(password); err == nil {
			t.Fatalf("invalid password accepted: %d runes, %d bytes", len([]rune(password)), len(password))
		}
	}
	if _, err := Password("  twelve chars"); err != nil {
		t.Fatalf("spaces were altered or rejected: %v", err)
	}
}

func TestPasswordPolicyReasons(t *testing.T) {
	for name, password := range map[string]string{
		"invalid UTF-8":  string([]byte{0xff}),
		"too short":      "short",
		"too many runes": strings.Repeat("a", 129),
		"too many bytes": strings.Repeat("a", 513),
		"common":         "passwordpassword",
	} {
		t.Run(name, func(t *testing.T) {
			if err := PasswordPolicy(password); err == nil {
				t.Fatal("PasswordPolicy accepted an invalid password")
			}
		})
	}
	if !errors.Is(PasswordPolicy("short"), ErrPasswordTooShort) {
		t.Fatal("short password did not retain its policy reason")
	}
}
