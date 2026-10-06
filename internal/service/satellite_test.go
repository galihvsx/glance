package service

// Issue satellites tests (Task 18): threaded comments, relation reverse
// derivation, versions + restore-creates-new-version, reaction/vote/
// subscriber toggles, history. All tests run against the real test
// database — no skips. Reuses the harness from workspace_test.go and the
// issue helpers from issue_test.go.

import (
	"context"
	"encoding/json"
	"strconv"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func testSatelliteFixture(t *testing.T, pool *pgxpool.Pool, tag string) (wsSlug, identifier, issueID, actorID string) {
	t.Helper()
	creator := createTestUser(t, pool, uniqueTestEmail("sat-"+tag))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-sat-"+tag), creator)
	p := createTestProject(t, pool, ws.Slug, creator, "Engineering", uniqueTestIdentifier())
	iss := createTestIssue(t, pool, ws.Slug, p.Identifier, creator, "Satellite host")
	return ws.Slug, p.Identifier, iss.ID, creator
}

func TestCommentThreading(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	wsSlug, ident, issueID, actor := testSatelliteFixture(t, pool, "thread")

	top, err := CreateComment(ctx, pool, wsSlug, ident, issueID, actor,
		json.RawMessage(`{"type":"doc","content":[]}`), nil)
	if err != nil {
		t.Fatalf("CreateComment: %v", err)
	}
	reply, err := CreateComment(ctx, pool, wsSlug, ident, issueID, actor,
		json.RawMessage(`{"type":"doc","content":[]}`), &top.ID)
	if err != nil {
		t.Fatalf("CreateComment reply: %v", err)
	}
	if reply.ParentID == nil || *reply.ParentID != top.ID {
		t.Fatalf("reply parent = %v, want %s", reply.ParentID, top.ID)
	}

	// List nests the reply under its parent.
	tree, err := ListComments(ctx, pool, wsSlug, ident, issueID, actor)
	if err != nil {
		t.Fatalf("ListComments: %v", err)
	}
	if len(tree) != 1 || tree[0].ID != top.ID {
		t.Fatalf("top-level comments = %d, want 1 (the parent)", len(tree))
	}
	if len(tree[0].Replies) != 1 || tree[0].Replies[0].ID != reply.ID {
		t.Fatalf("replies = %d, want 1 nested under the parent", len(tree[0].Replies))
	}

	// A parent from another issue is rejected.
	ws2, otherIdent, otherIssueID, actor2 := testSatelliteFixture(t, pool, "thread2")
	otherTop, err := CreateComment(ctx, pool, ws2, otherIdent, otherIssueID, actor2,
		json.RawMessage(`{"type":"doc"}`), nil)
	if err != nil {
		t.Fatalf("other issue comment: %v", err)
	}
	if _, err := CreateComment(ctx, pool, wsSlug, ident, issueID, actor,
		json.RawMessage(`{"type":"doc"}`), &otherTop.ID); err == nil {
		t.Fatal("cross-issue parent accepted, want ErrInvalidComment")
	}

	// A nonexistent parent is rejected.
	bogus := "00000000-0000-0000-0000-000000000000"
	if _, err := CreateComment(ctx, pool, wsSlug, ident, issueID, actor,
		json.RawMessage(`{"type":"doc"}`), &bogus); err == nil {
		t.Fatal("bogus parent accepted, want an error")
	}
}

func TestCommentEditDelete(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	wsSlug, ident, issueID, actor := testSatelliteFixture(t, pool, "cedit")
	other := createTestUser(t, pool, uniqueTestEmail("sat-cedit-other"))

	c, err := CreateComment(ctx, pool, wsSlug, ident, issueID, actor,
		json.RawMessage(`{"type":"doc"}`), nil)
	if err != nil {
		t.Fatalf("CreateComment: %v", err)
	}
	// Another member cannot edit someone else's comment.
	if _, err := UpdateComment(ctx, pool, wsSlug, ident, issueID, c.ID, other,
		json.RawMessage(`{"type":"doc","edited":true}`)); err == nil {
		t.Fatal("non-author edit accepted, want ErrForbidden")
	}
	updated, err := UpdateComment(ctx, pool, wsSlug, ident, issueID, c.ID, actor,
		json.RawMessage(`{"type":"doc","edited":true}`))
	if err != nil {
		t.Fatalf("author edit: %v", err)
	}
	// Compare semantically: JSONB normalizes key order on the round trip.
	var gotDoc map[string]any
	if err := json.Unmarshal(updated.Content, &gotDoc); err != nil {
		t.Fatalf("decode updated content: %v", err)
	}
	if gotDoc["type"] != "doc" || gotDoc["edited"] != true {
		t.Fatalf("content = %s, want the edited doc", updated.Content)
	}
	// Soft delete: gone from the list, second delete 404s.
	if err := DeleteComment(ctx, pool, wsSlug, ident, issueID, c.ID, actor); err != nil {
		t.Fatalf("DeleteComment: %v", err)
	}
	tree, err := ListComments(ctx, pool, wsSlug, ident, issueID, actor)
	if err != nil {
		t.Fatalf("ListComments: %v", err)
	}
	if len(tree) != 0 {
		t.Fatalf("comments after delete = %d, want 0", len(tree))
	}
	if err := DeleteComment(ctx, pool, wsSlug, ident, issueID, c.ID, actor); err == nil {
		t.Fatal("double delete accepted, want ErrCommentNotFound")
	}
}

func TestRelationReverseDerivation(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	wsSlug, ident, _, actor := testSatelliteFixture(t, pool, "rel")
	a := createTestIssue(t, pool, wsSlug, ident, actor, "A")
	b := createTestIssue(t, pool, wsSlug, ident, actor, "B")

	// A blocked_by B: one canonical row.
	if err := CreateRelation(ctx, pool, wsSlug, ident, a.ID, actor, b.ID, "blocked_by"); err != nil {
		t.Fatalf("CreateRelation: %v", err)
	}
	// Duplicate POST is idempotent, not a 409.
	if err := CreateRelation(ctx, pool, wsSlug, ident, a.ID, actor, b.ID, "blocked_by"); err != nil {
		t.Fatalf("duplicate CreateRelation: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM issue_relations WHERE issue_id = $1::uuid`, a.ID).Scan(&n); err != nil {
		t.Fatalf("count relations: %v", err)
	}
	if n != 1 {
		t.Fatalf("canonical rows = %d, want exactly 1", n)
	}

	// A's side shows the canonical direction.
	relsA, err := ListRelations(ctx, pool, wsSlug, ident, a.ID, actor)
	if err != nil {
		t.Fatalf("ListRelations A: %v", err)
	}
	if len(relsA) != 1 || relsA[0].Type != "blocked_by" || relsA[0].Direction != "outgoing" {
		t.Fatalf("A relations = %+v, want one outgoing blocked_by", relsA)
	}
	// B's side derives the reverse: blocking.
	relsB, err := ListRelations(ctx, pool, wsSlug, ident, b.ID, actor)
	if err != nil {
		t.Fatalf("ListRelations B: %v", err)
	}
	if len(relsB) != 1 || relsB[0].Type != "blocking" || relsB[0].Direction != "incoming" {
		t.Fatalf("B relations = %+v, want one incoming blocking", relsB)
	}

	// Invalid type and self-relation are rejected.
	if err := CreateRelation(ctx, pool, wsSlug, ident, a.ID, actor, b.ID, "eats"); err == nil {
		t.Fatal("invalid relation type accepted, want ErrInvalidRelationType")
	}
	if err := CreateRelation(ctx, pool, wsSlug, ident, a.ID, actor, a.ID, "relates_to"); err == nil {
		t.Fatal("self-relation accepted, want an error")
	}

	// Delete from B's side using the derived label resolves the
	// canonical row.
	if err := DeleteRelation(ctx, pool, wsSlug, ident, b.ID, actor, a.ID, "blocking"); err != nil {
		t.Fatalf("DeleteRelation via reverse label: %v", err)
	}
	relsA, _ = ListRelations(ctx, pool, wsSlug, ident, a.ID, actor)
	if len(relsA) != 0 {
		t.Fatalf("relations after delete = %d, want 0", len(relsA))
	}
}

func TestVersionSnapshotsAndRestore(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	wsSlug, ident, issueID, actor := testSatelliteFixture(t, pool, "ver")

	// Create snapshots version 1.
	vs, err := ListVersions(ctx, pool, wsSlug, ident, issueID, actor)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(vs) != 1 || vs[0].VersionNo != 1 {
		t.Fatalf("versions after create = %+v, want [v1]", vs)
	}

	// A PATCH snapshots version 2 (post-mutation state).
	name := "Renamed"
	if _, err := UpdateIssue(ctx, pool, wsSlug, ident, issueID, actor, IssuePatch{Name: &name}); err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}
	vs, err = ListVersions(ctx, pool, wsSlug, ident, issueID, actor)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(vs) != 2 {
		t.Fatalf("versions after patch = %d, want 2", len(vs))
	}
	v1, err := GetVersion(ctx, pool, wsSlug, ident, issueID, actor, 1)
	if err != nil {
		t.Fatalf("GetVersion 1: %v", err)
	}
	var v1Issue Issue
	if err := json.Unmarshal(v1.Snapshot, &v1Issue); err != nil {
		t.Fatalf("decode v1 snapshot: %v", err)
	}
	if v1Issue.Name == name {
		t.Fatal("v1 snapshot shows the renamed value — snapshots must be immutable")
	}

	// Restore v1: applies the old snapshot as a NEW version (v3),
	// history untouched.
	restored, err := RestoreIssueVersion(ctx, pool, wsSlug, ident, issueID, actor, 1)
	if err != nil {
		t.Fatalf("RestoreIssueVersion: %v", err)
	}
	if restored.Name == name {
		t.Fatalf("restored name = %q, want the v1 name", restored.Name)
	}
	vs, err = ListVersions(ctx, pool, wsSlug, ident, issueID, actor)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(vs) != 3 || vs[2].VersionNo != 3 {
		t.Fatalf("versions after restore = %+v, want [v1 v2 v3]", vs)
	}
	v1again, err := GetVersion(ctx, pool, wsSlug, ident, issueID, actor, 1)
	if err != nil {
		t.Fatalf("GetVersion 1 after restore: %v", err)
	}
	if string(v1again.Snapshot) != string(v1.Snapshot) {
		t.Fatal("v1 snapshot changed by the restore — versions must be immutable")
	}

	// A no-change PATCH writes no version.
	if _, err := UpdateIssue(ctx, pool, wsSlug, ident, issueID, actor, IssuePatch{Name: &restored.Name}); err != nil {
		t.Fatalf("no-change patch: %v", err)
	}
	vs, _ = ListVersions(ctx, pool, wsSlug, ident, issueID, actor)
	if len(vs) != 3 {
		t.Fatalf("versions after no-change patch = %d, want 3", len(vs))
	}

	// Restoring a nonexistent version 404s.
	if _, err := RestoreIssueVersion(ctx, pool, wsSlug, ident, issueID, actor, 99); err == nil {
		t.Fatal("restore of v99 accepted, want ErrVersionNotFound")
	}
}

func TestToggleIdempotency(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	wsSlug, ident, issueID, actor := testSatelliteFixture(t, pool, "toggle")

	// Votes: one per user; double vote is a no-op.
	if err := VoteIssue(ctx, pool, wsSlug, ident, issueID, actor); err != nil {
		t.Fatalf("VoteIssue: %v", err)
	}
	if err := VoteIssue(ctx, pool, wsSlug, ident, issueID, actor); err != nil {
		t.Fatalf("double VoteIssue: %v", err)
	}
	votes, err := GetIssueVotes(ctx, pool, wsSlug, ident, issueID, actor)
	if err != nil {
		t.Fatalf("GetIssueVotes: %v", err)
	}
	if votes.Count != 1 || !votes.Voted {
		t.Fatalf("votes = %+v, want count 1 voted true", votes)
	}
	if err := UnvoteIssue(ctx, pool, wsSlug, ident, issueID, actor); err != nil {
		t.Fatalf("UnvoteIssue: %v", err)
	}
	if err := UnvoteIssue(ctx, pool, wsSlug, ident, issueID, actor); err != nil {
		t.Fatalf("double UnvoteIssue: %v", err)
	}
	votes, _ = GetIssueVotes(ctx, pool, wsSlug, ident, issueID, actor)
	if votes.Count != 0 || votes.Voted {
		t.Fatalf("votes after unvote = %+v, want count 0 voted false", votes)
	}

	// Issue reactions group by emoji.
	if err := AddIssueReaction(ctx, pool, wsSlug, ident, issueID, actor, "👍"); err != nil {
		t.Fatalf("AddIssueReaction: %v", err)
	}
	if err := AddIssueReaction(ctx, pool, wsSlug, ident, issueID, actor, "👍"); err != nil {
		t.Fatalf("double AddIssueReaction: %v", err)
	}
	other := createTestUser(t, pool, uniqueTestEmail("sat-toggle-other"))
	addTestMember(t, pool, wsSlug, actor, other, 15) // member, so they may react
	if err := AddIssueReaction(ctx, pool, wsSlug, ident, issueID, other, "👍"); err != nil {
		t.Fatalf("other user reaction: %v", err)
	}
	reactions, err := ListIssueReactions(ctx, pool, wsSlug, ident, issueID, actor)
	if err != nil {
		t.Fatalf("ListIssueReactions: %v", err)
	}
	if len(reactions) != 1 || reactions[0].Emoji != "👍" || reactions[0].Count != 2 || !reactions[0].Reacted {
		t.Fatalf("reactions = %+v, want one 👍 group count 2 reacted true", reactions)
	}
	if err := RemoveIssueReaction(ctx, pool, wsSlug, ident, issueID, actor, "👍"); err != nil {
		t.Fatalf("RemoveIssueReaction: %v", err)
	}
	reactions, _ = ListIssueReactions(ctx, pool, wsSlug, ident, issueID, actor)
	if len(reactions) != 1 || reactions[0].Count != 1 || reactions[0].Reacted {
		t.Fatalf("reactions after remove = %+v, want count 1 reacted false", reactions)
	}

	// Subscribers: self-subscribe is idempotent.
	if err := SubscribeIssue(ctx, pool, wsSlug, ident, issueID, actor); err != nil {
		t.Fatalf("SubscribeIssue: %v", err)
	}
	if err := SubscribeIssue(ctx, pool, wsSlug, ident, issueID, actor); err != nil {
		t.Fatalf("double SubscribeIssue: %v", err)
	}
	subs, err := ListSubscribers(ctx, pool, wsSlug, ident, issueID, actor)
	if err != nil {
		t.Fatalf("ListSubscribers: %v", err)
	}
	if len(subs) != 1 {
		t.Fatalf("subscribers = %d, want 1", len(subs))
	}
	if err := UnsubscribeIssue(ctx, pool, wsSlug, ident, issueID, actor); err != nil {
		t.Fatalf("UnsubscribeIssue: %v", err)
	}
	subs, _ = ListSubscribers(ctx, pool, wsSlug, ident, issueID, actor)
	if len(subs) != 0 {
		t.Fatalf("subscribers after unsubscribe = %d, want 0", len(subs))
	}
}

func TestIssueHistory(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	wsSlug, ident, issueID, actor := testSatelliteFixture(t, pool, "hist")

	name := "Renamed for history"
	if _, err := UpdateIssue(ctx, pool, wsSlug, ident, issueID, actor, IssuePatch{Name: &name}); err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}
	hist, err := GetIssueHistory(ctx, pool, wsSlug, ident, issueID, actor)
	if err != nil {
		t.Fatalf("GetIssueHistory: %v", err)
	}
	// _created + name change, chronological.
	if len(hist) != 2 {
		t.Fatalf("history rows = %d, want 2", len(hist))
	}
	if hist[0].Field != "_created" || hist[1].Field != "name" {
		t.Fatalf("history fields = %q,%q, want _created,name (chronological)",
			hist[0].Field, hist[1].Field)
	}
	if hist[0].Actor.ID != actor || hist[0].Actor.Email == "" {
		t.Fatalf("history actor = %+v, want the creator with email", hist[0].Actor)
	}
}

