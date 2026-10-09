package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// Analytics endpoints (C4T3) nested under projects. Read-only; every
// route sits behind RequireAuth — workspace membership is the tenancy
// boundary (outsiders get the shared 404). Errors use the spec §5
// envelope via analyticsError, which delegates cycle sentinels to
// cycleError (which itself falls back to issueError).

// RegisterAnalyticsRoutes mounts the analytics endpoints. Call this
// before the SPA catch-all so API routes are never shadowed.
func RegisterAnalyticsRoutes(e *echo.Echo, h *IssueHandler) {
	g := e.Group("/api/v1/workspaces/:slug/projects/:identifier/analytics", RequireAuth(h.Pool))
	g.GET("/summary", h.getAnalyticsSummary)
	g.GET("/cycle/:cycleID/burndown", h.getAnalyticsCycleBurndown)
	g.GET("/trends", h.getAnalyticsTrends)
}

// analyticsError maps analytics sentinel errors to HTTP statuses,
// delegating everything else to cycleError (cycle not found lives
// there) and ultimately issueError.
func analyticsError(c *echo.Context, err error) error {
	switch {
	case errors.Is(err, service.ErrInvalidAnalyticsDays):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "days must be a positive integer", nil)
	default:
		return cycleError(c, err)
	}
}

// getAnalyticsSummary implements GET
// /api/v1/workspaces/{slug}/projects/{identifier}/analytics/summary.
func (h *IssueHandler) getAnalyticsSummary(c *echo.Context) error {
	s, err := service.GetAnalyticsSummary(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID)
	if err != nil {
		return analyticsError(c, err)
	}
	return c.JSON(http.StatusOK, s)
}

// getAnalyticsCycleBurndown implements GET
// /api/v1/workspaces/{slug}/projects/{identifier}/analytics/cycle/{cycleID}/burndown.
func (h *IssueHandler) getAnalyticsCycleBurndown(c *echo.Context) error {
	bd, err := service.GetAnalyticsCycleBurndown(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, c.Param("cycleID"))
	if err != nil {
		return analyticsError(c, err)
	}
	return c.JSON(http.StatusOK, bd)
}

// getAnalyticsTrends implements GET
// /api/v1/workspaces/{slug}/projects/{identifier}/analytics/trends?days=30.
func (h *IssueHandler) getAnalyticsTrends(c *echo.Context) error {
	days := 30
	if raw := c.QueryParam("days"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			return analyticsError(c, service.ErrInvalidAnalyticsDays)
		}
		if n > 90 {
			n = 90
		}
		days = n
	}
	trends, err := service.GetAnalyticsTrends(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, days)
	if err != nil {
		return analyticsError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"days": trends})
}
