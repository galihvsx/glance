package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrIssueNotFound is returned when the issue UUID matches nothing in
	// the project (or the row is soft-deleted). It is deliberately distinct
	// from ErrNotFound (missing workspace / non-member caller) and
	// ErrProjectNotFound: on this path the workspace and project are
	// confirmed and the caller is a member, so "issue not found" is the
	// honest answer.
	ErrIssueNotFound = errors.New("service: issue not found")
	// ErrInvalidState is returned when state_id matches no state, or a
	// state belonging to another project.
	ErrInvalidState = errors.New("service: invalid state")
	// ErrInvalidPriority is returned when priority is outside 0-4
	// (Plane vocabulary: 0=none, 1=low, 2=medium, 3=high, 4=urgent).
	ErrInvalidPriority = errors.New("service: invalid priority")
	// ErrParentNotFound is returned when parent_id matches no live issue
	// in the project.
	ErrParentNotFound = errors.New("service: parent issue not found")
	// ErrInvalidParent is returned when parent_id is malformed or the
	// issue would parent itself.
	ErrInvalidParent = errors.New("service: invalid parent")
	// ErrInvalidDateRange is returned when start_date is after target_date.
	ErrInvalidDateRange = errors.New("service: start_date is after target_date")
)

// PatchField is a tri-state patch value for nullable columns: Set=false
// means the client omitted the field (leave untouched); Set=true with a
// nil Value means explicit null (clear the column); Set=true with a
// non-nil Value assigns it. Plain *T fields (Task 12 pattern) stay for
// non-nullable columns, where nil unambiguously means "omitted".
type PatchField[T any] struct {
	Set   bool
	Value *T
}

// UnmarshalJSON records presence: omitted fields never reach this method,
// so Set stays false; an explicit null sets Value to nil with Set=true.
func (p *PatchField[T]) UnmarshalJSON(data []byte) error {
	p.Set = true
	if string(data) == "null" {
		p.Value = nil
		return nil
	}
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	p.Value = &v
	return nil
}

// SubtaskProgress is the computed rollup of an issue's DIRECT children
// (C17T2): total children and how many sit in a 'completed'-group state.
// Populated by the detail and list queries as a single aggregate subquery
// — never N+1. Nil on rows read through paths that don't select it.
type SubtaskProgress struct {
	Total int `json:"total"`
	Done  int `json:"done"`
}

