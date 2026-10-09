package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
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
	IsDraft         *bool
}

// hasFields reports whether the patch carries anything at all.
func (p IssuePatch) hasFields() bool {
	return p.Name != nil || p.Description.Set || p.Priority != nil ||
		p.StateID != nil || p.ParentID.Set || p.SortOrder != nil ||
		p.StartDate.Set || p.TargetDate.Set || p.EstimatePointID.Set ||
		p.IsDraft != nil
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

	// Version 1: the post-create full row. DisplayID is derived before the
	// marshal so the snapshot is complete.
	iss.DisplayID = ident + "-" + strconv.Itoa(iss.SequenceID)
	if err := snapshotVersionTx(ctx, tx, iss.ID, actorID, iss); err != nil {
		return nil, err
	}

	// Intake opt-in (Task 20): land the new issue in the inbox as pending,
	// atomically with the issue itself.
	if in.Intake {
		if err := addIntakeIssueTx(ctx, tx, projectID, iss.ID); err != nil {
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

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	announceIssueCreated(wsSlug, ident, iss)
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
	var assigneesJSON, labelsJSON []byte
	err := q.QueryRow(ctx,
		`SELECT `+issueColumns+`, `+assigneesAgg+`, `+labelsAgg+` FROM issues i
		 WHERE i.id = $1::uuid AND i.project_id = $2::uuid AND i.deleted_at IS NULL`,
		issueID, projectID).Scan(
		&item.ID, &item.ProjectID, &item.SequenceID, &item.Name,
		&description,
		&item.Priority, &item.StateID, &item.ParentID, &item.SortOrder,
		&item.StartDate, &item.TargetDate, &item.EstimatePointID,
		&item.IsDraft, &item.ArchivedAt,
		&item.CreatedBy, &item.CreatedAt, &item.UpdatedAt,
		&assigneesJSON, &labelsJSON,
	)
	if err != nil {
		return nil, err
	}
	if description != nil {
		item.Description = json.RawMessage(description)
	}
	if err := unmarshalRelations(assigneesJSON, labelsJSON, &item); err != nil {
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

	iss, notified, err := updateIssueTx(ctx, tx, projectID, ident, issueID, actorID, patch)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	announceIssueUpdated(wsSlug, ident, issueID, iss)
	announceNotifications(notified)
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
// commit via announceNotifications.
func updateIssueTx(ctx context.Context, tx pgx.Tx, projectID, ident, issueID, actorID string, patch IssuePatch) (*Issue, []*Notification, error) {
	// Lock the row and read the before-image for per-field comparison.
	old, err := scanIssue(tx.QueryRow(ctx,
		`SELECT `+issueColumns+` FROM issues
		 WHERE id = $1::uuid AND project_id = $2::uuid AND deleted_at IS NULL
		 FOR UPDATE`,
		issueID, projectID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return nil, nil, ErrIssueNotFound
		}
		return nil, nil, err
	}

	// Validate references before building the SET clause.
	var stateID *string
	if patch.StateID != nil {
		sid, err := checkStateInProject(ctx, tx, projectID, *patch.StateID)
		if err != nil {
			return nil, nil, err
		}
		stateID = &sid
	}
	var parentID PatchField[string]
	if patch.ParentID.Set {
		if patch.ParentID.Value != nil {
			pid := strings.ToLower(strings.TrimSpace(*patch.ParentID.Value))
			if pid == "" || pid == old.ID {
				return nil, nil, ErrInvalidParent
			}
			if err := checkParentIssue(ctx, tx, projectID, pid); err != nil {
				return nil, nil, err
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
				return nil, nil, err
			}
			estimatePointID = PatchField[string]{Set: true, Value: &epid}
		} else {
			estimatePointID = PatchField[string]{Set: true}
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
		return nil, nil, ErrInvalidDateRange
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
	if patch.IsDraft != nil && *patch.IsDraft != old.IsDraft {
		add("is_draft", "is_draft", "", *patch.IsDraft, old.IsDraft, *patch.IsDraft)
	}

	if len(sets) == 0 {
		// Fields were sent but nothing actually changed: return the
		// current row, no activity rows. (A patch with no fields at all
		// was already rejected by validateIssuePatch with
		// ErrNothingToUpdate.) The caller commits.
		old.DisplayID = ident + "-" + strconv.Itoa(old.SequenceID)
		return old, nil, nil
	}

	updated, err := scanIssue(tx.QueryRow(ctx,
		`UPDATE issues SET `+strings.Join(sets, ", ")+`, updated_at = now()
		 WHERE id = $1::uuid AND project_id = $2::uuid AND deleted_at IS NULL
		 RETURNING `+issueColumns,
		append([]any{issueID, projectID}, args...)...))
	if err != nil {
		return nil, nil, err
	}

	for _, a := range activities {
		if _, err := tx.Exec(ctx,
			`INSERT INTO issue_activities (issue_id, actor_id, field, old_value, new_value)
			 VALUES ($1::uuid, $2::uuid, $3, $4::jsonb, $5::jsonb)`,
			updated.ID, actorID, a.field, toJSONBParam(a.oldVal), toJSONBParam(a.newVal)); err != nil {
			return nil, nil, err
		}
	}

	updated.DisplayID = ident + "-" + strconv.Itoa(updated.SequenceID)
	// Append-only version snapshot of the post-mutation row. Rides along
	// inside the caller's tx/savepoint, so BulkUpdateIssues gets per-item
	// versions for free.
	if err := snapshotVersionTx(ctx, tx, updated.ID, actorID, updated); err != nil {
		return nil, nil, err
	}

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
	if stateChanged {
		watchers, err := issueWatchersTx(ctx, tx, issueID)
		if err != nil {
			return nil, nil, err
		}
		var stateName string
		if err := tx.QueryRow(ctx,
			`SELECT name FROM states WHERE id = $1::uuid`, updated.StateID).Scan(&stateName); err != nil {
			return nil, nil, err
		}
		actorName := actorDisplayName(ctx, tx, actorID)
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
			return nil, nil, err
		}
	}
	wsID, err := workspaceIDForProjectTx(ctx, tx, projectID)
	if err != nil {
		return nil, nil, err
	}
	if err := enqueueWebhookDeliveryTx(ctx, tx, wsID, EventIssueUpdated, map[string]any{
		"id":             updated.ID,
		"display_id":     updated.DisplayID,
		"changed_fields": changedFields,
		"actor_id":       actorID,
	}); err != nil {
		return nil, nil, err
	}
	return updated, notified, nil
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

	if err := deleteIssueTx(ctx, tx, projectID, ident, issueID, actorID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	announceIssueDeleted(wsSlug, ident, issueID)
	return nil
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
// _deleted activity row plus a version snapshot of the deleted state. It
// neither begins nor commits — callers own the transaction. Role checks
// stay with the callers.
func deleteIssueTx(ctx context.Context, tx pgx.Tx, projectID, ident, issueID, actorID string) error {
	del, err := scanIssue(tx.QueryRow(ctx,
		`UPDATE issues SET deleted_at = now(), updated_at = now()
		 WHERE id = $1::uuid AND project_id = $2::uuid AND deleted_at IS NULL
		 RETURNING `+issueColumns,
		issueID, projectID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return ErrIssueNotFound
		}
		return err
	}

	newVal, _ := json.Marshal(map[string]any{"deleted_at": time.Now().UTC()})
	if _, err := tx.Exec(ctx,
		`INSERT INTO issue_activities (issue_id, actor_id, field, new_value)
		 VALUES ($1::uuid, $2::uuid, '_deleted', $3::jsonb)`,
		del.ID, actorID, string(newVal)); err != nil {
		return err
	}

	del.DisplayID = ident + "-" + strconv.Itoa(del.SequenceID)
	if err := snapshotVersionTx(ctx, tx, del.ID, actorID, del); err != nil {
		return err
	}
	// Fan the deletion out to webhooks as issue.deleted. No in-app
	// notification: the matrix (Task 26) notifies on assign/unassign,
	// comments, state changes, and intake triage only.
	wsID, err := workspaceIDForProjectTx(ctx, tx, projectID)
	if err != nil {
		return err
	}
	if err := enqueueWebhookDeliveryTx(ctx, tx, wsID, EventIssueDeleted, map[string]any{
		"id":         del.ID,
		"display_id": del.DisplayID,
		"actor_id":   actorID,
	}); err != nil {
		return err
	}
	return nil
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
	var notified []*Notification
	for i, id := range ids {
		sp := fmt.Sprintf("bulk_item_%d", i)
		// The savepoint name is internally generated (never user input).
		if _, err := tx.Exec(ctx, "SAVEPOINT "+sp); err != nil {
			return nil, err
		}
		updated, itemNotified, err := updateIssueTx(ctx, tx, projectID, ident, id, actorID, patch)
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
		notified = append(notified, itemNotified...)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	for _, u := range announced {
		announceIssueUpdated(wsSlug, ident, u.ID, u)
	}
	announceNotifications(notified)
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
	for i, id := range ids {
		sp := fmt.Sprintf("bulk_item_%d", i)
		if _, err := tx.Exec(ctx, "SAVEPOINT "+sp); err != nil {
			return nil, err
		}
		if err := deleteIssueTx(ctx, tx, projectID, ident, id, actorID); err != nil {
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
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	for _, id := range deletedIDs {
		announceIssueDeleted(wsSlug, ident, id)
	}
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
	// Subscribed limits to issues the actor subscribed to.
	Subscribed bool
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
// for issues with no relations.
const assigneesAgg = `(SELECT COALESCE(json_agg(jsonb_build_object(
		'id', u.id::text,
		'name', COALESCE(u.name, u.email::text))
		ORDER BY COALESCE(u.name, u.email::text))::jsonb, '[]'::jsonb)
	FROM issue_assignees ia JOIN users u ON u.id = ia.user_id
	WHERE ia.issue_id = i.id)`

const labelsAgg = `(SELECT COALESCE(json_agg(jsonb_build_object(
		'id', l.id::text, 'name', l.name, 'color', l.color)
		ORDER BY l.name)::jsonb, '[]'::jsonb)
	FROM issue_labels il JOIN labels l ON l.id = il.label_id
	WHERE il.issue_id = i.id)`

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

// IssueListItem is one row of the list: the issue plus its aggregated
// relations — one query, never per-row lookups (Review Focus #3).
type IssueListItem struct {
	Issue
	Assignees []IssueAssignee `json:"assignees"`
	Labels    []IssueLabel    `json:"labels"`
}

// ListIssuesResult is the paginated list envelope (spec §5).
type ListIssuesResult struct {
	Issues     []IssueListItem `json:"results"`
	NextCursor string          `json:"next_cursor,omitempty"`
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

	for _, p := range in.Priorities {
		if p < 0 || p > 4 {
			return nil, ErrInvalidListFilter
		}
	}
	for _, f := range []string{in.State, in.Cycle} {
		if f != "" && !isUUIDFormat(f) {
			return nil, ErrInvalidListFilter
		}
	}
	// Multi-value UUID filters; Assignees/EstimatePoints also accept the
	// "none" sentinel (validated separately below).
	for _, id := range in.Labels {
		if !isUUIDFormat(id) {
			return nil, ErrInvalidListFilter
		}
	}
	for _, id := range in.Assignees {
		if id != filterNone && !isUUIDFormat(id) {
			return nil, ErrInvalidListFilter
		}
	}
	for _, id := range in.EstimatePoints {
		if id != filterNone && !isUUIDFormat(id) {
			return nil, ErrInvalidListFilter
		}
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

	cols := `i.id::text, i.project_id::text, i.sequence_id, i.name, ` + descCol + `,
		i.priority, i.state_id::text, i.parent_id::text, i.sort_order, i.start_date, i.target_date,
		i.estimate_point_id::text, i.is_draft, i.archived_at, i.created_by::text, i.created_at, i.updated_at,
		` + assigneesAgg + ` AS assignees, ` + labelsAgg + ` AS labels`

	var conds []string
	var args []any
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}

	conds = append(conds, "i.project_id = "+arg(projectID)+"::uuid")
	conds = append(conds, "i.deleted_at IS NULL")
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
	if in.Subscribed {
		conds = append(conds, "EXISTS (SELECT 1 FROM issue_subscribers s WHERE s.issue_id = i.id AND s.user_id = "+arg(actorID)+"::uuid)")
	}
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

	items := []IssueListItem{}
	for rows.Next() {
		var item IssueListItem
		var desc []byte
		var assigneesJSON, labelsJSON []byte
		if err := rows.Scan(
			&item.ID, &item.ProjectID, &item.SequenceID, &item.Name,
			&desc,
			&item.Priority, &item.StateID, &item.ParentID, &item.SortOrder,
			&item.StartDate, &item.TargetDate, &item.EstimatePointID,
			&item.IsDraft, &item.ArchivedAt,
			&item.CreatedBy, &item.CreatedAt, &item.UpdatedAt,
			&assigneesJSON, &labelsJSON,
		); err != nil {
			return nil, err
		}
		if desc != nil {
			item.Description = json.RawMessage(desc)
		}
		if err := unmarshalRelations(assigneesJSON, labelsJSON, &item); err != nil {
			return nil, err
		}
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
	return res, nil
}
