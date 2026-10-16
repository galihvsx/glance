package api

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// Page↔issue link endpoints (C36T0): the same link addressed from
// both sides — GET/POST/DELETE .../pages/{pageID}/issues and
// GET/POST/DELETE .../issues/{uuid}/pages.
//
// Every route sits behind RequireAuth — workspace membership is the
// tenancy boundary. Reads need any member (guest 5+); mutations need
// member (15)+, enforced in the service. Errors use the spec §5
// envelope via pageIssueLinkError (which falls back to pageError for
// the shared sentinels).
//
// Honest scope: duplicate → 409, cross-project → 409, unknown
// page/issue/link → 404. Archived and draft issues are linkable.

// RegisterPageIssueLinkRoutes mounts the page↔issue link endpoints.
// Call this before the SPA catch-all so API routes are never shadowed.
func RegisterPageIssueLinkRoutes(e *echo.Echo, h *IssueHandler) {
	pages := e.Group("/api/v1/workspaces/:slug/projects/:identifier/pages", RequireAuth(h.Pool))
	pages.GET("/:pageID/issues", h.listPageIssues)
	pages.POST("/:pageID/issues", h.linkPageIssue)
	pages.DELETE("/:pageID/issues/:issueID", h.unlinkPageIssue)

	issues := e.Group("/api/v1/workspaces/:slug/projects/:identifier/issues", RequireAuth(h.Pool))
	issues.GET("/:uuid/pages", h.listIssuePages)
	issues.POST("/:uuid/pages", h.linkIssuePage)
	issues.DELETE("/:uuid/pages/:pageID", h.unlinkIssuePage)
}

// pageIssueLinkError maps page↔issue link sentinel errors to HTTP
// statuses, delegating the shared sentinels (ErrPageNotFound,
// ErrIssueNotFound, ErrProjectNotFound, ErrNotFound, ErrForbidden,
// ErrInvalidPageID) to pageError.
func pageIssueLinkError(c *echo.Context, err error) error {
	switch {
	case errors.Is(err, service.ErrPageIssueLinkConflict):
		return WriteError(c, http.StatusConflict, ErrCodeConflict, "page-issue link already exists", nil)
	case errors.Is(err, service.ErrPageIssueLinkCrossProject):
		return WriteError(c, http.StatusConflict, ErrCodeConflict, "issue is in another project", nil)
	case errors.Is(err, service.ErrPageIssueLinkNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "page-issue link not found", nil)
	case errors.Is(err, service.ErrInvalidPageIssueLink):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid page-issue link: malformed id", nil)
	default:
		return pageError(c, err)
	}
}

// listPageIssues implements GET
// /api/v1/workspaces/{slug}/projects/{identifier}/pages/{pageID}/issues:
// the page's linked issues (display_id, title, state, priority,
// ordered by issue created_at). Any member.
func (h *IssueHandler) listPageIssues(c *echo.Context) error {
	pageID, ok := requireUUIDParam(c, "pageID", "page id")
	if !ok {
		return nil
	}
	issues, err := service.ListPageIssues(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), pageID, CurrentUser(c).ID)
	if err != nil {
		return pageIssueLinkError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"issues": issues})
}

type linkPageIssueBody struct {
	IssueID string `json:"issue_id"`
}

// linkPageIssue implements POST .../pages/{pageID}/issues
// {issue_id} → 201 link. Duplicate → 409, cross-project → 409,
// unknown issue → 404. Member (15)+.
func (h *IssueHandler) linkPageIssue(c *echo.Context) error {
	var body linkPageIssueBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	pageID, ok := requireUUIDParam(c, "pageID", "page id")
	if !ok {
		return nil
	}
	link, err := service.LinkPageIssue(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), pageID, CurrentUser(c).ID, body.IssueID)
	if err != nil {
		return pageIssueLinkError(c, err)
	}
	return c.JSON(http.StatusCreated, link)
}

// unlinkPageIssue implements DELETE
// .../pages/{pageID}/issues/{issueID}. Unknown link → 404.
// Member (15)+.
func (h *IssueHandler) unlinkPageIssue(c *echo.Context) error {
	pageID, ok := requireUUIDParam(c, "pageID", "page id")
	if !ok {
		return nil
	}
	issueID, ok := requireUUIDParam(c, "issueID", "issue id")
	if !ok {
		return nil
	}
	if err := service.UnlinkPageIssue(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), pageID, CurrentUser(c).ID, issueID); err != nil {
		return pageIssueLinkError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// listIssuePages implements GET
// /api/v1/workspaces/{slug}/projects/{identifier}/issues/{uuid}/pages:
// the issue's linked pages (title, updated_at, newest first).
// Any member.
func (h *IssueHandler) listIssuePages(c *echo.Context) error {
	slug, ident, uuid, actor, ok := h.issueParams(c)
	if !ok {
		return nil
	}
	pages, err := service.ListIssuePages(c.Request().Context(), h.Pool, slug, ident, uuid, actor)
	if err != nil {
		return pageIssueLinkError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"pages": pages})
}

type linkIssuePageBody struct {
	PageID string `json:"page_id"`
}

// linkIssuePage implements POST .../issues/{uuid}/pages {page_id} →
// 201 link — the same link as POST .../pages/{pageID}/issues, from
// the other direction. Duplicate → 409, cross-project → 409,
// unknown page → 404. Member (15)+.
func (h *IssueHandler) linkIssuePage(c *echo.Context) error {
	var body linkIssuePageBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	slug, ident, uuid, actor, ok := h.issueParams(c)
	if !ok {
		return nil
	}
	link, err := service.LinkPageIssue(c.Request().Context(), h.Pool,
		slug, ident, body.PageID, actor, uuid)
	if err != nil {
		return pageIssueLinkError(c, err)
	}
	return c.JSON(http.StatusCreated, link)
}

// unlinkIssuePage implements DELETE
// .../issues/{uuid}/pages/{pageID}. Unknown link → 404. Member (15)+.
func (h *IssueHandler) unlinkIssuePage(c *echo.Context) error {
	slug, ident, uuid, actor, ok := h.issueParams(c)
	if !ok {
		return nil
	}
	pageID, ok := requireUUIDParam(c, "pageID", "page id")
	if !ok {
		return nil
	}
	if err := service.UnlinkPageIssue(c.Request().Context(), h.Pool,
		slug, ident, pageID, actor, uuid); err != nil {
		return pageIssueLinkError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}
