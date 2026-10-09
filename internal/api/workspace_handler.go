package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// WorkspaceHandler serves the workspace endpoints (Task 11): CRUD plus
// member management. Every route sits behind RequireAuth — workspaces are
// the tenancy boundary, so there is no public surface here.
type WorkspaceHandler struct {
	Pool *pgxpool.Pool
}

// RegisterWorkspaceRoutes mounts the workspace endpoints. Call this before
// the SPA catch-all so API routes are never shadowed.
func RegisterWorkspaceRoutes(e *echo.Echo, h *WorkspaceHandler) {
	g := e.Group("/api/v1/workspaces", RequireAuth(h.Pool))
	g.POST("", h.createWorkspace)
	g.GET("", h.listWorkspaces)
	g.GET("/:slug", h.getWorkspace)
	g.PATCH("/:slug", h.updateWorkspace)
	g.DELETE("/:slug", h.deleteWorkspace)
	g.POST("/:slug/members", h.upsertMember)
	g.GET("/:slug/members", h.listMembers)
	g.DELETE("/:slug/members/:user_id", h.removeMember)
	g.POST("/:slug/invites", h.inviteMembers)
	// C9T3: fire a probe message at the workspace's Slack webhook.
	g.POST("/:slug/slack/test", h.testSlack)
}

// workspaceError maps service sentinel errors to HTTP statuses. Unknown
// errors are 500 with no detail leaked.
func workspaceError(c *echo.Context, err error) error {
	switch {
	// Admin-only failure modes: the workspace is confirmed to exist and
	// the caller is a confirmed admin here, so these must not surface as
	// "workspace not found" (that message is reserved for the
	// actor-resolution path: bad slug or non-member caller).
	case errors.Is(err, service.ErrUserNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeUserNotFound, "user not found", nil)
	case errors.Is(err, service.ErrMemberNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeMemberNotFound, "member not found", nil)
	case errors.Is(err, service.ErrNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "workspace not found", nil)
	case errors.Is(err, service.ErrForbidden):
		return WriteError(c, http.StatusForbidden, ErrCodeForbidden, "forbidden", nil)
	case errors.Is(err, service.ErrSlugConflict):
		return WriteError(c, http.StatusConflict, ErrCodeConflict, "slug already taken", nil)
	case errors.Is(err, service.ErrInvalidSlug):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid slug: use lowercase letters, numbers and hyphens", nil)
	case errors.Is(err, service.ErrInvalidRole):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid role: must be 5 (guest), 15 (member) or 20 (admin)", nil)
	case errors.Is(err, service.ErrBadSlackWebhookURL):
		// Never echo the URL back: it is a secret, and the rejection
		// reason is the same for every bad value (no oracle).
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "slack_webhook_url must be an https://hooks.slack.com/ URL", nil)
	case errors.Is(err, service.ErrSlackNotConfigured):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "slack webhook not configured", nil)
	case errors.Is(err, service.ErrLastAdmin):
		return WriteError(c, http.StatusConflict, ErrCodeConflict, "cannot remove or demote the last admin", nil)
	default:
		return WriteInternalError(c)
	}
}

type createWorkspaceBody struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// createWorkspace implements POST /api/v1/workspaces: creates the
// workspace with the caller as admin. 201 with the workspace JSON.
func (h *WorkspaceHandler) createWorkspace(c *echo.Context) error {
	var body createWorkspaceBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	ws, err := service.CreateWorkspace(c.Request().Context(), h.Pool, body.Name, body.Slug, CurrentUser(c).ID)
	if err != nil {
		if errors.Is(err, service.ErrNameRequired) {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "name is required", nil)
		}
		return workspaceError(c, err)
	}
	return c.JSON(http.StatusCreated, ws)
}

// listWorkspaces implements GET /api/v1/workspaces: the caller's
// workspaces with their role in each.
func (h *WorkspaceHandler) listWorkspaces(c *echo.Context) error {
	workspaces, err := service.ListWorkspaces(c.Request().Context(), h.Pool, CurrentUser(c).ID)
	if err != nil {
		return WriteInternalError(c)
	}
	return c.JSON(http.StatusOK, map[string]any{"workspaces": workspaces})
}

// getWorkspace implements GET /api/v1/workspaces/{slug}. Non-members get
// 404 (see service.GetWorkspace) — never a hint the slug exists.
func (h *WorkspaceHandler) getWorkspace(c *echo.Context) error {
	ws, role, err := service.GetWorkspace(c.Request().Context(), h.Pool, c.Param("slug"), CurrentUser(c).ID)
	if err != nil {
		return workspaceError(c, err)
	}
	return c.JSON(http.StatusOK, service.WorkspaceMembership{Workspace: *ws, Role: role})
}

type updateWorkspaceBody struct {
	// Name is a pointer so a Slack-only PATCH (no name key) does not
	// trip the "name is required" validation — the General settings
	// form always sends it, the Slack section never does.
	Name *string `json:"name"`
	Slug *string `json:"slug,omitempty"`
	// SlackWebhookURL uses *json.RawMessage (not **string) to tell
	// explicit null apart from an absent key: encoding/json unmarshals
	// JSON null into a nil pointer at ANY indirection depth, so a
	// double pointer cannot distinguish the two. RawMessage keeps the
	// literal bytes: nil = key absent (untouched), "null" = clear,
	// "string" = set (validated as an https://hooks.slack.com/ URL).
	// The stored URL is a secret and is never returned.
	SlackWebhookURL *json.RawMessage `json:"slack_webhook_url"`
}

