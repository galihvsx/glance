package api

// Calendar subscription feeds (C15T3):
//   GET /api/v1/workspaces/:slug/projects/:identifier/calendar.ics
//   GET /api/v1/workspaces/:slug/projects/:identifier/cycles/:cycleID/calendar.ics
//   GET|POST|DELETE /api/v1/me/calendar-token
//
// The two feed endpoints are deliberately NOT behind RequireAuth:
// external calendar apps subscribe by URL and cannot present the
// session cookie. They authenticate with an opaque per-user feed token
// (glcal_…) carried in the ?token= query parameter (Authorization:
// Bearer is accepted as a fallback). Feed tokens are minted/revoked via
// the /me/calendar-token management endpoints, which sit behind
// RequireSessionAuth (cookie only): a feed token must never be able to
// mint or revoke tokens — not even its own.
//
// Feed content: VEVENT per issue with a due date (target_date), SUMMARY
// "{display_id} {name}", DUE;VALUE=DATE, DTSTART;VALUE=DATE when the
// issue has a start date, URL + DESCRIPTION carrying the absolute issue
// URL. Membership gates are the service layer's (any workspace member,
// guests included); a feed token whose owner loses membership or is
// deactivated stops authenticating.

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"glance/internal/auth"
	"glance/internal/config"
	"glance/internal/service"
)

// CalendarFeedHandler serves the iCal subscription feeds and the
// feed-token management endpoints.
type CalendarFeedHandler struct {
	Pool   *pgxpool.Pool
	Config *config.Config
}

// RegisterCalendarFeedRoutes mounts the feed endpoints (token-authed,
// see package comment) and the session-only token management group.
// Call this before the SPA catch-all so API routes are never shadowed.
func RegisterCalendarFeedRoutes(e *echo.Echo, h *CalendarFeedHandler) {
	g := e.Group("/api/v1/workspaces/:slug/projects/:identifier")
	// Static ".ics" segments: Echo's specificity rules keep these clear
	// of GET /issues/:uuid and GET /cycles/:cycleID (same pattern as the
	// /issues/export route).
	g.GET("/calendar.ics", h.projectCalendar)
	g.GET("/cycles/:cycleID/calendar.ics", h.cycleCalendar)

	me := e.Group("/api/v1/me", RequireSessionAuth(h.Pool))
	me.GET("/calendar-token", h.getFeedToken)
	me.POST("/calendar-token", h.createFeedToken)
	me.DELETE("/calendar-token", h.revokeFeedToken)
}

// feedCredential extracts the feed token: the ?token= query parameter
// first (what calendar apps send), then Authorization: Bearer.
func feedCredential(c *echo.Context) string {
	if t := strings.TrimSpace(c.QueryParam("token")); t != "" {
		return t
	}
	return bearerToken(c)
}

// authenticateFeed validates the feed credential. A missing credential,
// an unknown/revoked token, or a deactivated owner all answer the same
// generic 401 (no enumeration); an exhausted per-token budget answers
// 429. Callers distinguish via the returned error.
func authenticateFeed(c *echo.Context, pool *pgxpool.Pool) (*auth.User, error) {
	token := feedCredential(c)
	if token == "" {
		return nil, auth.ErrFeedTokenInvalid
	}
	return auth.AuthenticateFeedToken(c.Request().Context(), pool, token)
}

// feedAuthError maps feed authentication failures to HTTP statuses,
// mirroring the Bearer gl_ path in serveTokenAuthed: rate limiting is
// honest (429), everything else is the generic 401.
func feedAuthError(c *echo.Context, err error) error {
	if errors.Is(err, auth.ErrTokenRateLimited) {
		return WriteError(c, http.StatusTooManyRequests, ErrCodeRateLimited, "too many requests, try again later", nil)
	}
	return WriteError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized", nil)
}

// feedBaseURL resolves the public base URL for absolute issue links in
// the feed: APP_URL wins when set (production); otherwise it is derived
// from the request (self-hosted dev) — the same discipline as the OAuth
// redirect URL.
func feedBaseURL(cfg *config.Config, c *echo.Context) string {
	base := strings.TrimSuffix(cfg.AppURL, "/")
	if base == "" {
		base = c.Scheme() + "://" + c.Request().Host
	}
	return base
}

