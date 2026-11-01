package api

// Saved view endpoints (C9T2; sharing added C10T1): per-user named
// filter+display presets on a project, persisted server-side so they roam
// across devices. A view is private by default; its owner may share it
// with the project (PATCH {shared}), making it visible to every project
// member as a read-only row annotated with owner_id/owner_name.
//
//   GET    /api/v1/projects/{id}/views            → 200 {views: [...]}
//
// The list carries the caller's own views plus the project's shared
// views. Shared rows are read-only for non-owners: any PATCH on a view
// the caller does not own is 403 (ErrForbidden), and DELETE is
// owner-only with one moderation exception — a workspace admin (role 20)
// may delete any SHARED view.
//
//   POST   /api/v1/projects/{id}/views            → 201 the view (409 on duplicate name)
//   PATCH  /api/v1/projects/{id}/views/{viewId}   → 200 the view (rename / set default / share)
//   DELETE /api/v1/projects/{id}/views/{viewId}   → 204 (idempotent)
//
// Workspace-scoped twin (C113T0, same payloads and status codes):
//
//   GET    /api/v1/workspaces/{slug}/views
//   POST   /api/v1/workspaces/{slug}/views
//   PATCH  /api/v1/workspaces/{slug}/views/{viewId}
//   DELETE /api/v1/workspaces/{slug}/views/{viewId}
//
// All auth-only, scoped to members of the project (404 "project not
// found" otherwise — same tenancy rule as the rest of the project API).
// Errors use the spec §5 envelope.

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// IssueViewHandler serves the saved-view routes.
type IssueViewHandler struct {
	Pool *pgxpool.Pool
}

// RegisterIssueViewRoutes mounts the saved-view endpoints, project-scoped
// and (C113T0) workspace-scoped. Call before the SPA catch-all.
func RegisterIssueViewRoutes(e *echo.Echo, h *IssueViewHandler) {
	g := e.Group("/api/v1/projects/:id/views", RequireAuth(h.Pool))
	g.GET("", h.listViews)
	g.POST("", h.createView)
	g.PATCH("/:viewId", h.updateView)
	g.DELETE("/:viewId", h.deleteView)

	wg := e.Group("/api/v1/workspaces/:slug/views", RequireAuth(h.Pool))
	wg.GET("", h.listWorkspaceViews)
	wg.POST("", h.createWorkspaceView)
	wg.PATCH("/:viewId", h.updateWorkspaceView)
	wg.DELETE("/:viewId", h.deleteWorkspaceView)
}

// issueViewError maps saved-view sentinel errors to HTTP statuses.
func issueViewError(c *echo.Context, err error) error {
	switch {
	case errors.Is(err, service.ErrViewProjectNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "project not found", nil)
	case errors.Is(err, service.ErrViewWorkspaceNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "workspace not found", nil)
	case errors.Is(err, service.ErrViewNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "saved view not found", nil)
	case errors.Is(err, service.ErrViewNameTaken):
		return WriteError(c, http.StatusConflict, ErrCodeConflict, "a view with that name already exists", nil)
	case errors.Is(err, service.ErrInvalidViewName):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "name is required (max 64 characters)", nil)
	case errors.Is(err, service.ErrInvalidViewFilters):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "filters must be a JSON object", nil)
	case errors.Is(err, service.ErrForbidden):
		return WriteError(c, http.StatusForbidden, ErrCodeForbidden, "only the view owner can change it", nil)
	default:
		return WriteInternalError(c)
	}
}

// issueViewBody is the POST payload: name, filters (IssueFilters object,
// stored opaquely), and optional display settings.
type issueViewBody struct {
	Name    string          `json:"name"`
	Filters json.RawMessage `json:"filters"`
	Display json.RawMessage `json:"display"`
}

