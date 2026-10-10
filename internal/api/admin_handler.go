package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// AdminHandler serves the instance-admin endpoints (C5T0): stats, user
// management (promote/demote, deactivate/reactivate) and workspace usage
// overview + deletion. Every route sits behind RequireAuth +
// RequireAdmin — instance admin is the only gate; there is no workspace
// tenancy here.
type AdminHandler struct {
	Pool *pgxpool.Pool
}

// RequireAdmin is the instance-admin middleware: it layers on top of
// RequireAuth (which must already have run) and answers 403 unless the
// authenticated user has is_admin=true. Non-admin callers learn nothing
// beyond "forbidden" — no enumeration of what exists behind the gate.
func RequireAdmin(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c *echo.Context) error {
		u := CurrentUser(c)
		if u == nil {
			return WriteError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized", nil)
		}
		if !u.IsAdmin {
			return WriteError(c, http.StatusForbidden, ErrCodeForbidden, "forbidden", nil)
		}
		return next(c)
	}
}

// RegisterAdminRoutes mounts the admin endpoints. Call this before the
// SPA catch-all so API routes are never shadowed.
func RegisterAdminRoutes(e *echo.Echo, h *AdminHandler) {
	g := e.Group("/api/v1/admin", RequireAuth(h.Pool), RequireAdmin)
	g.GET("/stats", h.getStats)
	g.GET("/users", h.listUsers)
	g.PATCH("/users/:id", h.patchUser)
	g.POST("/users/:id/deactivate", h.deactivateUser)
	g.POST("/users/:id/reactivate", h.reactivateUser)
	g.GET("/workspaces", h.listWorkspaces)
	g.DELETE("/workspaces/:id", h.deleteWorkspace)
	g.GET("/audit-log", h.getAuditLog)
}

// adminError maps service sentinel errors to HTTP statuses. Unknown
// errors are 500 with no detail leaked.
func adminError(c *echo.Context, err error) error {
	switch {
	case errors.Is(err, service.ErrUserNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeUserNotFound, "user not found", nil)
	case errors.Is(err, service.ErrNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "workspace not found", nil)
	case errors.Is(err, service.ErrAdminSelfDemote):
		return WriteError(c, http.StatusConflict, ErrCodeConflict, "cannot demote your own admin status", nil)
	case errors.Is(err, service.ErrAdminSelfDeactivate):
		return WriteError(c, http.StatusConflict, ErrCodeConflict, "cannot deactivate your own account", nil)
	case errors.Is(err, service.ErrLastAdmin):
		return WriteError(c, http.StatusConflict, ErrCodeConflict, "cannot demote the last admin", nil)
	case errors.Is(err, service.ErrAdminConfirmMismatch):
		return WriteError(c, http.StatusConflict, ErrCodeConflict, "workspace name confirmation does not match", nil)
	default:
		return WriteInternalError(c)
	}
}

// pageParams parses the shared page/per_page contract: page defaults to
// 1, per_page to 25; per_page outside 1-100 is rejected (400, never
// silently clamped — same fail-fast rule as every other list endpoint),
// as is page < 1.
func pageParams(c *echo.Context) (page, perPage int, err error) {
	page, perPage = 1, 25
	if s := c.QueryParam("page"); s != "" {
		p, convErr := strconv.Atoi(s)
		if convErr != nil || p < 1 {
			return 0, 0, WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid page: want >= 1", nil)
		}
		page = p
	}
	if s := c.QueryParam("per_page"); s != "" {
		p, convErr := strconv.Atoi(s)
		if convErr != nil || p < 1 || p > 100 {
			return 0, 0, WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid per_page: want 1-100", nil)
		}
		perPage = p
	}
	return page, perPage, nil
}

// pageEnvelope is the paginated list envelope: items plus the total row
// count so the UI can render page controls.
func pageEnvelope(items any, total int64, page, perPage int) map[string]any {
	return map[string]any{
		"items":    items,
		"total":    total,
		"page":     page,
		"per_page": perPage,
	}
}

// getStats implements GET /api/v1/admin/stats: instance-wide counts.
func (h *AdminHandler) getStats(c *echo.Context) error {
	stats, err := service.GetAdminStats(c.Request().Context(), h.Pool)
	if err != nil {
		return adminError(c, err)
	}
	return c.JSON(http.StatusOK, stats)
}

