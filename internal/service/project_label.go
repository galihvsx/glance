package service

// Project labels: the workspace-level project taxonomy (C76T0,
// migration 000070).
//
// Plane v2.5.0 headline #2: "Project Labels — Add labels to projects at
// the workspace level to organize them by team, function, priority, or
// any structure that fits your workflow." Labels are reusable across
// projects; the taxonomy is FLAT (no parent hierarchy — unlike the
// work-item labels of migration 000008, Plane's project labels have
// none; YAGNI).
//
// Discipline mirrors label.go, but the routes are WORKSPACE-mounted —
// not nested under a project identifier — because the taxonomy belongs
// to the workspace:
//   - name is required + trimmed, UNIQUE(workspace_id, name) →
//     ErrProjectLabelConflict (409).
//   - color goes through normalizeLabelColor (#rrggbb, default
//     #6b7280); bad hex is ErrInvalidLabel (400).
//   - guests (5+) may read; every write needs member (15)+
//     (ErrForbidden → 403). Non-members get ErrNotFound (404).
//   - assignment is per-project via the project_project_labels
//     junction: duplicate assign is ErrProjectLabelAssignConflict
//     (409); unassign is idempotent.
//   - deleting a label drops its junction rows only — projects are
//     never touched; deleting a project cascades its junction rows.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrProjectLabelNotFound is returned when the label id matches
	// nothing in the workspace. Deliberately distinct from ErrNotFound:
	// on this path the workspace is confirmed and the caller is a
	// member.
	ErrProjectLabelNotFound = errors.New("service: project label not found")
	// ErrProjectLabelConflict is returned when the label name is
	// already taken in the workspace (UNIQUE(workspace_id, name)).
	ErrProjectLabelConflict = errors.New("service: project label name already exists")
	// ErrProjectLabelAssignConflict is returned when a label is
	// assigned to a project that already carries it. (Issue labels
	// treat re-attach as an idempotent no-op; project-label assignment
	// is a deliberate 409 so a double-assigning client learns the row
	// already exists.)
	ErrProjectLabelAssignConflict = errors.New("service: project label already assigned")
	// ErrInvalidProjectLabelFilter is returned when a project-list
	// ?label= value is malformed or names no label of the workspace.
	// The handler maps it to 400 with the wrapped detail as the
	// message.
	ErrInvalidProjectLabelFilter = errors.New("service: invalid project label filter")
)

