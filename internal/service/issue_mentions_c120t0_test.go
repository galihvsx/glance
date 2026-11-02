package service

// C120T0: persisted @-mention index (migration 000082) —
// backfill sweep + write-path maintenance. Backfill: fixture
// comments inserted directly into the comments table (they
// predate the index, so they bypass the service write path)
// with resolvable, ambiguous, self-mention, non-member, and
// deleted-comment tokens → only the resolvable row lands;
// a re-run is a no-op, and the non-empty fast path skips a
// later pre-existing comment (documented sweep semantics).
// Write path: comment create inserts, edit removes + adds in
// the same tx, delete removes, and a second comment
// mentioning the same user survives the first comment's
// delete (per-comment grain). Real test database, no skips.

import (
	"context"
	"errors"
	"sort"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func mentionUserIDs(t *testing.T, pool *pgxpool.Pool, commentID string) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT user_id::text FROM issue_mentions WHERE comment_id = $1::uuid ORDER BY user_id`, commentID)
	if err != nil {
		t.Fatalf("query issue_mentions: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan issue_mentions: %v", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows issue_mentions: %v", err)
	}
	return out
}

func insertDirectComment(t *testing.T, pool *pgxpool.Pool, issueID, actorID, text string, deleted bool) string {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(),
		`INSERT INTO comments (issue_id, actor_id, content, deleted_at)
		 VALUES ($1::uuid, $2::uuid, $3::jsonb, CASE WHEN $4 THEN now() ELSE NULL END)
		 RETURNING id::text`,
		issueID, actorID, string(tiptapComment(text)), deleted).Scan(&id)
	if err != nil {
		t.Fatalf("insert direct comment: %v", err)
	}
	return id
}

func TestBackfillIssueMentions(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	actorID, watcherID, mentioneeID, wsSlug, ident, issueID := createMentionFixture(t, pool)
	_ = wsSlug
	_ = ident

	// A second member sharing the mentionee's display name
	// makes that name ambiguous (skipped by resolution).
	twin := createTestUser(t, pool, uniqueTestEmail("c120-twin"))
	if _, err := pool.Exec(ctx, `UPDATE users SET name = 'Mentee Galih' WHERE id = $1::uuid`, twin); err != nil {
		t.Fatalf("name twin: %v", err)
	}
	var wsID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM workspaces WHERE slug = $1`, wsSlug).Scan(&wsID); err != nil {
		t.Fatalf("workspace id: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO workspace_members (workspace_id, user_id, role)
		 VALUES ($1::uuid, $2::uuid, $3) ON CONFLICT DO NOTHING`,
		wsID, twin, RoleMember); err != nil {
		t.Fatalf("add twin member: %v", err)
	}

	// The sweep only runs when the table is empty (fast path);
	// the shared test DB may already hold rows from other
	// tests, so clear it first — no other test reads this
	// table, and this test re-creates exactly what it asserts.
	if _, err := pool.Exec(ctx, `DELETE FROM issue_mentions`); err != nil {
		t.Fatalf("clear issue_mentions: %v", err)
	}

	resolvable := insertDirectComment(t, pool, issueID, actorID, "hey @Watcher Wulan take a look", false)
	ambiguous := insertDirectComment(t, pool, issueID, actorID, "hey @Mentee Galih take a look", false)
	selfMention := insertDirectComment(t, pool, issueID, actorID, "note to self @Actor Andy", false)
	nonMember := insertDirectComment(t, pool, issueID, actorID, "hey @Ghost Person take a look", false)
	deleted := insertDirectComment(t, pool, issueID, actorID, "hey @Watcher Wulan (deleted)", true)
	plain := insertDirectComment(t, pool, issueID, actorID, "no mentions here", false)
	t.Cleanup(func() {
		for _, cid := range []string{resolvable, ambiguous, selfMention, nonMember, deleted, plain} {
			_, _ = pool.Exec(ctx, `DELETE FROM issue_mentions WHERE comment_id = $1::uuid`, cid)
			_, _ = pool.Exec(ctx, `DELETE FROM comments WHERE id = $1::uuid`, cid)
		}
	})

	if err := BackfillIssueMentions(ctx, pool); err != nil {
		t.Fatalf("BackfillIssueMentions: %v", err)
	}
	assertMentionRows := func(label, commentID string, want ...string) {
		t.Helper()
		got := mentionUserIDs(t, pool, commentID)
		sort.Strings(want)
		if len(got) != len(want) {
			t.Fatalf("%s: mention rows = %v, want %v", label, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%s: mention rows = %v, want %v", label, got, want)
			}
		}
	}
	assertMentionRows("resolvable", resolvable, watcherID)
	assertMentionRows("ambiguous skipped", ambiguous)
	assertMentionRows("self-mention skipped", selfMention)
	assertMentionRows("non-member skipped", nonMember)
	assertMentionRows("deleted comment excluded", deleted)
	assertMentionRows("plain comment", plain)
	// The ambiguous token must NOT have resolved to either twin.
	for _, cid := range []string{ambiguous} {
		for _, uid := range mentionUserIDs(t, pool, cid) {
			if uid == mentioneeID || uid == twin {
				t.Fatalf("ambiguous comment resolved to %s, want none", uid)
			}
		}
	}

	// Re-run is a no-op: same rows, no duplicates (PK would
	// reject them anyway — the count pins the no-op).
	var before int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM issue_mentions`).Scan(&before); err != nil {
		t.Fatalf("count before re-run: %v", err)
	}
	if err := BackfillIssueMentions(ctx, pool); err != nil {
		t.Fatalf("BackfillIssueMentions re-run: %v", err)
	}
	var after int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM issue_mentions`).Scan(&after); err != nil {
		t.Fatalf("count after re-run: %v", err)
	}
	if before != after {
		t.Fatalf("re-run changed row count %d -> %d, want no-op", before, after)
	}

	// Fast path: a NEW pre-existing comment inserted after a
	// completed sweep is NOT picked up — the table is
	// non-empty, so the sweep is skipped by design (the
	// normal write path covers new comments; only a boot
	// after a crash-mid-sweep, which leaves zero rows,
	// re-runs it).
	late := insertDirectComment(t, pool, issueID, actorID, "hey @Watcher Wulan late", false)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM issue_mentions WHERE comment_id = $1::uuid`, late)
		_, _ = pool.Exec(ctx, `DELETE FROM comments WHERE id = $1::uuid`, late)
	})
	if err := BackfillIssueMentions(ctx, pool); err != nil {
		t.Fatalf("BackfillIssueMentions fast-path run: %v", err)
	}
	assertMentionRows("fast-path skip", late)
}

