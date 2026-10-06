package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// IssueHandler serves the issue endpoints (Task 14) nested under projects:
// create / read / partial update / soft delete. Every route sits behind
// RequireAuth — workspace membership is the tenancy boundary, so there is
// no public surface here.
type IssueHandler struct {
	Pool *pgxpool.Pool
}

// RegisterIssueRoutes mounts the issue endpoints. Call this before the SPA
// catch-all so API routes are never shadowed.
func RegisterIssueRoutes(e *echo.Echo, h *IssueHandler) {
	g := e.Group("/api/v1/workspaces/:slug/projects/:identifier/issues", RequireAuth(h.Pool))
	g.POST("", h.createIssue)
	g.GET("", h.listIssues)
	g.POST("/bulk-update", h.bulkUpdateIssues)
	g.POST("/bulk-delete", h.bulkDeleteIssues)
	g.GET("/:uuid", h.getIssue)
	g.PATCH("/:uuid", h.updateIssue)
	g.DELETE("/:uuid", h.deleteIssue)
}

// RegisterTaxonomyRoutes mounts the label / assignee / estimate endpoints
// (Task 16) nested under the project path. Labels are workspace-scoped
// rows (spec §4); the project identifier in the route resolves the
// workspace and the caller's membership. Call before the SPA catch-all.
func RegisterTaxonomyRoutes(e *echo.Echo, h *IssueHandler) {
	g := e.Group("/api/v1/workspaces/:slug/projects/:identifier", RequireAuth(h.Pool))
	g.POST("/labels", h.createLabel)
	g.GET("/labels", h.listLabels)
	g.GET("/labels/:labelID", h.getLabel)
	g.PATCH("/labels/:labelID", h.updateLabel)
	g.DELETE("/labels/:labelID", h.deleteLabel)
	g.POST("/estimates", h.createEstimate)
	g.GET("/estimates", h.listEstimates)
	g.POST("/issues/:uuid/labels/:labelID", h.assignLabel)
	g.DELETE("/issues/:uuid/labels/:labelID", h.unassignLabel)
	g.POST("/issues/:uuid/assignees/:userID", h.assignAssignee)
	g.DELETE("/issues/:uuid/assignees/:userID", h.unassignAssignee)
}

// issueError maps service sentinel errors to HTTP statuses. Unknown errors
// are 500 with no detail leaked.
func issueError(c *echo.Context, err error) error {
	switch {
	// The project is confirmed to exist and the caller is a confirmed
	// member on this path, so a missing issue is its own 404 — never
	// "workspace not found" (reserved for the actor-resolution path: bad
	// slug or non-member caller).
	case errors.Is(err, service.ErrIssueNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "issue not found", nil)
	case errors.Is(err, service.ErrProjectNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "project not found", nil)
	case errors.Is(err, service.ErrNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "workspace not found", nil)
	case errors.Is(err, service.ErrForbidden):
		return WriteError(c, http.StatusForbidden, ErrCodeForbidden, "forbidden", nil)
	case errors.Is(err, service.ErrInvalidState):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid state_id: state does not exist or belongs to another project", nil)
	case errors.Is(err, service.ErrInvalidPriority):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid priority: must be 0-4", nil)
	case errors.Is(err, service.ErrParentNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "parent issue not found", nil)
	case errors.Is(err, service.ErrInvalidParent):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid parent_id", nil)
	case errors.Is(err, service.ErrInvalidDateRange):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "start_date must not be after target_date", nil)
	case errors.Is(err, service.ErrInvalidIdentifier):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid identifier: 1-12 letters and digits", nil)
	case errors.Is(err, service.ErrNameRequired):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "name is required", nil)
	case errors.Is(err, service.ErrNothingToUpdate):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "nothing to update", nil)
	// Task 17: bulk errors.
	case errors.Is(err, service.ErrBulkEmptyIDs):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "ids is required", nil)
	case errors.Is(err, service.ErrBulkTooManyIDs):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "too many ids: max 100", nil)
	// Task 16: taxonomy errors.
	case errors.Is(err, service.ErrLabelNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "label not found", nil)
	case errors.Is(err, service.ErrLabelConflict):
		return WriteError(c, http.StatusConflict, ErrCodeConflict, "label name already exists", nil)
	case errors.Is(err, service.ErrLabelCycle):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "label parent would create a cycle", nil)
	case errors.Is(err, service.ErrInvalidLabel):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid label", nil)
	case errors.Is(err, service.ErrEstimateNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "estimate not found", nil)
	case errors.Is(err, service.ErrEstimateConflict):
		return WriteError(c, http.StatusConflict, ErrCodeConflict, "estimate name already exists", nil)
	case errors.Is(err, service.ErrInvalidEstimatePoint):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid estimate point", nil)
	case errors.Is(err, service.ErrAssigneeNotMember):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "assignee is not a workspace member", nil)
	default:
		return WriteInternalError(c)
	}
}

