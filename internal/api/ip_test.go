package api

// C2T8 bug bundle B: proxy headers are trusted only from configured
// CIDRs. X-Forwarded-For from an untrusted peer must be ignored (the
// direct peer is the client IP), so it can neither evade nor poison
// IP-keyed rate limits.

import (
	"net"
	"net/http/httptest"
	"testing"
)

func mustCIDR(t *testing.T, s string) *net.IPNet {
	t.Helper()
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		t.Fatalf("ParseCIDR(%q): %v", s, err)
	}
	return n
}

func TestProxyAwareIPExtractor(t *testing.T) {
	ext := ProxyAwareIPExtractor([]*net.IPNet{mustCIDR(t, "10.0.0.0/8")})

	// Trusted peer + XFF: the leftmost (original client) is honored.
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.1.2.3:4567"
	req.Header.Set("X-Forwarded-For", "203.0.113.9, 10.1.2.3")
	if got := ext(req); got != "203.0.113.9" {
		t.Errorf("trusted peer: got %q, want 203.0.113.9", got)
	}

	// Untrusted peer + spoofed XFF: header ignored, direct peer wins.
	req = httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "198.51.100.7:4567"
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	if got := ext(req); got != "198.51.100.7" {
		t.Errorf("untrusted peer: got %q, want 198.51.100.7 (XFF must be ignored)", got)
	}

	// Trusted peer, no XFF: direct peer.
	req = httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.9.9.9:4567"
	if got := ext(req); got != "10.9.9.9" {
		t.Errorf("no XFF: got %q, want 10.9.9.9", got)
	}

	// Loopback peer is NOT implicitly trusted (defaults disabled).
	req = httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "127.0.0.1:4567"
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	if got := ext(req); got != "127.0.0.1" {
		t.Errorf("loopback peer: got %q, want 127.0.0.1 (loopback not implicitly trusted)", got)
	}
}