func TestSatellitesOfDeletedIssue404(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	wsSlug, ident, issueID, actor := testSatelliteFixture(t, pool, "del404")

	if err := DeleteIssue(ctx, pool, wsSlug, ident, issueID, actor); err != nil {
		t.Fatalf("DeleteIssue: %v", err)
	}
	if _, err := CreateComment(ctx, pool, wsSlug, ident, issueID, actor,
		json.RawMessage(`{"type":"doc"}`), nil); err == nil {
		t.Fatal("comment on deleted issue accepted, want ErrIssueNotFound")
	}
	if err := VoteIssue(ctx, pool, wsSlug, ident, issueID, actor); err == nil {
		t.Fatal("vote on deleted issue accepted, want ErrIssueNotFound")
	}
	if err := CreateRelation(ctx, pool, wsSlug, ident, issueID, actor, issueID, "relates_to"); err == nil {
		t.Fatal("relation on deleted issue accepted, want ErrIssueNotFound")
	}
	if _, err := ListVersions(ctx, pool, wsSlug, ident, issueID, actor); err == nil {
		t.Fatal("versions of deleted issue listed, want ErrIssueNotFound")
	}
	if _, err := GetIssueHistory(ctx, pool, wsSlug, ident, issueID, actor); err == nil {
		t.Fatal("history of deleted issue listed, want ErrIssueNotFound")
	}
}

