package realtime

// Ticket store unit tests (Task 24): single-use semantics and expiry.
// 60s production TTL; tests use a short TTL store directly.

import (
	"testing"
	"time"
)

func TestTicketSingleUse(t *testing.T) {
	s := newTicketStore(time.Minute)
	tok := s.issue("user-1")
	if tok == "" {
		t.Fatal("empty ticket")
	}

	uid, ok := s.consume(tok)
	if !ok || uid != "user-1" {
		t.Fatalf("consume = (%q, %v), want (user-1, true)", uid, ok)
	}
	if _, ok := s.consume(tok); ok {
		t.Fatal("ticket consumed twice — must be single-use")
	}
}

func TestTicketUnknownRejected(t *testing.T) {
	s := newTicketStore(time.Minute)
	if _, ok := s.consume("nope"); ok {
		t.Fatal("unknown ticket consumed")
	}
}

func TestTicketExpiry(t *testing.T) {
	s := newTicketStore(20 * time.Millisecond)
	tok := s.issue("user-1")
	time.Sleep(50 * time.Millisecond)
	if _, ok := s.consume(tok); ok {
		t.Fatal("expired ticket consumed")
	}
}

func TestTicketTokensUnique(t *testing.T) {
	s := newTicketStore(time.Minute)
	seen := map[string]struct{}{}
	for i := 0; i < 100; i++ {
		tok := s.issue("user-1")
		if _, dup := seen[tok]; dup {
			t.Fatal("duplicate ticket token")
		}
		seen[tok] = struct{}{}
	}
}
