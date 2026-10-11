package service

// Page comments (C18T2): a flat (non-threaded) comment list per wiki
// page, oldest first — the page analogue of issue comments (Task 18),
// minus threading, @-mentions, notifications, and webhooks (mentions
// are a documented follow-up; notifications/webhooks stay issue-only).
//
// Schema (000047): page_comments (page_id FK CASCADE, author_id FK
// SET NULL, body JSONB). Deleting a page deletes its comments via
// ON DELETE CASCADE; deleting a user NULLs their author attribution.
//
// Conventions (mirror the issue satellites and the pages module):
//   - Tenancy: every op resolves workspace membership + project via
//     resolveIssueProject, then the live page via resolvePage. A bad
//     slug or non-member caller surfaces ErrNotFound; a bad project
//     identifier surfaces ErrProjectNotFound; a bad page id surfaces
//     ErrPageNotFound. Distinct 404s on purpose (Task 11 ruling).
//   - Roles: viewer (10)+ may read; mutations need member (15)+.
//     Edit/delete is the comment's author or a workspace admin.
//   - Body: a TipTap JSON doc (JSONB), validated exactly like issue
//     comment content — whitespace-trimmed, rejected when empty or
//     JSON null (ErrInvalidComment). No raw-HTML storage anywhere.
//   - DELETE is a hard delete (no deleted_at column): a missing comment
//     is ErrCommentNotFound, exactly like a second delete.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PageComment is one page comment row with its author denormalized.
// Author is nil when the author's user row was deleted (ON DELETE SET
// NULL) — only an admin may then edit/delete it.
type PageComment struct {
	ID        string          `json:"id"`
	PageID    string          `json:"page_id"`
	Author    *ActorRef       `json:"author"`
	Body      json.RawMessage `json:"body"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

const pageCommentColumns = `c.id::text, c.page_id::text,
	c.author_id::text, u.name, u.email, c.body, c.created_at, c.updated_at`

func scanPageComment(row pgx.Row) (*PageComment, error) {
	var c PageComment
	var authorID *string
	var name *string
	var email *string
	var body []byte
	err := row.Scan(&c.ID, &c.PageID, &authorID, &name, &email,
		&body, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if authorID != nil {
		c.Author = &ActorRef{ID: *authorID, Name: name}
		if email != nil {
			c.Author.Email = *email
		}
	}
	c.Body = json.RawMessage(body)
	return &c, nil
}

func getPageComment(ctx context.Context, q queryRower, pageID, commentID string) (*PageComment, error) {
	c, err := scanPageComment(q.QueryRow(ctx,
		`SELECT `+pageCommentColumns+`
		 FROM page_comments c LEFT JOIN users u ON u.id = c.author_id
		 WHERE c.id = $1::uuid AND c.page_id = $2::uuid`,
		commentID, pageID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return nil, ErrCommentNotFound
		}
		return nil, err
	}
	return c, nil
}

// resolvePageCommentProject resolves (projectID, role) for the caller
// and asserts the page is live in that project, then enforces the
// minimum role for the operation: viewer (10)+ reads, member (15)+
// writes. Editing/deleting further requires author-or-admin (checked by
// the caller against the returned role).
func resolvePageCommentProject(ctx context.Context, q queryRower, wsSlug, identifier, pageID, actorID string, minRole int) (projectID string, role int, err error) {
	_, projectID, role, err = resolveIssueProject(ctx, q, wsSlug, identifier, actorID)
	if err != nil {
		return "", 0, err
	}
	if role < minRole {
		return "", 0, ErrForbidden
	}
	if _, err := resolvePage(ctx, q, projectID, pageID); err != nil {
		return "", 0, err
	}
	return projectID, role, nil
}

// validatePageCommentBody applies the issue-comment body rule:
// whitespace-trimmed, rejected when empty or JSON null.
func validatePageCommentBody(body json.RawMessage) (json.RawMessage, error) {
	body = bytes.TrimSpace(body)
	if len(body) == 0 || bytes.Equal(body, []byte("null")) {
		return nil, ErrInvalidComment
	}
	return body, nil
}

// CreatePageComment posts a comment on a page. Member (15)+.
func CreatePageComment(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, pageID, actorID string, body json.RawMessage) (*PageComment, error) {
	if _, _, err := resolvePageCommentProject(ctx, pool, wsSlug, identifier, pageID, actorID, RoleMember); err != nil {
		return nil, err
	}
	body, err := validatePageCommentBody(body)
	if err != nil {
		return nil, err
	}
	var id string
	err = pool.QueryRow(ctx,
		`INSERT INTO page_comments (page_id, author_id, body)
		 VALUES ($1::uuid, $2::uuid, $3::jsonb)
		 RETURNING id::text`,
		pageID, actorID, string(body)).Scan(&id)
	if err != nil {
		return nil, err
	}
	return getPageComment(ctx, pool, pageID, id)
}

// ListPageComments returns a page's comments, oldest first. Flat list
// (no threading). Viewer (10)+.
func ListPageComments(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, pageID, actorID string) ([]*PageComment, error) {
	if _, _, err := resolvePageCommentProject(ctx, pool, wsSlug, identifier, pageID, actorID, RoleViewer); err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx,
		`SELECT `+pageCommentColumns+`
		 FROM page_comments c LEFT JOIN users u ON u.id = c.author_id
		 WHERE c.page_id = $1::uuid
		 ORDER BY c.created_at ASC, c.id ASC`,
		pageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*PageComment{}
	for rows.Next() {
		c, err := scanPageComment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// UpdatePageComment edits a comment's body. The author or a workspace
// admin may edit; everyone else gets ErrForbidden. Mutations need
// member (15)+.
func UpdatePageComment(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, pageID, commentID, actorID string, body json.RawMessage) (*PageComment, error) {
	if _, role, err := resolvePageCommentProject(ctx, pool, wsSlug, identifier, pageID, actorID, RoleMember); err != nil {
		return nil, err
	} else if c, err := getPageComment(ctx, pool, pageID, commentID); err != nil {
		return nil, err
	} else if c.Author == nil || c.Author.ID != actorID {
		if role < RoleAdmin {
			return nil, ErrForbidden
		}
	}
	body, err := validatePageCommentBody(body)
	if err != nil {
		return nil, err
	}
	var id string
	err = pool.QueryRow(ctx,
		`UPDATE page_comments SET body = $1::jsonb, updated_at = now()
		 WHERE id = $2::uuid AND page_id = $3::uuid
		 RETURNING id::text`,
		string(body), commentID, pageID).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return nil, ErrCommentNotFound
		}
		return nil, err
	}
	return getPageComment(ctx, pool, pageID, id)
}

// DeletePageComment hard-deletes a comment. Author or admin; a second
// delete is ErrCommentNotFound. Mutations need member (15)+.
func DeletePageComment(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, pageID, commentID, actorID string) error {
	if _, role, err := resolvePageCommentProject(ctx, pool, wsSlug, identifier, pageID, actorID, RoleMember); err != nil {
		return err
	} else if c, err := getPageComment(ctx, pool, pageID, commentID); err != nil {
		return err
	} else if c.Author == nil || c.Author.ID != actorID {
		if role < RoleAdmin {
			return ErrForbidden
		}
	}
	tag, err := pool.Exec(ctx,
		`DELETE FROM page_comments WHERE id = $1::uuid AND page_id = $2::uuid`,
		commentID, pageID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrCommentNotFound
	}
	return nil
}