func TestSatelliteGuestReadOnly(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	wsSlug, ident, issueID, actor := testSatelliteFixture(t, pool, "guest")
	guest := createTestUser(t, pool, uniqueTestEmail("sat-guest-ro"))
	addTestMember(t, pool, wsSlug, actor, guest, 5) // guest role

	// Guests may read satellites...
	if _, err := ListComments(ctx, pool, wsSlug, ident, issueID, guest); err != nil {
		t.Fatalf("guest ListComments: %v", err)
	}
	if _, err := GetIssueHistory(ctx, pool, wsSlug, ident, issueID, guest); err != nil {
		t.Fatalf("guest GetIssueHistory: %v", err)
	}
	// ...but not mutate them.
	if _, err := CreateComment(ctx, pool, wsSlug, ident, issueID, guest,
		json.RawMessage(`{"type":"doc"}`), nil); err == nil {
		t.Fatal("guest comment accepted, want ErrForbidden")
	}
	if err := VoteIssue(ctx, pool, wsSlug, ident, issueID, guest); err == nil {
		t.Fatal("guest vote accepted, want ErrForbidden")
	}
	if err := CreateRelation(ctx, pool, wsSlug, ident, issueID, guest, issueID, "relates_to"); err == nil {
		t.Fatal("guest relation accepted, want ErrForbidden")
	}
}

func TestConcurrentPatchVersionsSequential(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	wsSlug, ident, issueID, actor := testSatelliteFixture(t, pool, "verconc")

	// 10 concurrent PATCHes: the FOR UPDATE serialization must yield 10
	// distinct version rows (v2..v11), no gaps, no duplicates.
	const n = 10
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := "concurrent-" + strconv.Itoa(i)
			_, err := UpdateIssue(ctx, pool, wsSlug, ident, issueID, actor, IssuePatch{Name: &name})
			errs[i] = err
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: UpdateIssue: %v", i, err)
		}
	}
	vs, err := ListVersions(ctx, pool, wsSlug, ident, issueID, actor)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(vs) != n+1 {
		t.Fatalf("versions = %d, want %d", len(vs), n+1)
	}
	for i, v := range vs {
		if v.VersionNo != i+1 {
			t.Fatalf("versions[%d].VersionNo = %d, want %d (gap or duplicate)", i, v.VersionNo, i+1)
		}
	}
}
