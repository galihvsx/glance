package service

// Duplicate merge service tests (C25T3): POST .../issues/{uuid}/merge
// {into_uuid} folds a duplicate issue into a surviving issue of the same
// project. One transaction moves comments, attachment metadata, links,
// reactions, votes, time entries and subscribers from source to target;
// the source lands in a cancelled-group state, a 'duplicate'
// issue_relations row records the surviving issue, and _merged activity
// entries are written on both sides. Re-merging into the same target is
// a 200 no-op (already_merged). Real test database, no skips. Reuses the
// parent harness (setupParentTest, createTestIssue, addTestMember,
// RoleGuest, uniqueTestEmail) and the satellite service helpers.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func mergeTestIssueStateGroup(t *testing.T, pool *pgxpool.Pool, ctx context.Context, issueID string) string {
	t.Helper()
	var group string
	if err := pool.QueryRow(ctx,
		`SELECT s."group" FROM issues i JOIN states s ON s.id = i.state_id WHERE i.id = $1::uuid`,
		issueID).Scan(&group); err != nil {
		t.Fatalf("state group lookup: %v", err)
	}
	return group
}

func mergeTestCount(t *testing.T, pool *pgxpool.Pool, ctx context.Context, q, issueID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, q, issueID).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", q, err)
	}
	return n
}

