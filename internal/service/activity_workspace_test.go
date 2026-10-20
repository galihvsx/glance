package service

// Workspace activity feed (C17T4) service tests: workspace-wide
// aggregation of the issue_activities audit rows with cursor pagination
// and type filtering. Visibility: member (15)+ sees every project in the
// workspace; guests are forbidden; non-members get 404; rows never leak
// across workspaces. Real test database, no skips.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// setupWorkspaceActivity seeds a workspace with two projects, each with
// one issue carrying pinned audit rows, and returns a second workspace
// (with its own project + issue) that shares nothing with the first.
// Timelines, oldest first:
//
//	ws1/P1.issueA: _created (T-3h), priority (T-2h), state_id (T-1h)
//	ws1/P2.issueB: _created (T-30m)
//	ws2/Q1.issueC: _created (T)
func setupWorkspaceActivity(t *testing.T, pool *pgxpool.Pool) (ws1, p1, p2, actor string, a, b *Issue, ws2, q1 string, c *Issue) {
	t.Helper()
	ctx := context.Background()
	actor = createTestUser(t, pool, uniqueTestEmail("wsactivity"))
	if _, err := pool.Exec(ctx, `UPDATE users SET name = 'Ada Member' WHERE id = $1::uuid`, actor); err != nil {
		t.Fatalf("name actor: %v", err)
	}
	ws1 = uniqueTestSlug("wsact-1")
	createTestWorkspace(t, pool, "WS One", ws1, actor)
	ident1 := uniqueTestIdentifier()
	ident2 := uniqueTestIdentifier()
	proj1 := createTestProject(t, pool, ws1, actor, "Eng", ident1)
	p1 = proj1.ID
	proj2 := createTestProject(t, pool, ws1, actor, "Design", ident2)
	p2 = proj2.ID

	a = createTestIssue(t, pool, ws1, ident1, actor, "First issue")
	b = createTestIssue(t, pool, ws1, ident2, actor, "Second issue")

	doneState := stateIDByGroup(t, pool, proj1.ID, "completed")
	pri := 2
	if _, err := UpdateIssue(ctx, pool, ws1, ident1, a.ID, actor, IssuePatch{Priority: &pri}); err != nil {
		t.Fatalf("bump priority: %v", err)
	}
	if _, err := UpdateIssue(ctx, pool, ws1, ident1, a.ID, actor, IssuePatch{StateID: &doneState}); err != nil {
		t.Fatalf("complete A: %v", err)
	}

	// A second workspace with its own project/issue — the caller's
	// feed on ws1 must never surface these rows.
	other := createTestUser(t, pool, uniqueTestEmail("wsactivity-other"))
	ws2 = uniqueTestSlug("wsact-2")
	createTestWorkspace(t, pool, "WS Two", ws2, other)
	ident3 := uniqueTestIdentifier()
	createTestProject(t, pool, ws2, other, "Eng", ident3)
	q1 = ident3
	c = createTestIssue(t, pool, ws2, ident3, other, "Other workspace issue")

	now := time.Now().UTC().Truncate(time.Second)
	pin := func(issueID, field string, at time.Time) {
		if _, err := pool.Exec(ctx,
			`UPDATE issue_activities SET created_at = $1 WHERE issue_id = $2::uuid AND field = $3`,
			at, issueID, field); err != nil {
			t.Fatalf("pin %s/%s: %v", issueID, field, err)
		}
	}
	pin(a.ID, "_created", now.Add(-3*time.Hour))
	pin(a.ID, "priority", now.Add(-2*time.Hour))
	pin(a.ID, "state_id", now.Add(-1*time.Hour))
	pin(b.ID, "_created", now.Add(-30*time.Minute))
	pin(c.ID, "_created", now)
	return ws1, p1, p2, actor, a, b, ws2, q1, c
}

// TestGetWorkspaceActivityAggregates checks the workspace feed returns
// every project's rows newest-first with project badge fields.
func TestGetWorkspaceActivityAggregates(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	ws1, p1, p2, actor, a, b, _, _, _ := setupWorkspaceActivity(t, pool)

	page, err := GetWorkspaceActivity(ctx, pool, ws1, actor, ActivityDefaultLimit, "", nil, "")
	if err != nil {
		t.Fatalf("GetWorkspaceActivity: %v", err)
	}
	if len(page.Entries) != 4 {
		t.Fatalf("entries = %d, want 4 (ws1 rows only)", len(page.Entries))
	}
	if page.NextCursor != "" {
		t.Fatalf("NextCursor = %q, want empty (feed exhausted)", page.NextCursor)
	}
	// Newest first: B._created, A.state_id, A.priority, A._created.
	want := []struct {
		field, issue, proj string
	}{
		{"_created", b.ID, p2},
		{"state_id", a.ID, p1},
		{"priority", a.ID, p1},
		{"_created", a.ID, p1},
	}
	for i, w := range want {
		e := page.Entries[i]
		if e.Field != w.field || e.IssueUUID != w.issue {
			t.Fatalf("entries[%d] = (%q,%q), want (%q,%q)", i, e.Field, e.IssueUUID, w.field, w.issue)
		}
		if e.ProjectIdentifier == "" || e.ProjectName == "" {
			t.Fatalf("entries[%d] missing project badge fields: %+v", i, e)
		}
		if e.Actor != "Ada Member" {
			t.Fatalf("entries[%d].actor = %q, want display name", i, e.Actor)
		}
	}
	// Strictly descending timestamps.
	for i := 1; i < len(page.Entries); i++ {
		if !page.Entries[i].At.Before(page.Entries[i-1].At) {
			t.Fatalf("entries not newest-first at %d", i)
		}
	}
	// Project badge carries the owning project, not a neighbor.
	if got := page.Entries[0].ProjectName; got != "Design" {
		t.Fatalf("B row project_name = %q, want Design", got)
	}
	if got := page.Entries[1].ProjectName; got != "Eng" {
		t.Fatalf("A row project_name = %q, want Eng", got)
	}
}

