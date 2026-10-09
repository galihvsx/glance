package service

// Activity feed (C5T7) service tests: seeded issue_activities audit rows
// surface as exact newest-first feed rows, the limit contract holds,
// guests are forbidden, soft-deleted issues drop out of the feed.
// Real test database, no skips.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// setupActivityFeed seeds a workspace + project with two issues and four
// audit rows with explicit created_at backdates (rows written inside one
// test share the same wall-clock microsecond, so the timestamps are
// pinned). Audit timeline, oldest first:
//
//	A._created (T-3h), A.priority 0→2 (T-2h), A.state_id →done (T-1h),
//	B._created (T).
//
// Returns slug, identifier, member actor id, issue A, issue B.
func setupActivityFeed(t *testing.T, pool *pgxpool.Pool) (slug, ident, actor string, a, b *Issue) {
	t.Helper()
	ctx := context.Background()
	actor = createTestUser(t, pool, uniqueTestEmail("activity"))
	// Named actor: the feed shows the name, not the email.
	if _, err := pool.Exec(ctx, `UPDATE users SET name = 'Ada Member' WHERE id = $1::uuid`, actor); err != nil {
		t.Fatalf("name actor: %v", err)
	}
	slug = uniqueTestSlug("activity-ws")
	createTestWorkspace(t, pool, "Activity Co", slug, actor)
	ident = uniqueTestIdentifier()
	proj := createTestProject(t, pool, slug, actor, "Eng", ident)

	a = createTestIssue(t, pool, slug, ident, actor, "First issue")
	b = createTestIssue(t, pool, slug, ident, actor, "Second issue")

	doneState := stateIDByGroup(t, pool, proj.ID, "completed")
	pri := 2
	if _, err := UpdateIssue(ctx, pool, slug, ident, a.ID, actor, IssuePatch{Priority: &pri}); err != nil {
		t.Fatalf("bump priority: %v", err)
	}
	if _, err := UpdateIssue(ctx, pool, slug, ident, a.ID, actor, IssuePatch{StateID: &doneState}); err != nil {
		t.Fatalf("complete A: %v", err)
	}

	now := time.Now().UTC()
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
	pin(b.ID, "_created", now)
	return slug, ident, actor, a, b
}

func TestGetProjectActivityFeedRows(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	slug, ident, actor, a, b := setupActivityFeed(t, pool)

	entries, err := GetProjectActivity(ctx, pool, slug, ident, actor, ActivityDefaultLimit)
	if err != nil {
		t.Fatalf("GetProjectActivity: %v", err)
	}
	if len(entries) != 4 {
		t.Fatalf("entries = %d, want 4", len(entries))
	}
	// Newest first: B._created, A.state_id, A.priority, A._created.
	wantFields := []string{"_created", "state_id", "priority", "_created"}
	wantIssues := []string{b.ID, a.ID, a.ID, a.ID}
	for i, want := range wantFields {
		if entries[i].Field != want {
			t.Fatalf("entries[%d].field = %q, want %q", i, entries[i].Field, want)
		}
		if entries[i].IssueUUID != wantIssues[i] {
			t.Fatalf("entries[%d].issue_uuid = %q, want %q", i, entries[i].IssueUUID, wantIssues[i])
		}
	}
	// Strictly descending timestamps.
	for i := 1; i < len(entries); i++ {
		if !entries[i].At.Before(entries[i-1].At) {
			t.Fatalf("entries not newest-first at %d", i)
		}
	}
	// Display identifiers: IDENT-1 / IDENT-2.
	if entries[0].IssueIdentifier != ident+"-2" {
		t.Fatalf("B identifier = %q, want %q", entries[0].IssueIdentifier, ident+"-2")
	}
	if entries[3].IssueIdentifier != ident+"-1" {
		t.Fatalf("A identifier = %q, want %q", entries[3].IssueIdentifier, ident+"-1")
	}
	// Actor is the display name, not the email.
	for i, e := range entries {
		if e.Actor != "Ada Member" {
			t.Fatalf("entries[%d].actor = %q, want display name", i, e.Actor)
		}
	}
	// Priority change row: old 0 → new 2 (raw JSONB text).
	pri := entries[2]
	if pri.Old == nil || *pri.Old != "0" || pri.New == nil || *pri.New != "2" {
		t.Fatalf("priority row old/new = %v/%v, want 0/2", strOrNil(pri.Old), strOrNil(pri.New))
	}
	// _created row: old NULL, new carries the creation snapshot.
	created := entries[3]
	if created.Old != nil {
		t.Fatalf("_created old = %q, want NULL", *created.Old)
	}
	if created.New == nil || !strings.Contains(*created.New, "First issue") {
		t.Fatalf("_created new missing snapshot: %v", strOrNil(created.New))
	}
}

