package service

// Pages (C3T3): project wiki/documentation pages with hierarchy,
// sibling ordering, and simple version history.
//
// Schema (000018): pages (project-scoped, parent_id self-FK) and
// page_revisions (content snapshots). Deleting a page deletes its whole
// subtree via ON DELETE CASCADE — the same cascade semantics as project
// deletion (documented choice, C3T3).
//
// Conventions (mirror cycles/modules):
//   - Tenancy: every op resolves workspace membership + project via
//     resolveIssueProject. A bad slug or non-member caller surfaces
//     ErrNotFound ("workspace not found"); a bad project identifier
//     surfaces ErrProjectNotFound; a bad page id surfaces
//     ErrPageNotFound. Distinct 404s on purpose (Task 11 ruling).
//   - Roles: any member (guest 5+) may read; mutations need member (15)+.
//   - PATCH tri-state for nullable fields: nil = untouched, pointer to
//     "" = clear (NULL), value = set. Content is TEXT and may be empty,
//     so "" sets (not clears) — only parent_id has a NULL semantic.
//   - Every title/content update records a pre-update revision; restore
//     records the pre-restore state first, so no edit is ever lost.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrPageNotFound is returned when the page id matches nothing in
	// the project. Deliberately distinct from ErrProjectNotFound: on this
	// path the workspace and project are confirmed and the caller is a
	// member.
	ErrPageNotFound = errors.New("service: page not found")
	// ErrInvalidPage is returned for malformed page input: empty title,
	// a parent that is not a page of this project, or nothing to update.
	ErrInvalidPage = errors.New("service: invalid page")
	// ErrInvalidPageID is returned when a page id — or a parent/
	// revision id passed to a page endpoint — is not a syntactically
	// valid UUID. The handler maps it to 400 bad_request.
	ErrInvalidPageID = errors.New("service: invalid page id")
	// ErrPageCycle is returned when a move would reparent a page under
	// itself or one of its own descendants. The handler maps it to 400.
	ErrPageCycle = errors.New("service: page move would create a cycle")
	// ErrRevisionNotFound is returned when the revision id matches
	// nothing on the page.
	ErrRevisionNotFound = errors.New("service: page revision not found")
)

// Page is one wiki/documentation page.
type Page struct {
	ID        string  `json:"id"`
	ProjectID string  `json:"project_id"`
	ParentID  *string `json:"parent_id,omitempty"`
	Title     string  `json:"title"`
	Content   string  `json:"content"`
	Position  int     `json:"position"`
	AuthorID  *string `json:"author_id,omitempty"`
	CreatedAt string  `json:"created_at"`
	UpdatedAt string  `json:"updated_at"`
}

// PageRevision is one content snapshot.
type PageRevision struct {
	ID        string  `json:"id"`
	PageID    string  `json:"page_id"`
	Title     string  `json:"title"`
	Content   string  `json:"content"`
	AuthorID  *string `json:"author_id,omitempty"`
	CreatedAt string  `json:"created_at"`
}

// PageInput carries page creation fields.
type PageInput struct {
	Title    string
	Content  *string // nil = ""
	ParentID *string // nil = root; must be a page of the same project
}

// PagePatch is a partial page update: nil fields are untouched.
type PagePatch struct {
	Title   *string
	Content *string // "" is a valid value (clears the body)
}

// PageMoveInput carries a move/reorder request. ParentIDSet tells a
// missing key ("keep") apart from an explicit null ("move to root").
type PageMoveInput struct {
	ParentIDSet bool
	ParentID    *string // nil with ParentIDSet = move to root
	PositionSet bool
	Position    int
}

const pageColumns = `id::text, project_id::text, parent_id::text,
	title, content, position, author_id::text,
	to_char(created_at, 'YYYY-MM-DD"T"HH24:MI:SS.MS"Z"'),
	to_char(updated_at, 'YYYY-MM-DD"T"HH24:MI:SS.MS"Z"')`

const revisionColumns = `id::text, page_id::text, title, content,
	author_id::text, to_char(created_at, 'YYYY-MM-DD"T"HH24:MI:SS.MS"Z"')`

