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
	g.DELETE("/:identifier", h.deleteProject)
	g.POST("/:identifier/archive", h.archiveProject)
	g.POST("/:identifier/unarchive", h.unarchiveProject)
	g.GET("/:identifier/states", h.listStates)

	// C92T0: direct project membership roster. Keep docs/openapi.yaml
	// in lockstep — the route-coverage test fails otherwise.
	g.GET("/:identifier/members", h.listProjectMembers)
	g.POST("/:identifier/members", h.addProjectMember)
	g.PATCH("/:identifier/members/:user_id", h.changeProjectMemberRole)
	g.DELETE("/:identifier/members/:user_id", h.removeProjectMember)

	// C76T0: workspace-level project-label taxonomy + assignment.
	registerProjectLabelRoutes(e, h)
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
	case errors.Is(err, service.ErrConfirmMismatch):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "confirm must match the project identifier", nil)
	case errors.Is(err, service.ErrInvalidTeamspace):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid teamspace_id: teamspace does not exist or belongs to another workspace", nil)
	case errors.Is(err, service.ErrInvalidProjectVisibility):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid visibility: must be public or private", nil)
	case errors.Is(err, service.ErrProjectTargetNotMember):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "target user is not a workspace member", nil)
	case errors.Is(err, service.ErrProjectMemberNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "project member not found", nil)
	case errors.Is(err, service.ErrProjectMemberRoleInvalid):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid project member role", nil)
	case errors.Is(err, service.ErrLastProjectAdmin):
		return WriteError(c, http.StatusConflict, ErrCodeConflict, "cannot remove the last project admin", nil)
	default:
		// ErrProjectDeleteBlocked carries the live-issue count in the
		// 409 details so the client can name the blocker.
		var blocked *service.DeleteBlockedError
		if errors.As(err, &blocked) {
			return WriteError(c, http.StatusConflict, ErrCodeConflict,
				"project has non-archived issues; archive them first or retry with ?force=true",
				map[string]any{"non_archived_issues": blocked.NonArchivedIssues})
		}
		return WriteInternalError(c)
	}
}

type createProjectBody struct {
	Name       string `json:"name"`
	Identifier string `json:"identifier"`
	// TemplateID is optional (C17T1): when set, the template's
	// states/labels/modules/estimate scale are seeded in the same
	// transaction as the project. Unknown id → 400.
	TemplateID *string `json:"template_id"`
	// Icon is the optional project emoji icon (C18T4): a single emoji.
	// Empty string normalizes to NULL (unset); anything else invalid
	// is a 400.
	Icon *string `json:"icon"`
}

// createProject implements POST /api/v1/workspaces/{slug}/projects: creates
// the project (identifier uppercased, unique per workspace) and seeds its
// default states, or the template's seeds when template_id is given. 201
// with the project JSON.
func (h *ProjectHandler) createProject(c *echo.Context) error {
	var body createProjectBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	p, err := service.CreateProjectWithTemplate(c.Request().Context(), h.Pool, c.Param("slug"), CurrentUser(c).ID, body.Name, body.Identifier, body.TemplateID, body.Icon)
	if err != nil {
		if errors.Is(err, service.ErrNameRequired) {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "name is required", nil)
		}
		if errors.Is(err, service.ErrUnknownTemplateID) {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "unknown template_id", nil)
		}
		if errors.Is(err, service.ErrInvalidIcon) {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid icon: must be a single emoji", nil)
		}
		return projectError(c, err)
	}
	return c.JSON(http.StatusCreated, p)
}