func TestGetProjectActivityLimit(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	slug, ident, actor, _, _ := setupActivityFeed(t, pool)

	entries, err := GetProjectActivity(ctx, pool, slug, ident, actor, 2)
	if err != nil {
		t.Fatalf("limit 2: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("limit 2 → %d rows, want 2", len(entries))
	}
	if entries[0].Field != "_created" || entries[1].Field != "state_id" {
		t.Fatalf("limit 2 order wrong: %q, %q", entries[0].Field, entries[1].Field)
	}

	for _, bad := range []int{0, -1, ActivityMaxLimit + 1} {
		if _, err := GetProjectActivity(ctx, pool, slug, ident, actor, bad); !errors.Is(err, ErrInvalidActivityLimit) {
			t.Fatalf("limit %d: err = %v, want ErrInvalidActivityLimit", bad, err)
		}
	}
}

func TestGetProjectActivityGuestForbidden(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	slug, ident, actor, _, _ := setupActivityFeed(t, pool)

	// Add a guest (role 5) to the workspace directly.
	var wsID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM workspaces WHERE slug = $1`, slug).Scan(&wsID); err != nil {
		t.Fatalf("workspace id: %v", err)
	}
	guest := createTestUser(t, pool, uniqueTestEmail("activity-guest"))
	if _, err := pool.Exec(ctx,
		`INSERT INTO workspace_members (workspace_id, user_id, role) VALUES ($1::uuid, $2::uuid, 5)`,
		wsID, guest); err != nil {
		t.Fatalf("add guest: %v", err)
	}
	if _, err := GetProjectActivity(ctx, pool, slug, ident, guest, ActivityDefaultLimit); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest: err = %v, want ErrForbidden", err)
	}

	// Member (the creator, role 20) and a plain non-member.
	if _, err := GetProjectActivity(ctx, pool, slug, ident, actor, ActivityDefaultLimit); err != nil {
		t.Fatalf("member: %v", err)
	}
	stranger := createTestUser(t, pool, uniqueTestEmail("activity-stranger"))
	if _, err := GetProjectActivity(ctx, pool, slug, ident, stranger, ActivityDefaultLimit); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stranger: err = %v, want ErrNotFound", err)
	}
}

func TestGetProjectActivityExcludesDeletedIssues(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	slug, ident, actor, _, b := setupActivityFeed(t, pool)

	if err := DeleteIssue(ctx, pool, slug, ident, b.ID, actor); err != nil {
		t.Fatalf("delete B: %v", err)
	}
	entries, err := GetProjectActivity(ctx, pool, slug, ident, actor, ActivityDefaultLimit)
	if err != nil {
		t.Fatalf("GetProjectActivity: %v", err)
	}
	for _, e := range entries {
		if e.IssueUUID == b.ID {
			t.Fatalf("deleted issue %s still in feed", b.ID)
		}
	}
	if len(entries) != 3 {
		// B's _created and _deleted rows are gone with the soft delete;
		// A's 3 rows remain.
		t.Fatalf("entries = %d, want 3 (A's rows only)", len(entries))
	}
}

func strOrNil(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}
