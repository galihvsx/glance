package service

// DeleteIssue child detach trail (C51T1): deleting a parent detaches its
// live children and leaves a trace on each one — one parent_id activity
// row (old = deleted parent, new = SQL NULL, same shape as Convert's
// parent_id row) + one version snapshot of the post-detach row — and
// announces issue.updated per detached child after commit. Soft-deleted
// children are untouched; a parent with no children writes nothing extra.
// All tests run against the real test database — no skips.

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

// parentIDDetachPair is one parent_id activity row: Old is the JSON
// string value, New is nil when new_value is SQL NULL.
type parentIDDetachPair struct {
	Old string
	New *string
}

// parentIDActivityPairs returns every parent_id activity row for an issue
// in insertion order.
func parentIDActivityPairs(t *testing.T, pool *pgxpool.Pool, issueID string) []parentIDDetachPair {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT old_value, new_value FROM issue_activities
		  WHERE issue_id = $1::uuid AND field = 'parent_id' ORDER BY created_at, id`,
		issueID)
	if err != nil {
		t.Fatalf("read parent_id activity: %v", err)
	}
	defer rows.Close()
	var pairs []parentIDDetachPair
	for rows.Next() {
		var oldRaw, newRaw []byte
		if err := rows.Scan(&oldRaw, &newRaw); err != nil {
			t.Fatalf("scan parent_id activity: %v", err)
		}
		var p parentIDDetachPair
		if err := json.Unmarshal(oldRaw, &p.Old); err != nil {
			t.Fatalf("unmarshal old parent_id: %v", err)
		}
		if newRaw != nil {
			var n string
			if err := json.Unmarshal(newRaw, &n); err != nil {
				t.Fatalf("unmarshal new parent_id: %v", err)
			}
			p.New = &n
		}
		pairs = append(pairs, p)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("parent_id activity rows: %v", err)
	}
	return pairs
}

// latestVersionIssue unmarshals the issue's newest version snapshot.
func latestVersionIssue(t *testing.T, pool *pgxpool.Pool, issueID string) *Issue {
	t.Helper()
	var snap []byte
	if err := pool.QueryRow(context.Background(),
		`SELECT snapshot FROM issue_versions
		  WHERE issue_id = $1::uuid ORDER BY version_no DESC LIMIT 1`,
		issueID).Scan(&snap); err != nil {
		t.Fatalf("latest version snapshot: %v", err)
	}
	var iss Issue
	if err := json.Unmarshal(snap, &iss); err != nil {
		t.Fatalf("unmarshal latest snapshot: %v", err)
	}
	return &iss
}

// detachFixture builds a parent with two live children.
func detachFixture(t *testing.T, pool *pgxpool.Pool, prefix string) (creator, wsSlug, ident string, parent, child1, child2 *Issue) {
	t.Helper()
	ctx := context.Background()
	creator = createTestUser(t, pool, uniqueTestEmail(prefix))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug(prefix), creator)
	p := createTestProject(t, pool, ws.Slug, creator, "Engineering", uniqueTestIdentifier())
	parent = createTestIssue(t, pool, ws.Slug, p.Identifier, creator, "Parent")
	var err error
	child1, err = CreateIssue(ctx, pool, ws.Slug, p.Identifier, creator,
		CreateIssueInput{Name: "Child 1", ParentID: &parent.ID})
	if err != nil {
		t.Fatalf("CreateIssue child1: %v", err)
	}
	child2, err = CreateIssue(ctx, pool, ws.Slug, p.Identifier, creator,
		CreateIssueInput{Name: "Child 2", ParentID: &parent.ID})
	if err != nil {
		t.Fatalf("CreateIssue child2: %v", err)
	}
	return creator, ws.Slug, p.Identifier, parent, child1, child2
}

// TestDeleteIssueDetachesChildrenWithTrail (a): deleting a parent with
// two live children gives each child exactly one parent_id activity row
// (old = parent, new = NULL) + one new version with ParentID == nil, and
// announces issue.updated per detached child.
func TestDeleteIssueDetachesChildrenWithTrail(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	creator, wsSlug, ident, parent, child1, child2 := detachFixture(t, pool, "detach-trail")

	fake := installCapturingBroadcaster(t)
	if err := DeleteIssue(ctx, pool, wsSlug, ident, parent.ID, creator); err != nil {
		t.Fatalf("DeleteIssue: %v", err)
	}

	for _, child := range []*Issue{child1, child2} {
		got, err := GetIssue(ctx, pool, wsSlug, ident, child.ID, creator)
		if err != nil {
			t.Fatalf("GetIssue child %s: %v", child.ID, err)
		}
		if got.ParentID != nil {
			t.Fatalf("child %s parent_id = %v, want nil", child.ID, *got.ParentID)
		}
		pairs := parentIDActivityPairs(t, pool, child.ID)
		if len(pairs) != 1 {
			t.Fatalf("child %s parent_id activity rows = %d, want 1", child.ID, len(pairs))
		}
		if pairs[0].Old != parent.ID {
			t.Fatalf("child %s parent_id old = %q, want %q", child.ID, pairs[0].Old, parent.ID)
		}
		if pairs[0].New != nil {
			t.Fatalf("child %s parent_id new = %q, want SQL NULL", child.ID, *pairs[0].New)
		}
		// Create (v1) + detach (v2); the newest snapshot is post-detach.
		if n := countVersions(t, pool, child.ID); n != 2 {
			t.Fatalf("child %s version rows = %d, want 2", child.ID, n)
		}
		if latest := latestVersionIssue(t, pool, child.ID); latest.ParentID != nil {
			t.Fatalf("child %s latest snapshot parent_id = %v, want nil", child.ID, *latest.ParentID)
		}
	}
	assertIssueUpdatedIDs(t, fake, child1.ID, child2.ID)
}

// TestDeleteIssueSoftDeletedChildUntouched (b): a child that is already
// soft-deleted is not detached and gains no trail.
func TestDeleteIssueSoftDeletedChildUntouched(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	creator, wsSlug, ident, parent, child1, child2 := detachFixture(t, pool, "detach-ghost")

	// Soft-delete child1 first: its own _deleted row + version only.
	if err := DeleteIssue(ctx, pool, wsSlug, ident, child1.ID, creator); err != nil {
		t.Fatalf("DeleteIssue child1: %v", err)
	}
	versionsBefore := countVersions(t, pool, child1.ID)

	if err := DeleteIssue(ctx, pool, wsSlug, ident, parent.ID, creator); err != nil {
		t.Fatalf("DeleteIssue parent: %v", err)
	}
	// The ghost keeps its parent link in the raw row and gains no trail.
	var stillParent int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM issues WHERE id = $1::uuid AND parent_id = $2::uuid`,
		child1.ID, parent.ID).Scan(&stillParent); err != nil {
		t.Fatalf("ghost parent link: %v", err)
	}
	if stillParent != 1 {
		t.Fatalf("soft-deleted child parent link rows = %d, want 1", stillParent)
	}
	if pairs := parentIDActivityPairs(t, pool, child1.ID); len(pairs) != 0 {
		t.Fatalf("soft-deleted child parent_id activity rows = %d, want 0", len(pairs))
	}
	if n := countVersions(t, pool, child1.ID); n != versionsBefore {
		t.Fatalf("soft-deleted child version rows = %d, want %d (unchanged)", n, versionsBefore)
	}
	// The live sibling still gets the full trail.
	if pairs := parentIDActivityPairs(t, pool, child2.ID); len(pairs) != 1 || pairs[0].Old != parent.ID || pairs[0].New != nil {
		t.Fatalf("live child parent_id activity = %+v, want [{old=%s new=NULL}]", pairs, parent.ID)
	}
}

