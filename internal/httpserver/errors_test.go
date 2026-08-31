package httpserver

import "testing"

func TestAuthErrorCodesUseGlobalPrefix(t *testing.T) {
	for _, code := range []Code{CodeInvalidCredentials, CodeInvalidRequest, CodeInvalidPassword, CodeUnauthenticated, CodePasswordChangeRequired, CodeLoginThrottled} {
		if len(code) < 5 || string(code[:5]) != "auth_" {
			t.Fatalf("auth code = %q", code)
		}
	}
}
