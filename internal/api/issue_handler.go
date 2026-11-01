package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
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
	// C8T7: duplicate detection on create. The static "similar" segment
	// wins over GET /:uuid by Echo's specificity rules (same pattern as
	// /export above).
	g.GET("/similar", h.similarIssues)
	g.POST("/bulk-update", h.bulkUpdateIssues)
	g.POST("/bulk-delete", h.bulkDeleteIssues)
	// C52T1: bulk restore from trash. Static segment wins over
	// POST /:uuid by Echo's specificity rules (same pattern as
	// /bulk-delete above).
	g.POST("/bulk-restore", h.bulkRestoreIssues)
	// C5T8: atomic bulk set. Static segment wins over PATCH /:uuid by
	// Echo's specificity rules (kept adjacent for readability).
	g.PATCH("/bulk", h.bulkSetIssues)
	g.POST("/rebalance", h.rebalanceIssues)
	g.GET("/:uuid", h.getIssue)
	g.PATCH("/:uuid", h.updateIssue)
	g.DELETE("/:uuid", h.deleteIssue)
	// C52T0: restore a soft-deleted issue from trash.
	g.POST("/:uuid/restore", h.restoreIssue)
	// C8T4: clone endpoint. The static "clone" segment keeps this distinct
	// from PATCH /:uuid by Echo's specificity rules (kept adjacent for
	// readability, same style as /export and /bulk).
	g.POST("/:uuid/clone", h.cloneIssue)
	// C24T0: move endpoint. The static "move" segment keeps this distinct
	// from PATCH /:uuid by Echo's specificity rules (same pattern as
	// /clone above).
	g.POST("/:uuid/move", h.moveIssue)
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
	// C16T3: blocker guard. 409 with code open_blockers; details names
	// the open blockers by display ID so the client can list them.
	case errors.Is(err, service.ErrOpenBlockers):
		var obe *service.OpenBlockersError
		blockers := []string{}
		if errors.As(err, &obe) && obe.Blockers != nil {
			blockers = obe.Blockers
		}
		return WriteError(c, http.StatusConflict, ErrCodeOpenBlockers,
			"issue has open blockers: "+strings.Join(blockers, ", "),
			map[string]any{"blockers": blockers})
	// C68T0: workflow transition governance. 409 with code
	// state_transition_not_allowed; details names the allowed next-state
	// IDs so the client can render them.
	case errors.Is(err, service.ErrStateTransitionNotAllowed):
		var ste *service.StateTransitionError
		allowed := []string{}
		if errors.As(err, &ste) && ste.Allowed != nil {
			allowed = ste.Allowed
		}
		return WriteError(c, http.StatusConflict, ErrCodeStateTransitionNotAllowed,
			"state transition not allowed by the project's workflow",
			map[string]any{"allowed": allowed})
	case errors.Is(err, service.ErrInvalidDateRange):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "start_date must not be after target_date", nil)
	case errors.Is(err, service.ErrInvalidIdentifier):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid identifier: 1-12 letters and digits", nil)
	case errors.Is(err, service.ErrNameRequired):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "name is required", nil)
	case errors.Is(err, service.ErrNothingToUpdate):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "nothing to update", nil)
	// C24T0: cross-project move errors.
	case errors.Is(err, service.ErrMoveSameProject):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "cannot move an issue into its own project", nil)
	case errors.Is(err, service.ErrMoveIntakeIssue):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "intake issues cannot be moved between projects; triage them first", nil)
	// C18T1: required custom fields missing on issue create.
	case errors.Is(err, service.ErrCustomFieldRequired):
		return writeCustomFieldRequiredError(c, err)
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
	// C19T3: release assignment errors on PATCH release_id.
	case errors.Is(err, service.ErrReleaseNotFound):
		return WriteError(c, http.StatusNotFound, ErrCodeNotFound, "release not found", nil)
	case errors.Is(err, service.ErrInvalidReleaseID):
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid release_id", nil)
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
	// C64T0: a restore whose snapshot is identical to the live row is
	// meaningless against current state — 409, matching the state-
	// conflict convention (cycle / dup-name / open-blocker cases).
	case errors.Is(err, service.ErrVersionRestoreNoOp):
		return WriteError(c, http.StatusConflict, ErrCodeConflict, "issue already matches that version", nil)
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
	// CustomValues carries optional initial custom-field values
	// (field_id → value, same shapes as the custom-values PUT body),
	// written atomically with the issue (C18T1). On non-draft creates
	// every required field of the project must arrive non-empty here,
	// else 400 naming the missing fields.
	CustomValues map[string]json.RawMessage `json:"custom_values"`
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
// response byte-identical without creating a second issue. C18T1: an
// optional custom_values map is written atomically with the issue;
// non-draft creates 400 (naming the missing fields) unless every
// required field of the project arrives non-empty.
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
				CustomValues:    body.CustomValues,
			})
		if err != nil {
			return 0, nil, err
		}
		return http.StatusCreated, iss, nil
	})
}

