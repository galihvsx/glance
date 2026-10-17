package service

// Release bulk ops trail (C51T2): Assign / Unassign / DeleteRelease
// (reassign + force) must write one release_id activity row per
// actually-changed issue (old → new, same shape as the PATCH path),
// enqueue one issue.updated webhook delivery per changed issue, and
// announce issue.updated per changed issue — all in one tx for the
// writes. Idempotent no-ops write / enqueue / announce nothing new
// (except the pre-existing release.updated, unchanged). A 409 delete
// writes nothing and announces nothing.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// releaseActivityPair is one release_id activity row: a nil pointer is
// a SQL NULL old/new value (unassigned side of the move).
type releaseActivityPair struct {
	old, new *string
}

// releaseIDActivityRows returns every release_id activity row for an
// issue, in insertion order.
func releaseIDActivityRows(t *testing.T, pool *pgxpool.Pool, issueID string) []releaseActivityPair {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT old_value, new_value FROM issue_activities
		  WHERE issue_id = $1::uuid AND field = 'release_id' ORDER BY created_at, id`,
		issueID)
	if err != nil {
		t.Fatalf("read release_id activity: %v", err)
	}
	defer rows.Close()
	var out []releaseActivityPair
	for rows.Next() {
		var oldRaw, newRaw []byte
		if err := rows.Scan(&oldRaw, &newRaw); err != nil {
			t.Fatalf("scan release_id activity: %v", err)
		}
		var p releaseActivityPair
		if oldRaw != nil {
			var s string
			if err := json.Unmarshal(oldRaw, &s); err != nil {
				t.Fatalf("unmarshal old release_id: %v", err)
			}
			p.old = &s
		}
		if newRaw != nil {
			var s string
			if err := json.Unmarshal(newRaw, &s); err != nil {
				t.Fatalf("unmarshal new release_id: %v", err)
			}
			p.new = &s
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("release_id activity rows: %v", err)
	}
	return out
}

// assertReleaseActivityRow asserts the issue has exactly one release_id
// activity row with the given old/new (nil = SQL NULL).
func assertReleaseActivityRow(t *testing.T, pool *pgxpool.Pool, issueID string, wantOld, wantNew *string) {
	t.Helper()
	rows := releaseIDActivityRows(t, pool, issueID)
	if len(rows) != 1 {
		t.Fatalf("issue %s: release_id activity rows = %d, want 1: %+v", issueID, len(rows), rows)
	}
	gotOld, gotNew := "<null>", "<null>"
	if rows[0].old != nil {
		gotOld = *rows[0].old
	}
	if rows[0].new != nil {
		gotNew = *rows[0].new
	}
	wantOldStr, wantNewStr := "<null>", "<null>"
	if wantOld != nil {
		wantOldStr = *wantOld
	}
	if wantNew != nil {
		wantNewStr = *wantNew
	}
	if gotOld != wantOldStr || gotNew != wantNewStr {
		t.Fatalf("issue %s: release_id activity = (%s → %s), want (%s → %s)",
			issueID, gotOld, gotNew, wantOldStr, wantNewStr)
	}
}

// createIssueUpdatedWebhook creates a webhook subscribed to
// issue.updated only, returning its id.
func createIssueUpdatedWebhook(t *testing.T, ctx context.Context, pool *pgxpool.Pool, slug, actor string) string {
	t.Helper()
	wh, err := CreateWebhook(ctx, pool, slug, actor, WebhookInput{
		URL:    "https://example.com/hook",
		Events: []string{EventIssueUpdated},
	})
	if err != nil {
		t.Fatalf("CreateWebhook: %v", err)
	}
	return wh.ID
}

// countIssueUpdatedDeliveries counts the webhook.deliver outbox rows for
// one webhook + one issue.
func countIssueUpdatedDeliveries(t *testing.T, pool *pgxpool.Pool, webhookID, issueID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM outbox
		  WHERE event = 'webhook.deliver'
		    AND payload->>'webhook_id' = $1
		    AND payload->'data'->>'id' = $2`,
		webhookID, issueID).Scan(&n); err != nil {
		t.Fatalf("count webhook deliveries: %v", err)
	}
	return n
}

