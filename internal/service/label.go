package service

// Labels, issue↔label assignment, assignees, and estimate scales
// (Task 16, spec §4).
//
// labels are WORKSPACE-scoped per spec §4 (workspace_id FK): one label
// taxonomy shared by every project in the workspace. The API nests the
// label routes under /projects/{identifier}/labels for Plane-style
// ergonomics and membership resolution, but the rows belong to the
// workspace — a label created via project ENG is visible to (and
// assignable from) every project in the workspace.
// name is unique per workspace; parent_id forms the hierarchy.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrLabelNotFound is returned when the label id matches nothing in
	// the workspace. Deliberately distinct from ErrNotFound: on this path
	// the workspace and project are confirmed and the caller is a member.
	ErrLabelNotFound = errors.New("service: label not found")
	// ErrLabelConflict is returned when the label name is already taken
	// in the workspace (UNIQUE(workspace_id, name)).
	ErrLabelConflict = errors.New("service: label name already exists")
	// ErrLabelCycle is returned when a parent change would introduce a
	// cycle in the label hierarchy (including self-parenting).
	ErrLabelCycle = errors.New("service: label parent would create a cycle")
	// ErrInvalidLabel is returned for malformed label input: bad parent
	// id, parent in another workspace, or bad color.
	ErrInvalidLabel = errors.New("service: invalid label")
	// ErrEstimateNotFound is returned when the estimate id matches
	// nothing in the project.
	ErrEstimateNotFound = errors.New("service: estimate not found")
	// ErrEstimateConflict is returned when the estimate name is already
	// taken in the project (UNIQUE(project_id, name)).
	ErrEstimateConflict = errors.New("service: estimate name already exists")
	// ErrInvalidEstimatePoint is returned when an estimate point id
	// matches nothing, belongs to another project's scale, or the scale
	// input itself is malformed (empty key, duplicate keys, no points).
	ErrInvalidEstimatePoint = errors.New("service: invalid estimate point")
	// ErrAssigneeNotMember is returned when the assignee is not a member
	// of the workspace.
	ErrAssigneeNotMember = errors.New("service: assignee is not a workspace member")
)

// labelColorRe pins colors to #rrggbb so the web client can render them
// blindly as CSS colors.
var labelColorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

const defaultLabelColor = "#6b7280"