// listProjects implements GET /api/v1/workspaces/{slug}/projects.
// Archived projects are excluded by default (sidebar + list + the
// C18T0 palette all consume this); ?archived=true includes them for the
// projects page's "Archived" section. ?label= (C76T0) filters to
// projects carrying any of the given project-label ids — repeatable
// and comma-separated; a malformed value is 400 naming it, and an id
// that names no label of the workspace is 400 via the service.
func (h *ProjectHandler) listProjects(c *echo.Context) error {
	includeArchived := c.QueryParam("archived") == "true" || c.QueryParam("archived") == "1"
	labelIDs, err := parseProjectLabelParam(c.QueryParams())
	if err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, err.Error(), nil)
	}
	projects, err := service.ListProjects(c.Request().Context(), h.Pool, c.Param("slug"), CurrentUser(c).ID, includeArchived, labelIDs)
	if err != nil {
		return projectLabelError(c, err)
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
	// AutomationRunRetentionDays is the automation run retention window
	// in days (C13T0): nil = untouched; 0 = keep runs forever; unset
	// (NULL) = the 90-day default. Negative is a 400.
	AutomationRunRetentionDays *int `json:"automation_run_retention_days"`
	// Icon is the project emoji icon (C18T4): nil = untouched; a single
	// emoji sets it; an explicit "" clears it (NULL = unset). Anything
	// else invalid is a 400.
	Icon *string `json:"icon"`
	// ParallelCycles is the parallel-cycles toggle (C78T0): nil =
	// untouched; true lets cycles of this project overlap in date;
	// false (default) makes cycle create/update reject ranges
	// overlapping another live cycle. Member (15)+; guests read-only.
	// No announce event: project-update has no broadcast path (verified
	// in internal/service/broadcast.go), and no new domain events are
	// added this cycle.
	ParallelCycles *bool `json:"parallel_cycles"`
	// TeamspaceID is the teamspace assignment (C89T0): tri-state via
	// PatchField — absent = unchanged, explicit null = ungroup,
	// UUID = move. Unknown, malformed, or foreign-workspace ids are
	// 400 (never a silent cross-workspace link).
	TeamspaceID service.PatchField[string] `json:"teamspace_id"`
	// Visibility is the project ACL gate (C92T0): public|private.
	// Changing it requires effective project Admin (20); anything
	// else is a 400. Nil = untouched.
	Visibility *string `json:"visibility"`
}

// updateProject implements PATCH /api/v1/workspaces/{slug}/projects/{identifier}.
// Member (15) or admin (20); the service enforces it.
func (h *ProjectHandler) updateProject(c *echo.Context) error {
	var body updateProjectBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	p, err := service.UpdateProject(c.Request().Context(), h.Pool, c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID,
		service.ProjectPatch{Name: body.Name, Description: body.Description, Icon: body.Icon, CloseInDays: body.CloseInDays,
			AutomationRunRetentionDays: body.AutomationRunRetentionDays, ParallelCycles: body.ParallelCycles, TeamspaceID: body.TeamspaceID, Visibility: body.Visibility})
	if err != nil {
		if errors.Is(err, service.ErrNameRequired) {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "name is required", nil)
		}
		if errors.Is(err, service.ErrNothingToUpdate) {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "nothing to update", nil)
		}
		if errors.Is(err, service.ErrInvalidIcon) {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid icon: must be a single emoji", nil)
		}
		if errors.Is(err, service.ErrInvalidCloseInDays) {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "close_in_days must be >= 0", nil)
		}
		if errors.Is(err, service.ErrInvalidAutomationRunRetentionDays) {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "automation_run_retention_days must be >= 0", nil)
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

// archiveProject implements POST /api/v1/workspaces/{slug}/projects/{identifier}/archive.
// Admin (20) ONLY; members/viewers/guests get 403, non-members 404.
// Archiving is idempotent (already-archived → 200 with the project) and
// archives every live issue of the project in the same transaction.
func (h *ProjectHandler) archiveProject(c *echo.Context) error {
	p, err := service.ArchiveProject(c.Request().Context(), h.Pool, c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, true)
	if err != nil {
		return projectError(c, err)
	}
	return c.JSON(http.StatusOK, p)
}

// unarchiveProject implements POST /api/v1/workspaces/{slug}/projects/{identifier}/unarchive.
// Admin (20) ONLY. Idempotent (already-live → 200). Issues the archive
// itself archived are restored; issues archived individually before the
// project archive stay archived.
func (h *ProjectHandler) unarchiveProject(c *echo.Context) error {
	p, err := service.ArchiveProject(c.Request().Context(), h.Pool, c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, false)
	if err != nil {
		return projectError(c, err)
	}
	return c.JSON(http.StatusOK, p)
}

// deleteProject implements DELETE /api/v1/workspaces/{slug}/projects/{identifier}.
// Admin (20) ONLY — the authz gate runs before any other check, so a
// non-admin learns nothing beyond 403. Safety interlocks:
//   - ?confirm=<identifier> must match the project identifier (400).
//   - With >0 non-archived issues the delete is refused with 409 naming
//     the blocker count, unless ?force=true.
//
// 204 on success; the project and all its children are gone.
func (h *ProjectHandler) deleteProject(c *echo.Context) error {
	qp := c.QueryParams()
	force := qp.Get("force") == "true" || qp.Get("force") == "1"
	err := service.DeleteProject(c.Request().Context(), h.Pool, c.Param("slug"), c.Param("identifier"),
		CurrentUser(c).ID, qp.Get("confirm"), force)
	if err != nil {
		return projectError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}
