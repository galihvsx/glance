package service

// Project status updates (C34T0): a flat timeline of status posts per
// project — each update carries a health indicator (On Track / At Risk
// / Off Track) plus a plain-text message, newest first. The latest
// update surfaces as a header chip on the project's Overview page.
//
// Schema (000055): project_updates (project_id FK CASCADE, author_id
// FK SET NULL, status CHECK, body TEXT). Deleting a project deletes
// its updates via ON DELETE CASCADE; deleting a user NULLs their
// author attribution.
//
// Conventions (mirror page comments, C18T2):
//   - Tenancy: every op resolves workspace membership + project via
//     resolveIssueProject. A bad slug or non-member caller surfaces
//     ErrNotFound; a bad project identifier surfaces
//     ErrProjectNotFound.
//   - Roles: viewer (10)+ may read; mutations need member (15)+.
//     Edit/delete is the update's author or a workspace admin.
//   - Status: must be one of on_track / at_risk / off_track, else
//     ErrInvalidProjectUpdate (400).
//   - Body: plain text, whitespace-trimmed, rejected when empty
//     (ErrInvalidProjectUpdate). No length cap — same posture as the
//     page-comment body rule, which carries no cap either.
//   - DELETE is a hard delete (no deleted_at column): a missing
//     update is ErrProjectUpdateNotFound, exactly like a second
//     delete.
//   - PATCH is tri-state: a missing key leaves the field untouched;
//     a present status must be valid, a present body must be
//     non-empty. A PATCH with neither key is ErrInvalidProjectUpdate.

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Project update health indicators.
const (
	ProjectUpdateOnTrack  = "on_track"
	ProjectUpdateAtRisk   = "at_risk"
	ProjectUpdateOffTrack = "off_track"
)

// ErrProjectUpdateNotFound is returned when the update id names no
// update of this project.
var ErrProjectUpdateNotFound = errors.New("service: project update not found")

// ErrInvalidProjectUpdate is returned for an invalid status, an empty
// body, or a PATCH that changes nothing.
var ErrInvalidProjectUpdate = errors.New("service: invalid project update")

// ProjectUpdate is one project status update with its author
// denormalized. Author is nil when the author's user row was deleted
// (ON DELETE SET NULL) — only an admin may then edit/delete it.
type ProjectUpdate struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"project_id"`
	Author    *ActorRef `json:"author"`
	Status    string    `json:"status"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ProjectUpdatePatch is the tri-state PATCH body: nil means "leave
// untouched".
type ProjectUpdatePatch struct {
	Status *string
	Body   *string
}

const projectUpdateColumns = `u.id::text, u.project_id::text,
	u.author_id::text, au.name, au.email, u.status, u.body,
	u.created_at, u.updated_at`

func scanProjectUpdate(row pgx.Row) (*ProjectUpdate, error) {
	var u ProjectUpdate
	var authorID *string
	var name *string
	var email *string
	err := row.Scan(&u.ID, &u.ProjectID, &authorID, &name, &email,
		&u.Status, &u.Body, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if authorID != nil {
		u.Author = &ActorRef{ID: *authorID, Name: name}
		if email != nil {
			u.Author.Email = *email
		}
	}
	return &u, nil
}

func getProjectUpdate(ctx context.Context, q queryRower, projectID, updateID string) (*ProjectUpdate, error) {
	u, err := scanProjectUpdate(q.QueryRow(ctx,
		`SELECT `+projectUpdateColumns+`
		 FROM project_updates u LEFT JOIN users au ON au.id = u.author_id
		 WHERE u.id = $1::uuid AND u.project_id = $2::uuid`,
		updateID, projectID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return nil, ErrProjectUpdateNotFound
		}
		return nil, err
	}
	return u, nil
}

// resolveProjectUpdateProject resolves (projectID, role) for the
// caller and enforces the minimum role for the operation: viewer (10)+
// reads, member (15)+ writes. Editing/deleting further requires
// author-or-admin (checked by the caller against the returned role).
func resolveProjectUpdateProject(ctx context.Context, q queryRower, wsSlug, identifier, actorID string, minRole int) (projectID string, role int, err error) {
	_, projectID, role, err = resolveIssueProject(ctx, q, wsSlug, identifier, actorID)
	if err != nil {
		return "", 0, err
	}
	if role < minRole {
		return "", 0, ErrForbidden
	}
	return projectID, role, nil
}

// validateProjectUpdateStatus rejects anything outside the three
// health indicators.
func validateProjectUpdateStatus(status string) error {
	switch status {
	case ProjectUpdateOnTrack, ProjectUpdateAtRisk, ProjectUpdateOffTrack:
		return nil
	default:
		return ErrInvalidProjectUpdate
	}
}

// validateProjectUpdateBody applies the page-comment body rule to
// plain text: whitespace-trimmed, rejected when empty.
func validateProjectUpdateBody(body string) (string, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return "", ErrInvalidProjectUpdate
	}
	return body, nil
}