// Issue is a work item inside a project. DisplayID is derived
// ({IDENTIFIER}-{sequence_id}, e.g. ENG-123) and never stored — the
// per-project sequence counter is the source of truth.
type Issue struct {
	ID              string          `json:"id"`
	ProjectID       string          `json:"project_id"`
	SequenceID      int             `json:"sequence_id"`
	DisplayID       string          `json:"display_id"`
	Name            string          `json:"name"`
	Description     json.RawMessage `json:"description,omitempty"`
	Priority        int             `json:"priority"`
	StateID         string          `json:"state_id"`
	ParentID        *string         `json:"parent_id,omitempty"`
	SortOrder       float64         `json:"sort_order"`
	StartDate       *time.Time      `json:"start_date,omitempty"`
	TargetDate      *time.Time      `json:"target_date,omitempty"`
	EstimatePointID *string         `json:"estimate_point_id,omitempty"`
	IsDraft         bool            `json:"is_draft"`
	ArchivedAt      *time.Time      `json:"archived_at,omitempty"`
	CreatedBy       string          `json:"created_by"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
	// StateEnteredAt is when the issue entered its current state
	// (C70T0): created_at of the latest issue_activities state_id row.
	// When no row exists (last transitioned before activity tracking,
	// or created directly in a state) it falls back to created_at — an
	// approximate UPPER BOUND on the true time in state. Populated only
	// by the detail + list read paths; omitempty keeps every other
	// response shape byte-identical.
	StateEnteredAt *time.Time `json:"state_entered_at,omitempty"`
	// TimeInStateSeconds is now − StateEnteredAt, computed server-side
	// at READ time (C70T0) — never stored, never ticked. Terminal
	// states keep counting. Deliberately absent from webhook payloads
	// and realtime broadcasts (derived values go stale in payloads).
	TimeInStateSeconds *int64 `json:"time_in_state_seconds,omitempty"`
	// SubtaskProgress is populated only by the detail/list queries that
	// select subtaskProgressAgg; omitempty keeps every other response
	// shape byte-identical.
	SubtaskProgress *SubtaskProgress `json:"subtask_progress,omitempty"`
	// DeletedAt is populated ONLY by ListDeletedIssues (C52T0, the trash
	// list): the soft-delete timestamp the Trash page sorts and renders
	// by. Nil everywhere else — scanIssue never selects deleted_at, so
	// omitempty keeps every other response shape byte-identical.
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
	// Epics is the issue's epic memberships (C71T0): [{id, name,
	// archived_at}]. Archived epics stay visible here (the UI dims
	// them). Populated by getIssueRow (detail paths) only; omitempty
	// keeps every other response shape byte-identical.
	Epics []EpicRef `json:"epics,omitempty"`
}

// CreateIssueInput carries the fields for a new issue. Nil pointers mean
// "not provided" (defaults apply); StateID nil selects the project's
// backlog-group state.
type CreateIssueInput struct {
	Name            string
	Description     json.RawMessage
	Priority        *int
	StateID         *string
	ParentID        *string
	SortOrder       *float64
	StartDate       *time.Time
	TargetDate      *time.Time
	EstimatePointID *string
	IsDraft         bool
	// Intake opts the new issue into the project's intake inbox (Task 20):
	// a pending intake_issues row is written in the same tx. Default false
	// preserves the direct-to-backlog behavior.
	Intake bool
	// CustomValues carries optional initial custom-field values
	// (field_id → raw JSON, same shape as the custom-values PUT body),
	// written in the same tx as the issue (C18T1). On non-draft creates
	// every required field of the project must arrive non-empty here —
	// absent, null, and empty/whitespace-only strings count as missing
	// (checkbox false counts as present); a violation is
	// *CustomFieldRequiredError (400). Drafts are exempt: a draft is
	// explicitly unfinished work.
	CustomValues map[string]json.RawMessage
}

// IssuePatch is a partial issue update: nil / Set=false fields are left
// untouched, so PATCH carries true partial-update semantics. A patch with
// no fields at all is ErrNothingToUpdate.
type IssuePatch struct {
	Name            *string
	Description     PatchField[json.RawMessage]
	Priority        *int
	StateID         *string
	ParentID        PatchField[string]
	SortOrder       *float64
	StartDate       PatchField[time.Time]
	TargetDate      PatchField[time.Time]
	EstimatePointID PatchField[string]
	// ReleaseID is the tri-state release assignment (C19T3): Set with a
	// value moves the issue to that release, Set with nil clears it.
	// Validated against the project's releases (resolveRelease).
	ReleaseID PatchField[string]
	IsDraft   *bool
	Archived  *bool // true = archive (now()), false = unarchive (NULL), nil = untouched
	// IgnoreBlockers is a request directive, not a field: when true, the
	// C16T3 blocker guard is bypassed on moves into a completed state.
	// It is never counted by hasFields.
	IgnoreBlockers bool
}

// hasFields reports whether the patch carries anything at all.
func (p IssuePatch) hasFields() bool {
	return p.Name != nil || p.Description.Set || p.Priority != nil ||
		p.StateID != nil || p.ParentID.Set || p.SortOrder != nil ||
		p.StartDate.Set || p.TargetDate.Set || p.EstimatePointID.Set ||
		p.ReleaseID.Set || p.IsDraft != nil || p.Archived != nil
}

// issueColumns is the bare column list for INSERT/UPDATE ... RETURNING and
// unaliased SELECTs (no table alias exists in those positions).
const issueColumns = `id::text, project_id::text, sequence_id, name, description,
	priority, state_id::text, parent_id::text, sort_order, start_date, target_date,
	estimate_point_id::text, is_draft, archived_at, created_by::text, created_at, updated_at`

// scanIssue scans the full issue column list. Nullable columns land in
// pointer / nil-able fields: a NULL description stays nil and is omitted
// from the JSON response via omitempty.
func scanIssue(row pgx.Row) (*Issue, error) {
	var iss Issue
	var description []byte
	err := row.Scan(
		&iss.ID, &iss.ProjectID, &iss.SequenceID, &iss.Name,
		&description,
		&iss.Priority, &iss.StateID, &iss.ParentID, &iss.SortOrder,
		&iss.StartDate, &iss.TargetDate, &iss.EstimatePointID,
		&iss.IsDraft, &iss.ArchivedAt,
		&iss.CreatedBy, &iss.CreatedAt, &iss.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if description != nil {
		iss.Description = json.RawMessage(description)
	}
	return &iss, nil
}

// queryRower abstracts *pgxpool.Pool and pgx.Tx for the small lookups below.
type queryRower interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// isInvalidUUID reports whether err is Postgres's invalid-text-representation
// for a ::uuid cast — i.e. the client sent a malformed UUID. Callers map it
// to their NotFound sentinel: a malformed id matches nothing.
func isInvalidUUID(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "22P02"
}

// resolveIssueProject resolves the workspace membership and the project in
// one shot: non-members get ErrNotFound (the tenancy boundary — the same
// rule as GetWorkspace), members asking for a missing identifier get
// ErrProjectNotFound.
func resolveIssueProject(ctx context.Context, q queryRower, wsSlug, ident, actorID string) (wsID, projectID string, role int, err error) {
	wsID, role, err = workspaceIDForActor(q.QueryRow(ctx,
		`SELECT w.id::text, m.role
		 FROM workspaces w
		 JOIN workspace_members m ON m.workspace_id = w.id
		 WHERE w.slug = $1 AND m.user_id = $2::uuid`,
		wsSlug, actorID))
	if err != nil {
		return "", "", 0, err
	}
	err = q.QueryRow(ctx,
		`SELECT p.id::text FROM projects p
		 WHERE p.workspace_id = $1::uuid AND p.identifier = $2`,
		wsID, ident).Scan(&projectID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", 0, ErrProjectNotFound
		}
		return "", "", 0, err
	}
	return wsID, projectID, role, nil
}

// checkStateInProject validates that stateID names a state of this project,
// returning its canonical (lowercase) form.
func checkStateInProject(ctx context.Context, q queryRower, projectID, stateID string) (string, error) {
	sid := strings.ToLower(strings.TrimSpace(stateID))
	var owner string
	err := q.QueryRow(ctx,
		`SELECT project_id::text FROM states WHERE id = $1::uuid`, sid).Scan(&owner)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return "", ErrInvalidState
		}
		return "", err
	}
	if owner != projectID {
		return "", ErrInvalidState
	}
	return sid, nil
}

// backlogState returns the id of the project's backlog-group state — the
// default column for new issues. CreateProject always seeds one, so a miss
// is data corruption, surfaced as an internal error.
func backlogState(ctx context.Context, q queryRower, projectID string) (string, error) {
	var id string
	err := q.QueryRow(ctx,
		`SELECT id::text FROM states
		 WHERE project_id = $1::uuid AND "group" = 'backlog'
		 ORDER BY sequence LIMIT 1`, projectID).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("service: project %s has no backlog state", projectID)
		}
		return "", err
	}
	return id, nil
}

// checkParentIssue validates that parentID names a live issue of this project.
func checkParentIssue(ctx context.Context, q queryRower, projectID, parentID string) error {
	var one int
	err := q.QueryRow(ctx,
		`SELECT 1 FROM issues
		 WHERE id = $1::uuid AND project_id = $2::uuid AND deleted_at IS NULL`,
		parentID, projectID).Scan(&one)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return ErrParentNotFound
		}
		return err
	}
	return nil
}

// CreateIssue inserts an issue inside one transaction: the sequence counter
// is incremented atomically (INSERT ... ON CONFLICT DO UPDATE takes the row
// lock, so concurrent creates serialize — gapless, no duplicates — and a
// missing counter row self-heals instead of failing), then the issue
// row and its _created activity row commit together. The caller must be a
// workspace member (15) or admin (20); guests get ErrForbidden, non-members
// ErrNotFound.
func CreateIssue(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string, in CreateIssueInput) (*Issue, error) {
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, ErrNameRequired
	}
	priority := 0
	if in.Priority != nil {
		priority = *in.Priority
		if priority < 0 || priority > 4 {
			return nil, ErrInvalidPriority
		}
	}
	if in.StartDate != nil && in.TargetDate != nil && in.StartDate.After(*in.TargetDate) {
		return nil, ErrInvalidDateRange
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	wsID, projectID, role, err := resolveIssueProject(ctx, tx, wsSlug, ident, actorID)
	if err != nil {
		return nil, err
	}
	if role < RoleMember {
		return nil, ErrForbidden
	}

	var stateID string
	if in.StateID != nil {
		stateID, err = checkStateInProject(ctx, tx, projectID, *in.StateID)
		if err != nil {
			return nil, err
		}
	} else {
		stateID, err = backlogState(ctx, tx, projectID)
		if err != nil {
			return nil, err
		}
	}

	var parentID *string
	if in.ParentID != nil {
		pid := strings.ToLower(strings.TrimSpace(*in.ParentID))
		if pid == "" {
			return nil, ErrInvalidParent
		}
		if err := checkParentIssue(ctx, tx, projectID, pid); err != nil {
			return nil, err
		}
		parentID = &pid
	}

	var estimatePointID *string
	if in.EstimatePointID != nil {
		epid, err := checkEstimatePoint(ctx, tx, projectID, *in.EstimatePointID)
		if err != nil {
			return nil, err
		}
		estimatePointID = &epid
	}

	// Atomic sequence increment, self-healing if the counter row is ever
	// missing (hand-inserted project, failed backfill, a future path
	// bypassing CreateProject). The INSERT ... ON CONFLICT ... DO UPDATE
	// serializes exactly like the bare UPDATE did: when the row exists the
	// arbiter matches and DO UPDATE takes the row lock, so concurrent
	// creates serialize; when it is absent one inserter wins and the
	// losers re-evaluate to the DO UPDATE branch on commit. Either way
	// sequence_ids come out gapless with no duplicates, and a missing row
	// inserts last_value=1 instead of 500ing every create for the project.
	var seq int
	if err := tx.QueryRow(ctx,
		`INSERT INTO issue_sequences (project_id, last_value) VALUES ($1::uuid, 1)
		 ON CONFLICT (project_id) DO UPDATE SET last_value = issue_sequences.last_value + 1
		 RETURNING last_value`,
		projectID).Scan(&seq); err != nil {
		return nil, fmt.Errorf("service: increment issue sequence: %w", err)
	}

	// JSONB params go through an explicit ::jsonb cast with the Go value
	// shipped as text: nil becomes SQL NULL, a string is parsed as JSON.
	// (Passing Go values without the cast lets pgx infer the wrong type.)
	var descParam any
	if in.Description != nil {
		descParam = string(in.Description)
	}
	sortOrder := 0.0
	if in.SortOrder != nil {
		sortOrder = *in.SortOrder
	}

	iss, err := scanIssue(tx.QueryRow(ctx,
		`INSERT INTO issues (project_id, sequence_id, name, description, priority,
			state_id, parent_id, sort_order, start_date, target_date,
			estimate_point_id, is_draft, created_by)
		 VALUES ($1::uuid, $2, $3, $4::jsonb, $5, $6::uuid, $7::uuid, $8, $9, $10,
			$11::uuid, $12, $13::uuid)
		 RETURNING `+issueColumns,
		projectID, seq, name, descParam, priority, stateID, parentID,
		sortOrder, in.StartDate, in.TargetDate, estimatePointID, in.IsDraft, actorID))
	if err != nil {
		return nil, err
	}

	newVal, _ := json.Marshal(map[string]any{"sequence_id": seq, "name": name})
	if _, err := tx.Exec(ctx,
		`INSERT INTO issue_activities (issue_id, actor_id, field, new_value)
		 VALUES ($1::uuid, $2::uuid, '_created', $3::jsonb)`,
		iss.ID, actorID, string(newVal)); err != nil {
		return nil, err
	}

	// DisplayID is derived here so the webhook/Slack payloads below
	// carry it. The v1 version snapshot is NOT taken here: it moves
	// to after the created-rules block below (C49T0), so it captures
	// the post-automation row instead of the pre-rules INSERT row.
	iss.DisplayID = ident + "-" + strconv.Itoa(iss.SequenceID)

	// Intake opt-in (Task 20): land the new issue in the inbox as pending,
	// atomically with the issue itself.
	if in.Intake {
		if err := addIntakeIssueTx(ctx, tx, projectID, iss.ID); err != nil {
			return nil, err
		}
	}

	// C18T1: initial custom values, written atomically with the issue.
	// Required enforcement uses full-create semantics: every required
	// field must arrive non-empty in the request. Drafts skip the
	// required check (unfinished by definition) but still get their
	// values type-checked and stored.
	if !in.IsDraft {
		required, err := requiredCustomFields(ctx, tx, projectID)
		if err != nil {
			return nil, err
		}
		if missing := checkRequiredCustomValues(required, in.CustomValues, nil, true); len(missing) > 0 {
			return nil, &CustomFieldRequiredError{FieldNames: missing}
		}
	}
	if len(in.CustomValues) > 0 {
		if err := writeCustomValuesTx(ctx, tx, projectID, iss.ID, in.CustomValues); err != nil {
			return nil, err
		}
	}

	// Fan the creation out to webhooks as issue.created. No in-app
	// notification on create (matrix, Task 26): assignees are notified when
	// assigned, watchers on the events that follow.
	if err := enqueueWebhookDeliveryTx(ctx, tx, wsID, EventIssueCreated, map[string]any{
		"id":         iss.ID,
		"display_id": iss.DisplayID,
		"name":       name,
		"actor_id":   actorID,
	}); err != nil {
		return nil, err
	}

	// Slack (C9T3): one compact message per created issue, enqueued
	// in-tx — never inline, so a down Slack endpoint cannot fail the
	// mutation.
	if err := enqueueSlackDeliveryTx(ctx, tx, wsID,
		slackIssueCreatedText(iss.DisplayID, name, actorDisplayName(ctx, tx, actorID))); err != nil {
		return nil, err
	}

	// C12T2: issue.created workflow automations. Matching rules run
	// their actions in this same tx — creation, automation writes, and
	// run rows commit atomically. The depth-1 loop guard
	// (!automationActive) means automation-driven changes never
	// re-trigger evaluation, so an automation's own set_state on this
	// path produces no cascade.
	var notified []*Notification
	if !automationActive(ctx) {
		notified = runAutomationCreatedRulesTx(ctx, tx, wsID, projectID, ident, iss.ID, actorID)
	}

	// C49T0: Version 1 snapshots the POST-automation row. The created
	// rules above may have mutated the row (set_state, set_priority,
	// ...), so the INSERT-time iss is stale. Re-read the row in-tx
	// (same tx, still under the row lock — do NOT reuse the stale iss,
	// do NOT snapshot post-commit) and snapshot THAT row, so restoring
	// v1 reproduces the post-rules state instead of silently reverting
	// the automation move.
	freshInTx, err := scanIssue(tx.QueryRow(ctx,
		`SELECT `+issueColumns+` FROM issues
		 WHERE id = $1::uuid AND project_id = $2::uuid AND deleted_at IS NULL
		 FOR UPDATE`,
		iss.ID, projectID))
	if err != nil {
		return nil, err
	}
	freshInTx.DisplayID = ident + "-" + strconv.Itoa(freshInTx.SequenceID)
	if err := snapshotVersionTx(ctx, tx, iss.ID, actorID, freshInTx); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	// C48T1: post-created-rules fresh snapshot. The created-rules above
	// run in the same tx as the INSERT, so the INSERT-time iss is stale
	// whenever a rule mutated the row (set_state, set_priority, ...).
	// Re-read the row post-commit (C45/C46 pattern: deleted_at IS NULL +
	// project filter) and use the fresh row for BOTH the announce and
	// the returned value. Best-effort: on lookup failure fall back to
	// the pre-rules iss, never fail the create. scanIssue carries no
	// derived DisplayID, so re-derive it from the fresh SequenceID.
	if fresh, err := scanIssue(pool.QueryRow(ctx,
		`SELECT `+issueColumns+` FROM issues
		  WHERE id = $1::uuid AND project_id = $2::uuid AND deleted_at IS NULL`,
		iss.ID, projectID)); err == nil {
		fresh.DisplayID = ident + "-" + strconv.Itoa(fresh.SequenceID)
		iss = fresh
	}
	announceIssueCreated(wsSlug, ident, iss)
	announceNotifications(notified)
	return iss, nil
}

// GetIssue returns one live issue by UUID, with its assignees and labels
// aggregated in the same query. Any workspace member (guest 5+) may read;
// non-members get ErrNotFound, a missing UUID ErrIssueNotFound.
func GetIssue(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, issueID, actorID string) (*IssueListItem, error) {
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return nil, err
	}
	_, projectID, _, err := resolveIssueProject(ctx, pool, wsSlug, ident, actorID)
	if err != nil {
		return nil, err
	}

	item, err := getIssueRow(ctx, pool, ident, projectID, issueID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return nil, ErrIssueNotFound
		}
		return nil, err
	}
	return item, nil
}

// getIssueRow loads one live issue by (project, UUID) with assignees and
// labels aggregated in the same query. ident is the project's normalized
// identifier, used only to derive DisplayID. Callers own the tenancy check;
// a missing row surfaces as pgx.ErrNoRows for the caller to map.
func getIssueRow(ctx context.Context, q queryRower, ident, projectID, issueID string) (*IssueListItem, error) {
	var item IssueListItem
	var description []byte
	var assigneesJSON, labelsJSON, progressJSON, refsJSON, epicsJSON []byte
	var enteredAt *time.Time
	err := q.QueryRow(ctx,
		`SELECT `+issueColumns+`, `+assigneesAgg+`, `+labelsAgg+`, `+subtaskProgressAgg+`, `+externalRefsAgg+`, `+epicsAgg+`, `+stateEnteredAtAgg+` FROM issues i
		 WHERE i.id = $1::uuid AND i.project_id = $2::uuid AND i.deleted_at IS NULL`,
		issueID, projectID).Scan(
		&item.ID, &item.ProjectID, &item.SequenceID, &item.Name,
		&description,
		&item.Priority, &item.StateID, &item.ParentID, &item.SortOrder,
		&item.StartDate, &item.TargetDate, &item.EstimatePointID,
		&item.IsDraft, &item.ArchivedAt,
		&item.CreatedBy, &item.CreatedAt, &item.UpdatedAt,
		&assigneesJSON, &labelsJSON, &progressJSON, &refsJSON, &epicsJSON,
		&enteredAt,
	)
	if err != nil {
		return nil, err
	}
	// C70T0: time-in-state is derived at read time inside the same
	// query (the correlated stateEnteredAtAgg above) — never stored.
	populateTimeInState(&item.Issue, enteredAt, time.Now())
	if description != nil {
		item.Description = json.RawMessage(description)
	}
	if err := unmarshalRelations(assigneesJSON, labelsJSON, &item); err != nil {
		return nil, err
	}
	if err := unmarshalSubtaskProgress(progressJSON, &item); err != nil {
		return nil, err
	}
	if err := unmarshalExternalRefs(refsJSON, &item); err != nil {
		return nil, err
	}
	if err := unmarshalEpicRefs(epicsJSON, &item); err != nil {
		return nil, err
	}
	item.DisplayID = ident + "-" + strconv.Itoa(item.SequenceID)
	return &item, nil
}

// toJSONBParam renders a Go value as a JSONB query parameter for the
// explicit ::jsonb casts used below: nil becomes SQL NULL, anything else
// its JSON encoding. Callers must dereference pointers first — an
// interface holding a typed nil pointer is not == nil.
func toJSONBParam(v any) any {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return string(b)
}

// derefPtr converts a nil-able pointer to an any holding nil or the value,
// so JSONB activity columns record SQL NULL (not JSON null) for absent
// values.
func derefPtr[T any](p *T) any {
	if p == nil {
		return nil
	}
	return *p
}

func strPtrEq(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// datePtrEq compares DATE values by calendar day — the time-of-day is
// always midnight UTC for DATE columns, but the comparison stays explicit.
func datePtrEq(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Format("2006-01-02") == b.Format("2006-01-02")
}

// UpdateIssue applies a partial update inside one transaction: the row is
// locked and read first, each set field is compared before/after, and only
// actual changes are written — with one issue_activities row per changed
// field (field, old_value, new_value). A patch that sets fields but changes
// nothing returns the current row with no activity rows; a patch with no
// fields at all is ErrNothingToUpdate. Member (15)+; guests ErrForbidden.
func UpdateIssue(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, issueID, actorID string, patch IssuePatch) (*Issue, error) {
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return nil, err
	}
	if err := validateIssuePatch(patch); err != nil {
		return nil, err
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	_, projectID, role, err := resolveIssueProject(ctx, tx, wsSlug, ident, actorID)
	if err != nil {
		return nil, err
	}
	if role < RoleMember {
		return nil, ErrForbidden
	}

	iss, notified, stateChanged, err := updateIssueTx(ctx, tx, projectID, ident, issueID, actorID, patch, true)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	announceIssueUpdated(wsSlug, ident, issueID, iss)
	announceNotifications(notified)
	// C44T1: a linked issue's state change moves KR progress — fan out
	// objective.updated for its linked objectives. Title-only (and
	// other non-state) edits report stateChanged=false: no event.
	if stateChanged {
		announceObjectivesForIssue(ctx, pool, wsSlug, issueID)
	}
	return iss, nil
}

// validateIssuePatch rejects patches with no fields at all, blank names,
// or out-of-range priorities before any DB work runs.
func validateIssuePatch(patch IssuePatch) error {
	if !patch.hasFields() {
		return ErrNothingToUpdate
	}
	if patch.Name != nil && strings.TrimSpace(*patch.Name) == "" {
		return ErrNameRequired
	}
	if patch.Priority != nil && (*patch.Priority < 0 || *patch.Priority > 4) {
		return ErrInvalidPriority
	}
	return nil
}

// updateIssueTx is the tx-scoped core of UpdateIssue. It neither begins nor
// commits: callers own the transaction (UpdateIssue for single updates,
// BulkUpdateIssues wraps each item in a savepoint). Role checks stay with
// the callers. It returns the updated issue plus the in-app notifications
// created (state changes notify watchers); callers broadcast them after
// commit via announceNotifications. The third return reports whether
// state_id actually changed in the final in-tx row — callers gate
// their objective.updated fan-out on it (C44T1).
//
// enforceWorkflow gates the C68T0 transition-rules check: user-initiated
// callers (UpdateIssue, BulkUpdateIssues, BulkSetIssues) pass true;
// system callers (intake accept, version restore) pass false — rules
// govern human workflow movement, never system integrity.
func updateIssueTx(ctx context.Context, tx pgx.Tx, projectID, ident, issueID, actorID string, patch IssuePatch, enforceWorkflow bool) (*Issue, []*Notification, bool, error) {
	// Lock the row and read the before-image for per-field comparison.
	old, err := scanIssue(tx.QueryRow(ctx,
		`SELECT `+issueColumns+` FROM issues
		 WHERE id = $1::uuid AND project_id = $2::uuid AND deleted_at IS NULL
		 FOR UPDATE`,
		issueID, projectID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return nil, nil, false, ErrIssueNotFound
		}
		return nil, nil, false, err
	}

	// Validate references before building the SET clause.
	var stateID *string
	if patch.StateID != nil {
		sid, err := checkStateInProject(ctx, tx, projectID, *patch.StateID)
		if err != nil {
			return nil, nil, false, err
		}
		stateID = &sid
	}
	// C16T3: blocker guard — moving into a completed state while open
	// 'blocks' inbound edges point at the issue is rejected, unless the
	// request carries ignore_blockers. Lives in the shared tx core, so it
	// covers single PATCH, bulk-update, and bulk-set alike.
	if stateID != nil && *stateID != old.StateID && !patch.IgnoreBlockers {
		if err := assertNoOpenBlockersTx(ctx, tx, projectID, ident, issueID, *stateID); err != nil {
			return nil, nil, false, err
		}
	}
	// C68T0: workflow transition governance — when the project defines
	// any transition rules, a user-initiated state move must be a listed
	// (old → new) pair. Checked before any mutation: a denial returns
	// ErrStateTransitionNotAllowed with no writes, so the caller commits
	// nothing (no activity row, no announce, no webhook). System callers
	// pass enforceWorkflow=false (see the doc comment above).
	if enforceWorkflow && stateID != nil && *stateID != old.StateID {
		if err := checkTransitionTx(ctx, tx, projectID, old.StateID, *stateID); err != nil {
			return nil, nil, false, err
		}
	}
	var parentID PatchField[string]
	if patch.ParentID.Set {
		if patch.ParentID.Value != nil {
			pid := strings.ToLower(strings.TrimSpace(*patch.ParentID.Value))
			if pid == "" || pid == old.ID {
				return nil, nil, false, ErrInvalidParent
			}
			if err := checkParentIssue(ctx, tx, projectID, pid); err != nil {
				return nil, nil, false, err
			}
			// C5T4: PATCH must not bypass the SetParent cycle guard — a
			// re-parent under one of the issue's own descendants would
			// otherwise close a cycle the routes could never create.
			if err := checkParentCycle(ctx, tx, old.ID, pid); err != nil {
				return nil, nil, false, err
			}
			parentID = PatchField[string]{Set: true, Value: &pid}
		} else {
			parentID = PatchField[string]{Set: true}
		}
	}

	var estimatePointID PatchField[string]
	if patch.EstimatePointID.Set {
		if patch.EstimatePointID.Value != nil {
			epid, err := checkEstimatePoint(ctx, tx, projectID, *patch.EstimatePointID.Value)
			if err != nil {
				return nil, nil, false, err
			}
			estimatePointID = PatchField[string]{Set: true, Value: &epid}
		} else {
			estimatePointID = PatchField[string]{Set: true}
		}
	}

	// C19T3: release assignment — validate the release belongs to the
	// project before building the SET clause (resolveRelease: 400 on a
	// malformed id, 404 when it matches nothing in this project).
	var releaseID PatchField[string]
	if patch.ReleaseID.Set {
		if patch.ReleaseID.Value != nil {
			rid := strings.ToLower(strings.TrimSpace(*patch.ReleaseID.Value))
			if _, err := resolveRelease(ctx, tx, projectID, rid); err != nil {
				return nil, nil, false, err
			}
			releaseID = PatchField[string]{Set: true, Value: &rid}
		} else {
			releaseID = PatchField[string]{Set: true}
		}
	}

	// Effective dates for the range check (patch wins over the old row).
	effStart := old.StartDate
	if patch.StartDate.Set {
		effStart = patch.StartDate.Value
	}
	effTarget := old.TargetDate
	if patch.TargetDate.Set {
		effTarget = patch.TargetDate.Value
	}
	if effStart != nil && effTarget != nil && effStart.After(*effTarget) {
		return nil, nil, false, ErrInvalidDateRange
	}

	// Build the SET clause from changed fields only. args[0]/args[1] are
	// reserved for issueID/projectID in the WHERE clause, hence the +2
	// offset on the placeholder indices (same trick as UpdateProject).
	// cast is appended to the placeholder (e.g. "::jsonb") — it must sit
	// on the VALUE, never on the SET target (SET col::type = ... is a
	// syntax error).
	var sets []string
	var args []any
	type activity struct {
		field          string
		oldVal, newVal any
	}
	var activities []activity
	add := func(field, column, cast string, arg any, oldVal, newVal any) {
		args = append(args, arg)
		sets = append(sets, fmt.Sprintf("%s = $%d%s", column, len(args)+2, cast))
		activities = append(activities, activity{field, oldVal, newVal})
	}

	if patch.Name != nil {
		name := strings.TrimSpace(*patch.Name)
		if name != old.Name {
			add("name", "name", "", name, old.Name, name)
		}
	}
	if patch.Description.Set {
		var newRaw json.RawMessage
		if patch.Description.Value != nil {
			newRaw = *patch.Description.Value
		}
		if !bytes.Equal(old.Description, newRaw) {
			var arg, newJSON any
			if newRaw != nil {
				arg = string(newRaw)
				newJSON = newRaw
			}
			var oldJSON any
			if old.Description != nil {
				oldJSON = old.Description
			}
			add("description", "description", "::jsonb", arg, oldJSON, newJSON)
		}
	}
	if patch.Priority != nil && *patch.Priority != old.Priority {
		add("priority", "priority", "", *patch.Priority, old.Priority, *patch.Priority)
	}
	if stateID != nil && *stateID != old.StateID {
		add("state_id", "state_id", "::uuid", *stateID, old.StateID, *stateID)
	}
	if parentID.Set && !strPtrEq(old.ParentID, parentID.Value) {
		add("parent_id", "parent_id", "::uuid", parentID.Value, derefPtr(old.ParentID), derefPtr(parentID.Value))
	}
	if patch.SortOrder != nil && *patch.SortOrder != old.SortOrder {
		add("sort_order", "sort_order", "", *patch.SortOrder, old.SortOrder, *patch.SortOrder)
	}
	if patch.StartDate.Set && !datePtrEq(old.StartDate, patch.StartDate.Value) {
		add("start_date", "start_date", "", patch.StartDate.Value, derefPtr(old.StartDate), derefPtr(patch.StartDate.Value))
	}
	if patch.TargetDate.Set && !datePtrEq(old.TargetDate, patch.TargetDate.Value) {
		add("target_date", "target_date", "", patch.TargetDate.Value, derefPtr(old.TargetDate), derefPtr(patch.TargetDate.Value))
	}
	if estimatePointID.Set && !strPtrEq(old.EstimatePointID, estimatePointID.Value) {
		add("estimate_point_id", "estimate_point_id", "::uuid", estimatePointID.Value,
			derefPtr(old.EstimatePointID), derefPtr(estimatePointID.Value))
	}
	// C19T3: release assignment. The before-image needs the old release_id,
	// which the Issue struct does not carry — read it under the same row
	// lock (FOR UPDATE above) so the change check is race-free.
	if releaseID.Set {
		var oldReleaseID *string
		if err := tx.QueryRow(ctx,
			`SELECT release_id::text FROM issues WHERE id = $1::uuid`,
			issueID).Scan(&oldReleaseID); err != nil {
			return nil, nil, false, err
		}
		if !strPtrEq(oldReleaseID, releaseID.Value) {
			add("release_id", "release_id", "::uuid", releaseID.Value,
				derefPtr(oldReleaseID), derefPtr(releaseID.Value))
		}
	}
	if patch.IsDraft != nil && *patch.IsDraft != old.IsDraft {
		add("is_draft", "is_draft", "", *patch.IsDraft, old.IsDraft, *patch.IsDraft)
	}
	if patch.Archived != nil {
		isArchived := old.ArchivedAt != nil
		if *patch.Archived != isArchived {
			if *patch.Archived {
				sets = append(sets, "archived_at = now()")
			} else {
				sets = append(sets, "archived_at = NULL")
			}
			activities = append(activities, activity{"archived", isArchived, *patch.Archived})
		}
	}

	if len(sets) == 0 {
		// Fields were sent but nothing actually changed: return the
		// current row, no activity rows. (A patch with no fields at all
		// was already rejected by validateIssuePatch with
		// ErrNothingToUpdate.) The caller commits.
		old.DisplayID = ident + "-" + strconv.Itoa(old.SequenceID)
		return old, nil, false, nil
	}

	updated, err := scanIssue(tx.QueryRow(ctx,
		`UPDATE issues SET `+strings.Join(sets, ", ")+`, updated_at = now()
		 WHERE id = $1::uuid AND project_id = $2::uuid AND deleted_at IS NULL
		 RETURNING `+issueColumns,
		append([]any{issueID, projectID}, args...)...))
	if err != nil {
		return nil, nil, false, err
	}

	for _, a := range activities {
		if _, err := tx.Exec(ctx,
			`INSERT INTO issue_activities (issue_id, actor_id, field, old_value, new_value)
			 VALUES ($1::uuid, $2::uuid, $3, $4::jsonb, $5::jsonb)`,
			updated.ID, actorID, a.field, toJSONBParam(a.oldVal), toJSONBParam(a.newVal)); err != nil {
			return nil, nil, false, err
		}
	}

	updated.DisplayID = ident + "-" + strconv.Itoa(updated.SequenceID)

	// State changes notify the issue's watchers (subscribers + assignees),
	// minus the actor. Every real field change also fans out to webhooks
	// as issue.updated.
	var notified []*Notification
	stateChanged := false
	var changedFields []string
	for _, a := range activities {
		changedFields = append(changedFields, a.field)
		if a.field == "state_id" {
			stateChanged = true
		}
	}
	// Hoisted for the Slack fan-out below: the state name and actor
	// display name are only known when the state actually changed.
	var stateName, actorName string
	if stateChanged {
		watchers, err := issueWatchersTx(ctx, tx, issueID)
		if err != nil {
			return nil, nil, false, err
		}
		if err := tx.QueryRow(ctx,
			`SELECT name FROM states WHERE id = $1::uuid`, updated.StateID).Scan(&stateName); err != nil {
			return nil, nil, false, err
		}
		actorName = actorDisplayName(ctx, tx, actorID)
		notified, err = notifyTx(ctx, tx, NotifyStateChanged,
			fmt.Sprintf("%s moved %s to %s", actorName, updated.DisplayID, stateName),
			fmt.Sprintf("Issue: %s", updated.Name),
			map[string]any{
				"issue_id":   updated.ID,
				"display_id": updated.DisplayID,
				"issue_name": updated.Name,
				"state_id":   updated.StateID,
				"state_name": stateName,
				"actor_id":   actorID,
				"project_id": projectID,
			},
			actorID, watchers)
		if err != nil {
			return nil, nil, false, err
		}
	}
	wsID, err := workspaceIDForProjectTx(ctx, tx, projectID)
	if err != nil {
		return nil, nil, false, err
	}
	if stateChanged {
		// Slack (C9T3): one compact message per state change, enqueued
		// in-tx like the generic webhook fan-out below — never inline.
		if err := enqueueSlackDeliveryTx(ctx, tx, wsID,
			slackStateChangedText(updated.DisplayID, updated.Name, stateName, actorName)); err != nil {
			return nil, nil, false, err
		}
		// C11T1: workflow automations. Matching rules run their actions
		// in this same tx; a failing action is logged, never fatal, and
		// the depth-1 loop guard (!automationActive) means automation
		// writes can never re-trigger evaluation. old.StateID is the
		// before-image (stateChanged implies it differs from new).
		if !automationActive(ctx) {
			notified = append(notified, runAutomationRulesTx(ctx, tx, wsID,
				projectID, ident, issueID, actorID, old.StateID, updated.StateID)...)
		}
	}
	// C16T0: field-level automation events. The activities slice holds
	// the before/after image of every real change, so each event fires
	// exactly when its field changed — evaluated in this same tx, so a
	// rolled-back write can never fire a rule. The priority activity
	// always carries ints (see the add call above).
	if !automationActive(ctx) {
		for _, a := range activities {
			switch a.field {
			case "priority":
				notified = append(notified, runAutomationPriorityChangedTx(ctx, tx, wsID,
					projectID, ident, issueID, actorID, a.oldVal.(int), a.newVal.(int))...)
			case "target_date":
				notified = append(notified, runAutomationDueDateChangedTx(ctx, tx, wsID,
					projectID, ident, issueID, actorID)...)
			case "estimate_point_id":
				notified = append(notified, runAutomationEstimateChangedTx(ctx, tx, wsID,
					projectID, ident, issueID, actorID)...)
			}
		}
	}
	// C49T1: snapshot + return the POST-automation row. Re-read the
	// full row in-tx AFTER all automation blocks above: a cascade
	// rule (e.g. priority_changed → set_state) can move the issue
	// after `updated` was scanned, and only the final in-tx row is
	// what commits. The snapshot replaces the old pre-automation one
	// — one version per user action, not two — and the fresh row is
	// reused for the C44T1 stateChanged computation (one fewer round
	// trip than the old standalone state_id re-read). A title-only
	// edit (or a net-zero cascade) still reports false.
	// Notification/Slack text above deliberately stays on the
	// user-patch `updated` row.
	fresh, err := scanIssue(tx.QueryRow(ctx,
		`SELECT `+issueColumns+` FROM issues
		 WHERE id = $1::uuid AND project_id = $2::uuid AND deleted_at IS NULL`,
		updated.ID, projectID))
	if err != nil {
		return nil, nil, false, err
	}
	fresh.DisplayID = ident + "-" + strconv.Itoa(fresh.SequenceID)
	// C56T3: the single issue.updated webhook enqueue, AFTER the
	// fresh re-read above (it used to sit before it). changed_fields
	// is the union of the user-patch activities fields (activities
	// first, so the direct change leads) and the automation-cascade
	// row diff (old before-image vs fresh post-automation row via the
	// shared issueRowChangedFields helper) — so a priority_changed →
	// set_state cascade surfaces BOTH priority and state_id. The
	// union dedupes: a field the patch already changed appears once.
	// Still exactly one enqueue per updateIssueTx call, still in-tx.
	changedFields = unionChangedFields(changedFields, issueRowChangedFields(old, fresh))
	if err := enqueueWebhookDeliveryTx(ctx, tx, wsID, EventIssueUpdated, map[string]any{
		"id":             updated.ID,
		"display_id":     updated.DisplayID,
		"changed_fields": changedFields,
		"actor_id":       actorID,
	}); err != nil {
		return nil, nil, false, err
	}
	// Append-only version snapshot of the post-automation row. Rides
	// along inside the caller's tx/savepoint, so BulkUpdateIssues gets
	// per-item versions for free.
	// C64T1 audit: this site cannot emit a duplicate-content snapshot —
	// the len(sets)==0 early return above exits before any write when
	// the patch changed nothing, so every snapshot here follows a real
	// UPDATE (a net-zero automation cascade still went through real
	// writes with activity rows; it is not a phantom).
	if err := snapshotVersionTx(ctx, tx, fresh.ID, actorID, fresh); err != nil {
		return nil, nil, false, err
	}
	return fresh, notified, fresh.StateID != old.StateID, nil
}

// DeleteIssue soft-deletes the issue (sets deleted_at) and writes a
// _deleted activity row, atomically. Reads exclude soft-deleted rows, so a
// deleted issue reads as ErrIssueNotFound — including a second DELETE.
// Member (15)+; guests ErrForbidden.
func DeleteIssue(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, issueID, actorID string) error {
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return err
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	_, projectID, role, err := resolveIssueProject(ctx, tx, wsSlug, ident, actorID)
	if err != nil {
		return err
	}
	if role < RoleMember {
		return ErrForbidden
	}

	detached, err := deleteIssueTx(ctx, tx, projectID, ident, issueID, actorID)
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	announceIssueDeleted(wsSlug, ident, issueID)
	// C44T1: a deleted issue drops out of KR progress — fan out
	// objective.updated for its (still-linked) objectives.
	announceObjectivesForIssue(ctx, pool, wsSlug, issueID)
	// C51T1: detached children changed parent_id, so each announces
	// issue.updated — no objective fan-out (parent_id does not affect
	// KR progress, state unchanged).
	announceDetachedChildren(ctx, pool, wsSlug, ident, projectID, detached)
	return nil
}

// RestoreIssue un-deletes a soft-deleted issue (sets deleted_at back to
// NULL) and writes the restore trail, atomically. It is the mirror of
// DeleteIssue: a live issue restores as ErrIssueNotFound ("not in trash"),
// exactly as a second DELETE reads as not-found. Member (15)+; guests
// ErrForbidden.
func RestoreIssue(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, issueID, actorID string) (*Issue, error) {
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return nil, err
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	_, projectID, role, err := resolveIssueProject(ctx, tx, wsSlug, ident, actorID)
	if err != nil {
		return nil, err
	}
	if role < RoleMember {
		return nil, ErrForbidden
	}

	restored, err := restoreIssueTx(ctx, tx, projectID, ident, issueID, actorID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	// C45/C46 pattern: re-read post-commit (deleted_at IS NULL) for the
	// announce, and skip on a vanishing row rather than failing the
	// (already committed) restore.
	if fresh, err := scanIssue(pool.QueryRow(ctx,
		`SELECT `+issueColumns+` FROM issues
		  WHERE id = $1::uuid AND project_id = $2::uuid AND deleted_at IS NULL`,
		restored.ID, projectID)); err == nil {
		fresh.DisplayID = ident + "-" + strconv.Itoa(fresh.SequenceID)
		announceIssueUpdated(wsSlug, ident, fresh.ID, fresh)
	}
	// C44T1 mirror: a restored issue re-enters KR progress — fan out
	// objective.updated for its (still-linked) objectives.
	announceObjectivesForIssue(ctx, pool, wsSlug, restored.ID)
	return restored, nil
}

// restoreIssueTx is the tx-scoped core of RestoreIssue: clear deleted_at
// plus the restore trail (parent re-validation, _restored activity row,
// version snapshot, issue.updated webhook). It returns the restored row
// (DisplayID derived, ParentID reflecting any parent clearing). It
// neither begins nor commits — callers own the transaction. Role checks
// stay with the callers. Written as a tx-scoped helper (like
// deleteIssueTx) so BulkRestoreIssues (C52T1) can reuse it per item
// inside its savepoint loop.
func restoreIssueTx(ctx context.Context, tx pgx.Tx, projectID, ident, issueID, actorID string) (*Issue, error) {
	// The UPDATE itself locks the row: a concurrent delete/restore of
	// the same issue serializes here — one wins, the other sees no row
	// (its deleted_at predicate no longer matches) and gets
	// ErrIssueNotFound.
	restored, err := scanIssue(tx.QueryRow(ctx,
		`UPDATE issues SET deleted_at = NULL, updated_at = now()
		 WHERE id = $1::uuid AND project_id = $2::uuid AND deleted_at IS NOT NULL
		 RETURNING `+issueColumns,
		issueID, projectID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return nil, ErrIssueNotFound
		}
		return nil, err
	}

	// State re-validation (RestoreIssueVersion precedent): the issue's
	// state must still exist in the project. If it was deleted since the
	// delete, reject the restore — do NOT silently move the issue to
	// another state; the caller re-creates or re-states explicitly.
	// In practice DeleteState forces a reassign of every referencing
	// issue (live or soft-deleted) before the state row can go, and the
	// issues.state_id FK blocks a raw state delete, so this is
	// defense-in-depth against a corrupt/orphaned state reference.
	// A failure here rolls the tx back: deleted_at stays set.
	if _, err := checkStateInProject(ctx, tx, projectID, restored.StateID); err != nil {
		return nil, err
	}

	// Parent re-validation: if parent_id is set, the parent must be a
	// live issue of this project. A deleted/missing parent is cleared
	// in-tx with a parent_id activity row (old = stale parent, new =
	// SQL NULL — same shape as C51T1's detach row); a live parent is
	// kept. No cycle check: the parent link predates the delete and was
	// valid when set, and nothing re-parents the restored issue, so no
	// new cycle can form here.
	parentCleared := false
	if restored.ParentID != nil {
		var one int
		err := tx.QueryRow(ctx,
			`SELECT 1 FROM issues
			 WHERE id = $1::uuid AND project_id = $2::uuid AND deleted_at IS NULL`,
			*restored.ParentID, projectID).Scan(&one)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) && !isInvalidUUID(err) {
			return nil, err
		}
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			staleParent := *restored.ParentID
			if _, err := tx.Exec(ctx,
				`UPDATE issues SET parent_id = NULL, updated_at = now()
				 WHERE id = $1::uuid`,
				restored.ID); err != nil {
				return nil, err
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO issue_activities (issue_id, actor_id, field, old_value, new_value)
				 VALUES ($1::uuid, $2::uuid, 'parent_id', $3::jsonb, $4::jsonb)`,
				restored.ID, actorID, toJSONBParam(derefPtr(&staleParent)), toJSONBParam(derefPtr((*string)(nil)))); err != nil {
				return nil, err
			}
			restored.ParentID = nil
			parentCleared = true
		}
	}

	// Trail, mirroring delete (C51T1 shape): a _restored activity row.
	// NOTE: RestoreIssueVersion already writes a _restored row with a
	// {"version_no": n} payload for version restores; this trash
	// restore reuses the same field with a {"restored_at": ...}
	// payload (the _deleted row's shape). Consumers distinguishing
	// the two restores must key on the payload, not the field alone.
	newVal, _ := json.Marshal(map[string]any{"restored_at": time.Now().UTC()})
	if _, err := tx.Exec(ctx,
		`INSERT INTO issue_activities (issue_id, actor_id, field, new_value)
		 VALUES ($1::uuid, $2::uuid, '_restored', $3::jsonb)`,
		restored.ID, actorID, string(newVal)); err != nil {
		return nil, err
	}

	restored.DisplayID = ident + "-" + strconv.Itoa(restored.SequenceID)
	// C64T1: no-op snapshot guard (same duplicate class as the C64T0
	// no-op version restore). deleteIssueTx snapshots the row at delete
	// time, and deleted_at is NOT in the Issue struct, so a restore
	// that clears no parent and changed nothing else would snapshot
	// content identical to the issue's latest version — a phantom
	// version for an event whose trail is already recorded by the
	// _restored activity row and the issue.updated webhook below. Skip
	// the snapshot when the restored row's content equals the latest
	// version. The parent-clear path above changed parent_id (and a
	// DeleteState reassign while deleted changes state_id), so those
	// restores still snapshot.
	dup, err := latestVersionContentEqualTx(ctx, tx, restored.ID, restored)
	if err != nil {
		return nil, err
	}
	if !dup {
		if err := snapshotVersionTx(ctx, tx, restored.ID, actorID, restored); err != nil {
			return nil, err
		}
	}

	// Webhook fan-out as issue.updated: the restored issue re-enters
	// every consumer's working set. changed_fields carries deleted_at
	// (the field a consumer needs to invalidate a cached deleted view)
	// plus parent_id when the parent was cleared above.
	wsID, err := workspaceIDForProjectTx(ctx, tx, projectID)
	if err != nil {
		return nil, err
	}
	changedFields := []string{"deleted_at"}
	if parentCleared {
		changedFields = append(changedFields, "parent_id")
	}
	if err := enqueueWebhookDeliveryTx(ctx, tx, wsID, EventIssueUpdated, map[string]any{
		"id":             restored.ID,
		"display_id":     restored.DisplayID,
		"changed_fields": changedFields,
		"actor_id":       actorID,
	}); err != nil {
		return nil, err
	}
	return restored, nil
}

