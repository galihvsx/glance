package api

import (
	"errors"
	"io"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// GitHub two-way linkage endpoints (C17T0): manual link/unlink of PRs and
// commits on an issue, plus the HMAC-verified inbound repo webhook.
//
// The refs routes sit behind RequireAuth — workspace membership is the
// tenancy boundary. Reads need any member (guest 5+); mutations need
// member (15)+, enforced in the service. The webhook route is intentionally
// PUBLIC (GitHub cannot present a session): authentication is the
// X-Hub-Signature-256 HMAC against the workspace's stored secret, and a
// bad signature is 401. Errors use the spec §5 envelope via
// githubLinkError (which falls back to issueError for the shared
// sentinels).

// RegisterGitHubLinkRoutes mounts the manual link/unlink endpoints. Call
// this before the SPA catch-all so API routes are never shadowed.
func RegisterGitHubLinkRoutes(e *echo.Echo, h *IssueHandler) {
	g := e.Group("/api/v1/workspaces/:slug/projects/:identifier/issues/:uuid/refs", RequireAuth(h.Pool))
	g.POST("", h.createExternalRef)
	g.DELETE("/:refID", h.deleteExternalRef)
}

// RegisterGitHubWebhookRoutes mounts the inbound GitHub webhook. It is
// deliberately outside every auth group: GitHub signs deliveries with the
// per-workspace secret instead of presenting credentials.
func RegisterGitHubWebhookRoutes(e *echo.Echo, h *GitHubWebhookHandler) {
	e.POST("/api/v1/integrations/github/webhook", h.githubWebhook)
}

// GitHubWebhookHandler serves the inbound GitHub webhook endpoint.
type GitHubWebhookHandler struct {
	Pool *pgxpool.Pool
}

// githubLinkError maps external-ref sentinel errors to HTTP statuses,
// delegating the shared sentinels (ErrIssueNotFound, ErrProjectNotFound,
// ErrNotFound, ErrForbidden) to issueError.
func githubLinkError(c *echo.Context, err error) error {
	switch {
	case errors.Is(err, service.ErrExternalRefConflict):
		return WriteError(c, http.StatusConflict, ErrCodeConflict, "external ref already linked to this issue", nil)
	case errors.Is(err, service.ErrExternalRefNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "external ref not found", nil)
	case errors.Is(err, service.ErrInvalidExternalRef):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid external ref: provider must be github, ref_type pr|commit, repo owner/name, ref_id a PR number or commit SHA, url http(s)", nil)
	default:
		return issueError(c, err)
	}
}

type createExternalRefBody struct {
	Provider string `json:"provider"` // optional; defaults to github
	Repo     string `json:"repo"`     // owner/name
	RefType  string `json:"ref_type"` // pr | commit
	RefID    string `json:"ref_id"`   // PR number | commit SHA
	URL      string `json:"url"`
}

// createExternalRef implements POST
// /api/v1/workspaces/{slug}/projects/{identifier}/issues/{uuid}/refs:
// manually link a PR or commit to the issue. 201 with the ref; 409 on a
// duplicate link; 400 on a malformed body. Member (15)+.
func (h *IssueHandler) createExternalRef(c *echo.Context) error {
	var body createExternalRefBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	slug, ident, uuid, actor, ok := h.issueParams(c)
	if !ok {
		return nil
	}
	ref, err := service.CreateExternalRef(c.Request().Context(), h.Pool,
		slug, ident, uuid, actor, service.CreateExternalRefInput{
			Provider: body.Provider,
			Repo:     body.Repo,
			RefType:  body.RefType,
			RefID:    body.RefID,
			URL:      body.URL,
		})
	if err != nil {
		return githubLinkError(c, err)
	}
	return c.JSON(http.StatusCreated, ref)
}

// deleteExternalRef implements DELETE .../issues/{uuid}/refs/{refID}:
// unlink a PR/commit from the issue. 204 on success; 404 when the ref
// does not exist on this issue. Member (15)+.
func (h *IssueHandler) deleteExternalRef(c *echo.Context) error {
	slug, ident, uuid, actor, ok := h.issueParams(c)
	if !ok {
		return nil
	}
	refID, ok := requireUUIDParam(c, "refID", "ref id")
	if !ok {
		return nil
	}
	if err := service.DeleteExternalRef(c.Request().Context(), h.Pool, slug, ident, uuid, actor, refID); err != nil {
		return githubLinkError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// githubWebhook implements POST /api/v1/integrations/github/webhook: one
// GitHub repo-webhook delivery. The body is capped (PR payloads are tens
// of KB), the X-Hub-Signature-256 HMAC is verified against the
// workspace's stored secret, and pull_request opened/closed deliveries
// auto-link / update linked PRs. 200 {"ok":true} on accept — including
// for ignored events (ping, other actions); 401 on a missing or
// non-verifying signature; 400 on a malformed delivery.
func (h *GitHubWebhookHandler) githubWebhook(c *echo.Context) error {
	r := c.Request()
	r.Body = http.MaxBytesReader(c.Response(), r.Body, service.GitHubWebhookMaxBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		// 413 like the importers (importMultipart): the delivery
		// exceeded the cap.
		return WriteError(c, http.StatusRequestEntityTooLarge, ErrCodePayloadTooLarge, "webhook delivery too large", nil)
	}
	event := r.Header.Get("X-GitHub-Event")
	signature := r.Header.Get("X-Hub-Signature-256")
	if err := service.HandleGitHubWebhook(r.Context(), h.Pool, event, signature, body); err != nil {
		switch {
		case errors.Is(err, service.ErrGitHubBadSignature):
			// Same 401 whether the signature is missing, malformed, or
			// simply matches no workspace: never hint at which
			// workspaces have webhooks configured.
			return WriteError(c, http.StatusUnauthorized, ErrCodeUnauthorized, "bad webhook signature", nil)
		case errors.Is(err, service.ErrGitHubBadPayload):
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "malformed webhook delivery", nil)
		default:
			return WriteInternalError(c)
		}
	}
	return c.JSON(http.StatusOK, map[string]any{"ok": true})
}
