package service

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrWorkItemNotFound is the single 404 sentinel for the global
	// work-item endpoints (GET /api/v1/work-items/{display-id}). Unknown
	// IDs, malformed IDs, ambiguous identifiers, and non-member callers
	// all collapse here — one answer, no enumeration oracle.
	ErrWorkItemNotFound = errors.New("service: work item not found")
	// ErrQueryRequired is returned when the global search is called
	// without a q parameter.
	ErrQueryRequired = errors.New("service: q is required")
)

// searchCursorOrder is the fixed order key minted into search cursors.
const searchCursorOrder = "search"

// parseDisplayID splits a {IDENTIFIER}-{SEQ} display ID (e.g. ENG-123).
// Identifiers never contain '-' (identifierRe), so the last dash is the
// separator. The identifier is normalized (uppercased); the sequence must
// be all digits and > 0. Anything else is ErrWorkItemNotFound — a
// malformed ID matches nothing, and the caller must not learn why.
func parseDisplayID(displayID string) (ident string, seq int, err error) {
	dash := strings.LastIndex(displayID, "-")
	if dash < 0 {
		return "", 0, ErrWorkItemNotFound
	}
	ident, err = normalizeIdentifier(displayID[:dash])
	if err != nil {
		return "", 0, ErrWorkItemNotFound
	}
	seqStr := displayID[dash+1:]
	if seqStr == "" {
		return "", 0, ErrWorkItemNotFound
	}
	for i := 0; i < len(seqStr); i++ {
		if seqStr[i] < '0' || seqStr[i] > '9' {
			return "", 0, ErrWorkItemNotFound
		}
	}
	seq, err = strconv.Atoi(seqStr)
	if err != nil || seq < 1 {
		return "", 0, ErrWorkItemNotFound
	}
	return ident, seq, nil
}

// GetIssueByDisplayID resolves a global display ID ({IDENTIFIER}-{SEQ})
// to the same issue-detail shape as GetIssue. Identifiers are unique per
// workspace, not globally: candidates are limited to workspaces the actor
// is a member of, and an identifier matching more than one of them is
// ambiguous → ErrWorkItemNotFound (no enumeration, no cross-workspace
// leak). Any workspace member (guest 5+) may read.
func GetIssueByDisplayID(ctx context.Context, pool *pgxpool.Pool, actorID, displayID string) (*IssueListItem, error) {
	ident, seq, err := parseDisplayID(displayID)
	if err != nil {
		return nil, err
	}

	rows, err := pool.Query(ctx,
		`SELECT p.id::text, p.identifier FROM projects p
		 JOIN workspace_members m ON m.workspace_id = p.workspace_id
		 WHERE m.user_id = $1::uuid AND p.identifier = $2`,
		actorID, ident)
	if err != nil {
		return nil, err
	}
	type projectHit struct{ id, identifier string }
	var hits []projectHit
	for rows.Next() {
		var h projectHit
		if err := rows.Scan(&h.id, &h.identifier); err != nil {
			rows.Close()
			return nil, err
		}
		hits = append(hits, h)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Zero hits (unknown identifier or non-member everywhere) and
	// multiple hits (ambiguous identifier) share one answer.
	if len(hits) != 1 {
		return nil, ErrWorkItemNotFound
	}

	var issueID string
	err = pool.QueryRow(ctx,
		`SELECT i.id::text FROM issues i
		 WHERE i.project_id = $1::uuid AND i.sequence_id = $2 AND i.deleted_at IS NULL`,
		hits[0].id, seq).Scan(&issueID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrWorkItemNotFound
		}
		return nil, err
	}
	return getIssueRow(ctx, pool, hits[0].identifier, hits[0].id, issueID)
}

// SearchIssueItem is one global-search hit: the issue plus the workspace
// and project context needed to link to it.
type SearchIssueItem struct {
	IssueListItem
	WorkspaceSlug     string `json:"workspace_slug"`
	ProjectIdentifier string `json:"project_identifier"`
	// rank carries the ts_rank of this row for cursor minting; it is
	// unexported so it never appears in the JSON response.
	rank float64
}

