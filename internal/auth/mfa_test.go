package auth

import (
	"testing"
	"time"
)

func TestTOTPVerificationAcceptsRFC6238AndSkew(t *testing.T) {
	secret := []byte("12345678901234567890")
	now := time.Unix(59, 0).UTC()
	if _, ok := verifyTOTP(secret, "287082", now); !ok {
		t.Fatal("RFC 6238 SHA-1 vector was rejected")
	}
	if _, ok := verifyTOTP(secret, totpCode(secret, now.Unix()/30+1), now); !ok {
		t.Fatal("adjacent TOTP step was rejected")
	}
	if _, ok := verifyTOTP(secret, "not-a-code", now); ok {
		t.Fatal("invalid TOTP value was accepted")
	}
}

func TestRecoveryCodesAreCrockfordAndDigestOnly(t *testing.T) {
	codes, digests, err := newRecoveryCodes()
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != recoveryCodeCount || len(digests) != recoveryCodeCount {
		t.Fatalf("codes/digests = %d/%d", len(codes), len(digests))
	}
	for index, code := range codes {
		if len(code) != recoveryCodeLength {
			t.Fatalf("code %d length = %d", index, len(code))
		}
		digest, err := recoveryDigest(code)
		if err != nil || digest != digests[index] {
			t.Fatalf("code %d digest failed", index)
		}
	}
}
