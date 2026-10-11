package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// Project template endpoints (C17T1): workspace-scoped project blueprints.
// The routes sit behind RequireAuth — workspace membership is the tenancy
// boundary. Reads need member (15)+ (guests get 403, non-members 404);
// writes need admin (20), enforced in the service. Errors use the spec §5
// envelope via projectTemplateError, which delegates the shared sentinels
// to projectError. No new error shapes.

// ProjectTemplateHandler serves the project-template endpoints.
type ProjectTemplateHandler struct {
	Pool *pgxpool.Pool
}

// RegisterProjectTemplateRoutes mounts the project-template endpoints.
// Call this before the SPA catch-all so API routes are never shadowed.
func RegisterProjectTemplateRoutes(e *echo.Echo, h *ProjectTemplateHandler) {
	g := e.Group("/api/v1/workspaces/:slug/templates", RequireAuth(h.Pool))
	g.POST("", h.createProjectTemplate)
	g.GET("", h.listProjectTemplates)
	g.GET("/:templateID", h.getProjectTemplate)
	g.PATCH("/:templateID", h.updateProjectTemplate)
	g.DELETE("/:templateID", h.deleteProjectTemplate)
}

// projectTemplateError maps project-template sentinel errors to HTTP
// statuses, delegating the shared sentinels (ErrNotFound, ErrForbidden,
// ErrNameRequired, ErrNothingToUpdate) to projectError.
func projectTemplateError(c *echo.Context, err error) error {
	switch {
	case errors.Is(err, service.ErrProjectTemplateNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "project template not found", nil)
	case errors.Is(err, service.ErrProjectTemplateConflict):
		return WriteError(c, http.StatusConflict, ErrCodeConflict, "template name already exists in this workspace", nil)
	case errors.Is(err, service.ErrInvalidProjectTemplate):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid project template: names 1-120 chars, state groups triage|backlog|unstarted|started|completed|cancelled, colors #rrggbb, estimate point keys 1-40 chars", nil)
	default:
		return projectError(c, err)
	}
}

type createProjectTemplateBody struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Payload     json.RawMessage `json:"payload"`
}

// createProjectTemplate implements POST
// /api/v1/workspaces/{slug}/templates. Admin (20) only. 201 with the
// template; 409 on a duplicate name; 400 on a bad payload.
func (h *ProjectTemplateHandler) createProjectTemplate(c *echo.Context) error {
	var body createProjectTemplateBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	t, err := service.CreateProjectTemplate(c.Request().Context(), h.Pool,
		c.Param("slug"), CurrentUser(c).ID,
		service.ProjectTemplateInput{Name: body.Name, Description: body.Description, Payload: body.Payload})
	if err != nil {
		if errors.Is(err, service.ErrNameRequired) {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "name is required", nil)
		}
		return projectTemplateError(c, err)
	}
	return c.JSON(http.StatusCreated, t)
}

// listProjectTemplates implements GET /api/v1/workspaces/{slug}/templates.
// Member (15)+.
func (h *ProjectTemplateHandler) listProjectTemplates(c *echo.Context) error {
	templates, err := service.ListProjectTemplates(c.Request().Context(), h.Pool,
		c.Param("slug"), CurrentUser(c).ID)
	if err != nil {
		return projectTemplateError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"templates": templates})
}

// getProjectTemplate implements GET
// /api/v1/workspaces/{slug}/templates/{templateID}. Member (15)+.
func (h *ProjectTemplateHandler) getProjectTemplate(c *echo.Context) error {
	t, err := service.GetProjectTemplate(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("templateID"), CurrentUser(c).ID)
	if err != nil {
		return projectTemplateError(c, err)
	}
	return c.JSON(http.StatusOK, t)
}

type updateProjectTemplateBody struct {
	// Pointers: PATCH is partial — nil means untouched, matching the
	// project handler's convention. Payload is a raw message so the
	// client can replace the whole document; nil = untouched.
	Name        *string         `json:"name"`
	Description *string         `json:"description"`
	Payload     json.RawMessage `json:"payload"`
}

// updateProjectTemplate implements PATCH
// /api/v1/workspaces/{slug}/templates/{templateID}. Admin (20) only.
func (h *ProjectTemplateHandler) updateProjectTemplate(c *echo.Context) error {
	var body updateProjectTemplateBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	// Echo leaves Payload nil when the key is absent; json.RawMessage
	// marshals an explicit null to "null" — treat both as untouched.
	var payload json.RawMessage
	if body.Payload != nil && string(body.Payload) != "null" {
		payload = body.Payload
	}
	t, err := service.UpdateProjectTemplate(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("templateID"), CurrentUser(c).ID,
		service.ProjectTemplatePatch{Name: body.Name, Description: body.Description, Payload: payload})
	if err != nil {
		if errors.Is(err, service.ErrNameRequired) {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "name is required", nil)
		}
		if errors.Is(err, service.ErrNothingToUpdate) {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "nothing to update", nil)
		}
		return projectTemplateError(c, err)
	}
	return c.JSON(http.StatusOK, t)
}

// deleteProjectTemplate implements DELETE
// /api/v1/workspaces/{slug}/templates/{templateID}. Admin (20) only. 204.
func (h *ProjectTemplateHandler) deleteProjectTemplate(c *echo.Context) error {
	err := service.DeleteProjectTemplate(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("templateID"), CurrentUser(c).ID)
	if err != nil {
		return projectTemplateError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}
