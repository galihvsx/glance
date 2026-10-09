package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// RegisterWorkItemRoutes mounts the global work-item endpoints under
// /api/v1. Call this before the SPA catch-all so API routes are never
// shadowed. It reuses IssueHandler for its pool.
func RegisterWorkItemRoutes(e *echo.Echo, h *IssueHandler) {
	g := e.Group("/api/v1", RequireAuth(h.Pool))
	g.GET("/work-items/:displayID", h.getWorkItemByDisplayID)
	g.GET("/search", h.searchIssues)
}

// workItemError maps service sentinel errors to HTTP statuses. Unknown
// errors are 500 with no detail leaked. Not-found variants share one
// message — the caller must not learn whether the ID was malformed,
// unknown, ambiguous, or in a workspace they cannot see.
func workItemError(c *echo.Context, err error) error {
	switch {
	case errors.Is(err, service.ErrWorkItemNotFound),
		errors.Is(err, service.ErrNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "work item not found", nil)
	case errors.Is(err, service.ErrQueryRequired):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "q is required", nil)
	case errors.Is(err, service.ErrInvalidCursor):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid cursor", nil)
	case errors.Is(err, service.ErrInvalidListFilter):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid per_page: want 1-100", nil)
	default:
		return WriteInternalError(c)
	}
}

// getWorkItemByDisplayID implements GET /api/v1/work-items/{display-id}:
// resolves {IDENTIFIER}-{SEQ} (e.g. ENG-123) to the same issue-detail
// shape as GET .../issues/{uuid}. Malformed IDs are 404, not 400.
func (h *IssueHandler) getWorkItemByDisplayID(c *echo.Context) error {
	iss, err := service.GetIssueByDisplayID(c.Request().Context(), h.Pool, CurrentUser(c).ID, c.Param("displayID"))
	if err != nil {
		return workItemError(c, err)
	}
	return c.JSON(http.StatusOK, iss)
}

// searchIssues implements GET /api/v1/search?q=: global full-text search
// over issue titles and descriptions, scoped to the caller's workspaces.
// 200 with the {results, next_cursor} envelope (spec §5).
func (h *IssueHandler) searchIssues(c *echo.Context) error {
	qp := c.QueryParams()

	// C2T8: per_page is REJECTED (400) when out of range — see listIssues
	// for the rationale (documented contract "want 1-100"; fail fast
	// instead of silently returning fewer rows).
	perPage := 25
	if s := qp.Get("per_page"); s != "" {
		p, err := strconv.Atoi(s)
		if err != nil || p < 1 || p > 100 {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid per_page: want 1-100", nil)
		}
		perPage = p
	}

	res, err := service.SearchIssues(c.Request().Context(), h.Pool,
		CurrentUser(c).ID, qp.Get("q"), qp.Get("cursor"), perPage)
	if err != nil {
		return workItemError(c, err)
	}
	return c.JSON(http.StatusOK, res)
}
