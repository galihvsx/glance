package service

// Bulk restore (C52T1): BulkRestoreIssues mirrors BulkDeleteIssues
// (per-item savepoint semantics, maxBulkItems cap, empty-ids error,
// per-item failures) with the full C52T0 trail per restored item, plus
// edge hardening: restore → re-delete round-trip, detached children
// stay detached, archived project does not block restore, deleted
// project → 404 at project resolution. All tests run against the real
// test database — no skips.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// bulkRestoreErrs maps result ids to their per-item error strings
// ("" when the item succeeded), for compact assertions.
func bulkRestoreErrs(t *testing.T, results []BulkItemResult) map[string]string {
	t.Helper()
	out := make(map[string]string, len(results))
	for _, r := range results {
		if r.Error != nil {
			out[r.ID] = *r.Error
		} else {
			out[r.ID] = ""
		}
	}
	return out
}

// TestBulkRestorePartial: deleted ids restore; a duplicate id restores
// once (the repeat is a per-item "issue not found"); live, unknown, and
// malformed ids are per-item failures, not a whole-batch failure.
func TestBulkRestorePartial(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	creator := createTestUser(t, pool, uniqueTestEmail("bulk-restore-part"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("bulk-restore-part"), creator)
	p := createTestProject(t, pool, ws.Slug, creator, "Engineering", uniqueTestIdentifier())

	del := createTestIssue(t, pool, ws.Slug, p.Identifier, creator, "Deleted")
	live := createTestIssue(t, pool, ws.Slug, p.Identifier, creator, "Live")
	if err := DeleteIssue(ctx, pool, ws.Slug, p.Identifier, del.ID, creator); err != nil {
		t.Fatalf("DeleteIssue: %v", err)
	}
	unknown := "00000000-0000-0000-0000-000000000000"

	results, err := BulkRestoreIssues(ctx, pool, ws.Slug, p.Identifier, creator,
		[]string{del.ID, del.ID, live.ID, unknown, "not-a-uuid"})
	if err != nil {
		t.Fatalf("BulkRestoreIssues: %v", err)
	}
	if len(results) != 5 {
		t.Fatalf("results = %d, want 5", len(results))
	}
	if !results[0].OK || results[0].Error != nil {
		t.Fatalf("results[0] = %+v, want ok", results[0])
	}
	for i, want := range []string{"issue not found", "issue not found", "issue not found", "issue not found"} {
		got := results[i+1]
		if got.OK || got.Error == nil || *got.Error != want {
			t.Fatalf("results[%d] = %+v, want failure %q", i+1, got, want)
		}
	}
	if trashIssueStillDeleted(t, pool, del.ID) {
		t.Fatalf("restored issue still has deleted_at")
	}
	if n := countActivities(t, pool, del.ID, "_restored"); n != 1 {
		t.Fatalf("_restored rows = %d, want exactly 1 (duplicate id restored once)", n)
	}
}

// TestBulkRestoreValidation: empty ids and oversized batches fail at
// the whole-call level; guests get ErrForbidden and nothing is restored.
func TestBulkRestoreValidation(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	creator, wsSlug, ident, iss := trashFixture(t, pool, "bulk-restore-val")

	if _, err := BulkRestoreIssues(ctx, pool, wsSlug, ident, creator, nil); !errors.Is(err, ErrBulkEmptyIDs) {
		t.Fatalf("empty ids: err = %v, want ErrBulkEmptyIDs", err)
	}
	ids := make([]string, maxBulkItems+1)
	for i := range ids {
		ids[i] = "00000000-0000-0000-0000-000000000000"
	}
	if _, err := BulkRestoreIssues(ctx, pool, wsSlug, ident, creator, ids); !errors.Is(err, ErrBulkTooManyIDs) {
		t.Fatalf("oversized batch: err = %v, want ErrBulkTooManyIDs", err)
	}

	guest := createTestUser(t, pool, uniqueTestEmail("bulk-restore-val-g"))
	if err := UpsertMember(ctx, pool, wsSlug, creator, guest, RoleGuest); err != nil {
		t.Fatalf("UpsertMember guest: %v", err)
	}
	if _, err := BulkRestoreIssues(ctx, pool, wsSlug, ident, guest, []string{iss.ID}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest bulk restore: err = %v, want ErrForbidden", err)
	}
	if !trashIssueStillDeleted(t, pool, iss.ID) {
		t.Fatalf("guest bulk restore cleared deleted_at")
	}
}

// TestBulkRestoreTrail: each restored item gets one _restored row,
// one issue.updated webhook row (changed_fields [deleted_at]), and
// one WS issue.updated announce. No version snapshot is written: the
// restored rows' content is identical to their delete-time versions
// (C64T1 no-op snapshot guard).
func TestBulkRestoreTrail(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	creator := createTestUser(t, pool, uniqueTestEmail("bulk-restore-trail"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("bulk-restore-trail"), creator)
	p := createTestProject(t, pool, ws.Slug, creator, "Engineering", uniqueTestIdentifier())

	var deleted []*Issue
	for _, name := range []string{"One", "Two"} {
		iss := createTestIssue(t, pool, ws.Slug, p.Identifier, creator, name)
		if err := DeleteIssue(ctx, pool, ws.Slug, p.Identifier, iss.ID, creator); err != nil {
			t.Fatalf("DeleteIssue: %v", err)
		}
		deleted = append(deleted, iss)
	}
	versionsBefore := map[string]int{}
	for _, iss := range deleted {
		versionsBefore[iss.ID] = countVersions(t, pool, iss.ID)
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
	fake := installCapturingBroadcaster(t)

	ids := []string{deleted[0].ID, deleted[1].ID}
	results, err := BulkRestoreIssues(ctx, pool, ws.Slug, p.Identifier, creator, ids)
	if err != nil {
		t.Fatalf("BulkRestoreIssues: %v", err)
	}
	for _, r := range results {
		if !r.OK {
			t.Fatalf("result %+v, want ok", r)
		}
	}

	for _, iss := range deleted {
		if trashIssueStillDeleted(t, pool, iss.ID) {
			t.Fatalf("issue %s still has deleted_at after bulk restore", iss.ID)
		}
		if n := countActivities(t, pool, iss.ID, "_restored"); n != 1 {
			t.Fatalf("issue %s _restored rows = %d, want 1", iss.ID, n)
		}
		if n := countVersions(t, pool, iss.ID); n != versionsBefore[iss.ID] {
			t.Fatalf("issue %s versions = %d, want %d (restore with identical content snapshots nothing — C64T1)", iss.ID, n, versionsBefore[iss.ID])
		}
	}
	assertIssueUpdatedIDs(t, fake, ids...)

	rows := webhookOutboxRows(t, pool, watermark, wh.ID, EventIssueUpdated)
	if len(rows) != 2 {
		t.Fatalf("issue.updated webhook rows = %d, want 2 (one per restored item)", len(rows))
	}
	for _, row := range rows {
		if len(row.Data.ChangedFields) != 1 || row.Data.ChangedFields[0] != "deleted_at" {
			t.Fatalf("webhook changed_fields = %v, want [deleted_at]", row.Data.ChangedFields)
		}
	}
}

// TestBulkRestoreObjectiveFanOut: restored issues re-enter KR
// progress — one objective.updated per linked objective across the
// whole batch (DISTINCT), even when two restored issues link to the
// same objective.
func TestBulkRestoreObjectiveFanOut(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	creator := createTestUser(t, pool, uniqueTestEmail("bulk-restore-okr"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("bulk-restore-okr"), creator)
	p := createTestProject(t, pool, ws.Slug, creator, "Engineering", uniqueTestIdentifier())

	issA := createTestIssue(t, pool, ws.Slug, p.Identifier, creator, "A")
	issB := createTestIssue(t, pool, ws.Slug, p.Identifier, creator, "B")

	ob1, err := CreateObjective(ctx, pool, ws.Slug, creator, ObjectiveInput{Title: "One"})
	if err != nil {
		t.Fatalf("CreateObjective 1: %v", err)
	}
	kr1, err := CreateKeyResult(ctx, pool, ws.Slug, creator, ob1.ID, KeyResultInput{
		Title: "KR1", Kind: "issue", TargetValue: fptr(10),
	})
	if err != nil {
		t.Fatalf("CreateKeyResult 1: %v", err)
	}
	ob2, err := CreateObjective(ctx, pool, ws.Slug, creator, ObjectiveInput{Title: "Two"})
	if err != nil {
		t.Fatalf("CreateObjective 2: %v", err)
	}
	kr2, err := CreateKeyResult(ctx, pool, ws.Slug, creator, ob2.ID, KeyResultInput{
		Title: "KR2", Kind: "issue", TargetValue: fptr(10),
	})
	if err != nil {
		t.Fatalf("CreateKeyResult 2: %v", err)
	}
	// A and B both link to ob1; only B links to ob2.
	for _, iss := range []*Issue{issA, issB} {
		if err := LinkIssueToKeyResult(ctx, pool, ws.Slug, creator, ob1.ID, kr1.ID, iss.ID); err != nil {
			t.Fatalf("link %s to ob1: %v", iss.ID, err)
		}
	}
	if err := LinkIssueToKeyResult(ctx, pool, ws.Slug, creator, ob2.ID, kr2.ID, issB.ID); err != nil {
		t.Fatalf("link B to ob2: %v", err)
	}

	for _, iss := range []*Issue{issA, issB} {
		if err := DeleteIssue(ctx, pool, ws.Slug, p.Identifier, iss.ID, creator); err != nil {
			t.Fatalf("DeleteIssue: %v", err)
		}
	}
	fake := installCapturingBroadcaster(t)

	if _, err := BulkRestoreIssues(ctx, pool, ws.Slug, p.Identifier, creator,
		[]string{issA.ID, issB.ID}); err != nil {
		t.Fatalf("BulkRestoreIssues: %v", err)
	}
	assertIssueUpdatedIDs(t, fake, issA.ID, issB.ID)

	var objectiveIDs []string
	for _, ev := range fake.snapshot() {
		if ev.eventType != EventObjectiveUpdated {
			continue
		}
		payload, ok := ev.data.(map[string]string)
		if !ok {
			t.Fatalf("objective.updated payload type = %T, want map[string]string", ev.data)
		}
		objectiveIDs = append(objectiveIDs, payload["id"])
	}
	if len(objectiveIDs) != 2 {
		t.Fatalf("objective.updated events = %v, want exactly one per objective (ob1 once despite two linked issues)", objectiveIDs)
	}
	got := strings.Join(objectiveIDs, ",")
	if !strings.Contains(got, ob1.ID) || !strings.Contains(got, ob2.ID) {
		t.Fatalf("objective.updated ids = %v, want [%s %s]", objectiveIDs, ob1.ID, ob2.ID)
	}
}

// TestBulkRestoreRoundTrip: delete → restore → delete again works;
// each transition writes its own activity row + version, and the
// second _deleted row does not clobber the _restored row.
func TestBulkRestoreRoundTrip(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	creator := createTestUser(t, pool, uniqueTestEmail("bulk-restore-rt"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("bulk-restore-rt"), creator)
	p := createTestProject(t, pool, ws.Slug, creator, "Engineering", uniqueTestIdentifier())
	iss := createTestIssue(t, pool, ws.Slug, p.Identifier, creator, "Round trip")

	if err := DeleteIssue(ctx, pool, ws.Slug, p.Identifier, iss.ID, creator); err != nil {
		t.Fatalf("DeleteIssue 1: %v", err)
	}
	if _, err := BulkRestoreIssues(ctx, pool, ws.Slug, p.Identifier, creator, []string{iss.ID}); err != nil {
		t.Fatalf("BulkRestoreIssues 1: %v", err)
	}
	if trashIssueStillDeleted(t, pool, iss.ID) {
		t.Fatalf("issue still deleted after first restore")
	}
	if err := DeleteIssue(ctx, pool, ws.Slug, p.Identifier, iss.ID, creator); err != nil {
		t.Fatalf("DeleteIssue 2: %v", err)
	}
	if !trashIssueStillDeleted(t, pool, iss.ID) {
		t.Fatalf("issue live after second delete")
	}
	if _, err := BulkRestoreIssues(ctx, pool, ws.Slug, p.Identifier, creator, []string{iss.ID}); err != nil {
		t.Fatalf("BulkRestoreIssues 2: %v", err)
	}
	if trashIssueStillDeleted(t, pool, iss.ID) {
		t.Fatalf("issue still deleted after second restore")
	}
	if n := countActivities(t, pool, iss.ID, "_deleted"); n != 2 {
		t.Fatalf("_deleted rows = %d, want 2 (one per delete)", n)
	}
	if n := countActivities(t, pool, iss.ID, "_restored"); n != 2 {
		t.Fatalf("_restored rows = %d, want 2 (one per restore)", n)
	}
}

// TestBulkRestoreChildrenStayDetached: deleting a parent detaches its
// live children; restoring the parent does NOT re-attach them.
func TestBulkRestoreChildrenStayDetached(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	creator := createTestUser(t, pool, uniqueTestEmail("bulk-restore-detach"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("bulk-restore-detach"), creator)
	p := createTestProject(t, pool, ws.Slug, creator, "Engineering", uniqueTestIdentifier())
	parent := createTestIssue(t, pool, ws.Slug, p.Identifier, creator, "Parent")
	child, err := CreateIssue(ctx, pool, ws.Slug, p.Identifier, creator,
		CreateIssueInput{Name: "Child", ParentID: &parent.ID})
	if err != nil {
		t.Fatalf("CreateIssue child: %v", err)
	}
	if err := DeleteIssue(ctx, pool, ws.Slug, p.Identifier, parent.ID, creator); err != nil {
		t.Fatalf("DeleteIssue parent: %v", err)
	}
	// The delete detached the child.
	got, err := GetIssue(ctx, pool, ws.Slug, p.Identifier, child.ID, creator)
	if err != nil {
		t.Fatalf("GetIssue child after parent delete: %v", err)
	}
	if got.ParentID != nil {
		t.Fatalf("child ParentID = %v after parent delete, want nil (detached)", *got.ParentID)
	}

	if _, err := BulkRestoreIssues(ctx, pool, ws.Slug, p.Identifier, creator, []string{parent.ID}); err != nil {
		t.Fatalf("BulkRestoreIssues parent: %v", err)
	}
	got, err = GetIssue(ctx, pool, ws.Slug, p.Identifier, child.ID, creator)
	if err != nil {
		t.Fatalf("GetIssue child after parent restore: %v", err)
	}
	if got.ParentID != nil {
		t.Fatalf("child ParentID = %v after parent restore, want nil (stays detached, no silent re-parenting)", *got.ParentID)
	}
	if _, err := GetIssue(ctx, pool, ws.Slug, p.Identifier, parent.ID, creator); err != nil {
		t.Fatalf("GetIssue parent after restore: %v, want the live row", err)
	}
}

// TestBulkRestoreArchivedProject: project-archive blocks NO issue
// mutation anywhere in the codebase (resolveIssueProject has no
// archived check), so restore follows the same rule — it succeeds in
// an archived project.
func TestBulkRestoreArchivedProject(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	creator, wsSlug, ident, iss := trashFixture(t, pool, "bulk-restore-arch")

	if _, err := ArchiveProject(ctx, pool, wsSlug, ident, creator, true); err != nil {
		t.Fatalf("ArchiveProject: %v", err)
	}
	results, err := BulkRestoreIssues(ctx, pool, wsSlug, ident, creator, []string{iss.ID})
	if err != nil {
		t.Fatalf("BulkRestoreIssues in archived project: %v", err)
	}
	if len(results) != 1 || !results[0].OK {
		t.Fatalf("results = %+v, want [ok] (archived project does not block restore)", results)
	}
	if trashIssueStillDeleted(t, pool, iss.ID) {
		t.Fatalf("issue still deleted after restore in archived project")
	}
}

// TestBulkRestoreDeletedProject: projects are HARD-deleted
// (DeleteProject + FK cascades), so restoring an issue of a deleted
// project cannot happen — project resolution fails first with
// ErrProjectNotFound.
func TestBulkRestoreDeletedProject(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	creator := createTestUser(t, pool, uniqueTestEmail("bulk-restore-delproj"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("bulk-restore-delproj"), creator)
	p := createTestProject(t, pool, ws.Slug, creator, "Engineering", uniqueTestIdentifier())
	iss := createTestIssue(t, pool, ws.Slug, p.Identifier, creator, "Doomed")
	if err := DeleteIssue(ctx, pool, ws.Slug, p.Identifier, iss.ID, creator); err != nil {
		t.Fatalf("DeleteIssue: %v", err)
	}
	if err := DeleteProject(ctx, pool, ws.Slug, p.Identifier, creator, p.Identifier, true); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	if _, err := BulkRestoreIssues(ctx, pool, ws.Slug, p.Identifier, creator, []string{iss.ID}); !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("bulk restore in deleted project: err = %v, want ErrProjectNotFound", err)
	}
}

// TestBulkRestoreDeletedParentClearsParent: restoring a child whose
// parent is deleted clears the stale parent link with a parent_id
// activity row (old = stale parent, new = NULL) and a parent_id
// webhook change — the per-item parent re-validation of restoreIssueTx.
func TestBulkRestoreDeletedParentClearsParent(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	creator := createTestUser(t, pool, uniqueTestEmail("bulk-restore-parent"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("bulk-restore-parent"), creator)
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

	results, err := BulkRestoreIssues(ctx, pool, ws.Slug, p.Identifier, creator, []string{child.ID})
	if err != nil {
		t.Fatalf("BulkRestoreIssues: %v", err)
	}
	if len(results) != 1 || !results[0].OK {
		t.Fatalf("results = %+v, want [ok]", results)
	}
	got, err := GetIssue(ctx, pool, ws.Slug, p.Identifier, child.ID, creator)
	if err != nil {
		t.Fatalf("GetIssue child after restore: %v", err)
	}
	if got.ParentID != nil {
		t.Fatalf("restored child ParentID = %v, want nil (parent was deleted)", *got.ParentID)
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

// TestBulkRestoreChildBeforeParentInBatch: per-item savepoint ordering
// — a batch restoring [child, parent] clears the child's parent link
// (the parent is still deleted at the child's turn) while the parent
// itself still restores. Documented, deterministic per-item semantics.
func TestBulkRestoreChildBeforeParentInBatch(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	creator := createTestUser(t, pool, uniqueTestEmail("bulk-restore-order"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("bulk-restore-order"), creator)
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
	if err := DeleteIssue(ctx, pool, ws.Slug, p.Identifier, parent.ID, creator); err != nil {
		t.Fatalf("DeleteIssue parent: %v", err)
	}

	results, err := BulkRestoreIssues(ctx, pool, ws.Slug, p.Identifier, creator,
		[]string{child.ID, parent.ID})
	if err != nil {
		t.Fatalf("BulkRestoreIssues: %v", err)
	}
	for _, r := range results {
		if !r.OK {
			t.Fatalf("result %+v, want ok", r)
		}
	}
	got, err := GetIssue(ctx, pool, ws.Slug, p.Identifier, child.ID, creator)
	if err != nil {
		t.Fatalf("GetIssue child: %v", err)
	}
	if got.ParentID != nil {
		t.Fatalf("child ParentID = %v, want nil (parent still deleted at the child's turn)", *got.ParentID)
	}
	if _, err := GetIssue(ctx, pool, ws.Slug, p.Identifier, parent.ID, creator); err != nil {
		t.Fatalf("GetIssue parent: %v, want the live row", err)
	}
}

// TestBulkRestoreInvalidStatePerItem: an issue whose state_id names a
// state of another project fails per-item (ErrInvalidState →
// "invalid state_id...") while the rest of the batch restores; the
// failed item stays deleted with no _restored row.
func TestBulkRestoreInvalidStatePerItem(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	creator := createTestUser(t, pool, uniqueTestEmail("bulk-restore-badstate"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("bulk-restore-badstate"), creator)
	pA := createTestProject(t, pool, ws.Slug, creator, "Engineering", uniqueTestIdentifier())
	pB := createTestProject(t, pool, ws.Slug, creator, "Other", uniqueTestIdentifier())
	bad := createTestIssue(t, pool, ws.Slug, pA.Identifier, creator, "Bad state")
	good := createTestIssue(t, pool, ws.Slug, pA.Identifier, creator, "Good")
	for _, iss := range []*Issue{bad, good} {
		if err := DeleteIssue(ctx, pool, ws.Slug, pA.Identifier, iss.ID, creator); err != nil {
			t.Fatalf("DeleteIssue: %v", err)
		}
	}
	statesB, err := ListStates(ctx, pool, ws.Slug, pB.Identifier, creator)
	if err != nil {
		t.Fatalf("ListStates B: %v", err)
	}
	if len(statesB) == 0 {
		t.Fatalf("project B has no states")
	}
	if _, err := pool.Exec(ctx,
		`UPDATE issues SET state_id = $1::uuid WHERE id = $2::uuid`, statesB[0].ID, bad.ID); err != nil {
		t.Fatalf("point issue at foreign state: %v", err)
	}

	results, err := BulkRestoreIssues(ctx, pool, ws.Slug, pA.Identifier, creator,
		[]string{bad.ID, good.ID})
	if err != nil {
		t.Fatalf("BulkRestoreIssues: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	if results[0].OK || results[0].Error == nil || *results[0].Error != "invalid state_id: state does not exist or belongs to another project" {
		t.Fatalf("results[0] = %+v, want the invalid-state per-item failure", results[0])
	}
	if !results[1].OK {
		t.Fatalf("results[1] = %+v, want ok (good item restores despite the bad one)", results[1])
	}
	if !trashIssueStillDeleted(t, pool, bad.ID) {
		t.Fatalf("failed item cleared deleted_at")
	}
	if n := countActivities(t, pool, bad.ID, "_restored"); n != 0 {
		t.Fatalf("failed item wrote %d _restored rows, want 0", n)
	}
	if trashIssueStillDeleted(t, pool, good.ID) {
		t.Fatalf("good item still deleted")
	}
}