// TestDeleteIssueNoChildrenWritesNothingExtra (c): deleting a
// childless parent writes no parent_id rows and no extra versions.
func TestDeleteIssueNoChildrenWritesNothingExtra(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	creator, wsSlug, ident, parent, _, _ := detachFixture(t, pool, "detach-none")

	// The fixture children belong to the parent; use a fresh childless
	// parent + bystander in the same project for the no-children case.
	lonely := createTestIssue(t, pool, wsSlug, ident, creator, "Lonely")
	bystander := createTestIssue(t, pool, wsSlug, ident, creator, "Bystander")
	_ = parent

	fake := installCapturingBroadcaster(t)
	if err := DeleteIssue(ctx, pool, wsSlug, ident, lonely.ID, creator); err != nil {
		t.Fatalf("DeleteIssue lonely: %v", err)
	}
	// Lonely: create + delete versions only, no parent_id rows.
	if n := countVersions(t, pool, lonely.ID); n != 2 {
		t.Fatalf("lonely version rows = %d, want 2", n)
	}
	if pairs := parentIDActivityPairs(t, pool, lonely.ID); len(pairs) != 0 {
		t.Fatalf("lonely parent_id activity rows = %d, want 0", len(pairs))
	}
	// Bystander untouched: still 1 version, no parent_id rows.
	if n := countVersions(t, pool, bystander.ID); n != 1 {
		t.Fatalf("bystander version rows = %d, want 1", n)
	}
	if pairs := parentIDActivityPairs(t, pool, bystander.ID); len(pairs) != 0 {
		t.Fatalf("bystander parent_id activity rows = %d, want 0", len(pairs))
	}
	assertIssueUpdatedIDs(t, fake)
}

