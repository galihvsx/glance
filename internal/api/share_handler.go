package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// Public share-link endpoints (C4T5).
//
// Member endpoints sit behind RequireAuth (workspace membership is the
// tenancy boundary) and are registered in RegisterSatelliteRoutes
// (issues) and RegisterPageRoutes (pages):
//   POST   .../issues/:uuid/share          (member 15+)
//   GET    .../issues/:uuid/share          (any member)
//   DELETE .../issues/:uuid/share/:token   (member 15+)
//   POST   .../pages/:pageID/share         (member 15+)
//   GET    .../pages/:pageID/share         (any member)
//   DELETE .../pages/:pageID/share/:token  (member 15+)
//
// The public endpoint is registered WITHOUT auth (RegisterPublicRoutes):
//   GET    /api/v1/public/s/:token
// Unknown, revoked, or expired tokens are 404 — never 403 — so token
// existence does not leak. Errors use the spec §5 envelope via
// shareError (falling back to issueError for shared sentinels).

// shareError maps share sentinel errors to HTTP statuses, delegating
// the shared sentinels to issueError.
func shareError(c *echo.Context, err error) error {
	switch {
	case errors.Is(err, service.ErrShareNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "share link not found", nil)
	case errors.Is(err, service.ErrInvalidShare):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid share link request", nil)
	default:
		return issueError(c, err)
	}
}

type createShareBody struct {
	// ExpiresAt is an optional RFC3339 timestamp; absent = never expires.
	ExpiresAt *string `json:"expires_at"`
}

func parseShareExpiry(c *echo.Context, body createShareBody) (*time.Time, error) {
	if body.ExpiresAt == nil || strings.TrimSpace(*body.ExpiresAt) == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(*body.ExpiresAt))
	if err != nil {
		return nil, service.ErrInvalidShare
	}
	return &t, nil
}

func shareLinkResponse(c *echo.Context, s *service.ShareLink) error {
	url := sharePublicURL(c, s.Token)
	return c.JSON(http.StatusCreated, map[string]any{
		"token": s.Token,
		"url":   url,
		"share": s,
	})
}

// sharePublicURL builds the absolute public URL for a token.
func sharePublicURL(c *echo.Context, token string) string {
	scheme := "http"
	if c.Request().TLS != nil {
		scheme = "https"
	}
	if xfProto := c.Request().Header.Get("X-Forwarded-Proto"); xfProto != "" {
		scheme = strings.Split(xfProto, ",")[0]
	}
	return scheme + "://" + c.Request().Host + "/s/" + token
}

// createIssueShare implements POST .../issues/:uuid/share.
func (h *IssueHandler) createIssueShare(c *echo.Context) error {
	slug, ident, uuid, actor, ok := h.issueParams(c)
	if !ok {
		return nil
	}
	var body createShareBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	expiresAt, err := parseShareExpiry(c, body)
	if err != nil {
		return shareError(c, err)
	}
	s, err := service.CreateShareLink(c.Request().Context(), h.Pool, slug, ident, uuid, actor, expiresAt)
	if err != nil {
		return shareError(c, err)
	}
	return shareLinkResponse(c, s)
}

// listIssueShares implements GET .../issues/:uuid/share.
func (h *IssueHandler) listIssueShares(c *echo.Context) error {
	slug, ident, uuid, actor, ok := h.issueParams(c)
	if !ok {
		return nil
	}
	links, err := service.ListShareLinks(c.Request().Context(), h.Pool, slug, ident, uuid, actor)
	if err != nil {
		return shareError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"links": links})
}

// revokeIssueShare implements DELETE .../issues/:uuid/share/:token.
func (h *IssueHandler) revokeIssueShare(c *echo.Context) error {
	slug := c.Param("slug")
	token := c.Param("token")
	if token == "" {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "token is required", nil)
	}
	if err := service.RevokeShareLink(c.Request().Context(), h.Pool, slug, CurrentUser(c).ID, token); err != nil {
		return shareError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// pageShareParams extracts (slug, identifier, pageID, actor) for the page
// share endpoints. Malformed page ids are 400 (service maps them to
// ErrPageNotFound → 404; the handler pre-validates UUID shape like the
// other page routes do — check page_handler.go conventions).
func (h *IssueHandler) pageShareParams(c *echo.Context) (slug, ident, pageID, actor string, ok bool) {
	pageID, ok = requireUUIDParam(c, "pageID", "page id")
	if !ok {
		return "", "", "", "", false
	}
	return c.Param("slug"), c.Param("identifier"), strings.ToLower(pageID), CurrentUser(c).ID, true
}

// createPageShare implements POST .../pages/:pageID/share.
func (h *IssueHandler) createPageShare(c *echo.Context) error {
	slug, ident, pageID, actor, ok := h.pageShareParams(c)
	if !ok {
		return nil
	}
	var body createShareBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	expiresAt, err := parseShareExpiry(c, body)
	if err != nil {
		return shareError(c, err)
	}
	s, err := service.CreatePageShareLink(c.Request().Context(), h.Pool, slug, ident, pageID, actor, expiresAt)
	if err != nil {
		return shareError(c, err)
	}
	return shareLinkResponse(c, s)
}

// listPageShares implements GET .../pages/:pageID/share.
func (h *IssueHandler) listPageShares(c *echo.Context) error {
	slug, ident, pageID, actor, ok := h.pageShareParams(c)
	if !ok {
		return nil
	}
	links, err := service.ListPageShareLinks(c.Request().Context(), h.Pool, slug, ident, pageID, actor)
	if err != nil {
		return shareError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"links": links})
}

// revokePageShare implements DELETE .../pages/:pageID/share/:token.
func (h *IssueHandler) revokePageShare(c *echo.Context) error {
	slug := c.Param("slug")
	token := c.Param("token")
	if token == "" {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "token is required", nil)
	}
	if err := service.RevokeShareLink(c.Request().Context(), h.Pool, slug, CurrentUser(c).ID, token); err != nil {
		return shareError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// getPublicShare implements GET /api/v1/public/s/:token — NO auth.
// The token is the only capability; unknown/revoked/expired → 404.
func (h *IssueHandler) getPublicShare(c *echo.Context) error {
	token := c.Param("token")
	if token == "" || len(token) > 128 {
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "share link not found", nil)
	}
	share, err := service.GetPublicShare(c.Request().Context(), h.Pool, token)
	if err != nil {
		return shareError(c, err)
	}
	return c.JSON(http.StatusOK, share)
}

// RegisterPublicRoutes mounts the unauthenticated public endpoints.
// Call before the SPA catch-all so /api/v1/public/* is never shadowed.
// NOTE: no RequireAuth here — the share token is the capability.
func RegisterPublicRoutes(e *echo.Echo, h *IssueHandler) {
	e.GET("/api/v1/public/s/:token", h.getPublicShare)
}
