package api

import (
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"glance/internal/config"
	"glance/internal/service"
)

// AIHandler serves the AI assist endpoints (C5T2): issue description
// drafting and triage suggestions, backed by a provider-agnostic
// OpenAI-compatible /chat/completions endpoint. Every route sits behind
// RequireAuth and is member-scoped to the workspace/project named in
// the request body (non-members get 404 — the tenancy boundary).
//
// When no provider key is configured the endpoints answer 503 with code
// ai_not_configured: an honest "not configured", never a fake error.
// Provider failures surface as 502 (bad_gateway), never 500.
type AIHandler struct {
	Pool *pgxpool.Pool
	AI   config.AIConfig
}

// RegisterAIRoutes mounts the AI assist endpoints. Call before the SPA
// catch-all.
func RegisterAIRoutes(e *echo.Echo, h *AIHandler) {
	g := e.Group("/api/v1/ai", RequireAuth(h.Pool))
	g.GET("/status", h.status)
	g.POST("/draft", h.draft)
	g.POST("/triage", h.triage)
}

// aiError maps service sentinel errors to HTTP statuses. The API key
// must never appear in logs or responses: provider errors are generic
// by construction (see internal/service/ai.go).
func aiError(c *echo.Context, err error) error {
	switch {
	case errors.Is(err, service.ErrAINotConfigured):
		return WriteError(c, http.StatusServiceUnavailable, ErrCodeAINotConfigured,
			"AI assist is not configured on this server", nil)
	case errors.Is(err, service.ErrAIProvider):
		return WriteError(c, http.StatusBadGateway, ErrCodeBadGateway,
			"AI provider error", nil)
	case errors.Is(err, service.ErrProjectNotFound), errors.Is(err, service.ErrNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "project not found", nil)
	default:
		return WriteInternalError(c)
	}
}

// aiStatusResponse is the GET /api/v1/ai/status payload.
type aiStatusResponse struct {
	Configured bool `json:"configured"`
}

// status implements GET /api/v1/ai/status: {configured}. Auth-only — no
// project scope is needed because it reveals nothing but a boolean.
// The UI uses it for its honest disabled state ("AI not configured by
// administrator") without burning a provider call, which neither the
// draft nor the triage endpoint can do cheaply (both validate input
// before consulting the provider and then call it).
func (h *AIHandler) status(c *echo.Context) error {
	return c.JSON(http.StatusOK, aiStatusResponse{Configured: h.AI.Configured()})
}

// aiScope carries the workspace/project tenancy both AI endpoints need.
type aiScope struct {
	WorkspaceSlug     string `json:"workspace_slug"`
	ProjectIdentifier string `json:"project_identifier"`
}

func (s aiScope) valid() bool {
	return s.WorkspaceSlug != "" && s.ProjectIdentifier != ""
}

type aiDraftBody struct {
	aiScope
	Title   string `json:"title"`
	Context string `json:"context"`
}

type aiDraftResponse struct {
	Description string `json:"description"`
}

// draft implements POST /api/v1/ai/draft: {workspace_slug,
// project_identifier, title, context?} → {description}.
func (h *AIHandler) draft(c *echo.Context) error {
	var body aiDraftBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	if !body.valid() {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest,
			"workspace_slug and project_identifier are required", nil)
	}
	if body.Title == "" {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "title is required", nil)
	}
	u := CurrentUser(c)
	if u == nil {
		return WriteError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized", nil)
	}
	desc, err := service.DraftIssueDescription(c.Request().Context(), h.Pool, h.AI,
		body.WorkspaceSlug, body.ProjectIdentifier, u.ID, body.Title, body.Context)
	if err != nil {
		return aiError(c, err)
	}
	return c.JSON(http.StatusOK, aiDraftResponse{Description: desc})
}

type aiTriageBody struct {
	aiScope
	Title       string `json:"title"`
	Description string `json:"description"`
}

// triage implements POST /api/v1/ai/triage: {workspace_slug,
// project_identifier, title, description?} → {priority, label_names,
// state_name?}.
func (h *AIHandler) triage(c *echo.Context) error {
	var body aiTriageBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	if !body.valid() {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest,
			"workspace_slug and project_identifier are required", nil)
	}
	if body.Title == "" {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "title is required", nil)
	}
	u := CurrentUser(c)
	if u == nil {
		return WriteError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "unauthorized", nil)
	}
	result, err := service.TriageIssue(c.Request().Context(), h.Pool, h.AI,
		body.WorkspaceSlug, body.ProjectIdentifier, u.ID, body.Title, body.Description)
	if err != nil {
		return aiError(c, err)
	}
	return c.JSON(http.StatusOK, result)
}
