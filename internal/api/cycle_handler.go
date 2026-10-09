package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// Cycle endpoints (Task 22) nested under projects. Every route sits behind
// RequireAuth — workspace membership is the tenancy boundary. Errors use
// the spec §5 envelope via cycleError (which falls back to issueError for
// the shared sentinels).

// RegisterCycleRoutes mounts the cycle endpoints. Call this before the SPA
// catch-all so API routes are never shadowed.
func RegisterCycleRoutes(e *echo.Echo, h *IssueHandler) {
	g := e.Group("/api/v1/workspaces/:slug/projects/:identifier/cycles", RequireAuth(h.Pool))
	g.POST("", h.createCycle)
	g.GET("", h.listCycles)
	g.GET("/:cycleID", h.getCycle)
	g.GET("/:cycleID/burndown", h.getCycleBurndown)
	g.PATCH("/:cycleID", h.updateCycle)
	g.DELETE("/:cycleID", h.deleteCycle)
	g.POST("/:cycleID/issues", h.addCycleIssues)
}

// cycleError maps cycle sentinel errors to HTTP statuses, delegating the
// shared sentinels (ErrNotFound, ErrProjectNotFound, ErrIssueNotFound,
// ErrForbidden, ErrNameRequired, ErrNothingToUpdate, ErrBulkEmptyIDs) to
// issueError.
func cycleError(c *echo.Context, err error) error {
	switch {
	case errors.Is(err, service.ErrCycleNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "cycle not found", nil)
	case errors.Is(err, service.ErrCycleConflict):
		return WriteError(c, http.StatusConflict, ErrCodeConflict, "cycle name already exists", nil)
	case errors.Is(err, service.ErrInvalidCycle):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid cycle: name is required and start_date must not be after end_date", nil)
	case errors.Is(err, service.ErrInvalidCycleID):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid id", nil)
	default:
		return issueError(c, err)
	}
}

type createCycleBody struct {
	Name      string `json:"name"`
	StartDate string `json:"start_date"` // YYYY-MM-DD
	EndDate   string `json:"end_date"`   // YYYY-MM-DD
}

// parseCycleDate parses a YYYY-MM-DD date. The zero time is UTC midnight;
// only the date part is ever compared or stored (::date).
func parseCycleDate(s string) (time.Time, error) {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}, err
	}
	return t, nil
}

// createCycle implements POST /api/v1/workspaces/{slug}/projects/{identifier}/cycles.
// Member (15)+; the service enforces it.
func (h *IssueHandler) createCycle(c *echo.Context) error {
	var body createCycleBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	start, err := parseCycleDate(body.StartDate)
	if err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "start_date must be YYYY-MM-DD", nil)
	}
	end, err := parseCycleDate(body.EndDate)
	if err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "end_date must be YYYY-MM-DD", nil)
	}
	cycle, err := service.CreateCycle(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID,
		service.CycleInput{Name: body.Name, StartDate: start, EndDate: end})
	if err != nil {
		if errors.Is(err, service.ErrNameRequired) {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "name is required", nil)
		}
		return cycleError(c, err)
	}
	return c.JSON(http.StatusCreated, cycle)
}

// listCycles implements GET /api/v1/workspaces/{slug}/projects/{identifier}/cycles.
func (h *IssueHandler) listCycles(c *echo.Context) error {
	cycles, err := service.ListCycles(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID)
	if err != nil {
		return cycleError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"cycles": cycles})
}

// getCycle implements GET .../cycles/{cycleID}. The response carries the
// live progress_snapshot (computed in one query by the service).
func (h *IssueHandler) getCycle(c *echo.Context) error {
	cycleID, ok := requireUUIDParam(c, "cycleID", "cycle id")
	if !ok {
		return nil
	}
	cycle, err := service.GetCycle(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, cycleID)
	if err != nil {
		return cycleError(c, err)
	}
	return c.JSON(http.StatusOK, cycle)
}

// getCycleBurndown implements GET
// /api/v1/workspaces/{slug}/projects/{identifier}/cycles/{cycleID}/burndown:
// the cycle's true burndown, reconstructed server-side from the
// issue_activities audit log (one aggregate query instead of N+1
// per-issue history fetches). Read-only; any workspace member may call.
func (h *IssueHandler) getCycleBurndown(c *echo.Context) error {
	cycleID, ok := requireUUIDParam(c, "cycleID", "cycle id")
	if !ok {
		return nil
	}
	bd, err := service.GetCycleBurndown(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, cycleID)
	if err != nil {
		return cycleError(c, err)
	}
	return c.JSON(http.StatusOK, bd)
}

// updateCycle implements PATCH .../cycles/{cycleID}. Partial semantics:
// nil fields are left untouched. Member (15)+.
func (h *IssueHandler) updateCycle(c *echo.Context) error {
	var body struct {
		Name      *string `json:"name"`
		StartDate *string `json:"start_date"`
		EndDate   *string `json:"end_date"`
	}
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	patch := service.CyclePatch{Name: body.Name}
	if body.StartDate != nil {
		start, err := parseCycleDate(*body.StartDate)
		if err != nil {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "start_date must be YYYY-MM-DD", nil)
		}
		patch.StartDate = &start
	}
	if body.EndDate != nil {
		end, err := parseCycleDate(*body.EndDate)
		if err != nil {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "end_date must be YYYY-MM-DD", nil)
		}
		patch.EndDate = &end
	}
	cycleID, ok := requireUUIDParam(c, "cycleID", "cycle id")
	if !ok {
		return nil
	}
	cycle, err := service.UpdateCycle(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, cycleID, patch)
	if err != nil {
		if errors.Is(err, service.ErrNameRequired) {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "name is required", nil)
		}
		return cycleError(c, err)
	}
	return c.JSON(http.StatusOK, cycle)
}

// deleteCycle implements DELETE .../cycles/{cycleID}. Member (15)+.
func (h *IssueHandler) deleteCycle(c *echo.Context) error {
	cycleID, ok := requireUUIDParam(c, "cycleID", "cycle id")
	if !ok {
		return nil
	}
	if err := service.DeleteCycle(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, cycleID); err != nil {
		return cycleError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

type addCycleIssuesBody struct {
	IssueIDs []string `json:"issue_ids"`
}

// addCycleIssues implements POST .../cycles/{cycleID}/issues
// {issue_ids}. Idempotent: repeats are no-ops. Member (15)+.
func (h *IssueHandler) addCycleIssues(c *echo.Context) error {
	var body addCycleIssuesBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	cycleID, ok := requireUUIDParam(c, "cycleID", "cycle id")
	if !ok {
		return nil
	}
	if err := service.AddCycleIssues(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, cycleID, body.IssueIDs); err != nil {
		return cycleError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"added": len(body.IssueIDs)})
}