// rebalanceSpacing is the even gap RebalanceSortOrder leaves between
// consecutive issues in a state. It must match the client-side
// SORT_ORDER_STEP in web/src/lib/sortOrder.ts.
const rebalanceSpacing = 1024

// RebalanceSortOrder re-spaces the sort_order values of all live issues in
// one state to even multiples of rebalanceSpacing (1024, 2048, ...),
// preserving their current relative order (sort_order, then sequence_id).
// It is the board's density escape hatch: when repeated midpoint drops
// collapse the gap between two neighbors below the client's threshold, the
// client calls this, refetches, and retries the drop. Member (15)+.
// Returns the number of issues respaced.
//
// updated_at is deliberately left untouched: rebalance is a maintenance
// op and the caller refetches the board explicitly afterwards, so it
// stays out of the delta-sync stream.
func RebalanceSortOrder(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, stateID string) (int, error) {
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return 0, err
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	_, projectID, role, err := resolveIssueProject(ctx, tx, wsSlug, ident, actorID)
	if err != nil {
		return 0, err
	}
	if role < RoleMember {
		return 0, ErrForbidden
	}

	sid, err := checkStateInProject(ctx, tx, projectID, stateID)
	if err != nil {
		return 0, err
	}

	// FOR UPDATE serializes concurrent rebalances and racing midpoint
	// drops on the same state's rows.
	rows, err := tx.Query(ctx,
		`SELECT id::text FROM issues
		  WHERE project_id = $1::uuid AND state_id = $2::uuid AND deleted_at IS NULL
		  ORDER BY sort_order, sequence_id
		  FOR UPDATE`,
		projectID, sid)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	for i, id := range ids {
		if _, err := tx.Exec(ctx,
			`UPDATE issues SET sort_order = $1 WHERE id = $2::uuid`,
			float64(i+1)*rebalanceSpacing, id); err != nil {
			return 0, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return len(ids), nil
}

// deleteIssueTx is the tx-scoped core of DeleteIssue: soft-delete plus the
// _deleted activity row plus a version snapshot of the deleted state,
// plus webhook fan-out (issue.deleted for the deleted issue,
// issue.updated per detached child — SetParent/Convert parity). It
// returns the ids of the live children detached by the delete, so callers
// can announce them after commit. On any error it returns nil ids —
// callers announce nothing for a rolled-back transaction. It neither
// begins nor commits — callers own the transaction. Role checks stay
// with the callers.
func deleteIssueTx(ctx context.Context, tx pgx.Tx, projectID, ident, issueID, actorID string) ([]string, error) {
	del, err := scanIssue(tx.QueryRow(ctx,
		`UPDATE issues SET deleted_at = now(), updated_at = now()
		 WHERE id = $1::uuid AND project_id = $2::uuid AND deleted_at IS NULL
		 RETURNING `+issueColumns,
		issueID, projectID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return nil, ErrIssueNotFound
		}
		return nil, err
	}

	// C5T4: soft deletes never fire the FK's ON DELETE SET NULL, so detach
	// the deleted issue's children explicitly — a deleted parent leaves
	// its children as top-level issues (their own deleted_at rows are
	// untouched; only live children are detached).
	// C51T1: RETURNING the detached rows so each one gets its trail in
	// the same tx — a parent_id activity row (old = deleted parent,
	// new = NULL, same shape as Convert's parent_id row) + a version
	// snapshot of the post-detach row, actor = the deleter.
	rows, err := tx.Query(ctx,
		`UPDATE issues SET parent_id = NULL, updated_at = now()
		 WHERE parent_id = $1::uuid AND deleted_at IS NULL
		 RETURNING `+issueColumns,
		del.ID)
	if err != nil {
		return nil, err
	}
	var detached []*Issue
	for rows.Next() {
		child, err := scanIssue(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		detached = append(detached, child)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// C51T1 webhook parity: the per-child fan-out below and the
	// issue.deleted fan-out share one workspace resolution.
	wsID, err := workspaceIDForProjectTx(ctx, tx, projectID)
	if err != nil {
		return nil, err
	}
	detachedIDs := make([]string, 0, len(detached))
	for _, child := range detached {
		child.DisplayID = ident + "-" + strconv.Itoa(child.SequenceID)
		if _, err := tx.Exec(ctx,
			`INSERT INTO issue_activities (issue_id, actor_id, field, old_value, new_value)
			 VALUES ($1::uuid, $2::uuid, 'parent_id', $3::jsonb, $4::jsonb)`,
			child.ID, actorID, toJSONBParam(derefPtr(&del.ID)), toJSONBParam(derefPtr((*string)(nil)))); err != nil {
			return nil, err
		}
		if err := snapshotVersionTx(ctx, tx, child.ID, actorID, child); err != nil {
			return nil, err
		}
		// Webhook parity with SetParent/Convert: a detached child's
		// parent_id change is an issue.updated event — webhook
		// consumers subscribed to it must not silently miss it.
		// In-tx enqueue means a rolled-back tx enqueues nothing.
		if err := enqueueWebhookDeliveryTx(ctx, tx, wsID, EventIssueUpdated, map[string]any{
			"id":             child.ID,
			"display_id":     child.DisplayID,
			"changed_fields": []string{"parent_id"},
			"actor_id":       actorID,
		}); err != nil {
			return nil, err
		}
		detachedIDs = append(detachedIDs, child.ID)
	}

	newVal, _ := json.Marshal(map[string]any{"deleted_at": time.Now().UTC()})
	if _, err := tx.Exec(ctx,
		`INSERT INTO issue_activities (issue_id, actor_id, field, new_value)
		 VALUES ($1::uuid, $2::uuid, '_deleted', $3::jsonb)`,
		del.ID, actorID, string(newVal)); err != nil {
		return nil, err
	}

	del.DisplayID = ident + "-" + strconv.Itoa(del.SequenceID)
	// C64T1 audit: this delete-time snapshot is the deliberate content
	// anchor for the trash-restore guard in restoreIssueTx — deleted_at
	// is not in the Issue struct, so the snapshot content equals the
	// pre-delete version's by construction, and it is the row state a
	// later restore compares against. The delete is a real event
	// (accompanied by the _deleted activity row and the issue.deleted
	// webhook below), not a phantom.
	if err := snapshotVersionTx(ctx, tx, del.ID, actorID, del); err != nil {
		return nil, err
	}
	// Fan the deletion out to webhooks as issue.deleted. No in-app
	// notification: the matrix (Task 26) notifies on assign/unassign,
	// comments, state changes, and intake triage only.
	// (wsID was resolved above for the per-child fan-out.)
	if err := enqueueWebhookDeliveryTx(ctx, tx, wsID, EventIssueDeleted, map[string]any{
		"id":         del.ID,
		"display_id": del.DisplayID,
		"actor_id":   actorID,
	}); err != nil {
		return nil, err
	}
	return detachedIDs, nil
}

// announceDetachedChildren announces issue.updated for each detached
// child AFTER commit only, mirroring the C45T2 pattern in state.go: the
// post-commit snapshot is re-read, and a row that vanished after commit
// (e.g. a child deleted later in the same bulk batch) is skipped rather
// than failing the (already committed) delete. No objective fan-out —
// parent_id changes do not affect KR progress.
func announceDetachedChildren(ctx context.Context, pool *pgxpool.Pool, wsSlug, ident, projectID string, detachedIDs []string) {
	seen := make(map[string]bool, len(detachedIDs))
	for _, id := range detachedIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		iss, err := scanIssue(pool.QueryRow(ctx,
			`SELECT `+issueColumns+` FROM issues
			  WHERE id = $1::uuid AND project_id = $2::uuid AND deleted_at IS NULL`,
			id, projectID))
		if err != nil {
			continue
		}
		announceIssueUpdated(wsSlug, ident, id, iss)
	}
}

// ---------- Task 17: bulk operations ----------

var (
	// ErrBulkEmptyIDs is returned when a bulk call carries no ids.
	ErrBulkEmptyIDs = errors.New("service: bulk ids must not be empty")
	// ErrBulkTooManyIDs is returned when a bulk call exceeds maxBulkItems.
	ErrBulkTooManyIDs = errors.New("service: too many bulk ids")
)

// maxBulkItems caps a single bulk call — a DoS guard on the per-item
// savepoint loop.
const maxBulkItems = 100

// BulkItemResult is the per-item outcome of a bulk call. Error is nil when
// OK; otherwise a short human-readable reason (the item failed, the rest
// still ran).
type BulkItemResult struct {
	ID    string  `json:"id"`
	OK    bool    `json:"ok"`
	Error *string `json:"error,omitempty"`
}

// bulkItemErrMessage maps a per-item failure to a client-facing message.
// Unknown errors stay generic — no internals leak through bulk results.
func bulkItemErrMessage(err error) string {
	switch {
	case errors.Is(err, ErrIssueNotFound):
		return "issue not found"
	case errors.Is(err, ErrInvalidState):
		return "invalid state_id: state does not exist or belongs to another project"
	case errors.Is(err, ErrInvalidPriority):
		return "invalid priority: must be 0-4"
	case errors.Is(err, ErrParentNotFound):
		return "parent issue not found"
	case errors.Is(err, ErrInvalidParent):
		return "invalid parent_id"
	case errors.Is(err, ErrInvalidDateRange):
		return "start_date must not be after target_date"
	case errors.Is(err, ErrInvalidEstimatePoint):
		return "invalid estimate point"
	case errors.Is(err, ErrNameRequired):
		return "name is required"
	case errors.Is(err, ErrNothingToUpdate):
		return "nothing to update"
	case errors.Is(err, ErrOpenBlockers):
		var obe *OpenBlockersError
		if errors.As(err, &obe) && len(obe.Blockers) > 0 {
			return "open blockers: " + strings.Join(obe.Blockers, ", ")
		}
		return "open blockers"
	// C68T0: per-item message for a denied workflow transition. The
	// allowed next-state IDs ride along when present so the client can
	// name them without a second call.
	case errors.Is(err, ErrStateTransitionNotAllowed):
		var ste *StateTransitionError
		if errors.As(err, &ste) && len(ste.Allowed) > 0 {
			return "state transition not allowed by the project's workflow (allowed: " +
				strings.Join(ste.Allowed, ", ") + ")"
		}
		return "state transition not allowed by the project's workflow"
	default:
		return "internal error"
	}
}

// BulkUpdateIssues applies the same tri-state patch to many issues in ONE
// transaction: each item runs inside its own savepoint, so a failing item
// rolls back to its savepoint while the valid ones still commit (partial
// success across items, atomicity per item). Member (15)+; the role is
// checked once for the whole call.
func BulkUpdateIssues(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string, ids []string, patch IssuePatch) ([]BulkItemResult, error) {
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, ErrBulkEmptyIDs
	}
	if len(ids) > maxBulkItems {
		return nil, ErrBulkTooManyIDs
	}
	if err := validateIssuePatch(patch); err != nil {
		return nil, err
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	_, projectID, role, err := resolveIssueProject(ctx, tx, wsSlug, ident, actorID)
	if err != nil {
		return nil, err
	}
	if role < RoleMember {
		return nil, ErrForbidden
	}

	results := make([]BulkItemResult, 0, len(ids))
	announced := make([]*Issue, 0, len(ids))
	stateChangedIDs := make([]string, 0, len(ids))
	var notified []*Notification
	for i, id := range ids {
		sp := fmt.Sprintf("bulk_item_%d", i)
		// The savepoint name is internally generated (never user input).
		if _, err := tx.Exec(ctx, "SAVEPOINT "+sp); err != nil {
			return nil, err
		}
		updated, itemNotified, stateChanged, err := updateIssueTx(ctx, tx, projectID, ident, id, actorID, patch, true)
		if err != nil {
			// Per-item failure: roll back this item only, record the
			// reason, keep going.
			if _, rbErr := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT "+sp); rbErr != nil {
				return nil, rbErr
			}
			if _, relErr := tx.Exec(ctx, "RELEASE SAVEPOINT "+sp); relErr != nil {
				return nil, relErr
			}
			msg := bulkItemErrMessage(err)
			results = append(results, BulkItemResult{ID: id, OK: false, Error: &msg})
			continue
		}
		if _, err := tx.Exec(ctx, "RELEASE SAVEPOINT "+sp); err != nil {
			return nil, err
		}
		results = append(results, BulkItemResult{ID: id, OK: true})
		announced = append(announced, updated)
		if stateChanged {
			stateChangedIDs = append(stateChangedIDs, id)
		}
		notified = append(notified, itemNotified...)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	for _, u := range announced {
		announceIssueUpdated(wsSlug, ident, u.ID, u)
	}
	announceNotifications(notified)
	// C44T1: one objective.updated per linked objective across the
	// whole batch (DISTINCT in the helper) — never per issue.
	announceObjectivesForIssues(ctx, pool, wsSlug, stateChangedIDs)
	return results, nil
}

// BulkDeleteIssues soft-deletes many issues in ONE transaction with the
// same per-item savepoint semantics as BulkUpdateIssues: invalid or
// already-deleted ids report per-item errors while the valid ones are
// deleted. Member (15)+.
func BulkDeleteIssues(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string, ids []string) ([]BulkItemResult, error) {
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, ErrBulkEmptyIDs
	}
	if len(ids) > maxBulkItems {
		return nil, ErrBulkTooManyIDs
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	_, projectID, role, err := resolveIssueProject(ctx, tx, wsSlug, ident, actorID)
	if err != nil {
		return nil, err
	}
	if role < RoleMember {
		return nil, ErrForbidden
	}

	results := make([]BulkItemResult, 0, len(ids))
	deletedIDs := make([]string, 0, len(ids))
	var detachedIDs []string
	for i, id := range ids {
		sp := fmt.Sprintf("bulk_item_%d", i)
		if _, err := tx.Exec(ctx, "SAVEPOINT "+sp); err != nil {
			return nil, err
		}
		detached, err := deleteIssueTx(ctx, tx, projectID, ident, id, actorID)
		if err != nil {
			if _, rbErr := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT "+sp); rbErr != nil {
				return nil, rbErr
			}
			if _, relErr := tx.Exec(ctx, "RELEASE SAVEPOINT "+sp); relErr != nil {
				return nil, relErr
			}
			msg := bulkItemErrMessage(err)
			results = append(results, BulkItemResult{ID: id, OK: false, Error: &msg})
			continue
		}
		if _, err := tx.Exec(ctx, "RELEASE SAVEPOINT "+sp); err != nil {
			return nil, err
		}
		results = append(results, BulkItemResult{ID: id, OK: true})
		deletedIDs = append(deletedIDs, id)
		detachedIDs = append(detachedIDs, detached...)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	for _, id := range deletedIDs {
		announceIssueDeleted(wsSlug, ident, id)
	}
	// C44T1: deleted issues drop out of KR progress — one
	// objective.updated per linked objective across the batch.
	announceObjectivesForIssues(ctx, pool, wsSlug, deletedIDs)
	// C51T1: detached children announce issue.updated (post-commit
	// re-read skips children deleted later in the same batch).
	announceDetachedChildren(ctx, pool, wsSlug, ident, projectID, detachedIDs)
	return results, nil
}

// BulkRestoreIssues restores many soft-deleted issues in ONE transaction
// with the same per-item savepoint semantics as BulkDeleteIssues: a live,
// unknown, or malformed id reports a per-item error while the valid ones
// are restored. Each restored item gets the full C52T0 trail (parent
// re-validation, _restored activity row, version snapshot, issue.updated
// webhook) via restoreIssueTx, then announces issue.updated + objective
// fan-out after commit. Member (15)+.
//
// Per-item ordering: each item is validated against the state at its own
// savepoint, so a batch restoring both a child and its deleted parent
// clears the child's parent link (the parent is still deleted at the
// child's turn) — the parent itself still restores. Like
// BulkDeleteIssues, duplicate ids in one batch restore once; the repeat
// reports "issue not found".
//
// WS announce uses the in-tx restored rows (the BulkUpdateIssues
// precedent), not RestoreIssue's post-commit re-read: a concurrent
// delete landing between commit and announce is a best-effort broadcast
// race the bulk precedent already accepts. The row lock inside
// restoreIssueTx still serializes a concurrent delete/restore of the
// same issue — one wins, the other gets a per-item 404.
func BulkRestoreIssues(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string, ids []string) ([]BulkItemResult, error) {
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, ErrBulkEmptyIDs
	}
	if len(ids) > maxBulkItems {
		return nil, ErrBulkTooManyIDs
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	_, projectID, role, err := resolveIssueProject(ctx, tx, wsSlug, ident, actorID)
	if err != nil {
		return nil, err
	}
	if role < RoleMember {
		return nil, ErrForbidden
	}

	results := make([]BulkItemResult, 0, len(ids))
	restored := make([]*Issue, 0, len(ids))
	restoredIDs := make([]string, 0, len(ids))
	for i, id := range ids {
		sp := fmt.Sprintf("bulk_item_%d", i)
		if _, err := tx.Exec(ctx, "SAVEPOINT "+sp); err != nil {
			return nil, err
		}
		iss, err := restoreIssueTx(ctx, tx, projectID, ident, id, actorID)
		if err != nil {
			if _, rbErr := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT "+sp); rbErr != nil {
				return nil, rbErr
			}
			if _, relErr := tx.Exec(ctx, "RELEASE SAVEPOINT "+sp); relErr != nil {
				return nil, relErr
			}
			msg := bulkItemErrMessage(err)
			results = append(results, BulkItemResult{ID: id, OK: false, Error: &msg})
			continue
		}
		if _, err := tx.Exec(ctx, "RELEASE SAVEPOINT "+sp); err != nil {
			return nil, err
		}
		results = append(results, BulkItemResult{ID: id, OK: true})
		restored = append(restored, iss)
		restoredIDs = append(restoredIDs, iss.ID)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	for _, iss := range restored {
		announceIssueUpdated(wsSlug, ident, iss.ID, iss)
	}
	// C44T1 mirror: restored issues re-enter KR progress — one
	// objective.updated per linked objective across the batch.
	announceObjectivesForIssues(ctx, pool, wsSlug, restoredIDs)
	return results, nil
}

// ---------- Task 15: issue list — filters, cursor pagination, delta sync ----------

var (
	// ErrInvalidOrderBy is returned when order_by names no whitelisted sort.
	ErrInvalidOrderBy = errors.New("service: invalid order_by")
	// ErrInvalidCursor is returned when the cursor is malformed, fails to
	// parse for its sort kind, or was minted for a different order_by.
	ErrInvalidCursor = errors.New("service: invalid cursor")
	// ErrInvalidListFilter is returned when a list filter parameter is
	// malformed (bad UUID, priority outside 0-4, negative per_page).
	ErrInvalidListFilter = errors.New("service: invalid list filter")
)

// ---------- C5T8: atomic bulk set ----------

// BulkIssueSet is the single set applied to every issue in a BulkSetIssues
// call. Semantics per field:
//   - StateID nil = untouched; non-nil must name a state of the project.
//   - Priority nil = untouched; non-nil must be 0-4.
//   - LabelIDs nil = untouched; non-nil REPLACES the issue's label set
//     (empty array clears). Every id must be a workspace label.
//   - AssigneeID follows PatchField tri-state: unset = untouched,
//     set-with-nil = clear all assignees, set-with-value = replace with
//     that single user (must be a workspace member).
type BulkIssueSet struct {
	StateID    *string
	Priority   *int
	LabelIDs   *[]string
	AssigneeID PatchField[string]
	// IgnoreBlockers bypasses the C16T3 blocker guard on moves into a
	// completed state for the whole batch.
	IgnoreBlockers bool
}

func (s BulkIssueSet) hasFields() bool {
	return s.StateID != nil || s.Priority != nil || s.LabelIDs != nil || s.AssigneeID.Set
}

// BulkSetIssues applies one set to many issues in ONE transaction with
// full atomicity: any unknown, deleted, cross-project, or malformed id,
// or any invalid set field, aborts the whole batch — no partial
// application. Per issue it reuses updateIssueTx, so every issue gets the
// same activity rows, version snapshot, state-change notifications, and
// webhook fan-out a single-issue PATCH writes. Label/assignee replacement
// writes one "labels"/"assignees" activity row per issue (old/new id
// arrays, only when the set actually changed); a newly added assignee is
// notified like AssignAssignee does. Member (15)+; the role is checked
// once for the whole call. Ids are deduplicated (batch order kept).
// Returns the number of issues updated and their (deduplicated) ids.
func BulkSetIssues(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string, ids []string, set BulkIssueSet) (int, []string, error) {
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return 0, nil, err
	}
	if len(ids) == 0 {
		return 0, nil, ErrBulkEmptyIDs
	}
	if len(ids) > maxBulkItems {
		return 0, nil, ErrBulkTooManyIDs
	}
	if !set.hasFields() {
		return 0, nil, ErrNothingToUpdate
	}
	// Normalize + dedupe, keeping batch order. A malformed id aborts the
	// batch like an unknown one (documented atomicity).
	norm := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		nid := strings.ToLower(strings.TrimSpace(id))
		if !isUUIDFormat(nid) {
			return 0, nil, ErrIssueNotFound
		}
		if _, ok := seen[nid]; ok {
			continue
		}
		seen[nid] = struct{}{}
		norm = append(norm, nid)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, nil, err
	}
	defer tx.Rollback(ctx)

	wsID, projectID, role, err := resolveIssueProject(ctx, tx, wsSlug, ident, actorID)
	if err != nil {
		return 0, nil, err
	}
	if role < RoleMember {
		return 0, nil, ErrForbidden
	}

	// Fail fast: every id must name a live issue of THIS project.
	// Missing/deleted/foreign ids abort the whole batch before any
	// mutation runs. updateIssueTx re-checks per row (FOR UPDATE) as a
	// backstop against races — a late failure still rolls the tx back.
	var have int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM issues
		  WHERE project_id = $1::uuid AND id = ANY($2::uuid[]) AND deleted_at IS NULL`,
		projectID, norm).Scan(&have); err != nil {
		return 0, nil, err
	}
	if have != len(norm) {
		return 0, nil, ErrIssueNotFound
	}

	// Validate the set fields once, before any mutation.
	var stateID *string
	if set.StateID != nil {
		sid, err := checkStateInProject(ctx, tx, projectID, *set.StateID)
		if err != nil {
			return 0, nil, err
		}
		stateID = &sid
	}
	if set.Priority != nil && (*set.Priority < 0 || *set.Priority > 4) {
		return 0, nil, ErrInvalidPriority
	}
	var labelIDs []string
	if set.LabelIDs != nil {
		labelIDs = dedupeSortedStrings(normalizeIDs(*set.LabelIDs))
		for _, lid := range labelIDs {
			var owner string
			err := tx.QueryRow(ctx,
				`SELECT workspace_id::text FROM labels WHERE id = $1::uuid`, lid).Scan(&owner)
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
					return 0, nil, ErrLabelNotFound
				}
				return 0, nil, err
			}
			if owner != wsID {
				return 0, nil, ErrLabelNotFound
			}
		}
	}
	var assigneeID PatchField[string]
	if set.AssigneeID.Set {
		if set.AssigneeID.Value != nil {
			uid := strings.ToLower(strings.TrimSpace(*set.AssigneeID.Value))
			var one int
			err := tx.QueryRow(ctx,
				`SELECT 1 FROM workspace_members WHERE workspace_id = $1::uuid AND user_id = $2::uuid`,
				wsID, uid).Scan(&one)
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
					return 0, nil, ErrAssigneeNotMember
				}
				return 0, nil, err
			}
			assigneeID = PatchField[string]{Set: true, Value: &uid}
		} else {
			assigneeID = PatchField[string]{Set: true}
		}
	}

	var notified []*Notification
	announced := make([]*Issue, 0, len(norm))
	stateChangedIDs := make([]string, 0, len(norm))
	for _, id := range norm {
		// C46T0: capture the before-image under the row lock (same tx,
		// same lock updateIssueTx takes below, so this is exactly
		// the state the batch starts from). The cascade detector
		// compares the final re-read against THIS, not against
		// updateIssueTx's returned row: that row predates
		// updateIssueTx's own automation cascade, so a net-zero
		// round trip (direct move + a rule moving it back) would
		// otherwise false-positive. C56T2: the before-image is the
		// FULL row now, not just state_id — the webhook
		// changed_fields union diffs it against the final row via
		// issueRowChangedFields.
		before, err := scanIssue(tx.QueryRow(ctx,
			`SELECT `+issueColumns+` FROM issues
			 WHERE id = $1::uuid AND project_id = $2::uuid AND deleted_at IS NULL
			 FOR UPDATE`,
			id, projectID))
		if err != nil {
			return 0, nil, err
		}
		preStateID := before.StateID
		// C56T2: the patch fields updateIssueTx will actually
		// change, in updateIssueTx's own activity order (priority
		// before state_id). updateIssueTx compares its patch
		// against the same locked row (== before), so these two
		// conditions mirror its add() calls exactly: patchFields
		// non-empty ⟺ updateIssueTx enqueues a webhook below.
		var patchFields []string
		if set.Priority != nil && *set.Priority != before.Priority {
			patchFields = append(patchFields, "priority")
		}
		if stateID != nil && *stateID != before.StateID {
			patchFields = append(patchFields, "state_id")
		}
		// C56T2: outbox watermark taken BEFORE updateIssueTx
		// runs, so the rows it enqueues for this item are exactly
		// the ones with id > watermark (matched by issue id +
		// domain event in bulkSetWebhookTx). Per-item, because one
		// batch tx enqueues for every item.
		var outboxWatermark int64
		if err := tx.QueryRow(ctx,
			`SELECT COALESCE(MAX(id), 0) FROM outbox`).Scan(&outboxWatermark); err != nil {
			return 0, nil, err
		}
		var patch IssuePatch
		if stateID != nil {
			patch.StateID = stateID
		}
		if set.Priority != nil {
			patch.Priority = set.Priority
		}
		patch.IgnoreBlockers = set.IgnoreBlockers
		// updateIssueTx is the single-issue PATCH core: row lock,
		// per-field activity rows, version snapshot, state-change
		// notifications, webhook fan-out. A patch with no changed fields
		// returns the current row without writing (labels/assignees-only
		// sets are fine). The returned row is the comparison baseline
		// for the C50T0 post-cascade snapshot below.
		updatedRow, itemNotified, _, err := updateIssueTx(ctx, tx, projectID, ident, id, actorID, patch, true)
		if err != nil {
			return 0, nil, err
		}
		notified = append(notified, itemNotified...)
		// C56T2: the replace fns report no changed flag (their
		// notification return is nil whenever no rule fires, even
		// on a real set change), so read the pre-replace sets here
		// — after updateIssueTx (which never touches labels /
		// assignees) and before the replaces — and compare against
		// the batch targets. Labels/assignees are not row columns,
		// so the before/final row diff below can never see them.
		setFields, err := bulkSetChangedSetFieldsTx(ctx, tx, id, set.LabelIDs != nil, labelIDs, assigneeID)
		if err != nil {
			return 0, nil, err
		}
		if set.LabelIDs != nil {
			ln, err := replaceIssueLabelsTx(ctx, tx, wsID, projectID, ident, id, actorID, labelIDs)
			if err != nil {
				return 0, nil, err
			}
			notified = append(notified, ln...)
		}
		if assigneeID.Set {
			added, err := replaceIssueAssigneesTx(ctx, tx, wsID, projectID, ident, id, actorID, assigneeID.Value)
			if err != nil {
				return 0, nil, err
			}
			notified = append(notified, added...)
		}
		// C46T0: the replace fns above evaluate labels_changed /
		// assigned / unassigned automations in this same tx, and a
		// rule may fire set_state AFTER updateIssueTx ran — a
		// cascade move updateIssueTx never saw. Re-read the final
		// in-tx row: it is the announced snapshot (post-replace
		// labels/assignees/state — exactly what commits) and its
		// state_id, compared against the before-image above, is
		// the fan-out detector (C45T1 before/after pattern: only
		// set_state touches state_id, so before≠after means
		// exactly one real net move, and a net-zero round trip
		// reports false). For the non-cascade path the replaces
		// never touch state_id, so final==end-of-updateIssueTx
		// state and membership is identical to before. The
		// announce loop still runs after tx.Commit, so a failed
		// commit announces nothing.
		final, err := scanIssue(tx.QueryRow(ctx,
			`SELECT `+issueColumns+` FROM issues
			 WHERE id = $1::uuid AND project_id = $2::uuid AND deleted_at IS NULL`,
			id, projectID))
		if err != nil {
			return 0, nil, err
		}
		final.DisplayID = ident + "-" + strconv.Itoa(final.SequenceID)
		// C68T0: workflow transition governance applies to the FINAL
		// state value (C46 discipline). The replace fns above may have
		// fired a membership automation that cascade-moved the row AFTER
		// updateIssueTx checked the requested move — the net movement
		// the user initiated is (preStateID → final.StateID). When rules
		// exist and that pair is not allowed, the whole batch fails 409
		// here and the tx rolls back: no mutation, no announce, no
		// activity row. (Standalone automation firings outside a user
		// request keep their existing bypass guarantee.)
		if final.StateID != preStateID {
			if err := checkTransitionTx(ctx, tx, projectID, preStateID, final.StateID); err != nil {
				return 0, nil, err
			}
		}
		// C50T0: the replace fns above may have fired a cascade rule
		// that moved the row AFTER updateIssueTx snapshotted it. If
		// the final row's content differs from the row updateIssueTx
		// returned, write one extra version carrying the post-cascade
		// row. If equal, snapshot nothing: updateIssueTx's snapshot
		// (or no snapshot, for a labels-only no-change patch) already
		// stands. No FOR UPDATE on this re-read: the C46T0
		// before-image SELECT ... FOR UPDATE above (same tx, same
		// row) already holds the row lock until commit, and
		// updateIssueTx re-took it — so this read is lock-covered
		// and snapshotVersionTx's sequencing cannot interleave.
		// In-tx like every other write here, so any error rolls the
		// snapshot back with the batch.
		// C64T1 audit: the issueRowContentEqual guard above covers this
		// call site — a no-change final row (no-op patch, no cascade)
		// snapshots nothing.
		c50SnapshotFired := false
		if !issueRowContentEqual(final, updatedRow) {
			if err := snapshotVersionTx(ctx, tx, id, actorID, final); err != nil {
				return 0, nil, err
			}
			c50SnapshotFired = true
		}
		// C65T1: a real label/assignee membership change (REUSED
		// setFields — never re-derived) that moved no row anywhere owes
		// exactly one version carrying the post-mutation row: membership
		// is not a row column, so neither updateIssueTx's snapshot nor
		// the C50T0 guard above could ever see it. Skipped when the item
		// already got a version this pass — updateIssueTx snapshotted
		// (len(patchFields) > 0 mirrors its sets exactly) or the C50T0
		// guard just fired (a cascade moved the row) — the exactly-one
		// invariant. Skipped on no-op sets: setFields carries neither
		// "labels" nor "assignees". final already has its derived
		// DisplayID set above.
		if !c50SnapshotFired && len(patchFields) == 0 && bulkSetMembershipChanged(setFields) {
			if err := snapshotVersionTx(ctx, tx, id, actorID, final); err != nil {
				return 0, nil, err
			}
		}
		// C56T2: exactly-one issue.updated webhook for this item —
		// fresh enqueue when the patch enqueued nothing, in-tx
		// extension of updateIssueTx's row when the set/cascade
		// changes add fields beyond the patch. No-op items (empty
		// union) enqueue nothing.
		if err := bulkSetWebhookTx(ctx, tx, wsID, id, actorID, outboxWatermark, patchFields, setFields, before, final); err != nil {
			return 0, nil, err
		}
		announced = append(announced, final)
		if final.StateID != preStateID {
			stateChangedIDs = append(stateChangedIDs, id)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, nil, err
	}
	for _, u := range announced {
		announceIssueUpdated(wsSlug, ident, u.ID, u)
	}
	announceNotifications(notified)
	// C44T1: same rule as BulkUpdateIssues — one objective.updated per
	// linked objective across the batch.
	announceObjectivesForIssues(ctx, pool, wsSlug, stateChangedIDs)
	return len(norm), norm, nil
}

// unionChangedFields merges field lists preserving first-seen order,
// dropping duplicates. The order is the caller's contract: patch
// fields (updateIssueTx order), then set fields, then cascade diff.
func unionChangedFields(lists ...[]string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, list := range lists {
		for _, f := range list {
			if !seen[f] {
				seen[f] = true
				out = append(out, f)
			}
		}
	}
	return out
}

// sameFieldSet reports whether two field lists hold the same set,
// ignoring order and duplicates.
func sameFieldSet(a, b []string) bool {
	if len(unionChangedFields(a)) != len(unionChangedFields(b)) {
		return false
	}
	seen := make(map[string]bool, len(a))
	for _, f := range a {
		seen[f] = true
	}
	for _, f := range b {
		if !seen[f] {
			return false
		}
	}
	return true
}

// bulkSetChangedSetFieldsTx compares the issue's pre-replace label /
// assignee sets against the batch targets and returns the direct
// set-change fields ("labels" / "assignees"). Only the sets the batch
// actually touches are read. Labels/assignees are not row columns, so
// the before/final row diff in bulkSetWebhookTx can never report them
// — they must be tracked separately, here.
func bulkSetChangedSetFieldsTx(ctx context.Context, tx pgx.Tx, issueID string, labelsSet bool, labelIDs []string, assigneeID PatchField[string]) ([]string, error) {
	var fields []string
	if labelsSet {
		var current []string
		rows, err := tx.Query(ctx,
			`SELECT label_id::text FROM issue_labels WHERE issue_id = $1::uuid ORDER BY label_id::text`, issueID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			current = append(current, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		// labelIDs is the canonical target (deduped + sorted, same
		// as the current query's ORDER BY), so a plain sorted
		// comparison decides changed-ness.
		if !equalStringSlices(current, labelIDs) {
			fields = append(fields, "labels")
		}
	}
	if assigneeID.Set {
		var current []string
		rows, err := tx.Query(ctx,
			`SELECT user_id::text FROM issue_assignees WHERE issue_id = $1::uuid ORDER BY user_id::text`, issueID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			current = append(current, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		var want []string
		if assigneeID.Value != nil {
			want = []string{*assigneeID.Value}
		}
		if !equalStringSlices(current, want) {
			fields = append(fields, "assignees")
		}
	}
	return fields, nil
}

// bulkSetMembershipChanged reports whether the direct set-change
// fields computed by bulkSetChangedSetFieldsTx record a real
// label/assignee membership change for this item (C65T1). The
// snapshot schema stays the post-mutation Issue row; the
// issue_activities 'labels'/'assignees' rows record the old/new sets.
func bulkSetMembershipChanged(setFields []string) bool {
	for _, f := range setFields {
		if f == "labels" || f == "assignees" {
			return true
		}
	}
	return false
}

// bulkSetWebhookTx enqueues exactly ONE issue.updated webhook in-tx
// for one BulkSetIssues item (C56T2). changed_fields is the union of
// the direct patch fields (patchFields — what updateIssueTx already
// enqueued, empty when the patch changed nothing), the direct
// labels/assignees set changes (setFields), and the cascade-caused
// row diff (issueRowChangedFields over the pre-batch before-image vs
// the final row — REUSED from C56T1, never re-derived).
//
// Exactly-one rule vs updateIssueTx's enqueue:
//   - Patch enqueued nothing + union non-empty → ONE fresh row
//     carrying the union (the labels-only / assignees-only case).
//   - Patch enqueued + union adds nothing beyond the patch → the
//     existing row is left byte-identical (patch-only case).
//   - Patch enqueued + union adds fields → the already-enqueued
//     row(s) are EXTENDED in place: one UPDATE per subscribed
//     webhook rewriting only payload.data.changed_fields. The rows
//     are uncommitted (same tx), invisible to the dispatcher, so
//     the rewrite cannot race a delivery — and no second row is
//     ever added, so the two-row exception from the plan is NOT
//     needed. (Deliberate limit: a webhook created concurrently
//     mid-batch, after updateIssueTx ran, is not covered by the
//     extend — same race as any two enqueue sites in one tx.)
//   - Patch enqueued + no rows found (no webhook was subscribed
//     when updateIssueTx ran) → ONE fresh row with the full union,
//     so a webhook created mid-batch still sees the mutation.
//   - Empty union → nothing enqueued (no-op BulkSet).
func bulkSetWebhookTx(ctx context.Context, tx pgx.Tx, wsID, issueID, actorID string, outboxWatermark int64, patchFields, setFields []string, before, final *Issue) error {
	changedFields := unionChangedFields(patchFields, setFields, issueRowChangedFields(before, final))
	if len(changedFields) == 0 {
		return nil
	}
	payload := map[string]any{
		"id":             issueID,
		"display_id":     final.DisplayID,
		"changed_fields": changedFields,
		"actor_id":       actorID,
	}
	if len(patchFields) == 0 {
		return enqueueWebhookDeliveryTx(ctx, tx, wsID, EventIssueUpdated, payload)
	}
	// Find exactly the rows this item's updateIssueTx enqueued: id >
	// the per-item watermark, this issue, this domain event, and NO
	// taxonomy field (labels/assignees) in changed_fields. The
	// taxonomy exclusion is load-bearing: PATCH/restore/BulkSet
	// writers lock the issue row, so a concurrent mutation of THIS
	// issue by one of them is blocked — but the label/assignee
	// writers (AssignLabel/UnassignLabel/AssignAssignee/
	// UnassignAssignee) take NO FOR UPDATE lock (plain SELECT
	// before-image + INSERT into the membership table), so a
	// concurrent AssignLabel on THIS issue can commit an outbox row
	// above the watermark while this tx runs. That row is safe from
	// the extend because updateIssueTx's row for a BulkSet item can
	// NEVER carry labels/assignees (it never touches them; BulkSet's
	// patch is priority/state only), while every label/assignee-path
	// row ALWAYS carries its direct taxonomy field (C56T1 prepends
	// it). So the guard makes the extend match only rows
	// updateIssueTx could have written for this item. A skipped
	// decoy row falls through to the len(rowIDs)==0 fresh-enqueue
	// fallback below, and the union lands in a fresh row.
	rows, err := tx.Query(ctx,
		`SELECT id FROM outbox
		  WHERE id > $1 AND event = $2
		    AND payload->>'event' = $3
		    AND payload->'data'->>'id' = $4
		    AND NOT (payload->'data'->'changed_fields' ? 'labels'
		      OR payload->'data'->'changed_fields' ? 'assignees')
		  ORDER BY id`,
		outboxWatermark, WebhookDeliverEvent, EventIssueUpdated, issueID)
	if err != nil {
		return err
	}
	var rowIDs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		rowIDs = append(rowIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(rowIDs) == 0 {
		return enqueueWebhookDeliveryTx(ctx, tx, wsID, EventIssueUpdated, payload)
	}
	if sameFieldSet(changedFields, patchFields) {
		return nil
	}
	for _, rid := range rowIDs {
		if _, err := tx.Exec(ctx,
			`UPDATE outbox
			  SET payload = jsonb_set(payload, '{data,changed_fields}', $1::jsonb)
			WHERE id = $2`,
			toJSONBParam(changedFields), rid); err != nil {
			return err
		}
	}
	return nil
}

// normalizeIDs lowercases/trims raw id strings; malformed entries are
// kept as-is so the caller's validation (uuid cast) rejects them.
func normalizeIDs(ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, strings.ToLower(strings.TrimSpace(id)))
	}
	return out
}

// dedupeSortedStrings dedupes and sorts — the canonical form used for
// set comparison and storage.
func dedupeSortedStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// equalStringSlices reports whether two sorted slices hold the same ids.
func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// replaceIssueLabelsTx replaces the issue's label set with exactly the
// given (canonical, sorted) ids — empty clears. It writes one "labels"
// issue_activities row with the old/new id arrays, but only when the set
// actually changed (mirroring updateIssueTx's changed-only convention).
// It returns the notifications created for the caller to announce after
// commit. C16T0: when the set changed it also evaluates
// issue.labels_changed automations in this same tx (guarded by
// !automationActive) — a rolled-back replace can never fire a rule.
func replaceIssueLabelsTx(ctx context.Context, tx pgx.Tx, wsID, projectID, ident, issueID, actorID string, labelIDs []string) ([]*Notification, error) {
	var old []string
	rows, err := tx.Query(ctx,
		`SELECT label_id::text FROM issue_labels WHERE issue_id = $1::uuid ORDER BY label_id::text`, issueID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		old = append(old, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if equalStringSlices(old, labelIDs) {
		return nil, nil
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM issue_labels WHERE issue_id = $1::uuid`, issueID); err != nil {
		return nil, err
	}
	for _, lid := range labelIDs {
		if _, err := tx.Exec(ctx,
			`INSERT INTO issue_labels (issue_id, label_id) VALUES ($1::uuid, $2::uuid)
			 ON CONFLICT DO NOTHING`, issueID, lid); err != nil {
			return nil, err
		}
	}
	oldArr := old
	if oldArr == nil {
		oldArr = []string{}
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO issue_activities (issue_id, actor_id, field, old_value, new_value)
		 VALUES ($1::uuid, $2::uuid, 'labels', $3::jsonb, $4::jsonb)`,
		issueID, actorID, toJSONBParam(oldArr), toJSONBParam(labelIDs)); err != nil {
		return nil, err
	}
	oldSet := make(map[string]struct{}, len(old))
	for _, id := range old {
		oldSet[id] = struct{}{}
	}
	newSet := make(map[string]struct{}, len(labelIDs))
	for _, id := range labelIDs {
		newSet[id] = struct{}{}
	}
	var added, removed []string
	for _, id := range labelIDs {
		if _, ok := oldSet[id]; !ok {
			added = append(added, id)
		}
	}
	for _, id := range old {
		if _, ok := newSet[id]; !ok {
			removed = append(removed, id)
		}
	}
	var notified []*Notification
	if !automationActive(ctx) {
		notified = runAutomationLabelsChangedTx(ctx, tx, wsID, projectID, ident, issueID, actorID, added, removed)
	}
	return notified, nil
}

// replaceIssueAssigneesTx replaces the issue's assignee set with the
// given single user (nil clears). It writes one "assignees"
// issue_activities row when the set changed, and notifies the assignee
// when they were newly added — the same notification AssignAssignee
// sends. C16T0: it also evaluates issue.assigned / issue.unassigned
// automations in this same tx when the set gained / lost members
// (guarded by !automationActive). Returns the created notifications for
// the caller to announce after commit.
func replaceIssueAssigneesTx(ctx context.Context, tx pgx.Tx, wsID, projectID, ident, issueID, actorID string, assigneeID *string) ([]*Notification, error) {
	var old []string
	rows, err := tx.Query(ctx,
		`SELECT user_id::text FROM issue_assignees WHERE issue_id = $1::uuid ORDER BY user_id::text`, issueID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		old = append(old, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var want []string
	if assigneeID != nil {
		want = []string{*assigneeID}
	}
	if equalStringSlices(old, want) {
		return nil, nil
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM issue_assignees WHERE issue_id = $1::uuid`, issueID); err != nil {
		return nil, err
	}
	for _, uid := range want {
		if _, err := tx.Exec(ctx,
			`INSERT INTO issue_assignees (issue_id, user_id) VALUES ($1::uuid, $2::uuid)
			 ON CONFLICT DO NOTHING`, issueID, uid); err != nil {
			return nil, err
		}
	}
	oldArr := old
	if oldArr == nil {
		oldArr = []string{}
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO issue_activities (issue_id, actor_id, field, old_value, new_value)
		 VALUES ($1::uuid, $2::uuid, 'assignees', $3::jsonb, $4::jsonb)`,
		issueID, actorID, toJSONBParam(oldArr), toJSONBParam(want)); err != nil {
		return nil, err
	}
	// Notify the newly added assignee (mirrors AssignAssignee).
	oldSet := make(map[string]struct{}, len(old))
	for _, id := range old {
		oldSet[id] = struct{}{}
	}
	wantSet := make(map[string]struct{}, len(want))
	for _, id := range want {
		wantSet[id] = struct{}{}
	}
	var added, removed []string
	for _, uid := range want {
		if _, ok := oldSet[uid]; !ok {
			added = append(added, uid)
		}
	}
	for _, id := range old {
		if _, ok := wantSet[id]; !ok {
			removed = append(removed, id)
		}
	}
	// C16T0: assignee add/remove automation events — in this same tx,
	// so a rolled-back bulk set can never fire a rule.
	var autoNotified []*Notification
	if !automationActive(ctx) {
		if len(added) > 0 {
			autoNotified = append(autoNotified,
				runAutomationAssignedTx(ctx, tx, wsID, projectID, ident, issueID, actorID)...)
		}
		if len(removed) > 0 {
			autoNotified = append(autoNotified,
				runAutomationUnassignedTx(ctx, tx, wsID, projectID, ident, issueID, actorID)...)
		}
	}
	if len(added) == 0 {
		return autoNotified, nil
	}
	displayID, name, err := issueNotifyContextTx(ctx, tx, ident, issueID)
	if err != nil {
		return nil, err
	}
	actorName := actorDisplayName(ctx, tx, actorID)
	notified, err := notifyTx(ctx, tx, NotifyIssueAssigned,
		fmt.Sprintf("%s assigned you to %s", actorName, displayID),
		fmt.Sprintf("Issue: %s", name),
		map[string]any{
			"issue_id":     issueID,
			"display_id":   displayID,
			"issue_name":   name,
			"actor_id":     actorID,
			"workspace_id": wsID,
			"project_id":   projectID,
		},
		actorID, added)
	if err != nil {
		return nil, err
	}
	return append(autoNotified, notified...), nil
}

// listSortKind classifies a whitelisted sort column so cursor values can
// be (de)serialized with the right type.
type listSortKind int

const (
	listSortTime listSortKind = iota
	listSortInt
	listSortFloat
)

// sqlCast returns the Postgres cast for a cursor value of this kind.
func (k listSortKind) sqlCast() string {
	switch k {
	case listSortTime:
		return "::timestamptz"
	case listSortInt:
		return "::int"
	default:
		return "::float8"
	}
}

type listOrder struct {
	column string // qualified SQL column — from the whitelist below, never user input
	desc   bool
	kind   listSortKind
}

// listOrders is the complete whitelist for ?order_by=. The column strings
// are interpolated into SQL, so this map — not user input — is the source.
var listOrders = map[string]listOrder{
	"updated_at":   {"i.updated_at", false, listSortTime},
	"-updated_at":  {"i.updated_at", true, listSortTime},
	"created_at":   {"i.created_at", false, listSortTime},
	"-created_at":  {"i.created_at", true, listSortTime},
	"sequence_id":  {"i.sequence_id", false, listSortInt},
	"-sequence_id": {"i.sequence_id", true, listSortInt},
	"sort_order":   {"i.sort_order", false, listSortFloat},
	"-sort_order":  {"i.sort_order", true, listSortFloat},
	"priority":     {"i.priority", false, listSortInt},
	"-priority":    {"i.priority", true, listSortInt},
}

const (
	defaultListPerPage = 25
	maxListPerPage     = 100
)

// listCursor is the opaque pagination cursor: the sort key of the last
// row on the page plus its id (tiebreak), minted for one order_by. JSON →
// base64url, no padding.
type listCursor struct {
	Order string `json:"o"`
	Value string `json:"v"`
	ID    string `json:"i"`
}

func encodeListCursor(order, value, id string) string {
	b, _ := json.Marshal(listCursor{Order: order, Value: value, ID: id})
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeListCursor(s, wantOrder string) (listCursor, error) {
	var c listCursor
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return c, ErrInvalidCursor
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, ErrInvalidCursor
	}
	if c.Order != wantOrder || c.Value == "" || c.ID == "" || !isUUIDFormat(c.ID) {
		return c, ErrInvalidCursor
	}
	return c, nil
}

// validCursorValue rejects hand-crafted cursors whose value does not parse
// for the sort kind (which would otherwise 500 on the SQL cast).
func validCursorValue(kind listSortKind, v string) bool {
	switch kind {
	case listSortTime:
		_, err := time.Parse(time.RFC3339Nano, v)
		return err == nil
	case listSortInt:
		_, err := strconv.Atoi(v)
		return err == nil
	default:
		_, err := strconv.ParseFloat(v, 64)
		return err == nil
	}
}

// isUUIDFormat is a dependency-free syntactic UUID check (8-4-4-4-12 hex).
func isUUIDFormat(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < 36; i++ {
		switch i {
		case 8, 13, 18, 23:
			if s[i] != '-' {
				return false
			}
		default:
			c := s[i]
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
				return false
			}
		}
	}
	return true
}

// listSortKey serializes the row's sort key for the cursor, in the same
// textual form the cursor predicate parses back.
func listSortKey(o listOrder, iss *Issue) string {
	switch o.column {
	case "i.updated_at":
		return iss.UpdatedAt.UTC().Format(time.RFC3339Nano)
	case "i.created_at":
		return iss.CreatedAt.UTC().Format(time.RFC3339Nano)
	case "i.sequence_id":
		return strconv.Itoa(iss.SequenceID)
	case "i.priority":
		return strconv.Itoa(iss.Priority)
	default: // i.sort_order
		return strconv.FormatFloat(iss.SortOrder, 'g', -1, 64)
	}
}

// ListIssuesInput carries the list filters. Empty/zero values mean "no
// filter". State/Assignee/Label/Cycle take UUIDs.
// filterNone is the sentinel value meaning "no relation" inside the
// Assignees and EstimatePoints multi-filters (matches issues with zero
// assignees / a NULL estimate point).
const filterNone = "none"

type ListIssuesInput struct {
	State   string
	Cycle   string
	Q       string
	OrderBy string
	Cursor  string
	PerPage int
	Fields  []string
	// Multi-value filters (C2T5). Each list is OR-matched; different
	// dimensions are ANDed. Empty list = no filter on that dimension.
	// Assignees and EstimatePoints accept the sentinel "none" for issues
	// with no assignees / no estimate point.
	Priorities     []int
	Labels         []string
	Assignees      []string
	EstimatePoints []string
	// Date ranges (inclusive start, exclusive end).
	CreatedAfter  *time.Time
	CreatedBefore *time.Time
	UpdatedAfter  *time.Time
	UpdatedBefore *time.Time
	DueAfter      *time.Time
	DueBefore     *time.Time
	// StartAfter/StartBefore bracket issues.start_date (C3T5: calendar
	// view places start-dated issues by their start date).
	StartAfter  *time.Time
	StartBefore *time.Time
	// Undated matches issues with neither a target nor a start date
	// (C3T5: the calendar "unscheduled" strip).
	Undated bool
	// Subscribed limits to issues the actor subscribed to.
	Subscribed bool
	// SequenceID looks up one issue by its per-project sequence number
	// (C6T5: the command palette's display-ID resolution). 0 = no filter.
	// Sequence ids are dense from 1 — there is no valid 0 to confuse.
	SequenceID int
	// Archived includes archived issues. Default (false) excludes them:
	// archived issues are out of the working set.
	Archived bool
	// Deleted (C52T0, ?deleted=1) selects the trash list: deleted-only
	// rows. It is honored ONLY by the list handler, which branches to
	// ListDeletedIssues before calling ListIssues — it never reaches
	// issueListConds, so the export path (which shares those conds)
	// can never leak deleted rows.
	Deleted bool
	// IncludeProjectStructure attaches the cycle/module/release membership
	// (?include_project_structure=1, C21T2) to every returned row in ONE
	// batched query — the batched replacement for the spreadsheet's old
	// per-row ?include_project_structure=1 detail fetches. False keeps the
	// list response byte-identical (no new fields, no extra query).
	IncludeProjectStructure bool
	// Draft tri-state (C6T4): nil = exclude drafts (the working-set
	// default — drafts live in the Drafts view until published);
	// true = drafts only; false = explicitly exclude drafts (same as
	// the default, for callers that want to be explicit). Revised
	// during implementation: no UI could create drafts before C6T4,
	// so no existing flow depends on drafts appearing in lists, and
	// "drafts hidden from the working set" is the only sane semantic.
	Draft *bool
}

// validateListFilterValues checks the UUID/priority formats of a
// ListIssuesInput. Shared by ListIssues and the export path (C8T3) so both
// reject malformed filters identically.
func validateListFilterValues(in ListIssuesInput) error {
	for _, p := range in.Priorities {
		if p < 0 || p > 4 {
			return ErrInvalidListFilter
		}
	}
	for _, f := range []string{in.State, in.Cycle} {
		if f != "" && !isUUIDFormat(f) {
			return ErrInvalidListFilter
		}
	}
	// Multi-value UUID filters; Assignees/EstimatePoints also accept the
	// "none" sentinel (validated separately below).
	for _, id := range in.Labels {
		if !isUUIDFormat(id) {
			return ErrInvalidListFilter
		}
	}
	for _, id := range in.Assignees {
		if id != filterNone && !isUUIDFormat(id) {
			return ErrInvalidListFilter
		}
	}
	for _, id := range in.EstimatePoints {
		if id != filterNone && !isUUIDFormat(id) {
			return ErrInvalidListFilter
		}
	}
	return nil
}

// IssueAssignee is one assignee on an issue, aggregated from
// issue_assignees in the same query as the issue row (Task 16).
type IssueAssignee struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// IssueLabel is one label on an issue, aggregated from issue_labels in
// the same query as the issue row (Task 16).
type IssueLabel struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

// assigneesAgg / labelsAgg are correlated subqueries aggregating an
// issue's relations as JSONB arrays inside the single list/detail query
// — one query total, never per-row lookups (Review Focus #3). The outer
// query must alias issues as i. COALESCE keeps the shape [] (never null)
// for issues with no relations. assigneesAgg also carries each user's
// email (C8T3: the export CSV's assignee column); unmarshalRelations
// ignores the extra key so the list shape is unchanged.
const assigneesAgg = `(SELECT COALESCE(json_agg(jsonb_build_object(
		'id', u.id::text,
		'name', COALESCE(u.name, u.email::text),
		'email', u.email::text)
		ORDER BY COALESCE(u.name, u.email::text))::jsonb, '[]'::jsonb)
	FROM issue_assignees ia JOIN users u ON u.id = ia.user_id
	WHERE ia.issue_id = i.id)`

const labelsAgg = `(SELECT COALESCE(json_agg(jsonb_build_object(
		'id', l.id::text, 'name', l.name, 'color', l.color)
		ORDER BY l.name)::jsonb, '[]'::jsonb)
	FROM issue_labels il JOIN labels l ON l.id = il.label_id
	WHERE il.issue_id = i.id)`

// externalRefsAgg aggregates an issue's linked PRs/commits (C17T0),
// oldest first. Selected only by the detail query (getIssueRow); the
// list query is untouched.
const externalRefsAgg = `(SELECT COALESCE(json_agg(jsonb_build_object(
		'id', r.id::text,
		'issue_id', r.issue_id::text,
		'provider', r.provider,
		'repo', r.repo,
		'ref_type', r.ref_type,
		'ref_id', r.ref_id,
		'url', r.url,
		'state', r.state,
		'created_at', r.created_at)
		ORDER BY r.created_at)::jsonb, '[]'::jsonb)
	FROM issue_external_refs r
	WHERE r.issue_id = i.id)`

// subtaskProgressAgg rolls up an issue's DIRECT children (C17T2): total
// children and how many sit in a 'completed'-group state. Only direct
// children count (parent_id = i.id, no recursion); soft-deleted children
// are excluded. A single correlated aggregate subquery, so the detail and
// list queries stay one round-trip with no per-row lookups — like
// assigneesAgg/labelsAgg, the outer query must alias issues as i.
// COUNT(*) can never return NULL, so the object is always present.
const subtaskProgressAgg = `(SELECT json_build_object(
		'total', COUNT(*),
		'done', COUNT(*) FILTER (WHERE s."group" = 'completed'))
	FROM issues c JOIN states s ON s.id = c.state_id
	WHERE c.parent_id = i.id AND c.project_id = i.project_id
	  AND c.deleted_at IS NULL)`

// epicsAgg aggregates an issue's epic memberships (C71T0) as
// [{id, name, archived_at}], name-ordered. Archived epics stay visible
// (the UI dims them). Selected only by the detail query (getIssueRow);
// the list query is untouched. archived_at serializes as JSON null when
// NULL — unmarshalEpicRefs leaves it nil (omitempty on EpicRef).
const epicsAgg = `(SELECT COALESCE(json_agg(jsonb_build_object(
		'id', e.id::text,
		'name', e.name,
		'archived_at', e.archived_at)
		ORDER BY e.name)::jsonb, '[]'::jsonb)
	FROM epic_issues ei JOIN epics e ON e.id = ei.epic_id
	WHERE ei.issue_id = i.id)`

// unmarshalEpicRefs decodes the epicsAgg JSON array into the issue's
// Epics slice. Always non-nil after a successful decode so the detail
// contract always carries the key (possibly empty).
func unmarshalEpicRefs(epicsJSON []byte, item *IssueListItem) error {
	item.Epics = []EpicRef{}
	if err := json.Unmarshal(epicsJSON, &item.Epics); err != nil {
		return fmt.Errorf("service: decode epics: %w", err)
	}
	return nil
}

// unmarshalRelations decodes the two aggregated JSONB columns into the
// list item's Assignees/Labels slices.
func unmarshalRelations(assigneesJSON, labelsJSON []byte, item *IssueListItem) error {
	item.Assignees = []IssueAssignee{}
	item.Labels = []IssueLabel{}
	if err := json.Unmarshal(assigneesJSON, &item.Assignees); err != nil {
		return fmt.Errorf("service: decode assignees: %w", err)
	}
	if err := json.Unmarshal(labelsJSON, &item.Labels); err != nil {
		return fmt.Errorf("service: decode labels: %w", err)
	}
	return nil
}

// unmarshalSubtaskProgress decodes the subtaskProgressAgg JSON object
// into the list item. Callers that select the aggregate always get a
// non-nil progress (COUNT(*) never yields NULL); a corrupt payload is a
// real error, never silently swallowed.
func unmarshalSubtaskProgress(progressJSON []byte, item *IssueListItem) error {
	var p SubtaskProgress
	if err := json.Unmarshal(progressJSON, &p); err != nil {
		return fmt.Errorf("service: decode subtask progress: %w", err)
	}
	item.SubtaskProgress = &p
	return nil
}

// unmarshalExternalRefs decodes the externalRefsAgg JSON array into the
// list item. The aggregate COALESCEs to '[]', so the detail response
// always embeds the array (empty when nothing is linked); a corrupt
// payload is a real error, never silently swallowed.
func unmarshalExternalRefs(refsJSON []byte, item *IssueListItem) error {
	refs := []ExternalRef{}
	if err := json.Unmarshal(refsJSON, &refs); err != nil {
		return fmt.Errorf("service: decode external refs: %w", err)
	}
	item.ExternalRefs = refs
	return nil
}

// issueListConds builds the WHERE fragments for the issue list query from
// a ListIssuesInput. Shared by ListIssues and the export path (C8T3) so
// both honor identical filter/scope semantics: what the user sees in the
// list is what the export contains. Cursor pagination stays in ListIssues
// (exports stream the whole filtered set in sequence order, unpaginated).
func issueListConds(in ListIssuesInput, projectID, actorID string, arg func(any) string) []string {
	var conds []string
	conds = append(conds, "i.project_id = "+arg(projectID)+"::uuid")
	conds = append(conds, "i.deleted_at IS NULL")
	if !in.Archived {
		conds = append(conds, "i.archived_at IS NULL")
	}
	if in.SequenceID > 0 {
		// A direct sequence lookup names the exact issue (palette
		// display-ID resolution) — it bypasses the draft filter.
		conds = append(conds, "i.sequence_id = "+arg(in.SequenceID))
	} else if in.Draft != nil && *in.Draft {
		conds = append(conds, "i.is_draft = TRUE")
	} else {
		conds = append(conds, "i.is_draft = FALSE")
	}
	if in.State != "" {
		conds = append(conds, "i.state_id = "+arg(in.State)+"::uuid")
	}
	if len(in.Priorities) > 0 {
		conds = append(conds, "i.priority = ANY("+arg(in.Priorities)+")")
	}
	if len(in.Assignees) > 0 {
		var ids []string
		wantNone := false
		for _, a := range in.Assignees {
			if a == filterNone {
				wantNone = true
			} else {
				ids = append(ids, a)
			}
		}
		var parts []string
		if len(ids) > 0 {
			parts = append(parts, "EXISTS (SELECT 1 FROM issue_assignees ia WHERE ia.issue_id = i.id AND ia.user_id = ANY("+arg(ids)+"::uuid[]))")
		}
		if wantNone {
			parts = append(parts, "NOT EXISTS (SELECT 1 FROM issue_assignees ia WHERE ia.issue_id = i.id)")
		}
		conds = append(conds, "("+strings.Join(parts, " OR ")+")")
	}
	if len(in.Labels) > 0 {
		conds = append(conds, "EXISTS (SELECT 1 FROM issue_labels il WHERE il.issue_id = i.id AND il.label_id = ANY("+arg(in.Labels)+"::uuid[]))")
	}
	if len(in.EstimatePoints) > 0 {
		var ids []string
		wantNone := false
		for _, e := range in.EstimatePoints {
			if e == filterNone {
				wantNone = true
			} else {
				ids = append(ids, e)
			}
		}
		var parts []string
		if len(ids) > 0 {
			parts = append(parts, "i.estimate_point_id = ANY("+arg(ids)+"::uuid[])")
		}
		if wantNone {
			parts = append(parts, "i.estimate_point_id IS NULL")
		}
		conds = append(conds, "("+strings.Join(parts, " OR ")+")")
	}
	if in.Cycle != "" {
		// Task 22: cycle_issues exists now — a real EXISTS filter on the
		// junction table (spec §4 schema, not a cycle_id column on
		// issues).
		conds = append(conds, "EXISTS (SELECT 1 FROM cycle_issues ci WHERE ci.issue_id = i.id AND ci.cycle_id = "+arg(in.Cycle)+"::uuid)")
	}
	if in.Q != "" {
		conds = append(conds, "i.search @@ plainto_tsquery('english', "+arg(in.Q)+")")
	}
	if in.UpdatedAfter != nil {
		conds = append(conds, "i.updated_at > "+arg(in.UpdatedAfter)+"::timestamptz")
	}
	if in.CreatedAfter != nil {
		conds = append(conds, "i.created_at >= "+arg(in.CreatedAfter)+"::timestamptz")
	}
	if in.CreatedBefore != nil {
		conds = append(conds, "i.created_at < "+arg(in.CreatedBefore)+"::timestamptz")
	}
	if in.UpdatedBefore != nil {
		conds = append(conds, "i.updated_at < "+arg(in.UpdatedBefore)+"::timestamptz")
	}
	if in.DueAfter != nil {
		conds = append(conds, "i.target_date >= "+arg(in.DueAfter)+"::date")
	}
	if in.DueBefore != nil {
		conds = append(conds, "i.target_date < "+arg(in.DueBefore)+"::date")
	}
	if in.StartAfter != nil {
		conds = append(conds, "i.start_date >= "+arg(in.StartAfter)+"::date")
	}
	if in.StartBefore != nil {
		conds = append(conds, "i.start_date < "+arg(in.StartBefore)+"::date")
	}
	if in.Undated {
		conds = append(conds, "i.target_date IS NULL AND i.start_date IS NULL")
	}
	if in.Subscribed {
		conds = append(conds, "EXISTS (SELECT 1 FROM issue_subscribers s WHERE s.issue_id = i.id AND s.user_id = "+arg(actorID)+"::uuid)")
	}
	return conds
}

// IssueListItem is one row of the list: the issue plus its aggregated
// relations — one query, never per-row lookups (Review Focus #3).
type IssueListItem struct {
	Issue
	Assignees []IssueAssignee `json:"assignees"`
	Labels    []IssueLabel    `json:"labels"`
	// ExternalRefs carries linked PRs/commits (C17T0). Populated only by
	// the detail query (getIssueRow) — list queries leave it nil and
	// omitempty keeps their responses byte-identical. The detail response
	// always embeds the array (empty when nothing is linked).
	ExternalRefs []ExternalRef `json:"external_refs,omitempty"`
	// ProjectStructure carries the cycle/module/release membership
	// (C21T2). Populated only when ListIssuesInput.IncludeProjectStructure
	// is set (?include_project_structure=1 on the list endpoint); nil
	// otherwise, and omitempty keeps every existing list response
	// byte-identical. Same shape as the detail endpoint's attachment.
	ProjectStructure *IssueProjectStructure `json:"project_structure,omitempty"`
}

// ListIssuesResult is the paginated list envelope (spec §5).
type ListIssuesResult struct {
	Issues     []IssueListItem `json:"results"`
	NextCursor string          `json:"next_cursor,omitempty"`
	// Links carries the dependency edges touching the listed issues,
	// fetched in ONE query. Populated only when the caller passes
	// ?include_links=true (the Gantt consumption contract, C4T0);
	// omitempty keeps every existing list response byte-identical.
	Links []IssueLink `json:"links,omitempty"`
}

// ListIssues returns the project's live issues with filters, cursor
// pagination, and delta sync. Assignees/labels are aggregated inside the
// single list query (correlated json_agg subqueries), so the query count
// is constant in the number of issues. Any workspace member (guest 5+)
// may read; non-members get ErrNotFound via resolveIssueProject.
func ListIssues(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string, in ListIssuesInput) (*ListIssuesResult, error) {
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return nil, err
	}

	orderKey := in.OrderBy
	if orderKey == "" {
		orderKey = "-updated_at"
	}
	ord, ok := listOrders[orderKey]
	if !ok {
		return nil, ErrInvalidOrderBy
	}

	perPage := in.PerPage
	switch {
	case perPage < 0:
		return nil, ErrInvalidListFilter
	case perPage == 0:
		perPage = defaultListPerPage
	case perPage > maxListPerPage:
		perPage = maxListPerPage
	}

	if err := validateListFilterValues(in); err != nil {
		return nil, err
	}

	var cur *listCursor
	if in.Cursor != "" {
		c, err := decodeListCursor(in.Cursor, orderKey)
		if err != nil {
			return nil, err
		}
		if !validCursorValue(ord.kind, c.Value) {
			return nil, ErrInvalidCursor
		}
		cur = &c
	}

	_, projectID, _, err := resolveIssueProject(ctx, pool, wsSlug, ident, actorID)
	if err != nil {
		return nil, err
	}

	// Sparse fieldsets (spec §5): description is only selected when
	// ?fields=description asks for it.
	wantDesc := false
	for _, f := range in.Fields {
		if strings.TrimSpace(f) == "description" {
			wantDesc = true
			break
		}
	}
	descCol := "NULL::jsonb"
	if wantDesc {
		descCol = "i.description"
	}

	// C17T2: the subtask rollup rides along in the same query (one
	// correlated aggregate per row, no extra round-trips) so list and
	// board cards can render the compact progress bar. C70T0: the
	// state-entered-at subquery rides along the same way — the export
	// (C8T3) contract demands list-shape parity, and the streaming
	// export cannot do a second batched lookup.
	cols := `i.id::text, i.project_id::text, i.sequence_id, i.name, ` + descCol + `,
		i.priority, i.state_id::text, i.parent_id::text, i.sort_order, i.start_date, i.target_date,
		i.estimate_point_id::text, i.is_draft, i.archived_at, i.created_by::text, i.created_at, i.updated_at,
		` + assigneesAgg + ` AS assignees, ` + labelsAgg + ` AS labels, ` + subtaskProgressAgg + ` AS subtask_progress, ` + stateEnteredAtAgg + ` AS state_entered_at`

	var args []any
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}

	// The WHERE fragments come from issueListConds, shared with the
	// export path (C8T3): one function, so the list and the export always
	// see the same working set for the same filters.
	conds := issueListConds(in, projectID, actorID, arg)
	if cur != nil {
		op := ">"
		if ord.desc {
			op = "<"
		}
		// Tuple comparison on (sort key, id): strictly after the cursor
		// row in the page order — new rows sorting before the cursor
		// never cause dupes or skips.
		conds = append(conds, fmt.Sprintf("(i.%s, i.id) %s (%s%s, %s::uuid)",
			strings.TrimPrefix(ord.column, "i."), op,
			arg(cur.Value), ord.kind.sqlCast(), arg(cur.ID)))
	}

	dir := "ASC"
	if ord.desc {
		dir = "DESC"
	}
	// ord.column comes from the whitelist above — never user input.
	query := `SELECT ` + cols + ` FROM issues i WHERE ` + strings.Join(conds, " AND ") +
		` ORDER BY ` + ord.column + ` ` + dir + `, i.id ` + dir +
		` LIMIT ` + arg(perPage+1)

	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// C70T0: one read instant for the whole page — every row's
	// time_in_state_seconds shares it.
	now := time.Now()
	items := []IssueListItem{}
	for rows.Next() {
		var item IssueListItem
		var desc []byte
		var assigneesJSON, labelsJSON, progressJSON []byte
		var enteredAt *time.Time
		if err := rows.Scan(
			&item.ID, &item.ProjectID, &item.SequenceID, &item.Name,
			&desc,
			&item.Priority, &item.StateID, &item.ParentID, &item.SortOrder,
			&item.StartDate, &item.TargetDate, &item.EstimatePointID,
			&item.IsDraft, &item.ArchivedAt,
			&item.CreatedBy, &item.CreatedAt, &item.UpdatedAt,
			&assigneesJSON, &labelsJSON, &progressJSON,
			&enteredAt,
		); err != nil {
			return nil, err
		}
		if desc != nil {
			item.Description = json.RawMessage(desc)
		}
		if err := unmarshalRelations(assigneesJSON, labelsJSON, &item); err != nil {
			return nil, err
		}
		if err := unmarshalSubtaskProgress(progressJSON, &item); err != nil {
			return nil, err
		}
		populateTimeInState(&item.Issue, enteredAt, now)
		item.DisplayID = ident + "-" + strconv.Itoa(item.SequenceID)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	res := &ListIssuesResult{Issues: items}
	if len(items) > perPage {
		items = items[:perPage]
		last := items[len(items)-1]
		res.Issues = items
		res.NextCursor = encodeListCursor(orderKey, listSortKey(ord, &last.Issue), last.ID)
	}
	// C21T2: ?include_project_structure=1 on the list endpoint attaches the
	// cycle/module/release membership to every returned row in ONE batched
	// query — never per-row. Off by default: no extra query, no new
	// fields, byte-identical responses.
	if in.IncludeProjectStructure && len(res.Issues) > 0 {
		ids := make([]string, 0, len(res.Issues))
		for i := range res.Issues {
			ids = append(ids, res.Issues[i].ID)
		}
		structures, err := listIssueProjectStructures(ctx, pool, projectID, ids)
		if err != nil {
			return nil, err
		}
		for i := range res.Issues {
			if s, ok := structures[res.Issues[i].ID]; ok {
				res.Issues[i].ProjectStructure = s
			} else {
				// Defensive: the ids came from this project's list query,
				// so every one should be in the map. An empty structure
				// keeps the shape identical to the detail endpoint.
				res.Issues[i].ProjectStructure = &IssueProjectStructure{Modules: []NamedRef{}}
			}
		}
	}
	return res, nil
}

// ---------- C52T0: trash list ----------

// ListDeletedIssues returns the project's soft-deleted issues only
// (deleted_at IS NOT NULL), most recently deleted first
// (deleted_at DESC, id DESC tiebreak). This deliberately differs from
// ?archived=1, which *includes* archived rows alongside live ones:
// deleted rows are excluded everywhere else, so "include deleted"
// would leak them into normal lists — trash is a separate,
// deleted-only view.
//
// Pagination mirrors the normal list (cursor + per_page, same
// per-page clamping, same ListIssuesResult envelope with
// next_cursor). The cursor is minted for order "-deleted_at" with
// the row's deleted_at as its value. All other list filters are
// ignored in trash mode: the trash is one flat recency-ordered view,
// not a filtered working set. Any workspace member (guest 5+) may
// read, same as the normal list — no role gate.
func ListDeletedIssues(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, cursor string, perPage int) (*ListIssuesResult, error) {
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return nil, err
	}

	perPage, err = deletedListPerPage(perPage)
	if err != nil {
		return nil, err
	}

	var cursorAt *time.Time
	var cursorID string
	if cursor != "" {
		c, err := decodeListCursor(cursor, "-deleted_at")
		if err != nil {
			return nil, err
		}
		t, err := time.Parse(time.RFC3339Nano, c.Value)
		if err != nil {
			return nil, ErrInvalidCursor
		}
		cursorAt = &t
		cursorID = c.ID
	}

	_, projectID, _, err := resolveIssueProject(ctx, pool, wsSlug, ident, actorID)
	if err != nil {
		return nil, err
	}

	// Same row shape as the normal list (description omitted — trash
	// mode has no ?fields= support), plus deleted_at for the
	// DeletedAt field and the next-page cursor.
	cols := `i.id::text, i.project_id::text, i.sequence_id, i.name, NULL::jsonb,
		i.priority, i.state_id::text, i.parent_id::text, i.sort_order, i.start_date, i.target_date,
		i.estimate_point_id::text, i.is_draft, i.archived_at, i.created_by::text, i.created_at, i.updated_at,
		` + assigneesAgg + ` AS assignees, ` + labelsAgg + ` AS labels, ` + subtaskProgressAgg + ` AS subtask_progress, i.deleted_at`

	var args []any
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}

	conds := []string{"i.project_id = " + arg(projectID) + "::uuid", "i.deleted_at IS NOT NULL"}
	if cursorAt != nil {
		// Tuple comparison on (deleted_at, id): strictly after the
		// cursor row in the page order — same shape as the normal
		// list's cursor predicate.
		conds = append(conds, fmt.Sprintf("(i.deleted_at, i.id) < (%s::timestamptz, %s::uuid)",
			arg(*cursorAt), arg(cursorID)))
	}

	query := `SELECT ` + cols + ` FROM issues i WHERE ` + strings.Join(conds, " AND ") +
		` ORDER BY i.deleted_at DESC, i.id DESC LIMIT ` + arg(perPage+1)

	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []IssueListItem{}
	for rows.Next() {
		var item IssueListItem
		var desc []byte
		var assigneesJSON, labelsJSON, progressJSON []byte
		var deletedAt time.Time
		if err := rows.Scan(
			&item.ID, &item.ProjectID, &item.SequenceID, &item.Name,
			&desc,
			&item.Priority, &item.StateID, &item.ParentID, &item.SortOrder,
			&item.StartDate, &item.TargetDate, &item.EstimatePointID,
			&item.IsDraft, &item.ArchivedAt,
			&item.CreatedBy, &item.CreatedAt, &item.UpdatedAt,
			&assigneesJSON, &labelsJSON, &progressJSON, &deletedAt,
		); err != nil {
			return nil, err
		}
		if desc != nil {
			item.Description = json.RawMessage(desc)
		}
		if err := unmarshalRelations(assigneesJSON, labelsJSON, &item); err != nil {
			return nil, err
		}
		if err := unmarshalSubtaskProgress(progressJSON, &item); err != nil {
			return nil, err
		}
		item.DisplayID = ident + "-" + strconv.Itoa(item.SequenceID)
		item.DeletedAt = &deletedAt
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	res := &ListIssuesResult{Issues: items}
	if len(items) > perPage {
		items = items[:perPage]
		last := items[len(items)-1]
		res.Issues = items
		res.NextCursor = encodeListCursor("-deleted_at", last.DeletedAt.UTC().Format(time.RFC3339Nano), last.ID)
	}
	return res, nil
}

// deletedListPerPage applies the normal list's per-page rule (same
// clamping as ListIssues): negative is a filter error, zero means the
// default, over-max clamps to the max.
func deletedListPerPage(perPage int) (int, error) {
	switch {
	case perPage < 0:
		return 0, ErrInvalidListFilter
	case perPage == 0:
		return defaultListPerPage, nil
	case perPage > maxListPerPage:
		return maxListPerPage, nil
	default:
		return perPage, nil
	}
}
