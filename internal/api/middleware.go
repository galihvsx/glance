package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"glance/internal/auth"
)

// authContextKey is the key type for auth values in the request context. A
// distinct unexported type — never a bare string — so no other package can
// collide with these keys.
type authContextKey int

const (
	authUserKey authContextKey = iota
	authSessionIDKey
	authTokenKey
)

// RequireAuth is the authentication middleware every protected endpoint
// sits behind (Phase 2+). It checks Authorization: Bearer FIRST: a value
// carrying the gl_ prefix goes through token auth (Task 29) — unknown,
// revoked, or expired tokens answer 401 with no cookie fallback. Any
// other Bearer value is ignored (not honored), and authentication falls
// back to the glance_session cookie validated via auth.AuthenticateSession
// (unknown/revoked/expired → 401).
//
// Requests authenticated by token are additionally scope-checked,
// centrally here rather than per handler: mutating methods (POST/PUT/
// PATCH/DELETE) require the write scope → 403; anything else requires
// read (which write implies) → 403. Session-authenticated requests are
// unaffected by scopes.
//
// It injects the *auth.User into the request context under a typed key
// (plus the session ID for cookie auth, or the *auth.TokenAuth for token
// auth). Failures answer 401/403 JSON, never a redirect: this is an API,
// and the SPA decides where to send the user.
func RequireAuth(pool *pgxpool.Pool) echo.MiddlewareFunc {
	return authenticate(pool, true)
}

// RequireSessionAuth is the cookie-only variant for endpoints a token
// must never reach — token management (POST/GET/DELETE /api/v1/tokens):
// a token must not be able to mint new tokens. Bearer values are ignored
// entirely here; only the glance_session cookie authenticates.
func RequireSessionAuth(pool *pgxpool.Pool) echo.MiddlewareFunc {
	return authenticate(pool, false)
}

func authenticate(pool *pgxpool.Pool, allowToken bool) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			ctx := c.Request().Context()

			if allowToken {
				if raw := bearerToken(c); raw != "" {
					if strings.HasPrefix(raw, auth.TokenPrefix) {
						return serveTokenAuthed(c, next, pool, raw)
					}
					// Non-glance Bearer: ignored, not honored — fall
					// through to cookie auth.
				}
			}

			cookie, err := c.Cookie(auth.SessionCookieName)
			if err != nil || cookie.Value == "" {
				return WriteError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized", nil)
			}
			result, err := auth.AuthenticateSession(ctx, pool, cookie.Value)
			if err != nil {
				return WriteError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized", nil)
			}
			ctx = context.WithValue(ctx, authUserKey, result.User)
			ctx = context.WithValue(ctx, authSessionIDKey, result.SessionID)
			c.SetRequest(c.Request().WithContext(ctx))
			return next(c)
		}
	}
}

// serveTokenAuthed completes the Bearer gl_… path: authenticate, enforce
// the per-token rate limit and scope, then inject user + token auth.
func serveTokenAuthed(c *echo.Context, next echo.HandlerFunc, pool *pgxpool.Pool, raw string) error {
	tokenAuth, err := auth.AuthenticateToken(c.Request().Context(), pool, raw)
	if err != nil {
		if errors.Is(err, auth.ErrTokenRateLimited) {
			return WriteError(c, http.StatusTooManyRequests, ErrCodeRateLimited, "too many requests, try again later", nil)
		}
		return WriteError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized", nil)
	}
	if isMutating(c.Request().Method) {
		if !tokenAuth.HasScope(auth.ScopeWrite) {
			return WriteError(c, http.StatusForbidden, ErrCodeForbidden, "forbidden", nil)
		}
	} else if !tokenAuth.HasScope(auth.ScopeRead) {
		return WriteError(c, http.StatusForbidden, ErrCodeForbidden, "forbidden", nil)
	}
	ctx := context.WithValue(c.Request().Context(), authUserKey, tokenAuth.User)
	ctx = context.WithValue(ctx, authTokenKey, tokenAuth)
	c.SetRequest(c.Request().WithContext(ctx))
	return next(c)
}

// bearerToken extracts the Bearer credential from the Authorization
// header, or "" when the header is absent or uses another scheme. The
// scheme match is case-insensitive per RFC 7235.
func bearerToken(c *echo.Context) string {
	h := c.Request().Header.Get("Authorization")
	parts := strings.SplitN(h, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

// isMutating reports whether the HTTP method changes server state — the
// set of methods that require the write scope on token-authed requests.
func isMutating(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

// CurrentUser returns the *auth.User injected by RequireAuth, or nil when
// the request did not pass through the middleware.
func CurrentUser(c *echo.Context) *auth.User {
	if u, ok := c.Request().Context().Value(authUserKey).(*auth.User); ok {
		return u
	}
	return nil
}

// CurrentSessionID returns the session ID injected by RequireAuth, or ""
// when the request did not pass through the middleware (including
// token-authed requests, which have no session).
func CurrentSessionID(c *echo.Context) string {
	if id, ok := c.Request().Context().Value(authSessionIDKey).(string); ok {
		return id
	}
	return ""
}

// CurrentTokenAuth returns the *auth.TokenAuth injected for Bearer-authenticated
// requests, or nil for session-authenticated requests and unauthenticated ones.
func CurrentTokenAuth(c *echo.Context) *auth.TokenAuth {
	if t, ok := c.Request().Context().Value(authTokenKey).(*auth.TokenAuth); ok {
		return t
	}
	return nil
}
