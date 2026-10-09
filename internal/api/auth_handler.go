// Package api serves glance's HTTP API.
package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"glance/internal/auth"
	"glance/internal/config"
)

// AuthHandler serves the passwordless auth endpoints (spec §7).
type AuthHandler struct {
	Pool   *pgxpool.Pool
	Config *config.Config
}

// RegisterAuthRoutes mounts the auth endpoints under /api/v1/auth, plus
// GET /api/v1/me. Call this before the SPA catch-all so API routes are
// never shadowed. Session endpoints (/me, /logout, /sessions*) sit behind
// RequireAuth; the login endpoints must stay public.
func RegisterAuthRoutes(e *echo.Echo, h *AuthHandler) {
	g := e.Group("/api/v1/auth")
	g.POST("/otp/request", h.requestOTP)
	g.POST("/otp/verify", h.verifyOTP)
	g.GET("/oauth/:provider/login", h.oauthLogin)
	g.GET("/oauth/:provider/callback", h.oauthCallback)
	g.POST("/logout", h.logout, RequireAuth(h.Pool))
	g.GET("/sessions", h.listSessions, RequireAuth(h.Pool))
	g.DELETE("/sessions/:id", h.deleteSession, RequireAuth(h.Pool))
	g.PATCH("/me", h.updateMe, RequireAuth(h.Pool))

	e.Group("/api/v1").GET("/me", h.me, RequireAuth(h.Pool))
}

// oauthStateCookieName holds the HMAC-signed OAuth state between the login
// redirect and the callback. Scoped to the OAuth endpoints, single-use
// (cleared on callback), 10-minute life.
const oauthStateCookieName = "glance_oauth_state"

