package api

// "My work" endpoint (C6T3): the caller's issues across a workspace's
// projects. Member-scoped to the caller's own user id — inherently
// tenant-safe.

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// MyWorkHandler serves the my-issues route.
type MyWorkHandler struct {
	Pool *pgxpool.Pool
}

// RegisterMyWorkRoutes mounts GET /api/v1/workspaces/:slug/my-issues.
// Call before the SPA catch-all.
func RegisterMyWorkRoutes(e *echo.Echo, h *MyWorkHandler) {
	g := e.Group("/api/v1/workspaces/:slug", RequireAuth(h.Pool))
	g.GET("/my-issues", h.listMyIssues)
}

// listMyIssues implements GET /api/v1/workspaces/{slug}/my-issues.
// ?filter=assigned|created|watched (default assigned), ?limit=1..200
// (default 100), ?pql= (C112T0: workspace-scoped PQL, ANDed with the
// filter + base predicates; blank degrades to the unfiltered filter
// list; a probe failure 400s with message + position, the C91
// contract, so the PQL editor can underline the offending token).
func (h *MyWorkHandler) listMyIssues(c *echo.Context) error {
	u := CurrentUser(c)
	if u == nil {
		return WriteError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized", nil)
	}
	filter := service.MyWorkFilter(c.QueryParam("filter"))
	limit := 100
	if q := c.QueryParam("limit"); q != "" {
		n, err := strconv.Atoi(q)
		if err != nil {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid limit", nil)
		}
		limit = n
	}
	items, err := service.ListMyWorkPQL(c.Request().Context(), h.Pool,
		c.Param("slug"), u.ID, filter, limit, c.QueryParam("pql"))
	if err != nil {
		var pqlErr *service.PQLQueryError
		if errors.As(err, &pqlErr) {
			var details any
			if pqlErr.Pos > 0 {
				details = map[string]any{"position": pqlErr.Pos}
			}
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, pqlErr.Msg, details)
		}
		switch {
		case errors.Is(err, service.ErrNotFound):
			return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "workspace not found", nil)
		case errors.Is(err, service.ErrInvalidMyWorkFilter):
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest,
				"invalid filter: must be assigned, created or watched", nil)
		default:
			return WriteInternalError(c)
		}
	}
	return c.JSON(http.StatusOK, map[string]any{"issues": items})
}
