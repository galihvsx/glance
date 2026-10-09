package api

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// Module endpoints (C3T1) nested under projects. Every route sits behind
// RequireAuth — workspace membership is the tenancy boundary. Errors use
// the spec §5 envelope via moduleError (which falls back to issueError for
// the shared sentinels).
//
// PATCH tri-state for nullable fields (name is required, status is a
// vocabulary): a missing key leaves the field untouched, an explicit ""
// clears it (NULL), any other value sets it. Applies to description,
// lead_id, start_date, target_date.

// RegisterModuleRoutes mounts the module endpoints. Call this before the
// SPA catch-all so API routes are never shadowed.
func RegisterModuleRoutes(e *echo.Echo, h *IssueHandler) {
	g := e.Group("/api/v1/workspaces/:slug/projects/:identifier/modules", RequireAuth(h.Pool))
	g.POST("", h.createModule)
	g.GET("", h.listModules)
	g.GET("/:moduleID", h.getModule)
	g.PATCH("/:moduleID", h.updateModule)
	g.DELETE("/:moduleID", h.deleteModule)
	g.GET("/:moduleID/issues", h.listModuleIssues)
	g.POST("/:moduleID/issues", h.addModuleIssues)
	g.DELETE("/:moduleID/issues", h.removeModuleIssues)
}

// moduleError maps module sentinel errors to HTTP statuses, delegating
// the shared sentinels (ErrNotFound, ErrProjectNotFound, ErrIssueNotFound,
// ErrForbidden, ErrNameRequired, ErrNothingToUpdate, ErrBulkEmptyIDs) to
// issueError.
func moduleError(c *echo.Context, err error) error {
	switch {
	case errors.Is(err, service.ErrModuleNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "module not found", nil)
	case errors.Is(err, service.ErrModuleConflict):
		return WriteError(c, http.StatusConflict, ErrCodeConflict, "module name already exists", nil)
	case errors.Is(err, service.ErrInvalidModule):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid module: bad status, dates, or lead id", nil)
	case errors.Is(err, service.ErrInvalidModuleID):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid id", nil)
	case errors.Is(err, service.ErrModuleHasIssues):
		return WriteError(c, http.StatusConflict, ErrCodeConflict, "module still has issues: remove them or retry with ?force=true", nil)
	case errors.Is(err, service.ErrLeadNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "lead user not found", nil)
	default:
		return issueError(c, err)
	}
}

type moduleBody struct {
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Status      string  `json:"status"`
	LeadID      *string `json:"lead_id"`
	StartDate   *string `json:"start_date"`  // YYYY-MM-DD
	TargetDate  *string `json:"target_date"` // YYYY-MM-DD
}

// createModule implements POST
// /api/v1/workspaces/{slug}/projects/{identifier}/modules.
// Member (15)+; the service enforces it.
func (h *IssueHandler) createModule(c *echo.Context) error {
	var body moduleBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	module, err := service.CreateModule(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID,
		service.ModuleInput{
			Name:        body.Name,
			Description: body.Description,
			Status:      body.Status,
			LeadID:      body.LeadID,
			StartDate:   body.StartDate,
			TargetDate:  body.TargetDate,
		})
	if err != nil {
		if errors.Is(err, service.ErrNameRequired) {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "name is required", nil)
		}
		return moduleError(c, err)
	}
	return c.JSON(http.StatusCreated, module)
}

// listModules implements GET
// /api/v1/workspaces/{slug}/projects/{identifier}/modules.
func (h *IssueHandler) listModules(c *echo.Context) error {
	modules, err := service.ListModules(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID)
	if err != nil {
		return moduleError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"modules": modules})
}

// getModule implements GET .../modules/{moduleID}.
func (h *IssueHandler) getModule(c *echo.Context) error {
	moduleID, ok := requireUUIDParam(c, "moduleID", "module id")
	if !ok {
		return nil
	}
	module, err := service.GetModule(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, moduleID)
	if err != nil {
		return moduleError(c, err)
	}
	return c.JSON(http.StatusOK, module)
}

type patchModuleBody struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	Status      *string `json:"status"`
	LeadID      *string `json:"lead_id"`
	StartDate   *string `json:"start_date"`
	TargetDate  *string `json:"target_date"`
}

// updateModule implements PATCH .../modules/{moduleID}. Partial
// semantics: missing keys are untouched, "" clears a nullable field.
// Member (15)+.
func (h *IssueHandler) updateModule(c *echo.Context) error {
	var body patchModuleBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	moduleID, ok := requireUUIDParam(c, "moduleID", "module id")
	if !ok {
		return nil
	}
	module, err := service.UpdateModule(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, moduleID,
		service.ModulePatch{
			Name:        body.Name,
			Description: body.Description,
			Status:      body.Status,
			LeadID:      body.LeadID,
			StartDate:   body.StartDate,
			TargetDate:  body.TargetDate,
		})
	if err != nil {
		if errors.Is(err, service.ErrNameRequired) {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "name is required", nil)
		}
		return moduleError(c, err)
	}
	return c.JSON(http.StatusOK, module)
}

// deleteModule implements DELETE .../modules/{moduleID}. Without
// ?force=true a module that still has issues is 409; with force the
// membership rows cascade and the issues themselves are untouched.
// Member (15)+.
func (h *IssueHandler) deleteModule(c *echo.Context) error {
	moduleID, ok := requireUUIDParam(c, "moduleID", "module id")
	if !ok {
		return nil
	}
	force := c.QueryParam("force") == "true"
	if err := service.DeleteModule(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, moduleID, force); err != nil {
		return moduleError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// listModuleIssues implements GET .../modules/{moduleID}/issues: the
// module's live issues as full list items.
func (h *IssueHandler) listModuleIssues(c *echo.Context) error {
	moduleID, ok := requireUUIDParam(c, "moduleID", "module id")
	if !ok {
		return nil
	}
	issues, err := service.ListModuleIssues(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, moduleID)
	if err != nil {
		return moduleError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"issues": issues})
}

type moduleIssuesBody struct {
	IssueIDs []string `json:"issue_ids"`
}

// addModuleIssues implements POST .../modules/{moduleID}/issues
// {issue_ids}. Idempotent: repeats are no-ops. Member (15)+.
func (h *IssueHandler) addModuleIssues(c *echo.Context) error {
	var body moduleIssuesBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	moduleID, ok := requireUUIDParam(c, "moduleID", "module id")
	if !ok {
		return nil
	}
	if err := service.AddModuleIssues(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, moduleID, body.IssueIDs); err != nil {
		return moduleError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"added": len(body.IssueIDs)})
}

// removeModuleIssues implements DELETE .../modules/{moduleID}/issues
// {issue_ids}. Idempotent: removing a non-member is a no-op. Member (15)+.
func (h *IssueHandler) removeModuleIssues(c *echo.Context) error {
	var body moduleIssuesBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	moduleID, ok := requireUUIDParam(c, "moduleID", "module id")
	if !ok {
		return nil
	}
	if err := service.RemoveModuleIssues(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, moduleID, body.IssueIDs); err != nil {
		return moduleError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"removed": len(body.IssueIDs)})
}
