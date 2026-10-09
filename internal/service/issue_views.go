package service

// Saved views (C9T2; sharing added C10T1): per-user named filter+display
// presets on a project, persisted in the issue_views table so views roam
// across devices. A view is private by default (shared=false); its owner
// may share it with the project, making it visible to every project
// member as a read-only row annotated with its owner.
//
// Conventions:
//   - Tenancy: every op resolves project membership from the project UUID.
//     A project the caller cannot see (missing, or in a workspace they are
//     not a member of) surfaces ErrViewProjectNotFound — one sentinel for
//     both so 404s never leak existence across workspaces.
//   - Visibility: views are private by default (shared=false); the list
//     returns the caller's own views plus the project's shared views.
//     A view id that exists but is private under a different user reads
//     as ErrViewNotFound — 404s never leak existence of private views.
//     A view that is shared is visible to the caller, so mutating it as
//     a non-owner reads as ErrForbidden (403).
//   - filters is stored opaquely (the IssueFilters shape from
//     web/src/lib/filters.ts): the backend validates it is a JSON object
//     and never interprets it.
//   - Name uniqueness is case-insensitive per (user, project)
//     (UNIQUE(user_id, project_id, lower(name))); a clash is
//     ErrViewNameTaken → 409.
//   - Setting is_default=true clears the caller's other defaults on the
//     same project in the same transaction — at most one default per
//     (user, project).
//   - Roles: any workspace member (guest 5+) may manage their own views
//     and see the project's shared views. Mutating someone else's view is
//     never allowed: sharing is per-view opt-in, and only the owner may
//     rename, re-share, set the default on, or delete their own views —
//     with one moderation exception (see DeleteIssueView): a workspace
//     admin (role 20) may delete any SHARED view.

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
	// Shared is true once the owner shares the view with the project
	// (C10T1); the row then appears in every project member's list.
	Shared bool `json:"shared"`
	// OwnerID / OwnerName identify the view's owner. Included on every
	// row (not just shared ones) so the frontend can gate owner-only
	// actions (share toggle, rename, delete) without a second request.
	OwnerID   string    `json:"owner_id"`
	OwnerName string    `json:"owner_name"`
	CreatedAt time.Time `json:"created_at"`
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
	// Shared toggles project-wide visibility. Owner-only: a non-owner
	// PATCH on another user's view is ErrForbidden when the view is
	// shared (visible to them) and ErrViewNotFound when it is private
	// (invisible to them — no existence leak).
	Shared *bool
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

// memberProject resolves the project UUID for the actor and returns the
// actor's workspace role alongside it. The project must exist and the
// actor must be a member of its workspace. Anything else is
// ErrViewProjectNotFound.
func memberProject(ctx context.Context, q queryRower, projectID, actorID string) (pid string, role int, err error) {
	err = q.QueryRow(ctx,
		`SELECT p.id::text, m.role
		 FROM projects p
		 JOIN workspaces w ON w.id = p.workspace_id
		 JOIN workspace_members m ON m.workspace_id = w.id AND m.user_id = $2::uuid
		 WHERE p.id = $1::uuid`,
		projectID, actorID).Scan(&pid, &role)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return "", 0, ErrViewProjectNotFound
		}
		return "", 0, err
	}
	return pid, role, nil
}

// issueViewColumns is the SELECT list shared by every read path, in
// scanIssueView order. The users join supplies the owner annotation
// (COALESCE(name, email) matches the convention in issue.go/workspace.go).
const issueViewColumns = `v.id::text, v.name, v.filters, v.display, v.is_default,
	v.shared, v.user_id::text, COALESCE(u.name, u.email::text), v.created_at`

const issueViewFrom = `FROM issue_views v JOIN users u ON u.id = v.user_id`

func scanIssueView(row pgx.Row) (*IssueView, error) {
	var v IssueView
	var display []byte
	if err := row.Scan(&v.ID, &v.Name, &v.Filters, &display, &v.IsDefault,
		&v.Shared, &v.OwnerID, &v.OwnerName, &v.CreatedAt); err != nil {
		return nil, err
	}
	if display != nil {
		v.Display = json.RawMessage(display)
	}
	return &v, nil
}

// getIssueView reads one view by id with the owner annotation.
func getIssueView(ctx context.Context, q queryRower, viewID string) (*IssueView, error) {
	return scanIssueView(q.QueryRow(ctx,
		`SELECT `+issueViewColumns+`
		 `+issueViewFrom+`
		 WHERE v.id = $1::uuid`, viewID))
}

