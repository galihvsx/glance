package api

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// Sub-issue parent endpoints (C5T4) nested under the issue path: set /
// clear the parent and the ?include_children=1 contract on the issue
// detail endpoint. The storage column is issues.parent_id (the design
// spec's sub-issue FK); the request body names it parent_issue_id per
// the C5T4 contract.
//
// Every route sits behind RequireAuth — workspace membership is the
// tenancy boundary. Reads need any member (guest 5+); mutations need
// member (15)+, enforced in the service. Errors use the spec §5 envelope
// via parentError (which falls back to issueError for the shared
// sentinels).

// registerIssueParentRoutes mounts the parent endpoints on the issue
// route group. Call this before the SPA catch-all so API routes are
// never shadowed.
func registerIssueParentRoutes(g *echo.Group, h *IssueHandler) {
	g.POST("/:uuid/parent", h.setIssueParent)
	g.DELETE("/:uuid/parent", h.clearIssueParent)
}

// parentError maps sub-issue sentinel errors to HTTP statuses,
// delegating the shared sentinels (ErrIssueNotFound, ErrProjectNotFound,
// ErrNotFound, ErrForbidden, ErrParentNotFound) to issueError.
func parentError(c *echo.Context, err error) error {
	switch {
	case errors.Is(err, service.ErrIssueSelfParent):
		return WriteError(c, http.StatusConflict, ErrCodeConflict, "issue cannot be its own parent", nil)
	case errors.Is(err, service.ErrIssueCrossProjectParent):
		return WriteError(c, http.StatusConflict, ErrCodeConflict, "parent issue is in another project", nil)
	case errors.Is(err, service.ErrIssueCyclicParent):
		return WriteError(c, http.StatusConflict, ErrCodeConflict, "parent assignment would create a cycle", nil)
	default:
		return issueError(c, err)
	}
}

type setIssueParentBody struct {
	ParentIssueID string `json:"parent_issue_id"`
}

// setIssueParent implements POST
// /api/v1/workspaces/{slug}/projects/{identifier}/issues/{uuid}/parent
// {parent_issue_id}. 409 on self-parent, cross-project parent, or a
// parent assignment that would close a cycle; 404 on an unknown parent
// id. Member (15)+.
func (h *IssueHandler) setIssueParent(c *echo.Context) error {
	var body setIssueParentBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	uuid, ok := requireUUIDParam(c, "uuid", "issue id")
	if !ok {
		return nil
	}
	parentID, err := service.SetParent(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), uuid, CurrentUser(c).ID, body.ParentIssueID)
	if err != nil {
		return parentError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"parent_issue_id": parentID})
}

// clearIssueParent implements DELETE
// /api/v1/workspaces/{slug}/projects/{identifier}/issues/{uuid}/parent:
// the issue becomes a top-level issue. Member (15)+.
func (h *IssueHandler) clearIssueParent(c *echo.Context) error {
	uuid, ok := requireUUIDParam(c, "uuid", "issue id")
	if !ok {
		return nil
	}
	if _, err := service.SetParent(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), uuid, CurrentUser(c).ID, ""); err != nil {
		return parentError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"parent_issue_id": nil})
}

// issueDetailResponse is the GET issue detail shape with the optional
// ?include_children=1 children attachment (C5T4) and the optional
// ?include_custom=1 custom-values attachment (C7T2). The list query is
// deliberately untouched (perf): both are fetched only on detail, in one
// extra query each. custom_values is keyed by field id and omitted when
// not requested (or when no values are set).
type issueDetailResponse struct {
	service.IssueListItem
	Children     []service.IssueChild           `json:"children,omitempty"`
	CustomValues map[string]service.CustomValue `json:"custom_values,omitempty"`
}

// getIssueChildren decorates a fetched issue with its child summaries
// when the caller passes ?include_children=1, and with its custom values
// when the caller passes ?include_custom=1 (C7T2). Without either flag
// the response is the bare IssueListItem, byte-identical to before.
func (h *IssueHandler) getIssueChildren(c *echo.Context, iss *service.IssueListItem) error {
	qp := c.QueryParams()
	wantChildren := qp.Get("include_children") == "1" || qp.Get("include_children") == "true"
	wantCustom := qp.Get("include_custom") == "1" || qp.Get("include_custom") == "true"
	if !wantChildren && !wantCustom {
		return c.JSON(http.StatusOK, iss)
	}
	resp := issueDetailResponse{IssueListItem: *iss}
	if wantChildren {
		children, err := service.ListIssueChildren(c.Request().Context(), h.Pool,
			c.Param("slug"), c.Param("identifier"), iss.ID, CurrentUser(c).ID)
		if err != nil {
			return parentError(c, err)
		}
		if children == nil {
			children = []service.IssueChild{}
		}
		resp.Children = children
	}
	if wantCustom {
		values, err := service.GetIssueCustomValues(c.Request().Context(), h.Pool,
			c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, iss.ID)
		if err != nil {
			return customFieldError(c, err)
		}
		resp.CustomValues = values
	}
	return c.JSON(http.StatusOK, resp)
}
