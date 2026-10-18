package service

// Trash API core (C52T0): ListDeletedIssues (deleted-only, deleted_at
// DESC, cursor pagination) + RestoreIssue (clear deleted_at + restore
// trail: _restored activity, version snapshot, issue.updated webhook +
// WS announce) with state/parent re-validation and role gates. All
// tests run against the real test database — no skips.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func c52BoolPtr(b bool) *bool { return &b }

// trashFixture builds a workspace + project + one issue, then deletes
// the issue, returning everything the trash tests need.
func trashFixture(t *testing.T, pool *pgxpool.Pool, prefix string) (creator, wsSlug, ident string, iss *Issue) {
	t.Helper()
	ctx := context.Background()
	creator = createTestUser(t, pool, uniqueTestEmail(prefix))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug(prefix), creator)
	p := createTestProject(t, pool, ws.Slug, creator, "Engineering", uniqueTestIdentifier())
	iss = createTestIssue(t, pool, ws.Slug, p.Identifier, creator, "Trash me")
	if err := DeleteIssue(ctx, pool, ws.Slug, p.Identifier, iss.ID, creator); err != nil {
		t.Fatalf("DeleteIssue fixture: %v", err)
	}
	return creator, ws.Slug, p.Identifier, iss
}

// trashRestoredPayloads returns the new_value payloads of every
// _restored activity row for an issue, in insertion order.
func trashRestoredPayloads(t *testing.T, pool *pgxpool.Pool, issueID string) []map[string]any {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT new_value FROM issue_activities
		  WHERE issue_id = $1::uuid AND field = '_restored' ORDER BY created_at, id`,
		issueID)
	if err != nil {
		t.Fatalf("read _restored activity: %v", err)
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			t.Fatalf("scan _restored activity: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("unmarshal _restored payload: %v", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("_restored activity rows: %v", err)
	}
	return out
}

// trashIssueStillDeleted reports whether the raw row still carries a
// deleted_at (bypasses the live-only read paths).
func trashIssueStillDeleted(t *testing.T, pool *pgxpool.Pool, issueID string) bool {
	t.Helper()
	var deleted bool
	if err := pool.QueryRow(context.Background(),
		`SELECT deleted_at IS NOT NULL FROM issues WHERE id = $1::uuid`,
		issueID).Scan(&deleted); err != nil {
		t.Fatalf("raw deleted_at check: %v", err)
	}
	return deleted
}

// TestListDeletedIssuesDeletedOnly: the trash list returns deleted
// rows only — live rows and archived-but-live rows are excluded —
// with DeletedAt populated, most recently deleted first.
func TestListDeletedIssuesDeletedOnly(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	creator := createTestUser(t, pool, uniqueTestEmail("trash-list"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("trash-list"), creator)
	p := createTestProject(t, pool, ws.Slug, creator, "Engineering", uniqueTestIdentifier())

	live := createTestIssue(t, pool, ws.Slug, p.Identifier, creator, "Live")
	archivedLive := createTestIssue(t, pool, ws.Slug, p.Identifier, creator, "Archived live")
	if _, err := UpdateIssue(ctx, pool, ws.Slug, p.Identifier, archivedLive.ID, creator,
		IssuePatch{Archived: c52BoolPtr(true)}); err != nil {
		t.Fatalf("archive live issue: %v", err)
	}
	oldDeleted := createTestIssue(t, pool, ws.Slug, p.Identifier, creator, "Old deleted")
	newDeleted := createTestIssue(t, pool, ws.Slug, p.Identifier, creator, "New deleted")
	archivedDeleted := createTestIssue(t, pool, ws.Slug, p.Identifier, creator, "Archived deleted")
	if _, err := UpdateIssue(ctx, pool, ws.Slug, p.Identifier, archivedDeleted.ID, creator,
		IssuePatch{Archived: c52BoolPtr(true)}); err != nil {
		t.Fatalf("archive to-be-deleted issue: %v", err)
	}
	for _, iss := range []*Issue{oldDeleted, newDeleted, archivedDeleted} {
		if err := DeleteIssue(ctx, pool, ws.Slug, p.Identifier, iss.ID, creator); err != nil {
			t.Fatalf("DeleteIssue %s: %v", iss.ID, err)
		}
	}
	// Pin distinct deleted_at values so the recency order is
	// deterministic (two back-to-back deletes could otherwise tie).
	if _, err := pool.Exec(ctx,
		`UPDATE issues SET deleted_at = now() - interval '2 hours' WHERE id = $1::uuid`, oldDeleted.ID); err != nil {
		t.Fatalf("pin old deleted_at: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE issues SET deleted_at = now() - interval '1 hour' WHERE id = $1::uuid`, archivedDeleted.ID); err != nil {
		t.Fatalf("pin archived deleted_at: %v", err)
	}

	res, err := ListDeletedIssues(ctx, pool, ws.Slug, p.Identifier, creator, "", 0)
	if err != nil {
		t.Fatalf("ListDeletedIssues: %v", err)
	}
	// New deleted (now) > archived deleted (1h ago) > old deleted (2h
	// ago). An archived+deleted row IS in trash: trash keys on
	// deleted_at, not archived_at.
	wantOrder := []string{newDeleted.ID, archivedDeleted.ID, oldDeleted.ID}
	if len(res.Issues) != len(wantOrder) {
		t.Fatalf("trash rows = %d, want %d", len(res.Issues), len(wantOrder))
	}
	for i, want := range wantOrder {
		got := res.Issues[i]
		if got.ID != want {
			t.Fatalf("trash[%d].id = %s, want %s (order: most recently deleted first)", i, got.ID, want)
		}
		if got.DeletedAt == nil {
			t.Fatalf("trash[%d] DeletedAt = nil, want the soft-delete timestamp", i)
		}
	}
	for _, excluded := range []*Issue{live, archivedLive} {
		for _, got := range res.Issues {
			if got.ID == excluded.ID {
				t.Fatalf("trash contains %q (%s), want deleted-only", excluded.Name, excluded.ID)
			}
		}
	}
	if res.NextCursor != "" {
		t.Fatalf("NextCursor = %q, want empty (all rows fit one page)", res.NextCursor)
	}
}

