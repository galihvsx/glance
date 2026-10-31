package service

// Pre-existing blocks-cycle report (C94T1): read-only detection of
// dependency cycles already stored for a project.
//
// This file intentionally does NOT touch CreateIssueLink or any other
// function in issue_link.go: the C94T0 guard stops new cycles, this
// report only surfaces cycles created before the guard (or otherwise
// present in stored data). It never deletes or modifies links —
// deleting an offending link stays a manual user action via the
// existing link-delete endpoint.
//
// Scope mirrors the C94T0 guard and the open-blocker guard: only
// 'blocks' edges count as ordering edges (relates_to / duplicates
// rings are legitimate vocabulary and are never reported), and only
// live, unarchived issues in the same project count as path nodes —
// an archived or deleted intermediary breaks the path, so a ring
// through such a node is not reported. The endpoint is kept out of
// the Gantt path: ListIssueLinksForIssues is untouched.

import (
	"context"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ListIssueLinkCycles returns every blocks-cycle currently stored for
// the project, each as an ordered list of display IDs
// ({IDENTIFIER}-{sequence}) in edge direction, closed by repeating the
// first display ID at the end (e.g. ["ENG-1", "ENG-2", "ENG-3",
// "ENG-1"]). The same ring is reported exactly once regardless of
// start point: enumeration anchors each cycle at its minimum-sequence
// node, so rotations of one ring collapse to a single entry. A clean
// project returns an empty (non-nil) slice, so the JSON envelope
// renders "cycles": [] rather than null. Any member (guest 5+) may
// read; a non-member surfaces ErrNotFound (404 tenancy, same as other
// satellite reads).
func ListIssueLinkCycles(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string) ([][]string, error) {
	projectID, _, err := resolveModuleProject(ctx, pool, wsSlug, identifier, actorID, false)
	if err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx,
		`SELECT p.identifier, src.id::text, src.sequence_id,
		        tgt.id::text, tgt.sequence_id
		 FROM issue_links l
		 JOIN issues src ON src.id = l.issue_id
		 JOIN issues tgt ON tgt.id = l.target_issue_id
		 JOIN projects p ON p.id = src.project_id
		 WHERE l.kind = 'blocks'
		   AND src.project_id = $1::uuid
		   AND tgt.project_id = $1::uuid
		   AND src.deleted_at IS NULL AND src.archived_at IS NULL
		   AND tgt.deleted_at IS NULL AND tgt.archived_at IS NULL`,
		projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// One query + in-memory walk: project graphs are small, so no
	// recursive CTE is needed.
	display := map[string]string{}
	seq := map[string]int{}
	adj := map[string][]string{}
	for rows.Next() {
		var ident, srcID, tgtID string
		var srcSeq, tgtSeq int
		if err := rows.Scan(&ident, &srcID, &srcSeq, &tgtID, &tgtSeq); err != nil {
			return nil, err
		}
		display[srcID] = fmt.Sprintf("%s-%d", ident, srcSeq)
		display[tgtID] = fmt.Sprintf("%s-%d", ident, tgtSeq)
		seq[srcID] = srcSeq
		seq[tgtID] = tgtSeq
		adj[srcID] = append(adj[srcID], tgtID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Nodes in ascending sequence order; neighbours likewise, so the
	// walk (and therefore the output order) is deterministic.
	nodes := make([]string, 0, len(display))
	for id := range display {
		nodes = append(nodes, id)
	}
	sort.Slice(nodes, func(i, j int) bool { return seq[nodes[i]] < seq[nodes[j]] })
	for src := range adj {
		sort.Slice(adj[src], func(i, j int) bool { return seq[adj[src][i]] < seq[adj[src][j]] })
	}

	// Enumerate elementary cycles, each anchored at its minimum-sequence
	// node: from anchor s, the DFS only visits nodes with sequence >=
	// s and records a cycle whenever an edge returns to s. Every
	// elementary cycle has exactly one minimum node and one directed
	// traversal from it, so each ring is found exactly once — no
	// post-hoc dedup pass is needed. A self-edge on s records [s, s].
	cycles := [][]string{}
	cycleSeqs := [][]int{}
	for _, anchor := range nodes {
		anchorSeq := seq[anchor]
		onPath := map[string]bool{anchor: true}
		var dfs func(cur string, path []string, pathSeq []int)
		dfs = func(cur string, path []string, pathSeq []int) {
			for _, nb := range adj[cur] {
				if nb == anchor {
					closed := append(append([]string{}, path...), display[anchor])
					closedSeq := append(append([]int{}, pathSeq...), anchorSeq)
					cycles = append(cycles, closed)
					cycleSeqs = append(cycleSeqs, closedSeq)
					continue
				}
				if onPath[nb] || seq[nb] < anchorSeq {
					continue
				}
				onPath[nb] = true
				dfs(nb, append(path, display[nb]), append(pathSeq, seq[nb]))
				delete(onPath, nb)
			}
		}
		dfs(anchor, []string{display[anchor]}, []int{anchorSeq})
	}

	// Sort by anchor sequence, then by the full sequence path, so
	// output order is stable regardless of map iteration.
	order := make([]int, len(cycles))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(i, j int) bool {
		a, b := cycleSeqs[order[i]], cycleSeqs[order[j]]
		for k := 0; k < len(a) && k < len(b); k++ {
			if a[k] != b[k] {
				return a[k] < b[k]
			}
		}
		return len(a) < len(b)
	})
	out := make([][]string, 0, len(cycles))
	for _, i := range order {
		out = append(out, cycles[i])
	}
	return out, nil
}