func TestCommentCreatePopulatesMentionIndex(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	actorID, _, mentioneeID, wsSlug, ident, issueID := createMentionFixture(t, pool)

	c, err := CreateComment(ctx, pool, wsSlug, ident, issueID, actorID,
		tiptapComment("hey @Mentee Galih take a look"), nil)
	if err != nil {
		t.Fatalf("CreateComment: %v", err)
	}
	got := mentionUserIDs(t, pool, c.ID)
	if len(got) != 1 || got[0] != mentioneeID {
		t.Fatalf("create index rows = %v, want [%s]", got, mentioneeID)
	}
}

func TestCommentUpdateSyncsMentionIndex(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	actorID, watcherID, mentioneeID, wsSlug, ident, issueID := createMentionFixture(t, pool)

	c, err := CreateComment(ctx, pool, wsSlug, ident, issueID, actorID,
		tiptapComment("hey @Watcher Wulan take a look"), nil)
	if err != nil {
		t.Fatalf("CreateComment: %v", err)
	}
	if got := mentionUserIDs(t, pool, c.ID); len(got) != 1 || got[0] != watcherID {
		t.Fatalf("create index rows = %v, want [%s]", got, watcherID)
	}
	// Edit removes the watcher mention and adds the mentionee.
	if _, err := UpdateComment(ctx, pool, wsSlug, ident, issueID, c.ID, actorID,
		tiptapComment("actually @Mentee Galih take a look")); err != nil {
		t.Fatalf("UpdateComment: %v", err)
	}
	got := mentionUserIDs(t, pool, c.ID)
	if len(got) != 1 || got[0] != mentioneeID {
		t.Fatalf("update index rows = %v, want [%s] (watcher removed, mentionee added)", got, mentioneeID)
	}
	// Edit to no mentions clears the rows entirely.
	if _, err := UpdateComment(ctx, pool, wsSlug, ident, issueID, c.ID, actorID,
		tiptapComment("never mind, no mentions")); err != nil {
		t.Fatalf("UpdateComment clear: %v", err)
	}
	if got := mentionUserIDs(t, pool, c.ID); len(got) != 0 {
		t.Fatalf("cleared index rows = %v, want empty", got)
	}
}

func TestCommentDeleteRemovesMentionIndex(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	actorID, _, mentioneeID, wsSlug, ident, issueID := createMentionFixture(t, pool)

	first, err := CreateComment(ctx, pool, wsSlug, ident, issueID, actorID,
		tiptapComment("hey @Mentee Galih one"), nil)
	if err != nil {
		t.Fatalf("CreateComment first: %v", err)
	}
	second, err := CreateComment(ctx, pool, wsSlug, ident, issueID, actorID,
		tiptapComment("hey @Mentee Galih two"), nil)
	if err != nil {
		t.Fatalf("CreateComment second: %v", err)
	}
	if got := mentionUserIDs(t, pool, first.ID); len(got) != 1 || got[0] != mentioneeID {
		t.Fatalf("first index rows = %v, want [%s]", got, mentioneeID)
	}
	if got := mentionUserIDs(t, pool, second.ID); len(got) != 1 || got[0] != mentioneeID {
		t.Fatalf("second index rows = %v, want [%s]", got, mentioneeID)
	}

	// Deleting the first comment removes ONLY its rows — the
	// second comment mentioning the same user survives (the
	// per-comment grain: no cross-comment recount).
	if err := DeleteComment(ctx, pool, wsSlug, ident, issueID, first.ID, actorID); err != nil {
		t.Fatalf("DeleteComment: %v", err)
	}
	if got := mentionUserIDs(t, pool, first.ID); len(got) != 0 {
		t.Fatalf("deleted comment index rows = %v, want empty", got)
	}
	if got := mentionUserIDs(t, pool, second.ID); len(got) != 1 || got[0] != mentioneeID {
		t.Fatalf("surviving comment index rows = %v, want [%s]", got, mentioneeID)
	}
	// A second delete is still ErrCommentNotFound.
	if err := DeleteComment(ctx, pool, wsSlug, ident, issueID, first.ID, actorID); !errors.Is(err, ErrCommentNotFound) {
		t.Fatalf("second DeleteComment err = %v, want ErrCommentNotFound", err)
	}
}
