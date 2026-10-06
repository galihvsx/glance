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
	g.GET("/:uuid", h.getIssue)
	g.PATCH("/:uuid", h.updateIssue)
	g.DELETE("/:uuid", h.deleteIssue)
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
	default:
		return WriteInternalError(c)
	}
}

type createIssueBody struct {
	Name        string          `json:"name"`
	Description json.RawMessage `json:"description"`
	Priority    *int            `json:"priority"`
	StateID     *string         `json:"state_id"`
	ParentID    *string         `json:"parent_id"`
	SortOrder   *float64        `json:"sort_order"`
	StartDate   *string         `json:"start_date"` // YYYY-MM-DD
	TargetDate  *string         `json:"target_date"`
	IsDraft     bool            `json:"is_draft"`
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
// issue JSON.
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
	iss, err := service.CreateIssue(c.Request().Context(), h.Pool, c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID,
		service.CreateIssueInput{
			Name:        body.Name,
			Description: body.Description,
			Priority:    body.Priority,
			StateID:     body.StateID,
			ParentID:    body.ParentID,
			SortOrder:   body.SortOrder,
			StartDate:   start,
			TargetDate:  target,
			IsDraft:     body.IsDraft,
		})
	if err != nil {
		if errors.Is(err, service.ErrNameRequired) {
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "name is required", nil)
		}
		return issueError(c, err)
	}
	return c.JSON(http.StatusCreated, iss)
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
	Description service.PatchField[json.RawMessage] `json:"description"`
	ParentID    service.PatchField[string]          `json:"parent_id"`
	StartDate   service.PatchField[string]          `json:"start_date"` // YYYY-MM-DD
	TargetDate  service.PatchField[string]          `json:"target_date"`
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

// updateIssue implements PATCH /api/v1/workspaces/{slug}/projects/{identifier}/issues/{uuid}.
// Partial semantics: only provided fields are touched, and only actual
// changes write activity rows. Member (15) or admin (20); the service
// enforces it.
func (h *IssueHandler) updateIssue(c *echo.Context) error {
	var body updateIssueBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	start, err := toDatePatch("start_date", body.StartDate)
	if err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid start_date: want YYYY-MM-DD", nil)
	}
	target, err := toDatePatch("target_date", body.TargetDate)
	if err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid target_date: want YYYY-MM-DD", nil)
	}
	iss, err := service.UpdateIssue(c.Request().Context(), h.Pool, c.Param("slug"), c.Param("identifier"), c.Param("uuid"), CurrentUser(c).ID,
		service.IssuePatch{
			Name:        body.Name,
			Description: body.Description,
			Priority:    body.Priority,
			StateID:     body.StateID,
			ParentID:    body.ParentID,
			SortOrder:   body.SortOrder,
			StartDate:   start,
			TargetDate:  target,
			IsDraft:     body.IsDraft,
		})
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

// strOrEmpty dereferences an optional string body field.
func strOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
