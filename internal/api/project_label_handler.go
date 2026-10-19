package api

// Project-label taxonomy HTTP endpoints (C76T0): workspace-level label
// CRUD plus per-project assignment. The taxonomy is mounted at the
// workspace — NOT nested under a project identifier — because the
// labels belong to the workspace and are reusable across projects
// (Plane: "at the workspace level"). Every route sits behind
// RequireAuth; the service enforces guest-read-only.

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// registerProjectLabelRoutes mounts the workspace-level project-label
// endpoints plus the per-project assignment routes. Called from
// RegisterProjectRoutes so the openapi route-coverage test
// (registerAllAPIRoutes) stays in lockstep with no changes.
func registerProjectLabelRoutes(e *echo.Echo, h *ProjectHandler) {
	pl := e.Group("/api/v1/workspaces/:slug/project-labels", RequireAuth(h.Pool))
	pl.POST("", h.createProjectLabel)
	pl.GET("", h.listProjectLabels)
	pl.GET("/:labelID", h.getProjectLabel)
	pl.PATCH("/:labelID", h.updateProjectLabel)
	pl.DELETE("/:labelID", h.deleteProjectLabel)

	g := e.Group("/api/v1/workspaces/:slug/projects", RequireAuth(h.Pool))
	// NOTE: the assign/unassign paths are namespaced as
	// .../project-labels/{labelID} (not .../labels/{labelID}) because the
	// work-item label taxonomy already owns
	// DELETE /projects/{identifier}/labels/{labelID} — the same path
	// cannot serve both taxonomies (Echo panics on the duplicate, and
	// the id would be ambiguous).
	g.POST("/:identifier/project-labels/:labelID", h.assignProjectLabel)
	g.DELETE("/:identifier/project-labels/:labelID", h.unassignProjectLabel)
}

// projectLabelError maps project-label service sentinels to HTTP
// statuses, delegating the shared tenancy/authz sentinels to
// projectError. Unknown errors are 500 with no detail leaked.
func projectLabelError(c *echo.Context, err error) error {
	switch {
	case errors.Is(err, service.ErrProjectLabelNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "project label not found", nil)
	case errors.Is(err, service.ErrProjectLabelConflict):
		return WriteError(c, http.StatusConflict, ErrCodeConflict, "project label name already exists", nil)
	case errors.Is(err, service.ErrProjectLabelAssignConflict):
		return WriteError(c, http.StatusConflict, ErrCodeConflict, "label already assigned to project", nil)
	case errors.Is(err, service.ErrInvalidLabel):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid color: must be #rrggbb", nil)
	case errors.Is(err, service.ErrNameRequired):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "name is required", nil)
	case errors.Is(err, service.ErrNothingToUpdate):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "nothing to update", nil)
	case errors.Is(err, service.ErrInvalidProjectLabelFilter):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, projectLabelFilterMessage(err), nil)
	default:
		return projectError(c, err)
	}
}

// projectLabelFilterMessage surfaces the wrapped detail of an
// ErrInvalidProjectLabelFilter (e.g. `unknown label "…"` or
// `malformed label "…" (want UUID)`) as the 400 message — "clear error
// message" per the C76 contract.
func projectLabelFilterMessage(err error) string {
	const prefix = "service: invalid project label filter"
	msg := strings.TrimPrefix(err.Error(), prefix+": ")
	if msg == "" || msg == err.Error() {
		return "invalid label filter"
	}
	return msg
}

type projectLabelBody struct {
	Name  string  `json:"name"`
	Color *string `json:"color"`
}

// createProjectLabel implements POST
// /api/v1/workspaces/{slug}/project-labels. Member (15)+; the service
// enforces it. 201 with the label JSON.
func (h *ProjectHandler) createProjectLabel(c *echo.Context) error {
	var body projectLabelBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	l, err := service.CreateProjectLabel(c.Request().Context(), h.Pool,
		c.Param("slug"), CurrentUser(c).ID,
		service.ProjectLabelInput{Name: body.Name, Color: body.Color})
	if err != nil {
		return projectLabelError(c, err)
	}
	return c.JSON(http.StatusCreated, l)
}

// listProjectLabels implements GET
// /api/v1/workspaces/{slug}/project-labels. Any member (guest 5+) may
// read. The envelope is {"project_labels": [...]} — never null.
func (h *ProjectHandler) listProjectLabels(c *echo.Context) error {
	labels, err := service.ListProjectLabels(c.Request().Context(), h.Pool,
		c.Param("slug"), CurrentUser(c).ID)
	if err != nil {
		return projectLabelError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"project_labels": labels})
}

// getProjectLabel implements GET
// /api/v1/workspaces/{slug}/project-labels/{labelID}.
func (h *ProjectHandler) getProjectLabel(c *echo.Context) error {
	l, err := service.GetProjectLabel(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("labelID"), CurrentUser(c).ID)
	if err != nil {
		return projectLabelError(c, err)
	}
	return c.JSON(http.StatusOK, l)
}

type projectLabelPatchBody struct {
	Name  *string `json:"name"`
	Color *string `json:"color"`
}

// updateProjectLabel implements PATCH
// /api/v1/workspaces/{slug}/project-labels/{labelID}. Partial semantics
// — nil fields are untouched. Member (15)+; the service enforces it.
func (h *ProjectHandler) updateProjectLabel(c *echo.Context) error {
	var body projectLabelPatchBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	l, err := service.UpdateProjectLabel(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("labelID"), CurrentUser(c).ID,
		service.ProjectLabelPatch{Name: body.Name, Color: body.Color})
	if err != nil {
		return projectLabelError(c, err)
	}
	return c.JSON(http.StatusOK, l)
}

// deleteProjectLabel implements DELETE
// /api/v1/workspaces/{slug}/project-labels/{labelID}. 204 on success;
// the label's project assignments cascade, the projects themselves are
// untouched. Member (15)+; the service enforces it.
func (h *ProjectHandler) deleteProjectLabel(c *echo.Context) error {
	err := service.DeleteProjectLabel(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("labelID"), CurrentUser(c).ID)
	if err != nil {
		return projectLabelError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// assignProjectLabel implements POST
// /api/v1/workspaces/{slug}/projects/{identifier}/project-labels/{labelID}.
// 204 on success; duplicate assignment is 409. Member (15)+; the
// service enforces it.
func (h *ProjectHandler) assignProjectLabel(c *echo.Context) error {
	err := service.AssignProjectLabel(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), c.Param("labelID"), CurrentUser(c).ID)
	if err != nil {
		return projectLabelError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// unassignProjectLabel implements DELETE
// /api/v1/workspaces/{slug}/projects/{identifier}/project-labels/{labelID}.
// Idempotent: 204 even when the label was never assigned. Member
// (15)+; the service enforces it.
func (h *ProjectHandler) unassignProjectLabel(c *echo.Context) error {
	err := service.UnassignProjectLabel(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), c.Param("labelID"), CurrentUser(c).ID)
	if err != nil {
		return projectLabelError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// parseProjectLabelParam parses the repeatable, comma-separated ?label=
// filter (C76T0 — same discipline as C75's ?parent=/?epic=): every value
// must be a UUID; garbage is 400 naming the bad value, never silently
// ignored. The service re-validates existence (unknown id → 400) as
// defense-in-depth for direct internal callers.
func parseProjectLabelParam(qp map[string][]string) ([]string, error) {
	var out []string
	for _, raw := range qp["label"] {
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if _, err := uuid.Parse(part); err != nil {
				return nil, fmt.Errorf("invalid label: %q is not a UUID", part)
			}
			out = append(out, part)
		}
	}
	return out, nil
}