// listIssues implements GET /api/v1/workspaces/{slug}/projects/{identifier}/issues:
// filters (?state=&assignee=&label=&priority=&estimate=&cycle=&module=&q=,
// ?parent=&epic= (C75T0: comma-separated or repeatable; "none" = top-level / no epic),
// ?created_after/before=&updated_after/before=&due_after/before=,
// ?due_date_empty=1 (C80T0: target_date IS NULL — "no due date",
// cannot be combined with ?due_after/before=),
// ?start_after/before= (C3T5: bracket issues.start_date for the calendar),
// ?undated=1 (C3T5: issues with neither target nor start date),
// ?subscribed=true), ordering (?order_by=-updated_at), cursor pagination
// (?cursor=&per_page=), delta sync (?updated_after=), and sparse fieldsets
// (?fields=description). C91T0: ?pql= (Plane Query Language, see
// internal/service/pql.go) is mutually exclusive with the visual
// filters — when non-blank, visual filter params are ignored entirely
// (not parsed, not validated); pagination still applies, and PQL
// parse/resolution failures are 400 with message + position. C98T0:
// a PQL ORDER BY clause wins over ?order_by= (the query is
// self-contained); without one, ?order_by= applies exactly as today,
// and cursors are validated/minted against the effective order.
// C99T1: ?order_by= accepts a comma-separated key list
// (e.g. ?order_by=priority,-updated_at), each key from the
// listOrders whitelist, "-" prefix for DESC; unknown key, empty
// segment, or duplicate key is 400 ErrInvalidOrderBy.
// C98T1: a PQL LIMIT clause caps the page to at most
// min(per_page, n) rows with next_cursor always null (single page);
// a cursor combined with LIMIT is a 400, never silently ignored.
// C95T0 exception: the scope containers ?cycle= and ?module= are NOT
// visual filters — they survive PQL mode and AND with the PQL
// predicate, so a query run inside a cycle/module stays inside it.
// Multi-value filters take comma-separated lists
// (single values keep working); assignee=/estimate= accept "none" for
// unassigned / unestimated issues. 200 with the {results, next_cursor}
// envelope (spec §5). ?include_links=true (C4T0) attaches the issue
// dependency edges for the listed issues as a top-level "links" array,
// fetched in ONE query — the Gantt view pairs it with
// ?start_after/?start_before=.
// issueListCommon carries the list params that are NOT visual filters:
// pagination/sort (per_page, cursor, order_by, fields) and list-mode
// flags (draft, sequence_id, archived, deleted,
// ?include_project_structure=1, C21T2). parseIssueListCommon is shared
// by the visual-filter parser and the PQL parser so both honor
// identical pagination/mode semantics.
type issueListCommon struct {
	OrderBy                 string
	Cursor                  string
	PerPage                 int
	Fields                  []string
	Draft                   *bool
	SequenceID              int
	Archived                bool
	Deleted                 bool
	IncludeProjectStructure bool
}