// assertIssueUpdatedDelivery asserts exactly one webhook.deliver outbox
// row for the webhook + issue, whose changed_fields include release_id.
func assertIssueUpdatedDelivery(t *testing.T, pool *pgxpool.Pool, webhookID, issueID string) {
	t.Helper()
	if n := countIssueUpdatedDeliveries(t, pool, webhookID, issueID); n != 1 {
		t.Fatalf("issue %s: issue.updated webhook deliveries = %d, want 1", issueID, n)
	}
	var raw []byte
	if err := pool.QueryRow(context.Background(),
		`SELECT payload->'data'->'changed_fields' FROM outbox
		  WHERE event = 'webhook.deliver'
		    AND payload->>'webhook_id' = $1
		    AND payload->'data'->>'id' = $2
		  ORDER BY id LIMIT 1`,
		webhookID, issueID).Scan(&raw); err != nil {
		t.Fatalf("read changed_fields: %v", err)
	}
	var fields []string
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("unmarshal changed_fields: %v", err)
	}
	for _, f := range fields {
		if f == "release_id" {
			return
		}
	}
	t.Fatalf("issue %s: changed_fields = %v, want it to include release_id", issueID, fields)
}

// parkIssueOnRelease sets an issue's release_id via direct SQL (no
// activity rows), for arranging the before-image of a bulk op.
func parkIssueOnRelease(t *testing.T, pool *pgxpool.Pool, issueID, releaseID string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`UPDATE issues SET release_id = $1::uuid WHERE id = $2::uuid`,
		releaseID, issueID); err != nil {
		t.Fatalf("park issue on release: %v", err)
	}
}

func TestReleaseBulkAssignWritesTrail(t *testing.T) {
	ctx, pool, slug, ident, actor := setupReleaseTest(t)
	target, err := CreateRelease(ctx, pool, slug, ident, actor, ReleaseInput{Name: "v5.0"})
	if err != nil {
		t.Fatalf("CreateRelease: %v", err)
	}
	iss1 := createTestIssue(t, pool, slug, ident, actor, "Assign one")
	iss2 := createTestIssue(t, pool, slug, ident, actor, "Assign two")
	iss3 := createTestIssue(t, pool, slug, ident, actor, "Assign three")
	whID := createIssueUpdatedWebhook(t, ctx, pool, slug, actor)
	fake := installCapturingBroadcaster(t)

	if err := AssignReleaseIssues(ctx, pool, slug, ident, actor, target.ID, []string{iss1.ID, iss2.ID}); err != nil {
		t.Fatalf("AssignReleaseIssues: %v", err)
	}
	for _, iss := range []*Issue{iss1, iss2} {
		assertReleaseActivityRow(t, pool, iss.ID, nil, &target.ID)
		assertIssueUpdatedDelivery(t, pool, whID, iss.ID)
	}
	assertIssueUpdatedIDs(t, fake, iss1.ID, iss2.ID)
	if n := countEventsOfType(t, fake, EventReleaseUpdated); n != 1 {
		t.Fatalf("release.updated events = %d, want 1", n)
	}

	// Mixed re-assign: iss1 is already on the target (no-op — no new
	// row), iss3 is fresh (one row). Only the actually-changed issue
	// gets a trail row / delivery / broadcast.
	if err := AssignReleaseIssues(ctx, pool, slug, ident, actor, target.ID, []string{iss1.ID, iss3.ID}); err != nil {
		t.Fatalf("mixed re-assign: %v", err)
	}
	if rows := releaseIDActivityRows(t, pool, iss1.ID); len(rows) != 1 {
		t.Fatalf("already-on-target issue: release_id rows = %d, want still 1", len(rows))
	}
	if n := countIssueUpdatedDeliveries(t, pool, whID, iss1.ID); n != 1 {
		t.Fatalf("already-on-target issue: deliveries = %d, want still 1", n)
	}
	assertReleaseActivityRow(t, pool, iss3.ID, nil, &target.ID)
	assertIssueUpdatedDelivery(t, pool, whID, iss3.ID)
}

func TestReleaseBulkAssignAlreadyOnTargetWritesNothing(t *testing.T) {
	ctx, pool, slug, ident, actor := setupReleaseTest(t)
	target, err := CreateRelease(ctx, pool, slug, ident, actor, ReleaseInput{Name: "v5.1"})
	if err != nil {
		t.Fatalf("CreateRelease: %v", err)
	}
	iss := createTestIssue(t, pool, slug, ident, actor, "Already there")
	parkIssueOnRelease(t, pool, iss.ID, target.ID)
	whID := createIssueUpdatedWebhook(t, ctx, pool, slug, actor)
	fake := installCapturingBroadcaster(t)

	if err := AssignReleaseIssues(ctx, pool, slug, ident, actor, target.ID, []string{iss.ID}); err != nil {
		t.Fatalf("AssignReleaseIssues: %v", err)
	}
	if rows := releaseIDActivityRows(t, pool, iss.ID); len(rows) != 0 {
		t.Fatalf("already-on-target: release_id rows = %d, want 0", len(rows))
	}
	if n := countIssueUpdatedDeliveries(t, pool, whID, iss.ID); n != 0 {
		t.Fatalf("already-on-target: deliveries = %d, want 0", n)
	}
	if n := countEventsOfType(t, fake, EventIssueUpdated); n != 0 {
		t.Fatalf("already-on-target: issue.updated events = %d, want 0", n)
	}
	// The pre-existing release.updated still fires, unchanged.
	if n := countEventsOfType(t, fake, EventReleaseUpdated); n != 1 {
		t.Fatalf("already-on-target: release.updated events = %d, want 1", n)
	}
}

