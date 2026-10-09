package api

// Issues export endpoint (C8T3):
//   GET /api/v1/workspaces/:slug/projects/:identifier/issues/export?format=csv|json
//
// Member-scoped: the same read gate as the issue list (any workspace
// member, guests included — resolveIssueProject in the service layer).
// Honors the list endpoint's filter params (q, archived, state, priority,
// label, assignee, estimate, cycle, date ranges, undated, subscribed,
// draft) through the shared parseIssueListInput parser: what the user
// sees is what gets exported. Pagination/sort params (cursor, per_page,
// order_by, fields) are accepted and ignored — exports always stream the
// whole filtered set in display-ID (sequence) order.
//
// format defaults to "csv"; anything else is 400. Rows stream from a pgx
// cursor with a per-row flush — the full set is never materialized. A
// pre-stream COUNT enforces the MaxExportRows cap with an honest 400
// naming the matched row count (never a silently truncated file).
// Content-Disposition: attachment; the filename is
// glance-<IDENTIFIER>-issues-<YYYY-MM-DD>.<ext> (UTC date).
//
// CSV columns mirror the CSV importer's column order where the concepts
// overlap (name, state, priority, labels, assignee). Cells whose first
// character is =, +, - or @ get a single-quote prefix (spreadsheet
// formula-injection mitigation); the quote becomes part of the exported
// value. JSON export is an array of issue objects in the exact shape the
// list endpoint returns (description always included — exports are not a
// working-set view, so sparse fieldsets don't apply).

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// exportCSVHeader is the CSV column order.
var exportCSVHeader = []string{
	"display_id", "name", "state", "priority", "assignee",
	"labels", "estimate", "due_date", "created_at", "updated_at",
}

// sanitizeCSVCell neutralizes spreadsheet formula injection: a cell whose
// first character is =, +, -, @, tab or CR gets a single-quote prefix
// (the OWASP recommended mitigation for CSV export). Documented in the
// OpenAPI description so importers know the quote is literal. Residual
// edge (accepted): a formula preceded by spaces — spreadsheet-dependent,
// and issue names are trimmed at creation.
// Rationale for prefixing over dropping: the task's contract (C8T3)
// allows either; prefixing preserves the data visibly instead of
// silently altering it.
func sanitizeCSVCell(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + s
	}
	return s
}

// exportCSVRecord renders one issue as a CSV record. Labels are
// semicolon-joined (commas would collide with the importer's
// comma-separated label syntax when a label name itself contains a
// comma); assignees are semicolon-joined emails.
func exportCSVRecord(item *service.ExportIssue) []string {
	var due string
	if item.TargetDate != nil {
		due = item.TargetDate.Format("2006-01-02")
	}
	labelNames := make([]string, 0, len(item.Labels))
	for _, l := range item.Labels {
		labelNames = append(labelNames, l.Name)
	}
	return []string{
		item.DisplayID,
		sanitizeCSVCell(item.Name),
		sanitizeCSVCell(item.StateName),
		sanitizeCSVCell(service.PriorityWord(item.Priority)),
		sanitizeCSVCell(strings.Join(item.AssigneeEmails, ";")),
		sanitizeCSVCell(strings.Join(labelNames, ";")),
		sanitizeCSVCell(item.EstimateKey),
		due,
		item.CreatedAt.Format(time.RFC3339),
		item.UpdatedAt.Format(time.RFC3339),
	}
}

// exportError maps export service errors to HTTP responses.
func exportError(c *echo.Context, err error) error {
	switch {
	case errors.Is(err, service.ErrExportTooLarge):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, err.Error(), nil)
	case errors.Is(err, service.ErrInvalidListFilter):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid filter", nil)
	case errors.Is(err, service.ErrInvalidIdentifier):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid project identifier", nil)
	}
	return issueError(c, err)
}

