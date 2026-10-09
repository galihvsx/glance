package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"glance/internal/service"
	"glance/internal/store"
)

// IssueHandler serves the issue endpoints (Task 14) nested under projects:
// create / read / partial update / soft delete. Every route sits behind
// RequireAuth — workspace membership is the tenancy boundary, so there is
// no public surface here.
type IssueHandler struct {
	Pool *pgxpool.Pool
	// Attachments is the file store for issue attachments (C4T2), rooted
	// at <data-dir>/attachments. Nil means unconfigured: attachment
	// routes fail closed (500) instead of writing nowhere.
	Attachments store.AttachmentStore
	// MaxUploadBytes caps a single attachment upload (GLANCE_MAX_UPLOAD_MB).
	MaxUploadBytes int64
}

// RegisterIssueRoutes mounts the issue endpoints. Call this before the SPA
// catch-all so API routes are never shadowed.
func RegisterIssueRoutes(e *echo.Echo, h *IssueHandler) {
	g := e.Group("/api/v1/workspaces/:slug/projects/:identifier/issues", RequireAuth(h.Pool))
	g.POST("", h.createIssue)
	g.GET("", h.listIssues)
	// C8T3: static segment wins over GET /:uuid by Echo's specificity
	// rules (kept adjacent for readability).
	g.GET("/export", h.exportIssues)
	g.POST("/bulk-update", h.bulkUpdateIssues)
	g.POST("/bulk-delete", h.bulkDeleteIssues)
	// C5T8: atomic bulk set. Static segment wins over PATCH /:uuid by
	// Echo's specificity rules (kept adjacent for readability).
	g.PATCH("/bulk", h.bulkSetIssues)
	g.POST("/rebalance", h.rebalanceIssues)
	g.GET("/:uuid", h.getIssue)
	g.PATCH("/:uuid", h.updateIssue)
	g.DELETE("/:uuid", h.deleteIssue)
	// C8T4: clone endpoint. The static "clone" segment keeps this distinct
	// from PATCH /:uuid by Echo's specificity rules (kept adjacent for
	// readability, same style as /export and /bulk).
	g.POST("/:uuid/clone", h.cloneIssue)
	// C5T4: sub-issue parent endpoints on the same group.
	registerIssueParentRoutes(g, h)
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
	g.DELETE("/estimates/:estimateID", h.deleteEstimate)
	g.POST("/estimates/:estimateID/points", h.addEstimatePoints)
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
	// C5T4: sub-issue parent guards (SetParent routes and the PATCH
	// parent_id cycle backstop).
	case errors.Is(err, service.ErrIssueSelfParent):
		return WriteError(c, http.StatusConflict, ErrCodeConflict, "issue cannot be its own parent", nil)
	case errors.Is(err, service.ErrIssueCrossProjectParent):
		return WriteError(c, http.StatusConflict, ErrCodeConflict, "parent issue is in another project", nil)
	case errors.Is(err, service.ErrIssueCyclicParent):
		return WriteError(c, http.StatusConflict, ErrCodeConflict, "parent assignment would create a cycle", nil)
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
	case errors.Is(err, service.ErrEstimateInUse):
		return WriteError(c, http.StatusConflict, ErrCodeConflict, "estimate scale is used by issues", nil)
	case errors.Is(err, service.ErrAssigneeNotMember):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "assignee is not a workspace member", nil)
	// Task 18: satellite errors.
	case errors.Is(err, service.ErrCommentNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "comment not found", nil)
	case errors.Is(err, service.ErrInvalidComment):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid comment", nil)
	case errors.Is(err, service.ErrInvalidReaction):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid reaction", nil)
	case errors.Is(err, service.ErrInvalidRelationType):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid relation type", nil)
	case errors.Is(err, service.ErrInvalidRelation):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid relation", nil)
	case errors.Is(err, service.ErrRelationNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "relation not found", nil)
	case errors.Is(err, service.ErrVersionNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "version not found", nil)
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
	// Intake opts the new issue into the project's intake inbox as
	// pending (Task 20). Default false: direct-to-backlog.
	Intake bool `json:"intake"`
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
				Intake:          body.Intake,
			})
		if err != nil {
			return 0, nil, err
		}
		return http.StatusCreated, iss, nil
	})
}