func parseIssueListCommon(qp url.Values) (issueListCommon, error) {
	// C2T8: per_page is REJECTED (400) when out of range, on every list
	// endpoint — never silently clamped. Rationale: the documented
	// contract is "want 1-100"; fail-fast surfaces client bugs instead of
	// returning fewer rows than asked for. (The service layer still
	// clamps as defense-in-depth for direct internal callers.)
	perPage := 25
	if s := qp.Get("per_page"); s != "" {
		p, err := strconv.Atoi(s)
		if err != nil || p < 1 || p > 100 {
			return issueListCommon{}, errors.New("invalid per_page: want 1-100")
		}
		perPage = p
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
			return issueListCommon{}, errors.New("invalid draft: want true or false")
		}
		draft = &b
	}
	// C6T5: sequence_id lookup (command palette display-ID resolution).
	// Must be a positive integer — garbage is 400, not ignored.
	var sequenceID int
	if s := qp.Get("sequence_id"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 {
			return issueListCommon{}, errors.New("invalid sequence_id: want a positive integer")
		}
		sequenceID = n
	}
	return issueListCommon{
		OrderBy:                 qp.Get("order_by"),
		Cursor:                  qp.Get("cursor"),
		PerPage:                 perPage,
		Fields:                  fields,
		Draft:                   draft,
		SequenceID:              sequenceID,
		Archived:                qp.Get("archived") == "true" || qp.Get("archived") == "1",
		Deleted:                 qp.Get("deleted") == "true" || qp.Get("deleted") == "1",
		IncludeProjectStructure: qp.Get("include_project_structure") == "1" || qp.Get("include_project_structure") == "true",
	}, nil
}

// parseRepeatIDList parses a repeatable AND comma-separated ID param
// (C75T0: parent=/epic=). "none" is the top-level / no-epic sentinel
// (the "none" convention of assignee=/estimate=); every other value
// must be a UUID — garbage is 400 naming the bad value, never silently
// ignored. The service re-validates (validateListFilterValues) as
// defense-in-depth for direct internal callers. Shared by the visual
// path and the PQL path (C108T0), so both validate ?epic= identically.
func parseRepeatIDList(qp url.Values, name string) ([]string, error) {
	var out []string
	for _, raw := range qp[name] {
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if part != "none" {
				if _, err := uuid.Parse(part); err != nil {
					return nil, fmt.Errorf("invalid %s: %q is not a UUID or \"none\"", name, part)
				}
			}
			out = append(out, part)
		}
	}
	return out, nil
}

// parseIssueListInput parses the query params shared by the issue list
// and the export endpoint (C8T3), so both honor identical filter
// semantics. Pagination/sort params and list-mode flags come from
// parseIssueListCommon; exporters ignore the pagination/sort ones. A
// non-nil error is always a 400-class client error with a
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
	parents, err := parseRepeatIDList(qp, "parent")
	if err != nil {
		return service.ListIssuesInput{}, err
	}
	epics, err := parseRepeatIDList(qp, "epic")
	if err != nil {
		return service.ListIssuesInput{}, err
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
	// C80T0: per-field is-empty due-date filter (?due_date_empty=1):
	// target_date (the due date) IS NULL, ANDed with every other filter.
	// Combining it with due_after=/due_before= on the same field is a 400:
	// the two predicates contradict each other, and silently preferring
	// one would hide a client bug (the same honest-error rule as the
	// malformed-input 400s above). due_date_empty + undated is allowed —
	// plain AND, just redundant.
	dueDateEmpty := qp.Get("due_date_empty") == "true" || qp.Get("due_date_empty") == "1"
	if dueDateEmpty && (dueAfter != nil || dueBefore != nil) {
		return service.ListIssuesInput{}, errors.New("invalid due_date_empty: cannot be combined with due_after/due_before")
	}
	common, err := parseIssueListCommon(qp)
	if err != nil {
		return service.ListIssuesInput{}, err
	}
	return service.ListIssuesInput{
		State:                   qp.Get("state"),
		Priorities:              priorities,
		Labels:                  labels,
		Assignees:               assignees,
		EstimatePoints:          estimatePoints,
		Parents:                 parents,
		Epics:                   epics,
		Cycle:                   qp.Get("cycle"),
		Module:                  qp.Get("module"),
		Q:                       qp.Get("q"),
		OrderBy:                 common.OrderBy,
		Cursor:                  common.Cursor,
		PerPage:                 common.PerPage,
		CreatedAfter:            createdAfter,
		CreatedBefore:           createdBefore,
		UpdatedAfter:            updatedAfter,
		UpdatedBefore:           updatedBefore,
		DueAfter:                dueAfter,
		DueBefore:               dueBefore,
		StartAfter:              startAfter,
		StartBefore:             startBefore,
		Undated:                 qp.Get("undated") == "true" || qp.Get("undated") == "1",
		DueDateEmpty:            dueDateEmpty,
		Subscribed:              qp.Get("subscribed") == "true" || qp.Get("subscribed") == "1",
		Archived:                common.Archived,
		Deleted:                 common.Deleted,
		Draft:                   common.Draft,
		SequenceID:              common.SequenceID,
		Fields:                  common.Fields,
		IncludeProjectStructure: common.IncludeProjectStructure,
	}, nil
}