type createIssueBody struct {
	Name            string          `json:"name"`
	Description     json.RawMessage `json:"description"`
	Priority        *int            `json:"priority"`
	StateID         *string         `json:"state_id"`
	ParentID        *string         `json:"parent_id"`
	SortOrder       *float64        `json:"sort_order"`
	StartDate       *string         `json:"start_date"` // YYYY-MM-DD
	TargetDate      *string         `json:"target_date"`
	EstimatePointID *string         `json:"estimate_point_id"`
	IsDraft         bool            `json:"is_draft"`
}

// parseDateBody parses an optional YYYY-MM-DD body field into a *time.Time.
// Empty means "not provided"; a malformed value is a 400.
func parseDateBody(field, v string) (*time.Time, error) {
	if v == "" {
		return nil, nil
	}
	t, err := time.Parse("2006-01-02", v)
	if err != nil {
		return nil, err
	}
	utc := t.UTC()
	return &utc, nil
}

// createIssue implements POST /api/v1/workspaces/{slug}/projects/{identifier}/issues:
// creates the issue with the next atomic sequence_id (display ID
// {IDENTIFIER}-{sequence_id}) and a _created activity row. 201 with the
// issue JSON. Honors Idempotency-Key: a replayed key returns the stored
// response byte-identical without creating a second issue.
func (h *IssueHandler) createIssue(c *echo.Context) error {
	var body createIssueBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	start, err := parseDateBody("start_date", strOrEmpty(body.StartDate))
	if err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid start_date: want YYYY-MM-DD", nil)
	}
	target, err := parseDateBody("target_date", strOrEmpty(body.TargetDate))
	if err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid target_date: want YYYY-MM-DD", nil)
	}
	return withIdempotency(c, h.Pool, "POST /issues", issueError, func() (int, any, error) {
		iss, err := service.CreateIssue(c.Request().Context(), h.Pool, c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID,
			service.CreateIssueInput{
				Name:            body.Name,
				Description:     body.Description,
				Priority:        body.Priority,
				StateID:         body.StateID,
				ParentID:        body.ParentID,
				SortOrder:       body.SortOrder,
				StartDate:       start,
				TargetDate:      target,
				EstimatePointID: body.EstimatePointID,
				IsDraft:         body.IsDraft,
			})
		if err != nil {
			return 0, nil, err
		}
		return http.StatusCreated, iss, nil
	})
}

// listIssues implements GET /api/v1/workspaces/{slug}/projects/{identifier}/issues:
// filters (?state=&assignee=&label=&priority=&cycle=&q=), ordering
// (?order_by=-updated_at), cursor pagination (?cursor=&per_page=), delta
// sync (?updated_after=), and sparse fieldsets (?fields=description).
// 200 with the {results, next_cursor} envelope (spec §5).
func (h *IssueHandler) listIssues(c *echo.Context) error {
	qp := c.QueryParams()

	var priority *int
	if s := qp.Get("priority"); s != "" {
		p, err := strconv.Atoi(s)
		if err != nil {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid priority: want 0-4", nil)
		}
		priority = &p
	}
	perPage := 25
	if s := qp.Get("per_page"); s != "" {
		p, err := strconv.Atoi(s)
		if err != nil || p < 1 {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid per_page: want 1-100", nil)
		}
		perPage = p
	}
	var updatedAfter *time.Time
	if s := qp.Get("updated_after"); s != "" {
		tm, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid updated_after: want RFC3339", nil)
		}
		updatedAfter = &tm
	}
	var fields []string
	if s := qp.Get("fields"); s != "" {
		fields = strings.Split(s, ",")
	}

	res, err := service.ListIssues(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID,
		service.ListIssuesInput{
			State:        qp.Get("state"),
			Assignee:     qp.Get("assignee"),
			Label:        qp.Get("label"),
			Priority:     priority,
			Cycle:        qp.Get("cycle"),
			Q:            qp.Get("q"),
			OrderBy:      qp.Get("order_by"),
			Cursor:       qp.Get("cursor"),
			PerPage:      perPage,
			UpdatedAfter: updatedAfter,
			Fields:       fields,
		})
	if err != nil {
		switch {
		case errors.Is(err, service.ErrInvalidOrderBy):
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid order_by", nil)
		case errors.Is(err, service.ErrInvalidCursor):
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid cursor", nil)
		case errors.Is(err, service.ErrInvalidListFilter):
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid filter", nil)
		}
		return issueError(c, err)
	}
	return c.JSON(http.StatusOK, res)
}

