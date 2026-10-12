package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// RegisterIntakeRoutes mounts the intake inbox + triage endpoints
// (Task 20) nested under the project path. Call before the SPA
// catch-all so API routes are never shadowed.
func RegisterIntakeRoutes(e *echo.Echo, h *IssueHandler) {
	g := e.Group("/api/v1/workspaces/:slug/projects/:identifier/intake", RequireAuth(h.Pool))
	g.GET("", h.getIntake)
	g.POST("/bulk-triage", h.bulkTriageIntakeIssues)
	g.POST("/issues/:uuid/accept", h.acceptIntakeIssue)
	g.POST("/issues/:uuid/reject", h.rejectIntakeIssue)
	g.POST("/issues/:uuid/snooze", h.snoozeIntakeIssue)
	g.POST("/issues/:uuid/duplicate", h.duplicateIntakeIssue)
}

// intakeError maps the intake service sentinels to HTTP statuses. It is
// consulted before issueError's default branch — unknown errors stay 500
// with no detail leaked. Returns true when it handled err.
func intakeError(c *echo.Context, err error) (bool, error) {
	switch {
	case errors.Is(err, service.ErrIntakeNotFound):
		return true, WriteError(c, http.StatusNotFound, ErrCodeNotFound, "intake issue not found", nil)
	case errors.Is(err, service.ErrIntakeAlreadyTriaged):
		return true, WriteError(c, http.StatusConflict, ErrCodeConflict, "intake issue already triaged", nil)
	case errors.Is(err, service.ErrSnoozeDatePast):
		return true, WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "snoozed_till must be in the future", nil)
	case errors.Is(err, service.ErrDuplicateTargetInvalid):
		return true, WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid duplicate_to_id: target must be a live issue in this project", nil)
	case errors.Is(err, service.ErrInvalidTriageAction):
		return true, WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, `action must be "accept" or "reject"`, nil)
	case errors.Is(err, service.ErrBulkEmptyIDs):
		return true, WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "intake_ids is required", nil)
	case errors.Is(err, service.ErrBulkTooManyIDs):
		return true, WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "too many intake_ids: max 100", nil)
	}
	return false, nil
}

// getIntake implements GET /api/v1/workspaces/{slug}/projects/{identifier}/intake:
// the triage inbox — pending items plus snoozed items whose snoozed_till
// has passed (they read back with effective status pending; see the
// service package for the surfacing rule). ?per_page= clamps to 1..100.
func (h *IssueHandler) getIntake(c *echo.Context) error {
	perPage := 50
	if raw := c.QueryParam("per_page"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 100 {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid per_page: want 1-100", nil)
		}
		perPage = n
	}
	in, err := service.GetIntake(c.Request().Context(), h.Pool, c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID)
	if err != nil {
		if handled, out := intakeError(c, err); handled {
			return out
		}
		return issueError(c, err)
	}
	items, err := service.ListIntakeIssues(c.Request().Context(), h.Pool, c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, perPage)
	if err != nil {
		if handled, out := intakeError(c, err); handled {
			return out
		}
		return issueError(c, err)
	}
	snoozed, err := service.ListSnoozedIntakeIssues(c.Request().Context(), h.Pool, c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, perPage)
	if err != nil {
		if handled, out := intakeError(c, err); handled {
			return out
		}
		return issueError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"intake": in, "items": items, "snoozed": snoozed})
}

// acceptIntakeIssue implements POST .../intake/issues/{uuid}/accept.
func (h *IssueHandler) acceptIntakeIssue(c *echo.Context) error {
	uuid, ok := requireUUIDParam(c, "uuid", "issue id")
	if !ok {
		return nil
	}
	ii, err := service.AcceptIntakeIssue(c.Request().Context(), h.Pool, c.Param("slug"), c.Param("identifier"), uuid, CurrentUser(c).ID)
	if err != nil {
		if handled, out := intakeError(c, err); handled {
			return out
		}
		return issueError(c, err)
	}
	return c.JSON(http.StatusOK, ii)
}