// ListIssueViews returns the caller's own views on the project plus the
// project's shared views (annotated with their owners), oldest first.
// The slice is never nil — the frontend renders an honest empty state off
// the length.
func ListIssueViews(ctx context.Context, pool *pgxpool.Pool, actorID, projectID string) ([]IssueView, error) {
	pid, _, err := memberProject(ctx, pool, projectID, actorID)
	if err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx,
		`SELECT `+issueViewColumns+`
		 `+issueViewFrom+`
		 WHERE v.project_id = $2::uuid AND (v.user_id = $1::uuid OR v.shared = TRUE)
		 ORDER BY v.created_at ASC`,
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
// (case-insensitive) name is ErrViewNameTaken. New views are private
// (shared=false); sharing with the project is an explicit PATCH toggle.
func CreateIssueView(ctx context.Context, pool *pgxpool.Pool, actorID, projectID string, in IssueViewInput) (*IssueView, error) {
	name, err := cleanViewName(in.Name)
	if err != nil {
		return nil, err
	}
	filters, err := cleanViewFilters(in.Filters)
	if err != nil {
		return nil, err
	}
	pid, _, err := memberProject(ctx, pool, projectID, actorID)
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
	var id string
	if err := pool.QueryRow(ctx,
		`INSERT INTO issue_views (user_id, project_id, name, filters, display)
		 VALUES ($1::uuid, $2::uuid, $3, $4::jsonb, $5::jsonb)
		 RETURNING id::text`,
		actorID, pid, name, filters, display).Scan(&id); err != nil {
		if isUniqueViolation(err) {
			return nil, ErrViewNameTaken
		}
		return nil, err
	}
	return getIssueView(ctx, pool, id)
}

// UpdateIssueView renames a view, flips its default flag, and/or toggles
// its shared flag. Setting is_default=true clears the caller's other
// defaults on the same project in the same transaction. A name clash is
// ErrViewNameTaken; an unknown view id (or a private view belonging to
// another user) is ErrViewNotFound.
//
// Ownership: only the owner may PATCH a view. A non-owner touching a
// SHARED view gets ErrForbidden (the view is visible to them in the list,
// so 403 is honest); a non-owner touching a PRIVATE view of another user
// gets ErrViewNotFound (no existence leak).
func UpdateIssueView(ctx context.Context, pool *pgxpool.Pool, actorID, projectID, viewID string, patch IssueViewPatch) (*IssueView, error) {
	pid, _, err := memberProject(ctx, pool, projectID, actorID)
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

	// Ownership gate: the view must exist on this project first; then
	// private views of other users read as not found, shared views of
	// other users read as forbidden.
	var ownerID string
	var shared bool
	err = tx.QueryRow(ctx,
		`SELECT user_id::text, shared FROM issue_views
		 WHERE id = $1::uuid AND project_id = $2::uuid`,
		viewID, pid).Scan(&ownerID, &shared)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return nil, ErrViewNotFound
		}
		return nil, err
	}
	if ownerID != actorID {
		if shared {
			return nil, ErrForbidden
		}
		return nil, ErrViewNotFound
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
	if patch.Shared != nil {
		if _, err := tx.Exec(ctx,
			`UPDATE issue_views SET shared = $1
			 WHERE id = $2::uuid AND user_id = $3::uuid AND project_id = $4::uuid`,
			*patch.Shared, viewID, actorID, pid); err != nil {
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

	v, err := getIssueView(ctx, tx, viewID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return v, nil
}

// DeleteIssueView removes a view.
//
// Moderation rule (C10T1): deletion is owner-only, with one exception —
// a workspace admin (role 20) may delete any SHARED view on the project
// (shared rows are project-visible, so admins can moderate spam or stale
// shared views they do not own). Private views of another user are
// untouched by anyone but their owner: deleting one is a silent no-op
// (idempotent; never an existence leak). A non-admin member deleting a
// shared view they do not own gets ErrForbidden — the view is visible to
// them in the list, so 403 is honest.
func DeleteIssueView(ctx context.Context, pool *pgxpool.Pool, actorID, projectID, viewID string) error {
	pid, role, err := memberProject(ctx, pool, projectID, actorID)
	if err != nil {
		return err
	}
	var ownerID string
	var shared bool
	err = pool.QueryRow(ctx,
		`SELECT user_id::text, shared FROM issue_views
		 WHERE id = $1::uuid AND project_id = $2::uuid`,
		viewID, pid).Scan(&ownerID, &shared)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return nil // unknown or malformed view id: nothing could match, no-op
		}
		return err
	}
	switch {
	case ownerID == actorID:
		// Owner: always allowed (shared or private).
	case shared && role == RoleAdmin:
		// Moderation override: workspace admin deleting a shared view.
	case shared:
		return ErrForbidden
	default:
		// Another user's private view: silent no-op, as before.
		return nil
	}
	_, err = pool.Exec(ctx,
		`DELETE FROM issue_views
		 WHERE id = $1::uuid AND project_id = $2::uuid`,
		viewID, pid)
	return err
}
