package service

// Favorites (C7T4): per-user stars on issues and projects.
//
// The favorites table stores (user_id, favoritable_type, favoritable_id)
// triples with no FK to issues/projects on purpose: the visibility check
// is re-done at every read (a target the user can no longer see — deleted
// issue, revoked workspace membership — silently drops out of the list
// instead of breaking a cascade). Star validates existence AND visibility
// up front so a 404 never leaks whether the UUID exists in another
// workspace.

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// FavoriteIssue stars an issue by UUID.
	FavoriteIssue = "issue"
	// FavoriteProject stars a project by UUID.
	FavoriteProject = "project"
)

// ErrFavoriteTargetNotFound is returned when the starred target matches
// nothing visible to the actor: a missing UUID, a deleted issue, or a
// target in a workspace the actor is not a member of. Deliberately one
// sentinel for all three so 404s never leak existence across workspaces.
var ErrFavoriteTargetNotFound = errors.New("service: favorite target not found or not visible")

// Favorite is one star row.
type Favorite struct {
	ID              string    `json:"id"`
	FavoritableType string    `json:"type"`
	FavoritableID   string    `json:"favoritable_id"`
	CreatedAt       time.Time `json:"created_at"`
}

// FavoriteIssueSummary is a starred issue with enough routing context to
// navigate to it: workspace slug + project identifier + issue UUID.
type FavoriteIssueSummary struct {
	ID                string    `json:"id"`
	DisplayID         string    `json:"display_id"`
	Name              string    `json:"name"`
	WorkspaceSlug     string    `json:"workspace_slug"`
	ProjectID         string    `json:"project_id"`
	ProjectIdentifier string    `json:"project_identifier"`
	ProjectName       string    `json:"project_name"`
	StarredAt         time.Time `json:"starred_at"`
}

// FavoriteProjectSummary is a starred project with its workspace slug.
type FavoriteProjectSummary struct {
	ID            string    `json:"id"`
	Identifier    string    `json:"identifier"`
	Name          string    `json:"name"`
	WorkspaceSlug string    `json:"workspace_slug"`
	StarredAt     time.Time `json:"starred_at"`
}

// FavoritesList is the GET /api/v1/favorites payload. Never nil slices —
// the frontend renders an honest empty state off the lengths.
type FavoritesList struct {
	Issues   []FavoriteIssueSummary   `json:"issues"`
	Projects []FavoriteProjectSummary `json:"projects"`
}

// validFavoriteType reports whether t is a star-able target type.
func validFavoriteType(t string) bool {
	return t == FavoriteIssue || t == FavoriteProject
}

// visibleIssue checks the issue exists, is not deleted, and the actor is
// a member of its workspace. Malformed UUIDs read as "not visible" (404,
// never 500).
func visibleIssue(ctx context.Context, q queryRower, issueID, actorID string) error {
	var one int
	err := q.QueryRow(ctx,
		`SELECT 1
		 FROM issues i
		 JOIN projects p ON p.id = i.project_id
		 JOIN workspaces w ON w.id = p.workspace_id
		 JOIN workspace_members m ON m.workspace_id = w.id AND m.user_id = $2::uuid
		 WHERE i.id = $1::uuid AND i.deleted_at IS NULL`,
		issueID, actorID).Scan(&one)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return ErrFavoriteTargetNotFound
		}
		return err
	}
	return nil
}

// visibleProject checks the project exists and the actor is a member of
// its workspace. Malformed UUIDs read as "not visible".
func visibleProject(ctx context.Context, q queryRower, projectID, actorID string) error {
	var one int
	err := q.QueryRow(ctx,
		`SELECT 1
		 FROM projects p
		 JOIN workspaces w ON w.id = p.workspace_id
		 JOIN workspace_members m ON m.workspace_id = w.id AND m.user_id = $2::uuid
		 WHERE p.id = $1::uuid`,
		projectID, actorID).Scan(&one)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return ErrFavoriteTargetNotFound
		}
		return err
	}
	return nil
}

// StarFavorite stars a target for the actor. Idempotent: starring twice
// returns the existing row with created=false. The target must exist AND
// be visible to the actor (workspace member), otherwise
// ErrFavoriteTargetNotFound. Unknown types are ErrInvalidFavoriteType via
// the caller — this function only accepts the two constants.
func StarFavorite(ctx context.Context, pool *pgxpool.Pool, actorID, ftype, targetID string) (*Favorite, bool, error) {
	if !validFavoriteType(ftype) {
		return nil, false, errors.New("service: invalid favoritable_type")
	}
	switch ftype {
	case FavoriteIssue:
		if err := visibleIssue(ctx, pool, targetID, actorID); err != nil {
			return nil, false, err
		}
	default:
		if err := visibleProject(ctx, pool, targetID, actorID); err != nil {
			return nil, false, err
		}
	}

	var fav Favorite
	err := pool.QueryRow(ctx,
		`INSERT INTO favorites (user_id, favoritable_type, favoritable_id)
		 VALUES ($1::uuid, $2, $3::uuid)
		 ON CONFLICT (user_id, favoritable_type, favoritable_id) DO NOTHING
		 RETURNING id::text, favoritable_type, favoritable_id::text, created_at`,
		actorID, ftype, targetID).Scan(&fav.ID, &fav.FavoritableType, &fav.FavoritableID, &fav.CreatedAt)
	if err == nil {
		return &fav, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, err
	}
	// Conflict path: the star already exists — fetch it so the handler can
	// answer 200 with the same shape as 201.
	if err := pool.QueryRow(ctx,
		`SELECT id::text, favoritable_type, favoritable_id::text, created_at
		 FROM favorites
		 WHERE user_id = $1::uuid AND favoritable_type = $2 AND favoritable_id = $3::uuid`,
		actorID, ftype, targetID).Scan(&fav.ID, &fav.FavoritableType, &fav.FavoritableID, &fav.CreatedAt); err != nil {
		return nil, false, err
	}
	return &fav, false, nil
}

