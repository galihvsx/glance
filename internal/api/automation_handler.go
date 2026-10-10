package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// Workflow automation rules (C11T1):
//   GET    /api/v1/workspaces/{slug}/projects/{identifier}/automations
//   GET    /api/v1/workspaces/{slug}/projects/{identifier}/automations/runs
//   POST   /api/v1/workspaces/{slug}/projects/{identifier}/automations
//   PATCH  /api/v1/workspaces/{slug}/projects/{identifier}/automations/{ruleID}
//   DELETE /api/v1/workspaces/{slug}/projects/{identifier}/automations/{ruleID}
//
// Auth (contract-first): reads need member (15)+, writes need member
// (15)+ — the project-settings convention (UpdateProject), documented
// in internal/service/automation.go. Guests (5) get 403 on both; the
// route sits behind RequireAuth so non-members never reach it.
// Errors use the spec §5 envelope via automationError, which delegates
// the shared sentinels (ErrNotFound, ErrForbidden, ErrProjectNotFound)
// to projectError.

// AutomationHandler serves the project automation-rule endpoints.
type AutomationHandler struct {
	Pool *pgxpool.Pool
}

// RegisterAutomationRoutes mounts the automation endpoints. Call this
// before the SPA catch-all so API routes are never shadowed. Keep
// internal/api/openapi_test.go's registerAllAPIRoutes mirror in
// lockstep (route-coverage test).
func RegisterAutomationRoutes(e *echo.Echo, h *AutomationHandler) {
	g := e.Group("/api/v1/workspaces/:slug/projects/:identifier/automations", RequireAuth(h.Pool))
	g.GET("", h.listAutomations)
	g.GET("/runs", h.listAutomationRuns)
	g.POST("", h.createAutomation)
	g.PATCH("/:ruleID", h.updateAutomation)
	g.DELETE("/:ruleID", h.deleteAutomation)
}

// automationError maps automation sentinel errors to HTTP statuses,
// delegating the shared sentinels to projectError.
func automationError(c *echo.Context, err error) error {
	switch {
	case errors.Is(err, service.ErrAutomationRuleNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "automation rule not found", nil)
	case errors.Is(err, service.ErrAutomationRuleLimit):
		return WriteError(c, http.StatusConflict, ErrCodeConflict,
			"automation rule limit reached (max 25 per project)", nil)
	case errors.Is(err, service.ErrInvalidAutomationTrigger):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid automation trigger", nil)
	case errors.Is(err, service.ErrInvalidAutomationAction):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid automation action", nil)
	case errors.Is(err, service.ErrNameRequired):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "name is required", nil)
	case errors.Is(err, service.ErrInvalidAutomationRunLimit):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid limit: want 1-100", nil)
	case errors.Is(err, service.ErrNothingToUpdate):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "nothing to update", nil)
	default:
		return projectError(c, err)
	}
}

type automationRuleBody struct {
	Name    string                     `json:"name"`
	Trigger service.AutomationTrigger  `json:"trigger"`
	Actions []service.AutomationAction `json:"actions"`
}

type automationRulePatchBody struct {
	Name    *string                     `json:"name"`
	Enabled *bool                       `json:"enabled"`
	Trigger *service.AutomationTrigger  `json:"trigger"`
	Actions *[]service.AutomationAction `json:"actions"`
}

// listAutomations implements GET .../automations: the project's rules,
// oldest first, alongside the project's automation run retention window
// (C13T0 — "read alongside rules": the effective window in days, 90 when
// unset, 0 = keep forever). Member (15)+.
func (h *AutomationHandler) listAutomations(c *echo.Context) error {
	ctx := c.Request().Context()
	slug, ident, actor := c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID
	rules, err := service.ListAutomationRules(ctx, h.Pool, slug, ident, actor)
	if err != nil {
		return automationError(c, err)
	}
	retention, err := service.GetAutomationRunRetentionDays(ctx, h.Pool, slug, ident, actor)
	if err != nil {
		return automationError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"automations": rules, "retention_days": retention})
}

// listAutomationRuns implements GET .../automations/runs: the
// project's automation run history, newest first. Query params:
// rule_id?, issue_id? (UUID filters, 400 on malformed), limit?
// (default 20; 0/absent = default, negative or non-integer = 400,
// >100 clamped to 100). Member (15)+.
func (h *AutomationHandler) listAutomationRuns(c *echo.Context) error {
	filter := service.AutomationRunFilter{}
	if raw := c.QueryParam("rule_id"); raw != "" {
		if !validUUID(raw) {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid rule_id", nil)
		}
		filter.RuleID = raw
	}
	if raw := c.QueryParam("issue_id"); raw != "" {
		if !validUUID(raw) {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid issue_id", nil)
		}
		filter.IssueID = raw
	}
	if raw := c.QueryParam("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return automationError(c, service.ErrInvalidAutomationRunLimit)
		}
		filter.Limit = n
	}
	runs, err := service.ListAutomationRuns(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, filter)
	if err != nil {
		return automationError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"runs": runs})
}

// createAutomation implements POST .../automations. Member (15)+; the
// 26th rule per project is 409.
func (h *AutomationHandler) createAutomation(c *echo.Context) error {
	var body automationRuleBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	r, err := service.CreateAutomationRule(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, service.AutomationRuleInput{
			Name:    body.Name,
			Trigger: body.Trigger,
			Actions: body.Actions,
		})
	if err != nil {
		return automationError(c, err)
	}
	return c.JSON(http.StatusCreated, r)
}

// updateAutomation implements PATCH .../automations/{ruleID}. Member (15)+.
func (h *AutomationHandler) updateAutomation(c *echo.Context) error {
	var body automationRulePatchBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	ruleID, ok := requireUUIDParam(c, "ruleID", "automation rule id")
	if !ok {
		return nil
	}
	r, err := service.UpdateAutomationRule(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), ruleID, CurrentUser(c).ID, service.AutomationRulePatch{
			Name:    body.Name,
			Enabled: body.Enabled,
			Trigger: body.Trigger,
			Actions: body.Actions,
		})
	if err != nil {
		return automationError(c, err)
	}
	return c.JSON(http.StatusOK, r)
}

// deleteAutomation implements DELETE .../automations/{ruleID}. Member (15)+.
func (h *AutomationHandler) deleteAutomation(c *echo.Context) error {
	ruleID, ok := requireUUIDParam(c, "ruleID", "automation rule id")
	if !ok {
		return nil
	}
	if err := service.DeleteAutomationRule(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), ruleID, CurrentUser(c).ID); err != nil {
		return automationError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}
