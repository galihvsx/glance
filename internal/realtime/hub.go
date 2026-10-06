package realtime

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"
)

// sendBufferSize bounds each subscriber's outbound queue. Broadcast never
// blocks: a subscriber whose buffer is full has its event dropped (it is a
// slow or dead client; the read loop will reap it). Clients resync via the
// ?updated_after= delta endpoint (spec §6: no replay buffer).
const sendBufferSize = 64

// Subscriber is one websocket connection's receive end. The send channel is
// drained by the connection's write loop in the api package.
type Subscriber struct {
	id     string
	userID string
	send   chan Event
}

// ID is the subscriber's unique connection id.
func (s *Subscriber) ID() string { return s.id }

// UserID is the authenticated user owning this connection.
func (s *Subscriber) UserID() string { return s.userID }

// Hub is the in-process pub/sub registry. Subscribers register per channel;
// Broadcast fans an event out to every subscriber of each named channel.
// The zero value is not usable — construct with NewHub.
type Hub struct {
	mu      sync.RWMutex
	subs    map[string]map[*Subscriber]struct{}
	tickets *ticketStore
}

// NewHub builds an empty hub with a 60s ticket store.
func NewHub() *Hub {
	return &Hub{
		subs:    make(map[string]map[*Subscriber]struct{}),
		tickets: newTicketStore(ticketTTL),
	}
}

// AddSubscriber registers a new connection for userID and returns its
// handle. The caller must RemoveSubscriber when the connection dies.
func (h *Hub) AddSubscriber(userID string) *Subscriber {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic("realtime: subscriber id rand: " + err.Error())
	}
	return &Subscriber{
		id:     "sub-" + base64.RawURLEncoding.EncodeToString(raw[:]),
		userID: userID,
		send:   make(chan Event, sendBufferSize),
	}
}

// RemoveSubscriber unregisters s from every channel. It is idempotent.
func (h *Hub) RemoveSubscriber(s *Subscriber) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch, set := range h.subs {
		if _, ok := set[s]; ok {
			delete(set, s)
			if len(set) == 0 {
				delete(h.subs, ch)
			}
		}
	}
}

// Subscribe adds s to channel.
func (h *Hub) Subscribe(s *Subscriber, channel string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	set, ok := h.subs[channel]
	if !ok {
		set = make(map[*Subscriber]struct{})
		h.subs[channel] = set
	}
	set[s] = struct{}{}
}

// Unsubscribe removes s from channel. It is idempotent.
func (h *Hub) Unsubscribe(s *Subscriber, channel string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if set, ok := h.subs[channel]; ok {
		delete(set, s)
		if len(set) == 0 {
			delete(h.subs, channel)
		}
	}
}

// Broadcast delivers eventType+data to every subscriber of each channel in
// channels. One Event is emitted per (subscriber, channel) pair so the
// payload's channel field always names the subscription that matched. Sends
// are non-blocking: a full subscriber buffer drops the event for that
// subscriber only. This method satisfies service.Broadcaster — the Hub is
// wired into the service layer by main, never the other way around.
func (h *Hub) Broadcast(channels []string, eventType string, data any) {
	now := time.Now().UTC()
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, ch := range channels {
		for s := range h.subs[ch] {
			select {
			case s.send <- Event{Event: eventType, Channel: ch, Data: data, At: now}:
			default:
				// Slow client: drop. Documented on sendBufferSize.
			}
		}
	}
}

// SendQueue exposes the subscriber's outbound queue. The api package's
// write loop drains it; the realtime package never touches the network.
func (s *Subscriber) SendQueue() <-chan Event { return s.send }

// IssueTicket mints a single-use 60s ticket for userID.
func (h *Hub) IssueTicket(userID string) string { return h.tickets.issue(userID) }

// ConsumeTicket validates and burns a ticket, returning the bound userID.
func (h *Hub) ConsumeTicket(token string) (string, bool) { return h.tickets.consume(token) }
