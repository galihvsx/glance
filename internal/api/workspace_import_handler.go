package api

// Workspace archive import endpoint (C11T0):
//   POST /api/v1/workspaces/:slug/import
//
// Admin-only (role 20): restores a `glance-export/1` JSON archive
// (as produced by GET /api/v1/workspaces/:slug/export) into the
// workspace. The request is multipart/form-data with a "file" field
// holding the archive.
//
// The body is hard-capped (100MB + 1MiB headroom — archives are JSON
// only, no binaries, but a large workspace can legitimately exceed the
// 10MB CSV-import cap) so a hostile body can't buffer unboundedly.
// Gating happens BEFORE the upload body is parsed: GetWorkspace resolves
// the actor (non-member → 404, never a hint the slug exists) and the
// role check keeps members/guests at 403, mirroring exportWorkspace.
//
// 200: the import report {format, source_workspace, target_workspace,
// imported: {...}, skipped: {count, reasons[]}}. Unknown archive format
// versions are refused with 400 (the schema_note contract) and import
// nothing; malformed JSON is 400.

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// archiveImportMaxBytes caps the archive upload body: 100MB + 1MiB
// headroom. Archives are JSON only (binaries are never exported), so
// this bounds the worst case while staying far above the 10MB
// CSV-import cap a busy workspace's archive can legitimately exceed.
const archiveImportMaxBytes = 100<<20 + (1 << 20)

// archiveImportError maps service sentinel errors to HTTP statuses.
func archiveImportError(c *echo.Context, err error) error {
	switch {
	case errors.Is(err, service.ErrArchiveVersion) || errors.Is(err, service.ErrArchiveMalformed):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, err.Error(), nil)
	default:
		return workspaceError(c, err)
	}
}

// importWorkspaceArchive implements POST /api/v1/workspaces/{slug}/import.
func (h *WorkspaceHandler) importWorkspaceArchive(c *echo.Context) error {
	ctx := c.Request().Context()
	slug := c.Param("slug")
	actorID := CurrentUser(c).ID

	// Gate before touching the upload body (mirrors exportWorkspace).
	if _, role, err := service.GetWorkspace(ctx, h.Pool, slug, actorID); err != nil {
		return workspaceError(c, err)
	} else if role != service.RoleAdmin {
		return workspaceError(c, service.ErrForbidden)
	}

	r := c.Request()
	r.Body = http.MaxBytesReader(c.Response(), r.Body, archiveImportMaxBytes)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		return WriteError(c, http.StatusRequestEntityTooLarge, ErrCodePayloadTooLarge,
			"request body exceeds the 100MB archive import size limit", nil)
	}
	f, fh, err := r.FormFile("file")
	if err != nil || fh == nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest,
			`multipart field "file" is required`, nil)
	}
	defer f.Close()

	rep, err := service.ImportWorkspaceArchive(ctx, h.Pool, slug, actorID, f)
	if err != nil {
		return archiveImportError(c, err)
	}
	return c.JSON(http.StatusOK, rep)
}
