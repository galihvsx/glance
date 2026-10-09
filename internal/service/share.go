package service

// Public share links (C4T5): issues and pages shareable via unguessable
// token URLs, no login required to view.
//
// Conventions:
//   - Tenancy: management endpoints (create/list/revoke) resolve
//     workspace membership + project, then the live resource, via
//     resolveSatelliteIssue (issues) or the page resolver (pages).
//     Mutations need member (15)+ via requireSatelliteWriter; listing
//     needs any member (guest 5+).
//   - The token is the ONLY capability for the public endpoint:
//     GetPublicShare looks up by token alone (single indexed query),
//     and treats unknown / revoked / expired tokens identically as
//     ErrShareNotFound (404, never 403 — existence must not leak).
//   - PII: the public payload is a purpose-built sanitized shape, NOT
//     the internal Issue/Page structs. Excluded, deliberately:
//       * all internal UUIDs (issue id, project id, user ids)
//       * created_by / author ids and any email addresses
//       * assignees (names+emails would leak the team roster)
//     Included: display_id, name/title, description/content, state
//     name+group, priority, label names+colors, dates, timestamps.
//     Display names of commenters are not included at all — comments
//     are not part of the shared payload.
//   - Tokens: crypto/rand 24 bytes, base64url no-padding (32 chars).
//     The UNIQUE constraint is the backstop; generation retries on the
//     astronomically unlikely collision.

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrShareNotFound is returned for an unknown, revoked, or expired
	// token, or a share link addressing a missing resource. The handler
	// maps it to 404 in all cases — never 403 — so token existence does
	// not leak.
	ErrShareNotFound = errors.New("service: share link not found")
	// ErrInvalidShare is returned for a malformed share request: bad
	// scope, unparseable expires_at, or expires_at in the past.
	ErrInvalidShare = errors.New("service: invalid share link request")
)

// Share scopes.
const (
	ShareScopeIssue = "issue"
	ShareScopePage  = "page"
)

