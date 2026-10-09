package api

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// Issue-link endpoints (C4T0) nested under the issue path: list, create,
// delete. These are the directed dependency edges the Gantt view
// consumes ("issue A (kind) issue B", default kind 'blocks').
//
// Every route sits behind RequireAuth — workspace membership is the
// tenancy boundary. Reads need any member (guest 5+); mutations need
// member (15)+, enforced in the service. Errors use the spec §5 envelope
// via linkError (which falls back to issueError for the shared
// sentinels).
//
// Honest scope, v0.3.0: only direct self-loops and duplicate edges are
// rejected. There is NO cycle detection — A→B→C→A is accepted.

// RegisterIssueLinkRoutes mounts the issue-link endpoints. Call this
// before the SPA catch-all so API routes are never shadowed.
func RegisterIssueLinkRoutes(e *echo.Echo, h *IssueHandler) {
	g := e.Group("/api/v1/workspaces/:slug/projects/:identifier/issues/:uuid/links", RequireAuth(h.Pool))
	g.GET("", h.listIssueLinks)
	g.POST("", h.createIssueLink)
	g.DELETE("/:linkID", h.deleteIssueLink)
}

// linkError maps issue-link sentinel errors to HTTP statuses, delegating
// the shared sentinels (ErrIssueNotFound, ErrProjectNotFound, ErrNotFound,
// ErrForbidden) to issueError.
func linkError(c *echo.Context, err error) error {
	switch {
	case errors.Is(err, service.ErrIssueLinkSelf):
		return WriteError(c, http.StatusConflict, ErrCodeConflict, "issue cannot link to itself", nil)
	case errors.Is(err, service.ErrIssueLinkConflict):
		return WriteError(c, http.StatusConflict, ErrCodeConflict, "issue link already exists", nil)
	case errors.Is(err, service.ErrIssueLinkCrossProject):
		return WriteError(c, http.StatusConflict, ErrCodeConflict, "target issue is in another project", nil)
	case errors.Is(err, service.ErrIssueLinkNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "issue link not found", nil)
	case errors.Is(err, service.ErrInvalidIssueLink):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid issue link: unknown kind or malformed id", nil)
	default:
		return issueError(c, err)
	}
}

// listIssueLinks implements GET
// /api/v1/workspaces/{slug}/projects/{identifier}/issues/{uuid}/links:
// every edge touching the issue, labeled outgoing/incoming. Any member.
func (h *IssueHandler) listIssueLinks(c *echo.Context) error {
	slug, ident, uuid, actor, ok := h.issueParams(c)
	if !ok {
		return nil
	}
	links, err := service.ListIssueLinks(c.Request().Context(), h.Pool, slug, ident, uuid, actor)
	if err != nil {
		return linkError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"links": links})
}

type createIssueLinkBody struct {
	TargetIssueID string `json:"target_issue_id"`
	Kind          string `json:"kind"` // optional; defaults to 'blocks'
}

// createIssueLink implements POST .../issues/{uuid}/links
// {target_issue_id, kind?}. 409 on self-link, cross-project target, or
// duplicate edge; 404 on unknown target. Member (15)+.
func (h *IssueHandler) createIssueLink(c *echo.Context) error {
	var body createIssueLinkBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	slug, ident, uuid, actor, ok := h.issueParams(c)
	if !ok {
		return nil
	}
	link, err := service.CreateIssueLink(c.Request().Context(), h.Pool,
		slug, ident, uuid, actor, body.TargetIssueID, body.Kind)
	if err != nil {
		return linkError(c, err)
	}
	return c.JSON(http.StatusCreated, link)
}

// deleteIssueLink implements DELETE .../issues/{uuid}/links/{linkID}.
// The link must touch the addressed issue; anything else is 404. Member
// (15)+.
func (h *IssueHandler) deleteIssueLink(c *echo.Context) error {
	slug, ident, uuid, actor, ok := h.issueParams(c)
	if !ok {
		return nil
	}
	linkID, ok := requireUUIDParam(c, "linkID", "link id")
	if !ok {
		return nil
	}
	if err := service.DeleteIssueLink(c.Request().Context(), h.Pool,
		slug, ident, uuid, actor, linkID); err != nil {
		return linkError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}
