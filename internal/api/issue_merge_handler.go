package api

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// Duplicate merge endpoint (C25T3): POST
// /api/v1/workspaces/{slug}/projects/{identifier}/issues/{uuid}/merge
// {"into_uuid": "<uuid>"} folds the issue into a surviving issue of the
// same project — satellites move, the source closes into a
// cancelled-group state, and a 'duplicate' relation records the
// survivor. The route is registered on the issue group next to /convert
// (static "merge" segment, Echo specificity rules). Member (15)+.
//
// Every route sits behind RequireAuth — workspace membership is the
// tenancy boundary. Errors use the spec §5 envelope via mergeError
// (which falls back to issueError for the shared sentinels).

type mergeIssueBody struct {
	IntoUUID string `json:"into_uuid"`
}

// mergeIssue implements POST
// /api/v1/workspaces/{slug}/projects/{identifier}/issues/{uuid}/merge
// (C25T3). 200 with {"merged": bool, "already_merged": bool,
// "issue": {...}, "into_display_id": "ENG-45"} — already_merged=true
// when the source was already merged into the same target (idempotent,
// no new writes). 400 on self-merge, cross-project target, merge into
// one of the issue's own sub-issues, or a missing into_uuid; 404 on an
// unknown issue or target; 409 when the project has no cancelled state;
// 403 for guests. Member (15)+.
func (h *IssueHandler) mergeIssue(c *echo.Context) error {
	var body mergeIssueBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	uuid, ok := requireUUIDParam(c, "uuid", "issue id")
	if !ok {
		return nil
	}
	res, err := service.MergeIssue(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), uuid, CurrentUser(c).ID, body.IntoUUID)
	if err != nil {
		return mergeError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{
		"merged":          res.Merged,
		"already_merged":  res.AlreadyMerged,
		"issue":           res.Issue,
		"into_display_id": res.IntoDisplayID,
	})
}

// mergeError maps the C25T3 merge sentinels to HTTP statuses, delegating
// the shared sentinels (ErrIssueNotFound, ErrProjectNotFound,
// ErrNotFound, ErrForbidden) to issueError.
func mergeError(c *echo.Context, err error) error {
	switch {
	case errors.Is(err, service.ErrMergeSelf):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "cannot merge an issue into itself", nil)
	case errors.Is(err, service.ErrMergeTargetRequired):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "into_uuid is required", nil)
	case errors.Is(err, service.ErrMergeCrossProject):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "cannot merge into an issue of another project", nil)
	case errors.Is(err, service.ErrMergeDescendant):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "cannot merge into one of the issue's own sub-issues", nil)
	case errors.Is(err, service.ErrMergeNoCancelledState):
		return WriteError(c, http.StatusConflict, ErrCodeConflict, "project has no cancelled state; add one in project settings", nil)
	default:
		return issueError(c, err)
	}
}