// ShareLink is one share link (management view — never served publicly).
type ShareLink struct {
	ID        string     `json:"id"`
	Token     string     `json:"token"`
	Scope     string     `json:"scope"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

// PublicIssue is the sanitized read-only issue payload served to
// anonymous token holders. See the package comment for the PII policy.
type PublicIssue struct {
	DisplayID   string        `json:"display_id"`
	Name        string        `json:"name"`
	Description any           `json:"description,omitempty"`
	State       string        `json:"state"`
	StateGroup  string        `json:"state_group"`
	Priority    int           `json:"priority"`
	Labels      []PublicLabel `json:"labels"`
	StartDate   *time.Time    `json:"start_date,omitempty"`
	TargetDate  *time.Time    `json:"target_date,omitempty"`
	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
}

// PublicLabel is a label with its display attributes only (no id).
type PublicLabel struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

// PublicPage is the sanitized read-only page payload.
type PublicPage struct {
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// PublicShare is the envelope for GET /api/v1/public/s/{token}.
type PublicShare struct {
	Scope string       `json:"scope"`
	Issue *PublicIssue `json:"issue,omitempty"`
	Page  *PublicPage  `json:"page,omitempty"`
}

// generateShareToken returns a url-safe 32-char token from 24 random bytes.
func generateShareToken() (string, error) {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

const shareColumns = `id::text, token, scope, expires_at, revoked_at, created_at`

func scanShareLink(row pgx.Row) (*ShareLink, error) {
	var s ShareLink
	if err := row.Scan(&s.ID, &s.Token, &s.Scope, &s.ExpiresAt, &s.RevokedAt, &s.CreatedAt); err != nil {
		return nil, err
	}
	return &s, nil
}

// CreateShareLink mints a share link for a live issue. Member (15)+.
// expiresAt nil = never expires; a past expiresAt is rejected.
func CreateShareLink(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, issueID, actorID string, expiresAt *time.Time) (*ShareLink, error) {
	_, projectID, role, err := resolveSatelliteIssue(ctx, pool, wsSlug, identifier, issueID, actorID)
	if err != nil {
		return nil, err
	}
	if err := requireSatelliteWriter(role); err != nil {
		return nil, err
	}
	return createShareLinkTx(ctx, pool, ShareScopeIssue, normalizeIssueID(issueID), projectID, wsSlug, actorID, expiresAt)
}

// CreatePageShareLink mints a share link for a live page. Member (15)+.
// resolvePageProject (page.go) enforces the writer role when needMember
// is true.
func CreatePageShareLink(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, pageID, actorID string, expiresAt *time.Time) (*ShareLink, error) {
	projectID, err := resolvePageProject(ctx, pool, wsSlug, identifier, actorID, true)
	if err != nil {
		return nil, err
	}
	if _, err := resolvePage(ctx, pool, projectID, pageID); err != nil {
		return nil, err
	}
	return createShareLinkTx(ctx, pool, ShareScopePage, pageID, projectID, wsSlug, actorID, expiresAt)
}

func createShareLinkTx(ctx context.Context, pool *pgxpool.Pool, scope, resourceID, projectID, wsSlug, actorID string, expiresAt *time.Time) (*ShareLink, error) {
	if expiresAt != nil && !expiresAt.After(time.Now()) {
		return nil, ErrInvalidShare
	}
	var wsID string
	err := pool.QueryRow(ctx,
		`SELECT id::text FROM workspaces WHERE slug = $1`, wsSlug).Scan(&wsID)
	if err != nil {
		// Unreachable in practice: the caller already resolved workspace
		// membership. Map to ErrNotFound for a clean envelope regardless.
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	// Retry on the astronomically unlikely token collision.
	for i := 0; i < 3; i++ {
		token, err := generateShareToken()
		if err != nil {
			return nil, err
		}
		var s ShareLink
		err = pool.QueryRow(ctx,
			`INSERT INTO share_links (token, scope, resource_id, workspace_id, project_id, created_by, expires_at)
			 VALUES ($1, $2, $3::uuid, $4::uuid, $5::uuid, $6::uuid, $7)
			 RETURNING `+shareColumns,
			token, scope, resourceID, wsID, projectID, actorID, expiresAt).Scan(
			&s.ID, &s.Token, &s.Scope, &s.ExpiresAt, &s.RevokedAt, &s.CreatedAt)
		if err == nil {
			return &s, nil
		}
		if isUniqueViolation(err) {
			continue
		}
		return nil, err
	}
	return nil, errors.New("service: share token collision after retries")
}

// ListShareLinks lists a resource's non-revoked share links. Any member.
func ListShareLinks(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, issueID, actorID string) ([]ShareLink, error) {
	if _, _, _, err := resolveSatelliteIssue(ctx, pool, wsSlug, identifier, issueID, actorID); err != nil {
		return nil, err
	}
	return listShareLinksTx(ctx, pool, ShareScopeIssue, normalizeIssueID(issueID))
}

// ListPageShareLinks lists a page's non-revoked share links. Any member.
func ListPageShareLinks(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, pageID, actorID string) ([]ShareLink, error) {
	projectID, err := resolvePageProject(ctx, pool, wsSlug, identifier, actorID, false)
	if err != nil {
		return nil, err
	}
	if _, err := resolvePage(ctx, pool, projectID, pageID); err != nil {
		return nil, err
	}
	return listShareLinksTx(ctx, pool, ShareScopePage, pageID)
}

func listShareLinksTx(ctx context.Context, pool *pgxpool.Pool, scope, resourceID string) ([]ShareLink, error) {
	rows, err := pool.Query(ctx,
		`SELECT `+shareColumns+` FROM share_links
		 WHERE scope = $1 AND resource_id = $2::uuid AND revoked_at IS NULL
		 ORDER BY created_at DESC`, scope, resourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ShareLink{}
	for rows.Next() {
		s, err := scanShareLink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *s)
	}
	return out, rows.Err()
}

// RevokeShareLink revokes a token. The caller must be a member (15)+ of the
// link's workspace; unknown or already-revoked tokens are ErrShareNotFound
// (404 — existence does not leak).
func RevokeShareLink(ctx context.Context, pool *pgxpool.Pool, wsSlug, actorID, token string) error {
	var wsID string
	var role int
	err := pool.QueryRow(ctx,
		`SELECT w.id::text, m.role FROM workspaces w
		 JOIN workspace_members m ON m.workspace_id = w.id
		 WHERE w.slug = $1 AND m.user_id = $2::uuid`, wsSlug, actorID).Scan(&wsID, &role)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if err := requireSatelliteWriter(role); err != nil {
		return err
	}
	ct, err := pool.Exec(ctx,
		`UPDATE share_links SET revoked_at = now()
		 WHERE token = $1 AND workspace_id = $2::uuid AND revoked_at IS NULL`,
		token, wsID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrShareNotFound
	}
	return nil
}

// GetPublicShare resolves a token to its sanitized payload. No auth.
// Unknown, revoked, or expired tokens — and tokens whose resource was
// deleted — all surface as ErrShareNotFound (404).
func GetPublicShare(ctx context.Context, pool *pgxpool.Pool, token string) (*PublicShare, error) {
	var scope, resourceID string
	err := pool.QueryRow(ctx,
		`SELECT scope, resource_id::text FROM share_links
		 WHERE token = $1 AND revoked_at IS NULL
		   AND (expires_at IS NULL OR expires_at > now())`, token).Scan(&scope, &resourceID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrShareNotFound
		}
		return nil, err
	}
	switch scope {
	case ShareScopeIssue:
		iss, err := publicIssue(ctx, pool, resourceID)
		if err != nil {
			return nil, err
		}
		return &PublicShare{Scope: scope, Issue: iss}, nil
	case ShareScopePage:
		p, err := publicPage(ctx, pool, resourceID)
		if err != nil {
			return nil, err
		}
		return &PublicShare{Scope: scope, Page: p}, nil
	default:
		return nil, ErrShareNotFound
	}
}

// publicIssue builds the sanitized issue payload: display attributes only.
// Soft-deleted issues 404 like the issue itself does.
func publicIssue(ctx context.Context, pool *pgxpool.Pool, issueID string) (*PublicIssue, error) {
	var out PublicIssue
	var desc []byte
	var labelsJSON []byte
	err := pool.QueryRow(ctx,
		`SELECT i.display_id, i.name, i.description, s.name, s."group",
		        i.priority, i.start_date, i.target_date, i.created_at, i.updated_at,
		        (SELECT COALESCE(json_agg(jsonb_build_object('name', l.name, 'color', l.color)
		             ORDER BY l.name)::jsonb, '[]'::jsonb)
		         FROM issue_labels il JOIN labels l ON l.id = il.label_id
		         WHERE il.issue_id = i.id)
		 FROM issues i JOIN states s ON s.id = i.state_id
		 WHERE i.id = $1::uuid AND i.deleted_at IS NULL`,
		issueID).Scan(&out.DisplayID, &out.Name, &desc, &out.State, &out.StateGroup,
		&out.Priority, &out.StartDate, &out.TargetDate, &out.CreatedAt, &out.UpdatedAt,
		&labelsJSON)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return nil, ErrShareNotFound
		}
		return nil, err
	}
	if len(desc) > 0 {
		out.Description = desc
	}
	out.Labels = []PublicLabel{}
	if err := json.Unmarshal(labelsJSON, &out.Labels); err != nil {
		return nil, fmt.Errorf("service: decode public labels: %w", err)
	}
	return &out, nil
}

// publicPage builds the sanitized page payload.
func publicPage(ctx context.Context, pool *pgxpool.Pool, pageID string) (*PublicPage, error) {
	var out PublicPage
	err := pool.QueryRow(ctx,
		`SELECT title, content, created_at, updated_at FROM pages
		 WHERE id = $1::uuid`, pageID).Scan(&out.Title, &out.Content, &out.CreatedAt, &out.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return nil, ErrShareNotFound
		}
		return nil, err
	}
	return &out, nil
}
