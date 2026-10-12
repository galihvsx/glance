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
	// C24T1: convert issue <-> sub-issue. The static "convert" segment
	// keeps this distinct from PATCH /:uuid by Echo's specificity rules
	// (same pattern as /clone and /move).
	g.POST("/:uuid/convert", h.convertIssue)
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

type convertIssueBody struct {
	// Tri-state: a UUID demotes to a sub-issue of that parent, null (or
	// absent/empty) promotes back to a standalone issue.
	ParentUUID *string `json:"parent_uuid"`
}

// convertIssue implements POST
// /api/v1/workspaces/{slug}/projects/{identifier}/issues/{uuid}/convert
// (C24T1): converts the issue to a sub-issue of the given parent, or
// back to a standalone issue when parent_uuid is null. 200 with
// {"converted": bool, "issue": {...}} in both directions (converted=false
// when already in the target state). 400 when the issue has sub-issues
// of its own, the parent is itself a sub-issue, or the parent is in
// another project; 409 on self-parent or a cycle; 404 on an unknown
// issue or parent. Member (15)+.
func (h *IssueHandler) convertIssue(c *echo.Context) error {
	var body convertIssueBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	uuid, ok := requireUUIDParam(c, "uuid", "issue id")
	if !ok {
		return nil
	}
	res, err := service.ConvertIssue(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), uuid, CurrentUser(c).ID, body.ParentUUID)
	if err != nil {
		return convertError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{
		"converted": res.Converted,
		"issue":     res.Issue,
	})
}

// convertError maps the C24T1 convert sentinels to HTTP statuses,
// delegating everything else (including the shared SetParent sentinels)
// to parentError.
func convertError(c *echo.Context, err error) error {
	switch {
	case errors.Is(err, service.ErrConvertHasChildren):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "issue has sub-issues; detach or move them first", nil)
	case errors.Is(err, service.ErrConvertParentIsSubIssue):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "parent issue is itself a sub-issue", nil)
	case errors.Is(err, service.ErrConvertCrossProject):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "parent issue is in another project", nil)
	default:
		return parentError(c, err)
	}
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
// ?include_children=1 children attachment (C5T4), the optional
// ?include_custom=1 custom-values attachment (C7T2), and the optional
// ?include_project_structure=1 cycle/module/release membership attachment
// (C19T3). The list query is deliberately untouched (perf): all three are
// fetched only on detail, in one extra query each (structure: three small
// queries). Attachments are keyed/omitted when not requested.
type issueDetailResponse struct {
	service.IssueListItem
	Children         []service.IssueChild           `json:"children,omitempty"`
	CustomValues     map[string]service.CustomValue `json:"custom_values,omitempty"`
	ProjectStructure *service.IssueProjectStructure `json:"project_structure,omitempty"`
}

// getIssueChildren decorates a fetched issue with its child summaries
// when the caller passes ?include_children=1, with its custom values
// when the caller passes ?include_custom=1 (C7T2), and with its
// cycle/module/release membership when the caller passes
// ?include_project_structure=1 (C19T3). Without any flag the response is
// the bare IssueListItem, byte-identical to before.
func (h *IssueHandler) getIssueChildren(c *echo.Context, iss *service.IssueListItem) error {
	qp := c.QueryParams()
	wantChildren := qp.Get("include_children") == "1" || qp.Get("include_children") == "true"
	wantCustom := qp.Get("include_custom") == "1" || qp.Get("include_custom") == "true"
	wantStructure := qp.Get("include_project_structure") == "1" || qp.Get("include_project_structure") == "true"
	if !wantChildren && !wantCustom && !wantStructure {
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
	if wantStructure {
		structure, err := service.GetIssueProjectStructure(c.Request().Context(), h.Pool,
			c.Param("slug"), c.Param("identifier"), iss.ID, CurrentUser(c).ID)
		if err != nil {
			return parentError(c, err)
		}
		resp.ProjectStructure = structure
	}
	return c.JSON(http.StatusOK, resp)
}