// getIssue implements GET /api/v1/workspaces/{slug}/projects/{identifier}/issues/{uuid}.
func (h *IssueHandler) getIssue(c *echo.Context) error {
	iss, err := service.GetIssue(c.Request().Context(), h.Pool, c.Param("slug"), c.Param("identifier"), c.Param("uuid"), CurrentUser(c).ID)
	if err != nil {
		return issueError(c, err)
	}
	return c.JSON(http.StatusOK, iss)
}

type updateIssueBody struct {
	// Plain pointers (Task 12 pattern): nil = omitted, leave untouched.
	Name      *string  `json:"name"`
	Priority  *int     `json:"priority"`
	StateID   *string  `json:"state_id"`
	SortOrder *float64 `json:"sort_order"`
	IsDraft   *bool    `json:"is_draft"`
	// Tri-state fields: Set=false means omitted; Set with nil Value clears
	// the column; Set with a Value assigns it.
	Description     service.PatchField[json.RawMessage] `json:"description"`
	ParentID        service.PatchField[string]          `json:"parent_id"`
	StartDate       service.PatchField[string]          `json:"start_date"` // YYYY-MM-DD
	TargetDate      service.PatchField[string]          `json:"target_date"`
	EstimatePointID service.PatchField[string]          `json:"estimate_point_id"`
}

// toDatePatch converts a tri-state YYYY-MM-DD string patch field into a
// tri-state time.Time patch field, preserving the Set flag.
func toDatePatch(field string, p service.PatchField[string]) (service.PatchField[time.Time], error) {
	if !p.Set {
		return service.PatchField[time.Time]{}, nil
	}
	if p.Value == nil {
		return service.PatchField[time.Time]{Set: true}, nil
	}
	t, err := parseDateBody(field, *p.Value)
	if err != nil {
		return service.PatchField[time.Time]{}, err
	}
	return service.PatchField[time.Time]{Set: true, Value: t}, nil
}

// patchFieldError is a date-parse failure on a PATCH body field.
type patchFieldError struct{ field string }

func (e *patchFieldError) Error() string { return "invalid " + e.field + ": want YYYY-MM-DD" }

// bindIssuePatch converts an updateIssueBody into a service.IssuePatch,
// preserving the tri-state semantics (nil = omitted, explicit null =
// clear). A malformed date yields a *patchFieldError naming the field.
func bindIssuePatch(body updateIssueBody) (service.IssuePatch, error) {
	start, err := toDatePatch("start_date", body.StartDate)
	if err != nil {
		return service.IssuePatch{}, &patchFieldError{field: "start_date"}
	}
	target, err := toDatePatch("target_date", body.TargetDate)
	if err != nil {
		return service.IssuePatch{}, &patchFieldError{field: "target_date"}
	}
	return service.IssuePatch{
		Name:            body.Name,
		Description:     body.Description,
		Priority:        body.Priority,
		StateID:         body.StateID,
		ParentID:        body.ParentID,
		SortOrder:       body.SortOrder,
		StartDate:       start,
		TargetDate:      target,
		EstimatePointID: body.EstimatePointID,
		IsDraft:         body.IsDraft,
	}, nil
}

// writePatchFieldError answers a *patchFieldError with the 400 envelope.
func writePatchFieldError(c *echo.Context, err error) error {
	var pfe *patchFieldError
	if errors.As(err, &pfe) {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid "+pfe.field+": want YYYY-MM-DD", nil)
	}
	return WriteInternalError(c)
}