// resolvePageProject resolves (projectID, role) for the caller.
// Guests may read; callers pass needMember to enforce member (15)+.
func resolvePageProject(ctx context.Context, q queryRower, wsSlug, identifier, actorID string, needMember bool) (projectID string, err error) {
	_, projectID, role, err := resolveIssueProject(ctx, q, wsSlug, identifier, actorID)
	if err != nil {
		return "", err
	}
	if needMember && role < RoleMember {
		return "", ErrForbidden
	}
	return projectID, nil
}

func scanPage(row pgx.Row) (Page, error) {
	var p Page
	err := row.Scan(&p.ID, &p.ProjectID, &p.ParentID, &p.Title, &p.Content,
		&p.Position, &p.AuthorID, &p.CreatedAt, &p.UpdatedAt)
	return p, err
}

func scanRevision(row pgx.Row) (PageRevision, error) {
	var r PageRevision
	err := row.Scan(&r.ID, &r.PageID, &r.Title, &r.Content, &r.AuthorID, &r.CreatedAt)
	return r, err
}

// resolvePage fetches a page of this project, else ErrPageNotFound.
func resolvePage(ctx context.Context, q queryRower, projectID, pageID string) (Page, error) {
	if !isUUIDFormat(pageID) {
		return Page{}, ErrInvalidPageID
	}
	p, err := scanPage(q.QueryRow(ctx,
		`SELECT `+pageColumns+` FROM pages WHERE id = $1::uuid AND project_id = $2::uuid`,
		pageID, projectID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Page{}, ErrPageNotFound
		}
		return Page{}, err
	}
	return p, nil
}

// resolveParent validates an optional parent id: nil = root; otherwise it
// must be a page of the same project.
func resolveParent(ctx context.Context, q queryRower, projectID string, parentID *string) error {
	if parentID == nil {
		return nil
	}
	if _, err := resolvePage(ctx, q, projectID, *parentID); err != nil {
		if errors.Is(err, ErrPageNotFound) {
			return ErrInvalidPage
		}
		return err
	}
	return nil
}

// CreatePage creates a page. Guests cannot create (member 15+). The page
// lands at the end of its sibling list. A foreign parent is 400, not a
// cross-project leak.
func CreatePage(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string, in PageInput) (*Page, error) {
	projectID, err := resolvePageProject(ctx, pool, wsSlug, identifier, actorID, true)
	if err != nil {
		return nil, err
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return nil, ErrInvalidPage
	}
	if err := resolveParent(ctx, pool, projectID, in.ParentID); err != nil {
		return nil, err
	}
	content := ""
	if in.Content != nil {
		content = *in.Content
	}
	var parentArg any
	if in.ParentID != nil {
		parentArg = *in.ParentID
	}
	p, err := scanPage(pool.QueryRow(ctx,
		`INSERT INTO pages (project_id, parent_id, title, content, position, author_id)
		 VALUES ($1::uuid, $2::uuid, $3, $4,
		         COALESCE((SELECT MAX(position) FROM pages
		                   WHERE project_id = $1::uuid
		                     AND parent_id IS NOT DISTINCT FROM $2::uuid), -1) + 1,
		         $5::uuid)
		 RETURNING `+pageColumns,
		projectID, parentArg, title, content, actorID))
	if err != nil {
		return nil, err
	}
	announcePageUpdated(ctx, pool, p.ID, projectID)
	return &p, nil
}

// ListPages returns every page of the project as a flat list; the
// frontend builds the tree. Roots first, then siblings by position.
func ListPages(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string) ([]Page, error) {
	projectID, err := resolvePageProject(ctx, pool, wsSlug, identifier, actorID, false)
	if err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx,
		`SELECT `+pageColumns+` FROM pages
		  WHERE project_id = $1::uuid
		  ORDER BY parent_id NULLS FIRST, position, id`,
		projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	pages := []Page{}
	for rows.Next() {
		p, err := scanPage(rows)
		if err != nil {
			return nil, err
		}
		pages = append(pages, p)
	}
	return pages, rows.Err()
}

