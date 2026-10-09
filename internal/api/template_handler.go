package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// Issue template endpoints (C7T0) nested under projects. Every route sits
// behind RequireAuth — workspace membership is the tenancy boundary.
// Errors use the spec §5 envelope via templateError (which falls back to
// issueError for the shared sentinels).
//
// Reads are open to any member (guest 5+); every mutation — including
// apply, which creates an issue — needs member (15)+.

// RegisterTemplateRoutes mounts the issue-template endpoints. Call this
// before the SPA catch-all so API routes are never shadowed.
func RegisterTemplateRoutes(e *echo.Echo, h *IssueHandler) {
	g := e.Group("/api/v1/workspaces/:slug/projects/:identifier/templates", RequireAuth(h.Pool))
	g.POST("", h.createTemplate)
	g.GET("", h.listTemplates)
	g.GET("/:templateID", h.getTemplate)
	g.PATCH("/:templateID", h.updateTemplate)
	g.DELETE("/:templateID", h.deleteTemplate)
	g.POST("/:templateID/apply", h.applyTemplate)
}

// templateError maps template sentinel errors to HTTP statuses,
// delegating the shared sentinels (ErrNotFound, ErrProjectNotFound,
// ErrForbidden, ErrNameRequired, ErrNothingToUpdate, ErrInvalidPriority,
// ErrInvalidState, ...) to issueError. A TemplateStaleRef is an honest
// 404 naming the missing ref kind + id.
func templateError(c *echo.Context, err error) error {
	var stale *service.TemplateStaleRef
	if errors.As(err, &stale) {
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound,
			"template references a missing "+stale.Kind+": "+stale.ID, nil)
	}
	switch {
	case errors.Is(err, service.ErrTemplateNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "template not found", nil)
	case errors.Is(err, service.ErrTemplateConflict):
		return WriteError(c, http.StatusConflict, ErrCodeConflict, "template name already exists", nil)
	case errors.Is(err, service.ErrInvalidTemplate):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid template: bad name, priority, or template_data", nil)
	case errors.Is(err, service.ErrInvalidTemplateID):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid id", nil)
	default:
		return issueError(c, err)
	}
}

type templateBody struct {
	Name         string          `json:"name"`
	Description  *string         `json:"description"`
	TemplateData json.RawMessage `json:"template_data"`
}

// createTemplate implements POST
// /api/v1/workspaces/{slug}/projects/{identifier}/templates.
func (h *IssueHandler) createTemplate(c *echo.Context) error {
	var body templateBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	tmpl, err := service.CreateTemplate(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID,
		service.TemplateInput{
			Name:         body.Name,
			Description:  body.Description,
			TemplateData: body.TemplateData,
		})
	if err != nil {
		if errors.Is(err, service.ErrNameRequired) {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "name is required", nil)
		}
		return templateError(c, err)
	}
	return c.JSON(http.StatusCreated, tmpl)
}

// listTemplates implements GET
// /api/v1/workspaces/{slug}/projects/{identifier}/templates.
func (h *IssueHandler) listTemplates(c *echo.Context) error {
	tmpls, err := service.ListTemplates(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID)
	if err != nil {
		return templateError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"templates": tmpls})
}

// getTemplate implements GET .../templates/{templateID}.
func (h *IssueHandler) getTemplate(c *echo.Context) error {
	templateID, ok := requireUUIDParam(c, "templateID", "template id")
	if !ok {
		return nil
	}
	tmpl, err := service.GetTemplate(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, templateID)
	if err != nil {
		return templateError(c, err)
	}
	return c.JSON(http.StatusOK, tmpl)
}

type templatePatchBody struct {
	Name         *string         `json:"name"`
	Description  *string         `json:"description"`
	TemplateData json.RawMessage `json:"template_data"`
}

// hasAnyTemplatePatchKey reports whether the body carries at least one
// known patch key, so PATCH {} is 400 (nothing to update) rather than a
// no-op 200.
func hasAnyTemplatePatchKey(body templatePatchBody) bool {
	return body.Name != nil || body.Description != nil || body.TemplateData != nil
}

// updateTemplate implements PATCH .../templates/{templateID}.
func (h *IssueHandler) updateTemplate(c *echo.Context) error {
	templateID, ok := requireUUIDParam(c, "templateID", "template id")
	if !ok {
		return nil
	}
	var body templatePatchBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	if !hasAnyTemplatePatchKey(body) {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "nothing to update", nil)
	}
	tmpl, err := service.UpdateTemplate(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, templateID,
		service.TemplatePatch{
			Name:         body.Name,
			Description:  body.Description,
			TemplateData: body.TemplateData,
		})
	if err != nil {
		return templateError(c, err)
	}
	return c.JSON(http.StatusOK, tmpl)
}

// deleteTemplate implements DELETE .../templates/{templateID}.
func (h *IssueHandler) deleteTemplate(c *echo.Context) error {
	templateID, ok := requireUUIDParam(c, "templateID", "template id")
	if !ok {
		return nil
	}
	if err := service.DeleteTemplate(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, templateID); err != nil {
		return templateError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

type applyTemplateBody struct {
	Name            *string         `json:"name"`
	Description     json.RawMessage `json:"description"`
	Priority        *int            `json:"priority"`
	StateID         *string         `json:"state_id"`
	EstimatePointID *string         `json:"estimate_point_id"`
	LabelIDs        *[]string       `json:"label_ids"`
}

// applyTemplate implements POST .../templates/{templateID}/apply: merge
// the template defaults with the optional body overrides, validate every
// referenced state/label/estimate still exists, and create the issue.
// A stale ref is 404 (the honest error), not a silently wrong issue.
// Member (15)+.
func (h *IssueHandler) applyTemplate(c *echo.Context) error {
	templateID, ok := requireUUIDParam(c, "templateID", "template id")
	if !ok {
		return nil
	}
	var body applyTemplateBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	issue, err := service.ApplyTemplate(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, templateID,
		service.TemplateOverrides{
			Name:            body.Name,
			Description:     body.Description,
			Priority:        body.Priority,
			StateID:         body.StateID,
			EstimatePointID: body.EstimatePointID,
			LabelIDs:        body.LabelIDs,
		})
	if err != nil {
		return templateError(c, err)
	}
	return c.JSON(http.StatusCreated, issue)
}