func TestGetWorkspaceActivityVisibility(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	ws1, _, _, actor, _, _, ws2, _, _ := setupWorkspaceActivity(t, pool)

	// The caller is not a member of ws2: 404, and ws2's rows never leak
	// into the ws1 feed (asserted by the aggregate test's count of 4).
	if _, err := GetWorkspaceActivity(ctx, pool, ws2, actor, ActivityDefaultLimit, "", nil, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other workspace: err = %v, want ErrNotFound", err)
	}

	// A guest (role 5) of ws1 is forbidden.
	var wsID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM workspaces WHERE slug = $1`, ws1).Scan(&wsID); err != nil {
		t.Fatalf("workspace id: %v", err)
	}
	guest := createTestUser(t, pool, uniqueTestEmail("wsactivity-guest"))
	if _, err := pool.Exec(ctx,
		`INSERT INTO workspace_members (workspace_id, user_id, role) VALUES ($1::uuid, $2::uuid, 5)`,
		wsID, guest); err != nil {
		t.Fatalf("add guest: %v", err)
	}
	if _, err := GetWorkspaceActivity(ctx, pool, ws1, guest, ActivityDefaultLimit, "", nil, ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest: err = %v, want ErrForbidden", err)
	}

	// A plain non-member gets 404.
	stranger := createTestUser(t, pool, uniqueTestEmail("wsactivity-stranger"))
	if _, err := GetWorkspaceActivity(ctx, pool, ws1, stranger, ActivityDefaultLimit, "", nil, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stranger: err = %v, want ErrNotFound", err)
	}

	// Bad slug is 404 too.
	if _, err := GetWorkspaceActivity(ctx, pool, "no-such-ws", actor, ActivityDefaultLimit, "", nil, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bad slug: err = %v, want ErrNotFound", err)
	}

	// Bad limit and bad cursor are 400s.
	for _, bad := range []int{0, -1, ActivityMaxLimit + 1} {
		if _, err := GetWorkspaceActivity(ctx, pool, ws1, actor, bad, "", nil, ""); !errors.Is(err, ErrInvalidActivityLimit) {
			t.Fatalf("limit %d: err = %v, want ErrInvalidActivityLimit", bad, err)
		}
	}
	for _, bad := range []string{"not-a-cursor", "!!!!", encodeActivityCursor(time.Now(), "not-a-uuid")} {
		if _, err := GetWorkspaceActivity(ctx, pool, ws1, actor, ActivityDefaultLimit, bad, nil, ""); !errors.Is(err, ErrInvalidActivityCursor) {
			t.Fatalf("cursor %q: err = %v, want ErrInvalidActivityCursor", bad, err)
		}
	}
}

func TestGetWorkspaceActivityPagination(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	ws1, _, _, actor, _, _, _, _, _ := setupWorkspaceActivity(t, pool)

	// Full feed in one page for the reference order.
	full, err := GetWorkspaceActivity(ctx, pool, ws1, actor, ActivityDefaultLimit, "", nil, "")
	if err != nil {
		t.Fatalf("full feed: %v", err)
	}
	if len(full.Entries) != 4 {
		t.Fatalf("full feed = %d rows, want 4", len(full.Entries))
	}

	// Walk pages of 2: continuity, no overlap, no repeats, exhausted end.
	var got []WorkspaceActivityEntry
	cursor := ""
	for i := 0; i < 10; i++ {
		page, err := GetWorkspaceActivity(ctx, pool, ws1, actor, 2, cursor, nil, "")
		if err != nil {
			t.Fatalf("page %d: %v", i, err)
		}
		if len(page.Entries) > 2 {
			t.Fatalf("page %d: %d rows, want ≤2", i, len(page.Entries))
		}
		got = append(got, page.Entries...)
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
	}
	if len(got) != len(full.Entries) {
		t.Fatalf("paged rows = %d, want %d", len(got), len(full.Entries))
	}
	for i := range got {
		if got[i].Field != full.Entries[i].Field || got[i].IssueUUID != full.Entries[i].IssueUUID {
			t.Fatalf("paged row %d = (%q,%q), want (%q,%q)", i,
				got[i].Field, got[i].IssueUUID, full.Entries[i].Field, full.Entries[i].IssueUUID)
		}
	}
	// The second page's cursor must actually decode to the last row of
	// the first page (round-trip sanity on the opaque cursor).
	p1, err := GetWorkspaceActivity(ctx, pool, ws1, actor, 2, "", nil, "")
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	at, id, err := decodeActivityCursor(p1.NextCursor)
	if err != nil {
		t.Fatalf("decode cursor: %v", err)
	}
	if !at.Equal(p1.Entries[1].At) {
		t.Fatalf("cursor time = %v, want %v", at, p1.Entries[1].At)
	}
	var rowID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM issue_activities WHERE issue_id = $1::uuid AND field = $2`,
		p1.Entries[1].IssueUUID, p1.Entries[1].Field).Scan(&rowID); err != nil {
		t.Fatalf("row id lookup: %v", err)
	}
	if id != rowID {
		t.Fatalf("cursor id = %q, want activity row %q", id, rowID)
	}
}