// listViews implements GET /api/v1/projects/{id}/views: the caller's own
// views plus the project's shared views (annotated with owner_id,
// owner_name, shared=true), oldest first. 404 when the project is
// missing or the caller is not a member.
func (h *IssueViewHandler) listViews(c *echo.Context) error {
	u := CurrentUser(c)
	if u == nil {
		return WriteError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized", nil)
	}
	projectID, ok := requireUUIDParam(c, "id", "project id")
	if !ok {
		return nil
	}
	views, err := service.ListIssueViews(c.Request().Context(), h.Pool, u.ID, projectID)
	if err != nil {
		return issueViewError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"views": views})
}

// createView implements POST /api/v1/projects/{id}/views: saves a view for
// the caller. 201 with the view; 409 when the (case-insensitive) name is
// taken by another of the caller's views on the project.
func (h *IssueViewHandler) createView(c *echo.Context) error {
	u := CurrentUser(c)
	if u == nil {
		return WriteError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized", nil)
	}
	projectID, ok := requireUUIDParam(c, "id", "project id")
	if !ok {
		return nil
	}
	var body issueViewBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	view, err := service.CreateIssueView(c.Request().Context(), h.Pool, u.ID, projectID,
		service.IssueViewInput{
			Name:    body.Name,
			Filters: body.Filters,
			Display: body.Display,
		})
	if err != nil {
		return issueViewError(c, err)
	}
	return c.JSON(http.StatusCreated, view)
}

// issueViewPatchBody is the PATCH payload: name, is_default, and/or
// shared (owner-only — the service 403s a non-owner). At least one known
// key must be present.
type issueViewPatchBody struct {
	Name      *string `json:"name"`
	IsDefault *bool   `json:"is_default"`
	Shared    *bool   `json:"shared"`
}

// updateView implements PATCH /api/v1/projects/{id}/views/{viewId}:
// renames the view, flips its default flag, and/or toggles sharing with
// the project. Owner-only: 403 on another user's shared view, 404 on an
// unknown (or another user's private) view; 409 on a name clash.
func (h *IssueViewHandler) updateView(c *echo.Context) error {
	u := CurrentUser(c)
	if u == nil {
		return WriteError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized", nil)
	}
	projectID, ok := requireUUIDParam(c, "id", "project id")
	if !ok {
		return nil
	}
	viewID, ok := requireUUIDParam(c, "viewId", "view id")
	if !ok {
		return nil
	}
	var body issueViewPatchBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	if body.Name == nil && body.IsDefault == nil && body.Shared == nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "nothing to update", nil)
	}
	view, err := service.UpdateIssueView(c.Request().Context(), h.Pool, u.ID, projectID, viewID,
		service.IssueViewPatch{Name: body.Name, IsDefault: body.IsDefault, Shared: body.Shared})
	if err != nil {
		return issueViewError(c, err)
	}
	return c.JSON(http.StatusOK, view)
}

