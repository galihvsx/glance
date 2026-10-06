// Package api serves glance's HTTP API.
package api

import (
	"errors"
	"net/http"

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
