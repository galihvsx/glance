package service

// Comment resolve threads (C32T0) service tests: resolve/reopen
// round-trip + activity entries, idempotency (no duplicate activity,
// timestamps unchanged), reply rejection (400), the resolved list
// filter (roots filtered, subtrees intact), tenancy, and the
// member-15+ gate. All tests run against the real test database —
// no skips.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func testResolveFixture(t *testing.T, pool *pgxpool.Pool, tag string) (wsSlug, ident, issueID, actor string) {
	t.Helper()
	return testSatelliteFixture(t, pool, tag)
}

func testResolveComment(t *testing.T, pool *pgxpool.Pool, wsSlug, ident, issueID, actor string, parentID *string) *Comment {
	t.Helper()
	c, err := CreateComment(context.Background(), pool, wsSlug, ident, issueID, actor,
		json.RawMessage(`{"type":"doc","content":[]}`), parentID)
	if err != nil {
		t.Fatalf("CreateComment: %v", err)
	}
	return c
}

func countCommentActivities(t *testing.T, pool *pgxpool.Pool, issueID, field string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM issue_activities WHERE issue_id = $1::uuid AND field = $2`,
		issueID, field).Scan(&n); err != nil {
		t.Fatalf("count activities %s: %v", field, err)
	}
	return n
}

func TestCommentResolveRoundTrip(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	wsSlug, ident, issueID, actor := testResolveFixture(t, pool, "resolve-rt")

	// A second member (15+, NOT the author) resolves: any member may
	// resolve any thread.
	resolver := createTestUser(t, pool, uniqueTestEmail("sat-resolve-rt-other"))
	addTestMember(t, pool, wsSlug, actor, resolver, RoleMember)

	top := testResolveComment(t, pool, wsSlug, ident, issueID, actor, nil)
	if top.ResolvedAt != nil || top.ResolvedBy != nil {
		t.Fatalf("new comment resolved_at = %v resolved_by = %v, want nil/nil", top.ResolvedAt, top.ResolvedBy)
	}

	resolved, err := ResolveComment(ctx, pool, wsSlug, ident, issueID, top.ID, resolver)
	if err != nil {
		t.Fatalf("ResolveComment: %v", err)
	}
	if resolved.ResolvedAt == nil {
		t.Fatal("resolved_at is nil after resolve")
	}
	if resolved.ResolvedBy == nil || resolved.ResolvedBy.ID != resolver {
		t.Fatalf("resolved_by = %+v, want resolver %s", resolved.ResolvedBy, resolver)
	}
	if countCommentActivities(t, pool, issueID, "_comment_resolved") != 1 {
		t.Fatalf("expected exactly 1 _comment_resolved activity entry")
	}
	var newVal string
	if err := pool.QueryRow(ctx,
		`SELECT new_value::text FROM issue_activities WHERE issue_id = $1::uuid AND field = '_comment_resolved'`,
		issueID).Scan(&newVal); err != nil {
		t.Fatalf("activity new_value: %v", err)
	}
	if !strings.Contains(newVal, top.ID) {
		t.Fatalf("activity new_value = %s, want it to carry comment_id %s", newVal, top.ID)
	}

	// The list surfaces the resolution.
	tree, err := ListComments(ctx, pool, wsSlug, ident, issueID, actor)
	if err != nil {
		t.Fatalf("ListComments: %v", err)
	}
	if len(tree) != 1 || tree[0].ResolvedAt == nil || tree[0].ResolvedBy == nil || tree[0].ResolvedBy.ID != resolver {
		t.Fatalf("listed root = %+v, want resolved by %s", tree[0], resolver)
	}

	// Reopen clears both columns and writes the reopen entry.
	reopened, err := ReopenComment(ctx, pool, wsSlug, ident, issueID, top.ID, resolver)
	if err != nil {
		t.Fatalf("ReopenComment: %v", err)
	}
	if reopened.ResolvedAt != nil || reopened.ResolvedBy != nil {
		t.Fatalf("reopened resolved_at = %v resolved_by = %v, want nil/nil", reopened.ResolvedAt, reopened.ResolvedBy)
	}
	if countCommentActivities(t, pool, issueID, "_comment_reopened") != 1 {
		t.Fatalf("expected exactly 1 _comment_reopened activity entry")
	}
}

func TestCommentResolveIdempotent(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	wsSlug, ident, issueID, actor := testResolveFixture(t, pool, "resolve-idem")
	top := testResolveComment(t, pool, wsSlug, ident, issueID, actor, nil)

	first, err := ResolveComment(ctx, pool, wsSlug, ident, issueID, top.ID, actor)
	if err != nil {
		t.Fatalf("first ResolveComment: %v", err)
	}
	second, err := ResolveComment(ctx, pool, wsSlug, ident, issueID, top.ID, actor)
	if err != nil {
		t.Fatalf("second ResolveComment: %v", err)
	}
	if !second.ResolvedAt.Equal(*first.ResolvedAt) {
		t.Fatalf("re-resolve moved resolved_at: %v -> %v", first.ResolvedAt, second.ResolvedAt)
	}
	if second.ResolvedBy == nil || second.ResolvedBy.ID != first.ResolvedBy.ID {
		t.Fatalf("re-resolve changed resolved_by: %+v -> %+v", first.ResolvedBy, second.ResolvedBy)
	}
	if n := countCommentActivities(t, pool, issueID, "_comment_resolved"); n != 1 {
		t.Fatalf("_comment_resolved entries = %d, want 1 (no duplicate on re-resolve)", n)
	}

	// Reopen twice: the second is a no-op with no new entry.
	if _, err := ReopenComment(ctx, pool, wsSlug, ident, issueID, top.ID, actor); err != nil {
		t.Fatalf("first ReopenComment: %v", err)
	}
	if _, err := ReopenComment(ctx, pool, wsSlug, ident, issueID, top.ID, actor); err != nil {
		t.Fatalf("second ReopenComment: %v", err)
	}
	if n := countCommentActivities(t, pool, issueID, "_comment_reopened"); n != 1 {
		t.Fatalf("_comment_reopened entries = %d, want 1 (no duplicate on re-reopen)", n)
	}
}

func TestCommentResolveReplyRejected(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	wsSlug, ident, issueID, actor := testResolveFixture(t, pool, "resolve-reply")
	top := testResolveComment(t, pool, wsSlug, ident, issueID, actor, nil)
	reply := testResolveComment(t, pool, wsSlug, ident, issueID, actor, &top.ID)

	if _, err := ResolveComment(ctx, pool, wsSlug, ident, issueID, reply.ID, actor); !errors.Is(err, ErrInvalidComment) {
		t.Fatalf("resolve reply: err = %v, want ErrInvalidComment", err)
	}
	if _, err := ReopenComment(ctx, pool, wsSlug, ident, issueID, reply.ID, actor); !errors.Is(err, ErrInvalidComment) {
		t.Fatalf("reopen reply: err = %v, want ErrInvalidComment", err)
	}
	if n := countCommentActivities(t, pool, issueID, "_comment_resolved"); n != 0 {
		t.Fatalf("_comment_resolved entries = %d, want 0 after rejected resolve", n)
	}
	// The root itself is still resolvable.
	if _, err := ResolveComment(ctx, pool, wsSlug, ident, issueID, top.ID, actor); err != nil {
		t.Fatalf("resolve root after rejected reply resolve: %v", err)
	}
}

func TestCommentResolveFilter(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	wsSlug, ident, issueID, actor := testResolveFixture(t, pool, "resolve-filter")

	openRoot := testResolveComment(t, pool, wsSlug, ident, issueID, actor, nil)
	resolvedRoot := testResolveComment(t, pool, wsSlug, ident, issueID, actor, nil)
	// A reply under the resolved root: the filter must keep the whole
	// subtree when the root passes.
	reply := testResolveComment(t, pool, wsSlug, ident, issueID, actor, &resolvedRoot.ID)
	if _, err := ResolveComment(ctx, pool, wsSlug, ident, issueID, resolvedRoot.ID, actor); err != nil {
		t.Fatalf("ResolveComment: %v", err)
	}

	// Default (all): both roots, subtree intact.
	tree, err := ListCommentsFiltered(ctx, pool, wsSlug, ident, issueID, actor, "all")
	if err != nil {
		t.Fatalf("filter all: %v", err)
	}
	if len(tree) != 2 {
		t.Fatalf("filter all roots = %d, want 2", len(tree))
	}
	// Empty string means all too.
	if tree, err := ListCommentsFiltered(ctx, pool, wsSlug, ident, issueID, actor, ""); err != nil || len(tree) != 2 {
		t.Fatalf("filter empty: roots = %d err = %v, want 2/nil", len(tree), err)
	}

	// Open: only the unresolved root, no replies attached to it.
	tree, err = ListCommentsFiltered(ctx, pool, wsSlug, ident, issueID, actor, "open")
	if err != nil {
		t.Fatalf("filter open: %v", err)
	}
	if len(tree) != 1 || tree[0].ID != openRoot.ID {
		t.Fatalf("filter open roots = %v, want [%s]", tree, openRoot.ID)
	}
	if len(tree[0].Replies) != 0 {
		t.Fatalf("filter open root has %d replies, want 0", len(tree[0].Replies))
	}

	// Resolved: only the resolved root, with its reply subtree intact.
	tree, err = ListCommentsFiltered(ctx, pool, wsSlug, ident, issueID, actor, "resolved")
	if err != nil {
		t.Fatalf("filter resolved: %v", err)
	}
	if len(tree) != 1 || tree[0].ID != resolvedRoot.ID {
		t.Fatalf("filter resolved roots = %v, want [%s]", tree, resolvedRoot.ID)
	}
	if len(tree[0].Replies) != 1 || tree[0].Replies[0].ID != reply.ID {
		t.Fatalf("filter resolved replies = %v, want [%s]", tree[0].Replies, reply.ID)
	}

	// Invalid value: 400.
	if _, err := ListCommentsFiltered(ctx, pool, wsSlug, ident, issueID, actor, "bogus"); !errors.Is(err, ErrInvalidComment) {
		t.Fatalf("filter bogus: err = %v, want ErrInvalidComment", err)
	}
}

func TestCommentResolveTenancyAndRoles(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	wsSlug, ident, issueID, actor := testResolveFixture(t, pool, "resolve-ten")
	top := testResolveComment(t, pool, wsSlug, ident, issueID, actor, nil)

	// Guest (role 5) may read but not resolve/reopen.
	guest := createTestUser(t, pool, uniqueTestEmail("sat-resolve-guest"))
	addTestMember(t, pool, wsSlug, actor, guest, RoleGuest)
	if _, err := ResolveComment(ctx, pool, wsSlug, ident, issueID, top.ID, guest); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest resolve: err = %v, want ErrForbidden", err)
	}
	if _, err := ReopenComment(ctx, pool, wsSlug, ident, issueID, top.ID, guest); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest reopen: err = %v, want ErrForbidden", err)
	}

	// Non-member: the actor-resolution contract — 404, never a hint
	// the workspace exists (same as every other comment endpoint).
	outsider := createTestUser(t, pool, uniqueTestEmail("sat-resolve-out"))
	if _, err := ResolveComment(ctx, pool, wsSlug, ident, issueID, top.ID, outsider); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-member resolve: err = %v, want ErrNotFound", err)
	}

	// Cross-workspace: a member of BOTH workspaces resolves a ws1
	// comment through ws2's slug/identifier — the issue is not in
	// that project, so 404.
	ws2Slug, ident2, _, ws2Admin := testSatelliteFixture(t, pool, "resolve-ten2")
	dual := createTestUser(t, pool, uniqueTestEmail("sat-resolve-dual"))
	addTestMember(t, pool, wsSlug, actor, dual, RoleMember)
	addTestMember(t, pool, ws2Slug, ws2Admin, dual, RoleMember)
	if _, err := ResolveComment(ctx, pool, ws2Slug, ident2, issueID, top.ID, dual); !errors.Is(err, ErrIssueNotFound) {
		t.Fatalf("cross-workspace resolve: err = %v, want ErrIssueNotFound", err)
	}

	// Unknown comment and deleted comment: 404.
	if _, err := ResolveComment(ctx, pool, wsSlug, ident, issueID, "00000000-0000-0000-0000-000000000000", actor); !errors.Is(err, ErrCommentNotFound) {
		t.Fatalf("unknown comment resolve: err = %v, want ErrCommentNotFound", err)
	}
	if err := DeleteComment(ctx, pool, wsSlug, ident, issueID, top.ID, actor); err != nil {
		t.Fatalf("DeleteComment: %v", err)
	}
	if _, err := ResolveComment(ctx, pool, wsSlug, ident, issueID, top.ID, actor); !errors.Is(err, ErrCommentNotFound) {
		t.Fatalf("deleted comment resolve: err = %v, want ErrCommentNotFound", err)
	}
}