// SetSessionCookie sets the glance_session cookie. Shared by every login
// path so OTP and OAuth can never drift apart on cookie attributes.
// Attributes live in the auth package (auth.SessionCookieName/MaxAge) —
// this resolves the Task 7/8 deferred note by keeping them in one place.
func SetSessionCookie(c *echo.Context, token string) {
	c.SetCookie(&http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   auth.SessionCookieMaxAge,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

// ClearSessionCookie expires the session cookie with the same attributes
// it was set with, so the browser reliably drops it on logout and on
// self-revocation of the current session.
func ClearSessionCookie(c *echo.Context) {
	c.SetCookie(&http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

type otpRequestBody struct {
	Email string `json:"email"`
}

// requestOTP implements POST /api/v1/auth/otp/request. It ALWAYS answers
// 200 {"ok":true} — even for unknown or malformed emails — so the endpoint
// never reveals whether an address is registered (no enumeration). The only
// other outcomes are 400 (unparseable body) and 429 (rate limited).
func (h *AuthHandler) requestOTP(c *echo.Context) error {
	var body otpRequestBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	if err := auth.RequestOTP(c.Request().Context(), h.Pool, h.Config, body.Email, c.RealIP()); err != nil {
		if errors.Is(err, auth.ErrRateLimited) {
			return WriteError(c, http.StatusTooManyRequests, ErrCodeRateLimited, "too many requests, try again later", nil)
		}
		return WriteInternalError(c)
	}
	return c.JSON(http.StatusOK, map[string]bool{"ok": true})
}

type otpVerifyBody struct {
	Email string `json:"email"`
	Code  string `json:"code"`
}

// verifyOTP implements POST /api/v1/auth/otp/verify. Every failure —
// unknown email, expired/consumed/burned code, wrong code — answers 401
// with the same generic message (no enumeration). Success sets the
// session cookie and answers 200 {"ok":true}.
func (h *AuthHandler) verifyOTP(c *echo.Context) error {
	var body otpVerifyBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	token, err := auth.VerifyOTP(
		c.Request().Context(), h.Pool, h.Config,
		body.Email, body.Code, c.Request().UserAgent(), c.RealIP(),
	)
	if err != nil {
		if errors.Is(err, auth.ErrInvalidCode) {
			return WriteError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "invalid or expired code", nil)
		}
		if errors.Is(err, auth.ErrRateLimited) {
			return WriteError(c, http.StatusTooManyRequests, ErrCodeRateLimited, "too many requests, try again later", nil)
		}
		if errors.Is(err, auth.ErrUserDeactivated) {
			return WriteError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "account is deactivated", nil)
		}
		return WriteInternalError(c)
	}
	SetSessionCookie(c, token)
	return c.JSON(http.StatusOK, map[string]bool{"ok": true})
}

// oauthRedirectURL is the redirect_uri registered with the provider for this
// login attempt. APP_URL wins when set (production); otherwise it is derived
// from the request (self-hosted dev). The SAME value is used for the login
// redirect and the callback's token exchange — the provider enforces the
// match.
func oauthRedirectURL(cfg *config.Config, c *echo.Context, p auth.Provider) string {
	base := strings.TrimSuffix(cfg.AppURL, "/")
	if base == "" {
		base = c.Scheme() + "://" + c.Request().Host
	}
	return base + "/api/v1/auth/oauth/" + string(p) + "/callback"
}

// oauthLogin implements GET /api/v1/auth/oauth/{google|github}/login: 302 to
// the provider with an unguessable state. Unknown providers and providers
// without credentials answer 404 (not 500).
func (h *AuthHandler) oauthLogin(c *echo.Context) error {
	provider, ok := auth.ParseProvider(c.Param("provider"))
	if !ok {
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "unknown oauth provider", nil)
	}
	// Unconfigured provider → 404 before any state or crypto work.
	if !auth.ProviderConfigured(h.Config, provider) {
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "oauth provider not configured", nil)
	}
	redirectURL := oauthRedirectURL(h.Config, c, provider)
	raw, signed, err := auth.NewOAuthState(h.Config)
	if err != nil {
		// OAUTH_STATE_SECRET empty (or RNG failure): fail fast, no redirect.
		return WriteInternalError(c)
	}
	authURL, err := auth.AuthorizationURL(h.Config, provider, redirectURL, raw)
	if err != nil {
		if errors.Is(err, auth.ErrOAuthNotConfigured) {
			return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "oauth provider not configured", nil)
		}
		return WriteInternalError(c)
	}
	c.SetCookie(&http.Cookie{
		Name:     oauthStateCookieName,
		Value:    signed,
		Path:     "/api/v1/auth/oauth",
		MaxAge:   600,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
	return c.Redirect(http.StatusFound, authURL)
}

// clearOAuthStateCookie consumes the single-use state cookie: it is deleted
// on every callback attempt, valid or not, so a captured state value can
// never be replayed.
func clearOAuthStateCookie(c *echo.Context) {
	c.SetCookie(&http.Cookie{
		Name:     oauthStateCookieName,
		Value:    "",
		Path:     "/api/v1/auth/oauth",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

// oauthCallback implements GET /api/v1/auth/oauth/{google|github}/callback:
// validates the single-use state, completes the login, sets the SAME session
// cookie as OTP, and redirects to the SPA.
func (h *AuthHandler) oauthCallback(c *echo.Context) error {
	provider, ok := auth.ParseProvider(c.Param("provider"))
	if !ok {
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "unknown oauth provider", nil)
	}
	// Unconfigured provider → 404 before touching state or the DB.
	if !auth.ProviderConfigured(h.Config, provider) {
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "oauth provider not configured", nil)
	}
	cookie, cookieErr := c.Cookie(oauthStateCookieName)
	clearOAuthStateCookie(c)
	if cookieErr != nil || !auth.VerifyOAuthState(h.Config, cookie.Value, c.QueryParam("state")) {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid oauth state", nil)
	}
	code := c.QueryParam("code")
	if code == "" {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "missing oauth code", nil)
	}
	token, err := auth.CompleteOAuthLogin(
		c.Request().Context(), h.Pool, h.Config,
		provider, code, oauthRedirectURL(h.Config, c, provider),
		c.Request().UserAgent(), c.RealIP(),
	)
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrOAuthNotConfigured):
			return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "oauth provider not configured", nil)
		case errors.Is(err, auth.ErrOAuthEmailUnverified):
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "oauth provider did not return a verified email", nil)
		case errors.Is(err, auth.ErrOAuthAlreadyLinked):
			return WriteError(c, http.StatusConflict, ErrCodeConflict, "oauth account is already linked to another user", nil)
		case errors.Is(err, auth.ErrUserDeactivated):
			return WriteError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "account is deactivated", nil)
		default:
			return WriteInternalError(c)
		}
	}
	SetSessionCookie(c, token)
	return c.Redirect(http.StatusFound, "/")
}

