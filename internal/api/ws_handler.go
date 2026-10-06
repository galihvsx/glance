package api

import (
	"context"
	"net/http"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"

	"glance/internal/auth"
	"glance/internal/realtime"
)

// wsWriter serializes all writes on one connection. nhooyr permits a
// single concurrent writer; the event pump (write loop) and the control
// acks (read loop) would otherwise race and corrupt frames.
type wsWriter struct {
	mu   sync.Mutex
	conn *websocket.Conn
}

func (w *wsWriter) write(ctx context.Context, v any) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return wsjson.Write(ctx, w.conn, v)
}

// WSHandler serves the realtime websocket hub (spec §6, Task 24).
type WSHandler struct {
	Pool *pgxpool.Pool
	Hub  *realtime.Hub
}

// RegisterWSRoutes mounts the websocket endpoint and the ticket endpoint.
// /ws does its own auth (cookie OR single-use ticket) so it is not behind
// RequireAuth; the ticket endpoint is.
func RegisterWSRoutes(e *echo.Echo, h *WSHandler) {
	e.GET("/ws", h.handleWS)
	e.POST("/api/v1/ws/ticket", h.issueTicket, RequireAuth(h.Pool))
}

// issueTicket mints a single-use 60s ticket for the authenticated user
// (spec §6). Token clients connect with /ws?ticket=.
func (h *WSHandler) issueTicket(c *echo.Context) error {
	u := CurrentUser(c)
	if u == nil {
		return WriteError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized", nil)
	}
	return c.JSON(http.StatusOK, map[string]string{"ticket": h.Hub.IssueTicket(u.ID)})
}

// authenticateWS resolves the connecting user: a ?ticket= is consumed
// (single-use) when present, otherwise the session cookie is validated
// exactly like the HTTP auth middleware does.
func (h *WSHandler) authenticateWS(c *echo.Context) (string, error) {
	if ticket := c.QueryParam("ticket"); ticket != "" {
		if userID, ok := h.Hub.ConsumeTicket(ticket); ok {
			return userID, nil
		}
		return "", errWSTicketInvalid
	}
	cookie, err := c.Cookie(auth.SessionCookieName)
	if err != nil || cookie.Value == "" {
		return "", errWSUnauthorized
	}
	result, err := auth.AuthenticateSession(c.Request().Context(), h.Pool, cookie.Value)
	if err != nil {
		return "", errWSUnauthorized
	}
	return result.User.ID, nil
}

// handleWS upgrades the connection and pumps messages. Auth failures answer
// 401 before the upgrade; channel-level failures answer an error control
// frame on the open connection (Review Focus #5).
func (h *WSHandler) handleWS(c *echo.Context) error {
	userID, err := h.authenticateWS(c)
	if err != nil {
		return WriteError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized", nil)
	}

	// nhooyr's default origin check: absent Origin is allowed (non-browser
	// clients); a present Origin must match the request Host. That is the
	// CSRF protection for cookie-authenticated browser clients — no
	// OriginPatterns override.
	conn, err := websocket.Accept(c.Response(), c.Request(), nil)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(c.Request().Context())
	defer cancel()

	sub := h.Hub.AddSubscriber(userID)
	defer h.Hub.RemoveSubscriber(sub)
	w := &wsWriter{conn: conn}

	// Write loop: drains the subscriber queue. The read loop below is the
	// single reader; all writes funnel through w (nhooyr: one writer).
	writeDone := make(chan struct{})
	go func() {
		defer close(writeDone)
		for {
			select {
			case evt := <-sub.SendQueue():
				if err := w.write(ctx, evt); err != nil {
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	// Read loop: the single reader. Any read error (client gone, protocol
	// violation) cancels the context, which stops the write loop.
	for {
		var msg realtime.ClientMessage
		if err := wsjson.Read(ctx, conn, &msg); err != nil {
			break
		}
		h.handleClientMessage(ctx, sub, msg, w)
	}

	cancel()
	<-writeDone
	conn.Close(websocket.StatusNormalClosure, "")
	return nil
}

// handleClientMessage routes one client control frame.
func (h *WSHandler) handleClientMessage(ctx context.Context, sub *realtime.Subscriber, msg realtime.ClientMessage, w *wsWriter) {
	write := func(v realtime.ControlMessage) {
		// Best-effort: a failed control write will surface on the next
		// read anyway when the connection is truly dead.
		_ = w.write(ctx, v)
	}
	switch msg.Action {
	case realtime.ActionSubscribe:
		if err := h.authorizeChannel(ctx, sub.UserID(), msg.Channel); err != nil {
			write(realtime.ControlMessage{Action: realtime.ActionError, Channel: msg.Channel, Message: err.Error()})
			return
		}
		h.Hub.Subscribe(sub, msg.Channel)
		write(realtime.ControlMessage{Action: realtime.ActionSubscribed, Channel: msg.Channel})
	case realtime.ActionUnsubscribe:
		h.Hub.Unsubscribe(sub, msg.Channel)
		write(realtime.ControlMessage{Action: realtime.ActionUnsubscribed, Channel: msg.Channel})
	default:
		write(realtime.ControlMessage{Action: realtime.ActionError, Message: "unknown action"})
	}
}

// authorizeChannel enforces channel-level auth (Review Focus #5): a
// subscriber only joins channels whose resource they can see.
// user:{id} requires the id to be the subscriber's own.
func (h *WSHandler) authorizeChannel(ctx context.Context, userID, channel string) error {
	prefix, rest, ok := strings.Cut(channel, ":")
	if !ok || rest == "" {
		return errWSBadChannel
	}
	switch prefix {
	case realtime.ChannelUser:
		if rest != userID {
			return errWSForbidden
		}
		return nil
	case realtime.ChannelWorkspace:
		var one int
		err := h.Pool.QueryRow(ctx,
			`SELECT 1 FROM workspace_members m
			  JOIN workspaces w ON w.id = m.workspace_id
			 WHERE w.slug = $1 AND m.user_id = $2::uuid`, rest, userID).Scan(&one)
		return authorizeErr(err)
	case realtime.ChannelProject:
		// Amended spec §6 (R11): project channels are workspace-qualified,
		// project:{slug}:{identifier}. Identifiers are unique per workspace
		// only, so the bare project:ENG scheme would leak events across
		// workspaces sharing an identifier. Slugs and identifiers cannot
		// contain colons, so anything but exactly two parts after the
		// prefix is malformed.
		slug, ident, ok := strings.Cut(rest, ":")
		if !ok || slug == "" || ident == "" || strings.Contains(ident, ":") {
			return errWSBadChannel
		}
		var one int
		err := h.Pool.QueryRow(ctx,
			`SELECT 1 FROM workspace_members m
			  JOIN workspaces w ON w.id = m.workspace_id
			  JOIN projects p ON p.workspace_id = w.id
			 WHERE w.slug = $1 AND UPPER(p.identifier) = UPPER($2) AND m.user_id = $3::uuid`, slug, ident, userID).Scan(&one)
		return authorizeErr(err)
	case realtime.ChannelIssue:
		var one int
		err := h.Pool.QueryRow(ctx,
			`SELECT 1 FROM workspace_members m
			  JOIN workspaces w ON w.id = m.workspace_id
			  JOIN projects p ON p.workspace_id = w.id
			  JOIN issues i ON i.project_id = p.id
			 WHERE i.id = $1::uuid AND m.user_id = $2::uuid`, rest, userID).Scan(&one)
		return authorizeErr(err)
	default:
		return errWSBadChannel
	}
}
