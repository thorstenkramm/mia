package identity

import (
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