// CreateProjectUpdate posts a status update on a project.
// Member (15)+.
func CreateProjectUpdate(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, status, body string) (*ProjectUpdate, error) {
	projectID, _, err := resolveProjectUpdateProject(ctx, pool, wsSlug, identifier, actorID, RoleMember)
	if err != nil {
		return nil, err
	}
	if err := validateProjectUpdateStatus(status); err != nil {
		return nil, err
	}
	body, err = validateProjectUpdateBody(body)
	if err != nil {
		return nil, err
	}
	var id string
	err = pool.QueryRow(ctx,
		`INSERT INTO project_updates (project_id, author_id, status, body)
		 VALUES ($1::uuid, $2::uuid, $3, $4)
		 RETURNING id::text`,
		projectID, actorID, status, body).Scan(&id)
	if err != nil {
		return nil, err
	}
	return getProjectUpdate(ctx, pool, projectID, id)
}

// ListProjectUpdates returns a project's updates, newest first.
// Viewer (10)+.
func ListProjectUpdates(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string) ([]*ProjectUpdate, error) {
	projectID, _, err := resolveProjectUpdateProject(ctx, pool, wsSlug, identifier, actorID, RoleViewer)
	if err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx,
		`SELECT `+projectUpdateColumns+`
		 FROM project_updates u LEFT JOIN users au ON au.id = u.author_id
		 WHERE u.project_id = $1::uuid
		 ORDER BY u.created_at DESC, u.id DESC`,
		projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*ProjectUpdate{}
	for rows.Next() {
		u, err := scanProjectUpdate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// UpdateProjectUpdate edits an update's status and/or body. The author
// or a workspace admin may edit; everyone else gets ErrForbidden.
// Mutations need member (15)+.
func UpdateProjectUpdate(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, updateID string, patch ProjectUpdatePatch) (*ProjectUpdate, error) {
	projectID, role, err := resolveProjectUpdateProject(ctx, pool, wsSlug, identifier, actorID, RoleMember)
	if err != nil {
		return nil, err
	}
	u, err := getProjectUpdate(ctx, pool, projectID, updateID)
	if err != nil {
		return nil, err
	}
	if u.Author == nil || u.Author.ID != actorID {
		if role < RoleAdmin {
			return nil, ErrForbidden
		}
	}
	if patch.Status == nil && patch.Body == nil {
		return nil, ErrInvalidProjectUpdate
	}
	newStatus := u.Status
	newBody := u.Body
	if patch.Status != nil {
		if err := validateProjectUpdateStatus(*patch.Status); err != nil {
			return nil, err
		}
		newStatus = *patch.Status
	}
	if patch.Body != nil {
		body, err := validateProjectUpdateBody(*patch.Body)
		if err != nil {
			return nil, err
		}
		newBody = body
	}
	var id string
	err = pool.QueryRow(ctx,
		`UPDATE project_updates SET status = $1, body = $2, updated_at = now()
		 WHERE id = $3::uuid AND project_id = $4::uuid
		 RETURNING id::text`,
		newStatus, newBody, updateID, projectID).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return nil, ErrProjectUpdateNotFound
		}
		return nil, err
	}
	return getProjectUpdate(ctx, pool, projectID, id)
}

// DeleteProjectUpdate hard-deletes an update. Author or admin; a
// second delete is ErrProjectUpdateNotFound. Mutations need member
// (15)+.
func DeleteProjectUpdate(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, updateID string) error {
	projectID, role, err := resolveProjectUpdateProject(ctx, pool, wsSlug, identifier, actorID, RoleMember)
	if err != nil {
		return err
	}
	u, err := getProjectUpdate(ctx, pool, projectID, updateID)
	if err != nil {
		return err
	}
	if u.Author == nil || u.Author.ID != actorID {
		if role < RoleAdmin {
			return ErrForbidden
		}
	}
	tag, err := pool.Exec(ctx,
		`DELETE FROM project_updates WHERE id = $1::uuid AND project_id = $2::uuid`,
		updateID, projectID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrProjectUpdateNotFound
	}
	return nil
}