func TestGetWorkspaceActivityTypeFilter(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	ws1, _, _, actor, a, b, _, _, _ := setupWorkspaceActivity(t, pool)

	page, err := GetWorkspaceActivity(ctx, pool, ws1, actor, ActivityDefaultLimit, "", []string{"state_id", "priority"}, "")
	if err != nil {
		t.Fatalf("type filter: %v", err)
	}
	if len(page.Entries) != 2 {
		t.Fatalf("filtered rows = %d, want 2", len(page.Entries))
	}
	if page.Entries[0].Field != "state_id" || page.Entries[1].Field != "priority" {
		t.Fatalf("filtered order wrong: %q, %q", page.Entries[0].Field, page.Entries[1].Field)
	}

	// Blank entries, whitespace, and duplicates are cleaned before
	// filtering.
	page, err = GetWorkspaceActivity(ctx, pool, ws1, actor, ActivityDefaultLimit, "", []string{" state_id ", "", "state_id"}, "")
	if err != nil {
		t.Fatalf("dirty filter: %v", err)
	}
	if len(page.Entries) != 1 || page.Entries[0].Field != "state_id" {
		t.Fatalf("dirty filter rows = %d, want 1 state_id row", len(page.Entries))
	}

	// Unknown types match nothing — empty, exhausted page.
	page, err = GetWorkspaceActivity(ctx, pool, ws1, actor, ActivityDefaultLimit, "", []string{"no_such_field"}, "")
	if err != nil {
		t.Fatalf("unknown type: %v", err)
	}
	if len(page.Entries) != 0 || page.NextCursor != "" {
		t.Fatalf("unknown type → %d rows cursor %q, want empty+exhausted", len(page.Entries), page.NextCursor)
	}

	// Filter composes with pagination: only _created rows, one per page.
	var fields []string
	cursor := ""
	for i := 0; i < 10; i++ {
		p, err := GetWorkspaceActivity(ctx, pool, ws1, actor, 1, cursor, []string{"_created"}, "")
		if err != nil {
			t.Fatalf("filtered page %d: %v", i, err)
		}
		for _, e := range p.Entries {
			fields = append(fields, e.Field)
		}
		cursor = p.NextCursor
		if cursor == "" {
			break
		}
	}
	if len(fields) != 2 || fields[0] != "_created" || fields[1] != "_created" {
		t.Fatalf("filtered pagination = %v, want [_created _created]", fields)
	}

	// Deleted issues drop out of the feed (scope parity with the
	// project feed). Deleting A removes its 3 rows; B's row remains.
	if err := DeleteIssue(ctx, pool, ws1, issueIdent(t, pool, a.ID), a.ID, actor); err != nil {
		t.Fatalf("delete issue: %v", err)
	}
	page, err = GetWorkspaceActivity(ctx, pool, ws1, actor, ActivityDefaultLimit, "", nil, "")
	if err != nil {
		t.Fatalf("after delete: %v", err)
	}
	if len(page.Entries) != 1 || page.Entries[0].IssueUUID != b.ID {
		t.Fatalf("after delete: %d rows, want B's single row", len(page.Entries))
	}
}

// issueIdent recovers a project's identifier for an issue id.
func issueIdent(t *testing.T, pool *pgxpool.Pool, issueID string) string {
	t.Helper()
	var ident string
	if err := pool.QueryRow(context.Background(),
		`SELECT p.identifier FROM issues i JOIN projects p ON p.id = i.project_id WHERE i.id = $1::uuid`,
		issueID).Scan(&ident); err != nil {
		t.Fatalf("issueIdent: %v", err)
	}
	return ident
}
