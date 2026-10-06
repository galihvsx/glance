package realtime

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"
)

// ticketTTL is the single-use ticket lifetime (spec §6: 60s).
const ticketTTL = 60 * time.Second

// ticket binds a token to the user it was issued for.
type ticket struct {
	userID  string
	expires time.Time
}

// ticketStore is the in-memory single-use ticket registry. Tickets live at
// most ticketTTL and are deleted on first consume — replay is impossible.
// In-process by design (spec §3 known limitation: one instance in v1), same
// as the hub itself.
type ticketStore struct {
	mu      sync.Mutex
	ttl     time.Duration
	tickets map[string]ticket
}

func newTicketStore(ttl time.Duration) *ticketStore {
	return &ticketStore{ttl: ttl, tickets: make(map[string]ticket)}
}

// issue mints a ticket for userID, sweeping expired ones first so the map
// cannot grow without bound.
func (s *ticketStore) issue(userID string) string {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic("realtime: ticket rand: " + err.Error())
	}
	tok := base64.RawURLEncoding.EncodeToString(raw[:])

	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for t, tk := range s.tickets {
		if !tk.expires.After(now) {
			delete(s.tickets, t)
		}
	}
	s.tickets[tok] = ticket{userID: userID, expires: now.Add(s.ttl)}
	return tok
}

// consume returns the bound userID and deletes the ticket. The second call
// for the same token — or any call after expiry — reports false.
func (s *ticketStore) consume(token string) (string, bool) {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	tk, ok := s.tickets[token]
	if !ok || !tk.expires.After(now) {
		delete(s.tickets, token)
		return "", false
	}
	delete(s.tickets, token)
	return tk.userID, true
}
