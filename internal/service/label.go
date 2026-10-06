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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
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

// AssignLabel attaches a workspace label to an issue. Idempotent:
// assigning twice succeeds with no duplicate row. The label must belong
// to the workspace (a foreign label is ErrLabelNotFound). Member (15)+.
func AssignLabel(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, issueID, labelID, actorID string) error {
	wsID, _, role, err := resolveIssueForTaxonomy(ctx, pool, wsSlug, identifier, issueID, actorID)
	if err != nil {
		return err
	}
	if role < RoleMember {
		return ErrForbidden
	}
	lid := strings.ToLower(strings.TrimSpace(labelID))
	var owner string
	err = pool.QueryRow(ctx,
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
	_, err = pool.Exec(ctx,
		`INSERT INTO issue_labels (issue_id, label_id) VALUES ($1::uuid, $2::uuid)
		 ON CONFLICT DO NOTHING`,
		issueID, lid)
	return err
}

// UnassignLabel detaches a label from an issue. Idempotent: unassigning
// an absent label succeeds silently. Member (15)+.
func UnassignLabel(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, issueID, labelID, actorID string) error {
	_, _, role, err := resolveIssueForTaxonomy(ctx, pool, wsSlug, identifier, issueID, actorID)
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
	_, err = pool.Exec(ctx,
		`DELETE FROM issue_labels WHERE issue_id = $1::uuid AND label_id = $2::uuid`,
		issueID, labelID)
	return err
}

// AssignAssignee assigns a workspace member to an issue. Idempotent.
// The assignee must be a member of the workspace (any role — guests can
// be assigned work). Member (15)+ to mutate.
func AssignAssignee(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, issueID, userID, actorID string) error {
	wsID, _, role, err := resolveIssueForTaxonomy(ctx, pool, wsSlug, identifier, issueID, actorID)
	if err != nil {
		return err
	}
	if role < RoleMember {
		return ErrForbidden
	}
	uid := strings.ToLower(strings.TrimSpace(userID))
	var one int
	err = pool.QueryRow(ctx,
		`SELECT 1 FROM workspace_members WHERE workspace_id = $1::uuid AND user_id = $2::uuid`,
		wsID, uid).Scan(&one)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return ErrAssigneeNotMember
		}
		return err
	}
	_, err = pool.Exec(ctx,
		`INSERT INTO issue_assignees (issue_id, user_id) VALUES ($1::uuid, $2::uuid)
		 ON CONFLICT DO NOTHING`,
		issueID, uid)
	return err
}

// UnassignAssignee removes an assignee from an issue. Idempotent:
// unassigning an absent assignee succeeds silently. Member (15)+.
func UnassignAssignee(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, issueID, userID, actorID string) error {
	_, _, role, err := resolveIssueForTaxonomy(ctx, pool, wsSlug, identifier, issueID, actorID)
	if err != nil {
		return err
	}
	if role < RoleMember {
		return ErrForbidden
	}
	if !isUUIDFormat(strings.TrimSpace(userID)) {
		// Malformed ids match nothing — the idempotent no-op answer.
		return nil
	}
	_, err = pool.Exec(ctx,
		`DELETE FROM issue_assignees WHERE issue_id = $1::uuid AND user_id = $2::uuid`,
		issueID, userID)
	return err
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
