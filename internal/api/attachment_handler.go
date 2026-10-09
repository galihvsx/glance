package api

import (
	"errors"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/labstack/echo/v5"

	"glance/internal/service"
	"glance/internal/store"
)

// Issue attachment endpoints (C4T2), nested under the issue satellite
// group: every route sits behind RequireAuth — workspace membership is
// the tenancy boundary. Errors use the spec §5 envelope via
// attachmentError (which falls back to issueError for the shared
// sentinels like ErrIssueNotFound / ErrForbidden).
//
// Routes (registered in RegisterSatelliteRoutes):
//   GET    .../issues/:uuid/attachments
//   POST   .../issues/:uuid/attachments            (multipart, field "file")
//   GET    .../issues/:uuid/attachments/:attachmentID
//   DELETE .../issues/:uuid/attachments/:attachmentID
//
// Reads need any member (guest 5+); upload and delete need member (15)+
// — the service enforces both via resolveSatelliteIssue.

// attachmentError maps attachment sentinel errors to HTTP statuses,
// delegating the shared sentinels to issueError.
func attachmentError(c *echo.Context, err error) error {
	switch {
	case errors.Is(err, service.ErrAttachmentNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "attachment not found", nil)
	case errors.Is(err, service.ErrAttachmentTooLarge):
		return WriteError(c, http.StatusRequestEntityTooLarge, ErrCodePayloadTooLarge, "attachment exceeds the per-file size limit", nil)
	case errors.Is(err, service.ErrInvalidAttachment):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid attachment: missing file or filename", nil)
	default:
		return issueError(c, err)
	}
}

// serveInline reports whether a stored content type may be served with
// Content-Disposition: inline. image/* and text/* render inline —
// EXCEPT text/html and script types, which are NEVER served inline:
// a stored XSS payload must download as bytes, never execute in the
// glance origin.
func serveInline(contentType string) bool {
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil || mt == "" {
		return false
	}
	switch mt {
	case "text/html",
		"application/javascript", "application/x-javascript",
		"application/ecmascript", "application/x-ecmascript",
		// SVG is XML that browsers render as a document when navigated
		// to directly — embedded <script> would execute in the glance
		// origin, so it is never served inline.
		"image/svg+xml", "image/svg":
		return false
	}
	return strings.HasPrefix(mt, "image/") || strings.HasPrefix(mt, "text/")
}

// listAttachments implements GET .../issues/:uuid/attachments.
func (h *IssueHandler) listAttachments(c *echo.Context) error {
	slug, ident, uuid, actor, ok := h.issueParams(c)
	if !ok {
		return nil
	}
	atts, err := service.ListAttachments(c.Request().Context(), h.Pool,
		slug, ident, uuid, actor)
	if err != nil {
		return attachmentError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"attachments": atts})
}

// uploadAttachment implements POST .../issues/:uuid/attachments.
// Multipart form, single file in field "file". The per-file cap comes
// from h.MaxUploadBytes (GLANCE_MAX_UPLOAD_MB); the request body is
// additionally hard-capped at cap+1MiB so a hostile body can't make the
// server buffer unboundedly before the service's precise per-file check
// runs.
func (h *IssueHandler) uploadAttachment(c *echo.Context) error {
	slug, ident, uuid, actor, ok := h.issueParams(c)
	if !ok {
		return nil
	}
	if h.Attachments == nil || h.MaxUploadBytes < 1 {
		return WriteError(c, http.StatusInternalServerError, ErrCodeInternal,
			"attachment storage is not configured", nil)
	}
	r := c.Request()
	r.Body = http.MaxBytesReader(c.Response(), r.Body, h.MaxUploadBytes+(1<<20))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		// Includes the MaxBytesReader overflow: the body exceeded the cap.
		return WriteError(c, http.StatusRequestEntityTooLarge, ErrCodePayloadTooLarge,
			"request body exceeds the upload size limit", nil)
	}
	f, fh, err := r.FormFile("file")
	if err != nil || fh == nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest,
			"multipart field \"file\" is required", nil)
	}
	defer f.Close()

	att, err := service.UploadAttachment(r.Context(), h.Pool, h.Attachments,
		slug, ident, uuid, actor, service.UploadInput{
			Filename:    fh.Filename,
			ContentType: fh.Header.Get("Content-Type"),
			Data:        f,
			MaxBytes:    h.MaxUploadBytes,
		})
	if err != nil {
		return attachmentError(c, err)
	}
	return c.JSON(http.StatusCreated, att)
}

// downloadAttachment implements GET
// .../issues/:uuid/attachments/:attachmentID. Streams the stored bytes
// with the stored content type; X-Content-Type-Options: nosniff is
// always set so a mislabeled file can't be sniffed into execution.
func (h *IssueHandler) downloadAttachment(c *echo.Context) error {
	slug, ident, uuid, actor, ok := h.issueParams(c)
	if !ok {
		return nil
	}
	attID, ok := requireUUIDParam(c, "attachmentID", "attachment id")
	if !ok {
		return nil
	}
	if h.Attachments == nil {
		return WriteError(c, http.StatusInternalServerError, ErrCodeInternal,
			"attachment storage is not configured", nil)
	}
	att, err := service.GetAttachment(c.Request().Context(), h.Pool,
		slug, ident, uuid, attID, actor)
	if err != nil {
		return attachmentError(c, err)
	}
	rc, err := h.Attachments.Open(att.StoredPath)
	if err != nil {
		// Row exists but bytes are gone: server-side inconsistency.
		return WriteError(c, http.StatusInternalServerError, ErrCodeInternal,
			"attachment bytes are unavailable", nil)
	}
	defer rc.Close()

	disposition := "attachment"
	if serveInline(att.ContentType) {
		disposition = "inline"
	}
	// The filename was sanitized at upload (no quotes/semicolons), but
	// re-sanitize at the trust boundary anyway.
	filename := store.SanitizeFilename(att.Filename)
	hdr := c.Response().Header()
	hdr.Set("Content-Type", att.ContentType)
	hdr.Set("Content-Disposition", disposition+`; filename="`+filename+`"`)
	hdr.Set("Content-Length", strconv.FormatInt(att.SizeBytes, 10))
	hdr.Set("X-Content-Type-Options", "nosniff")
	return c.Stream(http.StatusOK, att.ContentType, rc)
}

// deleteAttachment implements DELETE
// .../issues/:uuid/attachments/:attachmentID → 204.
func (h *IssueHandler) deleteAttachment(c *echo.Context) error {
	slug, ident, uuid, actor, ok := h.issueParams(c)
	if !ok {
		return nil
	}
	attID, ok := requireUUIDParam(c, "attachmentID", "attachment id")
	if !ok {
		return nil
	}
	if h.Attachments == nil || h.MaxUploadBytes < 1 {
		return WriteError(c, http.StatusInternalServerError, ErrCodeInternal,
			"attachment storage is not configured", nil)
	}
	if err := service.DeleteAttachment(c.Request().Context(), h.Pool, h.Attachments,
		slug, ident, uuid, attID, actor); err != nil {
		return attachmentError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}