// listIssues implements GET /api/v1/workspaces/{slug}/projects/{identifier}/issues:
// filters (?state=&assignee=&label=&priority=&estimate=&cycle=&q=,
// ?created_after/before=&updated_after/before=&due_after/before=,
// ?start_after/before= (C3T5: bracket issues.start_date for the calendar),
// ?undated=1 (C3T5: issues with neither target nor start date),
// ?subscribed=true), ordering (?order_by=-updated_at), cursor pagination
// (?cursor=&per_page=), delta sync (?updated_after=), and sparse fieldsets
// (?fields=description). Multi-value filters take comma-separated lists
// (single values keep working); assignee=/estimate= accept "none" for
// unassigned / unestimated issues. 200 with the {results, next_cursor}
// envelope (spec §5). ?include_links=true (C4T0) attaches the issue
// dependency edges for the listed issues as a top-level "links" array,
// fetched in ONE query — the Gantt view pairs it with
// ?start_after/?start_before=.
// parseIssueListInput parses the query params shared by the issue list
// and the export endpoint (C8T3), so both honor identical filter
// semantics. Pagination/sort params (per_page, cursor, order_by,
// fields, include_links) are parsed for the list; exporters ignore them.
// A non-nil error is always a 400-class client error with a
// human-readable message.
func parseIssueListInput(qp url.Values) (service.ListIssuesInput, error) {

	var priorities []int
	if s := qp.Get("priority"); s != "" {
		for _, part := range strings.Split(s, ",") {
			p, err := strconv.Atoi(strings.TrimSpace(part))
			if err != nil {
				return service.ListIssuesInput{}, errors.New("invalid priority: want comma-separated 0-4")
			}
			priorities = append(priorities, p)
		}
	}
	// parseIDList splits a comma-separated param. The "none" sentinel is
	// only meaningful for assignee=/estimate=; elsewhere it passes through
	// and the service rejects it as a malformed UUID (400).
	parseIDList := func(name string) []string {
		s := qp.Get(name)
		if s == "" {
			return nil
		}
		var out []string
		for _, part := range strings.Split(s, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			out = append(out, part)
		}
		return out
	}
	labels := parseIDList("label")
	assignees := parseIDList("assignee")
	estimatePoints := parseIDList("estimate")
	// C2T8: per_page is REJECTED (400) when out of range, on every list
	// endpoint — never silently clamped. Rationale: the documented
	// contract is "want 1-100"; fail-fast surfaces client bugs instead of
	// returning fewer rows than asked for. (The service layer still
	// clamps as defense-in-depth for direct internal callers.)
	perPage := 25
	if s := qp.Get("per_page"); s != "" {
		p, err := strconv.Atoi(s)
		if err != nil || p < 1 || p > 100 {
			return service.ListIssuesInput{}, errors.New("invalid per_page: want 1-100")
		}
		perPage = p
	}
	parseTime := func(name string) (*time.Time, error) {
		s := qp.Get(name)
		if s == "" {
			return nil, nil
		}
		tm, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return nil, err
		}
		return &tm, nil
	}
	var createdAfter, createdBefore, updatedAfter, updatedBefore, dueAfter, dueBefore, startAfter, startBefore *time.Time
	for _, tc := range []struct {
		name string
		dst  **time.Time
	}{
		{"created_after", &createdAfter},
		{"created_before", &createdBefore},
		{"updated_after", &updatedAfter},
		{"updated_before", &updatedBefore},
		{"due_after", &dueAfter},
		{"due_before", &dueBefore},
		{"start_after", &startAfter},
		{"start_before", &startBefore},
	} {
		tm, err := parseTime(tc.name)
		if err != nil {
			return service.ListIssuesInput{}, errors.New("invalid " + tc.name + ": want RFC3339")
		}
		*tc.dst = tm
	}
	var fields []string
	if s := qp.Get("fields"); s != "" {
		fields = strings.Split(s, ",")
	}
	// C6T4: tri-state draft filter. Absent = working-set default (drafts
	// excluded); true/false filter explicitly. Anything else is 400 —
	// silently coercing would hide client bugs.
	var draft *bool
	if s := qp.Get("draft"); s != "" {
		var b bool
		switch s {
		case "true", "1":
			b = true
		case "false", "0":
			b = false
		default:
			return service.ListIssuesInput{}, errors.New("invalid draft: want true or false")
		}
		draft = &b
	}
	// C6T5: sequence_id lookup (command palette display-ID resolution).
	// Must be a positive integer — garbage is 400, not ignored.
	var sequenceID int
	if s := qp.Get("sequence_id"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 {
			return service.ListIssuesInput{}, errors.New("invalid sequence_id: want a positive integer")
		}
		sequenceID = n
	}
	return service.ListIssuesInput{
		State:          qp.Get("state"),
		Priorities:     priorities,
		Labels:         labels,
		Assignees:      assignees,
		EstimatePoints: estimatePoints,
		Cycle:          qp.Get("cycle"),
		Q:              qp.Get("q"),
		OrderBy:        qp.Get("order_by"),
		Cursor:         qp.Get("cursor"),
		PerPage:        perPage,
		CreatedAfter:   createdAfter,
		CreatedBefore:  createdBefore,
		UpdatedAfter:   updatedAfter,
		UpdatedBefore:  updatedBefore,
		DueAfter:       dueAfter,
		DueBefore:      dueBefore,
		StartAfter:     startAfter,
		StartBefore:    startBefore,
		Undated:        qp.Get("undated") == "true" || qp.Get("undated") == "1",
		Subscribed:     qp.Get("subscribed") == "true" || qp.Get("subscribed") == "1",
		Archived:       qp.Get("archived") == "true" || qp.Get("archived") == "1",
		Draft:          draft,
		SequenceID:     sequenceID,
		Fields:         fields,
	}, nil
}

