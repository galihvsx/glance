package api

// Workflow transition controls endpoints (C68T0): per-project
// state-transition allowlist management.
//
// GET  /api/v1/workspaces/{slug}/projects/{identifier}/workflow — any
//      workspace member may read (guests included), mirroring the states
//      list auth.
// PUT  /api/v1/workspaces/{slug}/projects/{identifier}/workflow — member
//      (15)+, mirroring the states API auth convention; the service is
//      the gate, the handler maps sentinels to the spec §5 error envelope.
//
// A denied state move anywhere (PATCH /issues/{id}, bulk paths)
// answers 409 with code "state_transition_not_allowed" and details
// {"allowed": [...]} naming the permitted next-state IDs.

import (
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// WorkflowHandler serves the workflow routes.
type WorkflowHandler struct {
	Pool *pgxpool.Pool
}

// RegisterWorkflowRoutes mounts the workflow endpoints nested under the
// project path. Call before the SPA catch-all.
func RegisterWorkflowRoutes(e *echo.Echo, h *WorkflowHandler) {
	g := e.Group("/api/v1/workspaces/:slug/projects/:identifier", RequireAuth(h.Pool))
	g.GET("/workflow", h.getWorkflow)
	g.PUT("/workflow", h.setWorkflow)
}

// workflowError maps workflow-domain sentinels to HTTP statuses.
func workflowError(c *echo.Context, err error) error {
	switch {
	case errors.Is(err, service.ErrProjectNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "project not found", nil)
	case errors.Is(err, service.ErrNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "workspace not found", nil)
	case errors.Is(err, service.ErrForbidden):
		return WriteError(c, http.StatusForbidden, ErrCodeForbidden, "forbidden", nil)
	case errors.Is(err, service.ErrInvalidState):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest,
			"invalid state: every transition endpoint must be a state of this project", nil)
	case errors.Is(err, service.ErrWorkflowSelfTransition):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest,
			"transition must move to a different state", nil)
	default:
		return WriteInternalError(c)
	}
}

type setWorkflowBody struct {
	Transitions []service.WorkflowTransition `json:"transitions"`
}

// getWorkflow implements GET /api/v1/workspaces/{slug}/projects/{identifier}/workflow.
func (h *WorkflowHandler) getWorkflow(c *echo.Context) error {
	u := CurrentUser(c)
	if u == nil {
		return WriteError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized", nil)
	}
	wf, err := service.GetWorkflow(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), u.ID)
	if err != nil {
		return workflowError(c, err)
	}
	return c.JSON(http.StatusOK, wf)
}

// setWorkflow implements PUT /api/v1/workspaces/{slug}/projects/{identifier}/workflow.
func (h *WorkflowHandler) setWorkflow(c *echo.Context) error {
	u := CurrentUser(c)
	if u == nil {
		return WriteError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized", nil)
	}
	var b setWorkflowBody
	if err := c.Bind(&b); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	wf, err := service.SetWorkflow(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), u.ID, b.Transitions)
	if err != nil {
		return workflowError(c, err)
	}
	return c.JSON(http.StatusOK, wf)
}