// updateIssue implements PATCH /api/v1/workspaces/{slug}/projects/{identifier}/issues/{uuid}.
// Partial semantics: only provided fields are touched, and only actual
// changes write activity rows. Member (15) or admin (20); the service
// enforces it.
func (h *IssueHandler) updateIssue(c *echo.Context) error {
	var body updateIssueBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	patch, err := bindIssuePatch(body)
	if err != nil {
		return writePatchFieldError(c, err)
	}
	iss, err := service.UpdateIssue(c.Request().Context(), h.Pool, c.Param("slug"), c.Param("identifier"), c.Param("uuid"), CurrentUser(c).ID, patch)
	if err != nil {
		if errors.Is(err, service.ErrNameRequired) {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "name is required", nil)
		}
		if errors.Is(err, service.ErrNothingToUpdate) {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "nothing to update", nil)
		}
		return issueError(c, err)
	}
	return c.JSON(http.StatusOK, iss)
}

// deleteIssue implements DELETE /api/v1/workspaces/{slug}/projects/{identifier}/issues/{uuid}:
// soft delete (deleted_at) plus a _deleted activity row. 204.
func (h *IssueHandler) deleteIssue(c *echo.Context) error {
	if err := service.DeleteIssue(c.Request().Context(), h.Pool, c.Param("slug"), c.Param("identifier"), c.Param("uuid"), CurrentUser(c).ID); err != nil {
		return issueError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// ---------- Task 17: bulk operations ----------

type bulkUpdateBody struct {
	IDs   []string        `json:"ids"`
	Patch updateIssueBody `json:"patch"`
}

type bulkDeleteBody struct {
	IDs []string `json:"ids"`
}

// bulkUpdateIssues implements POST /api/v1/workspaces/{slug}/projects/{identifier}/issues/bulk-update:
// applies one tri-state patch to many issues inside a single transaction
// (per-item savepoints → partial success). 200 with
// {results:[{id, ok, error?}]}. Member (15)+; the service enforces it.
// Honors Idempotency-Key.
func (h *IssueHandler) bulkUpdateIssues(c *echo.Context) error {
	var body bulkUpdateBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	patch, err := bindIssuePatch(body.Patch)
	if err != nil {
		return writePatchFieldError(c, err)
	}
	return withIdempotency(c, h.Pool, "POST /issues/bulk-update", issueError, func() (int, any, error) {
		results, err := service.BulkUpdateIssues(c.Request().Context(), h.Pool,
			c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, body.IDs, patch)
		if err != nil {
			return 0, nil, err
		}
		return http.StatusOK, map[string]any{"results": results}, nil
	})
}

// bulkDeleteIssues implements POST /api/v1/workspaces/{slug}/projects/{identifier}/issues/bulk-delete:
// soft-deletes many issues with the same per-item savepoint semantics as
// bulk-update. 200 with {results:[{id, ok, error?}]}. Member (15)+.
// Honors Idempotency-Key.
func (h *IssueHandler) bulkDeleteIssues(c *echo.Context) error {
	var body bulkDeleteBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	return withIdempotency(c, h.Pool, "POST /issues/bulk-delete", issueError, func() (int, any, error) {
		results, err := service.BulkDeleteIssues(c.Request().Context(), h.Pool,
			c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, body.IDs)
		if err != nil {
			return 0, nil, err
		}
		return http.StatusOK, map[string]any{"results": results}, nil
	})
}

// strOrEmpty dereferences an optional string body field.
func strOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ---------- Task 16: labels ----------

type createLabelBody struct {
	Name     string  `json:"name"`
	Color    *string `json:"color"`
	ParentID *string `json:"parent_id"`
}

type updateLabelBody struct {
	Name  *string `json:"name"`
	Color *string `json:"color"`
	// Tri-state: unset leaves the parent untouched, null clears it,
	// a value reparents (cycle-checked).
	ParentID service.PatchField[string] `json:"parent_id"`
}

// createLabel implements POST /api/v1/workspaces/{slug}/projects/{identifier}/labels.
func (h *IssueHandler) createLabel(c *echo.Context) error {
	var body createLabelBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	l, err := service.CreateLabel(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID,
		service.LabelInput{Name: body.Name, Color: body.Color, ParentID: body.ParentID})
	if err != nil {
		if errors.Is(err, service.ErrNameRequired) {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "name is required", nil)
		}
		return issueError(c, err)
	}
	return c.JSON(http.StatusCreated, l)
}

// listLabels implements GET /api/v1/workspaces/{slug}/projects/{identifier}/labels.
func (h *IssueHandler) listLabels(c *echo.Context) error {
	labels, err := service.ListLabels(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID)
	if err != nil {
		return issueError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"labels": labels})
}