// issueFeedURL is the absolute URL of an issue in the SPA, carried in
// the VEVENT's URL property and DESCRIPTION.
func issueFeedURL(base, slug, identifier, issueID string) string {
	return base + "/w/" + url.PathEscape(slug) + "/p/" + url.PathEscape(identifier) +
		"/i/" + url.PathEscape(issueID)
}

// projectCalendar implements GET .../calendar.ics: the project's dated
// issues as a text/calendar feed.
func (h *CalendarFeedHandler) projectCalendar(c *echo.Context) error {
	user, err := authenticateFeed(c, h.Pool)
	if err != nil {
		return feedAuthError(c, err)
	}
	ctx := c.Request().Context()
	issues, scope, err := service.FeedIssues(ctx, h.Pool,
		c.Param("slug"), c.Param("identifier"), user.ID, "")
	if err != nil {
		return feedError(c, err)
	}
	return h.writeFeed(c, scope.CalName, c.Param("slug"), c.Param("identifier"), issues)
}

// cycleCalendar implements GET .../cycles/:cycleID/calendar.ics: the
// cycle's dated issues as a text/calendar feed.
func (h *CalendarFeedHandler) cycleCalendar(c *echo.Context) error {
	user, err := authenticateFeed(c, h.Pool)
	if err != nil {
		return feedAuthError(c, err)
	}
	ctx := c.Request().Context()
	issues, scope, err := service.FeedIssues(ctx, h.Pool,
		c.Param("slug"), c.Param("identifier"), user.ID, c.Param("cycleID"))
	if err != nil {
		return feedError(c, err)
	}
	return h.writeFeed(c, scope.CalName, c.Param("slug"), c.Param("identifier"), issues)
}

// feedError maps feed service errors to HTTP statuses. Cycle-scoped
// errors go through cycleError (which falls back to issueError for the
// shared sentinels), so the cycle feed answers exactly what the cycle
// endpoints would; the row cap is an honest 400 naming the matched
// count.
func feedError(c *echo.Context, err error) error {
	switch {
	case errors.Is(err, service.ErrCycleNotFound),
		errors.Is(err, service.ErrCycleConflict),
		errors.Is(err, service.ErrInvalidCycle),
		errors.Is(err, service.ErrInvalidCycleID):
		return cycleError(c, err)
	case errors.Is(err, service.ErrFeedTooLarge):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, err.Error(), nil)
	default:
		return issueError(c, err)
	}
}

// writeFeed renders the VCALENDAR body and answers 200 text/calendar.
// The whole body is built in memory first (bounded by MaxFeedRows), so a
// render failure never leaves a truncated 200 on the wire. No
// Content-Disposition: calendar apps subscribe to the URL directly.
func (h *CalendarFeedHandler) writeFeed(c *echo.Context, calName, slug, identifier string, issues []*service.FeedIssue) error {
	base := feedBaseURL(h.Config, c)
	stamp := time.Now().UTC().Format("20060102T150405Z")
	var b strings.Builder
	writeICalLine(&b, "BEGIN:VCALENDAR")
	writeICalLine(&b, "VERSION:2.0")
	writeICalLine(&b, "PRODID:-//glance//Calendar Feed//EN")
	writeICalLine(&b, "CALSCALE:GREGORIAN")
	writeICalLine(&b, "X-WR-CALNAME:"+escapeICalText(calName))
	for _, issue := range issues {
		issueURL := issueFeedURL(base, slug, identifier, issue.ID)
		writeICalLine(&b, "BEGIN:VEVENT")
		writeICalLine(&b, "UID:"+issue.ID+"@glance")
		writeICalLine(&b, "DTSTAMP:"+stamp)
		writeICalLine(&b, "SUMMARY:"+escapeICalText(issue.DisplayID+" "+issue.Name))
		if issue.StartDate != nil {
			writeICalLine(&b, "DTSTART;VALUE=DATE:"+icalDate(*issue.StartDate))
		}
		writeICalLine(&b, "DUE;VALUE=DATE:"+icalDate(issue.TargetDate))
		writeICalLine(&b, "URL:"+escapeICalText(issueURL))
		desc := service.TiptapPlainText(issue.Description)
		if desc != "" {
			desc += "\n"
		}
		desc += issueURL
		writeICalLine(&b, "DESCRIPTION:"+escapeICalText(desc))
		writeICalLine(&b, "END:VEVENT")
	}
	writeICalLine(&b, "END:VCALENDAR")
	return c.Blob(http.StatusOK, "text/calendar; charset=utf-8", []byte(b.String()))
}

