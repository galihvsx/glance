package api

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// Release endpoints (C4T6) nested under projects. Every route sits behind
// RequireAuth — workspace membership is the tenancy boundary. Errors use
// the spec §5 envelope via releaseError (which falls back to issueError
// for the shared sentinels).
//
// PATCH tri-state for nullable fields: a missing key leaves the field
// untouched, an explicit "" clears it (NULL), any other value sets it.
// Applies to description and release_date. Name must be non-empty when
// provided; status is a fixed vocabulary (planned|released).

// RegisterReleaseRoutes mounts the release endpoints. Call this before
// the SPA catch-all so API routes are never shadowed.
func RegisterReleaseRoutes(e *echo.Echo, h *IssueHandler) {
	g := e.Group("/api/v1/workspaces/:slug/projects/:identifier/releases", RequireAuth(h.Pool))
	g.POST("", h.createRelease)
	g.GET("", h.listReleases)
	g.GET("/:releaseID", h.getRelease)
	g.PATCH("/:releaseID", h.updateRelease)
	g.DELETE("/:releaseID", h.deleteRelease)
	g.GET("/:releaseID/issues", h.listReleaseIssues)
	g.POST("/:releaseID/issues", h.assignReleaseIssues)
	g.DELETE("/:releaseID/issues", h.unassignReleaseIssues)
}

// releaseError maps release sentinel errors to HTTP statuses, delegating
// the shared sentinels (ErrNotFound, ErrProjectNotFound, ErrIssueNotFound,
// ErrForbidden, ErrNameRequired, ErrNothingToUpdate, ErrBulkEmptyIDs) to
// issueError.
func releaseError(c *echo.Context, err error) error {
	switch {
	case errors.Is(err, service.ErrReleaseNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "release not found", nil)
	case errors.Is(err, service.ErrReleaseConflict):
		return WriteError(c, http.StatusConflict, ErrCodeConflict, "release name already exists", nil)
	case errors.Is(err, service.ErrInvalidRelease):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid release: bad status or date", nil)
	case errors.Is(err, service.ErrInvalidReleaseID):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid id", nil)
	case errors.Is(err, service.ErrReleaseHasIssues):
		return WriteError(c, http.StatusConflict, ErrCodeConflict, "release still has issues: reassign them with ?reassign=<id> or retry with ?force=true", nil)
	default:
		return issueError(c, err)
	}
}

type releaseBody struct {
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Status      string  `json:"status"`
	ReleaseDate *string `json:"release_date"`
}

// createRelease implements POST
// /api/v1/workspaces/{slug}/projects/{identifier}/releases.
func (h *IssueHandler) createRelease(c *echo.Context) error {
	var body releaseBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	release, err := service.CreateRelease(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID,
		service.ReleaseInput{
			Name:        body.Name,
			Description: body.Description,
			Status:      body.Status,
			ReleaseDate: body.ReleaseDate,
		})
	if err != nil {
		if errors.Is(err, service.ErrNameRequired) {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "name is required", nil)
		}
		return releaseError(c, err)
	}
	return c.JSON(http.StatusCreated, release)
}

// listReleases implements GET
// /api/v1/workspaces/{slug}/projects/{identifier}/releases.
func (h *IssueHandler) listReleases(c *echo.Context) error {
	releases, err := service.ListReleases(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID)
	if err != nil {
		return releaseError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"releases": releases})
}

// getRelease implements GET .../releases/{releaseID}.
func (h *IssueHandler) getRelease(c *echo.Context) error {
	releaseID, ok := requireUUIDParam(c, "releaseID", "release id")
	if !ok {
		return nil
	}
	release, err := service.GetRelease(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, releaseID)
	if err != nil {
		return releaseError(c, err)
	}
	return c.JSON(http.StatusOK, release)
}

type releasePatchBody struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	Status      *string `json:"status"`
	ReleaseDate *string `json:"release_date"`
}

// hasAnyReleasePatchKey reports whether the body carries at least one
// known patch key, so PATCH {} is 400 (nothing to update) rather than a
// no-op 200.
func hasAnyReleasePatchKey(body releasePatchBody) bool {
	return body.Name != nil || body.Description != nil ||
		body.Status != nil || body.ReleaseDate != nil
}

// updateRelease implements PATCH .../releases/{releaseID}.
func (h *IssueHandler) updateRelease(c *echo.Context) error {
	releaseID, ok := requireUUIDParam(c, "releaseID", "release id")
	if !ok {
		return nil
	}
	var body releasePatchBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	if !hasAnyReleasePatchKey(body) {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "nothing to update", nil)
	}
	release, err := service.UpdateRelease(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, releaseID,
		service.ReleasePatch{
			Name:        body.Name,
			Description: body.Description,
			Status:      body.Status,
			ReleaseDate: body.ReleaseDate,
		})
	if err != nil {
		return releaseError(c, err)
	}
	return c.JSON(http.StatusOK, release)
}

// deleteRelease implements DELETE .../releases/{releaseID}. A release
// with issues is 409 unless ?reassign=<releaseID> moves the issues or
// ?force=true detaches them.
func (h *IssueHandler) deleteRelease(c *echo.Context) error {
	releaseID, ok := requireUUIDParam(c, "releaseID", "release id")
	if !ok {
		return nil
	}
	var reassign *string
	if r := c.QueryParam("reassign"); r != "" {
		reassign = &r
	}
	force := c.QueryParam("force") == "true"
	if err := service.DeleteRelease(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, releaseID, reassign, force); err != nil {
		return releaseError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// listReleaseIssues implements GET .../releases/{releaseID}/issues: the
// release's live issues as full list items.
func (h *IssueHandler) listReleaseIssues(c *echo.Context) error {
	releaseID, ok := requireUUIDParam(c, "releaseID", "release id")
	if !ok {
		return nil
	}
	issues, err := service.ListReleaseIssues(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, releaseID)
	if err != nil {
		return releaseError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"issues": issues})
}

type releaseIssuesBody struct {
	IssueIDs []string `json:"issue_ids"`
}

// assignReleaseIssues implements POST .../releases/{releaseID}/issues
// {issue_ids}. Idempotent: repeats are no-ops. Member (15)+.
func (h *IssueHandler) assignReleaseIssues(c *echo.Context) error {
	releaseID, ok := requireUUIDParam(c, "releaseID", "release id")
	if !ok {
		return nil
	}
	var body releaseIssuesBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	if len(body.IssueIDs) == 0 {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "issue_ids is required", nil)
	}
	if err := service.AssignReleaseIssues(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, releaseID, body.IssueIDs); err != nil {
		return releaseError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"assigned": len(body.IssueIDs)})
}

// unassignReleaseIssues implements DELETE .../releases/{releaseID}/issues
// {issue_ids}. Idempotent. Member (15)+.
func (h *IssueHandler) unassignReleaseIssues(c *echo.Context) error {
	releaseID, ok := requireUUIDParam(c, "releaseID", "release id")
	if !ok {
		return nil
	}
	var body releaseIssuesBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	if len(body.IssueIDs) == 0 {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "issue_ids is required", nil)
	}
	if err := service.UnassignReleaseIssues(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, releaseID, body.IssueIDs); err != nil {
		return releaseError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"unassigned": len(body.IssueIDs)})
}
