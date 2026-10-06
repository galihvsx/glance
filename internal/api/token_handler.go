package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"glance/internal/auth"
)

// TokenHandler serves the token-management endpoints. All of them sit
// behind RequireSessionAuth (cookie only): a token must never be able to
// mint, list, or revoke tokens — not even its own.
type TokenHandler struct {
	Pool *pgxpool.Pool
}

// RegisterTokenRoutes mounts the management group. Session auth only.
func RegisterTokenRoutes(e *echo.Echo, h *TokenHandler) {
	g := e.Group("/api/v1/tokens", RequireSessionAuth(h.Pool))
	g.POST("", h.createToken)
	g.GET("", h.listTokens)
	g.DELETE("/:id", h.revokeToken)
}

// createTokenBody is the POST /api/v1/tokens payload. expires_at is
// optional (RFC 3339); absent means the token never expires.
type createTokenBody struct {
	Name      string     `json:"name"`
	Scopes    []string   `json:"scopes"`
	ExpiresAt *time.Time `json:"expires_at"`
}

// createToken mints a token for the session-authenticated user. The
// plaintext is returned exactly once in the 201 body — there is no
// endpoint that reveals it again.
func (h *TokenHandler) createToken(c *echo.Context) error {
	var body createTokenBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	user := CurrentUser(c)
	if user == nil {
		return WriteError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized", nil)
	}
	created, err := auth.CreateToken(c.Request().Context(), h.Pool, user.ID, body.Name, body.Scopes, body.ExpiresAt)
	if err != nil {
		if errors.Is(err, auth.ErrInvalidScope) {
			// Validation errors are safe to echo; strip the internal
			// "auth:" prefix so the message reads as an API message.
			msg := strings.TrimPrefix(err.Error(), "auth: ")
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, msg, nil)
		}
		return WriteInternalError(c)
	}
	return c.JSON(http.StatusCreated, created)
}

// listTokens returns the user's live tokens — id, name, scopes, timestamps.
// Hashes and plaintext never leave the server.
func (h *TokenHandler) listTokens(c *echo.Context) error {
	user := CurrentUser(c)
	if user == nil {
		return WriteError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized", nil)
	}
	tokens, err := auth.ListTokens(c.Request().Context(), h.Pool, user.ID)
	if err != nil {
		return WriteInternalError(c)
	}
	return c.JSON(http.StatusOK, map[string]any{"tokens": tokens})
}

// revokeToken revokes one of the user's tokens. Idempotent within
// ownership: revoking an unknown, already-revoked, or foreign token is
// 404, never distinguishing the cases.
func (h *TokenHandler) revokeToken(c *echo.Context) error {
	user := CurrentUser(c)
	if user == nil {
		return WriteError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized", nil)
	}
	ok, err := auth.RevokeToken(c.Request().Context(), h.Pool, user.ID, c.Param("id"))
	if err != nil {
		return WriteInternalError(c)
	}
	if !ok {
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "token not found", nil)
	}
	return c.NoContent(http.StatusNoContent)
}
