package api

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// Workspace audit log endpoints (C37T0): workspace-Admin-only reads
// of the append-only workspace_audit_log table. The list is paginated
// (pageParams) with the same filter contract as the service layer;
// the export streams the same filtered rows as CSV.
//
// Gates (enforced in service.ListWorkspaceAuditLog, mapped here):
// non-member → 404 (never leak the workspace exists), member
// non-admin → 403, malformed filter → 400.

// workspaceAuditExportCap bounds the export: at most this many rows
// are written, and X-Truncated: true marks a truncated export. A var
// (not const) so tests can lower it without seeding 10,001 rows.
var workspaceAuditExportCap = 10000

// workspaceAuditError maps workspace-audit sentinel errors to HTTP
// statuses. Unknown errors are 500 with no detail leaked.
func workspaceAuditError(c *echo.Context, err error) error {
	switch {
	case errors.Is(err, service.ErrNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "workspace not found", nil)
	case errors.Is(err, service.ErrForbidden):
		return WriteError(c, http.StatusForbidden, ErrCodeForbidden, "forbidden", nil)
	case errors.Is(err, service.ErrInvalidWorkspaceAuditFilter):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid audit-log filter", nil)
	default:
		return WriteInternalError(c)
	}
}

// workspaceAuditFilter builds the service filter from the request's
// query params. Shared by the list and export endpoints so the two
// can never drift apart.
func workspaceAuditFilter(c *echo.Context) service.WorkspaceAuditFilter {
	return service.WorkspaceAuditFilter{
		Action:     c.QueryParam("action"),
		ActorID:    c.QueryParam("actor_id"),
		Actor:      c.QueryParam("actor"),
		EntityType: c.QueryParam("entity_type"),
		Since:      c.QueryParam("since"),
		Until:      c.QueryParam("until"),
	}
}

// getWorkspaceAuditLog implements
// GET /api/v1/workspaces/{slug}/audit-log: paginated audit entries,
// newest first. Admin only.
func (h *WorkspaceHandler) getWorkspaceAuditLog(c *echo.Context) error {
	page, perPage, err := pageParams(c)
	if err != nil {
		return err
	}
	entries, total, err := service.ListWorkspaceAuditLog(c.Request().Context(), h.Pool,
		c.Param("slug"), CurrentUser(c).ID, perPage, (page-1)*perPage, workspaceAuditFilter(c))
	if err != nil {
		return workspaceAuditError(c, err)
	}
	return c.JSON(http.StatusOK, pageEnvelope(entries, total, page, perPage))
}

// exportWorkspaceAuditLog implements
// GET /api/v1/workspaces/{slug}/audit-log/export?format=csv: the same
// filtered rows as the list endpoint, as a CSV download
// (at,actor_email,action,entity_type,entity_id,ip,meta). Admin only.
// The export is hard-capped at workspaceAuditExportCap rows; a
// truncated export sets X-Truncated: true. The filename is
// audit-log-<slug>-<YYYY-MM-DD>.csv — the slug is filename-safe by
// the slug contract, and it names a workspace the caller just proved
// admin membership of (the list call 404s otherwise).
func (h *WorkspaceHandler) exportWorkspaceAuditLog(c *echo.Context) error {
	if format := c.QueryParam("format"); format != "" && format != "csv" {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "format must be csv", nil)
	}
	slug := c.Param("slug")
	entries, _, err := service.ListWorkspaceAuditLog(c.Request().Context(), h.Pool,
		slug, CurrentUser(c).ID, workspaceAuditExportCap+1, 0, workspaceAuditFilter(c))
	if err != nil {
		return workspaceAuditError(c, err)
	}
	truncated := len(entries) > workspaceAuditExportCap
	if truncated {
		entries = entries[:workspaceAuditExportCap]
	}
	csvBytes, err := service.WorkspaceAuditCSV(entries)
	if err != nil {
		return WriteInternalError(c)
	}
	filename := fmt.Sprintf("audit-log-%s-%s.csv", slug, time.Now().UTC().Format("2006-01-02"))
	c.Response().Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	if truncated {
		c.Response().Header().Set("X-Truncated", "true")
	}
	return c.Blob(http.StatusOK, "text/csv; charset=utf-8", csvBytes)
}
