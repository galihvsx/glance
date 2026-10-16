package service

// Page↔issue links (C36T0): links between wiki pages and issues, in
// both directions, from one link table — the same link is addressed
// from the page side (GET/POST/DELETE .../pages/{pageID}/issues) and
// from the issue side (GET/POST/DELETE .../issues/{uuid}/pages).
//
// A link is a pointer, not a copy: no bidirectional sync, no
// annotations, no notifications. Archived and draft issues ARE
// linkable, same as issue_links — no exclusion is invented for either.
//
// Tenancy (mirrors issue_links + page comments): every op resolves
// workspace membership + project via resolveIssueProject. A bad slug
// or non-member caller surfaces ErrNotFound ("workspace not found");
// a bad project identifier surfaces ErrProjectNotFound; an unknown
// page surfaces ErrPageNotFound; an unknown issue surfaces
// ErrIssueNotFound. Distinct 404s on purpose (Task 11 ruling).
//
// Roles: any member (guest 5+) may read; mutations need member (15)+.
//
// Honest scope: duplicate → 409 via the UNIQUE (page_id, issue_id)
// backstop; a live issue in another project → 409
// ErrPageIssueLinkCrossProject. There is no self-link concept (a page
// and an issue are different entity kinds).

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrPageIssueLinkNotFound is returned when the (page, issue) pair
	// matches no link row. The handler maps it to 404.
	ErrPageIssueLinkNotFound = errors.New("service: page-issue link not found")
	// ErrPageIssueLinkConflict is returned on a duplicate link: the
	// unique (page_id, issue_id) is the backstop, so a racing double
	// create serializes into a conflict instead of two rows. The
	// handler maps it to 409.
	ErrPageIssueLinkConflict = errors.New("service: page-issue link already exists")
	// ErrPageIssueLinkCrossProject is returned when the issue is live
	// but belongs to a different project than the page. The handler
	// maps it to 409.
	ErrPageIssueLinkCrossProject = errors.New("service: page-issue link issue is in another project")
	// ErrInvalidPageIssueLink is returned for a malformed page or issue
	// id supplied to link/unlink. The handler maps it to 400.
	ErrInvalidPageIssueLink = errors.New("service: invalid page-issue link")
)

