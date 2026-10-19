package api

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// Initiative endpoints (C27T0) nested under workspaces — unlike modules
// and cycles they are workspace-scoped, not project-scoped. Every route
// sits behind RequireAuth — workspace membership is the tenancy boundary.
// Errors use the spec §5 envelope via initiativeError (which falls back
// to issueError for the shared sentinels: ErrNotFound for a bad slug or
// a non-member caller, ErrForbidden for guest writes, ErrProjectNotFound
// for a foreign/unknown project id).

// RegisterInitiativeRoutes mounts the initiative endpoints. Call this
// before the SPA catch-all so API routes are never shadowed.
func RegisterInitiativeRoutes(e *echo.Echo, h *IssueHandler) {
	g := e.Group("/api/v1/workspaces/:slug/initiatives", RequireAuth(h.Pool))
	g.POST("", h.createInitiative)
	g.GET("", h.listInitiatives)
	g.GET("/:initiativeID", h.getInitiative)
	g.PATCH("/:initiativeID", h.updateInitiative)
	g.DELETE("/:initiativeID", h.deleteInitiative)
	g.GET("/:initiativeID/rollup", h.getInitiativeRollup)
	g.GET("/:initiativeID/projects", h.listInitiativeProjects)
	g.POST("/:initiativeID/projects", h.addInitiativeProject)
	g.DELETE("/:initiativeID/projects/:projectID", h.removeInitiativeProject)
	// C71T0: initiative↔epic links, mirroring the project links above.
	g.GET("/:initiativeID/epics", h.listInitiativeEpics)
	g.POST("/:initiativeID/epics", h.addInitiativeEpic)
	g.DELETE("/:initiativeID/epics/:epicID", h.removeInitiativeEpic)
}

// initiativeError maps initiative sentinel errors to HTTP statuses,
// delegating the shared sentinels (ErrNotFound, ErrForbidden,
// ErrProjectNotFound, ErrNameRequired, ErrNothingToUpdate) to issueError.
func initiativeError(c *echo.Context, err error) error {
	switch {
	case errors.Is(err, service.ErrInitiativeNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "initiative not found", nil)
	case errors.Is(err, service.ErrInitiativeConflict):
		return WriteError(c, http.StatusConflict, ErrCodeConflict, "initiative name already exists", nil)
	case errors.Is(err, service.ErrInvalidInitiative):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid initiative: bad status", nil)
	case errors.Is(err, service.ErrInvalidInitiativeID):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid id", nil)
	default:
		return issueError(c, err)
	}
}

type initiativeBody struct {
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Status      string  `json:"status"`
}

// createInitiative implements POST
// /api/v1/workspaces/{slug}/initiatives. Member (15)+; the service
// enforces it.
func (h *IssueHandler) createInitiative(c *echo.Context) error {
	var body initiativeBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	init, err := service.CreateInitiative(c.Request().Context(), h.Pool,
		c.Param("slug"), CurrentUser(c).ID,
		service.InitiativeInput{
			Name:        body.Name,
			Description: body.Description,
			Status:      body.Status,
		})
	if err != nil {
		if errors.Is(err, service.ErrNameRequired) {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "name is required", nil)
		}
		return initiativeError(c, err)
	}
	return c.JSON(http.StatusCreated, init)
}

// listInitiatives implements GET /api/v1/workspaces/{slug}/initiatives.
// Archived are excluded by default; ?include_archived=1 includes them.
func (h *IssueHandler) listInitiatives(c *echo.Context) error {
	includeArchived := c.QueryParam("include_archived") == "1"
	inits, err := service.ListInitiatives(c.Request().Context(), h.Pool,
		c.Param("slug"), CurrentUser(c).ID, includeArchived)
	if err != nil {
		return initiativeError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"initiatives": inits})
}

// getInitiative implements GET .../initiatives/{initiativeID}.
func (h *IssueHandler) getInitiative(c *echo.Context) error {
	initiativeID, ok := requireUUIDParam(c, "initiativeID", "initiative id")
	if !ok {
		return nil
	}
	init, err := service.GetInitiative(c.Request().Context(), h.Pool,
		c.Param("slug"), CurrentUser(c).ID, initiativeID)
	if err != nil {
		return initiativeError(c, err)
	}
	return c.JSON(http.StatusOK, init)
}

type patchInitiativeBody struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	Status      *string `json:"status"`
}

// updateInitiative implements PATCH .../initiatives/{initiativeID}.
// Partial semantics: missing keys are untouched, "" clears description,
// any other value sets it. Member (15)+.
func (h *IssueHandler) updateInitiative(c *echo.Context) error {
	var body patchInitiativeBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	initiativeID, ok := requireUUIDParam(c, "initiativeID", "initiative id")
	if !ok {
		return nil
	}
	init, err := service.UpdateInitiative(c.Request().Context(), h.Pool,
		c.Param("slug"), CurrentUser(c).ID, initiativeID,
		service.InitiativePatch{
			Name:        body.Name,
			Description: body.Description,
			Status:      body.Status,
		})
	if err != nil {
		if errors.Is(err, service.ErrNameRequired) {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "name is required", nil)
		}
		return initiativeError(c, err)
	}
	return c.JSON(http.StatusOK, init)
}