// Label is one workspace-scoped tag. ParentID links the hierarchy;
// nil means a root label.
type Label struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	ParentID    *string   `json:"parent_id,omitempty"`
	Name        string    `json:"name"`
	Color       string    `json:"color"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// LabelInput carries label creation fields.
type LabelInput struct {
	Name     string
	Color    *string
	ParentID *string
}

// LabelPatch is a partial label update. ParentID is tri-state: unset
// leaves the parent untouched, set-with-nil clears it, set-with-value
// reparents (cycle-checked).
type LabelPatch struct {
	Name     *string
	Color    *string
	ParentID PatchField[string]
}

func (p LabelPatch) hasFields() bool {
	return p.Name != nil || p.Color != nil || p.ParentID.Set
}

const labelColumns = `id::text, workspace_id::text, parent_id::text, name, color, created_at, updated_at`

func scanLabel(row pgx.Row) (*Label, error) {
	var l Label
	if err := row.Scan(&l.ID, &l.WorkspaceID, &l.ParentID, &l.Name, &l.Color, &l.CreatedAt, &l.UpdatedAt); err != nil {
		return nil, err
	}
	return &l, nil
}

// resolveLabelScope resolves the workspace membership via the
// project-nested route: non-members get ErrNotFound (the tenancy
// boundary), members asking for a missing identifier ErrProjectNotFound.
// Labels belong to the returned workspace.
func resolveLabelScope(ctx context.Context, q queryRower, wsSlug, identifier, actorID string) (string, int, error) {
	wsID, _, role, err := resolveIssueProject(ctx, q, wsSlug, identifier, actorID)
	return wsID, role, err
}

// checkLabelParent validates that parentID names a label of this
// workspace and returns its canonical form.
func checkLabelParent(ctx context.Context, q queryRower, wsID, parentID string) (string, error) {
	pid := strings.ToLower(strings.TrimSpace(parentID))
	var owner string
	err := q.QueryRow(ctx,
		`SELECT workspace_id::text FROM labels WHERE id = $1::uuid`, pid).Scan(&owner)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return "", ErrInvalidLabel
		}
		return "", err
	}
	if owner != wsID {
		return "", ErrInvalidLabel
	}
	return pid, nil
}

// labelWouldCycle reports whether setting labelID's parent to newParentID
// would create a cycle: it walks the ancestor chain of newParentID and
// checks whether labelID appears. Self-parenting is a cycle of length 1.
func labelWouldCycle(ctx context.Context, q queryRower, labelID, newParentID string) (bool, error) {
	var hit bool
	err := q.QueryRow(ctx,
		`WITH RECURSIVE chain(id) AS (
			 SELECT $1::uuid
			 UNION
			 SELECT l.parent_id FROM labels l JOIN chain c ON l.id = c.id
			 WHERE l.parent_id IS NOT NULL
		 )
		 SELECT EXISTS(SELECT 1 FROM chain WHERE id = $2::uuid)`,
		newParentID, labelID).Scan(&hit)
	return hit, err
}

func normalizeLabelColor(in *string) (string, error) {
	if in == nil || strings.TrimSpace(*in) == "" {
		return defaultLabelColor, nil
	}
	c := strings.TrimSpace(*in)
	if !labelColorRe.MatchString(c) {
		return "", ErrInvalidLabel
	}
	return c, nil
}

// CreateLabel inserts a workspace label. The caller must be a workspace
// member (15) or admin (20); guests get ErrForbidden, non-members
// ErrNotFound. Duplicate names in the workspace are ErrLabelConflict.
func CreateLabel(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string, in LabelInput) (*Label, error) {
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 120 {
		return nil, ErrNameRequired
	}
	color, err := normalizeLabelColor(in.Color)
	if err != nil {
		return nil, err
	}

	wsID, role, err := resolveLabelScope(ctx, pool, wsSlug, ident, actorID)
	if err != nil {
		return nil, err
	}
	if role < RoleMember {
		return nil, ErrForbidden
	}

	var parentID *string
	if in.ParentID != nil {
		pid, err := checkLabelParent(ctx, pool, wsID, *in.ParentID)
		if err != nil {
			return nil, err
		}
		parentID = &pid
	}

	l, err := scanLabel(pool.QueryRow(ctx,
		`INSERT INTO labels (workspace_id, parent_id, name, color)
		 VALUES ($1::uuid, $2::uuid, $3, $4)
		 RETURNING `+labelColumns,
		wsID, parentID, name, color))
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrLabelConflict
		}
		return nil, err
	}
	return l, nil
}

// ListLabels returns the workspace's labels ordered by name. Any
// workspace member (guest 5+) may read.
func ListLabels(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string) ([]Label, error) {
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return nil, err
	}
	wsID, _, err := resolveLabelScope(ctx, pool, wsSlug, ident, actorID)
	if err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx,
		`SELECT `+labelColumns+` FROM labels WHERE workspace_id = $1::uuid ORDER BY name`, wsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Label{}
	for rows.Next() {
		l, err := scanLabel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *l)
	}
	return out, rows.Err()
}

// GetLabel returns one label of the workspace. A foreign id reads as
// ErrLabelNotFound — never "workspace not found".
func GetLabel(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, labelID, actorID string) (*Label, error) {
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return nil, err
	}
	wsID, _, err := resolveLabelScope(ctx, pool, wsSlug, ident, actorID)
	if err != nil {
		return nil, err
	}
	l, err := scanLabel(pool.QueryRow(ctx,
		`SELECT `+labelColumns+` FROM labels WHERE id = $1::uuid AND workspace_id = $2::uuid`,
		labelID, wsID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return nil, ErrLabelNotFound
		}
		return nil, err
	}
	return l, nil
}

// UpdateLabel applies a partial label update. Renaming to an existing
// name is ErrLabelConflict; reparenting is cycle-checked
// (ErrLabelCycle); a patch with no fields is ErrNothingToUpdate.
// Member (15)+; guests ErrForbidden.
func UpdateLabel(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, labelID, actorID string, patch LabelPatch) (*Label, error) {
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return nil, err
	}
	if !patch.hasFields() {
		return nil, ErrNothingToUpdate
	}
	if patch.Name != nil {
		if n := strings.TrimSpace(*patch.Name); n == "" || len(n) > 120 {
			return nil, ErrNameRequired
		}
	}
	var color *string
	if patch.Color != nil {
		c, err := normalizeLabelColor(patch.Color)
		if err != nil {
			return nil, err
		}
		color = &c
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	wsID, role, err := resolveLabelScope(ctx, tx, wsSlug, ident, actorID)
	if err != nil {
		return nil, err
	}
	if role < RoleMember {
		return nil, ErrForbidden
	}

	old, err := scanLabel(tx.QueryRow(ctx,
		`SELECT `+labelColumns+` FROM labels
		 WHERE id = $1::uuid AND workspace_id = $2::uuid FOR UPDATE`,
		labelID, wsID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return nil, ErrLabelNotFound
		}
		return nil, err
	}

	var parent PatchField[string]
	if patch.ParentID.Set {
		if patch.ParentID.Value != nil {
			pid, err := checkLabelParent(ctx, tx, wsID, *patch.ParentID.Value)
			if err != nil {
				return nil, err
			}
			if cycle, err := labelWouldCycle(ctx, tx, old.ID, pid); err != nil {
				return nil, err
			} else if cycle {
				return nil, ErrLabelCycle
			}
			parent = PatchField[string]{Set: true, Value: &pid}
		} else {
			parent = PatchField[string]{Set: true}
		}
	}

	var sets []string
	var args []any
	add := func(column, cast string, arg any) {
		args = append(args, arg)
		// args[0]/args[1] are reserved for id/workspace_id in the WHERE
		// clause, hence the +2 offset (same trick as UpdateIssue).
		sets = append(sets, fmt.Sprintf("%s = $%d%s", column, len(args)+2, cast))
	}
	if patch.Name != nil {
		name := strings.TrimSpace(*patch.Name)
		if name != old.Name {
			add("name", "", name)
		}
	}
	if color != nil && *color != old.Color {
		add("color", "", *color)
	}
	if parent.Set && !strPtrEq(old.ParentID, parent.Value) {
		add("parent_id", "::uuid", parent.Value)
	}

	if len(sets) == 0 {
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return old, nil
	}

	updated, err := scanLabel(tx.QueryRow(ctx,
		`UPDATE labels SET `+strings.Join(sets, ", ")+`, updated_at = now()
		 WHERE id = $1::uuid AND workspace_id = $2::uuid
		 RETURNING `+labelColumns,
		append([]any{old.ID, wsID}, args...)...))
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrLabelConflict
		}
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return updated, nil
}

// DeleteLabel removes a workspace label. Its issue assignments cascade;
// its children are promoted to roots (parent_id SET NULL). Member (15)+.
func DeleteLabel(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, labelID, actorID string) error {
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return err
	}
	wsID, role, err := resolveLabelScope(ctx, pool, wsSlug, ident, actorID)
	if err != nil {
		return err
	}
	if role < RoleMember {
		return ErrForbidden
	}
	ct, err := pool.Exec(ctx,
		`DELETE FROM labels WHERE id = $1::uuid AND workspace_id = $2::uuid`,
		labelID, wsID)
	if err != nil {
		if isInvalidUUID(err) {
			return ErrLabelNotFound
		}
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrLabelNotFound
	}
	return nil
}

// resolveIssueForTaxonomy resolves the project and one live issue in it:
// the shared preamble for label/assignee assignment. Returns wsID,
// projectID, role.
func resolveIssueForTaxonomy(ctx context.Context, q queryRower, wsSlug, identifier, issueID, actorID string) (wsID, projectID string, role int, err error) {
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return "", "", 0, err
	}
	wsID, projectID, role, err = resolveIssueProject(ctx, q, wsSlug, ident, actorID)
	if err != nil {
		return "", "", 0, err
	}
	var one int
	err = q.QueryRow(ctx,
		`SELECT 1 FROM issues
		 WHERE id = $1::uuid AND project_id = $2::uuid AND deleted_at IS NULL`,
		issueID, projectID).Scan(&one)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return "", "", 0, ErrIssueNotFound
		}
		return "", "", 0, err
	}
	return wsID, projectID, role, nil
}

// issueBeforeImageInTx reads the full issue row inside tx, as the
// before-image for the cascade detector and the version snapshot. The
// second return reports whether the read succeeded; a failed read must
// never fail the mutation — the caller proceeds, the cascade detector
// in announceLabelChange simply skips the objective fan-out, and no
// snapshot is taken (there is nothing to compare against).
func issueBeforeImageInTx(ctx context.Context, tx pgx.Tx, issueID string) (*Issue, bool) {
	iss, err := scanIssue(tx.QueryRow(ctx,
		`SELECT `+issueColumns+` FROM issues WHERE id = $1::uuid`, issueID))
	if err != nil {
		return nil, false
	}
	return iss, true
}

// snapshotCascadeVersionTx re-reads the issue row FOR UPDATE in-tx
// AFTER the automation firing and snapshots it iff it differs from the
// before-image. The comparison IS the guard: idempotent no-ops (0
// rows), firings whose actions are all no-ops, and automationActive
// nesting (no firing runs) all leave the row unchanged, so none of
// them snapshots. A snapshot lands in the same tx and rolls back with
// it — never post-commit. The snapshot carries the derived DisplayID,
// like every other snapshot call site. The bool return reports whether
// the snapshot fired (C65T0): callers that owe a membership version on
// a real set change but no cascade movement use it to avoid
// double-snapshotting.
func snapshotCascadeVersionTx(ctx context.Context, tx pgx.Tx, issueID, actorID, ident string, before *Issue) (bool, error) {
	after, err := scanIssue(tx.QueryRow(ctx,
		`SELECT `+issueColumns+` FROM issues WHERE id = $1::uuid FOR UPDATE`, issueID))
	if err != nil {
		return false, err
	}
	if issueRowContentEqual(before, after) {
		return false, nil
	}
	after.DisplayID = ident + "-" + strconv.Itoa(after.SequenceID)
	if err := snapshotVersionTx(ctx, tx, issueID, actorID, after); err != nil {
		return false, err
	}
	return true, nil
}

// snapshotMembershipVersionTx writes exactly one version after a
// label/assignee membership mutation (C65T0). If the automation
// cascade moved the issue row, that snapshot already landed (via
// snapshotCascadeVersionTx, fired=true) and nothing more is written —
// never a double-snapshot. Otherwise, on a REAL set change (changed),
// the post-mutation row is snapshotted once with the derived
// DisplayID, like every other snapshot call site. Idempotent no-ops
// (changed=false) and a missing before-image (haveBefore=false,
// nothing to compare against) write nothing. The snapshot lands in
// the same tx and rolls back with it — never post-commit.
func snapshotMembershipVersionTx(ctx context.Context, tx pgx.Tx, issueID, actorID, ident string, before *Issue, haveBefore, changed bool) error {
	if !haveBefore || !changed {
		return nil
	}
	fired, err := snapshotCascadeVersionTx(ctx, tx, issueID, actorID, ident, before)
	if err != nil {
		return err
	}
	if fired {
		return nil
	}
	after, err := scanIssue(tx.QueryRow(ctx,
		`SELECT `+issueColumns+` FROM issues WHERE id = $1::uuid FOR UPDATE`, issueID))
	if err != nil {
		return err
	}
	after.DisplayID = ident + "-" + strconv.Itoa(after.SequenceID)
	return snapshotVersionTx(ctx, tx, issueID, actorID, after)
}

// issueRowChangedFields diffs two scanned issue rows and returns the
// changed_fields names (the updateIssueTx vocabulary) for every tracked
// row column that differs. It is the cascade half of the C56T1 webhook
// payload: the direct change ("labels"/"assignees") is never a row
// column, so it can never appear here and can never collide with it.
// Identity / derived / timestamp columns are excluded, mirroring
// issueRowContentEqual: ID, ProjectID, SequenceID and CreatedBy never
// change on these paths; CreatedAt/UpdatedAt are timestamps, not
// content; DisplayID is derived; SubtaskProgress/DeletedAt are never
// populated by scanIssue. ArchivedAt maps to "archived", the activity
// field name updateIssueTx uses for the archived_at toggle. The diff
// is computed from the rows themselves, never from a firing's return —
// a firing that reports success but changed nothing (or vice versa)
// cannot skew changed_fields.
func issueRowChangedFields(before, after *Issue) []string {
	if before == nil || after == nil {
		return nil
	}
	var fields []string
	if before.Name != after.Name {
		fields = append(fields, "name")
	}
	if !bytes.Equal(before.Description, after.Description) {
		fields = append(fields, "description")
	}
	if before.Priority != after.Priority {
		fields = append(fields, "priority")
	}
	if before.StateID != after.StateID {
		fields = append(fields, "state_id")
	}
	if !strPtrEq(before.ParentID, after.ParentID) {
		fields = append(fields, "parent_id")
	}
	if before.SortOrder != after.SortOrder {
		fields = append(fields, "sort_order")
	}
	if !datePtrEq(before.StartDate, after.StartDate) {
		fields = append(fields, "start_date")
	}
	if !datePtrEq(before.TargetDate, after.TargetDate) {
		fields = append(fields, "target_date")
	}
	if !strPtrEq(before.EstimatePointID, after.EstimatePointID) {
		fields = append(fields, "estimate_point_id")
	}
	if before.IsDraft != after.IsDraft {
		fields = append(fields, "is_draft")
	}
	if !archivedAtEq(before.ArchivedAt, after.ArchivedAt) {
		fields = append(fields, "archived")
	}
	return fields
}

// archivedAtEq reports whether two archived_at values are equal: both
// NULL, or both set to the same instant. datePtrEq is wrong here —
// archived_at is a timestamp, not a DATE column, so same-day
// different-time stamps must still count as a change.
func archivedAtEq(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}

// enqueueLabelWebhookTx enqueues exactly ONE issue.updated webhook
// in-tx for a label/assignee attach/detach (C56T1). changed is the same
// RowsAffected() > 0 condition that gates announceLabelChange: an
// idempotent no-op re-attach/detach enqueues nothing. changed_fields
// is the direct change (directField: "labels" or "assignees") plus the
// automation-cascade row diff (issueRowChangedFields over the
// before-image vs the post-firing row re-read in-tx). The payload
// matches the restoreIssueTx shape: {id, display_id, changed_fields,
// actor_id}. The enqueue sits in-tx, before commit, so a rolled-back
// mutation never enqueues — and it is NOT also done from
// announceLabelChange (post-commit, wrong layer). These four functions
// previously enqueued zero webhooks, so this one enqueue is exactly-one
// by construction. When the before-image read failed (haveBefore
// false), the cascade diff is skipped but the direct-change webhook
// still goes out with the display_id from the re-read row.
func enqueueLabelWebhookTx(ctx context.Context, tx pgx.Tx, wsID, ident, issueID, actorID, directField string, before *Issue, haveBefore, changed bool) error {
	if !changed {
		return nil
	}
	after, err := scanIssue(tx.QueryRow(ctx,
		`SELECT `+issueColumns+` FROM issues WHERE id = $1::uuid`, issueID))
	if err != nil {
		return err
	}
	changedFields := []string{directField}
	if haveBefore {
		changedFields = append(changedFields, issueRowChangedFields(before, after)...)
	}
	return enqueueWebhookDeliveryTx(ctx, tx, wsID, EventIssueUpdated, map[string]any{
		"id":             issueID,
		"display_id":     ident + "-" + strconv.Itoa(after.SequenceID),
		"changed_fields": changedFields,
		"actor_id":       actorID,
	})
}

// announceLabelChange announces issue.updated after a committed
// label/assignee attach/detach (C46T1). changed is the firing's
// RowsAffected() > 0: idempotent no-ops (0 rows) announce nothing, and
// the announce sits strictly after tx.Commit, so a failed commit
// announces nothing. The snapshot is read post-commit via pool (never
// the in-tx row): deleted_at IS NULL + project filter, best-effort
// skip on lookup failure. When the in-tx automation firing moved the
// issue (beforeStateID != snapshot state_id — only set_state touches
// state_id), linked objectives get objective.updated too. Exactly one
// issue.updated per call, even when the firing cascades.
func announceLabelChange(ctx context.Context, pool *pgxpool.Pool, wsSlug, ident, projectID, issueID, beforeStateID string, haveBefore, changed bool) {
	if !changed {
		return
	}
	iss, err := scanIssue(pool.QueryRow(ctx,
		`SELECT `+issueColumns+` FROM issues
		  WHERE id = $1::uuid AND project_id = $2::uuid AND deleted_at IS NULL`,
		issueID, projectID))
	if err != nil {
		return
	}
	announceIssueUpdated(wsSlug, ident, issueID, iss)
	if haveBefore && iss.StateID != beforeStateID {
		announceObjectivesForIssue(ctx, pool, wsSlug, issueID)
	}
}

// AssignLabel attaches a workspace label to an issue. Idempotent:
// assigning twice succeeds with no duplicate row. The label must belong
// to the workspace (a foreign label is ErrLabelNotFound). Member (15)+.
// C16T0: the attach and its issue.labels_changed automation evaluation
// share one tx, so a rolled-back attach can never fire a rule; the
// idempotent re-attach (0 rows) fires nothing. C46T1: a real attach
// announces issue.updated after commit (+ objective.updated if a
// firing's set_state moved the issue); a no-op attach announces
// nothing.
func AssignLabel(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, issueID, labelID, actorID string) error {
	wsID, projectID, role, err := resolveIssueForTaxonomy(ctx, pool, wsSlug, identifier, issueID, actorID)
	if err != nil {
		return err
	}
	if role < RoleMember {
		return ErrForbidden
	}
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return err
	}
	lid := strings.ToLower(strings.TrimSpace(labelID))

	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// C46T1: pre-mutation before-image for the cascade detector (only
	// set_state touches state_id, so before != after means exactly a
	// real cascade move) and the C50T1 cascade version snapshot.
	before, haveBefore := issueBeforeImageInTx(ctx, tx, issueID)
	beforeStateID := ""
	if haveBefore {
		beforeStateID = before.StateID
	}

	var owner string
	err = tx.QueryRow(ctx,
		`SELECT workspace_id::text FROM labels WHERE id = $1::uuid`, lid).Scan(&owner)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return ErrLabelNotFound
		}
		return err
	}
	if owner != wsID {
		return ErrLabelNotFound
	}
	tag, err := tx.Exec(ctx,
		`INSERT INTO issue_labels (issue_id, label_id) VALUES ($1::uuid, $2::uuid)
		 ON CONFLICT DO NOTHING`,
		issueID, lid)
	if err != nil {
		return err
	}
	var notified []*Notification
	if tag.RowsAffected() > 0 && !automationActive(ctx) {
		notified = runAutomationLabelsChangedTx(ctx, tx, wsID, projectID, ident, issueID, actorID,
			[]string{lid}, nil)
	}
	changed := tag.RowsAffected() > 0
	// C50T1 + C65T0: exactly one version per real membership change --
	// the cascade snapshot iff the firing moved the row, else one
	// snapshot of the post-mutation row. No-ops snapshot nothing.
	if err := snapshotMembershipVersionTx(ctx, tx, issueID, actorID, ident, before, haveBefore, changed); err != nil {
		return err
	}
	// C56T1: exactly one issue.updated webhook in-tx for a real attach
	// (direct "labels" + any cascade row diff).
	if err := enqueueLabelWebhookTx(ctx, tx, wsID, ident, issueID, actorID, "labels", before, haveBefore, changed); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	announceNotifications(notified)
	announceLabelChange(ctx, pool, wsSlug, ident, projectID, issueID, beforeStateID, haveBefore, changed)
	return nil
}

// UnassignLabel detaches a label from an issue. Idempotent: unassigning
// an absent label succeeds silently. Member (15)+. C16T0: the detach and
// its issue.labels_changed automation evaluation share one tx, so a
// rolled-back detach can never fire a rule; detaching an absent label
// (0 rows) fires nothing. C46T1: a real detach announces issue.updated
// after commit (+ objective.updated on a cascade set_state move); a
// no-op detach announces nothing.
func UnassignLabel(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, issueID, labelID, actorID string) error {
	wsID, projectID, role, err := resolveIssueForTaxonomy(ctx, pool, wsSlug, identifier, issueID, actorID)
	if err != nil {
		return err
	}
	if role < RoleMember {
		return ErrForbidden
	}
	if !isUUIDFormat(strings.TrimSpace(labelID)) {
		// Malformed ids match nothing — the idempotent no-op answer.
		return nil
	}
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return err
	}
	lid := strings.ToLower(strings.TrimSpace(labelID))

	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// C46T1: pre-mutation before-image for the cascade detector and
	// the C50T1 cascade version snapshot.
	before, haveBefore := issueBeforeImageInTx(ctx, tx, issueID)
	beforeStateID := ""
	if haveBefore {
		beforeStateID = before.StateID
	}

	tag, err := tx.Exec(ctx,
		`DELETE FROM issue_labels WHERE issue_id = $1::uuid AND label_id = $2::uuid`,
		issueID, lid)
	if err != nil {
		return err
	}
	var notified []*Notification
	if tag.RowsAffected() > 0 && !automationActive(ctx) {
		notified = runAutomationLabelsChangedTx(ctx, tx, wsID, projectID, ident, issueID, actorID,
			nil, []string{lid})
	}
	changed := tag.RowsAffected() > 0
	// C50T1 + C65T0: exactly one version per real membership change --
	// the cascade snapshot iff the firing moved the row, else one
	// snapshot of the post-mutation row. No-ops snapshot nothing.
	if err := snapshotMembershipVersionTx(ctx, tx, issueID, actorID, ident, before, haveBefore, changed); err != nil {
		return err
	}
	// C56T1: exactly one issue.updated webhook in-tx for a real detach
	// (direct "labels" + any cascade row diff).
	if err := enqueueLabelWebhookTx(ctx, tx, wsID, ident, issueID, actorID, "labels", before, haveBefore, changed); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	announceNotifications(notified)
	announceLabelChange(ctx, pool, wsSlug, ident, projectID, issueID, beforeStateID, haveBefore, changed)
	return nil
}

// AssignAssignee assigns a workspace member to an issue. Idempotent.
// The assignee must be a member of the workspace (any role — guests can
// be assigned work). Member (15)+ to mutate. C46T1: a real assignment
// announces issue.updated after commit (+ objective.updated on a
// cascade set_state move); an idempotent re-assign announces nothing.
func AssignAssignee(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, issueID, userID, actorID string) error {
	wsID, projectID, role, err := resolveIssueForTaxonomy(ctx, pool, wsSlug, identifier, issueID, actorID)
	if err != nil {
		return err
	}
	if role < RoleMember {
		return ErrForbidden
	}
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return err
	}
	uid := strings.ToLower(strings.TrimSpace(userID))

	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// C46T1: pre-mutation before-image for the cascade detector and
	// the C50T1 cascade version snapshot.
	before, haveBefore := issueBeforeImageInTx(ctx, tx, issueID)
	beforeStateID := ""
	if haveBefore {
		beforeStateID = before.StateID
	}

	var one int
	err = tx.QueryRow(ctx,
		`SELECT 1 FROM workspace_members WHERE workspace_id = $1::uuid AND user_id = $2::uuid`,
		wsID, uid).Scan(&one)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return ErrAssigneeNotMember
		}
		return err
	}
	tag, err := tx.Exec(ctx,
		`INSERT INTO issue_assignees (issue_id, user_id) VALUES ($1::uuid, $2::uuid)
		 ON CONFLICT DO NOTHING`,
		issueID, uid)
	if err != nil {
		return err
	}
	var notified []*Notification
	if tag.RowsAffected() > 0 {
		// New assignment: notify the assignee (in-app row + optional
		// email per prefs). Idempotent re-assign notifies nobody.
		displayID, name, err := issueNotifyContextTx(ctx, tx, ident, issueID)
		if err != nil {
			return err
		}
		actorName := actorDisplayName(ctx, tx, actorID)
		notified, err = notifyTx(ctx, tx, NotifyIssueAssigned,
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
			actorID, []string{uid})
		if err != nil {
			return err
		}
		// C16T0: a real assignment fires issue.assigned rules in this
		// same tx — a rolled-back assignment can never fire a rule.
		if !automationActive(ctx) {
			notified = append(notified,
				runAutomationAssignedTx(ctx, tx, wsID, projectID, ident, issueID, actorID)...)
		}
	}
	changed := tag.RowsAffected() > 0
	// C50T1 + C65T0: exactly one version per real membership change --
	// the cascade snapshot iff the firing moved the row, else one
	// snapshot of the post-mutation row. No-ops snapshot nothing.
	if err := snapshotMembershipVersionTx(ctx, tx, issueID, actorID, ident, before, haveBefore, changed); err != nil {
		return err
	}
	// C56T1: exactly one issue.updated webhook in-tx for a real
	// assignment (direct "assignees" + any cascade row diff).
	if err := enqueueLabelWebhookTx(ctx, tx, wsID, ident, issueID, actorID, "assignees", before, haveBefore, changed); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	announceNotifications(notified)
	announceLabelChange(ctx, pool, wsSlug, ident, projectID, issueID, beforeStateID, haveBefore, changed)
	return nil
}

// UnassignAssignee removes an assignee from an issue. Idempotent:
// unassigning an absent assignee succeeds silently (and notifies nobody).
// Member (15)+. C46T1: a real unassignment announces issue.updated after
// commit (+ objective.updated on a cascade set_state move); a no-op
// unassignment announces nothing.
func UnassignAssignee(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, issueID, userID, actorID string) error {
	wsID, projectID, role, err := resolveIssueForTaxonomy(ctx, pool, wsSlug, identifier, issueID, actorID)
	if err != nil {
		return err
	}
	if role < RoleMember {
		return ErrForbidden
	}
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return err
	}
	if !isUUIDFormat(strings.TrimSpace(userID)) {
		// Malformed ids match nothing — the idempotent no-op answer.
		return nil
	}
	uid := strings.ToLower(strings.TrimSpace(userID))

	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// C46T1: pre-mutation before-image for the cascade detector and
	// the C50T1 cascade version snapshot.
	before, haveBefore := issueBeforeImageInTx(ctx, tx, issueID)
	beforeStateID := ""
	if haveBefore {
		beforeStateID = before.StateID
	}

	tag, err := tx.Exec(ctx,
		`DELETE FROM issue_assignees WHERE issue_id = $1::uuid AND user_id = $2::uuid`,
		issueID, uid)
	if err != nil {
		return err
	}
	var notified []*Notification
	if tag.RowsAffected() > 0 {
		displayID, name, err := issueNotifyContextTx(ctx, tx, ident, issueID)
		if err != nil {
			return err
		}
		actorName := actorDisplayName(ctx, tx, actorID)
		notified, err = notifyTx(ctx, tx, NotifyIssueUnassigned,
			fmt.Sprintf("%s unassigned you from %s", actorName, displayID),
			fmt.Sprintf("Issue: %s", name),
			map[string]any{
				"issue_id":     issueID,
				"display_id":   displayID,
				"issue_name":   name,
				"actor_id":     actorID,
				"workspace_id": wsID,
				"project_id":   projectID,
			},
			actorID, []string{uid})
		if err != nil {
			return err
		}
		// C16T0: a real unassignment fires issue.unassigned rules in
		// this same tx — a rolled-back unassignment can never fire a
		// rule.
		if !automationActive(ctx) {
			notified = append(notified,
				runAutomationUnassignedTx(ctx, tx, wsID, projectID, ident, issueID, actorID)...)
		}
	}
	changed := tag.RowsAffected() > 0
	// C50T1 + C65T0: exactly one version per real membership change --
	// the cascade snapshot iff the firing moved the row, else one
	// snapshot of the post-mutation row. No-ops snapshot nothing.
	if err := snapshotMembershipVersionTx(ctx, tx, issueID, actorID, ident, before, haveBefore, changed); err != nil {
		return err
	}
	// C56T1: exactly one issue.updated webhook in-tx for a real
	// unassignment (direct "assignees" + any cascade row diff).
	if err := enqueueLabelWebhookTx(ctx, tx, wsID, ident, issueID, actorID, "assignees", before, haveBefore, changed); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	announceNotifications(notified)
	announceLabelChange(ctx, pool, wsSlug, ident, projectID, issueID, beforeStateID, haveBefore, changed)
	return nil
}

// ---------- Estimates ----------

// EstimatePointInput is one selectable value of a scale.
type EstimatePointInput struct {
	Key         string
	Value       int
	Description *string
}

// EstimateInput carries estimate-scale creation fields.
type EstimateInput struct {
	Name   string
	Points []EstimatePointInput
}

// EstimatePoint is one selectable value of a scale.
type EstimatePoint struct {
	ID          string  `json:"id"`
	Key         string  `json:"key"`
	Value       int     `json:"value"`
	Description *string `json:"description,omitempty"`
}

// Estimate is a per-project estimation scale (e.g. "Fibonacci") with its
// points.
type Estimate struct {
	ID        string          `json:"id"`
	ProjectID string          `json:"project_id"`
	Name      string          `json:"name"`
	Points    []EstimatePoint `json:"points"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// CreateEstimate creates a scale with its points in one transaction.
