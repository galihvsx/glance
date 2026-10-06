package api

import (
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// WorkspaceHandler serves the workspace endpoints (Task 11): CRUD plus
// member management. Every route sits behind RequireAuth — workspaces are
// the tenancy boundary, so there is no public surface here.
type WorkspaceHandler struct {
	Pool *pgxpool.Pool
}

// RegisterWorkspaceRoutes mounts the workspace endpoints. Call this before
// the SPA catch-all so API routes are never shadowed.
func RegisterWorkspaceRoutes(e *echo.Echo, h *WorkspaceHandler) {
	g := e.Group("/api/v1/workspaces", RequireAuth(h.Pool))
	g.POST("", h.createWorkspace)
	g.GET("", h.listWorkspaces)
	g.GET("/:slug", h.getWorkspace)
	g.PATCH("/:slug", h.updateWorkspace)
	g.POST("/:slug/members", h.upsertMember)
	g.DELETE("/:slug/members/:user_id", h.removeMember)
}

// workspaceError maps service sentinel errors to HTTP statuses. Unknown
// errors are 500 with no detail leaked.
func workspaceError(c *echo.Context, err error) error {
	switch {
	case errors.Is(err, service.ErrNotFound):
		return c.JSON(http.StatusNotFound, map[string]string{"error": "workspace not found"})
	case errors.Is(err, service.ErrForbidden):
		return c.JSON(http.StatusForbidden, map[string]string{"error": "forbidden"})
	case errors.Is(err, service.ErrSlugConflict):
		return c.JSON(http.StatusConflict, map[string]string{"error": "slug already taken"})
	case errors.Is(err, service.ErrInvalidSlug):
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid slug: use lowercase letters, numbers and hyphens"})
	case errors.Is(err, service.ErrInvalidRole):
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid role: must be 5 (guest), 15 (member) or 20 (admin)"})
	case errors.Is(err, service.ErrLastAdmin):
		return c.JSON(http.StatusConflict, map[string]string{"error": "cannot remove or demote the last admin"})
	default:
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}
}

type createWorkspaceBody struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// createWorkspace implements POST /api/v1/workspaces: creates the
// workspace with the caller as admin. 201 with the workspace JSON.
func (h *WorkspaceHandler) createWorkspace(c *echo.Context) error {
	var body createWorkspaceBody
	if err := c.Bind(&body); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}
	ws, err := service.CreateWorkspace(c.Request().Context(), h.Pool, body.Name, body.Slug, CurrentUser(c).ID)
	if err != nil {
		if errors.Is(err, service.ErrNameRequired) {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "name is required"})
		}
		return workspaceError(c, err)
	}
	return c.JSON(http.StatusCreated, ws)
}

// listWorkspaces implements GET /api/v1/workspaces: the caller's
// workspaces with their role in each.
func (h *WorkspaceHandler) listWorkspaces(c *echo.Context) error {
	workspaces, err := service.ListWorkspaces(c.Request().Context(), h.Pool, CurrentUser(c).ID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}
	return c.JSON(http.StatusOK, map[string]any{"workspaces": workspaces})
}

// getWorkspace implements GET /api/v1/workspaces/{slug}. Non-members get
// 404 (see service.GetWorkspace) — never a hint the slug exists.
func (h *WorkspaceHandler) getWorkspace(c *echo.Context) error {
	ws, role, err := service.GetWorkspace(c.Request().Context(), h.Pool, c.Param("slug"), CurrentUser(c).ID)
	if err != nil {
		return workspaceError(c, err)
	}
	return c.JSON(http.StatusOK, service.WorkspaceMembership{Workspace: *ws, Role: role})
}

type updateWorkspaceBody struct {
	Name string `json:"name"`
}

// updateWorkspace implements PATCH /api/v1/workspaces/{slug}: rename.
// Admin only (the service enforces it).
func (h *WorkspaceHandler) updateWorkspace(c *echo.Context) error {
	var body updateWorkspaceBody
	if err := c.Bind(&body); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}
	ws, err := service.UpdateWorkspace(c.Request().Context(), h.Pool, c.Param("slug"), CurrentUser(c).ID, body.Name)
	if err != nil {
		if errors.Is(err, service.ErrNameRequired) {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "name is required"})
		}
		return workspaceError(c, err)
	}
	return c.JSON(http.StatusOK, ws)
}

type upsertMemberBody struct {
	UserID string `json:"user_id"`
	Role   int    `json:"role"`
}

// upsertMember implements POST /api/v1/workspaces/{slug}/members: adds a
// member or changes their role. Admin only.
func (h *WorkspaceHandler) upsertMember(c *echo.Context) error {
	var body upsertMemberBody
	if err := c.Bind(&body); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}
	if !validUUID(body.UserID) {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid user_id"})
	}
	if err := service.UpsertMember(c.Request().Context(), h.Pool, c.Param("slug"), CurrentUser(c).ID, body.UserID, body.Role); err != nil {
		return workspaceError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]bool{"ok": true})
}

// removeMember implements DELETE /api/v1/workspaces/{slug}/members/{user_id}.
// Admin only; removing the last admin is refused.
func (h *WorkspaceHandler) removeMember(c *echo.Context) error {
	userID := c.Param("user_id")
	if !validUUID(userID) {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid user_id"})
	}
	if err := service.RemoveMember(c.Request().Context(), h.Pool, c.Param("slug"), CurrentUser(c).ID, userID); err != nil {
		return workspaceError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]bool{"ok": true})
}
