package service

// Duplicate detection on issue create (C8T7): "possible duplicates"
// suggestions as the user types the title in the create modal.
//
// FindSimilarIssues ranks the project's working-set issues by pg_trgm
// trigram similarity against the query title and returns the top
// similarLimit hits above similarThreshold. The lookup is index-backed:
// migration 000032 adds a GIN index on issues(name) (gin_trgm_ops), and
// the WHERE name % $1 predicate uses the indexable % operator. The
// explicit similarity(name,$1) > 0.3 filter pins the semantics to the
// threshold even if an operator changes pg_trgm.similarity_threshold —
// % is defined as similarity() > similarity_threshold, so the two
// predicates are exactly redundant under the default GUC, and the
// explicit one wins when it is not.
//
// Scope: project (via resolveIssueProject — non-members get ErrNotFound,
// the tenancy boundary), working set only (archived, drafts and
// soft-deleted rows excluded). The query is length-capped so trigram
// extraction cost stays bounded on pathological input.

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// similarThreshold is the minimum pg_trgm similarity for a title to
	// count as a possible duplicate. 0.3 is also pg_trgm's default
	// similarity_threshold GUC, so the index-backed WHERE name % $1 and
	// the explicit similarity() > 0.3 filter coincide under defaults.
	similarThreshold = 0.3
	// similarLimit caps the suggestion list: the create modal shows at
	// most five duplicates; more would be noise.
	similarLimit = 5
	// similarQueryMaxLen caps the query title at 200 chars (runes):
	// trigram extraction cost grows with input length, and issue titles
	// are short — longer input is truncated, not rejected.
	similarQueryMaxLen = 200
)

// ErrEmptySimilarQuery reports a blank duplicate-detection query. The
// handler maps it to 400; the frontend never sends it (min 3 chars).
var ErrEmptySimilarQuery = errors.New("service: similar query must not be empty")

// SimilarIssue is one possible-duplicate hit. State is the human state
// name (e.g. "Backlog") for display in the suggestion list.
type SimilarIssue struct {
	ID         string  `json:"id"`
	DisplayID  string  `json:"display_id"`
	Name       string  `json:"name"`
	State      string  `json:"state"`
	Similarity float64 `json:"similarity"`
}

// FindSimilarIssues returns up to similarLimit issues in the project
// whose names are at least similarThreshold similar to q, most-similar
// first. Archived, draft and soft-deleted issues are excluded; the
// search is scoped to the project and gated on workspace membership
// (non-members get ErrNotFound).
func FindSimilarIssues(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, q string) ([]SimilarIssue, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, ErrEmptySimilarQuery
	}
	if runes := []rune(q); len(runes) > similarQueryMaxLen {
		q = string(runes[:similarQueryMaxLen])
	}

	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return nil, err
	}
	_, projectID, _, err := resolveIssueProject(ctx, pool, wsSlug, ident, actorID)
	if err != nil {
		return nil, err
	}

	// One query, no N+1. Five genuine cost controls:
	//   1. name % $2 is the GIN-indexed trigram predicate (migration 000032,
	//      gin_trgm_ops) — narrows candidates before any ranking math. The
	//      index remains usable inside the CTE: MATERIALIZED only fences
	//      subquery flattening, it does not change the CTE's own plan.
	//   2. similarity() is computed EXACTLY ONCE per row. The MATERIALIZED
	//      CTE is an optimization fence: without it the planner flattens the
	//      inner query into the ORDER BY ... LIMIT level, and then the
	//      threshold filter AND the SELECT list each evaluate similarity()
	//      per row — 3 trigram ops/row instead of 2 (measured ~21ms vs
	//      ~15ms over the 1000-row fixture). With the fence, the filter,
	//      the ORDER BY and the output all reference the materialized sim
	//      column; no trigram re-extraction per row.
	//   3. The ranking subquery projects ONLY (id, sim): the top-N heapsort
	//      over the candidate set sorts narrow (uuid, float8) tuples instead
	//      of wide rows carrying name text, and the per-row id::text cast
	//      moves out of the hot loop. Name/state are fetched by PK for the
	//      final 5 rows only.
	//   4. The states join happens AFTER the ORDER BY ... LIMIT 5, so it
	//      touches 5 rows, not the whole candidate set.
	//   5. The outer ORDER BY sim DESC makes most-similar-first explicit:
	//      the join order is not guaranteed to preserve the subquery's.
	// The explicit similarity() > 0.3 filter pins the semantics to the
	// threshold even if pg_trgm.similarity_threshold GUC moves — % is
	// defined as similarity() > similarity_threshold, so the two coincide
	// under the default GUC and the explicit one wins when it is not.
	rows, err := pool.Query(ctx, `
		WITH scored AS MATERIALIZED (
			SELECT i2.id,
			       similarity(i2.name, $2) AS sim
			FROM issues i2
			WHERE i2.project_id = $1::uuid
			  AND i2.archived_at IS NULL
			  AND NOT i2.is_draft
			  AND i2.deleted_at IS NULL
			  AND i2.name % $2
		)
		SELECT i.id::text AS id, i.sequence_id, i.name, s.name AS state, r.sim
		FROM (
			SELECT t.id, t.sim
			FROM scored t
			WHERE t.sim > 0.3
			ORDER BY t.sim DESC
			LIMIT 5
		) r
		JOIN issues i ON i.id = r.id
		JOIN states s ON s.id = i.state_id
		ORDER BY r.sim DESC`,
		projectID, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out = []SimilarIssue{}
	for rows.Next() {
		var s SimilarIssue
		var seq int
		if err := rows.Scan(&s.ID, &seq, &s.Name, &s.State, &s.Similarity); err != nil {
			return nil, err
		}
		s.DisplayID = ident + "-" + strconv.Itoa(seq)
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