func TestMergeHappyPath(t *testing.T) {
	fx := setupParentTest(t, "mg-happy")
	ctx := context.Background()

	source := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Crash on login")
	target := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Login crash")
	other := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Unrelated")
	watcher := createTestUser(t, fx.pool, uniqueTestEmail("mg-watcher"))
	addTestMember(t, fx.pool, fx.wsSlug, fx.actor, watcher, RoleMember)

	content := json.RawMessage(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"same bug"}]}]}`)
	if _, err := CreateComment(ctx, fx.pool, fx.wsSlug, fx.identifier, source.ID, fx.actor, content, nil); err != nil {
		t.Fatalf("CreateComment: %v", err)
	}
	if err := AddIssueReaction(ctx, fx.pool, fx.wsSlug, fx.identifier, source.ID, watcher, "👍"); err != nil {
		t.Fatalf("AddIssueReaction: %v", err)
	}
	if err := VoteIssue(ctx, fx.pool, fx.wsSlug, fx.identifier, source.ID, watcher); err != nil {
		t.Fatalf("VoteIssue: %v", err)
	}
	if err := SubscribeIssue(ctx, fx.pool, fx.wsSlug, fx.identifier, source.ID, watcher); err != nil {
		t.Fatalf("SubscribeIssue: %v", err)
	}
	if _, err := CreateIssueLink(ctx, fx.pool, fx.wsSlug, fx.identifier, source.ID, fx.actor, other.ID, "blocks"); err != nil {
		t.Fatalf("CreateIssueLink outgoing: %v", err)
	}
	if _, err := CreateIssueLink(ctx, fx.pool, fx.wsSlug, fx.identifier, other.ID, fx.actor, source.ID, "relates_to"); err != nil {
		t.Fatalf("CreateIssueLink incoming: %v", err)
	}
	if _, err := fx.pool.Exec(ctx,
		`INSERT INTO attachments (issue_id, filename, content_type, size_bytes, stored_path, uploaded_by)
		 VALUES ($1::uuid, 'a.txt', 'text/plain', 3, 'k', $2::uuid)`, source.ID, fx.actor); err != nil {
		t.Fatalf("insert attachment: %v", err)
	}
	if _, err := fx.pool.Exec(ctx,
		`INSERT INTO time_entries (issue_id, user_id, started_at, ended_at) VALUES ($1::uuid, $2::uuid, now() - interval '1 hour', now())`,
		source.ID, fx.actor); err != nil {
		t.Fatalf("insert time entry: %v", err)
	}

	res, err := MergeIssue(ctx, fx.pool, fx.wsSlug, fx.identifier, source.ID, fx.actor, target.ID)
	if err != nil {
		t.Fatalf("MergeIssue: %v", err)
	}
	if !res.Merged || res.AlreadyMerged {
		t.Fatalf("result = %+v, want {Merged:true AlreadyMerged:false}", res)
	}
	if res.IntoDisplayID == "" {
		t.Fatal("into_display_id is empty")
	}
	if res.Issue == nil || res.Issue.ID != source.ID {
		t.Fatalf("issue = %+v, want the merged source", res.Issue)
	}

	// Source is closed in a cancelled-group state.
	if g := mergeTestIssueStateGroup(t, fx.pool, ctx, source.ID); g != "cancelled" {
		t.Fatalf("source state group = %q, want cancelled", g)
	}

	// Every satellite moved to the target; none left on the source.
	moved := map[string]string{
		"comments":          `SELECT COUNT(*) FROM comments WHERE issue_id = $1::uuid`,
		"issue_reactions":   `SELECT COUNT(*) FROM issue_reactions WHERE issue_id = $1::uuid`,
		"issue_votes":       `SELECT COUNT(*) FROM issue_votes WHERE issue_id = $1::uuid`,
		"issue_subscribers": `SELECT COUNT(*) FROM issue_subscribers WHERE issue_id = $1::uuid`,
		"attachments":       `SELECT COUNT(*) FROM attachments WHERE issue_id = $1::uuid`,
		"time_entries":      `SELECT COUNT(*) FROM time_entries WHERE issue_id = $1::uuid`,
	}
	for name, q := range moved {
		if n := mergeTestCount(t, fx.pool, ctx, q, source.ID); n != 0 {
			t.Fatalf("%s rows left on source = %d, want 0", name, n)
		}
		if n := mergeTestCount(t, fx.pool, ctx, q, target.ID); n == 0 {
			t.Fatalf("%s rows on target = 0, want > 0", name)
		}
	}

	// Links: nothing touches the source anymore; both edges were
	// rewritten onto the target (outgoing blocks → target blocks other;
	// incoming relates_to → other relates_to target).
	if n := mergeTestCount(t, fx.pool, ctx,
		`SELECT COUNT(*) FROM issue_links WHERE issue_id = $1::uuid OR target_issue_id = $1::uuid`, source.ID); n != 0 {
		t.Fatalf("links touching source = %d, want 0", n)
	}
	var rewritten int
	if err := fx.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM issue_links WHERE issue_id = $1::uuid AND target_issue_id = $2::uuid AND kind = 'blocks'`,
		target.ID, other.ID).Scan(&rewritten); err != nil {
		t.Fatalf("rewritten outgoing link: %v", err)
	}
	if rewritten != 1 {
		t.Fatalf("rewritten outgoing link rows = %d, want 1", rewritten)
	}
	if err := fx.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM issue_links WHERE issue_id = $1::uuid AND target_issue_id = $2::uuid AND kind = 'relates_to'`,
		other.ID, target.ID).Scan(&rewritten); err != nil {
		t.Fatalf("rewritten incoming link: %v", err)
	}
	if rewritten != 1 {
		t.Fatalf("rewritten incoming link rows = %d, want 1", rewritten)
	}

	// The duplicate relation records the surviving issue.
	var relType string
	if err := fx.pool.QueryRow(ctx,
		`SELECT type FROM issue_relations
		 WHERE type = 'duplicate'
		   AND ((issue_id = $1::uuid AND related_issue_id = $2::uuid)
		     OR (issue_id = $2::uuid AND related_issue_id = $1::uuid))`,
		source.ID, target.ID).Scan(&relType); err != nil {
		t.Fatalf("duplicate relation: %v", err)
	}
	if relType != "duplicate" {
		t.Fatalf("relation type = %q, want duplicate", relType)
	}

	// Activity: _merged on both sides, plus the uniform state_id entry
	// on the source.
	if n := countActivities(t, fx.pool, source.ID, "_merged"); n != 1 {
		t.Fatalf("_merged activity rows on source = %d, want 1", n)
	}
	if n := countActivities(t, fx.pool, target.ID, "_merged"); n != 1 {
		t.Fatalf("_merged activity rows on target = %d, want 1", n)
	}
	if n := countActivities(t, fx.pool, source.ID, "state_id"); n != 1 {
		t.Fatalf("state_id activity rows on source = %d, want 1", n)
	}
}

func TestMergeSelf(t *testing.T) {
	fx := setupParentTest(t, "mg-self")
	ctx := context.Background()

	iss := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Lonely")
	if _, err := MergeIssue(ctx, fx.pool, fx.wsSlug, fx.identifier, iss.ID, fx.actor, iss.ID); !errors.Is(err, ErrMergeSelf) {
		t.Fatalf("err = %v, want ErrMergeSelf", err)
	}
}

func TestMergeEmptyTarget(t *testing.T) {
	fx := setupParentTest(t, "mg-empty")
	ctx := context.Background()

	iss := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Lonely")
	if _, err := MergeIssue(ctx, fx.pool, fx.wsSlug, fx.identifier, iss.ID, fx.actor, ""); !errors.Is(err, ErrMergeTargetRequired) {
		t.Fatalf("err = %v, want ErrMergeTargetRequired", err)
	}
}

func TestMergeCrossProject(t *testing.T) {
	fx := setupParentTest(t, "mg-xproj")
	ctx := context.Background()

	other := createTestProject(t, fx.pool, fx.wsSlug, fx.actor, "Design", uniqueTestIdentifier())
	foreign := createTestIssue(t, fx.pool, fx.wsSlug, other.Identifier, fx.actor, "Foreign")
	iss := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Local")

	if _, err := MergeIssue(ctx, fx.pool, fx.wsSlug, fx.identifier, iss.ID, fx.actor, foreign.ID); !errors.Is(err, ErrMergeCrossProject) {
		t.Fatalf("err = %v, want ErrMergeCrossProject", err)
	}
}

func TestMergeIntoDescendant(t *testing.T) {
	fx := setupParentTest(t, "mg-desc")
	ctx := context.Background()

	parent := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Parent")
	child := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Child")
	if _, err := SetParent(ctx, fx.pool, fx.wsSlug, fx.identifier, child.ID, fx.actor, parent.ID); err != nil {
		t.Fatalf("setup SetParent: %v", err)
	}

	// Merging the parent into its own sub-issue is rejected.
	if _, err := MergeIssue(ctx, fx.pool, fx.wsSlug, fx.identifier, parent.ID, fx.actor, child.ID); !errors.Is(err, ErrMergeDescendant) {
		t.Fatalf("err = %v, want ErrMergeDescendant", err)
	}
}

func TestMergeUnknownTarget(t *testing.T) {
	fx := setupParentTest(t, "mg-unknownt")
	ctx := context.Background()

	iss := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Lonely")
	if _, err := MergeIssue(ctx, fx.pool, fx.wsSlug, fx.identifier, iss.ID, fx.actor,
		"00000000-0000-0000-0000-000000000000"); !errors.Is(err, ErrIssueNotFound) {
		t.Fatalf("err = %v, want ErrIssueNotFound", err)
	}
	if _, err := MergeIssue(ctx, fx.pool, fx.wsSlug, fx.identifier, iss.ID, fx.actor,
		"not-a-uuid"); !errors.Is(err, ErrIssueNotFound) {
		t.Fatalf("malformed target err = %v, want ErrIssueNotFound", err)
	}
}

func TestMergeUnknownSource(t *testing.T) {
	fx := setupParentTest(t, "mg-unknowns")
	ctx := context.Background()

	target := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Target")
	if _, err := MergeIssue(ctx, fx.pool, fx.wsSlug, fx.identifier,
		"00000000-0000-0000-0000-000000000000", fx.actor, target.ID); !errors.Is(err, ErrIssueNotFound) {
		t.Fatalf("err = %v, want ErrIssueNotFound", err)
	}
}

func TestMergeIdempotent(t *testing.T) {
	fx := setupParentTest(t, "mg-idem")
	ctx := context.Background()

	source := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Dup")
	target := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Original")

	res, err := MergeIssue(ctx, fx.pool, fx.wsSlug, fx.identifier, source.ID, fx.actor, target.ID)
	if err != nil {
		t.Fatalf("first MergeIssue: %v", err)
	}
	if !res.Merged || res.AlreadyMerged {
		t.Fatalf("first result = %+v, want merged", res)
	}

	res2, err := MergeIssue(ctx, fx.pool, fx.wsSlug, fx.identifier, source.ID, fx.actor, target.ID)
	if err != nil {
		t.Fatalf("second MergeIssue: %v", err)
	}
	if res2.Merged || !res2.AlreadyMerged {
		t.Fatalf("second result = %+v, want already_merged", res2)
	}

	// The re-merge wrote nothing new: still exactly one _merged entry
	// per side and one duplicate relation.
	if n := countActivities(t, fx.pool, source.ID, "_merged"); n != 1 {
		t.Fatalf("_merged rows on source after re-merge = %d, want 1", n)
	}
	if n := countActivities(t, fx.pool, target.ID, "_merged"); n != 1 {
		t.Fatalf("_merged rows on target after re-merge = %d, want 1", n)
	}
}

func TestMergeGuestForbidden(t *testing.T) {
	fx := setupParentTest(t, "mg-guest")
	ctx := context.Background()

	guest := createTestUser(t, fx.pool, uniqueTestEmail("mg-guest"))
	addTestMember(t, fx.pool, fx.wsSlug, fx.actor, guest, RoleGuest)

	source := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Dup")
	target := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Original")
	if _, err := MergeIssue(ctx, fx.pool, fx.wsSlug, fx.identifier, source.ID, guest, target.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
}

func TestMergeDuplicateSatellites(t *testing.T) {
	fx := setupParentTest(t, "mg-dupsat")
	ctx := context.Background()

	source := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Dup")
	target := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Original")
	voter := createTestUser(t, fx.pool, uniqueTestEmail("mg-voter"))
	addTestMember(t, fx.pool, fx.wsSlug, fx.actor, voter, RoleMember)

	// The same user already voted, reacted and subscribed on BOTH
	// issues: the merge must not 500 on the unique constraints.
	for _, id := range []string{source.ID, target.ID} {
		if err := VoteIssue(ctx, fx.pool, fx.wsSlug, fx.identifier, id, voter); err != nil {
			t.Fatalf("VoteIssue: %v", err)
		}
		if err := AddIssueReaction(ctx, fx.pool, fx.wsSlug, fx.identifier, id, voter, "👍"); err != nil {
			t.Fatalf("AddIssueReaction: %v", err)
		}
		if err := SubscribeIssue(ctx, fx.pool, fx.wsSlug, fx.identifier, id, voter); err != nil {
			t.Fatalf("SubscribeIssue: %v", err)
		}
	}

	if _, err := MergeIssue(ctx, fx.pool, fx.wsSlug, fx.identifier, source.ID, fx.actor, target.ID); err != nil {
		t.Fatalf("MergeIssue with duplicate satellites: %v", err)
	}
	for _, q := range []string{
		`SELECT COUNT(*) FROM issue_votes WHERE issue_id = $1::uuid`,
		`SELECT COUNT(*) FROM issue_reactions WHERE issue_id = $1::uuid`,
		`SELECT COUNT(*) FROM issue_subscribers WHERE issue_id = $1::uuid`,
	} {
		var n int
		if err := fx.pool.QueryRow(ctx, q, target.ID).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		if n != 1 {
			t.Fatalf("target rows for %q = %d, want 1 (deduped)", q, n)
		}
	}
}

func TestMergeCollidingRunningTimer(t *testing.T) {
	fx := setupParentTest(t, "mg-timer")
	ctx := context.Background()

	source := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Dup")
	target := createTestIssue(t, fx.pool, fx.wsSlug, fx.identifier, fx.actor, "Original")

	// The actor has a RUNNING timer on both issues: the partial unique
	// index would reject a naive move. The merge stops the source timer
	// and moves it instead of dropping data.
	for _, id := range []string{source.ID, target.ID} {
		if _, err := fx.pool.Exec(ctx,
			`INSERT INTO time_entries (issue_id, user_id, started_at) VALUES ($1::uuid, $2::uuid, now() - interval '10 minutes')`,
			id, fx.actor); err != nil {
			t.Fatalf("insert running timer: %v", err)
		}
	}

	if _, err := MergeIssue(ctx, fx.pool, fx.wsSlug, fx.identifier, source.ID, fx.actor, target.ID); err != nil {
		t.Fatalf("MergeIssue with colliding timers: %v", err)
	}
	var running, moved int
	if err := fx.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM time_entries WHERE issue_id = $1::uuid AND ended_at IS NULL`,
		target.ID).Scan(&running); err != nil {
		t.Fatalf("running count: %v", err)
	}
	if running != 1 {
		t.Fatalf("running timers on target = %d, want 1", running)
	}
	if err := fx.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM time_entries WHERE issue_id = $1::uuid`, target.ID).Scan(&moved); err != nil {
		t.Fatalf("moved count: %v", err)
	}
	if moved != 2 {
		t.Fatalf("time entries on target = %d, want 2 (one stopped)", moved)
	}
	if n := mergeTestCount(t, fx.pool, ctx, `SELECT COUNT(*) FROM time_entries WHERE issue_id = $1::uuid`, source.ID); n != 0 {
		t.Fatalf("time entries left on source = %d, want 0", n)
	}
}