// deleteView implements DELETE /api/v1/projects/{id}/views/{viewId}.
// Owner-only, with the moderation exception that a workspace admin may
// delete any SHARED view. 403 for a non-owner (non-admin) deleting a
// shared view; always 204 for unknown ids or another user's private view
// (idempotent, no existence leak).
func (h *IssueViewHandler) deleteView(c *echo.Context) error {
	u := CurrentUser(c)
	if u == nil {
		return WriteError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized", nil)
	}
	projectID, ok := requireUUIDParam(c, "id", "project id")
	if !ok {
		return nil
	}
	viewID, ok := requireUUIDParam(c, "viewId", "view id")
	if !ok {
		return nil
	}
	if err := service.DeleteIssueView(c.Request().Context(), h.Pool, u.ID, projectID, viewID); err != nil {
		return issueViewError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// ---- Workspace-scoped views (C113T0) ------------------------------------
//
//   GET    /api/v1/workspaces/{slug}/views            → 200 {views: [...]}
//   POST   /api/v1/workspaces/{slug}/views            → 201 the view (409 on duplicate name)
//   PATCH  /api/v1/workspaces/{slug}/views/{viewId}   → 200 the view (rename / set default / share)
//   DELETE /api/v1/workspaces/{slug}/views/{viewId}   → 204 (idempotent)
//
// Payloads mirror the project group exactly; only the scope changes
// (workspace slug in place of the project UUID). Slug resolution 404s
// for non-members exactly like the project group's project resolution
// does — one 404 for missing and non-member alike, so 404s never leak
// workspace existence. The slug is a path segment, not a UUID, so it is
// read verbatim (no requireUUIDParam); the service's membership join
// is the only gate.

// listWorkspaceViews implements GET /api/v1/workspaces/{slug}/views:
// the caller's own workspace views plus the workspace's shared views,
// oldest first. 404 when the workspace is missing or the caller is not
// a member.
func (h *IssueViewHandler) listWorkspaceViews(c *echo.Context) error {
	u := CurrentUser(c)
	if u == nil {
		return WriteError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized", nil)
	}
	views, err := service.ListWorkspaceIssueViews(c.Request().Context(), h.Pool, u.ID, c.Param("slug"))
	if err != nil {
		return issueViewError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"views": views})
}

// createWorkspaceView implements POST /api/v1/workspaces/{slug}/views:
// saves a view for the caller on the workspace. 201 with the view;
// 409 when the (case-insensitive) name is taken by another of the
// caller's views on the workspace.
func (h *IssueViewHandler) createWorkspaceView(c *echo.Context) error {
	u := CurrentUser(c)
	if u == nil {
		return WriteError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized", nil)
	}
	var body issueViewBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	view, err := service.CreateWorkspaceIssueView(c.Request().Context(), h.Pool, u.ID, c.Param("slug"),
		service.IssueViewInput{
			Name:    body.Name,
			Filters: body.Filters,
			Display: body.Display,
		})
	if err != nil {
		return issueViewError(c, err)
	}
	return c.JSON(http.StatusCreated, view)
}

// updateWorkspaceView implements PATCH /api/v1/workspaces/{slug}/views/{viewId}:
// renames the view, flips its default flag, and/or toggles sharing with
// the workspace. Owner-only: 403 on another user's shared view, 404 on
// an unknown (or another user's private) view; 409 on a name clash.
func (h *IssueViewHandler) updateWorkspaceView(c *echo.Context) error {
	u := CurrentUser(c)
	if u == nil {
		return WriteError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized", nil)
	}
	viewID, ok := requireUUIDParam(c, "viewId", "view id")
	if !ok {
		return nil
	}
	var body issueViewPatchBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	if body.Name == nil && body.IsDefault == nil && body.Shared == nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "nothing to update", nil)
	}
	view, err := service.UpdateWorkspaceIssueView(c.Request().Context(), h.Pool, u.ID, c.Param("slug"), viewID,
		service.IssueViewPatch{Name: body.Name, IsDefault: body.IsDefault, Shared: body.Shared})
	if err != nil {
		return issueViewError(c, err)
	}
	return c.JSON(http.StatusOK, view)
}

// deleteWorkspaceView implements DELETE /api/v1/workspaces/{slug}/views/{viewId}.
// Owner-only, with the moderation exception that a workspace admin may
// delete any SHARED view. 403 for a non-owner (non-admin) deleting a
// shared view; always 204 for unknown ids or another user's private view
// (idempotent, no existence leak).
func (h *IssueViewHandler) deleteWorkspaceView(c *echo.Context) error {
	u := CurrentUser(c)
	if u == nil {
		return WriteError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized", nil)
	}
	viewID, ok := requireUUIDParam(c, "viewId", "view id")
	if !ok {
		return nil
	}
	if err := service.DeleteWorkspaceIssueView(c.Request().Context(), h.Pool, u.ID, c.Param("slug"), viewID); err != nil {
		return issueViewError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}