// TestBulkDeleteParentAndChild (d): parent + child in the same batch are
// both deleted. The child history shows detach-then-delete (parent first)
// or delete-only (child first) — either order is correct, so assert the
// final state, not the order. A detached-then-deleted child is never
// announced as issue.updated (post-commit re-read skips deleted rows).
func TestBulkDeleteParentAndChild(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	creator, wsSlug, ident, parent, child1, child2 := detachFixture(t, pool, "detach-bulk")

	fake := installCapturingBroadcaster(t)
	results, err := BulkDeleteIssues(ctx, pool, wsSlug, ident, creator, []string{parent.ID, child1.ID})
	if err != nil {
		t.Fatalf("BulkDeleteIssues: %v", err)
	}
	for _, r := range results {
		if !r.OK {
			t.Fatalf("bulk result %+v: want OK", r)
		}
	}
	for _, id := range []string{parent.ID, child1.ID} {
		if _, err := GetIssue(ctx, pool, wsSlug, ident, id, creator); !errors.Is(err, ErrIssueNotFound) {
			t.Fatalf("get after bulk delete %s: err = %v, want ErrIssueNotFound", id, err)
		}
	}
	// Parent: _deleted + create/delete versions only.
	if n := countActivities(t, pool, parent.ID, "_deleted"); n != 1 {
		t.Fatalf("parent _deleted rows = %d, want 1", n)
	}
	if n := countVersions(t, pool, parent.ID); n != 2 {
		t.Fatalf("parent version rows = %d, want 2", n)
	}
	// Child: deleted exactly once; detach trail present iff the parent
	// was processed first (0 or 1 parent_id rows, never more).
	if n := countActivities(t, pool, child1.ID, "_deleted"); n != 1 {
		t.Fatalf("child _deleted rows = %d, want 1", n)
	}
	pairs := parentIDActivityPairs(t, pool, child1.ID)
	if len(pairs) > 1 {
		t.Fatalf("child parent_id activity rows = %d, want 0 or 1", len(pairs))
	}
	if len(pairs) == 1 && (pairs[0].Old != parent.ID || pairs[0].New != nil) {
		t.Fatalf("child detach pair = %+v, want {old=%s new=NULL}", pairs[0], parent.ID)
	}
	// create + delete (+ detach iff parent first) = 2 or 3 versions.
	if n := countVersions(t, pool, child1.ID); n != 2 && n != 3 {
		t.Fatalf("child version rows = %d, want 2 or 3", n)
	}
	if len(pairs) == 1 && countVersions(t, pool, child1.ID) != 3 {
		t.Fatalf("child with detach trail has %d versions, want 3", countVersions(t, pool, child1.ID))
	}
	if len(pairs) == 0 && countVersions(t, pool, child1.ID) != 2 {
		t.Fatalf("child without detach trail has %d versions, want 2", countVersions(t, pool, child1.ID))
	}
	// No issue.updated for the (deleted) child in either order — but
	// the surviving sibling was detached and IS announced.
	assertIssueUpdatedIDs(t, fake, child2.ID)
	// The surviving sibling got the full trail.
	if pairs := parentIDActivityPairs(t, pool, child2.ID); len(pairs) != 1 || pairs[0].Old != parent.ID || pairs[0].New != nil {
		t.Fatalf("sibling parent_id activity = %+v, want [{old=%s new=NULL}]", pairs, parent.ID)
	}
	if n := countVersions(t, pool, child2.ID); n != 2 {
		t.Fatalf("sibling version rows = %d, want 2", n)
	}
}