// UnstarFavorite removes a star. Idempotent: unstarring a target that was
// never starred is a no-op (no error). No visibility check — deleting a
// row you may no longer see must not 404.
func UnstarFavorite(ctx context.Context, pool *pgxpool.Pool, actorID, ftype, targetID string) error {
	if !validFavoriteType(ftype) {
		return errors.New("service: invalid favoritable_type")
	}
	_, err := pool.Exec(ctx,
		`DELETE FROM favorites
		 WHERE user_id = $1::uuid AND favoritable_type = $2 AND favoritable_id = $3::uuid`,
		actorID, ftype, targetID)
	if err != nil {
		return err
	}
	return nil
}

// ListFavorites returns the actor's stars, re-checking visibility at read
// time: deleted issues and targets in workspaces the actor left (or was
// removed from) silently drop out. Newest stars first.
func ListFavorites(ctx context.Context, pool *pgxpool.Pool, actorID string) (*FavoritesList, error) {
	out := &FavoritesList{
		Issues:   []FavoriteIssueSummary{},
		Projects: []FavoriteProjectSummary{},
	}

	issueRows, err := pool.Query(ctx,
		`SELECT i.id::text, (p.identifier || '-' || i.sequence_id), i.name,
		        w.slug, p.id::text, p.identifier, p.name, f.created_at
		 FROM favorites f
		 JOIN issues i ON i.id = f.favoritable_id AND i.deleted_at IS NULL
		 JOIN projects p ON p.id = i.project_id
		 JOIN workspaces w ON w.id = p.workspace_id
		 JOIN workspace_members m ON m.workspace_id = w.id AND m.user_id = f.user_id
		 WHERE f.user_id = $1::uuid AND f.favoritable_type = 'issue'
		 ORDER BY f.created_at DESC`,
		actorID)
	if err != nil {
		return nil, err
	}
	for issueRows.Next() {
		var s FavoriteIssueSummary
		if err := issueRows.Scan(&s.ID, &s.DisplayID, &s.Name, &s.WorkspaceSlug,
			&s.ProjectID, &s.ProjectIdentifier, &s.ProjectName, &s.StarredAt); err != nil {
			issueRows.Close()
			return nil, err
		}
		out.Issues = append(out.Issues, s)
	}
	if err := issueRows.Err(); err != nil {
		issueRows.Close()
		return nil, err
	}
	issueRows.Close()

	projectRows, err := pool.Query(ctx,
		`SELECT p.id::text, p.identifier, p.name, w.slug, f.created_at
		 FROM favorites f
		 JOIN projects p ON p.id = f.favoritable_id
		 JOIN workspaces w ON w.id = p.workspace_id
		 JOIN workspace_members m ON m.workspace_id = w.id AND m.user_id = f.user_id
		 WHERE f.user_id = $1::uuid AND f.favoritable_type = 'project'
		 ORDER BY f.created_at DESC`,
		actorID)
	if err != nil {
		return nil, err
	}
	for projectRows.Next() {
		var s FavoriteProjectSummary
		if err := projectRows.Scan(&s.ID, &s.Identifier, &s.Name, &s.WorkspaceSlug, &s.StarredAt); err != nil {
			projectRows.Close()
			return nil, err
		}
		out.Projects = append(out.Projects, s)
	}
	if err := projectRows.Err(); err != nil {
		projectRows.Close()
		return nil, err
	}
	projectRows.Close()

	return out, nil
}

// FavoriteIsStarred reports whether the actor has starred the target.
// Used by detail endpoints so the star toggle can render its state.
func FavoriteIsStarred(ctx context.Context, pool *pgxpool.Pool, actorID, ftype, targetID string) (bool, error) {
	if !validFavoriteType(ftype) {
		return false, nil
	}
	var one int
	err := pool.QueryRow(ctx,
		`SELECT 1 FROM favorites
		 WHERE user_id = $1::uuid AND favoritable_type = $2 AND favoritable_id = $3::uuid`,
		actorID, ftype, targetID).Scan(&one)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
