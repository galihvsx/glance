package service

// Batched direct-subtask rows (C122T0): ?include_subtasks=1 on the
// issue list attaches, per returned row, the row's DIRECT children
// only — one level, no recursion — in exactly TWO batched queries
// for the whole page (never per-parent):
//   1. one query for the direct children of all returned parents
//      (parent_id IN ..., ordered by sort_order ASC),
//   2. one query for those children's assignees (skipped entirely
//      when the page has no children).
// Only children of parents IN THE RETURNED PAGE are attached: a
// child whose parent is not in the page is attached nowhere
// server-side (the frontend orphan rule keeps it a flat card).
//
// Liveness mirrors the subtask_progress rollup (subtaskProgressAgg):
// soft-deleted children are excluded; archived/draft children are
// NOT excluded, because the rollup counts them and the nested row
// shows the child's real state either way. The flag gates the
// field everywhere: ListIssues is the only caller, and every other
// IssueListItem producer leaves Subtasks nil, so omitempty keeps
// their responses byte-identical.
//
// Contract types (cycle 122 plan, fixed so T0/T1 can run in
// parallel): SubtaskRow carries EXACTLY id, display_id, title,
// state_id, priority, assignee_ids, sort_order — no more.
//   - state_id / priority are typed nullable (*string) because the
//     contract says uuid|null / string|null. In practice both
//     columns are NOT NULL (migrations 000007: state_id UUID NOT
//     NULL, priority SMALLINT NOT NULL 0-4), so null never
//     materializes for a real row; the pointer type keeps the
//     contract honest without inventing a null case.
//   - priority is a STRING, not the int the rest of the API uses:
//     the contract fixes string|null, and the issues table stores
//     no string priority, so the int is mapped through the
//     Plane-style lowercase vocabulary below (0 none, 1 low,
//     2 medium, 3 high, 4 urgent). A value outside 0-4 cannot
//     exist (CHECK constraint) and maps to null rather than a
//     fabricated label. T1 / merge check MUST confirm this
//     vocabulary against the frontend's priority-icon mapping
//     before merging T1 — it is the one deliberate mapping
//     choice in this file, and the only place it lives.
//   - Subtasks on IssueListItem is a *[]SubtaskRow, not a plain
//     slice: encoding/json's omitempty drops even a non-nil empty
//     slice, which would break the "always an array, never null"
//     rule for childless issues. A nil pointer omits the field
//     (flag off, byte-identical); a pointer to an empty slice
//     marshals as [] (flag on, childless).

import (
	"context"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"
)

// SubtaskRow is one nested row under a parent list item (C122T0).
// Field order matches the contract; no extra fields.
type SubtaskRow struct {
	ID          string   `json:"id"`
	DisplayID   string   `json:"display_id"`
	Title       string   `json:"title"`
	StateID     *string  `json:"state_id"`
	Priority    *string  `json:"priority"`
	AssigneeIDs []string `json:"assignee_ids"`
	SortOrder   float64  `json:"sort_order"`
}

// subtaskPriorityLabel maps the issues.priority SMALLINT (0-4) to
// the contract's string vocabulary. Out-of-range values map to nil
// (they cannot exist under the CHECK constraint).
func subtaskPriorityLabel(p int) *string {
	var s string
	switch p {
	case 0:
		s = "none"
	case 1:
		s = "low"
	case 2:
		s = "medium"
	case 3:
		s = "high"
	case 4:
		s = "urgent"
	default:
		return nil
	}
	return &s
}

// attachIssueSubtasks fills Subtasks on every item in place: each
// row's DIRECT children (live, same project), ordered by
// sort_order ASC, with batched assignee ids. An empty slice is a
// no-op (no queries). Every returned row ends with a non-nil
// Subtasks pointer — empty, never null — so the JSON shape holds
// for childless issues too.
func attachIssueSubtasks(ctx context.Context, pool *pgxpool.Pool, projectID, ident string, items []IssueListItem) error {
	if len(items) == 0 {
		return nil
	}
	for i := range items {
		empty := []SubtaskRow{}
		items[i].Subtasks = &empty
	}
	parentIDs := make([]string, 0, len(items))
	indexByID := make(map[string]int, len(items))
	for i := range items {
		parentIDs = append(parentIDs, items[i].ID)
		indexByID[items[i].ID] = i
	}

	// Query 1: direct children of the whole page, sort_order ASC.
	// The per-parent subsequence of this global order stays ASC,
	// so grouping below preserves the contract order with no
	// re-sort. sequence_id feeds the display id, exactly as the
	// list rows build theirs (ident + "-" + sequence_id).
	rows, err := pool.Query(ctx,
		`SELECT c.id::text, c.parent_id::text, c.sequence_id, c.name,
			c.state_id::text, c.priority, c.sort_order
		 FROM issues c
		 WHERE c.parent_id = ANY($1::uuid[])
		   AND c.project_id = $2::uuid
		   AND c.deleted_at IS NULL
		 ORDER BY c.sort_order ASC`,
		parentIDs, projectID)
	if err != nil {
		return err
	}
	type childRow struct {
		parentID string
		row      SubtaskRow
	}
	var children []childRow
	for rows.Next() {
		var cr childRow
		var seq int
		var priority int
		if err := rows.Scan(&cr.row.ID, &cr.parentID, &seq, &cr.row.Title,
			&cr.row.StateID, &priority, &cr.row.SortOrder); err != nil {
			rows.Close()
			return err
		}
		cr.row.DisplayID = ident + "-" + strconv.Itoa(seq)
		cr.row.Priority = subtaskPriorityLabel(priority)
		cr.row.AssigneeIDs = []string{}
		children = append(children, cr)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(children) == 0 {
		return nil
	}

	// Query 2 (only when children exist): their assignees, name-
	// ordered like the list's assigneesAgg, so assignee_ids order
	// matches the parent rows' assignee order convention.
	childIDs := make([]string, 0, len(children))
	byChildID := make(map[string]*SubtaskRow, len(children))
	for i := range children {
		childIDs = append(childIDs, children[i].row.ID)
		byChildID[children[i].row.ID] = &children[i].row
	}
	arows, err := pool.Query(ctx,
		`SELECT ia.issue_id::text, ia.user_id::text
		 FROM issue_assignees ia
		 JOIN users u ON u.id = ia.user_id
		 WHERE ia.issue_id = ANY($1::uuid[])
		 ORDER BY ia.issue_id, COALESCE(u.name, u.email::text)`,
		childIDs)
	if err != nil {
		return err
	}
	for arows.Next() {
		var childID, userID string
		if err := arows.Scan(&childID, &userID); err != nil {
			arows.Close()
			return err
		}
		if r, ok := byChildID[childID]; ok {
			r.AssigneeIDs = append(r.AssigneeIDs, userID)
		}
	}
	arows.Close()
	if err := arows.Err(); err != nil {
		return err
	}

	// Attach in page order: each parent's rows keep the query's
	// sort_order ASC order (append order = query order).
	for i := range children {
		parentIdx, ok := indexByID[children[i].parentID]
		if !ok {
			// Defensive: the parent_id IN filter names only page
			// parents, so this cannot happen; skipping (not
			// failing) keeps a stray row from breaking the page.
			continue
		}
		*items[parentIdx].Subtasks = append(*items[parentIdx].Subtasks, children[i].row)
	}
	return nil
}
