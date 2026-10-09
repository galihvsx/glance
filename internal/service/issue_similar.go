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

	// One query, no N+1. Three genuine cost controls:
	//   1. name % $2 is the GIN-indexed trigram predicate (migration 000032,
	//      gin_trgm_ops) — narrows candidates before any ranking math.
	//   2. similarity() is computed ONCE per row in the innermost subquery;
	//      the threshold filter and the ORDER BY reuse that value instead of
	//      re-extracting trigrams per row.
	//   3. The states join happens AFTER the ORDER BY ... LIMIT 5, so it
	//      touches 5 rows, not the whole candidate set.
	// The explicit similarity() > 0.3 filter pins the semantics to the
	// threshold even if pg_trgm.similarity_threshold GUC moves — % is
	// defined as similarity() > similarity_threshold, so the two coincide
	// under the default GUC and the explicit one wins when it is not.
	rows, err := pool.Query(ctx, `
		SELECT u.id, u.sequence_id, u.name, s.name AS state, u.sim
		FROM (
			SELECT t.id, t.sequence_id, t.name, t.state_id, t.sim
			FROM (
				SELECT i.id::text AS id, i.sequence_id, i.name, i.state_id,
				       similarity(i.name, $2) AS sim
				FROM issues i
				WHERE i.project_id = $1::uuid
				  AND i.archived_at IS NULL
				  AND NOT i.is_draft
				  AND i.deleted_at IS NULL
				  AND i.name % $2
			) t
			WHERE t.sim > 0.3
			ORDER BY t.sim DESC
			LIMIT 5
		) u
		JOIN states s ON s.id = u.state_id`,
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
