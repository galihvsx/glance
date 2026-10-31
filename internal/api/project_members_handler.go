package api

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// Project members endpoints (C92T0): the direct membership roster
// nested under projects. Routes are registered in
// RegisterProjectRoutes; keep docs/openapi.yaml in lockstep — the
// route-coverage test fails otherwise.
//
// Error mapping reuses projectError (shared sentinels plus the
// project-members sentinels registered there): a private project the
// caller cannot reach is 404 (ErrNotFound -> "workspace not found",
// the shared no-oracle shape); a missing project for a confirmed
// member is 404 "project not found"; management by anyone but an
// effective project Admin is 403.

// listProjectMembers implements GET
// /api/v1/workspaces/{slug}/projects/{identifier}/members: the direct
// roster ordered by user name. Any caller with effective access may
// read; a private project the caller cannot reach is 404, never the
// roster.
func (h *ProjectHandler) listProjectMembers(c *echo.Context) error {
	members, err := service.ListProjectMembers(c.Request().Context(), h.Pool, c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID)
	if err != nil {
		return projectError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"members": members})
}

type addProjectMemberBody struct {
	UserID string `json:"user_id"`
	Role   int    `json:"role"`
}

// addProjectMember implements POST
// /api/v1/workspaces/{slug}/projects/{identifier}/members. Effective
// project Admin (20) only; the service enforces it. 200 with the
// member row — upsert semantics, mirroring the teamspace add: a
// duplicate add is idempotent OK, not a fresh creation, so it is not
// a 201, and it does NOT overwrite the existing role (change-role is
// the update path). A target who is not a workspace member is a 400.
func (h *ProjectHandler) addProjectMember(c *echo.Context) error {
	var body addProjectMemberBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	if !validUUID(body.UserID) {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid user_id", nil)
	}
	member, err := service.AddProjectMember(c.Request().Context(), h.Pool, c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, body.UserID, body.Role)
	if err != nil {
		return projectError(c, err)
	}
	return c.JSON(http.StatusOK, member)
}

type changeProjectMemberRoleBody struct {
	Role int `json:"role"`
}

// changeProjectMemberRole implements PATCH
// /api/v1/workspaces/{slug}/projects/{identifier}/members/{user_id}.
// Effective project Admin (20) only. Demoting the last direct admin
// is a 409; changing a non-member is a 404.
func (h *ProjectHandler) changeProjectMemberRole(c *echo.Context) error {
	userID, ok := requireUUIDParam(c, "user_id", "user_id")
	if !ok {
		return nil
	}
	var body changeProjectMemberRoleBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	member, err := service.ChangeProjectMemberRole(c.Request().Context(), h.Pool, c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, userID, body.Role)
	if err != nil {
		if errors.Is(err, service.ErrProjectTargetNotMember) {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid user_id", nil)
		}
		return projectError(c, err)
	}
	return c.JSON(http.StatusOK, member)
}

// removeProjectMember implements DELETE
// /api/v1/workspaces/{slug}/projects/{identifier}/members/{user_id}.
// Effective project Admin (20) only. Removing a non-member is
// idempotent OK (200); removing the last direct admin is a 409.
func (h *ProjectHandler) removeProjectMember(c *echo.Context) error {
	userID, ok := requireUUIDParam(c, "user_id", "user_id")
	if !ok {
		return nil
	}
	if err := service.RemoveProjectMember(c.Request().Context(), h.Pool, c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, userID); err != nil {
		if errors.Is(err, service.ErrProjectTargetNotMember) {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid user_id", nil)
		}
		return projectError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]bool{"ok": true})
}
