// Package realtime is the in-process websocket hub (spec §6): /ws accepts
// the session cookie or a single-use ticket, clients subscribe to channels,
// and service-layer mutations fan events out to the right channels.
//
// Wire protocol:
//   - client → server: {"action":"subscribe"|"unsubscribe","channel":"..."}
//   - server → client: {"action":"subscribed"|"unsubscribed","channel":"..."}
//     or {"action":"error","message":"..."}
//   - server → client events: {"event":"issue.created","channel":"project:acme:ENG",
//     "data":{...},"at":"2026-10-06T..."}. data is the minimal resource JSON,
//     never the full object — the client refetches via REST.
//
// Dependency direction (ruling R1): this package never imports
// glance/internal/service. The service package owns the Broadcaster
// interface and the event vocabulary; main wires a *Hub into it.
package realtime

import "time"

// Channel prefixes. Full channel names: workspace:{slug},
// project:{slug}:{identifier} (workspace-qualified, spec §6 R11),
// issue:{uuid}, user:{id}.
const (
	ChannelWorkspace = "workspace"
	ChannelProject   = "project"
	ChannelIssue     = "issue"
	ChannelUser      = "user"
)

// Event is the wire payload for a broadcast (spec §6: {event, channel,
// data, at}). Channel names which subscription the event matched — a client
// subscribed to both issue:{uuid} and project:{slug}:{identifier} receives one
// Event per matching channel.
type Event struct {
	Event   string    `json:"event"`
	Channel string    `json:"channel"`
	Data    any       `json:"data"`
	At      time.Time `json:"at"`
}

// ClientMessage is a client → server control frame.
type ClientMessage struct {
	Action  string `json:"action"`
	Channel string `json:"channel"`
}

// Client actions.
const (
	ActionSubscribe   = "subscribe"
	ActionUnsubscribe = "unsubscribe"
)

// ControlMessage is a server → client control frame (acks and errors).
type ControlMessage struct {
	Action  string `json:"action"`
	Channel string `json:"channel,omitempty"`
	Message string `json:"message,omitempty"`
}

// Server → client control actions.
const (
	ActionSubscribed   = "subscribed"
	ActionUnsubscribed = "unsubscribed"
	ActionError        = "error"
)