// GetPage returns one page of the project.
func GetPage(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, pageID string) (*Page, error) {
	projectID, err := resolvePageProject(ctx, pool, wsSlug, identifier, actorID, false)
	if err != nil {
		return nil, err
	}
	p, err := resolvePage(ctx, pool, projectID, pageID)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// insertRevision records a pre-update snapshot inside a tx.
func insertRevision(ctx context.Context, tx pgx.Tx, p Page, actorID string) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO page_revisions (page_id, title, content, author_id)
		 VALUES ($1::uuid, $2, $3, $4::uuid)`,
		p.ID, p.Title, p.Content, actorID)
	return err
}

// UpdatePage applies a partial update. A title/content change records a
// revision of the pre-update state first (same tx — no lost history).
// Member (15)+.
func UpdatePage(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, pageID string, patch PagePatch) (*Page, error) {
	projectID, err := resolvePageProject(ctx, pool, wsSlug, identifier, actorID, true)
	if err != nil {
		return nil, err
	}
	p, err := resolvePage(ctx, pool, projectID, pageID)
	if err != nil {
		return nil, err
	}

	var sets []string
	var args []any
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if patch.Title != nil {
		title := strings.TrimSpace(*patch.Title)
		if title == "" {
			return nil, ErrInvalidPage
		}
		sets = append(sets, "title = "+arg(title))
	}
	if patch.Content != nil {
		sets = append(sets, "content = "+arg(*patch.Content))
	}
	if len(sets) == 0 {
		return nil, ErrNothingToUpdate
	}
	sets = append(sets, "updated_at = now()")

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// Only snapshot when something visible actually changed.
	if patch.Title != nil && *patch.Title != p.Title || patch.Content != nil && *patch.Content != p.Content {
		if err := insertRevision(ctx, tx, p, actorID); err != nil {
			return nil, err
		}
	}
	up, err := scanPage(tx.QueryRow(ctx,
		`UPDATE pages SET `+strings.Join(sets, ", ")+`
		  WHERE id = `+arg(pageID)+`::uuid AND project_id = `+arg(projectID)+`::uuid
		  RETURNING `+pageColumns, args...))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	announcePageUpdated(ctx, pool, up.ID, projectID)
	return &up, nil
}

// DeletePage deletes a page; its whole subtree goes with it via
// ON DELETE CASCADE (documented choice, C3T3). Member (15)+.
func DeletePage(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, pageID string) error {
	projectID, err := resolvePageProject(ctx, pool, wsSlug, identifier, actorID, true)
	if err != nil {
		return err
	}
	if _, err := resolvePage(ctx, pool, projectID, pageID); err != nil {
		return err
	}
	_, err = pool.Exec(ctx,
		`DELETE FROM pages WHERE id = $1::uuid AND project_id = $2::uuid`,
		pageID, projectID)
	if err != nil {
		return err
	}
	announcePageUpdated(ctx, pool, pageID, projectID)
	return nil
}

// sameParent compares two nullable parent ids.
func sameParent(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// isDescendant reports whether candidate is pageID itself or one of its
// descendants (recursive CTE).
func isDescendant(ctx context.Context, q queryRower, pageID, candidate string) (bool, error) {
	var one int
	err := q.QueryRow(ctx,
		`WITH RECURSIVE descendants AS (
		     SELECT id FROM pages WHERE id = $1::uuid
		     UNION ALL
		     SELECT p.id FROM pages p JOIN descendants d ON p.parent_id = d.id
		 )
		 SELECT 1 FROM descendants WHERE id = $2::uuid`,
		pageID, candidate).Scan(&one)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// MovePage reparents and/or reorders a page. ParentIDSet distinguishes a
// missing key (keep parent) from explicit null (move to root). A move
// under itself or a descendant is 400 (cycle guard). Member (15)+.
func MovePage(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, pageID string, in PageMoveInput) (*Page, error) {
	projectID, err := resolvePageProject(ctx, pool, wsSlug, identifier, actorID, true)
	if err != nil {
		return nil, err
	}
	p, err := resolvePage(ctx, pool, projectID, pageID)
	if err != nil {
		return nil, err
	}
	if !in.ParentIDSet && !in.PositionSet {
		return nil, ErrNothingToUpdate
	}

	newParent := p.ParentID // keep by default
	if in.ParentIDSet {
		if in.ParentID != nil {
			if err := resolveParent(ctx, pool, projectID, in.ParentID); err != nil {
				return nil, err
			}
			// Cycle guard: the new parent must not be the page itself
			// or one of its descendants.
			desc, err := isDescendant(ctx, pool, pageID, *in.ParentID)
			if err != nil {
				return nil, err
			}
			if desc {
				return nil, ErrPageCycle
			}
		}
		newParent = in.ParentID
	}
	reparented := in.ParentIDSet && !sameParent(p.ParentID, newParent)
	position := p.Position
	if in.PositionSet {
		if in.Position < 0 {
			return nil, ErrInvalidPage
		}
		position = in.Position
	}
	var parentArg any
	if newParent != nil {
		parentArg = *newParent
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if !in.PositionSet && reparented {
		// Reparented without an explicit position: land at the end of
		// the new sibling list (like CreatePage), not with the stale
		// position from the old parent. MAX is read inside the tx so
		// the shift below sees a consistent snapshot.
		var maxPos *int
		err := tx.QueryRow(ctx,
			`SELECT MAX(position) FROM pages
			  WHERE project_id = $1::uuid
			    AND parent_id IS NOT DISTINCT FROM $2::uuid
			    AND id <> $3::uuid`,
			projectID, parentArg, pageID).Scan(&maxPos)
		if err != nil {
			return nil, err
		}
		if maxPos == nil {
			position = 0
		} else {
			position = *maxPos + 1
		}
	}

	// Make room at the target position among the new siblings.
	if _, err := tx.Exec(ctx,
		`UPDATE pages SET position = position + 1
		  WHERE project_id = $1::uuid
		    AND parent_id IS NOT DISTINCT FROM $2::uuid
		    AND id <> $3::uuid
		    AND position >= $4`,
		projectID, parentArg, pageID, position); err != nil {
		return nil, err
	}
	up, err := scanPage(tx.QueryRow(ctx,
		`UPDATE pages SET parent_id = $2::uuid, position = $3, updated_at = now()
		  WHERE id = $1::uuid AND project_id = $4::uuid
		  RETURNING `+pageColumns,
		pageID, parentArg, position, projectID))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	announcePageUpdated(ctx, pool, up.ID, projectID)
	return &up, nil
}

// ListPageRevisions returns a page's revision history, newest first.
func ListPageRevisions(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, pageID string) ([]PageRevision, error) {
	projectID, err := resolvePageProject(ctx, pool, wsSlug, identifier, actorID, false)
	if err != nil {
		return nil, err
	}
	if _, err := resolvePage(ctx, pool, projectID, pageID); err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx,
		`SELECT `+revisionColumns+` FROM page_revisions
		  WHERE page_id = $1::uuid
		  ORDER BY created_at DESC, id DESC`,
		pageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	revs := []PageRevision{}
	for rows.Next() {
		r, err := scanRevision(rows)
		if err != nil {
			return nil, err
		}
		revs = append(revs, r)
	}
	return revs, rows.Err()
}

// RestorePageRevision restores a page to a revision's title+content. The
// pre-restore state is snapshotted first (same tx), so a restore is
// itself undoable. Member (15)+.
func RestorePageRevision(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, pageID, revisionID string) (*Page, error) {
	projectID, err := resolvePageProject(ctx, pool, wsSlug, identifier, actorID, true)
	if err != nil {
		return nil, err
	}
	p, err := resolvePage(ctx, pool, projectID, pageID)
	if err != nil {
		return nil, err
	}
	if !isUUIDFormat(revisionID) {
		return nil, ErrInvalidPageID
	}
	rev, err := scanRevision(pool.QueryRow(ctx,
		`SELECT `+revisionColumns+` FROM page_revisions
		  WHERE id = $1::uuid AND page_id = $2::uuid`,
		revisionID, pageID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrRevisionNotFound
		}
		return nil, err
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err := insertRevision(ctx, tx, p, actorID); err != nil {
		return nil, err
	}
	up, err := scanPage(tx.QueryRow(ctx,
		`UPDATE pages SET title = $2, content = $3, updated_at = now()
		  WHERE id = $1::uuid AND project_id = $4::uuid
		  RETURNING `+pageColumns,
		pageID, rev.Title, rev.Content, projectID))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	announcePageUpdated(ctx, pool, up.ID, projectID)
	return &up, nil
}