// parsePQLListInput parses the list params for a PQL request (C91T0):
// the shared pagination/mode params plus the ?pql= predicate. Visual
// filter params are NOT parsed here at all — not even validated — so a
// stale visual param (even a malformed one) can never 400 a PQL
// request; the service layer additionally zeroes any visual filters on
// the input, so the exclusivity contract holds end to end. C95T0
// exception: the scope containers ?cycle= and ?module= ARE parsed
// here — they are containers, not filter dimensions, and they survive
// PQL mode (service withoutVisualFilters) and AND with the predicate.
// C108T0 adds ?epic= to that exception, same shape: the scope
// container is parsed here (repeat-ID list, same UUID/"none"
// validation and honest 400s as the visual path) and survives PQL
// mode — so /issues?epic=<uuid>&pql=... stays inside the epic. A blank
// ?pql= never reaches this function: listIssues treats it as absent
// and parses the visual filters instead.
func parsePQLListInput(qp url.Values) (service.ListIssuesInput, error) {
	common, err := parseIssueListCommon(qp)
	if err != nil {
		return service.ListIssuesInput{}, err
	}
	epics, err := parseRepeatIDList(qp, "epic")
	if err != nil {
		return service.ListIssuesInput{}, err
	}
	return service.ListIssuesInput{
		PQL:                     strings.TrimSpace(qp.Get("pql")),
		Epics:                   epics,
		Cycle:                   qp.Get("cycle"),
		Module:                  qp.Get("module"),
		OrderBy:                 common.OrderBy,
		Cursor:                  common.Cursor,
		PerPage:                 common.PerPage,
		Archived:                common.Archived,
		Deleted:                 common.Deleted,
		Draft:                   common.Draft,
		SequenceID:              common.SequenceID,
		Fields:                  common.Fields,
		IncludeProjectStructure: common.IncludeProjectStructure,
	}, nil
}