func (h *IssueHandler) listIssues(c *echo.Context) error {
	qp := c.QueryParams()
	in, err := parseIssueListInput(qp)
	if err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, err.Error(), nil)
	}

	res, err := service.ListIssues(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, in)
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
	// C4T0: the Gantt consumption contract. ?include_links=true (the
	// gantt view pairs this with ?start_after/?start_before=) attaches
	// the dependency edges for the listed issues, fetched in ONE query
	// — no N+1 from the client.
	if qp.Get("include_links") == "true" || qp.Get("include_links") == "1" {
		ids := make([]string, 0, len(res.Issues))
		for _, it := range res.Issues {
			ids = append(ids, it.ID)
		}
		links, err := service.ListIssueLinksForIssues(c.Request().Context(), h.Pool,
			c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, ids)
		if err != nil {
			return linkError(c, err)
		}
		if links == nil {
			links = []service.IssueLink{}
		}
		res.Links = links
	}
	return c.JSON(http.StatusOK, res)
}

// getIssue implements GET /api/v1/workspaces/{slug}/projects/{identifier}/issues/{uuid}.
func (h *IssueHandler) getIssue(c *echo.Context) error {
	uuid, ok := requireUUIDParam(c, "uuid", "issue id")
	if !ok {
		return nil
	}
	iss, err := service.GetIssue(c.Request().Context(), h.Pool, c.Param("slug"), c.Param("identifier"), uuid, CurrentUser(c).ID)
	if err != nil {
		return issueError(c, err)
	}
	// C5T4: ?include_children=1 attaches the child summaries (detail
	// only — the list query is untouched).
	return h.getIssueChildren(c, iss)
}

// cloneIssue implements POST
// /api/v1/workspaces/{slug}/projects/{identifier}/issues/{uuid}/clone
// (C8T4): duplicates the issue into the same project via
// service.CloneIssue and returns the new issue with 201. The member
// (15)+ gate mirrors issue create; guests get 403, non-members and
// cross-project sources 404.
func (h *IssueHandler) cloneIssue(c *echo.Context) error {
	slug, ident, uuid, actor, ok := h.issueParams(c)
	if !ok {
		return nil
	}
	// Idempotency-Key is honored exactly like issue create (opt-in via
	// header); without it the POST is a plain create.
	return withIdempotency(c, h.Pool, "POST /issues/:uuid/clone", issueError, func() (int, any, error) {
		iss, err := service.CloneIssue(c.Request().Context(), h.Pool, slug, ident, uuid, actor)
		if err != nil {
			return 0, nil, err
		}
		return http.StatusCreated, iss, nil
	})
}