// TestRejectIntakeIssueDetachesChildrenWithTrail: rejecting an intake
// parent detaches its live children with the same trail + announce.
func TestRejectIntakeIssueDetachesChildrenWithTrail(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	creator, wsSlug, ident := intakeFixture(t, pool, "detach-reject")
	parent := createIntakeIssue(t, pool, wsSlug, ident, creator, "Intake parent")
	child, err := CreateIssue(ctx, pool, wsSlug, ident, creator,
		CreateIssueInput{Name: "Intake child", ParentID: &parent.ID})
	if err != nil {
		t.Fatalf("CreateIssue child: %v", err)
	}

	fake := installCapturingBroadcaster(t)
	if _, err := RejectIntakeIssue(ctx, pool, wsSlug, ident, parent.ID, creator); err != nil {
		t.Fatalf("RejectIntakeIssue: %v", err)
	}

	got, err := GetIssue(ctx, pool, wsSlug, ident, child.ID, creator)
	if err != nil {
		t.Fatalf("GetIssue child: %v", err)
	}
	if got.ParentID != nil {
		t.Fatalf("child parent_id = %v, want nil", *got.ParentID)
	}
	pairs := parentIDActivityPairs(t, pool, child.ID)
	if len(pairs) != 1 || pairs[0].Old != parent.ID || pairs[0].New != nil {
		t.Fatalf("child parent_id activity = %+v, want [{old=%s new=NULL}]", pairs, parent.ID)
	}
	if n := countVersions(t, pool, child.ID); n != 2 {
		t.Fatalf("child version rows = %d, want 2", n)
	}
	if latest := latestVersionIssue(t, pool, child.ID); latest.ParentID != nil {
		t.Fatalf("child latest snapshot parent_id = %v, want nil", *latest.ParentID)
	}
	assertIssueUpdatedIDs(t, fake, child.ID)
}

// outboxWatermark returns the current max outbox id so a test can scope
// its assertions to rows enqueued after this point (the test DB is
// shared across runs).
func outboxWatermark(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	var w int64
	if err := pool.QueryRow(context.Background(),
		`SELECT COALESCE(MAX(id), 0) FROM outbox`).Scan(&w); err != nil {
		t.Fatalf("outbox watermark: %v", err)
	}
	return w
}

// webhookOutboxPayload is the JSON shape of a webhook.deliver outbox row:
// the webhookDelivery struct marshalled by mail.Enqueue.
type webhookOutboxPayload struct {
	WebhookID string `json:"webhook_id"`
	Event     string `json:"event"`
	Data      struct {
		ID            string   `json:"id"`
		DisplayID     string   `json:"display_id"`
		ChangedFields []string `json:"changed_fields"`
		ActorID       string   `json:"actor_id"`
	} `json:"data"`
}