// deleteInitiative implements DELETE .../initiatives/{initiativeID}. The
// membership rows cascade; member projects and all issue data are
// untouched (initiatives own no issues). Member (15)+.
func (h *IssueHandler) deleteInitiative(c *echo.Context) error {
	initiativeID, ok := requireUUIDParam(c, "initiativeID", "initiative id")
	if !ok {
		return nil
	}
	if err := service.DeleteInitiative(c.Request().Context(), h.Pool,
		c.Param("slug"), CurrentUser(c).ID, initiativeID); err != nil {
		return initiativeError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// getInitiativeRollup implements GET .../initiatives/{initiativeID}/rollup:
// issue counts by state group across all member projects, per-project
// breakdown, and completion %.
func (h *IssueHandler) getInitiativeRollup(c *echo.Context) error {
	initiativeID, ok := requireUUIDParam(c, "initiativeID", "initiative id")
	if !ok {
		return nil
	}
	roll, err := service.GetInitiativeRollup(c.Request().Context(), h.Pool,
		c.Param("slug"), CurrentUser(c).ID, initiativeID)
	if err != nil {
		return initiativeError(c, err)
	}
	return c.JSON(http.StatusOK, roll)
}

// listInitiativeProjects implements GET .../initiatives/{initiativeID}/projects.
func (h *IssueHandler) listInitiativeProjects(c *echo.Context) error {
	initiativeID, ok := requireUUIDParam(c, "initiativeID", "initiative id")
	if !ok {
		return nil
	}
	projects, err := service.ListInitiativeProjects(c.Request().Context(), h.Pool,
		c.Param("slug"), CurrentUser(c).ID, initiativeID)
	if err != nil {
		return initiativeError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"projects": projects})
}

type initiativeProjectBody struct {
	ProjectID string `json:"project_id"`
}

// addInitiativeProject implements POST .../initiatives/{initiativeID}/projects
// {project_id}. Idempotent: repeats are no-ops. Member (15)+.
func (h *IssueHandler) addInitiativeProject(c *echo.Context) error {
	var body initiativeProjectBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	initiativeID, ok := requireUUIDParam(c, "initiativeID", "initiative id")
	if !ok {
		return nil
	}
	if err := service.AddInitiativeProject(c.Request().Context(), h.Pool,
		c.Param("slug"), CurrentUser(c).ID, initiativeID, body.ProjectID); err != nil {
		return initiativeError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"added": body.ProjectID})
}

// removeInitiativeProject implements DELETE
// .../initiatives/{initiativeID}/projects/{projectID}. Idempotent:
// removing a non-member is a no-op. Member (15)+.
func (h *IssueHandler) removeInitiativeProject(c *echo.Context) error {
	initiativeID, ok := requireUUIDParam(c, "initiativeID", "initiative id")
	if !ok {
		return nil
	}
	projectID, ok := requireUUIDParam(c, "projectID", "project id")
	if !ok {
		return nil
	}
	if err := service.RemoveInitiativeProject(c.Request().Context(), h.Pool,
		c.Param("slug"), CurrentUser(c).ID, initiativeID, projectID); err != nil {
		return initiativeError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"removed": projectID})
}

// listInitiativeEpics implements GET .../initiatives/{initiativeID}/epics:
// the initiative's linked epics (with their projects).
func (h *IssueHandler) listInitiativeEpics(c *echo.Context) error {
	initiativeID, ok := requireUUIDParam(c, "initiativeID", "initiative id")
	if !ok {
		return nil
	}
	epics, err := service.ListInitiativeEpics(c.Request().Context(), h.Pool,
		c.Param("slug"), CurrentUser(c).ID, initiativeID)
	if err != nil {
		return initiativeEpicError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"epics": epics})
}

type initiativeEpicBody struct {
	EpicID string `json:"epic_id"`
}

// addInitiativeEpic implements POST .../initiatives/{initiativeID}/epics
// {epic_id}. The epic must belong to a project of the workspace.
// Idempotent: repeats are no-ops. Member (15)+.
func (h *IssueHandler) addInitiativeEpic(c *echo.Context) error {
	var body initiativeEpicBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	initiativeID, ok := requireUUIDParam(c, "initiativeID", "initiative id")
	if !ok {
		return nil
	}
	if err := service.AddInitiativeEpic(c.Request().Context(), h.Pool,
		c.Param("slug"), CurrentUser(c).ID, initiativeID, body.EpicID); err != nil {
		return initiativeEpicError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"added": body.EpicID})
}

// removeInitiativeEpic implements DELETE
// .../initiatives/{initiativeID}/epics/{epicID}. Idempotent: removing a
// non-link is a no-op. Member (15)+.
func (h *IssueHandler) removeInitiativeEpic(c *echo.Context) error {
	initiativeID, ok := requireUUIDParam(c, "initiativeID", "initiative id")
	if !ok {
		return nil
	}
	epicID, ok := requireUUIDParam(c, "epicID", "epic id")
	if !ok {
		return nil
	}
	if err := service.RemoveInitiativeEpic(c.Request().Context(), h.Pool,
		c.Param("slug"), CurrentUser(c).ID, initiativeID, epicID); err != nil {
		return initiativeEpicError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"removed": epicID})
}

// initiativeEpicError maps the epic sentinels onto the initiative
// envelope: a foreign-workspace epic is 404 (project not found), a
// malformed or unknown epic id is 400/404 respectively.
func initiativeEpicError(c *echo.Context, err error) error {
	switch {
	case errors.Is(err, service.ErrEpicNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "epic not found", nil)
	case errors.Is(err, service.ErrInvalidEpicID):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid id", nil)
	default:
		return initiativeError(c, err)
	}
}
