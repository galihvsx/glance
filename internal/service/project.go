package service

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
	// ErrIdentifierConflict is returned when the project identifier is
	// already taken in the workspace (compared case-insensitively: the
	// service uppercases before insert, so "eng" and "ENG" collide).
	ErrIdentifierConflict = errors.New("service: identifier already taken")
	// ErrInvalidIdentifier is returned when the identifier is empty,
	// longer than 12 chars, or contains anything but letters/digits.
	ErrInvalidIdentifier = errors.New("service: invalid identifier")
	// ErrProjectNotFound is returned when the project identifier matches
	// nothing in the workspace. It is deliberately distinct from
	// ErrNotFound (missing workspace / non-member caller): the workspace
	// is confirmed to exist and the caller confirmed a member on this
	// path, so "workspace not found" would be actively misleading.
	ErrProjectNotFound = errors.New("service: project not found")
	// ErrNothingToUpdate is returned by UpdateProject when the patch carries
	// no fields: PATCH with {} is a client error, not a no-op success.
	ErrNothingToUpdate = errors.New("service: nothing to update")
)

// Project is an issue container inside a workspace, addressed by its
// uppercase identifier (ENG-123 style display IDs build on it in Task 14).
type Project struct {
	ID            string          `json:"id"`
	WorkspaceID   string          `json:"workspace_id"`
	Identifier    string          `json:"identifier"`
	Name          string          `json:"name"`
	Description   string          `json:"description"`
	ViewFlags     json.RawMessage `json:"view_flags,omitempty"`
	ArchiveInDays *int            `json:"archive_in_days,omitempty"`
	CloseInDays   *int            `json:"close_in_days,omitempty"`
	// AutomationRunRetentionDays is the automation run retention window
	// in days (C13T0, migration 000039): NULL = unset = the 90-day
	// default (service.DefaultAutomationRunRetentionDays), 0 = keep
	// forever. Negative is rejected at write.
	AutomationRunRetentionDays *int      `json:"automation_run_retention_days,omitempty"`
	CreatedAt                  time.Time `json:"created_at"`
	UpdatedAt                  time.Time `json:"updated_at"`
}

// State is a kanban column of a project. Group is the Plane vocabulary
// (triage/backlog/unstarted/started/completed/cancelled); Sequence orders
// the columns left to right.
type State struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"project_id"`
	Name      string    `json:"name"`
	Group     string    `json:"group"`
	Color     string    `json:"color"`
	Sequence  int       `json:"sequence"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// identifierRe enforces the identifier contract: 1-12 uppercase letters
// and digits. Input is uppercased before matching, so "eng" is accepted
// and stored as "ENG".
var identifierRe = regexp.MustCompile(`^[A-Z0-9]+$`)

func normalizeIdentifier(s string) (string, error) {
	ident := strings.ToUpper(strings.TrimSpace(s))
	if len(ident) < 1 || len(ident) > 12 || !identifierRe.MatchString(ident) {
		return "", ErrInvalidIdentifier
	}
	return ident, nil
}

// defaultStates are seeded for every new project in the same transaction
// as the project row — a project without states is unusable, so the two
// must commit atomically. Sequences are spaced 10000 apart so a future
// column-reorder (Task 23 style) has room without renumbering.
var defaultStates = []struct{ name, group, color string }{
	{"Backlog", "backlog", "#6b7280"},
	{"Todo", "unstarted", "#3b82f6"},
	{"In Progress", "started", "#f59e0b"},
	{"Done", "completed", "#22c55e"},
	{"Cancelled", "cancelled", "#ef4444"},
}

