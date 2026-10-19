package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// Epic endpoints (C71T0) nested under projects, mirroring the module
// route shapes. Every route sits behind RequireAuth — workspace
// membership is the tenancy boundary. Errors use the spec §5 envelope
// via epicError (which falls back to issueError for the shared
// sentinels).
//
// PATCH tri-state for nullable fields (name is required): a missing key
// leaves the field untouched, an explicit "" clears description (NULL),
// any other value sets it. Lifecycle is archive, not status — see
// POST .../epics/{epicID}/archive and .../unarchive.

// RegisterEpicRoutes mounts the epic endpoints. Call this before the
// SPA catch-all so API routes are never shadowed.
func RegisterEpicRoutes(e *echo.Echo, h *IssueHandler) {
	g := e.Group("/api/v1/workspaces/:slug/projects/:identifier/epics", RequireAuth(h.Pool))
	g.POST("", h.createEpic)
	g.GET("", h.listEpics)
	g.GET("/:epicID", h.getEpic)
	g.PATCH("/:epicID", h.updateEpic)
	g.DELETE("/:epicID", h.deleteEpic)
	g.POST("/:epicID/archive", h.archiveEpic)
	g.POST("/:epicID/unarchive", h.unarchiveEpic)
	g.GET("/:epicID/issues", h.listEpicIssues)
	g.POST("/:epicID/issues", h.addEpicIssues)
	g.DELETE("/:epicID/issues", h.removeEpicIssues)
}

// epicError maps epic sentinel errors to HTTP statuses, delegating
// the shared sentinels (ErrNotFound, ErrProjectNotFound, ErrIssueNotFound,
// ErrForbidden, ErrNameRequired, ErrNothingToUpdate, ErrBulkEmptyIDs) to
// issueError.
func epicError(c *echo.Context, err error) error {
	switch {
	case errors.Is(err, service.ErrEpicNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "epic not found", nil)
	case errors.Is(err, service.ErrEpicConflict):
		return WriteError(c, http.StatusConflict, ErrCodeConflict, "epic name already exists", nil)
	case errors.Is(err, service.ErrInvalidEpic):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid epic", nil)
	case errors.Is(err, service.ErrInvalidEpicID):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid id", nil)
	case errors.Is(err, service.ErrInvalidEpicFilter):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, epicFilterMessage(err), nil)
	case errors.Is(err, service.ErrEpicHasIssues):
		return WriteError(c, http.StatusConflict, ErrCodeConflict, "epic still has issues: remove them or retry with ?force=true", nil)
	default:
		return issueError(c, err)
	}
}

type epicBody struct {
	Name        string  `json:"name"`
	Description *string `json:"description"`
}

// createEpic implements POST
// /api/v1/workspaces/{slug}/projects/{identifier}/epics.
// Member (15)+; the service enforces it.
func (h *IssueHandler) createEpic(c *echo.Context) error {
	var body epicBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	epic, err := service.CreateEpic(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID,
		service.EpicInput{
			Name:        body.Name,
			Description: body.Description,
		})
	if err != nil {
		if errors.Is(err, service.ErrNameRequired) {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "name is required", nil)
		}
		return epicError(c, err)
	}
	return c.JSON(http.StatusCreated, epic)
}

// epicFilterMessage surfaces the wrapped detail of an
// ErrInvalidEpicFilter (e.g. "unknown sort \"x\" (...)") as the 400
// message — "clear error message" per the C74 contract.
func epicFilterMessage(err error) string {
	const prefix = "service: invalid epic list filter"
	msg := strings.TrimPrefix(err.Error(), prefix+": ")
	if msg == "" || msg == err.Error() {
		return "invalid epic filter"
	}
	return msg
}

// listEpics implements GET
// /api/v1/workspaces/{slug}/projects/{identifier}/epics with rich
// filters (C74T0 — mirror of the C73 initiative filters):
//   - ?status=active&status=archived — repeatable status multi-value;
//     replaces the binary includeArchived. ?include_archived=1|true is
//     kept backward-compatible and maps to "no status exclusion" (an
//     explicit status list wins over it).
//   - ?q= — text search (ILIKE) on name + description.
//   - ?created_by=, ?initiative_id= — uuid filters (400 on malformed).
//   - ?created_from=, ?created_to= — ISO-8601 on created_at, date-only
//     is whole-day-inclusive (400 on unparseable).
//   - ?sort=name|created_at|updated_at|issue_count,
//     ?order=asc|desc — unknown values → 400 with a clear message.
func (h *IssueHandler) listEpics(c *echo.Context) error {
	qp := c.QueryParams()
	inc := qp.Get("include_archived")
	epics, err := service.ListEpics(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID,
		service.EpicListFilter{
			IncludeArchived: inc == "1" || inc == "true",
			Statuses:        qp["status"],
			Q:               qp.Get("q"),
			CreatedBy:       qp.Get("created_by"),
			CreatedFrom:     qp.Get("created_from"),
			CreatedTo:       qp.Get("created_to"),
			InitiativeID:    qp.Get("initiative_id"),
			Sort:            qp.Get("sort"),
			Order:           qp.Get("order"),
		})
	if err != nil {
		return epicError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"epics": epics})
}

