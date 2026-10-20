package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// Project activity feed endpoints (C5T7): GET
// /api/v1/workspaces/{slug}/projects/{identifier}/activity?limit=50.
//
// Workspace activity feed (C17T4): GET
// /api/v1/workspaces/{slug}/activity?limit&before&types&actor — the same rows
// aggregated across every project of the workspace the caller is a
// member (15)+ of, with cursor pagination, an optional field-type
// filter, and an optional ?actor= member-UUID filter (C80T0).
//
// Read-only; the routes sit behind RequireAuth — workspace membership is
// the tenancy boundary. Reads need member (15)+: guests (5) get 403, and
// outsiders get the shared 404. Errors use the spec §5 envelope via
// activityError, which delegates the shared sentinels to issueError.

// RegisterActivityRoutes mounts the activity feed endpoints. Call this
// before the SPA catch-all so API routes are never shadowed.
func RegisterActivityRoutes(e *echo.Echo, h *IssueHandler) {
	g := e.Group("/api/v1/workspaces/:slug/projects/:identifier", RequireAuth(h.Pool))
	g.GET("/activity", h.getProjectActivity)
	// C17T4: the workspace-wide feed lives on the workspace path. The
	// literal "activity" segment wins over the ":slug" param of the
	// workspace group, so there is no route conflict.
	wg := e.Group("/api/v1/workspaces/:slug", RequireAuth(h.Pool))
	wg.GET("/activity", h.getWorkspaceActivity)
}

// activityError maps activity sentinel errors to HTTP statuses,
// delegating the shared sentinels (ErrNotFound, ErrForbidden,
// ErrProjectNotFound) to issueError. ErrInvalidActivityActor is the
// malformed ?actor= UUID (400); ErrMemberNotFound is an ?actor= UUID
// that matches no member of the workspace (404, same sentinel as the
// members endpoints).
func activityError(c *echo.Context, err error) error {
	switch {
	case errors.Is(err, service.ErrInvalidActivityLimit):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid limit: want 1-200", nil)
	case errors.Is(err, service.ErrInvalidActivityCursor):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid before: not a valid activity cursor", nil)
	case errors.Is(err, service.ErrInvalidActivityActor):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid actor: not a UUID", nil)
	case errors.Is(err, service.ErrMemberNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeMemberNotFound, "member not found", nil)
	default:
		return issueError(c, err)
	}
}

// getProjectActivity implements GET
// /api/v1/workspaces/{slug}/projects/{identifier}/activity?limit=50:
// the project's field-level change feed, newest first. The limit
// defaults to 50 and is capped at 200.
func (h *IssueHandler) getProjectActivity(c *echo.Context) error {
	limit := service.ActivityDefaultLimit
	if raw := c.QueryParam("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return activityError(c, service.ErrInvalidActivityLimit)
		}
		limit = n
	}
	entries, err := service.GetProjectActivity(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, limit)
	if err != nil {
		return activityError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"activity": entries})
}

// getWorkspaceActivity implements GET
// /api/v1/workspaces/{slug}/activity?limit&before&types&actor: the workspace's
// field-level change feed across all its projects, newest first. The
// limit defaults to 50 and is capped at 200; `before` is the opaque
// cursor from a previous page's `next_cursor`; `types` is a
// comma-separated list of issue_activities.field values (e.g.
// "state_id,priority"). `actor` (C80T0) is a workspace member's UUID,
// filtering the feed to that member's rows; a malformed UUID is 400,
// a UUID matching no workspace member is 404. `next_cursor` is null
// when the feed is exhausted.
func (h *IssueHandler) getWorkspaceActivity(c *echo.Context) error {
	limit := service.ActivityDefaultLimit
	if raw := c.QueryParam("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return activityError(c, service.ErrInvalidActivityLimit)
		}
		limit = n
	}
	var types []string
	if raw := c.QueryParam("types"); raw != "" {
		types = strings.Split(raw, ",")
	}
	// C80T0: validate the actor UUID eagerly so a malformed value is 400
	// (the honest-error rule of the other list filters); the service
	// re-validates as defense-in-depth and resolves membership to 404.
	var actorFilter string
	if raw := c.QueryParam("actor"); raw != "" {
		if _, err := uuid.Parse(raw); err != nil {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid actor: not a UUID", nil)
		}
		actorFilter = raw
	}
	page, err := service.GetWorkspaceActivity(c.Request().Context(), h.Pool,
		c.Param("slug"), CurrentUser(c).ID, limit, c.QueryParam("before"), types, actorFilter)
	if err != nil {
		return activityError(c, err)
	}
	var nextCursor any
	if page.NextCursor != "" {
		nextCursor = page.NextCursor
	}
	return c.JSON(http.StatusOK, map[string]any{
		"activity":    page.Entries,
		"next_cursor": nextCursor,
	})
}