func (h *IssueHandler) listIssues(c *echo.Context) error {
	qp := c.QueryParams()
	// C91T0: PQL mode is exclusive — a non-blank ?pql= replaces the
	// visual filters entirely (they are not parsed, not validated, not
	// applied). C95T0: the scope params ?cycle=/?module= are the one
	// exception — they are parsed in PQL mode and AND with the
	// predicate. C108T0 adds ?epic= to that exception, same shape.
	// A blank ?pql= is absent: the visual filters parse as
	// usual, so a cleared PQL editor degrades to an unfiltered list
	// rather than a 400.
	var in service.ListIssuesInput
	var err error
	if strings.TrimSpace(qp.Get("pql")) != "" {
		in, err = parsePQLListInput(qp)
	} else {
		in, err = parseIssueListInput(qp)
	}
	if err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, err.Error(), nil)
	}

	// C52T0: ?deleted=1 is the trash list — deleted-only, most recently
	// deleted first. It branches here, before the normal list: the
	// other filters/order are ignored in trash mode, and
	// ?include_links= (the Gantt contract) does not apply to deleted
	// rows. The service's Deleted flag exists only to carry the parse
	// result to this branch; it never reaches the export path.
	if in.Deleted {
		res, err := service.ListDeletedIssues(c.Request().Context(), h.Pool,
			c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, in.Cursor, in.PerPage)
		if err != nil {
			switch {
			case errors.Is(err, service.ErrInvalidCursor):
				return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid cursor", nil)
			case errors.Is(err, service.ErrInvalidListFilter):
				return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid filter", nil)
			}
			return issueError(c, err)
		}
		return c.JSON(http.StatusOK, res)
	}

	res, err := service.ListIssues(c.Request().Context(), h.Pool,
		c.Param("slug"), c.Param("identifier"), CurrentUser(c).ID, in)
	if err != nil {
		// C91T0: a PQL failure is a client error with message +
		// position, never a 500. details carries the 1-based column
		// when the error names one, so the PQL editor can underline
		// the offending token.
		var pqlErr *service.PQLQueryError
		if errors.As(err, &pqlErr) {
			var details any
			if pqlErr.Pos > 0 {
				details = map[string]any{"position": pqlErr.Pos}
			}
			return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, pqlErr.Msg, details)
		}
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

type moveIssueBody struct {
	TargetProjectIdentifier string `json:"target_project_identifier"`
}

