package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// Time tracking endpoints (C3T7) nested under the issue satellite group.
// Every route sits behind RequireAuth — workspace membership is the
// tenancy boundary. Errors use the spec §5 envelope via timeError (which
// falls back to issueError for the shared sentinels).

// timeError maps time-tracking sentinel errors to HTTP statuses,
// delegating the shared sentinels (ErrNotFound, ErrIssueNotFound,
// ErrForbidden, ErrInvalidTimeEntry's cousins) to issueError.
func timeError(c *echo.Context, err error) error {
	switch {
	case errors.Is(err, service.ErrTimerAlreadyRunning):
		return WriteError(c, http.StatusConflict, ErrCodeConflict, "timer already running", nil)
	case errors.Is(err, service.ErrNoRunningTimer):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "no running timer", nil)
	case errors.Is(err, service.ErrInvalidTimeEntry):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid time entry: ended_at must be after started_at", nil)
	default:
		return issueError(c, err)
	}
}

// startTimer implements POST .../issues/{uuid}/time/start → 201.
// Member (15)+; the service enforces it. 409 when the caller already has
// a running timer on this issue.
func (h *IssueHandler) startTimer(c *echo.Context) error {
	slug, ident, uuid, actor, ok := h.issueParams(c)
	if !ok {
		return nil
	}
	e, err := service.StartTimer(c.Request().Context(), h.Pool, slug, ident, uuid, actor)
	if err != nil {
		return timeError(c, err)
	}
	return c.JSON(http.StatusCreated, e)
}

// stopTimer implements POST .../issues/{uuid}/time/stop → 200.
// 404 when the caller has no running timer on this issue.
func (h *IssueHandler) stopTimer(c *echo.Context) error {
	slug, ident, uuid, actor, ok := h.issueParams(c)
	if !ok {
		return nil
	}
	e, err := service.StopTimer(c.Request().Context(), h.Pool, slug, ident, uuid, actor)
	if err != nil {
		return timeError(c, err)
	}
	return c.JSON(http.StatusOK, e)
}

type logTimeEntryBody struct {
	StartedAt string `json:"started_at"` // RFC3339
	EndedAt   string `json:"ended_at"`   // RFC3339
	Note      string `json:"note"`
}

// logTimeEntry implements POST .../issues/{uuid}/time/log → 201.
// Records a manual entry; ended_at must be after started_at (400).
func (h *IssueHandler) logTimeEntry(c *echo.Context) error {
	slug, ident, uuid, actor, ok := h.issueParams(c)
	if !ok {
		return nil
	}
	var body logTimeEntryBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	started, err := time.Parse(time.RFC3339, body.StartedAt)
	if err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "started_at must be RFC3339", nil)
	}
	ended, err := time.Parse(time.RFC3339, body.EndedAt)
	if err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "ended_at must be RFC3339", nil)
	}
	e, err := service.LogTimeEntry(c.Request().Context(), h.Pool, slug, ident, uuid, actor, started, ended, body.Note)
	if err != nil {
		return timeError(c, err)
	}
	return c.JSON(http.StatusCreated, e)
}

// listTimeEntries implements GET .../issues/{uuid}/time → 200
// {entries: [...], total_seconds}. Any member may read.
func (h *IssueHandler) listTimeEntries(c *echo.Context) error {
	slug, ident, uuid, actor, ok := h.issueParams(c)
	if !ok {
		return nil
	}
	entries, total, err := service.ListTimeEntries(c.Request().Context(), h.Pool, slug, ident, uuid, actor)
	if err != nil {
		return timeError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{
		"entries":       entries,
		"total_seconds": total,
	})
}

// RegisterTimeSummaryRoutes mounts the project time-report endpoint.
// Call this before the SPA catch-all so API routes are never shadowed.
func RegisterTimeSummaryRoutes(e *echo.Echo, h *IssueHandler) {
	g := e.Group("/api/v1/workspaces/:slug/projects/:identifier/time", RequireAuth(h.Pool))
	g.GET("/summary", h.getTimeSummary)
}

// getTimeSummary implements GET
// /api/v1/workspaces/{slug}/projects/{identifier}/time/summary?days=30&group_by=day.
// Completed entries only, grouped by day (default), week, user, or issue.
// Any member may read. Mirrors the analytics trends ?days= convention:
// garbage is 400, oversized clamps to 365.
func (h *IssueHandler) getTimeSummary(c *echo.Context) error {
	days := 30
	if raw := c.QueryParam("days"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "days must be a positive integer", nil)
		}
		if n > 365 {
			n = 365
		}
		days = n
	}
	groupBy := c.QueryParam("group_by")
	if groupBy == "" {
		groupBy = "day"
	}
	switch groupBy {
	case "day", "week", "user", "issue":
	default:
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid group_by: want day, week, user, or issue", nil)
	}
	sum, err := service.GetTimeSummary(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, days, groupBy)
	if err != nil {
		return timeError(c, err)
	}
	return c.JSON(http.StatusOK, sum)
}
