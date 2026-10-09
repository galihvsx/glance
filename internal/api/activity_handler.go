package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// Project activity feed endpoints (C5T7): GET
// /api/v1/workspaces/{slug}/projects/{identifier}/activity?limit=50.
//
// Read-only; the route sits behind RequireAuth — workspace membership is
// the tenancy boundary. Reads need member (15)+: guests (5) get 403, and
// outsiders get the shared 404. Errors use the spec §5 envelope via
// activityError, which delegates the shared sentinels to issueError.

// RegisterActivityRoutes mounts the activity feed endpoint. Call this
// before the SPA catch-all so API routes are never shadowed.
func RegisterActivityRoutes(e *echo.Echo, h *IssueHandler) {
	g := e.Group("/api/v1/workspaces/:slug/projects/:identifier", RequireAuth(h.Pool))
	g.GET("/activity", h.getProjectActivity)
}

// activityError maps activity sentinel errors to HTTP statuses,
// delegating the shared sentinels (ErrNotFound, ErrForbidden,
// ErrProjectNotFound) to issueError.
func activityError(c *echo.Context, err error) error {
	switch {
	case errors.Is(err, service.ErrInvalidActivityLimit):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid limit: want 1-200", nil)
	default:
		return issueError(c, err)
	}
}

// getProjectActivity implements GET
// /api/v1/workspaces/{slug}/projects/{identifier}/activity?limit=50:
// the project's field-level change feed, newest first. The limit
// defaults to 50 and is capped at 200.
func (h *IssueHandler) getProjectActivity(c *echo.Context) error {
	limit := service.ActivityDefaultLimit
	if raw := c.QueryParam("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return activityError(c, service.ErrInvalidActivityLimit)
		}
		limit = n
	}
	entries, err := service.GetProjectActivity(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, limit)
	if err != nil {
		return activityError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"activity": entries})
}