// me implements GET /api/v1/me: the current user as JSON, straight from
// the *auth.User the RequireAuth middleware injected. Cookie auth only.
func (h *AuthHandler) me(c *echo.Context) error {
	return c.JSON(http.StatusOK, CurrentUser(c))
}

type updateMeBody struct {
	Name string `json:"name"`
}

// updateMe implements PATCH /api/v1/auth/me: the user updates their own
// display name. Email is identity (OTP/OAuth) and is never editable here.
func (h *AuthHandler) updateMe(c *echo.Context) error {
	u := CurrentUser(c)
	if u == nil {
		return WriteError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized", nil)
	}
	var b updateMeBody
	if err := c.Bind(&b); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	updated, err := auth.UpdateUserName(c.Request().Context(), h.Pool, u.ID, b.Name)
	if err != nil {
		if errors.Is(err, auth.ErrInvalidName) {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest,
				"name is required and must be at most 100 characters", nil)
		}
		return WriteInternalError(c)
	}
	return c.JSON(http.StatusOK, updated)
}

// logout implements POST /api/v1/auth/logout: revokes the current session
// row and clears the cookie. It requires auth — there is no session to
// log out without one.
func (h *AuthHandler) logout(c *echo.Context) error {
	if err := auth.RevokeSession(c.Request().Context(), h.Pool, CurrentSessionID(c)); err != nil {
		return WriteInternalError(c)
	}
	ClearSessionCookie(c)
	return c.JSON(http.StatusOK, map[string]bool{"ok": true})
}

// listSessions implements GET /api/v1/auth/sessions: the user's active
// sessions for the settings page. Never includes token hashes. Each row
// carries a `current` flag so the UI can badge the caller's own session
// (the session id lives in an HttpOnly cookie — the client cannot tell).
func (h *AuthHandler) listSessions(c *echo.Context) error {
	sessions, err := auth.ListSessions(c.Request().Context(), h.Pool, CurrentUser(c).ID)
	if err != nil {
		return WriteInternalError(c)
	}
	currentID := CurrentSessionID(c)
	views := make([]sessionView, 0, len(sessions))
	for _, s := range sessions {
		views = append(views, sessionView{
			SessionInfo: s,
			Current:     currentID != "" && strings.EqualFold(s.ID, currentID),
		})
	}
	return c.JSON(http.StatusOK, map[string]any{"sessions": views})
}

// sessionView is auth.SessionInfo plus the caller's current-session flag.
type sessionView struct {
	auth.SessionInfo
	Current bool `json:"current"`
}

// deleteSession implements DELETE /api/v1/auth/sessions/{id}: revokes one
// of the user's sessions. A session that does not exist, is already
// revoked, belongs to another user, or is malformed all answer 404 — the
// endpoint never reveals which case hit. Revoking the current session also
// clears its cookie (it is a logout by another name).
func (h *AuthHandler) deleteSession(c *echo.Context) error {
	id := c.Param("id")
	if !validUUID(id) {
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "session not found", nil)
	}
	revoked, err := auth.RevokeSessionForUser(c.Request().Context(), h.Pool, CurrentUser(c).ID, id)
	if err != nil {
		return WriteInternalError(c)
	}
	if !revoked {
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "session not found", nil)
	}
	// EqualFold, not ==: UUIDs are case-insensitive, and the client may echo
	// the id back in a different case than Postgres stored.
	if strings.EqualFold(id, CurrentSessionID(c)) {
		ClearSessionCookie(c)
	}
	return c.JSON(http.StatusOK, map[string]bool{"ok": true})
}
