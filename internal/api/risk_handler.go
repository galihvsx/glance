package api

// Project risk radar endpoints (C30T1).
//
//   GET /api/v1/workspaces/:slug/risk
//     Per-project risk report (risk score 0-100, level, contributing
//     factors, open counts). Any workspace member (guest 5+) may read.
//     Aggregates only project-level signals; no per-member data leaks.
//
// Error codes follow the api envelope conventions: ErrNotFound → 404
// workspace. A bad slug or a non-member caller is a 404 (never a
// leak); a caller below minRole gets a 403.

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// RiskHandler serves the risk radar endpoints.
type RiskHandler struct {
	Pool *pgxpool.Pool
}

// RegisterRiskRoutes mounts the risk endpoints on the workspace group.
// Call this before the SPA catch-all so API routes are never shadowed.
func RegisterRiskRoutes(e *echo.Echo, h *RiskHandler) {
	g := e.Group("/api/v1/workspaces/:slug", RequireAuth(h.Pool))
	g.GET("/risk", h.getRisk)
}

// riskError maps risk sentinel errors to HTTP statuses.
func riskError(c *echo.Context, err error) error {
	switch {
	case errors.Is(err, service.ErrNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "workspace not found", nil)
	default:
		return WriteInternalError(c)
	}
}

// resolveRiskWorkspace resolves :slug (slug or UUID) to the workspace
// id for the calling member. A bad slug or a non-member caller gets a
// 404; a caller below minRole gets a 403 (never a leak). minRole 0
// skips the role check (any member may read). ok=false means the
// response was already written — the caller must return nil.
func (h *RiskHandler) resolveRiskWorkspace(c *echo.Context, minRole int) (wsID uuid.UUID, ok bool) {
	var rawID string
	var role int
	err := h.Pool.QueryRow(c.Request().Context(),
		`SELECT w.id::text, m.role
		   FROM workspaces w
		   JOIN workspace_members m ON m.workspace_id = w.id
		  WHERE (w.slug = $1 OR w.id::text = $1)
		    AND m.user_id = $2::uuid`,
		c.Param("slug"), CurrentUser(c).ID).Scan(&rawID, &role)
	if err != nil {
		WriteError(c, http.StatusNotFound, ErrCodeNotFound, "workspace not found", nil)
		return uuid.Nil, false
	}
	if role < minRole {
		WriteError(c, http.StatusForbidden, ErrCodeForbidden, "forbidden", nil)
		return uuid.Nil, false
	}
	id, err := uuid.Parse(rawID)
	if err != nil {
		WriteInternalError(c) // cannot happen: uuid from the database
		return uuid.Nil, false
	}
	return id, true
}

// ---------- get ----------

// getRisk implements GET /api/v1/workspaces/:slug/risk: the
// per-project risk report. Any workspace member may read.
func (h *RiskHandler) getRisk(c *echo.Context) error {
	wsID, ok := h.resolveRiskWorkspace(c, 0)
	if !ok {
		return nil
	}
	rep, err := service.GetRiskReport(c.Request().Context(), h.Pool, wsID)
	if err != nil {
		return riskError(c, err)
	}
	return c.JSON(http.StatusOK, rep)
}