// icalDate normalizes a YYYY-MM-DD date to the iCalendar DATE form
// (YYYYMMDD). Malformed input passes through rather than failing the
// whole feed — the service layer only ever stores valid dates.
func icalDate(yyyyMmDd string) string {
	return strings.ReplaceAll(yyyyMmDd, "-", "")
}

// escapeICalText escapes a TEXT property value per RFC 5545 §3.3.11:
// backslash, semicolon, comma, and newlines. Double quotes need no
// escaping in TEXT values.
func escapeICalText(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, ";", "\\;")
	s = strings.ReplaceAll(s, ",", "\\,")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.ReplaceAll(s, "\n", "\\n")
	return s
}

// writeICalLine appends one content line with CRLF termination, folding
// at 75 octets per RFC 5545 §3.1 (continuation lines start with a single
// space). Folds never split a UTF-8 rune.
func writeICalLine(b *strings.Builder, line string) {
	const maxOctets = 75
	octets := 0
	for i := 0; i < len(line); {
		// Start a continuation line when the next rune would overflow.
		_, size := utf8.DecodeRuneInString(line[i:])
		if octets+size > maxOctets && octets > 0 {
			b.WriteString("\r\n ")
			octets = 1 // the continuation space counts
		}
		b.WriteString(line[i : i+size])
		octets += size
		i += size
	}
	b.WriteString("\r\n")
}

// feedTokenStatusBody is the GET /api/v1/me/calendar-token response:
// timestamps of the live token, or active:false. Never the secret.
type feedTokenStatusBody struct {
	Active     bool       `json:"active"`
	CreatedAt  *time.Time `json:"created_at,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

// getFeedToken implements GET /api/v1/me/calendar-token: whether the
// caller has a live feed token (and its timestamps). 200 with
// active:false when there is none — no 404, the frontend polls this to
// decide the button label.
func (h *CalendarFeedHandler) getFeedToken(c *echo.Context) error {
	user := CurrentUser(c)
	if user == nil {
		return WriteError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized", nil)
	}
	status, err := auth.GetFeedTokenStatus(c.Request().Context(), h.Pool, user.ID)
	if err != nil {
		return WriteInternalError(c)
	}
	return c.JSON(http.StatusOK, feedTokenStatusBody{
		Active:     status.Active,
		CreatedAt:  status.CreatedAt,
		LastUsedAt: status.LastUsedAt,
	})
}

// createFeedToken implements POST /api/v1/me/calendar-token: mint (or
// regenerate — the previous live token is revoked) the caller's feed
// token. The plaintext is returned exactly once in the 201 body — there
// is no endpoint that reveals it again.
func (h *CalendarFeedHandler) createFeedToken(c *echo.Context) error {
	user := CurrentUser(c)
	if user == nil {
		return WriteError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized", nil)
	}
	created, err := auth.CreateFeedToken(c.Request().Context(), h.Pool, user.ID)
	if err != nil {
		return WriteInternalError(c)
	}
	return c.JSON(http.StatusCreated, created)
}

// revokeFeedToken implements DELETE /api/v1/me/calendar-token: revoke
// the caller's live feed token. No live token → 404 (never distinguishing
// absence from foreign ownership — there is no id to distinguish by).
func (h *CalendarFeedHandler) revokeFeedToken(c *echo.Context) error {
	user := CurrentUser(c)
	if user == nil {
		return WriteError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized", nil)
	}
	ok, err := auth.RevokeFeedToken(c.Request().Context(), h.Pool, user.ID)
	if err != nil {
		return WriteInternalError(c)
	}
	if !ok {
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "token not found", nil)
	}
	return c.NoContent(http.StatusNoContent)
}