// scanProject scans the full project column list produced by the queries
// below. view_flags is nullable JSONB: a NULL scans into a nil []byte and
// stays omitted from the JSON response via omitempty.
func scanProject(row pgx.Row) (*Project, error) {
	var p Project
	var viewFlags []byte
	err := row.Scan(
		&p.ID, &p.WorkspaceID, &p.Identifier, &p.Name, &p.Description,
		&viewFlags, &p.ArchiveInDays, &p.CloseInDays, &p.AutomationRunRetentionDays, &p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if viewFlags != nil {
		p.ViewFlags = json.RawMessage(viewFlags)
	}
	return &p, nil
}

// projectColumns is the bare column list for INSERT/UPDATE ... RETURNING
// (no table alias exists there); projectColumnsP is the same list with
// the p. alias for SELECT ... FROM projects p.
const projectColumns = `id::text, workspace_id::text, identifier, name, description,
	view_flags, archive_in_days, close_in_days, automation_run_retention_days, created_at, updated_at`

const projectColumnsP = `p.id::text, p.workspace_id::text, p.identifier, p.name, p.description,
	p.view_flags, p.archive_in_days, p.close_in_days, p.automation_run_retention_days, p.created_at, p.updated_at`

// CreateProject inserts the project and seeds its default states in one
// transaction. The caller must be a workspace member (15) or admin (20);
// guests (5) get ErrForbidden, non-members ErrNotFound.
func CreateProject(ctx context.Context, pool *pgxpool.Pool, wsSlug, actorID, name, identifier string) (*Project, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, ErrNameRequired
	}
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return nil, err
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// Membership lookup reuses Task 11's helper (same package): the query
	// shape is the caller's, the row interpretation is shared.
	wsID, role, err := workspaceIDForActor(tx.QueryRow(ctx,
		`SELECT w.id::text, m.role
		 FROM workspaces w
		 JOIN workspace_members m ON m.workspace_id = w.id
		 WHERE w.slug = $1 AND m.user_id = $2::uuid`,
		wsSlug, actorID))
	if err != nil {
		return nil, err
	}
	if role < RoleMember {
		return nil, ErrForbidden
	}

	p, err := scanProject(tx.QueryRow(ctx,
		`INSERT INTO projects (workspace_id, identifier, name)
		 VALUES ($1::uuid, $2, $3)
		 RETURNING `+projectColumns,
		wsID, ident, name))
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrIdentifierConflict
		}
		return nil, err
	}

	seq := 10000
	for _, s := range defaultStates {
		if _, err := tx.Exec(ctx,
			`INSERT INTO states (project_id, name, "group", color, sequence)
			 VALUES ($1::uuid, $2, $3, $4, $5)`,
			p.ID, s.name, s.group, s.color, seq); err != nil {
			return nil, err
		}
		seq += 10000
	}

	// The per-project issue sequence counter (Task 14) is born with the
	// project, so the atomic UPDATE..RETURNING in CreateIssue is
	// unconditional — no "row missing" branch in the hot path.
	if _, err := tx.Exec(ctx,
		`INSERT INTO issue_sequences (project_id) VALUES ($1::uuid)`,
		p.ID); err != nil {
		return nil, err
	}

	// The default intake inbox (Task 20) is born with the project in the
	// same tx — every project is triage-ready from creation.
	if err := seedIntakeTx(ctx, tx, p.ID); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return p, nil
}

// ListProjects returns the workspace's projects ordered by name.
// Non-members get ErrNotFound (see GetWorkspace).
func ListProjects(ctx context.Context, pool *pgxpool.Pool, wsSlug, actorID string) ([]Project, error) {
	wsID, _, err := workspaceIDForActor(pool.QueryRow(ctx,
		`SELECT w.id::text, m.role
		 FROM workspaces w
		 JOIN workspace_members m ON m.workspace_id = w.id
		 WHERE w.slug = $1 AND m.user_id = $2::uuid`,
		wsSlug, actorID))
	if err != nil {
		return nil, err
	}

	rows, err := pool.Query(ctx,
		`SELECT `+projectColumnsP+` FROM projects p
		 WHERE p.workspace_id = $1::uuid
		 ORDER BY p.name`,
		wsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Project{}
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// GetProject returns one project by identifier (case-insensitive: the
// lookup is uppercased the same way inserts are). Non-members get
// ErrNotFound; members asking for a missing identifier get
// ErrProjectNotFound.
func GetProject(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string) (*Project, error) {
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return nil, err
	}
	wsID, _, err := workspaceIDForActor(pool.QueryRow(ctx,
		`SELECT w.id::text, m.role
		 FROM workspaces w
		 JOIN workspace_members m ON m.workspace_id = w.id
		 WHERE w.slug = $1 AND m.user_id = $2::uuid`,
		wsSlug, actorID))
	if err != nil {
		return nil, err
	}

	p, err := scanProject(pool.QueryRow(ctx,
		`SELECT `+projectColumnsP+` FROM projects p
		 WHERE p.workspace_id = $1::uuid AND p.identifier = $2`,
		wsID, ident))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrProjectNotFound
		}
		return nil, err
	}
	return p, nil
}