// PageIssueLink is one page↔issue link row.
type PageIssueLink struct {
	ID        string    `json:"id"`
	PageID    string    `json:"page_id"`
	IssueID   string    `json:"issue_id"`
	CreatedBy *string   `json:"created_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// PageLinkedIssue is the page-side list shape: the linked issue's
// display_id, title, state name, and priority, ordered by issue
// created_at. The link id itself is not exposed — unlink addresses
// the pair (pageID, issueID).
type PageLinkedIssue struct {
	ID        string `json:"id"`
	DisplayID string `json:"display_id"`
	Title     string `json:"title"`
	State     string `json:"state"`
	StateID   string `json:"state_id"`
	Priority  int    `json:"priority"`
}

// PageLinkedPage is the issue-side list shape: the linked page's title
// and updated_at, ordered by page updated_at desc.
type PageLinkedPage struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	UpdatedAt time.Time `json:"updated_at"`
}

func scanPageIssueLink(row pgx.Row) (*PageIssueLink, error) {
	var l PageIssueLink
	err := row.Scan(&l.ID, &l.PageID, &l.IssueID, &l.CreatedBy, &l.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &l, nil
}

// resolvePageIssueLinkProject resolves (projectID, role) for the
// caller and enforces the minimum role: guest (5)+ reads, member
// (15)+ writes. The identifier is normalized first so display IDs
// are built from the canonical (uppercase) form.
func resolvePageIssueLinkProject(ctx context.Context, q queryRower, wsSlug, identifier, actorID string, minRole int) (ident, projectID string, role int, err error) {
	ident, err = normalizeIdentifier(identifier)
	if err != nil {
		return "", "", 0, err
	}
	_, projectID, role, err = resolveIssueProject(ctx, q, wsSlug, ident, actorID)
	if err != nil {
		return "", "", 0, err
	}
	if role < minRole {
		return "", "", 0, ErrForbidden
	}
	return ident, projectID, role, nil
}

// ListPageIssues returns a page's linked issues: display_id, title,
// state, priority, ordered by issue created_at. Soft-deleted issues
// never appear; archived and draft issues do. Any member (guest 5+)
// may read.
func ListPageIssues(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, pageID, actorID string) ([]PageLinkedIssue, error) {
	ident, projectID, _, err := resolvePageIssueLinkProject(ctx, pool, wsSlug, identifier, actorID, RoleGuest)
	if err != nil {
		return nil, err
	}
	if _, err := resolvePage(ctx, pool, projectID, pageID); err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx,
		`SELECT i.id::text, $2 || '-' || i.sequence_id::text, i.name,
		        s.name, i.state_id::text, i.priority
		 FROM page_issue_links l
		 JOIN issues i ON i.id = l.issue_id
		 JOIN states s ON s.id = i.state_id
		 WHERE l.page_id = $1::uuid
		   AND i.project_id = $3::uuid
		   AND i.deleted_at IS NULL
		 ORDER BY i.created_at ASC, i.id ASC`,
		pageID, ident, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PageLinkedIssue{}
	for rows.Next() {
		var li PageLinkedIssue
		if err := rows.Scan(&li.ID, &li.DisplayID, &li.Title, &li.State, &li.StateID, &li.Priority); err != nil {
			return nil, err
		}
		out = append(out, li)
	}
	return out, rows.Err()
}

// ListIssuePages returns an issue's linked pages: title, updated_at,
// ordered by page updated_at desc. Any member (guest 5+) may read.
func ListIssuePages(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, issueID, actorID string) ([]PageLinkedPage, error) {
	issueID = normalizeIssueID(issueID)
	_, projectID, role, err := resolveSatelliteIssue(ctx, pool, wsSlug, identifier, issueID, actorID)
	if err != nil {
		return nil, err
	}
	if role < RoleGuest {
		return nil, ErrForbidden
	}
	rows, err := pool.Query(ctx,
		`SELECT p.id::text, p.title, p.updated_at
		 FROM page_issue_links l
		 JOIN pages p ON p.id = l.page_id
		 WHERE l.issue_id = $1::uuid
		   AND p.project_id = $2::uuid
		 ORDER BY p.updated_at DESC, p.id ASC`,
		issueID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PageLinkedPage{}
	for rows.Next() {
		var lp PageLinkedPage
		if err := rows.Scan(&lp.ID, &lp.Title, &lp.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, lp)
	}
	return out, rows.Err()
}

// LinkPageIssue links a page to an issue. The page must be live in
// the caller's project (else ErrPageNotFound); the issue must be a
// live (not soft-deleted) issue in the same project — an unknown issue
// is ErrIssueNotFound, a live issue in another project is
// ErrPageIssueLinkCrossProject. A duplicate pair is
// ErrPageIssueLinkConflict. Archived and draft issues link fine.
// Member (15)+.
func LinkPageIssue(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, pageID, actorID, issueID string) (*PageIssueLink, error) {
	_, projectID, _, err := resolvePageIssueLinkProject(ctx, pool, wsSlug, identifier, actorID, RoleMember)
	if err != nil {
		return nil, err
	}
	if _, err := resolvePage(ctx, pool, projectID, pageID); err != nil {
		return nil, err
	}
	issueID = normalizeIssueID(issueID)
	if !isUUIDFormat(issueID) {
		return nil, ErrIssueNotFound
	}
	var issueProject string
	err = pool.QueryRow(ctx,
		`SELECT project_id::text FROM issues
		 WHERE id = $1::uuid AND deleted_at IS NULL`,
		issueID).Scan(&issueProject)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return nil, ErrIssueNotFound
		}
		return nil, err
	}
	if issueProject != projectID {
		return nil, ErrPageIssueLinkCrossProject
	}
	l, err := scanPageIssueLink(pool.QueryRow(ctx,
		`INSERT INTO page_issue_links (page_id, issue_id, created_by)
		 VALUES ($1::uuid, $2::uuid, $3::uuid)
		 RETURNING id::text, page_id::text, issue_id::text, created_by::text, created_at`,
		pageID, issueID, actorID))
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrPageIssueLinkConflict
		}
		return nil, err
	}
	return l, nil
}

// UnlinkPageIssue removes the link between a page and an issue. The
// page must be live in the caller's project (else ErrPageNotFound); a
// pair with no link row is ErrPageIssueLinkNotFound. The delete is
// scoped by the verified page, so a foreign issue id can never delete
// another project's link. Member (15)+.
func UnlinkPageIssue(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, pageID, actorID, issueID string) error {
	_, projectID, _, err := resolvePageIssueLinkProject(ctx, pool, wsSlug, identifier, actorID, RoleMember)
	if err != nil {
		return err
	}
	if _, err := resolvePage(ctx, pool, projectID, pageID); err != nil {
		return err
	}
	issueID = normalizeIssueID(issueID)
	if !isUUIDFormat(issueID) {
		return ErrInvalidPageIssueLink
	}
	tag, err := pool.Exec(ctx,
		`DELETE FROM page_issue_links
		 WHERE page_id = $1::uuid AND issue_id = $2::uuid`,
		pageID, issueID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrPageIssueLinkNotFound
	}
	return nil
}