func TestReleaseBulkAssignMoveWritesOldRelease(t *testing.T) {
	ctx, pool, slug, ident, actor := setupReleaseTest(t)
	from, err := CreateRelease(ctx, pool, slug, ident, actor, ReleaseInput{Name: "v5.2a"})
	if err != nil {
		t.Fatalf("CreateRelease from: %v", err)
	}
	to, err := CreateRelease(ctx, pool, slug, ident, actor, ReleaseInput{Name: "v5.2b"})
	if err != nil {
		t.Fatalf("CreateRelease to: %v", err)
	}
	iss := createTestIssue(t, pool, slug, ident, actor, "Mover")
	parkIssueOnRelease(t, pool, iss.ID, from.ID)
	whID := createIssueUpdatedWebhook(t, ctx, pool, slug, actor)
	fake := installCapturingBroadcaster(t)

	if err := AssignReleaseIssues(ctx, pool, slug, ident, actor, to.ID, []string{iss.ID}); err != nil {
		t.Fatalf("AssignReleaseIssues: %v", err)
	}
	assertReleaseActivityRow(t, pool, iss.ID, &from.ID, &to.ID)
	assertIssueUpdatedDelivery(t, pool, whID, iss.ID)
	assertIssueUpdatedIDs(t, fake, iss.ID)
}

func TestReleaseBulkUnassignWritesTrail(t *testing.T) {
	ctx, pool, slug, ident, actor := setupReleaseTest(t)
	target, err := CreateRelease(ctx, pool, slug, ident, actor, ReleaseInput{Name: "v5.3"})
	if err != nil {
		t.Fatalf("CreateRelease: %v", err)
	}
	iss := createTestIssue(t, pool, slug, ident, actor, "Member")
	parkIssueOnRelease(t, pool, iss.ID, target.ID)
	whID := createIssueUpdatedWebhook(t, ctx, pool, slug, actor)
	fake := installCapturingBroadcaster(t)

	if err := UnassignReleaseIssues(ctx, pool, slug, ident, actor, target.ID, []string{iss.ID}); err != nil {
		t.Fatalf("UnassignReleaseIssues: %v", err)
	}
	assertReleaseActivityRow(t, pool, iss.ID, &target.ID, nil)
	assertIssueUpdatedDelivery(t, pool, whID, iss.ID)
	assertIssueUpdatedIDs(t, fake, iss.ID)
	if n := countEventsOfType(t, fake, EventReleaseUpdated); n != 1 {
		t.Fatalf("release.updated events = %d, want 1", n)
	}
}

func TestReleaseBulkUnassignNonMemberWritesNothing(t *testing.T) {
	ctx, pool, slug, ident, actor := setupReleaseTest(t)
	target, err := CreateRelease(ctx, pool, slug, ident, actor, ReleaseInput{Name: "v5.4"})
	if err != nil {
		t.Fatalf("CreateRelease: %v", err)
	}
	iss := createTestIssue(t, pool, slug, ident, actor, "Not a member")
	// release_id is NULL: not on the release.
	whID := createIssueUpdatedWebhook(t, ctx, pool, slug, actor)
	fake := installCapturingBroadcaster(t)

	if err := UnassignReleaseIssues(ctx, pool, slug, ident, actor, target.ID, []string{iss.ID}); err != nil {
		t.Fatalf("UnassignReleaseIssues: %v", err)
	}
	if rows := releaseIDActivityRows(t, pool, iss.ID); len(rows) != 0 {
		t.Fatalf("non-member: release_id rows = %d, want 0", len(rows))
	}
	if n := countIssueUpdatedDeliveries(t, pool, whID, iss.ID); n != 0 {
		t.Fatalf("non-member: deliveries = %d, want 0", n)
	}
	if n := countEventsOfType(t, fake, EventIssueUpdated); n != 0 {
		t.Fatalf("non-member: issue.updated events = %d, want 0", n)
	}
	// The pre-existing release.updated still fires, unchanged.
	if n := countEventsOfType(t, fake, EventReleaseUpdated); n != 1 {
		t.Fatalf("non-member: release.updated events = %d, want 1", n)
	}
}