// Member (15)+. Duplicate scale names in the project are
// ErrEstimateConflict; malformed points (empty key, duplicate keys, no
// points at all) are ErrInvalidEstimatePoint.
func CreateEstimate(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string, in EstimateInput) (*Estimate, error) {
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 120 {
		return nil, ErrNameRequired
	}
	if len(in.Points) == 0 {
		return nil, ErrInvalidEstimatePoint
	}
	seen := map[string]bool{}
	for _, p := range in.Points {
		k := strings.TrimSpace(p.Key)
		if k == "" || len(k) > 32 {
			return nil, ErrInvalidEstimatePoint
		}
		if seen[k] {
			return nil, ErrInvalidEstimatePoint
		}
		seen[k] = true
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

	var est Estimate
	err = tx.QueryRow(ctx,
		`INSERT INTO estimates (project_id, name) VALUES ($1::uuid, $2)
		 RETURNING id::text, project_id::text, name, created_at, updated_at`,
		projectID, name).Scan(&est.ID, &est.ProjectID, &est.Name, &est.CreatedAt, &est.UpdatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrEstimateConflict
		}
		return nil, err
	}
	est.Points = make([]EstimatePoint, 0, len(in.Points))
	for _, p := range in.Points {
		var pt EstimatePoint
		k := strings.TrimSpace(p.Key)
		err := tx.QueryRow(ctx,
			`INSERT INTO estimate_points (estimate_id, key, value, description)
			 VALUES ($1::uuid, $2, $3, $4)
			 RETURNING id::text, key, value, description`,
			est.ID, k, p.Value, p.Description).Scan(&pt.ID, &pt.Key, &pt.Value, &pt.Description)
		if err != nil {
			if isUniqueViolation(err) {
				return nil, ErrInvalidEstimatePoint
			}
			return nil, err
		}
		est.Points = append(est.Points, pt)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &est, nil
}

// ListEstimates returns the project's scales with their points, ordered
// by name. Any workspace member (guest 5+) may read.
func ListEstimates(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string) ([]Estimate, error) {
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return nil, err
	}
	_, projectID, _, err := resolveIssueProject(ctx, pool, wsSlug, ident, actorID)
	if err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx,
		`SELECT e.id::text, e.project_id::text, e.name, e.created_at, e.updated_at,
		        COALESCE((SELECT json_agg(jsonb_build_object(
		             'id', p.id::text, 'key', p.key, 'value', p.value,
		             'description', p.description) ORDER BY p.value, p.key)::jsonb
		         FROM estimate_points p WHERE p.estimate_id = e.id), '[]'::jsonb)
		 FROM estimates e
		 WHERE e.project_id = $1::uuid
		 ORDER BY e.name`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Estimate{}
	for rows.Next() {
		var est Estimate
		var pointsJSON []byte
		if err := rows.Scan(&est.ID, &est.ProjectID, &est.Name,
			&est.CreatedAt, &est.UpdatedAt, &pointsJSON); err != nil {
			return nil, err
		}
		// 'description': null in the built object unmarshals to a nil
		// *string — the omitempty keeps it out of the response.
		type pointJSON struct {
			ID          string  `json:"id"`
			Key         string  `json:"key"`
			Value       int     `json:"value"`
			Description *string `json:"description"`
		}
		var pjs []pointJSON
		if err := json.Unmarshal(pointsJSON, &pjs); err != nil {
			return nil, fmt.Errorf("service: decode estimate points: %w", err)
		}
		est.Points = make([]EstimatePoint, 0, len(pjs))
		for _, pj := range pjs {
			est.Points = append(est.Points, EstimatePoint{
				ID: pj.ID, Key: pj.Key, Value: pj.Value, Description: pj.Description,
			})
		}
		out = append(out, est)
	}
	return out, rows.Err()
}

// checkEstimatePoint validates that pointID names a point of a scale
// belonging to this project, returning its canonical form.
func checkEstimatePoint(ctx context.Context, q queryRower, projectID, pointID string) (string, error) {
	pid := strings.ToLower(strings.TrimSpace(pointID))
	var owner string
	err := q.QueryRow(ctx,
		`SELECT e.project_id::text
		 FROM estimate_points ep JOIN estimates e ON e.id = ep.estimate_id
		 WHERE ep.id = $1::uuid`, pid).Scan(&owner)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return "", ErrInvalidEstimatePoint
		}
		return "", err
	}
	if owner != projectID {
		return "", ErrInvalidEstimatePoint
	}
	return pid, nil
}
