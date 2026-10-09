package api

import (
	"net/http"

	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// Project overview endpoint (C8T5): GET
// /api/v1/workspaces/{slug}/projects/{identifier}/overview.
//
// Single-round-trip payload for the Overview page: project identity +
// editable description, the caller's role (drives the edit gate),
// analytics summary, states (group resolution), recent activity (also
// feeds top contributors), and the live cycle's completed/total counts.
// Read-only; member (15)+ — the activity feed inside is member-scoped,
// so guests get 403 here just like on the standalone /activity route.
// Errors use the spec §5 envelope via issueError; no new sentinels.

// RegisterOverviewRoutes mounts the overview endpoint. Call this before
// the SPA catch-all so API routes are never shadowed.
func RegisterOverviewRoutes(e *echo.Echo, h *IssueHandler) {
	g := e.Group("/api/v1/workspaces/:slug/projects/:identifier", RequireAuth(h.Pool))
	g.GET("/overview", h.getProjectOverview)
}

// getProjectOverview implements GET
// /api/v1/workspaces/{slug}/projects/{identifier}/overview.
func (h *IssueHandler) getProjectOverview(c *echo.Context) error {
	ov, err := service.GetProjectOverview(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID)
	if err != nil {
		return issueError(c, err)
	}
	return c.JSON(http.StatusOK, ov)
}
