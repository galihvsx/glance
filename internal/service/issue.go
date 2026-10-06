package service

import (
	"bytes"
	"context"
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
	Name        string
	Description json.RawMessage
	Priority    *int
	StateID     *string
	ParentID    *string
	SortOrder   *float64
	StartDate   *time.Time
	TargetDate  *time.Time
	IsDraft     bool
}

// IssuePatch is a partial issue update: nil / Set=false fields are left
// untouched, so PATCH carries true partial-update semantics. A patch with
// no fields at all is ErrNothingToUpdate.
type IssuePatch struct {
	Name        *string
	Description PatchField[json.RawMessage]
	Priority    *int
	StateID     *string
	ParentID    PatchField[string]
	SortOrder   *float64
	StartDate   PatchField[time.Time]
	TargetDate  PatchField[time.Time]
	IsDraft     *bool
}

// hasFields reports whether the patch carries anything at all.
func (p IssuePatch) hasFields() bool {
	return p.Name != nil || p.Description.Set || p.Priority != nil ||
		p.StateID != nil || p.ParentID.Set || p.SortOrder != nil ||
		p.StartDate.Set || p.TargetDate.Set || p.IsDraft != nil
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

	_, projectID, role, err := resolveIssueProject(ctx, tx, wsSlug, ident, actorID)
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
			state_id, parent_id, sort_order, start_date, target_date, is_draft, created_by)
		 VALUES ($1::uuid, $2, $3, $4::jsonb, $5, $6::uuid, $7::uuid, $8, $9, $10, $11, $12::uuid)
		 RETURNING `+issueColumns,
		projectID, seq, name, descParam, priority, stateID, parentID,
		sortOrder, in.StartDate, in.TargetDate, in.IsDraft, actorID))
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

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	iss.DisplayID = ident + "-" + strconv.Itoa(iss.SequenceID)
	return iss, nil
}

// GetIssue returns one live issue by UUID. Any workspace member (guest 5+)
// may read; non-members get ErrNotFound, a missing UUID ErrIssueNotFound.
func GetIssue(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, issueID, actorID string) (*Issue, error) {
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return nil, err
	}
	_, projectID, _, err := resolveIssueProject(ctx, pool, wsSlug, ident, actorID)
	if err != nil {
		return nil, err
	}

	iss, err := scanIssue(pool.QueryRow(ctx,
		`SELECT `+issueColumns+` FROM issues
		 WHERE id = $1::uuid AND project_id = $2::uuid AND deleted_at IS NULL`,
		issueID, projectID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return nil, ErrIssueNotFound
		}
		return nil, err
	}
	iss.DisplayID = ident + "-" + strconv.Itoa(iss.SequenceID)
	return iss, nil
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
	if !patch.hasFields() {
		return nil, ErrNothingToUpdate
	}
	if patch.Name != nil && strings.TrimSpace(*patch.Name) == "" {
		return nil, ErrNameRequired
	}
	if patch.Priority != nil && (*patch.Priority < 0 || *patch.Priority > 4) {
		return nil, ErrInvalidPriority
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

	// Lock the row and read the before-image for per-field comparison.
	old, err := scanIssue(tx.QueryRow(ctx,
		`SELECT `+issueColumns+` FROM issues
		 WHERE id = $1::uuid AND project_id = $2::uuid AND deleted_at IS NULL
		 FOR UPDATE`,
		issueID, projectID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return nil, ErrIssueNotFound
		}
		return nil, err
	}

	// Validate references before building the SET clause.
	var stateID *string
	if patch.StateID != nil {
		sid, err := checkStateInProject(ctx, tx, projectID, *patch.StateID)
		if err != nil {
			return nil, err
		}
		stateID = &sid
	}
	var parentID PatchField[string]
	if patch.ParentID.Set {
		if patch.ParentID.Value != nil {
			pid := strings.ToLower(strings.TrimSpace(*patch.ParentID.Value))
			if pid == "" || pid == old.ID {
				return nil, ErrInvalidParent
			}
			if err := checkParentIssue(ctx, tx, projectID, pid); err != nil {
				return nil, err
			}
			parentID = PatchField[string]{Set: true, Value: &pid}
		} else {
			parentID = PatchField[string]{Set: true}
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
		return nil, ErrInvalidDateRange
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
	if patch.IsDraft != nil && *patch.IsDraft != old.IsDraft {
		add("is_draft", "is_draft", "", *patch.IsDraft, old.IsDraft, *patch.IsDraft)
	}

	if len(sets) == 0 {
		// Fields were sent but nothing actually changed: return the
		// current row, no activity rows. (A patch with no fields at all
		// was already rejected above with ErrNothingToUpdate.)
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		old.DisplayID = ident + "-" + strconv.Itoa(old.SequenceID)
		return old, nil
	}

	updated, err := scanIssue(tx.QueryRow(ctx,
		`UPDATE issues SET `+strings.Join(sets, ", ")+`, updated_at = now()
		 WHERE id = $1::uuid AND project_id = $2::uuid AND deleted_at IS NULL
		 RETURNING `+issueColumns,
		append([]any{issueID, projectID}, args...)...))
	if err != nil {
		return nil, err
	}

	for _, a := range activities {
		if _, err := tx.Exec(ctx,
			`INSERT INTO issue_activities (issue_id, actor_id, field, old_value, new_value)
			 VALUES ($1::uuid, $2::uuid, $3, $4::jsonb, $5::jsonb)`,
			updated.ID, actorID, a.field, toJSONBParam(a.oldVal), toJSONBParam(a.newVal)); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	updated.DisplayID = ident + "-" + strconv.Itoa(updated.SequenceID)
	return updated, nil
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

	var id string
	err = tx.QueryRow(ctx,
		`UPDATE issues SET deleted_at = now(), updated_at = now()
		 WHERE id = $1::uuid AND project_id = $2::uuid AND deleted_at IS NULL
		 RETURNING id::text`,
		issueID, projectID).Scan(&id)
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
		id, actorID, string(newVal)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
