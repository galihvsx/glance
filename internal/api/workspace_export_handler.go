package api

// Workspace data export endpoint (C10T3):
//   GET /api/v1/workspaces/:slug/export
//
// Admin-only (role 20): streams the whole workspace as a single JSON
// archive (format "glance-export/1") via service.StreamWorkspaceExport.
// The archive streams with chunked encoding — the service encodes row by
// row and flushes after each value, so server memory stays flat
// regardless of workspace size.
//
// Gating happens BEFORE any response byte is committed: GetWorkspace
// resolves the actor (non-member → 404, never a hint the slug exists)
// and the role check keeps members/guests at 403, so the
// Content-Disposition header is set only for callers who will actually
// get the archive. A 403/404 response never carries archive bytes.
//
// Content-Disposition: attachment; the filename is
// glance-<slug>-export-<YYYY-MM-DD>.json (UTC date, canonical slug —
// filename-safe by the slug contract).
//
// The archive carries attachment METADATA only (filename, content_type,
// size_bytes): binaries are not included. Archives are restored by the
// importer at POST /api/v1/workspaces/{slug}/import (C11T0), which reads
// against the format version pin and the schema_note embedded in the
// archive.

import (
	"fmt"
	"net/http"
	"time"

	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// exportWorkspace implements GET /api/v1/workspaces/{slug}/export.
func (h *WorkspaceHandler) exportWorkspace(c *echo.Context) error {
	ctx := c.Request().Context()
	slug := c.Param("slug")
	actorID := CurrentUser(c).ID

	ws, role, err := service.GetWorkspace(ctx, h.Pool, slug, actorID)
	if err != nil {
		return workspaceError(c, err)
	}
	if role != service.RoleAdmin {
		return workspaceError(c, service.ErrForbidden)
	}

	filename := fmt.Sprintf("glance-%s-export-%s.json", ws.Slug, time.Now().UTC().Format("2006-01-02"))
	res := c.Response()
	res.Header().Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	res.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	// c.Response() implements http.Flusher: the service flushes after
	// every encoded row, producing chunked transfer encoding.
	if _, ok := any(res).(http.Flusher); !ok {
		return WriteInternalError(c)
	}
	// StreamWorkspaceExport gates inside its snapshot transaction before
	// writing, so a failure here is still pre-commit: answering with a
	// status is safe. A mid-stream write failure (client gone) returns
	// the write error for echo to log.
	if err := service.StreamWorkspaceExport(ctx, h.Pool, slug, actorID, res); err != nil {
		return workspaceError(c, err)
	}
	return nil
}
