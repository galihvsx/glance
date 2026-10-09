package api

// CSV issue importer endpoints (C4T8).
//
//   POST /api/v1/workspaces/:slug/projects/:identifier/imports
//     multipart: "file" = the CSV, "mapping" = JSON ImportMapping.
//     Creates issues; per-row errors never abort the batch.
//     200 {created, failed, errors:[{row, message}]}. Member (15)+.
//
//   POST .../imports/preview — same inputs, no writes.
//     200 {rows:[{row, values}], errors:[...]}.
//
//   GET  .../imports/template.csv — the template CSV download.
//
// The request body is hard-capped (10MB + 1MiB headroom, same pattern as
// the attachment upload) so a hostile body can't buffer unboundedly.

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// importMaxBytes caps the CSV upload body: 10MB + 1MiB headroom.
const importMaxBytes = 10<<20 + (1 << 20)

// importError maps service sentinel errors to HTTP statuses.
func importError(c *echo.Context, err error) error {
	switch {
	case errors.Is(err, service.ErrImportMapping) || errors.Is(err, service.ErrImportEmpty):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, err.Error(), nil)
	default:
		return projectError(c, err)
	}
}

// RegisterImportRoutes mounts the importer endpoints on the project group.
func RegisterImportRoutes(e *echo.Echo, h *ProjectHandler) {
	g := e.Group("/api/v1/workspaces/:slug/projects/:identifier/imports", RequireAuth(h.Pool))
	g.POST("", h.importIssues)
	g.POST("/preview", h.previewImport)
	g.GET("/template.csv", h.importTemplate)
}

// importMultipart extracts the CSV file and the JSON mapping from a
// multipart request.
func importMultipart(c *echo.Context) (file interface {
	Read([]byte) (int, error)
	Close() error
}, mapping service.ImportMapping, ok bool) {
	r := c.Request()
	r.Body = http.MaxBytesReader(c.Response(), r.Body, importMaxBytes)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		WriteError(c, http.StatusRequestEntityTooLarge, ErrCodePayloadTooLarge,
			"request body exceeds the 10MB import size limit", nil)
		return nil, mapping, false
	}
	f, fh, err := r.FormFile("file")
	if err != nil || fh == nil {
		WriteError(c, http.StatusBadRequest, ErrCodeBadRequest,
			"multipart field \"file\" is required", nil)
		return nil, mapping, false
	}
	if err := json.Unmarshal([]byte(r.FormValue("mapping")), &mapping); err != nil {
		f.Close()
		WriteError(c, http.StatusBadRequest, ErrCodeBadRequest,
			"multipart field \"mapping\" must be valid JSON", nil)
		return nil, mapping, false
	}
	return f, mapping, true
}

// importIssues implements POST .../imports: parse, validate per-row,
// insert valid rows.
func (h *ProjectHandler) importIssues(c *echo.Context) error {
	f, mapping, ok := importMultipart(c)
	if !ok {
		return nil
	}
	defer f.Close()
	res, err := service.ImportIssues(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, f, mapping)
	if err != nil {
		return importError(c, err)
	}
	return c.JSON(http.StatusOK, res)
}

// previewImport implements POST .../imports/preview: parse + validate the
// first 10 rows, no writes.
func (h *ProjectHandler) previewImport(c *echo.Context) error {
	f, mapping, ok := importMultipart(c)
	if !ok {
		return nil
	}
	defer f.Close()
	prev, err := service.PreviewImport(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, f, mapping)
	if err != nil {
		return importError(c, err)
	}
	return c.JSON(http.StatusOK, prev)
}

// importTemplate implements GET .../imports/template.csv.
func (h *ProjectHandler) importTemplate(c *echo.Context) error {
	return c.Blob(http.StatusOK, "text/csv; charset=utf-8",
		[]byte(service.ImportTemplateCSV()))
}
