package api

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// Project status updates (C34T0): flat timeline per project.
// Routes (nested under the project group, same tenancy pattern as
// page comments): GET/POST .../projects/{identifier}/updates,
// PATCH/DELETE .../projects/{identifier}/updates/{updateID}.
// Authz: viewer (10)+ reads, member (15)+ writes; edit/delete is the
// update's author or a workspace admin. Bodies are plain text (NOT
// markdown) in v1.

// RegisterProjectUpdateRoutes mounts the project-update endpoints.
// Call this before the SPA catch-all so API routes are never
// shadowed.
func RegisterProjectUpdateRoutes(e *echo.Echo, h *IssueHandler) {
	g := e.Group("/api/v1/workspaces/:slug/projects/:identifier/updates", RequireAuth(h.Pool))
	g.GET("", h.listProjectUpdates)
	g.POST("", h.createProjectUpdate)
	g.PATCH("/:updateID", h.updateProjectUpdate)
	g.DELETE("/:updateID", h.deleteProjectUpdate)
}

// projectUpdateError maps project-update sentinel errors to HTTP
// statuses, delegating the shared sentinels (ErrNotFound,
// ErrProjectNotFound, ErrForbidden) to projectError.
func projectUpdateError(c *echo.Context, err error) error {
	switch {
	case errors.Is(err, service.ErrProjectUpdateNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "project update not found", nil)
	case errors.Is(err, service.ErrInvalidProjectUpdate):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid project update: status must be on_track, at_risk, or off_track and body must be non-empty", nil)
	default:
		return projectError(c, err)
	}
}

type createProjectUpdateBody struct {
	Status string `json:"status"`
	Body   string `json:"body"`
}

// listProjectUpdates implements GET
// .../projects/{identifier}/updates: the update timeline, newest
// first. Viewer (10)+.
func (h *IssueHandler) listProjectUpdates(c *echo.Context) error {
	updates, err := service.ListProjectUpdates(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID)
	if err != nil {
		return projectUpdateError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"updates": updates})
}

// createProjectUpdate implements POST .../updates
// {"status", "body"}. Member (15)+.
func (h *IssueHandler) createProjectUpdate(c *echo.Context) error {
	var body createProjectUpdateBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	update, err := service.CreateProjectUpdate(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, body.Status, body.Body)
	if err != nil {
		return projectUpdateError(c, err)
	}
	return c.JSON(http.StatusCreated, update)
}

type patchProjectUpdateBody struct {
	Status *string `json:"status"`
	Body   *string `json:"body"`
}

// updateProjectUpdate implements PATCH .../updates/{updateID}:
// tri-state — a missing key leaves the field untouched. Author or
// admin; empty body → 400.
func (h *IssueHandler) updateProjectUpdate(c *echo.Context) error {
	var body patchProjectUpdateBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	updateID, ok := requireUUIDParam(c, "updateID", "update id")
	if !ok {
		return nil
	}
	update, err := service.UpdateProjectUpdate(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, updateID,
		service.ProjectUpdatePatch{Status: body.Status, Body: body.Body})
	if err != nil {
		return projectUpdateError(c, err)
	}
	return c.JSON(http.StatusOK, update)
}

// deleteProjectUpdate implements DELETE .../updates/{updateID}:
// hard delete, author or admin.
func (h *IssueHandler) deleteProjectUpdate(c *echo.Context) error {
	updateID, ok := requireUUIDParam(c, "updateID", "update id")
	if !ok {
		return nil
	}
	if err := service.DeleteProjectUpdate(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, updateID); err != nil {
		return projectUpdateError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}