// ProjectPatch is a partial project update: nil fields are left unchanged,
// so PATCH carries true partial-update semantics (a body that omits
// description does not wipe it). An explicit empty string is honored as a
// clear — JSON null and "" stay distinct.
type ProjectPatch struct {
	Name        *string
	Description *string
	// CloseInDays is the ticker's rollover rule (Task 22): days after a
	// cycle's end_date before incomplete issues detach to the backlog
	// when no next cycle exists. Nil = untouched; explicit 0 (or a NULL
	// column) detaches at completion. Negative is rejected.
	CloseInDays *int
	// AutomationRunRetentionDays is the automation run retention window
	// in days (C13T0): nil = untouched; explicit 0 keeps runs forever
	// (or a NULL column = the 90-day default). Negative is rejected
	// with ErrInvalidAutomationRunRetentionDays.
	AutomationRunRetentionDays *int
}

// UpdateProject applies a partial project update. Member (15) or admin
// (20); guests get ErrForbidden. Only non-nil patch fields are written,
// so omitting a field leaves it untouched — PATCH carries true
// partial-update semantics. An explicit empty string is honored as a clear
// (null and "" stay distinct). A patch with no fields is
// ErrNothingToUpdate. The role check and the update run in one transaction
// so a concurrent demotion cannot slip between them.
func UpdateProject(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string, patch ProjectPatch) (*Project, error) {
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return nil, err
	}

	// Build the SET clause from non-nil fields only. args[0]/args[1] are
	// reserved for wsID/ident in the WHERE clause, hence the +2 offset on
	// the placeholder indices.
	var sets []string
	args := make([]any, 0, 4)
	if patch.Name != nil {
		name := strings.TrimSpace(*patch.Name)
		if name == "" {
			return nil, ErrNameRequired
		}
		args = append(args, name)
		sets = append(sets, fmt.Sprintf("name = $%d", len(args)+2))
	}
	if patch.Description != nil {
		args = append(args, *patch.Description)
		sets = append(sets, fmt.Sprintf("description = $%d", len(args)+2))
	}
	if patch.CloseInDays != nil {
		if *patch.CloseInDays < 0 {
			return nil, ErrInvalidCloseInDays
		}
		args = append(args, *patch.CloseInDays)
		sets = append(sets, fmt.Sprintf("close_in_days = $%d", len(args)+2))
	}
	if patch.AutomationRunRetentionDays != nil {
		if *patch.AutomationRunRetentionDays < 0 {
			return nil, ErrInvalidAutomationRunRetentionDays
		}
		args = append(args, *patch.AutomationRunRetentionDays)
		sets = append(sets, fmt.Sprintf("automation_run_retention_days = $%d", len(args)+2))
	}
	if len(sets) == 0 {
		return nil, ErrNothingToUpdate
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	wsID, role, err := workspaceIDForActor(tx.QueryRow(ctx,
		`SELECT w.id::text, m.role
		 FROM workspaces w
		 JOIN workspace_members m ON m.workspace_id = w.id
		 WHERE w.slug = $1 AND m.user_id = $2::uuid`,
		wsSlug, actorID))
	if err != nil {
		return nil, err
	}
	if role < RoleMember {
		return nil, ErrForbidden
	}

	p, err := scanProject(tx.QueryRow(ctx,
		`UPDATE projects p SET `+strings.Join(sets, ", ")+`, updated_at = now()
		 WHERE p.workspace_id = $1::uuid AND p.identifier = $2
		 RETURNING `+projectColumns,
		append([]any{wsID, ident}, args...)...))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrProjectNotFound
		}
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return p, nil
}

// ListStates returns the project's states ordered by sequence (kanban
// column order). Non-members get ErrNotFound; a missing project is
// ErrProjectNotFound.
func ListStates(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string) ([]State, error) {
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return nil, err
	}
	wsID, _, err := workspaceIDForActor(pool.QueryRow(ctx,
		`SELECT w.id::text, m.role
		 FROM workspaces w
		 JOIN workspace_members m ON m.workspace_id = w.id
		 WHERE w.slug = $1 AND m.user_id = $2::uuid`,
		wsSlug, actorID))
	if err != nil {
		return nil, err
	}

	var projectID string
	if err := pool.QueryRow(ctx,
		`SELECT p.id::text FROM projects p
		 WHERE p.workspace_id = $1::uuid AND p.identifier = $2`,
		wsID, ident).Scan(&projectID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrProjectNotFound
		}
		return nil, err
	}

	rows, err := pool.Query(ctx,
		`SELECT s.id::text, s.project_id::text, s.name, s."group", s.color,
		        s.sequence, s.created_at, s.updated_at
		 FROM states s
		 WHERE s.project_id = $1::uuid
		 ORDER BY s.sequence`,
		projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []State{}
	for rows.Next() {
		var s State
		if err := rows.Scan(
			&s.ID, &s.ProjectID, &s.Name, &s.Group, &s.Color,
			&s.Sequence, &s.CreatedAt, &s.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
