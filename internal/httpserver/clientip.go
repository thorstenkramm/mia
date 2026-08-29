package httpserver

import (
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// ClientIPResolver derives client addresses from explicitly trusted proxies.
// It is safe for concurrent use after construction.
type ClientIPResolver struct{ trusted []netip.Prefix }

const unixTransportIdentity = "unix"

// NewClientIPResolver constructs a resolver with loopback networks always trusted.
func NewClientIPResolver(cidrs []string) (*ClientIPResolver, error) {
	trusted := []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("::1/128")}
	for _, cidr := range cidrs {
		prefix, err := netip.ParsePrefix(cidr)
		if err != nil {
			return nil, err
		}
		trusted = append(trusted, prefix)
	}
	return &ClientIPResolver{trusted: trusted}, nil
}

// Resolve returns a safe source-IP key, ignoring malformed forwarded headers.
func (resolver *ClientIPResolver) Resolve(request *http.Request) string {
	peer, err := remoteAddress(request.RemoteAddr)
	if err != nil {
		if isUnixTransport(request.RemoteAddr) {
			return resolver.forwarded(unixTransportIdentity, request.Header.Get("X-Forwarded-For"), true)
		}
		return "unknown"
	}
	return resolver.forwarded(peer.String(), request.Header.Get("X-Forwarded-For"), resolver.isTrusted(peer))
}

func (resolver *ClientIPResolver) forwarded(peer, forwarded string, trusted bool) string {
	if !trusted {
		return peer
	}
	if len(forwarded) == 0 {
		return peer
	}
	if len(forwarded) > 2048 {
		return peer
	}
	parts := strings.Split(forwarded, ",")
	if len(parts) > 20 {
		return peer
	}
	current, err := netip.ParseAddr(peer)
	if err != nil { // Unix is a trusted transport with no IP peer.
		return resolver.forwardedFromTrustedTransport(peer, parts)
	}
	for index := len(parts) - 1; index >= 0; index-- {
		candidate, err := netip.ParseAddr(strings.TrimSpace(parts[index]))
		if err != nil {
			return peer
		}
		if !resolver.isTrusted(current) {
			return current.String()
		}
		current = candidate
	}
	return current.String()
}

func (resolver *ClientIPResolver) forwardedFromTrustedTransport(peer string, parts []string) string {
	for index := len(parts) - 1; index >= 0; index-- {
		candidate, err := netip.ParseAddr(strings.TrimSpace(parts[index]))
		if err != nil {
			return peer
		}
		if !resolver.isTrusted(candidate) {
			return candidate.String()
		}
	}
	return netip.MustParseAddr(strings.TrimSpace(parts[0])).String()
}

func (resolver *ClientIPResolver) isTrusted(address netip.Addr) bool {
	for _, prefix := range resolver.trusted {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}
func remoteAddress(value string) (netip.Addr, error) {
	host, _, err := net.SplitHostPort(value)
	if err != nil {
		return netip.Addr{}, err
	}
	address, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, errors.New("invalid remote address")
	}
	return address, nil
}

func isUnixTransport(value string) bool {
	return value == "" || value == "@" || strings.HasPrefix(value, "@") || strings.Contains(value, "/")
}