// moveIssue implements POST
// /api/v1/workspaces/{slug}/projects/{identifier}/issues/{uuid}/move
// (C24T0): moves the issue (plus its sub-issue subtree) into another
// project of the same workspace via service.MoveIssue and returns the
// move report with 200. Member (15)+ on both projects; 400 on
// same-project, intake issues, or a bad target identifier; 404 when the
// source issue or the target project is missing.
func (h *IssueHandler) moveIssue(c *echo.Context) error {
	slug, ident, uuid, actor, ok := h.issueParams(c)
	if !ok {
		return nil
	}
	var body moveIssueBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	if strings.TrimSpace(body.TargetProjectIdentifier) == "" {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "target_project_identifier is required", nil)
	}
	// Idempotency-Key is honored exactly like the clone endpoint
	// (opt-in via header); without it the POST is a plain move.
	return withIdempotency(c, h.Pool, "POST /issues/:uuid/move", issueError, func() (int, any, error) {
		res, err := service.MoveIssue(c.Request().Context(), h.Pool, slug, ident, uuid, actor,
			service.MoveIssueInput{TargetProjectIdentifier: body.TargetProjectIdentifier})
		if err != nil {
			return 0, nil, err
		}
		return http.StatusOK, res, nil
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
	// C19T3: tri-state release assignment (id string; null clears).
	ReleaseID service.PatchField[string] `json:"release_id"`
	// C16T3: bypasses the open-blocker guard when moving to a completed
	// state. A request directive, not a patch field (never persisted).
	IgnoreBlockers bool `json:"ignore_blockers"`
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
		ReleaseID:       body.ReleaseID,
		IsDraft:         body.IsDraft,
		Archived:        body.Archived,
		IgnoreBlockers:  body.IgnoreBlockers,
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

// restoreIssue implements POST /api/v1/workspaces/{slug}/projects/{identifier}/issues/{uuid}/restore
// (C52T0): un-deletes a soft-deleted issue and returns the restored
// issue with 200. A live issue restores as 404 ("not in trash"), exactly
// as a second DELETE reads as not-found; unknown ids are 404; guests
// get 403. No Idempotency-Key (delete precedent): a replayed restore
// is a 404, not a replayed success.
func (h *IssueHandler) restoreIssue(c *echo.Context) error {
	slug, ident, uuid, actor, ok := h.issueParams(c)
	if !ok {
		return nil
	}
	iss, err := service.RestoreIssue(c.Request().Context(), h.Pool, slug, ident, uuid, actor)
	if err != nil {
		return issueError(c, err)
	}
	return c.JSON(http.StatusOK, iss)
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
	// C16T3: bypasses the open-blocker guard when the set moves issues
	// to a completed state (applies to the whole batch).
	IgnoreBlockers bool `json:"ignore_blockers"`
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
			StateID:        body.Set.StateID,
			Priority:       body.Set.Priority,
			LabelIDs:       body.Set.LabelIDs,
			AssigneeID:     body.Set.AssigneeID,
			IgnoreBlockers: body.Set.IgnoreBlockers,
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

// bulkRestoreIssues implements POST /api/v1/workspaces/{slug}/projects/{identifier}/issues/bulk-restore:
// restores many soft-deleted issues with the same per-item savepoint
// semantics as bulk-delete. 200 with {results:[{id, ok, error?}]}.
// A live id in the batch is a per-item failure, not a whole-batch
// failure. Member (15)+. Honors Idempotency-Key.
func (h *IssueHandler) bulkRestoreIssues(c *echo.Context) error {
	var body bulkDeleteBody
	if err := c.Bind(&body); err != nil {
		return WriteError(c, http.StatusBadRequest, ErrCodeBadRequest, "invalid request body", nil)
	}
	return withIdempotency(c, h.Pool, "POST /issues/bulk-restore", issueError, func() (int, any, error) {
		results, err := service.BulkRestoreIssues(c.Request().Context(), h.Pool,
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
	g.POST("/comments/:commentID/resolve", h.resolveComment)
	g.POST("/comments/:commentID/reopen", h.reopenComment)
	g.GET("/comments/:commentID/reactions", h.listCommentReactions)
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
// comment tree, oldest first. ?resolved=all|open|resolved filters
// thread roots (default all); an invalid value is a 400.
func (h *IssueHandler) listComments(c *echo.Context) error {
	slug, ident, uuid, actor, ok := h.issueParams(c)
	if !ok {
		return nil
	}
	tree, err := service.ListCommentsFiltered(c.Request().Context(), h.Pool, slug, ident, uuid, actor, c.QueryParam("resolved"))
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

// resolveComment implements POST .../issues/{uuid}/comments/{commentID}/resolve:
// marks a thread root resolved (200 Comment). Member (15)+; resolve on
// a reply is a 400.
func (h *IssueHandler) resolveComment(c *echo.Context) error {
	slug, ident, uuid, actor, ok := h.issueParams(c)
	if !ok {
		return nil
	}
	commentID, ok := requireUUIDParam(c, "commentID", "comment id")
	if !ok {
		return nil
	}
	comment, err := service.ResolveComment(c.Request().Context(), h.Pool, slug, ident, uuid, commentID, actor)
	if err != nil {
		return issueError(c, err)
	}
	return c.JSON(http.StatusOK, comment)
}

// reopenComment implements POST .../issues/{uuid}/comments/{commentID}/reopen:
// clears a thread root's resolution (200 Comment). Member (15)+;
// reopen on a reply is a 400.
func (h *IssueHandler) reopenComment(c *echo.Context) error {
	slug, ident, uuid, actor, ok := h.issueParams(c)
	if !ok {
		return nil
	}
	commentID, ok := requireUUIDParam(c, "commentID", "comment id")
	if !ok {
		return nil
	}
	comment, err := service.ReopenComment(c.Request().Context(), h.Pool, slug, ident, uuid, commentID, actor)
	if err != nil {
		return issueError(c, err)
	}
	return c.JSON(http.StatusOK, comment)
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

// listCommentReactions implements GET
// .../issues/{uuid}/comments/{commentID}/reactions: emoji groups with
// counts and the caller's reacted state. Mirrors listIssueReactions.
func (h *IssueHandler) listCommentReactions(c *echo.Context) error {
	slug, ident, uuid, actor, ok := h.issueParams(c)
	if !ok {
		return nil
	}
	commentID, ok := requireUUIDParam(c, "commentID", "comment id")
	if !ok {
		return nil
	}
	groups, err := service.ListCommentReactions(c.Request().Context(), h.Pool, slug, ident, uuid, commentID, actor)
	if err != nil {
		return issueError(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"reactions": groups})
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