// rejectIntakeIssue implements POST .../intake/issues/{uuid}/reject.
func (h *IssueHandler) rejectIntakeIssue(c *echo.Context) error {
	uuid, ok := requireUUIDParam(c, "uuid", "issue id")
	if !ok {
		return nil
	}
	ii, err := service.RejectIntakeIssue(c.Request().Context(), h.Pool, c.Param("slug"), c.Param("identifier"), uuid, CurrentUser(c).ID)
	if err != nil {
		if handled, out := intakeError(c, err); handled {
			return out
		}
		return issueError(c, err)
	}
	return c.JSON(http.StatusOK, ii)
}

type snoozeIntakeBody struct {
	SnoozedTill *string `json:"snoozed_till"` // RFC3339, must be in the future
}

// snoozeIntakeIssue implements POST .../intake/issues/{uuid}/snooze.
func (h *IssueHandler) snoozeIntakeIssue(c *echo.Context) error {
	var body snoozeIntakeBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	if body.SnoozedTill == nil || *body.SnoozedTill == "" {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "snoozed_till is required", nil)
	}
	till, err := time.Parse(time.RFC3339, *body.SnoozedTill)
	if err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid snoozed_till: want RFC3339", nil)
	}
	uuid, ok := requireUUIDParam(c, "uuid", "issue id")
	if !ok {
		return nil
	}
	ii, err := service.SnoozeIntakeIssue(c.Request().Context(), h.Pool, c.Param("slug"), c.Param("identifier"), uuid, CurrentUser(c).ID, till)
	if err != nil {
		if handled, out := intakeError(c, err); handled {
			return out
		}
		return issueError(c, err)
	}
	return c.JSON(http.StatusOK, ii)
}

type duplicateIntakeBody struct {
	DuplicateToID *string `json:"duplicate_to_id"`
}

type bulkTriageIntakeBody struct {
	IntakeIDs  []string `json:"intake_ids"`
	Action     string   `json:"action"`
	AssigneeID *string  `json:"assignee_id"`
}

// bulkTriageIntakeIssues implements POST
// /api/v1/workspaces/{slug}/projects/{identifier}/intake/bulk-triage:
// triages many intake rows in one call — {intake_ids, action:
// "accept"|"reject", assignee_id?} — reusing the per-item accept/reject
// service logic with one transaction per item, so a failing item never
// blocks the rest. 200 with
// {accepted, rejected, skipped, errors:[{intake_id, error}]}; the four
// buckets partition the request ids. Member (15)+; 400 on empty
// intake_ids, a bad action, or a non-member assignee. assignee_id is
// applied on accept only (ignored for reject, where the issue is
// soft-deleted).
func (h *IssueHandler) bulkTriageIntakeIssues(c *echo.Context) error {
	var body bulkTriageIntakeBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	res, err := service.BulkTriageIntake(c.Request().Context(), h.Pool, c.Param("slug"), c.Param("identifier"),
		body.IntakeIDs, body.Action, body.AssigneeID, CurrentUser(c).ID)
	if err != nil {
		if handled, out := intakeError(c, err); handled {
			return out
		}
		return issueError(c, err)
	}
	return c.JSON(http.StatusOK, res)
}

// duplicateIntakeIssue implements POST .../intake/issues/{uuid}/duplicate.
func (h *IssueHandler) duplicateIntakeIssue(c *echo.Context) error {
	var body duplicateIntakeBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	if body.DuplicateToID == nil || *body.DuplicateToID == "" {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "duplicate_to_id is required", nil)
	}
	uuid, ok := requireUUIDParam(c, "uuid", "issue id")
	if !ok {
		return nil
	}
	ii, err := service.DuplicateIntakeIssue(c.Request().Context(), h.Pool, c.Param("slug"), c.Param("identifier"), uuid, CurrentUser(c).ID, *body.DuplicateToID)
	if err != nil {
		if handled, out := intakeError(c, err); handled {
			return out
		}
		return issueError(c, err)
	}
	return c.JSON(http.StatusOK, ii)
}
