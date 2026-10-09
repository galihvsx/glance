package api

// State management endpoints (C6T2): create / update / delete project
// states under the project path. List stays on ProjectHandler (GET
// /:identifier/states). All mutations are member (15)+; the service is
// the gate, the handler maps sentinels to the spec §5 error envelope.

import (
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// StateHandler serves the state CRUD routes.
type StateHandler struct {
	Pool *pgxpool.Pool
}

// RegisterStateRoutes mounts the state endpoints nested under the project
// path. Call before the SPA catch-all.
func RegisterStateRoutes(e *echo.Echo, h *StateHandler) {
	g := e.Group("/api/v1/workspaces/:slug/projects/:identifier", RequireAuth(h.Pool))
	g.POST("/states", h.createState)
	g.PATCH("/states/:stateID", h.updateState)
	g.DELETE("/states/:stateID", h.deleteState)
}

// stateError maps state-domain sentinels to HTTP statuses.
func stateError(c *echo.Context, err error) error {
	switch {
	case errors.Is(err, service.ErrProjectNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "project not found", nil)
	case errors.Is(err, service.ErrNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "workspace not found", nil)
	case errors.Is(err, service.ErrForbidden):
		return WriteError(c, http.StatusForbidden, ErrCodeForbidden, "forbidden", nil)
	case errors.Is(err, service.ErrNameRequired):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "name is required", nil)
	case errors.Is(err, service.ErrInvalidStateGroup):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest,
			"invalid group: must be one of triage, backlog, unstarted, started, completed, cancelled", nil)
	case errors.Is(err, service.ErrNothingToUpdate):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "nothing to update", nil)
	case errors.Is(err, service.ErrInvalidReassign):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest,
			"invalid reassign_to: must be another state in this project", nil)
	case errors.Is(err, service.ErrStateNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "state not found", nil)
	case errors.Is(err, service.ErrStateConflict):
		return WriteError(c, http.StatusConflict, ErrCodeConflict, "state name already exists", nil)
	case errors.Is(err, service.ErrStateInUse):
		return WriteError(c, http.StatusConflict, ErrCodeConflict,
			"state is used by issues: pass reassign_to to move them", nil)
	case errors.Is(err, service.ErrLastState):
		return WriteError(c, http.StatusConflict, ErrCodeConflict,
			"project must keep at least one state", nil)
	default:
		return WriteInternalError(c)
	}
}

type createStateBody struct {
	Name     string `json:"name"`
	Group    string `json:"group"`
	Color    string `json:"color"`
	Sequence *int   `json:"sequence"`
}

// createState implements POST /api/v1/workspaces/{slug}/projects/{identifier}/states.
func (h *StateHandler) createState(c *echo.Context) error {
	u := CurrentUser(c)
	if u == nil {
		return WriteError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized", nil)
	}
	var b createStateBody
	if err := c.Bind(&b); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	s, err := service.CreateState(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), u.ID,
		service.StateInput{Name: b.Name, Group: b.Group, Color: b.Color, Sequence: b.Sequence})
	if err != nil {
		return stateError(c, err)
	}
	return c.JSON(http.StatusCreated, s)
}

type updateStateBody struct {
	Name     *string `json:"name"`
	Group    *string `json:"group"`
	Color    *string `json:"color"`
	Sequence *int    `json:"sequence"`
}

// updateState implements PATCH /api/v1/workspaces/{slug}/projects/{identifier}/states/{stateID}.
func (h *StateHandler) updateState(c *echo.Context) error {
	u := CurrentUser(c)
	if u == nil {
		return WriteError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized", nil)
	}
	stateID, ok := requireUUIDParam(c, "stateID", "state id")
	if !ok {
		return nil
	}
	var b updateStateBody
	if err := c.Bind(&b); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	s, err := service.UpdateState(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), stateID, u.ID,
		service.StatePatch{Name: b.Name, Group: b.Group, Color: b.Color, Sequence: b.Sequence})
	if err != nil {
		return stateError(c, err)
	}
	return c.JSON(http.StatusOK, s)
}

// deleteState implements DELETE
// /api/v1/workspaces/{slug}/projects/{identifier}/states/{stateID}[?reassign_to={stateID}].
func (h *StateHandler) deleteState(c *echo.Context) error {
	u := CurrentUser(c)
	if u == nil {
		return WriteError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized", nil)
	}
	stateID, ok := requireUUIDParam(c, "stateID", "state id")
	if !ok {
		return nil
	}
	var reassignTo *string
	if q := c.QueryParam("reassign_to"); q != "" {
		reassignTo = &q
	}
	if err := service.DeleteState(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), stateID, u.ID, reassignTo); err != nil {
		return stateError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}