// SearchIssuesResult is the paginated search envelope (spec §5).
type SearchIssuesResult struct {
	Results    []SearchIssueItem `json:"results"`
	NextCursor string            `json:"next_cursor,omitempty"`
}

// SearchIssues runs a global full-text search over issue titles and
// descriptions, scoped to workspaces the actor is a member of. Ranking is
// ts_rank DESC with the issue id as a stable tiebreak; pagination is a
// keyset cursor minted for the fixed "search" order. Any workspace member
// (guest 5+) may search.
func SearchIssues(ctx context.Context, pool *pgxpool.Pool, actorID, q, cursor string, perPage int) (*SearchIssuesResult, error) {
	if strings.TrimSpace(q) == "" {
		return nil, ErrQueryRequired
	}
	switch {
	case perPage < 0:
		return nil, ErrInvalidListFilter
	case perPage == 0:
		perPage = defaultListPerPage
	case perPage > maxListPerPage:
		perPage = maxListPerPage
	}

	var cur *listCursor
	if cursor != "" {
		c, err := decodeListCursor(cursor, searchCursorOrder)
		if err != nil {
			return nil, err
		}
		if !validCursorValue(listSortFloat, c.Value) {
			return nil, ErrInvalidCursor
		}
		cur = &c
	}

	// The rank expression is deterministic for a fixed query, so keyset
	// pagination on (rank, id) is stable across pages.
	query := `WITH q AS (SELECT plainto_tsquery('english', $1) AS tsq)
		SELECT ` + issueColumnsI + `, ` + assigneesAgg + `, ` + labelsAgg + `,
			ts_rank(i.search, q.tsq) AS rank, p.identifier, w.slug
		FROM issues i
		JOIN projects p ON p.id = i.project_id
		JOIN workspaces w ON w.id = p.workspace_id
		JOIN workspace_members m ON m.workspace_id = w.id AND m.user_id = $2::uuid
		CROSS JOIN q
		WHERE i.deleted_at IS NULL AND i.search @@ q.tsq`
	args := []any{q, actorID}
	if cur != nil {
		query += ` AND (ts_rank(i.search, q.tsq) < $3::float8
			OR (ts_rank(i.search, q.tsq) = $3::float8 AND i.id::text > $4))`
		args = append(args, cur.Value, cur.ID)
	}
	query += ` ORDER BY rank DESC, i.id::text ASC LIMIT ` + strconv.Itoa(perPage+1)

	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	res := &SearchIssuesResult{Results: []SearchIssueItem{}}
	for rows.Next() {
		var item SearchIssueItem
		var description []byte
		var assigneesJSON, labelsJSON []byte
		var rank float64
		if err := rows.Scan(
			&item.ID, &item.ProjectID, &item.SequenceID, &item.Name,
			&description,
			&item.Priority, &item.StateID, &item.ParentID, &item.SortOrder,
			&item.StartDate, &item.TargetDate, &item.EstimatePointID,
			&item.IsDraft, &item.ArchivedAt,
			&item.CreatedBy, &item.CreatedAt, &item.UpdatedAt,
			&assigneesJSON, &labelsJSON,
			&rank, &item.ProjectIdentifier, &item.WorkspaceSlug,
		); err != nil {
			return nil, err
		}
		if description != nil {
			item.Description = json.RawMessage(description)
		}
		if err := unmarshalRelations(assigneesJSON, labelsJSON, &item.IssueListItem); err != nil {
			return nil, err
		}
		item.DisplayID = item.ProjectIdentifier + "-" + strconv.Itoa(item.SequenceID)
		item.rank = rank
		res.Results = append(res.Results, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(res.Results) > perPage {
		last := res.Results[perPage-1]
		res.NextCursor = encodeListCursor(searchCursorOrder,
			strconv.FormatFloat(last.rank, 'g', -1, 64), last.ID)
		res.Results = res.Results[:perPage]
	}
	return res, nil
}
