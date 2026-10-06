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

// RegisterAuthRoutes mounts the auth endpoints under /api/v1/auth. Call this
// before the SPA catch-all so API routes are never shadowed.
func RegisterAuthRoutes(e *echo.Echo, h *AuthHandler) {
	g := e.Group("/api/v1/auth")
	g.POST("/otp/request", h.requestOTP)
	g.POST("/otp/verify", h.verifyOTP)
	g.GET("/oauth/:provider/login", h.oauthLogin)
	g.GET("/oauth/:provider/callback", h.oauthCallback)
}

// Session cookie attributes for the token returned by every login path
// (OTP verify, OAuth callback). Spec §7: HttpOnly, Secure, SameSite=Lax;
// 30-day expiry.
const (
	sessionCookieName   = "glance_session"
	sessionCookieMaxAge = 30 * 24 * 3600 // 2592000
)

// oauthStateCookieName holds the HMAC-signed OAuth state between the login
// redirect and the callback. Scoped to the OAuth endpoints, single-use
// (cleared on callback), 10-minute life.
const oauthStateCookieName = "glance_oauth_state"

// SetSessionCookie sets the glance_session cookie. Shared by every login
// path so OTP and OAuth can never drift apart on cookie attributes.
func SetSessionCookie(c *echo.Context, token string) {
	c.SetCookie(&http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   sessionCookieMaxAge,
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
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}
	if err := auth.RequestOTP(c.Request().Context(), h.Pool, h.Config, body.Email, c.RealIP()); err != nil {
		if errors.Is(err, auth.ErrRateLimited) {
			return c.JSON(http.StatusTooManyRequests, map[string]string{"error": "too many requests, try again later"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
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
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}
	token, err := auth.VerifyOTP(
		c.Request().Context(), h.Pool, h.Config,
		body.Email, body.Code, c.Request().UserAgent(), c.RealIP(),
	)
	if err != nil {
		if errors.Is(err, auth.ErrInvalidCode) {
			return c.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid or expired code"})
		}
		if errors.Is(err, auth.ErrRateLimited) {
			return c.JSON(http.StatusTooManyRequests, map[string]string{"error": "too many requests, try again later"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
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
		return c.JSON(http.StatusNotFound, map[string]string{"error": "unknown oauth provider"})
	}
	// Unconfigured provider → 404 before any state or crypto work.
	if !auth.ProviderConfigured(h.Config, provider) {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "oauth provider not configured"})
	}
	redirectURL := oauthRedirectURL(h.Config, c, provider)
	raw, signed, err := auth.NewOAuthState(h.Config)
	if err != nil {
		// OAUTH_STATE_SECRET empty (or RNG failure): fail fast, no redirect.
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "oauth not configured"})
	}
	authURL, err := auth.AuthorizationURL(h.Config, provider, redirectURL, raw)
	if err != nil {
		if errors.Is(err, auth.ErrOAuthNotConfigured) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "oauth provider not configured"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
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
		return c.JSON(http.StatusNotFound, map[string]string{"error": "unknown oauth provider"})
	}
	// Unconfigured provider → 404 before touching state or the DB.
	if !auth.ProviderConfigured(h.Config, provider) {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "oauth provider not configured"})
	}
	cookie, cookieErr := c.Cookie(oauthStateCookieName)
	clearOAuthStateCookie(c)
	if cookieErr != nil || !auth.VerifyOAuthState(h.Config, cookie.Value, c.QueryParam("state")) {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid oauth state"})
	}
	code := c.QueryParam("code")
	if code == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "missing oauth code"})
	}
	token, err := auth.CompleteOAuthLogin(
		c.Request().Context(), h.Pool, h.Config,
		provider, code, oauthRedirectURL(h.Config, c, provider),
		c.Request().UserAgent(), c.RealIP(),
	)
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrOAuthNotConfigured):
			return c.JSON(http.StatusNotFound, map[string]string{"error": "oauth provider not configured"})
		case errors.Is(err, auth.ErrOAuthEmailUnverified):
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "oauth provider did not return a verified email"})
		case errors.Is(err, auth.ErrOAuthAlreadyLinked):
			return c.JSON(http.StatusConflict, map[string]string{"error": "oauth account is already linked to another user"})
		default:
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
		}
	}
	SetSessionCookie(c, token)
	return c.Redirect(http.StatusFound, "/")
}