// getLabel implements GET /api/v1/workspaces/{slug}/projects/{identifier}/labels/{labelID}.
func (h *IssueHandler) getLabel(c *echo.Context) error {
	l, err := service.GetLabel(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), c.Param("labelID"), CurrentUser(c).ID)
	if err != nil {
		return issueError(c, err)
	}
	return c.JSON(http.StatusOK, l)
}

// updateLabel implements PATCH /api/v1/workspaces/{slug}/projects/{identifier}/labels/{labelID}.
func (h *IssueHandler) updateLabel(c *echo.Context) error {
	var body updateLabelBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	l, err := service.UpdateLabel(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), c.Param("labelID"), CurrentUser(c).ID,
		service.LabelPatch{Name: body.Name, Color: body.Color, ParentID: body.ParentID})
	if err != nil {
		if errors.Is(err, service.ErrNameRequired) {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "name is required", nil)
		}
		if errors.Is(err, service.ErrNothingToUpdate) {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "nothing to update", nil)
		}
		return issueError(c, err)
	}
	return c.JSON(http.StatusOK, l)
}

// deleteLabel implements DELETE /api/v1/workspaces/{slug}/projects/{identifier}/labels/{labelID}.
func (h *IssueHandler) deleteLabel(c *echo.Context) error {
	if err := service.DeleteLabel(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), c.Param("labelID"), CurrentUser(c).ID); err != nil {
		return issueError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// assignLabel implements POST /api/v1/workspaces/{slug}/projects/{identifier}/issues/{uuid}/labels/{labelID}.
// Idempotent: assigning twice succeeds with no duplicate.
func (h *IssueHandler) assignLabel(c *echo.Context) error {
	if err := service.AssignLabel(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), c.Param("uuid"), c.Param("labelID"), CurrentUser(c).ID); err != nil {
		return issueError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// unassignLabel implements DELETE .../issues/{uuid}/labels/{labelID}.
// Idempotent: unassigning an absent label succeeds silently.
func (h *IssueHandler) unassignLabel(c *echo.Context) error {
	if err := service.UnassignLabel(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), c.Param("uuid"), c.Param("labelID"), CurrentUser(c).ID); err != nil {
		return issueError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// assignAssignee implements POST .../issues/{uuid}/assignees/{userID}.
// Idempotent; the assignee must be a workspace member.
func (h *IssueHandler) assignAssignee(c *echo.Context) error {
	if err := service.AssignAssignee(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), c.Param("uuid"), c.Param("userID"), CurrentUser(c).ID); err != nil {
		return issueError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// unassignAssignee implements DELETE .../issues/{uuid}/assignees/{userID}.
// Idempotent.
func (h *IssueHandler) unassignAssignee(c *echo.Context) error {
	if err := service.UnassignAssignee(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), c.Param("uuid"), c.Param("userID"), CurrentUser(c).ID); err != nil {
		return issueError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// ---------- Task 16: estimates ----------

type estimatePointBody struct {
	Key         string  `json:"key"`
	Value       int     `json:"value"`
	Description *string `json:"description"`
}

type createEstimateBody struct {
	Name   string              `json:"name"`
	Points []estimatePointBody `json:"points"`
}

// createEstimate implements POST /api/v1/workspaces/{slug}/projects/{identifier}/estimates:
// creates a scale with its points in one call.
func (h *IssueHandler) createEstimate(c *echo.Context) error {
	var body createEstimateBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	points := make([]service.EstimatePointInput, 0, len(body.Points))
	for _, p := range body.Points {
		points = append(points, service.EstimatePointInput{
			Key: p.Key, Value: p.Value, Description: p.Description,
		})
	}
	est, err := service.CreateEstimate(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID,
		service.EstimateInput{Name: body.Name, Points: points})
	if err != nil {
		if errors.Is(err, service.ErrNameRequired) {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "name is required", nil)
		}
		return issueError(c, err)
	}
	return c.JSON(http.StatusCreated, est)
}

// listEstimates implements GET /api/v1/workspaces/{slug}/projects/{identifier}/estimates.
func (h *IssueHandler) listEstimates(c *echo.Context) error {
	estimates, err := service.ListEstimates(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID)
	if err != nil {
		return issueError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"estimates": estimates})
}