type updateIssueBody struct {
	// Plain pointers (Task 12 pattern): nil = omitted, leave untouched.
	Name      *string  `json:"name"`
	Priority  *int     `json:"priority"`
	StateID   *string  `json:"state_id"`
	SortOrder *float64 `json:"sort_order"`
	IsDraft   *bool    `json:"is_draft"`
	Archived  *bool    `json:"archived"` // true = archive, false = unarchive
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
		Archived:        body.Archived,
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
	uuid, ok := requireUUIDParam(c, "uuid", "issue id")
	if !ok {
		return nil
	}
	iss, err := service.UpdateIssue(c.Request().Context(), h.Pool, c.Param("slug"), c.Param("identifier"), uuid, CurrentUser(c).ID, patch)
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
	uuid, ok := requireUUIDParam(c, "uuid", "issue id")
	if !ok {
		return nil
	}
	if err := service.DeleteIssue(c.Request().Context(), h.Pool, c.Param("slug"), c.Param("identifier"), uuid, CurrentUser(c).ID); err != nil {
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

// ---------- C5T8: atomic bulk set ----------

type bulkSetFields struct {
	StateID  *string `json:"state_id"`
	Priority *int    `json:"priority"`
	// LabelIDs nil = untouched; non-nil replaces the issue's label set
	// (empty array clears).
	LabelIDs *[]string `json:"label_ids"`
	// AssigneeID is tri-state: omitted = untouched, null = clear all
	// assignees, value = replace with that single user.
	AssigneeID service.PatchField[string] `json:"assignee_id"`
}

type bulkSetBody struct {
	IssueIDs []string      `json:"issue_ids"`
	Set      bulkSetFields `json:"set"`
}

// bulkSetIssues implements PATCH /api/v1/workspaces/{slug}/projects/{identifier}/issues/bulk:
// applies ONE set to many issues in a single fully-atomic transaction —
// any unknown, deleted, cross-project, or malformed id, or any invalid
// set field, aborts the whole batch (no partial application). 200 with
// {updated, issue_ids}. Member (15)+; every existing issueError mapping
// covers the failure modes (400/403/404).
func (h *IssueHandler) bulkSetIssues(c *echo.Context) error {
	var body bulkSetBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	updated, ids, err := service.BulkSetIssues(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, body.IssueIDs,
		service.BulkIssueSet{
			StateID:    body.Set.StateID,
			Priority:   body.Set.Priority,
			LabelIDs:   body.Set.LabelIDs,
			AssigneeID: body.Set.AssigneeID,
		})
	if err != nil {
		return issueError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"updated": updated, "issue_ids": ids})
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

// rebalanceIssues implements POST /api/v1/workspaces/{slug}/projects/{identifier}/issues/rebalance
// {state_id}: re-spaces sort_order within one state to even 1024-steps.
// The board's density escape hatch — see service.RebalanceSortOrder.
// Member (15)+.
func (h *IssueHandler) rebalanceIssues(c *echo.Context) error {
	var body struct {
		StateID string `json:"state_id"`
	}
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	n, err := service.RebalanceSortOrder(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, body.StateID)
	if err != nil {
		return issueError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"rebalanced": n})
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
	labelID, ok := requireUUIDParam(c, "labelID", "label id")
	if !ok {
		return nil
	}
	l, err := service.GetLabel(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), labelID, CurrentUser(c).ID)
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
	labelID, ok := requireUUIDParam(c, "labelID", "label id")
	if !ok {
		return nil
	}
	l, err := service.UpdateLabel(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), labelID, CurrentUser(c).ID,
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
	labelID, ok := requireUUIDParam(c, "labelID", "label id")
	if !ok {
		return nil
	}
	if err := service.DeleteLabel(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), labelID, CurrentUser(c).ID); err != nil {
		return issueError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// assignLabel implements POST /api/v1/workspaces/{slug}/projects/{identifier}/issues/{uuid}/labels/{labelID}.
// Idempotent: assigning twice succeeds with no duplicate.
func (h *IssueHandler) assignLabel(c *echo.Context) error {
	uuid, ok := requireUUIDParam(c, "uuid", "issue id")
	if !ok {
		return nil
	}
	labelID, ok := requireUUIDParam(c, "labelID", "label id")
	if !ok {
		return nil
	}
	if err := service.AssignLabel(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), uuid, labelID, CurrentUser(c).ID); err != nil {
		return issueError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// unassignLabel implements DELETE .../issues/{uuid}/labels/{labelID}.
// Idempotent: unassigning an absent label succeeds silently.
func (h *IssueHandler) unassignLabel(c *echo.Context) error {
	uuid, ok := requireUUIDParam(c, "uuid", "issue id")
	if !ok {
		return nil
	}
	labelID, ok := requireUUIDParam(c, "labelID", "label id")
	if !ok {
		return nil
	}
	if err := service.UnassignLabel(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), uuid, labelID, CurrentUser(c).ID); err != nil {
		return issueError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// assignAssignee implements POST .../issues/{uuid}/assignees/{userID}.
// Idempotent; the assignee must be a workspace member.
func (h *IssueHandler) assignAssignee(c *echo.Context) error {
	uuid, ok := requireUUIDParam(c, "uuid", "issue id")
	if !ok {
		return nil
	}
	userID, ok := requireUUIDParam(c, "userID", "user id")
	if !ok {
		return nil
	}
	if err := service.AssignAssignee(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), uuid, userID, CurrentUser(c).ID); err != nil {
		return issueError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// unassignAssignee implements DELETE .../issues/{uuid}/assignees/{userID}.
// Idempotent.
func (h *IssueHandler) unassignAssignee(c *echo.Context) error {
	uuid, ok := requireUUIDParam(c, "uuid", "issue id")
	if !ok {
		return nil
	}
	userID, ok := requireUUIDParam(c, "userID", "user id")
	if !ok {
		return nil
	}
	if err := service.UnassignAssignee(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), uuid, userID, CurrentUser(c).ID); err != nil {
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

// deleteEstimate implements DELETE
// /api/v1/workspaces/{slug}/projects/{identifier}/estimates/{estimateID}.
// 409 when live issues reference the scale's points.
func (h *IssueHandler) deleteEstimate(c *echo.Context) error {
	estimateID, ok := requireUUIDParam(c, "estimateID", "estimate id")
	if !ok {
		return nil
	}
	if err := service.DeleteEstimate(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), estimateID, CurrentUser(c).ID); err != nil {
		return issueError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

type addEstimatePointsBody struct {
	Points []estimatePointBody `json:"points"`
}

// addEstimatePoints implements POST
// /api/v1/workspaces/{slug}/projects/{identifier}/estimates/{estimateID}/points.
func (h *IssueHandler) addEstimatePoints(c *echo.Context) error {
	estimateID, ok := requireUUIDParam(c, "estimateID", "estimate id")
	if !ok {
		return nil
	}
	var body addEstimatePointsBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	points := make([]service.EstimatePointInput, 0, len(body.Points))
	for _, p := range body.Points {
		points = append(points, service.EstimatePointInput{
			Key: p.Key, Value: p.Value, Description: p.Description,
		})
	}
	est, err := service.AddEstimatePoints(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), estimateID, CurrentUser(c).ID, points)
	if err != nil {
		return issueError(c, err)
	}
	return c.JSON(http.StatusOK, est)
}

// ---------- Task 18: satellites ----------

// RegisterSatelliteRoutes mounts the comment / reaction / vote /
// subscriber / relation / history / version endpoints (Task 18) nested
// under the issue path. Toggle endpoints are idempotent POST/DELETE
// pairs (never POST-toggles): POST adds, DELETE removes, repeats are
// no-ops. Call before the SPA catch-all.
func RegisterSatelliteRoutes(e *echo.Echo, h *IssueHandler) {
	g := e.Group("/api/v1/workspaces/:slug/projects/:identifier/issues/:uuid", RequireAuth(h.Pool))
	g.GET("/comments", h.listComments)
	g.POST("/comments", h.createComment)
	g.PATCH("/comments/:commentID", h.updateComment)
	g.DELETE("/comments/:commentID", h.deleteComment)
	g.POST("/comments/:commentID/reactions", h.addCommentReaction)
	g.DELETE("/comments/:commentID/reactions", h.removeCommentReaction)
	g.GET("/reactions", h.listIssueReactions)
	g.POST("/reactions", h.addIssueReaction)
	g.DELETE("/reactions", h.removeIssueReaction)
	g.GET("/votes", h.getIssueVotes)
	g.POST("/votes", h.voteIssue)
	g.DELETE("/votes", h.unvoteIssue)
	g.GET("/subscribers", h.listSubscribers)
	g.POST("/subscribers", h.subscribeIssue)
	g.DELETE("/subscribers", h.unsubscribeIssue)
	g.GET("/relations", h.listRelations)
	g.POST("/relations", h.createRelation)
	g.DELETE("/relations/:relatedID", h.deleteRelation)
	g.GET("/history", h.getIssueHistory)
	g.GET("/versions", h.listVersions)
	g.GET("/versions/:n", h.getVersion)
	g.POST("/versions/:n/restore", h.restoreVersion)
	g.POST("/time/start", h.startTimer)
	g.POST("/time/stop", h.stopTimer)
	g.POST("/time/log", h.logTimeEntry)
	g.GET("/time", h.listTimeEntries)
	g.GET("/attachments", h.listAttachments)
	g.POST("/attachments", h.uploadAttachment)
	g.GET("/attachments/:attachmentID", h.downloadAttachment)
	g.DELETE("/attachments/:attachmentID", h.deleteAttachment)
	g.POST("/share", h.createIssueShare)
	g.GET("/share", h.listIssueShares)
	g.DELETE("/share/:token", h.revokeIssueShare)
}

func (h *IssueHandler) issueParams(c *echo.Context) (slug, ident, uuid, actor string, ok bool) {
	// The :uuid path param is lowercased once here: service code compares
	// it as a string against Postgres-rendered (lowercase) UUID text in
	// several places, and an uppercase URL UUID would corrupt those
	// comparisons. SQL ::uuid casts are case-insensitive, so this is
	// behavior-neutral for the query paths.
	//
	// The uuid is validated up front (400 on malformed) so no satellite
	// route lets a client-controlled string reach a ::uuid cast (500).
	// ok=false means the 400 was already written; the caller returns nil.
	uuid, ok = requireUUIDParam(c, "uuid", "issue id")
	if !ok {
		return "", "", "", "", false
	}
	return c.Param("slug"), c.Param("identifier"), strings.ToLower(uuid), CurrentUser(c).ID, true
}

// listComments implements GET .../issues/{uuid}/comments: the threaded
// comment tree, oldest first.
func (h *IssueHandler) listComments(c *echo.Context) error {
	slug, ident, uuid, actor, ok := h.issueParams(c)
	if !ok {
		return nil
	}
	tree, err := service.ListComments(c.Request().Context(), h.Pool, slug, ident, uuid, actor)
	if err != nil {
		return issueError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"comments": tree})
}

type createCommentBody struct {
	Content  json.RawMessage `json:"content"`
	ParentID *string         `json:"parent_id"`
}

// createComment implements POST .../issues/{uuid}/comments. Covered by
// Idempotency-Key (C2T8): a retried POST with the same key returns the
// original comment byte-identical instead of creating a duplicate.
// Validation (body bind + issue params) runs BEFORE the key claim, so
// malformed requests never consume a key.
func (h *IssueHandler) createComment(c *echo.Context) error {
	var body createCommentBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	slug, ident, uuid, actor, ok := h.issueParams(c)
	if !ok {
		return nil
	}
	return withIdempotency(c, h.Pool, "POST /issues/{uuid}/comments", issueError, func() (int, any, error) {
		comment, err := service.CreateComment(c.Request().Context(), h.Pool, slug, ident, uuid, actor, body.Content, body.ParentID)
		if err != nil {
			return 0, nil, err
		}
		return http.StatusCreated, comment, nil
	})
}

type updateCommentBody struct {
	Content json.RawMessage `json:"content"`
}

// updateComment implements PATCH .../issues/{uuid}/comments/{commentID}:
// author or admin.
func (h *IssueHandler) updateComment(c *echo.Context) error {
	var body updateCommentBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	slug, ident, uuid, actor, ok := h.issueParams(c)
	if !ok {
		return nil
	}
	commentID, ok := requireUUIDParam(c, "commentID", "comment id")
	if !ok {
		return nil
	}
	comment, err := service.UpdateComment(c.Request().Context(), h.Pool, slug, ident, uuid, commentID, actor, body.Content)
	if err != nil {
		return issueError(c, err)
	}
	return c.JSON(http.StatusOK, comment)
}

// deleteComment implements DELETE .../issues/{uuid}/comments/{commentID}:
// soft delete, author or admin.
func (h *IssueHandler) deleteComment(c *echo.Context) error {
	slug, ident, uuid, actor, ok := h.issueParams(c)
	if !ok {
		return nil
	}
	commentID, ok := requireUUIDParam(c, "commentID", "comment id")
	if !ok {
		return nil
	}
	if err := service.DeleteComment(c.Request().Context(), h.Pool, slug, ident, uuid, commentID, actor); err != nil {
		return issueError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

type reactionBody struct {
	Emoji string `json:"emoji"`
}

// addIssueReaction implements POST .../issues/{uuid}/reactions {emoji}:
// idempotent add.
func (h *IssueHandler) addIssueReaction(c *echo.Context) error {
	var body reactionBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	slug, ident, uuid, actor, ok := h.issueParams(c)
	if !ok {
		return nil
	}
	if err := service.AddIssueReaction(c.Request().Context(), h.Pool, slug, ident, uuid, actor, body.Emoji); err != nil {
		return issueError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// removeIssueReaction implements DELETE .../issues/{uuid}/reactions?emoji=:
// idempotent remove.
func (h *IssueHandler) removeIssueReaction(c *echo.Context) error {
	slug, ident, uuid, actor, ok := h.issueParams(c)
	if !ok {
		return nil
	}
	if err := service.RemoveIssueReaction(c.Request().Context(), h.Pool, slug, ident, uuid, actor, c.QueryParam("emoji")); err != nil {
		return issueError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// listIssueReactions implements GET .../issues/{uuid}/reactions: emoji
// groups with counts and the caller's reacted state.
func (h *IssueHandler) listIssueReactions(c *echo.Context) error {
	slug, ident, uuid, actor, ok := h.issueParams(c)
	if !ok {
		return nil
	}
	groups, err := service.ListIssueReactions(c.Request().Context(), h.Pool, slug, ident, uuid, actor)
	if err != nil {
		return issueError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"reactions": groups})
}

// addCommentReaction implements POST .../issues/{uuid}/comments/{commentID}/reactions {emoji}.
func (h *IssueHandler) addCommentReaction(c *echo.Context) error {
	var body reactionBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	slug, ident, uuid, actor, ok := h.issueParams(c)
	if !ok {
		return nil
	}
	commentID, ok := requireUUIDParam(c, "commentID", "comment id")
	if !ok {
		return nil
	}
	if err := service.AddCommentReaction(c.Request().Context(), h.Pool, slug, ident, uuid, commentID, actor, body.Emoji); err != nil {
		return issueError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// removeCommentReaction implements DELETE .../issues/{uuid}/comments/{commentID}/reactions?emoji=.
func (h *IssueHandler) removeCommentReaction(c *echo.Context) error {
	slug, ident, uuid, actor, ok := h.issueParams(c)
	if !ok {
		return nil
	}
	commentID, ok := requireUUIDParam(c, "commentID", "comment id")
	if !ok {
		return nil
	}
	if err := service.RemoveCommentReaction(c.Request().Context(), h.Pool, slug, ident, uuid, commentID, actor, c.QueryParam("emoji")); err != nil {
		return issueError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// getIssueVotes implements GET .../issues/{uuid}/votes: {count, voted}.
func (h *IssueHandler) getIssueVotes(c *echo.Context) error {
	slug, ident, uuid, actor, ok := h.issueParams(c)
	if !ok {
		return nil
	}
	votes, err := service.GetIssueVotes(c.Request().Context(), h.Pool, slug, ident, uuid, actor)
	if err != nil {
		return issueError(c, err)
	}
	return c.JSON(http.StatusOK, votes)
}

// voteIssue implements POST .../issues/{uuid}/votes: idempotent upvote.
func (h *IssueHandler) voteIssue(c *echo.Context) error {
	slug, ident, uuid, actor, ok := h.issueParams(c)
	if !ok {
		return nil
	}
	if err := service.VoteIssue(c.Request().Context(), h.Pool, slug, ident, uuid, actor); err != nil {
		return issueError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// unvoteIssue implements DELETE .../issues/{uuid}/votes: idempotent remove.
func (h *IssueHandler) unvoteIssue(c *echo.Context) error {
	slug, ident, uuid, actor, ok := h.issueParams(c)
	if !ok {
		return nil
	}
	if err := service.UnvoteIssue(c.Request().Context(), h.Pool, slug, ident, uuid, actor); err != nil {
		return issueError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// listSubscribers implements GET .../issues/{uuid}/subscribers.
func (h *IssueHandler) listSubscribers(c *echo.Context) error {
	slug, ident, uuid, actor, ok := h.issueParams(c)
	if !ok {
		return nil
	}
	subs, err := service.ListSubscribers(c.Request().Context(), h.Pool, slug, ident, uuid, actor)
	if err != nil {
		return issueError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"subscribers": subs})
}

// subscribeIssue implements POST .../issues/{uuid}/subscribers: the
// caller subscribes themselves; idempotent.
func (h *IssueHandler) subscribeIssue(c *echo.Context) error {
	slug, ident, uuid, actor, ok := h.issueParams(c)
	if !ok {
		return nil
	}
	if err := service.SubscribeIssue(c.Request().Context(), h.Pool, slug, ident, uuid, actor); err != nil {
		return issueError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// unsubscribeIssue implements DELETE .../issues/{uuid}/subscribers:
// idempotent.
func (h *IssueHandler) unsubscribeIssue(c *echo.Context) error {
	slug, ident, uuid, actor, ok := h.issueParams(c)
	if !ok {
		return nil
	}
	if err := service.UnsubscribeIssue(c.Request().Context(), h.Pool, slug, ident, uuid, actor); err != nil {
		return issueError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

type createRelationBody struct {
	RelatedIssueID string `json:"related_issue_id"`
	Type           string `json:"type"`
}

// listRelations implements GET .../issues/{uuid}/relations: canonical
// rows plus derived reverses (A blocked_by B → B shows blocking A).
func (h *IssueHandler) listRelations(c *echo.Context) error {
	slug, ident, uuid, actor, ok := h.issueParams(c)
	if !ok {
		return nil
	}
	rels, err := service.ListRelations(c.Request().Context(), h.Pool, slug, ident, uuid, actor)
	if err != nil {
		return issueError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"relations": rels})
}

// createRelation implements POST .../issues/{uuid}/relations
// {related_issue_id, type}: one canonical row; repeats are no-ops.
func (h *IssueHandler) createRelation(c *echo.Context) error {
	var body createRelationBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	slug, ident, uuid, actor, ok := h.issueParams(c)
	if !ok {
		return nil
	}
	if err := service.CreateRelation(c.Request().Context(), h.Pool, slug, ident, uuid, actor, body.RelatedIssueID, body.Type); err != nil {
		return issueError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// deleteRelation implements DELETE .../issues/{uuid}/relations/{relatedID}?type=:
// the type is the label from this issue's side — a reverse label
// (e.g. blocking) resolves to the canonical row stored from the other side.
func (h *IssueHandler) deleteRelation(c *echo.Context) error {
	slug, ident, uuid, actor, ok := h.issueParams(c)
	if !ok {
		return nil
	}
	relatedID, ok := requireUUIDParam(c, "relatedID", "related issue id")
	if !ok {
		return nil
	}
	if err := service.DeleteRelation(c.Request().Context(), h.Pool, slug, ident, uuid, actor, relatedID, c.QueryParam("type")); err != nil {
		return issueError(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// getIssueHistory implements GET .../issues/{uuid}/history: the
// issue_activities rows, chronological, with actor info.
func (h *IssueHandler) getIssueHistory(c *echo.Context) error {
	slug, ident, uuid, actor, ok := h.issueParams(c)
	if !ok {
		return nil
	}
	hist, err := service.GetIssueHistory(c.Request().Context(), h.Pool, slug, ident, uuid, actor)
	if err != nil {
		return issueError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"history": hist})
}

// listVersions implements GET .../issues/{uuid}/versions: oldest first.
func (h *IssueHandler) listVersions(c *echo.Context) error {
	slug, ident, uuid, actor, ok := h.issueParams(c)
	if !ok {
		return nil
	}
	versions, err := service.ListVersions(c.Request().Context(), h.Pool, slug, ident, uuid, actor)
	if err != nil {
		return issueError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"versions": versions})
}

// getVersion implements GET .../issues/{uuid}/versions/{n}.
func (h *IssueHandler) getVersion(c *echo.Context) error {
	n, err := strconv.Atoi(c.Param("n"))
	if err != nil || n < 1 {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid version number", nil)
	}
	slug, ident, uuid, actor, ok := h.issueParams(c)
	if !ok {
		return nil
	}
	ver, err := service.GetVersion(c.Request().Context(), h.Pool, slug, ident, uuid, actor, n)
	if err != nil {
		return issueError(c, err)
	}
	return c.JSON(http.StatusOK, ver)
}

// restoreVersion implements POST .../issues/{uuid}/versions/{n}/restore:
// applies the old snapshot as a NEW version — history is never rewritten.
func (h *IssueHandler) restoreVersion(c *echo.Context) error {
	n, err := strconv.Atoi(c.Param("n"))
	if err != nil || n < 1 {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid version number", nil)
	}
	slug, ident, uuid, actor, ok := h.issueParams(c)
	if !ok {
		return nil
	}
	iss, err := service.RestoreIssueVersion(c.Request().Context(), h.Pool, slug, ident, uuid, actor, n)
	if err != nil {
		return issueError(c, err)
	}
	return c.JSON(http.StatusOK, iss)
}
