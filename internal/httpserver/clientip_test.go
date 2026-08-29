package httpserver

import (
	"net/http/httptest"
	"testing"
)

func TestClientIPResolverTrustsOnlyTrustedPeer(t *testing.T) {
	resolver, err := NewClientIPResolver([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "http://mia.test/", nil)
	request.RemoteAddr = "198.51.100.9:1234"
	request.Header.Set("X-Forwarded-For", "203.0.113.4")
	if got := resolver.Resolve(request); got != "198.51.100.9" {
		t.Fatalf("untrusted peer resolved to %q", got)
	}
	request.RemoteAddr = "10.1.2.3:1234"
	if got := resolver.Resolve(request); got != "203.0.113.4" {
		t.Fatalf("trusted peer resolved to %q", got)
	}
}

func TestClientIPResolverRejectsMalformedForwardedHeaderAndTrustsUnixTransport(t *testing.T) {
	resolver, err := NewClientIPResolver([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "http://mia.test/", nil)
	request.RemoteAddr = "10.1.2.3:1234"
	request.Header.Set("X-Forwarded-For", "203.0.113.1, bad")
	if got := resolver.Resolve(request); got != "10.1.2.3" {
		t.Fatalf("malformed header resolved to %q", got)
	}
	request.RemoteAddr = "@"
	request.Header.Set("X-Forwarded-For", "203.0.113.1, 10.1.2.3")
	if got := resolver.Resolve(request); got != "203.0.113.1" {
		t.Fatalf("Unix transport resolved to %q", got)
	}
}