// ProjectLabel is one workspace-scoped project tag.
type ProjectLabel struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	Name        string    `json:"name"`
	Color       string    `json:"color"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ProjectLabelInput carries project-label creation fields.
type ProjectLabelInput struct {
	Name  string
	Color *string
}

// ProjectLabelPatch is a partial project-label update: nil fields are
// untouched.
type ProjectLabelPatch struct {
	Name  *string
	Color *string
}

func (p ProjectLabelPatch) hasFields() bool {
	return p.Name != nil || p.Color != nil
}

const projectLabelColumns = `id::text, workspace_id::text, name, color, created_at, updated_at`

func scanProjectLabel(row pgx.Row) (*ProjectLabel, error) {
	var l ProjectLabel
	if err := row.Scan(&l.ID, &l.WorkspaceID, &l.Name, &l.Color, &l.CreatedAt, &l.UpdatedAt); err != nil {
		return nil, err
	}
	return &l, nil
}

// resolveProjectLabelWorkspace resolves (workspaceID, role) for the
// caller. Guests (5+) may read; needMember enforces member (15)+.
// Non-members (and bad slugs) surface ErrNotFound — the tenancy
// boundary.
func resolveProjectLabelWorkspace(ctx context.Context, q queryRower, wsSlug, actorID string, needMember bool) (string, int, error) {
	wsID, role, err := workspaceIDForActor(q.QueryRow(ctx,
		`SELECT w.id::text, m.role
		 FROM workspaces w
		 JOIN workspace_members m ON m.workspace_id = w.id
		 WHERE w.slug = $1 AND m.user_id = $2::uuid`,
		wsSlug, actorID))
	if err != nil {
		return "", 0, err
	}
	if needMember && role < RoleMember {
		return "", 0, ErrForbidden
	}
	return wsID, role, nil
}

// CreateProjectLabel inserts a workspace project label. The caller must
// be a workspace member (15) or admin (20); guests get ErrForbidden,
// non-members ErrNotFound. Duplicate names in the workspace are
// ErrProjectLabelConflict; bad hex colors are ErrInvalidLabel.
func CreateProjectLabel(ctx context.Context, pool *pgxpool.Pool, wsSlug, actorID string, in ProjectLabelInput) (*ProjectLabel, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 120 {
		return nil, ErrNameRequired
	}
	color, err := normalizeLabelColor(in.Color)
	if err != nil {
		return nil, err
	}

	wsID, _, err := resolveProjectLabelWorkspace(ctx, pool, wsSlug, actorID, true)
	if err != nil {
		return nil, err
	}

	l, err := scanProjectLabel(pool.QueryRow(ctx,
		`INSERT INTO project_labels (workspace_id, name, color)
		 VALUES ($1::uuid, $2, $3)
		 RETURNING `+projectLabelColumns,
		wsID, name, color))
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrProjectLabelConflict
		}
		return nil, err
	}
	return l, nil
}

// ListProjectLabels returns the workspace's project labels ordered by
// name. Any workspace member (guest 5+) may read; the result is never
// nil.
func ListProjectLabels(ctx context.Context, pool *pgxpool.Pool, wsSlug, actorID string) ([]ProjectLabel, error) {
	wsID, _, err := resolveProjectLabelWorkspace(ctx, pool, wsSlug, actorID, false)
	if err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx,
		`SELECT `+projectLabelColumns+` FROM project_labels WHERE workspace_id = $1::uuid ORDER BY name`, wsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProjectLabel{}
	for rows.Next() {
		l, err := scanProjectLabel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *l)
	}
	return out, rows.Err()
}

// GetProjectLabel returns one project label of the workspace. A foreign
// or malformed id reads as ErrProjectLabelNotFound — never "workspace
// not found".
func GetProjectLabel(ctx context.Context, pool *pgxpool.Pool, wsSlug, labelID, actorID string) (*ProjectLabel, error) {
	wsID, _, err := resolveProjectLabelWorkspace(ctx, pool, wsSlug, actorID, false)
	if err != nil {
		return nil, err
	}
	l, err := scanProjectLabel(pool.QueryRow(ctx,
		`SELECT `+projectLabelColumns+` FROM project_labels WHERE id = $1::uuid AND workspace_id = $2::uuid`,
		labelID, wsID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return nil, ErrProjectLabelNotFound
		}
		return nil, err
	}
	return l, nil
}

// UpdateProjectLabel applies a partial project-label update. Renaming to
// an existing name is ErrProjectLabelConflict; a patch with no fields is
// ErrNothingToUpdate. Member (15)+; guests ErrForbidden.
func UpdateProjectLabel(ctx context.Context, pool *pgxpool.Pool, wsSlug, labelID, actorID string, patch ProjectLabelPatch) (*ProjectLabel, error) {
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

	wsID, _, err := resolveProjectLabelWorkspace(ctx, tx, wsSlug, actorID, true)
	if err != nil {
		return nil, err
	}

	old, err := scanProjectLabel(tx.QueryRow(ctx,
		`SELECT `+projectLabelColumns+` FROM project_labels
		 WHERE id = $1::uuid AND workspace_id = $2::uuid FOR UPDATE`,
		labelID, wsID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return nil, ErrProjectLabelNotFound
		}
		return nil, err
	}

	var sets []string
	var args []any
	add := func(column string, arg any) {
		args = append(args, arg)
		// args[0]/args[1] are reserved for id/workspace_id in the WHERE
		// clause, hence the +2 offset (same trick as UpdateLabel).
		sets = append(sets, fmt.Sprintf("%s = $%d", column, len(args)+2))
	}
	if patch.Name != nil {
		name := strings.TrimSpace(*patch.Name)
		if name != old.Name {
			add("name", name)
		}
	}
	if color != nil && *color != old.Color {
		add("color", *color)
	}

	if len(sets) == 0 {
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return old, nil
	}

	updated, err := scanProjectLabel(tx.QueryRow(ctx,
		`UPDATE project_labels SET `+strings.Join(sets, ", ")+`, updated_at = now()
		 WHERE id = $1::uuid AND workspace_id = $2::uuid
		 RETURNING `+projectLabelColumns,
		append([]any{old.ID, wsID}, args...)...))
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrProjectLabelConflict
		}
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return updated, nil
}

// DeleteProjectLabel removes a workspace project label. Its
// project_project_labels junction rows cascade; the projects themselves
// are never touched. Member (15)+; guests ErrForbidden.
func DeleteProjectLabel(ctx context.Context, pool *pgxpool.Pool, wsSlug, labelID, actorID string) error {
	wsID, _, err := resolveProjectLabelWorkspace(ctx, pool, wsSlug, actorID, true)
	if err != nil {
		return err
	}
	ct, err := pool.Exec(ctx,
		`DELETE FROM project_labels WHERE id = $1::uuid AND workspace_id = $2::uuid`,
		labelID, wsID)
	if err != nil {
		if isInvalidUUID(err) {
			return ErrProjectLabelNotFound
		}
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrProjectLabelNotFound
	}
	return nil
}

// resolveProjectForLabel resolves a project of the workspace by
// identifier (case-insensitive, like GetProject). Members asking for a
// missing identifier get ErrProjectNotFound.
func resolveProjectForLabel(ctx context.Context, q queryRower, wsID, identifier string) (string, error) {
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return "", err
	}
	var projectID string
	err = q.QueryRow(ctx,
		`SELECT id::text FROM projects
		 WHERE workspace_id = $1::uuid AND identifier = $2`,
		wsID, ident).Scan(&projectID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrProjectNotFound
		}
		return "", err
	}
	return projectID, nil
}

// resolveProjectLabel checks that labelID names a project label of the
// workspace, returning its canonical id. Foreign or malformed ids are
// ErrProjectLabelNotFound.
func resolveProjectLabel(ctx context.Context, q queryRower, wsID, labelID string) (string, error) {
	lid := strings.ToLower(strings.TrimSpace(labelID))
	var owner string
	err := q.QueryRow(ctx,
		`SELECT workspace_id::text FROM project_labels WHERE id = $1::uuid`, lid).Scan(&owner)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return "", ErrProjectLabelNotFound
		}
		return "", err
	}
	if owner != wsID {
		return "", ErrProjectLabelNotFound
	}
	return lid, nil
}

// AssignProjectLabel attaches a workspace project label to a project.
// The label must belong to the workspace; the project must exist.
// Duplicate assignment is ErrProjectLabelAssignConflict (409) —
// deliberate, unlike the idempotent issue-label re-attach. Member
// (15)+; guests ErrForbidden.
func AssignProjectLabel(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, labelID, actorID string) error {
	wsID, _, err := resolveProjectLabelWorkspace(ctx, pool, wsSlug, actorID, false)
	if err != nil {
		return err
	}
	projectID, err := resolveProjectForLabel(ctx, pool, wsID, identifier)
	if err != nil {
		return err
	}
	// C92T0: label assignment is a project-scoped write — the
	// effective-access order applies (a private project the caller
	// cannot reach is ErrNotFound) and the member+ gate runs on the
	// EFFECTIVE role, so a teamspace-derived member passes and a
	// direct guest does not.
	if _, effRole, err := effectiveRoleForProjectID(ctx, pool, wsID, projectID, actorID); err != nil {
		return err
	} else if effRole < RoleMember {
		return ErrForbidden
	}
	lid, err := resolveProjectLabel(ctx, pool, wsID, labelID)
	if err != nil {
		return err
	}
	_, err = pool.Exec(ctx,
		`INSERT INTO project_project_labels (project_id, label_id)
		 VALUES ($1::uuid, $2::uuid)`,
		projectID, lid)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrProjectLabelAssignConflict
		}
		return err
	}
	return nil
}

// UnassignProjectLabel detaches a project label from a project.
// Idempotent: unassigning an absent or malformed label id succeeds
// silently. Member (15)+; guests ErrForbidden.
func UnassignProjectLabel(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, labelID, actorID string) error {
	wsID, _, err := resolveProjectLabelWorkspace(ctx, pool, wsSlug, actorID, false)
	if err != nil {
		return err
	}
	projectID, err := resolveProjectForLabel(ctx, pool, wsID, identifier)
	if err != nil {
		return err
	}
	// C92T0: same effective-access gate as AssignProjectLabel (see
	// above). The malformed-id idempotent no-op below still applies
	// — but only after the caller proves access to the project.
	if _, effRole, err := effectiveRoleForProjectID(ctx, pool, wsID, projectID, actorID); err != nil {
		return err
	} else if effRole < RoleMember {
		return ErrForbidden
	}
	lid := strings.ToLower(strings.TrimSpace(labelID))
	if !isUUIDFormat(lid) {
		// Malformed ids match nothing — the idempotent no-op answer.
		return nil
	}
	_, err = pool.Exec(ctx,
		`DELETE FROM project_project_labels
		 WHERE project_id = $1::uuid AND label_id = $2::uuid`,
		projectID, lid)
	return err
}

// validateProjectLabelFilter normalizes the ?label= values into deduped
// canonical UUIDs. A malformed value, or an id that names no project
// label of the workspace, is ErrInvalidProjectLabelFilter — a filter
// must name real labels, so a typo'd id never silently matches
// nothing (400, not 404).
func validateProjectLabelFilter(ctx context.Context, q queryRower, wsID string, labelIDs []string) ([]string, error) {
	seen := map[string]bool{}
	var ids []string
	for _, raw := range labelIDs {
		id := strings.ToLower(strings.TrimSpace(raw))
		if id == "" {
			continue
		}
		if !isUUIDFormat(id) {
			return nil, fmt.Errorf("%w: malformed label %q (want UUID)", ErrInvalidProjectLabelFilter, raw)
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		var owner string
		err := q.QueryRow(ctx,
			`SELECT workspace_id::text FROM project_labels WHERE id = $1::uuid`, id).Scan(&owner)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, fmt.Errorf("%w: unknown label %q", ErrInvalidProjectLabelFilter, raw)
			}
			return nil, err
		}
		if owner != wsID {
			return nil, fmt.Errorf("%w: unknown label %q", ErrInvalidProjectLabelFilter, raw)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// projectLabelsAgg is the per-project label-chip aggregate (C76T0):
// [{id,name,color}] ordered by name, [] never null (COALESCE). The
// tableRef is the projects table reference in the enclosing query —
// "projects" for INSERT/UPDATE ... RETURNING (no alias), "p" for
// SELECT ... FROM projects p.
func projectLabelsAgg(tableRef string) string {
	return `COALESCE((SELECT json_agg(jsonb_build_object(
		'id', pl.id::text, 'name', pl.name, 'color', pl.color)
		ORDER BY pl.name)::jsonb
	 FROM project_project_labels ppl
	 JOIN project_labels pl ON pl.id = ppl.label_id
	 WHERE ppl.project_id = ` + tableRef + `.id), '[]'::jsonb)`
}

// queryRows is the Query half of pgx: satisfied by *pgxpool.Pool and
// pgx.Tx alike.
type queryRows interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// loadProjectLabels returns one project's label chips ordered by
// name — [] never nil. Used by the write paths whose RETURNING clause
// cannot carry the correlated chip aggregate (UPDATE ... RETURNING).
func loadProjectLabels(ctx context.Context, q queryRows, projectID string) ([]ProjectLabel, error) {
	rows, err := q.Query(ctx,
		`SELECT pl.id::text, pl.workspace_id::text, pl.name, pl.color, pl.created_at, pl.updated_at
		 FROM project_project_labels ppl
		 JOIN project_labels pl ON pl.id = ppl.label_id
		 WHERE ppl.project_id = $1::uuid
		 ORDER BY pl.name`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProjectLabel{}
	for rows.Next() {
		l, err := scanProjectLabel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *l)
	}
	return out, rows.Err()
}

// decodeProjectLabels unmarshals the json_agg chip column into the
// []-never-null shape the API promises.
func decodeProjectLabels(raw []byte) ([]ProjectLabel, error) {
	out := []ProjectLabel{}
	if len(raw) > 0 {
		var decoded []ProjectLabel
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return nil, fmt.Errorf("service: decode project labels: %w", err)
		}
		out = decoded
	}
	if out == nil {
		out = []ProjectLabel{}
	}
	return out, nil
}