func TestReleaseDeleteReassignWritesTrail(t *testing.T) {
	ctx, pool, slug, ident, actor := setupReleaseTest(t)
	doomed, err := CreateRelease(ctx, pool, slug, ident, actor, ReleaseInput{Name: "v5.5-doomed"})
	if err != nil {
		t.Fatalf("CreateRelease doomed: %v", err)
	}
	target, err := CreateRelease(ctx, pool, slug, ident, actor, ReleaseInput{Name: "v5.5-target"})
	if err != nil {
		t.Fatalf("CreateRelease target: %v", err)
	}
	iss1 := createTestIssue(t, pool, slug, ident, actor, "Moved one")
	iss2 := createTestIssue(t, pool, slug, ident, actor, "Moved two")
	parkIssueOnRelease(t, pool, iss1.ID, doomed.ID)
	parkIssueOnRelease(t, pool, iss2.ID, doomed.ID)
	whID := createIssueUpdatedWebhook(t, ctx, pool, slug, actor)
	fake := installCapturingBroadcaster(t)

	if err := DeleteRelease(ctx, pool, slug, ident, actor, doomed.ID, &target.ID, false); err != nil {
		t.Fatalf("DeleteRelease reassign: %v", err)
	}
	for _, iss := range []*Issue{iss1, iss2} {
		assertReleaseActivityRow(t, pool, iss.ID, &doomed.ID, &target.ID)
		assertIssueUpdatedDelivery(t, pool, whID, iss.ID)
	}
	assertIssueUpdatedIDs(t, fake, iss1.ID, iss2.ID)
	if n := countEventsOfType(t, fake, EventReleaseUpdated); n != 1 {
		t.Fatalf("release.updated events = %d, want 1", n)
	}
}

func TestReleaseDeleteForceWritesTrail(t *testing.T) {
	ctx, pool, slug, ident, actor := setupReleaseTest(t)
	doomed, err := CreateRelease(ctx, pool, slug, ident, actor, ReleaseInput{Name: "v5.6-doomed"})
	if err != nil {
		t.Fatalf("CreateRelease: %v", err)
	}
	iss := createTestIssue(t, pool, slug, ident, actor, "Detached")
	parkIssueOnRelease(t, pool, iss.ID, doomed.ID)
	whID := createIssueUpdatedWebhook(t, ctx, pool, slug, actor)
	fake := installCapturingBroadcaster(t)

	if err := DeleteRelease(ctx, pool, slug, ident, actor, doomed.ID, nil, true); err != nil {
		t.Fatalf("DeleteRelease force: %v", err)
	}
	assertReleaseActivityRow(t, pool, iss.ID, &doomed.ID, nil)
	assertIssueUpdatedDelivery(t, pool, whID, iss.ID)
	assertIssueUpdatedIDs(t, fake, iss.ID)
	if n := countEventsOfType(t, fake, EventReleaseUpdated); n != 1 {
		t.Fatalf("release.updated events = %d, want 1", n)
	}
}

func TestReleaseDeleteConflictWritesNothing(t *testing.T) {
	ctx, pool, slug, ident, actor := setupReleaseTest(t)
	doomed, err := CreateRelease(ctx, pool, slug, ident, actor, ReleaseInput{Name: "v5.7-doomed"})
	if err != nil {
		t.Fatalf("CreateRelease: %v", err)
	}
	iss := createTestIssue(t, pool, slug, ident, actor, "Guarded")
	parkIssueOnRelease(t, pool, iss.ID, doomed.ID)
	whID := createIssueUpdatedWebhook(t, ctx, pool, slug, actor)
	fake := installCapturingBroadcaster(t)

	if err := DeleteRelease(ctx, pool, slug, ident, actor, doomed.ID, nil, false); !errors.Is(err, ErrReleaseHasIssues) {
		t.Fatalf("DeleteRelease: err = %v, want ErrReleaseHasIssues", err)
	}
	if rows := releaseIDActivityRows(t, pool, iss.ID); len(rows) != 0 {
		t.Fatalf("409 delete: release_id rows = %d, want 0", len(rows))
	}
	if n := countIssueUpdatedDeliveries(t, pool, whID, iss.ID); n != 0 {
		t.Fatalf("409 delete: deliveries = %d, want 0", n)
	}
	assertZeroEvents(t, fake)
}
