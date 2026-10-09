package service

// Saved views (C9T2): per-user named filter+display presets on a project,
// persisted in the issue_views table so views roam across devices.
//
// Conventions:
//   - Tenancy: every op resolves project membership from the project UUID.
//     A project the caller cannot see (missing, or in a workspace they are
//     not a member of) surfaces ErrViewProjectNotFound — one sentinel for
//     both so 404s never leak existence across workspaces.
//   - Views are strictly per-user: list/create/update/delete all scope by
//     (user_id, project_id). Another user's views are invisible — a view
//     id that exists under a different user reads as ErrViewNotFound.
//   - filters is stored opaquely (the IssueFilters shape from
//     web/src/lib/filters.ts): the backend validates it is a JSON object
//     and never interprets it.
//   - Name uniqueness is case-insensitive per (user, project)
//     (UNIQUE(user_id, project_id, lower(name))); a clash is
//     ErrViewNameTaken → 409.
//   - Setting is_default=true clears the caller's other defaults on the
//     same project in the same transaction — at most one default per
//     (user, project).
//   - Roles: any workspace member (guest 5+) may manage their own views;
//     they are personal UI state, not shared project configuration.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// MaxIssueViewNameLen mirrors the frontend's MAX_NAME_LEN (64 chars).
const MaxIssueViewNameLen = 64

var (
	// ErrViewProjectNotFound is returned when the project id matches
	// nothing visible to the actor: a missing project or one in a
	// workspace they are not a member of. Deliberately one sentinel for
	// both so 404s never leak existence across workspaces.
	ErrViewProjectNotFound = errors.New("service: project not found or not visible")
	// ErrViewNotFound is returned when the view id matches nothing of the
	// caller's on the project — including when the view exists but belongs
	// to a different user.
	ErrViewNotFound = errors.New("service: saved view not found")
	// ErrViewNameTaken is returned when the name is already taken by
	// another of the caller's views on the project (case-insensitive).
	ErrViewNameTaken = errors.New("service: saved view name already exists")
	// ErrInvalidViewName is returned for a blank or over-long view name.
	ErrInvalidViewName = errors.New("service: invalid saved view name")
	// ErrInvalidViewFilters is returned when filters is not a JSON object.
	ErrInvalidViewFilters = errors.New("service: saved view filters must be a JSON object")
)

// IssueView is one saved-view row.
type IssueView struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Filters   json.RawMessage `json:"filters"`
	Display   json.RawMessage `json:"display"`
	IsDefault bool            `json:"is_default"`
	CreatedAt time.Time       `json:"created_at"`
}

// IssueViewInput is the create payload.
type IssueViewInput struct {
	Name    string
	Filters json.RawMessage
	Display json.RawMessage // nil/empty → NULL (no display settings)
}

// IssueViewPatch is the PATCH payload: nil fields are left untouched.
type IssueViewPatch struct {
	Name      *string
	IsDefault *bool
}

// cleanViewName trims and validates a view name.
func cleanViewName(name string) (string, error) {
	clean := strings.TrimSpace(name)
	if clean == "" || len([]rune(clean)) > MaxIssueViewNameLen {
		return "", ErrInvalidViewName
	}
	return clean, nil
}

// cleanViewFilters validates that filters is a non-empty JSON object.
func cleanViewFilters(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, ErrInvalidViewFilters
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, ErrInvalidViewFilters
	}
	return raw, nil
}

// memberProjectID resolves the project UUID for the actor: the project
// must exist and the actor must be a member of its workspace. Anything
// else is ErrViewProjectNotFound.
func memberProjectID(ctx context.Context, q queryRower, projectID, actorID string) (string, error) {
	var id string
	err := q.QueryRow(ctx,
		`SELECT p.id::text
		 FROM projects p
		 JOIN workspaces w ON w.id = p.workspace_id
		 JOIN workspace_members m ON m.workspace_id = w.id AND m.user_id = $2::uuid
		 WHERE p.id = $1::uuid`,
		projectID, actorID).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return "", ErrViewProjectNotFound
		}
		return "", err
	}
	return id, nil
}

func scanIssueView(row pgx.Row) (*IssueView, error) {
	var v IssueView
	var display []byte
	if err := row.Scan(&v.ID, &v.Name, &v.Filters, &display, &v.IsDefault, &v.CreatedAt); err != nil {
		return nil, err
	}
	if display != nil {
		v.Display = json.RawMessage(display)
	}
	return &v, nil
}