// exportIssues implements GET .../issues/export?format=csv|json.
func (h *IssueHandler) exportIssues(c *echo.Context) error {
	format := strings.ToLower(strings.TrimSpace(c.QueryParam("format")))
	if format == "" {
		format = "csv"
	}
	if format != "csv" && format != "json" {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, `invalid format: want "csv" or "json"`, nil)
	}
	in, err := parseIssueListInput(c.QueryParams())
	if err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, err.Error(), nil)
	}

	ctx := c.Request().Context()
	slug := c.Param("slug")
	identifier := c.Param("identifier")
	actorID := CurrentUser(c).ID

	// Resolve scope first: validates the identifier (normalized form is
	// filename-safe: ^[A-Z0-9]+$), the caller's membership, and the
	// filter values — before any response byte is committed.
	ident, err := service.ExportScope(ctx, h.Pool, slug, identifier, actorID, in)
	if err != nil {
		return exportError(c, err)
	}
	filename := fmt.Sprintf("glance-%s-issues-%s.%s", ident, time.Now().UTC().Format("2006-01-02"), format)

	res := c.Response()
	res.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	if format == "csv" {
		return h.streamExportCSV(c, slug, identifier, actorID, in)
	}
	return h.streamExportJSON(c, slug, identifier, actorID, in)
}

// streamExportCSV streams the CSV body. The header sits in csv.Writer's
// buffer until the first Flush inside the row loop; the service enforces
// the row cap before the first row is yielded, so an over-cap export
// still answers 400 with no partial body on the wire.
func (h *IssueHandler) streamExportCSV(c *echo.Context, slug, identifier, actorID string, in service.ListIssuesInput) error {
	c.Response().Header().Set(echo.HeaderContentType, "text/csv; charset=utf-8")
	w := csv.NewWriter(c.Response())
	if err := w.Write(exportCSVHeader); err != nil {
		return err
	}
	flusher, _ := c.Response().(http.Flusher)
	err := service.StreamExportIssues(c.Request().Context(), h.Pool, slug, identifier, actorID, in,
		func(item *service.ExportIssue) error {
			if err := w.Write(exportCSVRecord(item)); err != nil {
				return err
			}
			w.Flush()
			if err := w.Error(); err != nil {
				return err
			}
			if flusher != nil {
				flusher.Flush()
			}
			return nil
		})
	if err != nil {
		// Pre-stream failures (cap, scope, filters) land here with
		// nothing flushed yet (the header is still in csv.Writer's
		// buffer) — still safe to answer with a status. A mid-stream
		// failure means a truncated body; the 200 was already
		// committed, so report it and let echo log it.
		if exportPreStream(err) {
			return exportError(c, err)
		}
		return err
	}
	w.Flush()
	return w.Error()
}

// streamExportJSON streams a JSON array of issue objects. The opening
// bracket is written lazily on the first row so an over-cap (or
// unauthorized) export still answers 400/404 with an empty body; an
// empty working set yields "[]".
func (h *IssueHandler) streamExportJSON(c *echo.Context, slug, identifier, actorID string, in service.ListIssuesInput) error {
	c.Response().Header().Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	w := c.Response()
	flusher, _ := w.(http.Flusher)
	first := true
	err := service.StreamExportIssues(c.Request().Context(), h.Pool, slug, identifier, actorID, in,
		func(item *service.ExportIssue) error {
			if first {
				if _, err := w.Write([]byte("[")); err != nil {
					return err
				}
				first = false
			} else if _, err := w.Write([]byte(",")); err != nil {
				return err
			}
			b, err := json.Marshal(&item.IssueListItem)
			if err != nil {
				return err
			}
			if _, err := w.Write(b); err != nil {
				return err
			}
			if flusher != nil {
				flusher.Flush()
			}
			return nil
		})
	if err != nil {
		if exportPreStream(err) {
			return exportError(c, err)
		}
		return err
	}
	if first {
		_, err = w.Write([]byte("[]"))
	} else {
		_, err = w.Write([]byte("]"))
	}
	return err
}

// exportPreStream reports whether err came from the service's pre-stream
// phase (scope resolution, filter validation, row-cap check) — i.e.
// before the first row was yielded. Only then is answering with an HTTP
// status still possible.
func exportPreStream(err error) bool {
	return errors.Is(err, service.ErrExportTooLarge) ||
		errors.Is(err, service.ErrInvalidListFilter) ||
		errors.Is(err, service.ErrInvalidIdentifier) ||
		errors.Is(err, service.ErrNotFound) ||
		errors.Is(err, service.ErrProjectNotFound)
}