// webhookOutboxRows returns the webhook.deliver outbox rows enqueued after
// the watermark for one webhook + domain event, in insertion order.
func webhookOutboxRows(t *testing.T, pool *pgxpool.Pool, watermark int64, webhookID, domainEvent string) []webhookOutboxPayload {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT payload FROM outbox
		  WHERE id > $1 AND event = 'webhook.deliver'
		    AND payload->>'webhook_id' = $2 AND payload->>'event' = $3
		  ORDER BY id`,
		watermark, webhookID, domainEvent)
	if err != nil {
		t.Fatalf("read webhook outbox: %v", err)
	}
	defer rows.Close()
	var out []webhookOutboxPayload
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			t.Fatalf("scan webhook outbox: %v", err)
		}
		var p webhookOutboxPayload
		if err := json.Unmarshal(raw, &p); err != nil {
			t.Fatalf("unmarshal webhook outbox: %v", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("webhook outbox rows: %v", err)
	}
	return out
}

// TestDeleteIssueDetachEnqueuesWebhookIssueUpdated: deleting a parent
// with one child enqueues exactly one webhook.deliver row carrying an
// issue.updated payload for the detached child (parent_id change, same
// shape as SetParent's), alongside the parent's own issue.deleted row.
func TestDeleteIssueDetachEnqueuesWebhookIssueUpdated(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	creator := createTestUser(t, pool, uniqueTestEmail("detach-wh"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("detach-wh"), creator)
	p := createTestProject(t, pool, ws.Slug, creator, "Engineering", uniqueTestIdentifier())
	parent := createTestIssue(t, pool, ws.Slug, p.Identifier, creator, "Parent")
	child, err := CreateIssue(ctx, pool, ws.Slug, p.Identifier, creator,
		CreateIssueInput{Name: "Child", ParentID: &parent.ID})
	if err != nil {
		t.Fatalf("CreateIssue child: %v", err)
	}
	// httptest target, like the other webhook tests: the URL is never
	// fetched here (only enqueued), and a later dispatcher pass against
	// this closed server fails fast on connection-refused instead of
	// stalling on a real host.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	wh, err := CreateWebhook(ctx, pool, ws.Slug, creator, WebhookInput{
		URL: srv.URL, Secret: strPtr2("s3cret"),
		Events: []string{EventIssueUpdated, EventIssueDeleted},
	})
	if err != nil {
		t.Fatalf("CreateWebhook: %v", err)
	}

	watermark := outboxWatermark(t, pool)
	if err := DeleteIssue(ctx, pool, ws.Slug, p.Identifier, parent.ID, creator); err != nil {
		t.Fatalf("DeleteIssue: %v", err)
	}

	updated := webhookOutboxRows(t, pool, watermark, wh.ID, EventIssueUpdated)
	if len(updated) != 1 {
		t.Fatalf("issue.updated webhook rows = %d, want exactly 1", len(updated))
	}
	got := updated[0]
	if got.Data.ID != child.ID {
		t.Fatalf("issue.updated data.id = %q, want child %q", got.Data.ID, child.ID)
	}
	if got.Data.DisplayID != p.Identifier+"-"+strconv.Itoa(child.SequenceID) {
		t.Fatalf("issue.updated data.display_id = %q, want %q",
			got.Data.DisplayID, p.Identifier+"-"+strconv.Itoa(child.SequenceID))
	}
	if len(got.Data.ChangedFields) != 1 || got.Data.ChangedFields[0] != "parent_id" {
		t.Fatalf("issue.updated data.changed_fields = %v, want [parent_id]", got.Data.ChangedFields)
	}
	if got.Data.ActorID != creator {
		t.Fatalf("issue.updated data.actor_id = %q, want %q", got.Data.ActorID, creator)
	}
	// The parent's own issue.deleted row is pre-existing behavior and
	// must still be enqueued.
	deleted := webhookOutboxRows(t, pool, watermark, wh.ID, EventIssueDeleted)
	if len(deleted) != 1 || deleted[0].Data.ID != parent.ID {
		t.Fatalf("issue.deleted webhook rows = %+v, want exactly 1 for parent %s", deleted, parent.ID)
	}
}

// TestDeleteIssueNoChildrenEnqueuesNoDetachWebhook: deleting a
// childless parent enqueues no issue.updated webhook row carrying a
// parent_id change (the parent's own issue.deleted row is pre-existing
// behavior and out of scope here).
func TestDeleteIssueNoChildrenEnqueuesNoDetachWebhook(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	creator := createTestUser(t, pool, uniqueTestEmail("detach-wh-none"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("detach-wh-none"), creator)
	p := createTestProject(t, pool, ws.Slug, creator, "Engineering", uniqueTestIdentifier())
	lonely := createTestIssue(t, pool, ws.Slug, p.Identifier, creator, "Lonely")
	// httptest target, like the other webhook tests: the URL is never
	// fetched here (only enqueued), and a later dispatcher pass against
	// this closed server fails fast on connection-refused instead of
	// stalling on a real host.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	wh, err := CreateWebhook(ctx, pool, ws.Slug, creator, WebhookInput{
		URL: srv.URL, Secret: strPtr2("s3cret"),
		Events: []string{EventIssueUpdated, EventIssueDeleted},
	})
	if err != nil {
		t.Fatalf("CreateWebhook: %v", err)
	}

	watermark := outboxWatermark(t, pool)
	if err := DeleteIssue(ctx, pool, ws.Slug, p.Identifier, lonely.ID, creator); err != nil {
		t.Fatalf("DeleteIssue: %v", err)
	}

	// No detach-shaped issue.updated row: none of the issue.updated rows
	// enqueued by the delete may carry a parent_id change.
	for _, row := range webhookOutboxRows(t, pool, watermark, wh.ID, EventIssueUpdated) {
		for _, f := range row.Data.ChangedFields {
			if f == "parent_id" {
				t.Fatalf("unexpected parent_id issue.updated webhook row: %+v", row)
			}
		}
	}
	// Sanity: the plumbing works — the parent's own issue.deleted row
	// WAS enqueued.
	deleted := webhookOutboxRows(t, pool, watermark, wh.ID, EventIssueDeleted)
	if len(deleted) != 1 || deleted[0].Data.ID != lonely.ID {
		t.Fatalf("issue.deleted webhook rows for lonely = %+v, want exactly 1 for %s", deleted, lonely.ID)
	}
}

// parent a duplicate detaches its live children with the same trail +
// announce.
func TestDuplicateIntakeIssueDetachesChildrenWithTrail(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	creator, wsSlug, ident := intakeFixture(t, pool, "detach-dupe")
	target := createTestIssue(t, pool, wsSlug, ident, creator, "Original")
	parent := createIntakeIssue(t, pool, wsSlug, ident, creator, "Intake dupe")
	child, err := CreateIssue(ctx, pool, wsSlug, ident, creator,
		CreateIssueInput{Name: "Dupe child", ParentID: &parent.ID})
	if err != nil {
		t.Fatalf("CreateIssue child: %v", err)
	}

	fake := installCapturingBroadcaster(t)
	if _, err := DuplicateIntakeIssue(ctx, pool, wsSlug, ident, parent.ID, creator, target.ID); err != nil {
		t.Fatalf("DuplicateIntakeIssue: %v", err)
	}

	got, err := GetIssue(ctx, pool, wsSlug, ident, child.ID, creator)
	if err != nil {
		t.Fatalf("GetIssue child: %v", err)
	}
	if got.ParentID != nil {
		t.Fatalf("child parent_id = %v, want nil", *got.ParentID)
	}
	pairs := parentIDActivityPairs(t, pool, child.ID)
	if len(pairs) != 1 || pairs[0].Old != parent.ID || pairs[0].New != nil {
		t.Fatalf("child parent_id activity = %+v, want [{old=%s new=NULL}]", pairs, parent.ID)
	}
	if n := countVersions(t, pool, child.ID); n != 2 {
		t.Fatalf("child version rows = %d, want 2", n)
	}
	if latest := latestVersionIssue(t, pool, child.ID); latest.ParentID != nil {
		t.Fatalf("child latest snapshot parent_id = %v, want nil", *latest.ParentID)
	}
	assertIssueUpdatedIDs(t, fake, child.ID)
}