// getEpic implements GET .../epics/{epicID}: the epic with live issue
// count and state-group progress.
func (h *IssueHandler) getEpic(c *echo.Context) error {
	epicID, ok := requireUUIDParam(c, "epicID", "epic id")
	if !ok {
		return nil
	}
	epic, err := service.GetEpic(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, epicID)
	if err != nil {
		return epicError(c, err)
	}
	return c.JSON(http.StatusOK, epic)
}

type patchEpicBody struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
}

// updateEpic implements PATCH .../epics/{epicID}. Partial semantics:
// missing keys are untouched, "" clears description (NULL).
// Member (15)+.
func (h *IssueHandler) updateEpic(c *echo.Context) error {
	var body patchEpicBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	epicID, ok := requireUUIDParam(c, "epicID", "epic id")
	if !ok {
		return nil
	}
	epic, err := service.UpdateEpic(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, epicID,
		service.EpicPatch{
			Name:        body.Name,
			Description: body.Description,
		})
	if err != nil {
		if errors.Is(err, service.ErrNameRequired) {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "name is required", nil)
		}
		return epicError(c, err)
	}
	return c.JSON(http.StatusOK, epic)
}

// deleteEpic implements DELETE .../epics/{epicID}. Without ?force=true
// an epic that still has issues is 409; with force the membership rows
// cascade and the issues themselves are untouched. Member (15)+.
func (h *IssueHandler) deleteEpic(c *echo.Context) error {
	epicID, ok := requireUUIDParam(c, "epicID", "epic id")
	if !ok {
		return nil
	}
	force := c.QueryParam("force") == "true"
	if err := service.DeleteEpic(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, epicID, force); err != nil {
		return epicError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// archiveEpic implements POST .../epics/{epicID}/archive: sets
// archived_at; the epic drops out of the default list. Member (15)+.
func (h *IssueHandler) archiveEpic(c *echo.Context) error {
	epicID, ok := requireUUIDParam(c, "epicID", "epic id")
	if !ok {
		return nil
	}
	epic, err := service.ArchiveEpic(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, epicID)
	if err != nil {
		return epicError(c, err)
	}
	return c.JSON(http.StatusOK, epic)
}

// unarchiveEpic implements POST .../epics/{epicID}/unarchive: clears
// archived_at. Member (15)+.
func (h *IssueHandler) unarchiveEpic(c *echo.Context) error {
	epicID, ok := requireUUIDParam(c, "epicID", "epic id")
	if !ok {
		return nil
	}
	epic, err := service.UnarchiveEpic(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, epicID)
	if err != nil {
		return epicError(c, err)
	}
	return c.JSON(http.StatusOK, epic)
}

// listEpicIssues implements GET .../epics/{epicID}/issues: the epic's
// live issues as full list items.
func (h *IssueHandler) listEpicIssues(c *echo.Context) error {
	epicID, ok := requireUUIDParam(c, "epicID", "epic id")
	if !ok {
		return nil
	}
	issues, err := service.ListEpicIssues(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, epicID)
	if err != nil {
		return epicError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"issues": issues})
}

type epicIssuesBody struct {
	IssueIDs []string `json:"issue_ids"`
}

// addEpicIssues implements POST .../epics/{epicID}/issues {issue_ids}.
// Idempotent: repeats are no-ops. Member (15)+.
func (h *IssueHandler) addEpicIssues(c *echo.Context) error {
	var body epicIssuesBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	epicID, ok := requireUUIDParam(c, "epicID", "epic id")
	if !ok {
		return nil
	}
	if err := service.AddEpicIssues(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, epicID, body.IssueIDs); err != nil {
		return epicError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"added": len(body.IssueIDs)})
}

// removeEpicIssues implements DELETE .../epics/{epicID}/issues
// {issue_ids}. Idempotent: removing a non-member is a no-op.
// Member (15)+.
func (h *IssueHandler) removeEpicIssues(c *echo.Context) error {
	var body epicIssuesBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	epicID, ok := requireUUIDParam(c, "epicID", "epic id")
	if !ok {
		return nil
	}
	if err := service.RemoveEpicIssues(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, epicID, body.IssueIDs); err != nil {
		return epicError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"removed": len(body.IssueIDs)})
}
