package service

// Pages global search (C66T0). GET /api/v1/search was issues-only; pages
// (title + content, migration 000066) now get the same search
// discipline as issues: plainto_tsquery, ts_rank DESC with the page id as
// a stable tiebreak, keyset cursor on (rank, id), member-only authz
// (guest 5+), archived-project skip. Pages have no deleted_at
// (ON DELETE CASCADE with their project) so there is nothing to filter.

import (
	"context"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// SearchPageItem is one page hit in global search: the page plus the
// workspace and project context needed to link to it.
type SearchPageItem struct {
	ID                string `json:"id"`
	Title             string `json:"title"`
	ProjectIdentifier string `json:"project_identifier"`
	WorkspaceSlug     string `json:"workspace_slug"`
	// rank carries the ts_rank of this row for cursor minting; it is
	// unexported so it never appears in the JSON response.
	rank float64
}

// SearchPagesResult is the paginated page-search envelope. Its JSON tags
// are namespaced (page_results/page_next_cursor) because it is merged
// into the GET /api/v1/search envelope next to the issue hits.
type SearchPagesResult struct {
	Results    []SearchPageItem `json:"page_results"`
	NextCursor string           `json:"page_next_cursor,omitempty"`
}

// archivedProjectsFilter returns the SQL predicate fragment excluding
// archived projects, qualified with the given projects-table alias.
// C18T3: the filter is defensive — projects.archived_at may be absent on
// a database migrated before 000048 — so its presence is probed in Go
// and the predicate is only appended when the column exists. (A SQL
// OR-guard would NOT work: an unknown column is a parse-time error
// regardless of runtime short-circuiting.)
func archivedProjectsFilter(ctx context.Context, pool *pgxpool.Pool, alias string) (string, error) {
	var hasArchivedCol bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM information_schema.columns
		WHERE table_name = 'projects' AND column_name = 'archived_at')`).Scan(&hasArchivedCol); err != nil {
		return "", err
	}
	if hasArchivedCol {
		return " AND " + alias + ".archived_at IS NULL", nil
	}
	return "", nil
}

// SearchPages runs a global full-text search over page titles and
// content, scoped to workspaces the actor is a member of. The title is
// weighted above the content (setweight 'A'/'B' in migration 000066);
// ranking is ts_rank DESC with the page id as a stable tiebreak;
// pagination is a keyset cursor minted for the fixed "search" order.
// Any workspace member (guest 5+) may search.
func SearchPages(ctx context.Context, pool *pgxpool.Pool, actorID, q, cursor string, perPage int) (*SearchPagesResult, error) {
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

	// Same defensive archived-project skip as SearchIssues: filter in SQL
	// (not post-hoc) so keyset pagination page sizes stay correct.
	archivedFilter, err := archivedProjectsFilter(ctx, pool, "p")
	if err != nil {
		return nil, err
	}
	// The rank expression is deterministic for a fixed query, so keyset
	// pagination on (rank, id) is stable across pages.
	query := `WITH q AS (SELECT plainto_tsquery('english', $1) AS tsq)
		SELECT pg.id::text, pg.title, p.identifier, w.slug,
			ts_rank(pg.search, q.tsq) AS rank
		FROM pages pg
		JOIN projects p ON p.id = pg.project_id
		JOIN workspaces w ON w.id = p.workspace_id
		JOIN workspace_members m ON m.workspace_id = w.id AND m.user_id = $2::uuid
		CROSS JOIN q
		WHERE pg.search @@ q.tsq` + archivedFilter
	args := []any{q, actorID}
	if cur != nil {
		query += ` AND (ts_rank(pg.search, q.tsq) < $3::float8
			OR (ts_rank(pg.search, q.tsq) = $3::float8 AND pg.id::text > $4))`
		args = append(args, cur.Value, cur.ID)
	}
	query += ` ORDER BY rank DESC, pg.id::text ASC LIMIT ` + strconv.Itoa(perPage+1)

	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	res := &SearchPagesResult{Results: []SearchPageItem{}}
	for rows.Next() {
		var item SearchPageItem
		var rank float64
		if err := rows.Scan(
			&item.ID, &item.Title, &item.ProjectIdentifier, &item.WorkspaceSlug,
			&rank,
		); err != nil {
			return nil, err
		}
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
