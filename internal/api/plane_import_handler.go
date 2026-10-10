package api

// Plane-native export importer HTTP surface (C14T4, cycle 14
// "Plane-native import").
//
//   POST .../imports/plane-import/analyze — multipart `file` = the Plane
//     JSON export (raw .json or .zip containing one .json). Parses and
//     returns the unresolved inventory (service.PlaneImportAnalysis); no
//     writes. Member (any role)+, per design decision 9.
//   POST .../imports/plane-import/execute — multipart `file` +
//     `resolutions` (JSON service.PlaneImportResolutions) + optional
//     `options` (JSON service.PlaneImportExecuteOpts, e.g. strict_states).
//     Imports everything in one transaction; returns the
//     service.PlaneImportReport. Member (15)+.
//
// The request body is hard-capped (256MB, mirroring the service layer's
// parse cap). Parts above ParseMultipartForm's memory threshold spill to
// temp files, so handler memory stays bounded.

import (
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// planeImportHTTPMaxBytes caps the plane-import upload body: 256 MiB,
// mirroring the service layer's parse cap (service.planeImportMaxBytes).
const planeImportHTTPMaxBytes = 256 << 20

// planeAnalyzeMaxWarnings bounds how many of T1's per-row shape failures
// are folded into the analysis warnings.
const planeAnalyzeMaxWarnings = 25

// planeImportError maps plane-import service errors to HTTP statuses.
// Unknown errors go through projectError's tenancy-aware mapping
// (ErrNotFound -> 404 "workspace not found", ErrProjectNotFound ->
// 404 "project not found", ErrForbidden -> 403).
func planeImportError(c *echo.Context, err error) error {
	switch {
	case errors.Is(err, service.ErrPlaneImportShape) ||
		errors.Is(err, service.ErrPlaneImportEmpty) ||
		errors.Is(err, service.ErrPlaneImportMultiProject):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, err.Error(), nil)
	case strings.Contains(err.Error(), "exceeds") && strings.Contains(err.Error(), "size cap"):
		return WriteError(c, http.StatusRequestEntityTooLarge, ErrCodePayloadTooLarge, err.Error(), nil)
	case strings.HasPrefix(err.Error(), "service: plane import resolution") ||
		strings.Contains(err.Error(), "exceeding the soft cap"):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, err.Error(), nil)
	default:
		return projectError(c, err)
	}
}

// planeImportMultipart parses the multipart body for the plane-import
// endpoints and extracts the `file` part. The caller closes the file.
// ok=false means the error was already written.
func planeImportMultipart(c *echo.Context) (multipart.File, bool) {
	r := c.Request()
	r.Body = http.MaxBytesReader(c.Response(), r.Body, planeImportHTTPMaxBytes)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		WriteError(c, http.StatusRequestEntityTooLarge, ErrCodePayloadTooLarge,
			"request body exceeds the 256MB plane-import size limit", nil)
		return nil, false
	}
	f, fh, err := r.FormFile("file")
	if err != nil || fh == nil {
		WriteError(c, http.StatusBadRequest, ErrCodeBadRequest,
			"multipart field \"file\" is required", nil)
		return nil, false
	}
	return f, true
}

// analyzePlaneImport implements POST .../imports/plane-import/analyze:
// parse the export, resolve what can be resolved, return the unresolved
// inventory. Read-only — every query is a SELECT.
func (h *ProjectHandler) analyzePlaneImport(c *echo.Context) error {
	f, ok := planeImportMultipart(c)
	if !ok {
		return nil
	}
	defer f.Close()
	parsed, err := service.ParsePlaneImport(f)
	if err != nil {
		return planeImportError(c, err)
	}
	analysis, err := service.AnalyzePlaneImport(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, parsed.Rows)
	if err != nil {
		return planeImportError(c, err)
	}
	// T1's per-row shape failures are already known-bad: surface them as
	// warnings so the analyze response stays honest without changing the
	// PlaneImportAnalysis contract the SPA consumes.
	for i, re := range parsed.Errors {
		if i >= planeAnalyzeMaxWarnings {
			analysis.Warnings = append(analysis.Warnings,
				fmt.Sprintf("... and %d more row-level parse failures (reported per-row at execute)",
					len(parsed.Errors)-i))
			break
		}
		analysis.Warnings = append(analysis.Warnings,
			fmt.Sprintf("row %s skipped: %s", re.Identifier, re.Message))
	}
	return c.JSON(http.StatusOK, analysis)
}

// executePlaneImport implements POST .../imports/plane-import/execute:
// parse the export, apply the user's resolutions, import in one
// transaction, return the report.
func (h *ProjectHandler) executePlaneImport(c *echo.Context) error {
	f, ok := planeImportMultipart(c)
	if !ok {
		return nil
	}
	defer f.Close()
	var resolutions service.PlaneImportResolutions
	if raw := c.Request().FormValue("resolutions"); raw == "" {
		WriteError(c, http.StatusBadRequest, ErrCodeBadRequest,
			"multipart field \"resolutions\" is required (JSON PlaneImportResolutions)", nil)
		return nil
	} else if err := json.Unmarshal([]byte(raw), &resolutions); err != nil {
		WriteError(c, http.StatusBadRequest, ErrCodeBadRequest,
			"multipart field \"resolutions\" must be valid JSON", nil)
		return nil
	}
	var opts service.PlaneImportExecuteOpts
	if raw := c.Request().FormValue("options"); raw != "" {
		if err := json.Unmarshal([]byte(raw), &opts); err != nil {
			WriteError(c, http.StatusBadRequest, ErrCodeBadRequest,
				"multipart field \"options\" must be valid JSON", nil)
			return nil
		}
	}
	projectID, err := h.planeImportProjectID(c)
	if err != nil {
		return planeImportError(c, err)
	}
	report, err := service.ExecutePlaneImport(c.Request().Context(), h.Pool,
		projectID, CurrentUser(c).ID, f, resolutions, opts)
	if err != nil {
		return planeImportError(c, err)
	}
	return c.JSON(http.StatusOK, report)
}

// planeImportProjectID resolves :slug/:identifier to the project id for
// the plane-import execute endpoint, mirroring
// service.resolveIssueProject's tenancy semantics: bad slug or non-member
// -> service.ErrNotFound ("workspace not found" 404); a member with a bad
// identifier -> service.ErrProjectNotFound (404 "project not found"). The
// service's authorize() still gates role < member -> ErrForbidden (403).
func (h *ProjectHandler) planeImportProjectID(c *echo.Context) (string, error) {
	ctx := c.Request().Context()
	var wsID string
	err := h.Pool.QueryRow(ctx,
		`SELECT w.id::text FROM workspaces w
		  JOIN workspace_members m ON m.workspace_id = w.id
		 WHERE w.slug = $1 AND m.user_id = $2::uuid`,
		c.Param("slug"), CurrentUser(c).ID).Scan(&wsID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", service.ErrNotFound
		}
		return "", err
	}
	var projectID string
	err = h.Pool.QueryRow(ctx,
		`SELECT p.id::text FROM projects p
		 WHERE p.workspace_id = $1::uuid AND p.identifier = $2`,
		wsID, c.Param("identifier")).Scan(&projectID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", service.ErrProjectNotFound
		}
		return "", err
	}
	return projectID, nil
}