// listUsers implements GET /api/v1/admin/users: paginated user directory
// with admin flags and workspace counts.
func (h *AdminHandler) listUsers(c *echo.Context) error {
	page, perPage, err := pageParams(c)
	if err != nil {
		return err
	}
	users, total, err := service.ListAdminUsers(c.Request().Context(), h.Pool,
		perPage, (page-1)*perPage)
	if err != nil {
		return adminError(c, err)
	}
	return c.JSON(http.StatusOK, pageEnvelope(users, total, page, perPage))
}

type patchUserBody struct {
	IsAdmin *bool `json:"is_admin"`
}

// patchUser implements PATCH /api/v1/admin/users/:id: flips the
// instance-admin flag. Self-demotion is 409; demoting the last remaining
// admin is 409. A missing is_admin field is 400 — the client must say
// what it wants, explicitly.
func (h *AdminHandler) patchUser(c *echo.Context) error {
	id := c.Param("id")
	if !validUUID(id) {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid user id", nil)
	}
	var body patchUserBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	if body.IsAdmin == nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "is_admin is required", nil)
	}
	if err := service.SetUserAdmin(c.Request().Context(), h.Pool,
		CurrentUser(c).ID, id, *body.IsAdmin, c.RealIP()); err != nil {
		return adminError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]bool{"ok": true})
}

// deactivateUser implements POST /api/v1/admin/users/:id/deactivate:
// disables the account and revokes all its sessions immediately.
func (h *AdminHandler) deactivateUser(c *echo.Context) error {
	id := c.Param("id")
	if !validUUID(id) {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid user id", nil)
	}
	if err := service.DeactivateUser(c.Request().Context(), h.Pool,
		CurrentUser(c).ID, id, c.RealIP()); err != nil {
		return adminError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]bool{"ok": true})
}

// reactivateUser implements POST /api/v1/admin/users/:id/reactivate:
// re-enables a deactivated account. Sessions revoked at deactivation
// stay revoked — the user logs in fresh.
func (h *AdminHandler) reactivateUser(c *echo.Context) error {
	id := c.Param("id")
	if !validUUID(id) {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid user id", nil)
	}
	if err := service.ReactivateUser(c.Request().Context(), h.Pool,
		CurrentUser(c).ID, id, c.RealIP()); err != nil {
		return adminError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]bool{"ok": true})
}

// listWorkspaces implements GET /api/v1/admin/workspaces: every workspace
// with member/project/issue usage counts, paginated.
func (h *AdminHandler) listWorkspaces(c *echo.Context) error {
	page, perPage, err := pageParams(c)
	if err != nil {
		return err
	}
	workspaces, total, err := service.ListAdminWorkspaces(c.Request().Context(), h.Pool,
		perPage, (page-1)*perPage)
	if err != nil {
		return adminError(c, err)
	}
	return c.JSON(http.StatusOK, pageEnvelope(workspaces, total, page, perPage))
}

// deleteWorkspace implements DELETE /api/v1/admin/workspaces/:id: removes
// the workspace (by id or slug) and everything in it via FK cascades.
// The ?confirm=<workspace-name> param must exactly match the workspace's
// name — the typed confirmation is enforced server-side, not just in
// the UI, because this endpoint is destructive. Missing confirm is 400,
// mismatch is 409.
func (h *AdminHandler) deleteWorkspace(c *echo.Context) error {
	confirm := c.QueryParam("confirm")
	if confirm == "" {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "confirm query param is required: pass the workspace name", nil)
	}
	if err := service.DeleteWorkspaceAsAdmin(c.Request().Context(), h.Pool,
		CurrentUser(c).ID, c.Param("id"), confirm, c.RealIP()); err != nil {
		return adminError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// getAuditLog implements GET /api/v1/admin/audit-log: the append-only
// admin audit log, newest first, paginated like every other admin list
// endpoint. Exact-match filters: action, actor_id (must be a uuid), and
// entity_type. The read path never mutates audit_log — there is no
// update/delete for it anywhere.
func (h *AdminHandler) getAuditLog(c *echo.Context) error {
	page, perPage, err := pageParams(c)
	if err != nil {
		return err
	}
	filter := service.AuditLogFilter{
		Action:     c.QueryParam("action"),
		ActorID:    c.QueryParam("actor_id"),
		EntityType: c.QueryParam("entity_type"),
	}
	if filter.ActorID != "" && !validUUID(filter.ActorID) {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid actor_id: want uuid", nil)
	}
	entries, total, err := service.ListAuditLog(c.Request().Context(), h.Pool,
		perPage, (page-1)*perPage, filter)
	if err != nil {
		return adminError(c, err)
	}
	return c.JSON(http.StatusOK, pageEnvelope(entries, total, page, perPage))
}