// updateWorkspace implements PATCH /api/v1/workspaces/{slug}: rename
// (name, plus optional slug change) and/or set/clear the Slack
// incoming-webhook URL. Admin only (the service enforces it).
func (h *WorkspaceHandler) updateWorkspace(c *echo.Context) error {
	var body updateWorkspaceBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	ctx := c.Request().Context()
	slug := c.Param("slug")
	actorID := CurrentUser(c).ID

	var ws *service.Workspace
	if body.Name != nil || body.Slug != nil {
		name := ""
		if body.Name != nil {
			name = *body.Name
		} else {
			// Slug-only change: keep the current name.
			cur, _, err := service.GetWorkspace(ctx, h.Pool, slug, actorID)
			if err != nil {
				return workspaceError(c, err)
			}
			name = cur.Name
		}
		var err error
		ws, err = service.UpdateWorkspace(ctx, h.Pool, slug, actorID, name, body.Slug)
		if err != nil {
			if errors.Is(err, service.ErrNameRequired) {
				return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "name is required", nil)
			}
			return workspaceError(c, err)
		}
		slug = ws.Slug // a slug change moves subsequent lookups
	}
	if body.SlackWebhookURL != nil {
		var urlStr *string
		raw := strings.TrimSpace(string(*body.SlackWebhookURL))
		if raw != "" && raw != "null" {
			var s string
			if err := json.Unmarshal(*body.SlackWebhookURL, &s); err != nil {
				return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "slack_webhook_url must be a string or null", nil)
			}
			urlStr = &s
		}
		var err error
		ws, err = service.SetWorkspaceSlackURL(ctx, h.Pool, slug, actorID, urlStr)
		if err != nil {
			return workspaceError(c, err)
		}
	}
	if ws == nil {
		// Empty patch: return the current workspace.
		var err error
		ws, _, err = service.GetWorkspace(ctx, h.Pool, slug, actorID)
		if err != nil {
			return workspaceError(c, err)
		}
	}
	return c.JSON(http.StatusOK, ws)
}

// testSlack implements POST /api/v1/workspaces/{slug}/slack/test:
// enqueue a probe message at the workspace's Slack webhook. Admin only.
// 202 on enqueue (delivery itself is async via the outbox); 400 when no
// webhook is configured.
func (h *WorkspaceHandler) testSlack(c *echo.Context) error {
	if err := service.SendSlackTest(c.Request().Context(), h.Pool, c.Param("slug"), CurrentUser(c).ID); err != nil {
		return workspaceError(c, err)
	}
	return c.JSON(http.StatusAccepted, map[string]any{"ok": true})
}

// deleteWorkspace implements DELETE /api/v1/workspaces/{slug}: removes the
// workspace and everything in it (projects/issues/members cascade via FK).
// Admin only. 204 on success; the typed confirmation lives client-side.
func (h *WorkspaceHandler) deleteWorkspace(c *echo.Context) error {
	if err := service.DeleteWorkspace(c.Request().Context(), h.Pool, c.Param("slug"), CurrentUser(c).ID); err != nil {
		return workspaceError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

type upsertMemberBody struct {
	UserID string `json:"user_id"`
	Role   int    `json:"role"`
}

// upsertMember implements POST /api/v1/workspaces/{slug}/members: adds a
// member or changes their role. Admin only.
func (h *WorkspaceHandler) upsertMember(c *echo.Context) error {
	var body upsertMemberBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	if !validUUID(body.UserID) {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid user_id", nil)
	}
	if err := service.UpsertMember(c.Request().Context(), h.Pool, c.Param("slug"), CurrentUser(c).ID, body.UserID, body.Role); err != nil {
		return workspaceError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]bool{"ok": true})
}

// removeMember implements DELETE /api/v1/workspaces/{slug}/members/{user_id}.
// Admin only; removing the last admin is refused.
func (h *WorkspaceHandler) removeMember(c *echo.Context) error {
	userID, ok := requireUUIDParam(c, "user_id", "user_id")
	if !ok {
		return nil
	}
	if err := service.RemoveMember(c.Request().Context(), h.Pool, c.Param("slug"), CurrentUser(c).ID, userID); err != nil {
		return workspaceError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]bool{"ok": true})
}

// listMembers implements GET /api/v1/workspaces/{slug}/members: the
// workspace's members with their user identity. Any member may read — the
// issue page's assignee picker needs it. Wrapped shape: {"members": [...]}.
func (h *WorkspaceHandler) listMembers(c *echo.Context) error {
	members, err := service.ListMembers(c.Request().Context(), h.Pool, c.Param("slug"), CurrentUser(c).ID)
	if err != nil {
		return workspaceError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"members": members})
}

type inviteMembersBody struct {
	Emails []string `json:"emails"`
	Role   int      `json:"role"`
}

// inviteMembers implements POST /api/v1/workspaces/{slug}/invites: adds
// registered users as workspace members by email. Admin only. Every email
// gets a per-email result (invited | already-member | not-registered |
// invalid) — unknown emails are results, not errors, so the onboarding
// wizard keeps them as "Pending" without failing. 200 with the wrapped
// {"results": [...]} shape.
func (h *WorkspaceHandler) inviteMembers(c *echo.Context) error {
	var body inviteMembersBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	results, err := service.InviteMembers(c.Request().Context(), h.Pool, c.Param("slug"), CurrentUser(c).ID, body.Emails, body.Role)
	if err != nil {
		return workspaceError(c, err)
	}
	if results == nil {
		results = []service.InviteResult{}
	}
	return c.JSON(http.StatusOK, map[string]any{"results": results})
}
