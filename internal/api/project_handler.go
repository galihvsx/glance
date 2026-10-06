package api

import (
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// ProjectHandler serves the project endpoints (Task 12) nested under
// workspaces: CRUD plus the project's states. Every route sits behind
// RequireAuth — membership is the tenancy boundary, so there is no public
// surface here.
type ProjectHandler struct {
	Pool *pgxpool.Pool
}

// RegisterProjectRoutes mounts the project endpoints. Call this before
// the SPA catch-all so API routes are never shadowed.
func RegisterProjectRoutes(e *echo.Echo, h *ProjectHandler) {
	g := e.Group("/api/v1/workspaces/:slug/projects", RequireAuth(h.Pool))
	g.POST("", h.createProject)
	g.GET("", h.listProjects)
	g.GET("/:identifier", h.getProject)
	g.PATCH("/:identifier", h.updateProject)
	g.GET("/:identifier/states", h.listStates)
}

// projectError maps service sentinel errors to HTTP statuses. Unknown
// errors are 500 with no detail leaked.
func projectError(c *echo.Context, err error) error {
	switch {
	// The workspace is confirmed to exist and the caller is a confirmed
	// member on this path, so a missing project is its own 404 — never
	// "workspace not found" (reserved for the actor-resolution path: bad
	// slug or non-member caller).
	case errors.Is(err, service.ErrProjectNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "project not found", nil)
	case errors.Is(err, service.ErrNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "workspace not found", nil)
	case errors.Is(err, service.ErrForbidden):
		return WriteError(c, http.StatusForbidden, ErrCodeForbidden, "forbidden", nil)
	case errors.Is(err, service.ErrIdentifierConflict):
		return WriteError(c, http.StatusConflict, ErrCodeConflict, "identifier already taken", nil)
	case errors.Is(err, service.ErrInvalidIdentifier):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid identifier: 1-12 letters and digits", nil)
	default:
		return WriteInternalError(c)
	}
}

type createProjectBody struct {
	Name       string `json:"name"`
	Identifier string `json:"identifier"`
}

// createProject implements POST /api/v1/workspaces/{slug}/projects: creates
// the project (identifier uppercased, unique per workspace) and seeds its
// default states. 201 with the project JSON.
func (h *ProjectHandler) createProject(c *echo.Context) error {
	var body createProjectBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	p, err := service.CreateProject(c.Request().Context(), h.Pool, c.Param("slug"), CurrentUser(c).ID, body.Name, body.Identifier)
	if err != nil {
		if errors.Is(err, service.ErrNameRequired) {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "name is required", nil)
		}
		return projectError(c, err)
	}
	return c.JSON(http.StatusCreated, p)
}

// listProjects implements GET /api/v1/workspaces/{slug}/projects.
func (h *ProjectHandler) listProjects(c *echo.Context) error {
	projects, err := service.ListProjects(c.Request().Context(), h.Pool, c.Param("slug"), CurrentUser(c).ID)
	if err != nil {
		return projectError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"projects": projects})
}

// getProject implements GET /api/v1/workspaces/{slug}/projects/{identifier}.
func (h *ProjectHandler) getProject(c *echo.Context) error {
	p, err := service.GetProject(c.Request().Context(), h.Pool, c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID)
	if err != nil {
		return projectError(c, err)
	}
	return c.JSON(http.StatusOK, p)
}

type updateProjectBody struct {
	// Pointers: PATCH is partial — a nil field means "not provided, leave
	// untouched". An explicit "" is honored as a clear (null vs empty
	// stays distinct).
	Name        *string `json:"name"`
	Description *string `json:"description"`
	// CloseInDays is the cycle-rollover grace (Task 22): days after a
	// cycle's end_date before incomplete issues detach to the backlog
	// when no next cycle exists. Nil = untouched; 0 detaches at
	// completion; negative is a 400.
	CloseInDays *int `json:"close_in_days"`
}

// updateProject implements PATCH /api/v1/workspaces/{slug}/projects/{identifier}.
// Member (15) or admin (20); the service enforces it.
func (h *ProjectHandler) updateProject(c *echo.Context) error {
	var body updateProjectBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	p, err := service.UpdateProject(c.Request().Context(), h.Pool, c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID,
		service.ProjectPatch{Name: body.Name, Description: body.Description, CloseInDays: body.CloseInDays})
	if err != nil {
		if errors.Is(err, service.ErrNameRequired) {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "name is required", nil)
		}
		if errors.Is(err, service.ErrNothingToUpdate) {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "nothing to update", nil)
		}
		if errors.Is(err, service.ErrInvalidCloseInDays) {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "close_in_days must be >= 0", nil)
		}
		return projectError(c, err)
	}
	return c.JSON(http.StatusOK, p)
}

// listStates implements GET /api/v1/workspaces/{slug}/projects/{identifier}/states.
func (h *ProjectHandler) listStates(c *echo.Context) error {
	states, err := service.ListStates(c.Request().Context(), h.Pool, c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID)
	if err != nil {
		return projectError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"states": states})
}
