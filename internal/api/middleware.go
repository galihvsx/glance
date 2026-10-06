package api

import (
	"context"
	"net/http"

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
)

// RequireAuth is the authentication middleware every protected endpoint
// sits behind (Phase 2+). It reads the glance_session cookie, validates it
// via auth.AuthenticateSession (unknown/revoked/expired → 401), and injects
// the *auth.User and the session ID into the request context under typed
// keys.
//
// Cookie auth only: Authorization: Bearer gl_… is API tokens (Task 29), not
// sessions — a Bearer token presented here is ignored, not honored.
//
// Failures answer 401 JSON, never a redirect: this is an API, and the SPA
// decides where to send the user.
func RequireAuth(pool *pgxpool.Pool) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			cookie, err := c.Cookie(auth.SessionCookieName)
			if err != nil || cookie.Value == "" {
				return c.JSON(http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			}
			result, err := auth.AuthenticateSession(c.Request().Context(), pool, cookie.Value)
			if err != nil {
				return c.JSON(http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			}
			ctx := context.WithValue(c.Request().Context(), authUserKey, result.User)
			ctx = context.WithValue(ctx, authSessionIDKey, result.SessionID)
			c.SetRequest(c.Request().WithContext(ctx))
			return next(c)
		}
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
// when the request did not pass through the middleware.
func CurrentSessionID(c *echo.Context) string {
	if id, ok := c.Request().Context().Value(authSessionIDKey).(string); ok {
		return id
	}
	return ""
}
