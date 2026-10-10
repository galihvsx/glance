package api

// Scheduled backup admin endpoints (C15T2):
//   GET  /api/v1/admin/backups      — backup run history, newest first,
//                                     paginated with the pageEnvelope
//                                     contract; optional
//                                     ?workspace_slug= filter. The
//                                     response also carries the
//                                     schedule/retention config so the
//                                     admin UI can display it without a
//                                     second round-trip.
//   POST /api/v1/admin/backups/run  — trigger a backup now. Optional
//                                     JSON body {"workspace_slug"} backs
//                                     up one workspace; an empty body
//                                     backs up every workspace. Runs
//                                     synchronously and returns the
//                                     recorded run rows.
//
// Both sit behind RequireAuth + RequireAdmin like every other admin
// route. The manual trigger works even when the schedule is disabled
// (GLANCE_BACKUP_INTERVAL empty): the dir/retention defaults still
// apply.

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// backupConfigView is the schedule/retention config as the admin UI
// needs it. Interval is the duration string ("" when disabled); dir is
// the configured target directory.
type backupConfigView struct {
	Enabled   bool   `json:"enabled"`
	Interval  string `json:"interval"`
	Dir       string `json:"dir"`
	Retention int    `json:"retention"`
}

func (h *AdminHandler) backupConfig() backupConfigView {
	interval := ""
	if h.BackupCfg.BackupEnabled() {
		interval = h.BackupCfg.Interval.String()
	}
	return backupConfigView{
		Enabled:   h.BackupCfg.BackupEnabled(),
		Interval:  interval,
		Dir:       h.BackupCfg.Dir,
		Retention: h.BackupCfg.Retention,
	}
}

// listBackups implements GET /api/v1/admin/backups.
func (h *AdminHandler) listBackups(c *echo.Context) error {
	page, perPage, err := pageParams(c)
	if err != nil {
		return err
	}
	runs, total, err := service.ListBackups(c.Request().Context(), h.Pool,
		perPage, (page-1)*perPage, c.QueryParam("workspace_slug"))
	if err != nil {
		return adminError(c, err)
	}
	out := pageEnvelope(runs, total, page, perPage)
	out["config"] = h.backupConfig()
	return c.JSON(http.StatusOK, out)
}

// runBackupRequest is the optional POST body for the manual trigger.
type runBackupRequest struct {
	WorkspaceSlug string `json:"workspace_slug"`
}

// runBackupNow implements POST /api/v1/admin/backups/run.
func (h *AdminHandler) runBackupNow(c *echo.Context) error {
	ctx := c.Request().Context()
	var req runBackupRequest
	// An empty body means "all workspaces" — not an error. Read the
	// body manually instead of c.Bind so an empty POST is valid.
	if c.Request().Body != nil {
		data, err := io.ReadAll(c.Request().Body)
		if err != nil {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "cannot read request body", nil)
		}
		if len(bytes.TrimSpace(data)) > 0 {
			if err := json.Unmarshal(data, &req); err != nil {
				return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body: want JSON", nil)
			}
		}
	}
	triggeredBy := "admin"
	if u := CurrentUser(c); u != nil && u.Email != "" {
		triggeredBy = u.Email
	}
	if req.WorkspaceSlug != "" {
		run, err := service.RunBackup(ctx, h.Pool, h.BackupCfg.Dir,
			h.BackupCfg.Retention, req.WorkspaceSlug, triggeredBy)
		if err != nil {
			return backupRunError(c, err)
		}
		return c.JSON(http.StatusOK, map[string]any{"backups": []service.BackupRun{run}})
	}
	runs, err := service.RunAllBackups(ctx, h.Pool, h.BackupCfg.Dir,
		h.BackupCfg.Retention, triggeredBy)
	if err != nil {
		// Partial failure: the runs that succeeded are recorded and
		// returned; the joined per-workspace errors ride the 207 so
		// the UI can show both. (207 Multi-Status is the honest code
		// for "some succeeded, some failed".)
		return c.JSON(http.StatusMultiStatus, map[string]any{
			"backups": runs,
			"error":   err.Error(),
		})
	}
	return c.JSON(http.StatusOK, map[string]any{"backups": runs})
}

// backupRunError maps backup failures to statuses: unknown workspace is
// 404, everything else is a 500 with the message (admins are the only
// audience; no detail is leaked to outsiders).
func backupRunError(c *echo.Context, err error) error {
	if errors.Is(err, service.ErrNotFound) {
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "workspace not found", nil)
	}
	return WriteError(c, http.StatusInternalServerError, ErrCodeInternal, "backup failed: "+err.Error(), nil)
}
