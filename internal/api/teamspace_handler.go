package api

import (
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// Teamspace endpoints (C89T0) nested under workspaces — the sidebar
// grouping surface: CRUD plus project assignment via PATCH projects
// (see updateProject). Every route sits behind RequireAuth — workspace
// membership is the tenancy boundary, so there is no public surface
// here.
//
// Error mapping follows the initiative handler's shape: teamspace
// sentinels map here, shared sentinels (ErrNotFound for a bad slug or
// a non-member caller, ErrForbidden for guest/viewer writes,
// ErrNameRequired, ErrNothingToUpdate) delegate to issueError.

type TeamspaceHandler struct {
	Pool *pgxpool.Pool
}

// RegisterTeamspaceRoutes mounts the teamspace endpoints. Call this
// before the SPA catch-all so API routes are never shadowed. Keep
// registerAllAPIRoutes in internal/api/openapi_test.go and
// cmd/glance/main.go in lockstep — the route-coverage test fails
// otherwise.
func RegisterTeamspaceRoutes(e *echo.Echo, h *TeamspaceHandler) {
	g := e.Group("/api/v1/workspaces/:slug/teamspaces", RequireAuth(h.Pool))
	g.POST("", h.createTeamspace)
	g.GET("", h.listTeamspaces)
	g.PATCH("/:teamspaceID", h.updateTeamspace)
	g.DELETE("/:teamspaceID", h.deleteTeamspace)
}

// teamspaceError maps service sentinel errors to HTTP statuses. Unknown
// errors are 500 with no detail leaked.
func teamspaceError(c *echo.Context, err error) error {
	switch {
	case errors.Is(err, service.ErrTeamspaceNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "teamspace not found", nil)
	case errors.Is(err, service.ErrTeamspaceConflict):
		return WriteError(c, http.StatusConflict, ErrCodeConflict, "teamspace name already exists", nil)
	case errors.Is(err, service.ErrInvalidTeamspaceID):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid teamspace id", nil)
	default:
		return issueError(c, err)
	}
}

type createTeamspaceBody struct {
	Name string `json:"name"`
}

// createTeamspace implements POST /api/v1/workspaces/{slug}/teamspaces.
// Member (15)+; the service enforces it. 201 with the teamspace JSON.
func (h *TeamspaceHandler) createTeamspace(c *echo.Context) error {
	var body createTeamspaceBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	ts, err := service.CreateTeamspace(c.Request().Context(), h.Pool, c.Param("slug"), CurrentUser(c).ID, body.Name)
	if err != nil {
		return teamspaceError(c, err)
	}
	return c.JSON(http.StatusCreated, ts)
}

// listTeamspaces implements GET /api/v1/workspaces/{slug}/teamspaces:
// ordered by name, each entry carrying its live project count. Any
// workspace member may read.
func (h *TeamspaceHandler) listTeamspaces(c *echo.Context) error {
	teamspaces, err := service.ListTeamspaces(c.Request().Context(), h.Pool, c.Param("slug"), CurrentUser(c).ID)
	if err != nil {
		return teamspaceError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"teamspaces": teamspaces})
}

type updateTeamspaceBody struct {
	Name *string `json:"name"`
}

// updateTeamspace implements PATCH
// /api/v1/workspaces/{slug}/teamspaces/{teamspaceID} (rename only).
// Member (15)+; the service enforces it. A body without name is
// "nothing to update" (400); renaming to the same name is a no-op
// success.
func (h *TeamspaceHandler) updateTeamspace(c *echo.Context) error {
	id, ok := requireUUIDParam(c, "teamspaceID", "teamspace id")
	if !ok {
		return nil
	}
	var body updateTeamspaceBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	ts, err := service.UpdateTeamspace(c.Request().Context(), h.Pool, c.Param("slug"), CurrentUser(c).ID, id,
		service.TeamspacePatch{Name: body.Name})
	if err != nil {
		return teamspaceError(c, err)
	}
	return c.JSON(http.StatusOK, ts)
}

// deleteTeamspace implements DELETE
// /api/v1/workspaces/{slug}/teamspaces/{teamspaceID}. Member (15)+;
// the service enforces it. Deleting a teamspace never deletes
// projects: the response names exactly how many projects were
// ungrouped so the UI can say "N projects moved to Ungrouped"
// honestly.
func (h *TeamspaceHandler) deleteTeamspace(c *echo.Context) error {
	id, ok := requireUUIDParam(c, "teamspaceID", "teamspace id")
	if !ok {
		return nil
	}
	count, err := service.DeleteTeamspace(c.Request().Context(), h.Pool, c.Param("slug"), CurrentUser(c).ID, id)
	if err != nil {
		return teamspaceError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"id": id, "ungrouped_projects": count})
}