// ListIssueViews returns the caller's views on the project, oldest first.
// The slice is never nil — the frontend renders an honest empty state off
// the length.
func ListIssueViews(ctx context.Context, pool *pgxpool.Pool, actorID, projectID string) ([]IssueView, error) {
	pid, err := memberProjectID(ctx, pool, projectID, actorID)
	if err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx,
		`SELECT id::text, name, filters, display, is_default, created_at
		 FROM issue_views
		 WHERE user_id = $1::uuid AND project_id = $2::uuid
		 ORDER BY created_at ASC`,
		actorID, pid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []IssueView{}
	for rows.Next() {
		v, err := scanIssueView(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// CreateIssueView saves a view for the caller on the project. A duplicate
// (case-insensitive) name is ErrViewNameTaken.
func CreateIssueView(ctx context.Context, pool *pgxpool.Pool, actorID, projectID string, in IssueViewInput) (*IssueView, error) {
	name, err := cleanViewName(in.Name)
	if err != nil {
		return nil, err
	}
	filters, err := cleanViewFilters(in.Filters)
	if err != nil {
		return nil, err
	}
	pid, err := memberProjectID(ctx, pool, projectID, actorID)
	if err != nil {
		return nil, err
	}
	// nil display → NULL (no display settings); the ::jsonb casts match
	// the custom-fields convention (json.RawMessage params). A literal
	// JSON null normalizes to SQL NULL so reads see a clean null.
	display := in.Display
	if len(display) == 0 || string(display) == "null" {
		display = nil
	}
	v, err := scanIssueView(pool.QueryRow(ctx,
		`INSERT INTO issue_views (user_id, project_id, name, filters, display)
		 VALUES ($1::uuid, $2::uuid, $3, $4::jsonb, $5::jsonb)
		 RETURNING id::text, name, filters, display, is_default, created_at`,
		actorID, pid, name, filters, display))
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrViewNameTaken
		}
		return nil, err
	}
	return v, nil
}

// UpdateIssueView renames a view and/or flips its default flag. Setting
// is_default=true clears the caller's other defaults on the same project
// in the same transaction. A name clash is ErrViewNameTaken; an unknown
// (or another user's) view id is ErrViewNotFound.
func UpdateIssueView(ctx context.Context, pool *pgxpool.Pool, actorID, projectID, viewID string, patch IssueViewPatch) (*IssueView, error) {
	pid, err := memberProjectID(ctx, pool, projectID, actorID)
	if err != nil {
		return nil, err
	}
	var name *string
	if patch.Name != nil {
		clean, err := cleanViewName(*patch.Name)
		if err != nil {
			return nil, err
		}
		name = &clean
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var exists int
	err = tx.QueryRow(ctx,
		`SELECT 1 FROM issue_views
		 WHERE id = $1::uuid AND user_id = $2::uuid AND project_id = $3::uuid`,
		viewID, actorID, pid).Scan(&exists)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return nil, ErrViewNotFound
		}
		return nil, err
	}

	if name != nil {
		if _, err := tx.Exec(ctx,
			`UPDATE issue_views SET name = $1
			 WHERE id = $2::uuid AND user_id = $3::uuid AND project_id = $4::uuid`,
			*name, viewID, actorID, pid); err != nil {
			if isUniqueViolation(err) {
				return nil, ErrViewNameTaken
			}
			return nil, err
		}
	}
	if patch.IsDefault != nil && *patch.IsDefault {
		if _, err := tx.Exec(ctx,
			`UPDATE issue_views SET is_default = FALSE
			 WHERE user_id = $1::uuid AND project_id = $2::uuid AND id <> $3::uuid`,
			actorID, pid, viewID); err != nil {
			return nil, err
		}
	}
	if patch.IsDefault != nil {
		if _, err := tx.Exec(ctx,
			`UPDATE issue_views SET is_default = $1
			 WHERE id = $2::uuid AND user_id = $3::uuid AND project_id = $4::uuid`,
			*patch.IsDefault, viewID, actorID, pid); err != nil {
			return nil, err
		}
	}

	v, err := scanIssueView(tx.QueryRow(ctx,
		`SELECT id::text, name, filters, display, is_default, created_at
		 FROM issue_views
		 WHERE id = $1::uuid AND user_id = $2::uuid AND project_id = $3::uuid`,
		viewID, actorID, pid))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return v, nil
}

// DeleteIssueView removes the caller's view. Idempotent: deleting a view
// that does not exist (or belongs to another user) is a silent no-op.
func DeleteIssueView(ctx context.Context, pool *pgxpool.Pool, actorID, projectID, viewID string) error {
	pid, err := memberProjectID(ctx, pool, projectID, actorID)
	if err != nil {
		return err
	}
	_, err = pool.Exec(ctx,
		`DELETE FROM issue_views
		 WHERE id = $1::uuid AND user_id = $2::uuid AND project_id = $3::uuid`,
		viewID, actorID, pid)
	if err != nil && isInvalidUUID(err) {
		return nil // malformed view id: nothing could match, no-op
	}
	return err
}
