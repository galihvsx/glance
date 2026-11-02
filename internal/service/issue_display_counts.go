package service

// Batched per-row display counts (C121T0): attachment_count,
// link_count, and relation_count on every list row. ONE query per
// count source for the whole page — never a per-row query —
// following the C21T2 project-structure batch shape.
//
// Counting semantics mirror the detail endpoints' lists, so a card
// count always equals the number of rows the detail page shows:
//   - attachments: every attachments row for the issue (hard-deleted
//     rows are gone, so no liveness filter is needed).
//   - links: every issue_links edge touching the issue from EITHER
//     side (ListIssueLinks shows outgoing + incoming), counted only
//     when the OTHER endpoint is live (deleted_at IS NULL) and in
//     the same project as the listed issue. A self-edge, if one ever
//     exists, counts once, not once per side.
//   - relations: same either-side shape over issue_relations
//     (ListRelations shows outgoing + incoming), same liveness and
//     same-project rule for the other endpoint, same self-edge guard.
// Rows are counted, not distinct pairs: two relation rows between
// the same pair with different types count as two, exactly as the
// detail list renders two rows.
//
// The queries take no project parameter: the same-project rule is
// expressed as a join against the listed issue's own project, so the
// helper serves single-project lists, the trash list, detail rows,
// scope lists, and cross-project search alike.

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// issueDisplayCounts is one row's three counts. Zero-valued when the
// issue has no rows in a source — the caller assigns the struct to
// every returned row, so a missing map entry still means zero.
type issueDisplayCounts struct {
	Attachment int
	Link       int
	Relation   int
}

// listIssueDisplayCounts resolves the three counts for every id in
// ONE query per source. An empty id set returns an empty map with no
// query at all.
func listIssueDisplayCounts(ctx context.Context, pool *pgxpool.Pool, ids []string) (map[string]issueDisplayCounts, error) {
	out := make(map[string]issueDisplayCounts, len(ids))
	if len(ids) == 0 {
		return out, nil
	}

	// Attachments: plain per-issue grouping, no liveness join.
	rows, err := pool.Query(ctx,
		`SELECT a.issue_id::text, COUNT(*)
		 FROM attachments a
		 WHERE a.issue_id = ANY($1::uuid[])
		 GROUP BY a.issue_id`, ids)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		var n int64
		if err := rows.Scan(&id, &n); err != nil {
			rows.Close()
			return nil, err
		}
		c := out[id]
		c.Attachment = int(n)
		out[id] = c
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Links and relations share one shape: UNION ALL over the two
	// sides, so an edge with both endpoints in the page counts once
	// for EACH endpoint (one branch per side), and a self-edge counts
	// once (the second branch excludes it). The other endpoint must
	// be live and in the listed issue's own project.
	edgeQueries := []struct {
		table  string
		from   string
		to     string
		assign func(*issueDisplayCounts, int)
	}{
		{"issue_links", "issue_id", "target_issue_id", func(c *issueDisplayCounts, n int) { c.Link = n }},
		{"issue_relations", "issue_id", "related_issue_id", func(c *issueDisplayCounts, n int) { c.Relation = n }},
	}
	for _, q := range edgeQueries {
		sql := fmt.Sprintf(`SELECT x.id, COUNT(*) FROM (
				SELECT l.%[2]s::text AS id
				FROM %[1]s l
				JOIN issues me ON me.id = l.%[2]s
				JOIN issues o ON o.id = l.%[3]s
				WHERE l.%[2]s = ANY($1::uuid[])
				  AND o.deleted_at IS NULL AND o.project_id = me.project_id
				UNION ALL
				SELECT l.%[3]s::text AS id
				FROM %[1]s l
				JOIN issues me ON me.id = l.%[3]s
				JOIN issues o ON o.id = l.%[2]s
				WHERE l.%[3]s = ANY($1::uuid[])
				  AND l.%[2]s <> l.%[3]s
				  AND o.deleted_at IS NULL AND o.project_id = me.project_id
			) x GROUP BY x.id`, q.table, q.from, q.to)
		rows, err := pool.Query(ctx, sql, ids)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			var n int64
			if err := rows.Scan(&id, &n); err != nil {
				rows.Close()
				return nil, err
			}
			c := out[id]
			q.assign(&c, int(n))
			out[id] = c
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// attachIssueDisplayCounts fills AttachmentCount, LinkCount, and
// RelationCount on every item in place. Rows absent from the count
// map keep the zero value — zero always means "none", never "not
// computed", because every caller passes exactly the rows it
// returns. An empty slice is a no-op (no queries).
func attachIssueDisplayCounts(ctx context.Context, pool *pgxpool.Pool, items []IssueListItem) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]string, 0, len(items))
	for i := range items {
		ids = append(ids, items[i].ID)
	}
	counts, err := listIssueDisplayCounts(ctx, pool, ids)
	if err != nil {
		return err
	}
	for i := range items {
		c := counts[items[i].ID]
		items[i].AttachmentCount = c.Attachment
		items[i].LinkCount = c.Link
		items[i].RelationCount = c.Relation
	}
	return nil
}
