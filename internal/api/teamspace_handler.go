package api

import (
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// Teamspace endpoints (C89T0, membership + visibility C90T0) nested
// under workspaces — the sidebar grouping surface: CRUD plus project
// assignment via PATCH projects (see updateProject), plus the members
// roster (Admin-only add/remove; members + Admins read). Every route
// sits behind RequireAuth — workspace membership is the tenancy
// boundary, so there is no public surface here.
//
// Error mapping follows the initiative handler's shape: teamspace
// sentinels map here, shared sentinels (ErrNotFound for a bad slug or
// a non-member caller, ErrForbidden for guest/viewer writes,
// ErrNameRequired, ErrNothingToUpdate) delegate to issueError.
// Membership mutations emit no audit-log rows and no broadcast events:
// the C89 audit found grouping entities emit neither, and membership
// follows the same precedent rather than inventing one.

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
	// C90T0: membership roster. Keep docs/openapi.yaml in lockstep —
	// the route-coverage test fails otherwise.
	g.POST("/:teamspaceID/members", h.addTeamspaceMember)
	g.GET("/:teamspaceID/members", h.listTeamspaceMembers)
	g.DELETE("/:teamspaceID/members/:user_id", h.removeTeamspaceMember)
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
	case errors.Is(err, service.ErrTeamspaceTargetNotMember):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "target user is not a workspace member", nil)
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

type addTeamspaceMemberBody struct {
	UserID string `json:"user_id"`
}

// addTeamspaceMember implements POST
// /api/v1/workspaces/{slug}/teamspaces/{teamspaceID}/members.
// Admin (20) only; the service enforces it. 200 with the member row —
// upsert semantics, mirroring the workspace upsertMember handler: a
// duplicate add is idempotent OK, not a fresh creation, so it is not
// a 201. A target who is not a workspace member is a 400 (the same
// error whether the user id matches nobody or a non-member, so no
// existence oracle).
func (h *TeamspaceHandler) addTeamspaceMember(c *echo.Context) error {
	id, ok := requireUUIDParam(c, "teamspaceID", "teamspace id")
	if !ok {
		return nil
	}
	var body addTeamspaceMemberBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	if !validUUID(body.UserID) {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid user_id", nil)
	}
	member, err := service.AddTeamspaceMember(c.Request().Context(), h.Pool, c.Param("slug"), id, CurrentUser(c).ID, body.UserID)
	if err != nil {
		return teamspaceError(c, err)
	}
	return c.JSON(http.StatusOK, member)
}

// listTeamspaceMembers implements GET
// /api/v1/workspaces/{slug}/teamspaces/{teamspaceID}/members:
// the roster ordered by user name. Teamspace members and workspace
// Admins may read; a non-member non-admin gets a 404 (never the
// roster, never a leak of the teamspace's existence).
func (h *TeamspaceHandler) listTeamspaceMembers(c *echo.Context) error {
	id, ok := requireUUIDParam(c, "teamspaceID", "teamspace id")
	if !ok {
		return nil
	}
	members, err := service.ListTeamspaceMembers(c.Request().Context(), h.Pool, c.Param("slug"), id, CurrentUser(c).ID)
	if err != nil {
		return teamspaceError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"members": members})
}

// removeTeamspaceMember implements DELETE
// /api/v1/workspaces/{slug}/teamspaces/{teamspaceID}/members/{user_id}.
// Admin (20) only; the service enforces it. Removing a non-member is
// idempotent OK (200), mirroring the workspace removeMember response
// shape — not the workspace handler's 404-on-non-member, which this
// surface deliberately does not follow (see the service doc).
func (h *TeamspaceHandler) removeTeamspaceMember(c *echo.Context) error {
	id, ok := requireUUIDParam(c, "teamspaceID", "teamspace id")
	if !ok {
		return nil
	}
	userID, ok := requireUUIDParam(c, "user_id", "user_id")
	if !ok {
		return nil
	}
	if err := service.RemoveTeamspaceMember(c.Request().Context(), h.Pool, c.Param("slug"), id, CurrentUser(c).ID, userID); err != nil {
		return teamspaceError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]bool{"ok": true})
}