// TestListDeletedIssuesPagination: cursor pagination pages through
// the trash in deleted_at DESC order with no dupes or skips.
func TestListDeletedIssuesPagination(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	creator := createTestUser(t, pool, uniqueTestEmail("trash-page"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("trash-page"), creator)
	p := createTestProject(t, pool, ws.Slug, creator, "Engineering", uniqueTestIdentifier())

	var ids []string
	for i := 0; i < 3; i++ {
		iss := createTestIssue(t, pool, ws.Slug, p.Identifier, creator, "Deleted")
		if err := DeleteIssue(ctx, pool, ws.Slug, p.Identifier, iss.ID, creator); err != nil {
			t.Fatalf("DeleteIssue: %v", err)
		}
		// Stagger deleted_at so page order is deterministic.
		if _, err := pool.Exec(ctx,
			`UPDATE issues SET deleted_at = now() - make_interval(mins => $1) WHERE id = $2::uuid`,
			2-i, iss.ID); err != nil {
			t.Fatalf("pin deleted_at: %v", err)
		}
		ids = append(ids, iss.ID)
	}
	// ids[2] deleted "now" (0 min ago) sorts first, then ids[1], ids[0].
	wantOrder := []string{ids[2], ids[1], ids[0]}

	page1, err := ListDeletedIssues(ctx, pool, ws.Slug, p.Identifier, creator, "", 2)
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if len(page1.Issues) != 2 || page1.NextCursor == "" {
		t.Fatalf("page 1: rows = %d cursor = %q, want 2 rows + cursor", len(page1.Issues), page1.NextCursor)
	}
	if page1.Issues[0].ID != wantOrder[0] || page1.Issues[1].ID != wantOrder[1] {
		t.Fatalf("page 1 ids = [%s %s], want [%s %s]",
			page1.Issues[0].ID, page1.Issues[1].ID, wantOrder[0], wantOrder[1])
	}
	page2, err := ListDeletedIssues(ctx, pool, ws.Slug, p.Identifier, creator, page1.NextCursor, 2)
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if len(page2.Issues) != 1 || page2.Issues[0].ID != wantOrder[2] {
		t.Fatalf("page 2 ids = %v, want [%s]", page2.Issues[0].ID, wantOrder[2])
	}
	if page2.NextCursor != "" {
		t.Fatalf("page 2 NextCursor = %q, want empty (last page)", page2.NextCursor)
	}
}

// TestListDeletedIssuesBadCursor: a garbage cursor and a cursor
// minted for another order are both ErrInvalidCursor.
func TestListDeletedIssuesBadCursor(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	creator, wsSlug, ident, _ := trashFixture(t, pool, "trash-badcur")

	for _, cur := range []string{"not-a-cursor", encodeListCursor("-updated_at", "2026-01-01T00:00:00Z", "00000000-0000-0000-0000-000000000000")} {
		if _, err := ListDeletedIssues(ctx, pool, wsSlug, ident, creator, cur, 0); !errors.Is(err, ErrInvalidCursor) {
			t.Fatalf("cursor %q: err = %v, want ErrInvalidCursor", cur, err)
		}
	}
}

// TestListDeletedIssuesRoles: guests may read trash (same rule as
// the normal list); non-members get ErrNotFound.
func TestListDeletedIssuesRoles(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	creator, wsSlug, ident, iss := trashFixture(t, pool, "trash-roles")

	guest := createTestUser(t, pool, uniqueTestEmail("trash-roles-guest"))
	if err := UpsertMember(ctx, pool, wsSlug, creator, guest, RoleGuest); err != nil {
		t.Fatalf("UpsertMember guest: %v", err)
	}
	res, err := ListDeletedIssues(ctx, pool, wsSlug, ident, guest, "", 0)
	if err != nil {
		t.Fatalf("guest trash list: %v, want the list rule (guests may read)", err)
	}
	if len(res.Issues) != 1 || res.Issues[0].ID != iss.ID {
		t.Fatalf("guest trash rows = %v, want [%s]", res.Issues, iss.ID)
	}

	outsider := createTestUser(t, pool, uniqueTestEmail("trash-roles-out"))
	if _, err := ListDeletedIssues(ctx, pool, wsSlug, ident, outsider, "", 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("outsider trash list: err = %v, want ErrNotFound", err)
	}
}

// TestRestoreIssueClearsDeletedAtAndWritesTrail: restore clears
// deleted_at, writes one _restored row ({"restored_at": ...}), one
// issue.updated webhook row (changed_fields ["deleted_at"]), announces
// issue.updated, and the issue reappears in the normal list. No version
// snapshot is written: the restored row's content is identical to the
// delete-time version (C64T1 no-op snapshot guard).
func TestRestoreIssueClearsDeletedAtAndWritesTrail(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	creator := createTestUser(t, pool, uniqueTestEmail("trash-restore"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("trash-restore"), creator)
	p := createTestProject(t, pool, ws.Slug, creator, "Engineering", uniqueTestIdentifier())
	iss := createTestIssue(t, pool, ws.Slug, p.Identifier, creator, "Restore me")
	if err := DeleteIssue(ctx, pool, ws.Slug, p.Identifier, iss.ID, creator); err != nil {
		t.Fatalf("DeleteIssue: %v", err)
	}
	versionsBefore := countVersions(t, pool, iss.ID)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	wh, err := CreateWebhook(ctx, pool, ws.Slug, creator, WebhookInput{
		URL: srv.URL, Secret: strPtr2("s3cret"),
		Events: []string{EventIssueUpdated},
	})
	if err != nil {
		t.Fatalf("CreateWebhook: %v", err)
	}
	watermark := outboxWatermark(t, pool)
	fake := installCapturingBroadcaster(t)

	restored, err := RestoreIssue(ctx, pool, ws.Slug, p.Identifier, iss.ID, creator)
	if err != nil {
		t.Fatalf("RestoreIssue: %v", err)
	}
	if restored.ID != iss.ID || restored.DisplayID != p.Identifier+"-"+strconv.Itoa(restored.SequenceID) {
		t.Fatalf("restored = %+v, want id %s display %s", restored, iss.ID, p.Identifier+"-1")
	}
	if trashIssueStillDeleted(t, pool, iss.ID) {
		t.Fatalf("issue still has deleted_at after restore")
	}
	if _, err := GetIssue(ctx, pool, ws.Slug, p.Identifier, iss.ID, creator); err != nil {
		t.Fatalf("GetIssue after restore: %v, want the live row", err)
	}
	// Normal list contains it again.
	list, err := ListIssues(ctx, pool, ws.Slug, p.Identifier, creator, ListIssuesInput{})
	if err != nil {
		t.Fatalf("ListIssues after restore: %v", err)
	}
	found := false
	for _, it := range list.Issues {
		if it.ID == iss.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("restored issue missing from the normal list")
	}

	payloads := trashRestoredPayloads(t, pool, iss.ID)
	if len(payloads) != 1 {
		t.Fatalf("_restored rows = %d, want 1", len(payloads))
	}
	if _, ok := payloads[0]["restored_at"]; !ok {
		t.Fatalf("_restored payload = %v, want a restored_at key", payloads[0])
	}
	if n := countVersions(t, pool, iss.ID); n != versionsBefore {
		t.Fatalf("version rows = %d, want %d (restore with identical content snapshots nothing — C64T1)", n, versionsBefore)
	}
	assertIssueUpdatedIDs(t, fake, iss.ID)

	updated := webhookOutboxRows(t, pool, watermark, wh.ID, EventIssueUpdated)
	if len(updated) != 1 {
		t.Fatalf("issue.updated webhook rows = %d, want exactly 1", len(updated))
	}
	got := updated[0]
	if got.Data.ID != iss.ID {
		t.Fatalf("webhook data.id = %q, want %q", got.Data.ID, iss.ID)
	}
	if len(got.Data.ChangedFields) != 1 || got.Data.ChangedFields[0] != "deleted_at" {
		t.Fatalf("webhook changed_fields = %v, want [deleted_at]", got.Data.ChangedFields)
	}
	if got.Data.ActorID != creator {
		t.Fatalf("webhook actor_id = %q, want %q", got.Data.ActorID, creator)
	}
}

// TestRestoreLiveIssueNotFound: restoring a live issue reads as
// "not in trash" (404), exactly as a second DELETE does.
func TestRestoreLiveIssueNotFound(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	creator := createTestUser(t, pool, uniqueTestEmail("trash-live"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("trash-live"), creator)
	p := createTestProject(t, pool, ws.Slug, creator, "Engineering", uniqueTestIdentifier())
	iss := createTestIssue(t, pool, ws.Slug, p.Identifier, creator, "Live")

	if _, err := RestoreIssue(ctx, pool, ws.Slug, p.Identifier, iss.ID, creator); !errors.Is(err, ErrIssueNotFound) {
		t.Fatalf("restore live: err = %v, want ErrIssueNotFound", err)
	}
	if n := countActivities(t, pool, iss.ID, "_restored"); n != 0 {
		t.Fatalf("live restore wrote %d _restored rows, want 0", n)
	}
}

// TestRestoreSecondRestoreNotFound: the second restore of the same
// issue is a 404 — the first restore already took it out of trash.
func TestRestoreSecondRestoreNotFound(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	creator, wsSlug, ident, iss := trashFixture(t, pool, "trash-twice")

	if _, err := RestoreIssue(ctx, pool, wsSlug, ident, iss.ID, creator); err != nil {
		t.Fatalf("first restore: %v", err)
	}
	if _, err := RestoreIssue(ctx, pool, wsSlug, ident, iss.ID, creator); !errors.Is(err, ErrIssueNotFound) {
		t.Fatalf("second restore: err = %v, want ErrIssueNotFound", err)
	}
	if n := countActivities(t, pool, iss.ID, "_restored"); n != 1 {
		t.Fatalf("_restored rows = %d, want exactly 1", n)
	}
}

// TestRestoreGuestForbidden: guests may read trash but not restore.
func TestRestoreGuestForbidden(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	creator, wsSlug, ident, iss := trashFixture(t, pool, "trash-guest")

	guest := createTestUser(t, pool, uniqueTestEmail("trash-guest-g"))
	if err := UpsertMember(ctx, pool, wsSlug, creator, guest, RoleGuest); err != nil {
		t.Fatalf("UpsertMember guest: %v", err)
	}
	if _, err := RestoreIssue(ctx, pool, wsSlug, ident, iss.ID, guest); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest restore: err = %v, want ErrForbidden", err)
	}
	if !trashIssueStillDeleted(t, pool, iss.ID) {
		t.Fatalf("guest restore cleared deleted_at")
	}
}

// TestRestoreDeletedParentClearsParent: the issue is deleted while
// still linked to its parent, then the parent is deleted (the
// soft-deleted child keeps its link — C51T1 ghost rule). Restoring
// the child clears the stale parent with a parent_id activity row
// (old = stale parent, new = NULL) and a parent_id webhook change.
func TestRestoreDeletedParentClearsParent(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	creator := createTestUser(t, pool, uniqueTestEmail("trash-parent"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("trash-parent"), creator)
	p := createTestProject(t, pool, ws.Slug, creator, "Engineering", uniqueTestIdentifier())
	parent := createTestIssue(t, pool, ws.Slug, p.Identifier, creator, "Parent")
	child, err := CreateIssue(ctx, pool, ws.Slug, p.Identifier, creator,
		CreateIssueInput{Name: "Child", ParentID: &parent.ID})
	if err != nil {
		t.Fatalf("CreateIssue child: %v", err)
	}
	// Delete the child first (link intact), then the parent: the
	// ghost child keeps its parent_id.
	if err := DeleteIssue(ctx, pool, ws.Slug, p.Identifier, child.ID, creator); err != nil {
		t.Fatalf("DeleteIssue child: %v", err)
	}
	if err := DeleteIssue(ctx, pool, ws.Slug, p.Identifier, parent.ID, creator); err != nil {
		t.Fatalf("DeleteIssue parent: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	wh, err := CreateWebhook(ctx, pool, ws.Slug, creator, WebhookInput{
		URL: srv.URL, Secret: strPtr2("s3cret"),
		Events: []string{EventIssueUpdated},
	})
	if err != nil {
		t.Fatalf("CreateWebhook: %v", err)
	}
	watermark := outboxWatermark(t, pool)

	restored, err := RestoreIssue(ctx, pool, ws.Slug, p.Identifier, child.ID, creator)
	if err != nil {
		t.Fatalf("RestoreIssue child: %v", err)
	}
	if restored.ParentID != nil {
		t.Fatalf("restored ParentID = %v, want nil (parent was deleted)", *restored.ParentID)
	}
	pairs := parentIDActivityPairs(t, pool, child.ID)
	if len(pairs) != 1 || pairs[0].Old != parent.ID || pairs[0].New != nil {
		t.Fatalf("parent_id activity = %+v, want [{old=%s new=NULL}]", pairs, parent.ID)
	}
	updated := webhookOutboxRows(t, pool, watermark, wh.ID, EventIssueUpdated)
	if len(updated) != 1 {
		t.Fatalf("issue.updated webhook rows = %d, want exactly 1", len(updated))
	}
	fields := map[string]bool{}
	for _, f := range updated[0].Data.ChangedFields {
		fields[f] = true
	}
	if !fields["deleted_at"] || !fields["parent_id"] {
		t.Fatalf("webhook changed_fields = %v, want [deleted_at parent_id]", updated[0].Data.ChangedFields)
	}
}

// TestRestoreLiveParentKept: a deleted child whose parent is still
// live keeps its parent link on restore, with no parent_id trail.
func TestRestoreLiveParentKept(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	creator := createTestUser(t, pool, uniqueTestEmail("trash-liveparent"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("trash-liveparent"), creator)
	p := createTestProject(t, pool, ws.Slug, creator, "Engineering", uniqueTestIdentifier())
	parent := createTestIssue(t, pool, ws.Slug, p.Identifier, creator, "Parent")
	child, err := CreateIssue(ctx, pool, ws.Slug, p.Identifier, creator,
		CreateIssueInput{Name: "Child", ParentID: &parent.ID})
	if err != nil {
		t.Fatalf("CreateIssue child: %v", err)
	}
	if err := DeleteIssue(ctx, pool, ws.Slug, p.Identifier, child.ID, creator); err != nil {
		t.Fatalf("DeleteIssue child: %v", err)
	}

	restored, err := RestoreIssue(ctx, pool, ws.Slug, p.Identifier, child.ID, creator)
	if err != nil {
		t.Fatalf("RestoreIssue child: %v", err)
	}
	if restored.ParentID == nil || *restored.ParentID != parent.ID {
		t.Fatalf("restored ParentID = %v, want %s (live parent kept)", restored.ParentID, parent.ID)
	}
	if pairs := parentIDActivityPairs(t, pool, child.ID); len(pairs) != 0 {
		t.Fatalf("parent_id activity rows = %d, want 0 (parent stayed live)", len(pairs))
	}
}

// TestRestoreCrossProjectStateRejected: an issue whose state_id names
// a state of ANOTHER project (a corrupt/orphaned reference — the
// issues.state_id FK makes a truly deleted state unconstructible,
// and DeleteState reassigns every referencing issue first) rejects
// the restore with ErrInvalidState, and the rollback leaves the
// issue still deleted with no trail.
func TestRestoreCrossProjectStateRejected(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	creator := createTestUser(t, pool, uniqueTestEmail("trash-badstate"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("trash-badstate"), creator)
	pA := createTestProject(t, pool, ws.Slug, creator, "Engineering", uniqueTestIdentifier())
	pB := createTestProject(t, pool, ws.Slug, creator, "Other", uniqueTestIdentifier())
	iss := createTestIssue(t, pool, ws.Slug, pA.Identifier, creator, "Bad state")
	if err := DeleteIssue(ctx, pool, ws.Slug, pA.Identifier, iss.ID, creator); err != nil {
		t.Fatalf("DeleteIssue: %v", err)
	}
	// Point the deleted issue at a state of project B: FK-legal (the
	// state row exists) but foreign to this project.
	statesB, err := ListStates(ctx, pool, ws.Slug, pB.Identifier, creator)
	if err != nil {
		t.Fatalf("ListStates B: %v", err)
	}
	if len(statesB) == 0 {
		t.Fatalf("project B has no states")
	}
	if _, err := pool.Exec(ctx,
		`UPDATE issues SET state_id = $1::uuid WHERE id = $2::uuid`, statesB[0].ID, iss.ID); err != nil {
		t.Fatalf("point issue at foreign state: %v", err)
	}

	versionsBefore := countVersions(t, pool, iss.ID)
	if _, err := RestoreIssue(ctx, pool, ws.Slug, pA.Identifier, iss.ID, creator); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("restore with foreign state: err = %v, want ErrInvalidState", err)
	}
	// Atomicity: the failed restore rolled back — still deleted, no
	// trail, no new version.
	if !trashIssueStillDeleted(t, pool, iss.ID) {
		t.Fatalf("failed restore cleared deleted_at")
	}
	if n := countActivities(t, pool, iss.ID, "_restored"); n != 0 {
		t.Fatalf("failed restore wrote %d _restored rows, want 0", n)
	}
	if n := countVersions(t, pool, iss.ID); n != versionsBefore {
		t.Fatalf("failed restore wrote versions: %d, want %d", n, versionsBefore)
	}
}

// TestRestoreAfterStateReassignSucceeds: the real-world deleted-state
// path — DeleteState with reassign_to moves the soft-deleted issue to
// the surviving state, and the restore then succeeds on that state.
func TestRestoreAfterStateReassignSucceeds(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	creator := createTestUser(t, pool, uniqueTestEmail("trash-reassign"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("trash-reassign"), creator)
	p := createTestProject(t, pool, ws.Slug, creator, "Engineering", uniqueTestIdentifier())

	oldState := backlogStateID(t, pool, ws.Slug, p.Identifier, creator)
	survivor, err := CreateState(ctx, pool, ws.Slug, p.Identifier, creator,
		StateInput{Name: "Survivor", Group: "backlog"})
	if err != nil {
		t.Fatalf("CreateState: %v", err)
	}
	iss := createTestIssue(t, pool, ws.Slug, p.Identifier, creator, "Reassigned")
	if iss.StateID != oldState {
		t.Fatalf("issue state = %s, want backlog %s", iss.StateID, oldState)
	}
	if err := DeleteIssue(ctx, pool, ws.Slug, p.Identifier, iss.ID, creator); err != nil {
		t.Fatalf("DeleteIssue: %v", err)
	}
	if err := DeleteState(ctx, pool, ws.Slug, p.Identifier, oldState, creator, &survivor.ID); err != nil {
		t.Fatalf("DeleteState with reassign: %v", err)
	}

	restored, err := RestoreIssue(ctx, pool, ws.Slug, p.Identifier, iss.ID, creator)
	if err != nil {
		t.Fatalf("RestoreIssue after reassign: %v", err)
	}
	if restored.StateID != survivor.ID {
		t.Fatalf("restored state = %s, want reassigned %s", restored.StateID, survivor.ID)
	}
}
