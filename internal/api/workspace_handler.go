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
	g.GET("/:slug/members", h.listMembers)
	g.DELETE("/:slug/members/:user_id", h.removeMember)
	g.POST("/:slug/invites", h.inviteMembers)
}

// workspaceError maps service sentinel errors to HTTP statuses. Unknown
// errors are 500 with no detail leaked.
func workspaceError(c *echo.Context, err error) error {
	switch {
	// Admin-only failure modes: the workspace is confirmed to exist and
	// the caller is a confirmed admin here, so these must not surface as
	// "workspace not found" (that message is reserved for the
	// actor-resolution path: bad slug or non-member caller).
	case errors.Is(err, service.ErrUserNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeUserNotFound, "user not found", nil)
	case errors.Is(err, service.ErrMemberNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeMemberNotFound, "member not found", nil)
	case errors.Is(err, service.ErrNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "workspace not found", nil)
	case errors.Is(err, service.ErrForbidden):
		return WriteError(c, http.StatusForbidden, ErrCodeForbidden, "forbidden", nil)
	case errors.Is(err, service.ErrSlugConflict):
		return WriteError(c, http.StatusConflict, ErrCodeConflict, "slug already taken", nil)
	case errors.Is(err, service.ErrInvalidSlug):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid slug: use lowercase letters, numbers and hyphens", nil)
	case errors.Is(err, service.ErrInvalidRole):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid role: must be 5 (guest), 15 (member) or 20 (admin)", nil)
	case errors.Is(err, service.ErrLastAdmin):
		return WriteError(c, http.StatusConflict, ErrCodeConflict, "cannot remove or demote the last admin", nil)
	default:
		return WriteInternalError(c)
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
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	ws, err := service.CreateWorkspace(c.Request().Context(), h.Pool, body.Name, body.Slug, CurrentUser(c).ID)
	if err != nil {
		if errors.Is(err, service.ErrNameRequired) {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "name is required", nil)
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
		return WriteInternalError(c)
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
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	ws, err := service.UpdateWorkspace(c.Request().Context(), h.Pool, c.Param("slug"), CurrentUser(c).ID, body.Name)
	if err != nil {
		if errors.Is(err, service.ErrNameRequired) {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "name is required", nil)
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
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	if !validUUID(body.UserID) {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid user_id", nil)
	}
	if err := service.UpsertMember(c.Request().Context(), h.Pool, c.Param("slug"), CurrentUser(c).ID, body.UserID, body.Role); err != nil {
		return workspaceError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]bool{"ok": true})
}

// removeMember implements DELETE /api/v1/workspaces/{slug}/members/{user_id}.
// Admin only; removing the last admin is refused.
func (h *WorkspaceHandler) removeMember(c *echo.Context) error {
	userID, ok := requireUUIDParam(c, "user_id", "user_id")
	if !ok {
		return nil
	}
	if err := service.RemoveMember(c.Request().Context(), h.Pool, c.Param("slug"), CurrentUser(c).ID, userID); err != nil {
		return workspaceError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]bool{"ok": true})
}

// listMembers implements GET /api/v1/workspaces/{slug}/members: the
// workspace's members with their user identity. Any member may read — the
// issue page's assignee picker needs it. Wrapped shape: {"members": [...]}.
func (h *WorkspaceHandler) listMembers(c *echo.Context) error {
	members, err := service.ListMembers(c.Request().Context(), h.Pool, c.Param("slug"), CurrentUser(c).ID)
	if err != nil {
		return workspaceError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"members": members})
}

type inviteMembersBody struct {
	Emails []string `json:"emails"`
	Role   int      `json:"role"`
}

// inviteMembers implements POST /api/v1/workspaces/{slug}/invites: adds
// registered users as workspace members by email. Admin only. Every email
// gets a per-email result (invited | already-member | not-registered |
// invalid) — unknown emails are results, not errors, so the onboarding
// wizard keeps them as "Pending" without failing. 200 with the wrapped
// {"results": [...]} shape.
func (h *WorkspaceHandler) inviteMembers(c *echo.Context) error {
	var body inviteMembersBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	results, err := service.InviteMembers(c.Request().Context(), h.Pool, c.Param("slug"), CurrentUser(c).ID, body.Emails, body.Role)
	if err != nil {
		return workspaceError(c, err)
	}
	if results == nil {
		results = []service.InviteResult{}
	}
	return c.JSON(http.StatusOK, map[string]any{"results": results})
}
