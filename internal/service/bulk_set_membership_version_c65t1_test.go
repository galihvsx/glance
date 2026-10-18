package service

// BulkSet membership versions (C65T1): a BulkSetIssues label/assignee
// replace that REALLY changes the set — setFields carries "labels" /
// "assignees", computed by bulkSetChangedSetFieldsTx — owes exactly
// one version per item carrying the post-mutation row. Membership is
// not a row column, so neither updateIssueTx's snapshot (patch-only
// row changes) nor the C50T0 post-cascade guard (cascade-moved rows)
// could ever see it; the issue_activities 'labels'/'assignees' rows
// record the old/new sets, so the snapshot stays the plain
// post-mutation row (DisplayID derived, like every call site).
//
// Exactly-one rule: no membership version when the item already got a
// version this pass — updateIssueTx snapshotted (a row field changed
// too; patchFields mirrors its sets exactly) or the C50T0 guard fired
// (a cascade moved the row). No version on no-op set fields
// (identical sets → setFields carries neither field). The "empty
// union → no webhook" contract is untouched.

import (
	"strconv"
	"strings"
	"testing"
)

// c65t1AssertMembershipSnapshot asserts the newest version carries the
// live post-mutation row: same content as the live row
// (issueRowContentEqual semantics) and the derived DisplayID every
// snapshot call site stamps.
func c65t1AssertMembershipSnapshot(t *testing.T, fx *linkedIssueFixture, issueID string) {
	t.Helper()
	live := c65LiveRow(t, fx.pool, issueID)
	snap := c65LatestSnapshot(t, fx.pool, fx.slug, fx.ident, issueID, fx.actor)
	if !issueRowContentEqual(&snap, live) {
		t.Fatalf("latest snapshot row content differs from the live row")
	}
	if want := strings.ToUpper(fx.ident) + "-" + strconv.Itoa(live.SequenceID); snap.DisplayID != want {
		t.Fatalf("snapshot DisplayID = %q, want derived %q", snap.DisplayID, want)
	}
}

func c65t1SecondIssue(t *testing.T, fx *linkedIssueFixture, name string) string {
	t.Helper()
	iss, err := CreateIssue(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		CreateIssueInput{Name: name, StateID: &fx.backlogID})
	if err != nil {
		t.Fatalf("CreateIssue %q: %v", name, err)
	}
	return iss.ID
}

func c65t1Member(t *testing.T, fx *linkedIssueFixture, tag string) string {
	t.Helper()
	member := createTestUser(t, fx.pool, uniqueTestEmail(tag))
	addTestMember(t, fx.pool, fx.slug, fx.actor, member, RoleMember)
	return member
}

// 1. Labels-only bulk set with a REAL change → exactly one version per
// item (on top of the create version), carrying the live row.
func TestC65T1BulkSetLabelsOnlyRealChangeSnapshotsOneVersionPerItem(t *testing.T) {
	fx := setupLinkedIssue(t)
	second := c65t1SecondIssue(t, fx, "second")
	for _, id := range []string{fx.iss.ID, second} {
		if got := versionCount(t, fx, id); got != 1 {
			t.Fatalf("versions before BulkSet = %d, want 1 (create only)", got)
		}
	}
	labelID := createBulkSetLabel(t, fx, "c65t1-label")

	labelIDs := []string{labelID}
	if _, _, err := BulkSetIssues(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		[]string{fx.iss.ID, second}, BulkIssueSet{LabelIDs: &labelIDs}); err != nil {
		t.Fatalf("BulkSetIssues labels: %v", err)
	}

	for _, id := range []string{fx.iss.ID, second} {
		if got := versionCount(t, fx, id); got != 2 {
			t.Fatalf("versions = %d, want 2 (create + membership)", got)
		}
		c65t1AssertMembershipSnapshot(t, fx, id)
	}
}

// 2. Labels-only bulk set with IDENTICAL sets → zero new versions.
func TestC65T1BulkSetLabelsOnlyIdenticalSetsSnapshotsNothing(t *testing.T) {
	fx := setupLinkedIssue(t)
	labelID := createBulkSetLabel(t, fx, "c65t1-keep")
	labelIDs := []string{labelID}
	if _, _, err := BulkSetIssues(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		[]string{fx.iss.ID}, BulkIssueSet{LabelIDs: &labelIDs}); err != nil {
		t.Fatalf("BulkSetIssues labels setup: %v", err)
	}
	if got := versionCount(t, fx, fx.iss.ID); got != 2 {
		t.Fatalf("versions after setup = %d, want 2 (create + membership)", got)
	}

	// Same set again: no set change, no version.
	if _, _, err := BulkSetIssues(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		[]string{fx.iss.ID}, BulkIssueSet{LabelIDs: &labelIDs}); err != nil {
		t.Fatalf("BulkSetIssues labels again: %v", err)
	}
	if got := versionCount(t, fx, fx.iss.ID); got != 2 {
		t.Fatalf("versions = %d, want 2 (identical-set no-op must not snapshot)", got)
	}
}

// 3. Assignees-only bulk set with a REAL change → exactly one version.
func TestC65T1BulkSetAssigneesOnlyRealChangeSnapshotsOneVersion(t *testing.T) {
	fx := setupLinkedIssue(t)
	member := c65t1Member(t, fx, "bulk-assignee")
	if got := versionCount(t, fx, fx.iss.ID); got != 1 {
		t.Fatalf("versions before BulkSet = %d, want 1 (create only)", got)
	}

	if _, _, err := BulkSetIssues(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		[]string{fx.iss.ID}, BulkIssueSet{AssigneeID: PatchField[string]{Set: true, Value: &member}}); err != nil {
		t.Fatalf("BulkSetIssues assignee: %v", err)
	}
	if got := versionCount(t, fx, fx.iss.ID); got != 2 {
		t.Fatalf("versions = %d, want 2 (create + membership)", got)
	}
	c65t1AssertMembershipSnapshot(t, fx, fx.iss.ID)
}

// 4. Row-field (priority) + labels bulk set → exactly one version per
// item — the row one from updateIssueTx, no double membership
// snapshot.
func TestC65T1BulkSetPriorityPlusLabelsSnapshotsExactlyOneVersion(t *testing.T) {
	fx := setupLinkedIssue(t)
	labelID := createBulkSetLabel(t, fx, "c65t1-mixed")

	labelIDs := []string{labelID}
	if _, _, err := BulkSetIssues(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		[]string{fx.iss.ID}, BulkIssueSet{Priority: autoIntPtr(1), LabelIDs: &labelIDs}); err != nil {
		t.Fatalf("BulkSetIssues priority+labels: %v", err)
	}

	if got := versionCount(t, fx, fx.iss.ID); got != 2 {
		t.Fatalf("versions = %d, want 2 (create + the single row version, no double)", got)
	}
	snap := latestVersionSnapshot(t, fx, fx.iss.ID)
	if snap.Priority != 1 {
		t.Fatalf("latest snapshot Priority = %d, want row-set 1", snap.Priority)
	}
}

// 5. Label change firing a set_state automation cascade (row moves) →
// exactly one version per item — the post-cascade one, never two.
func TestC65T1BulkSetLabelsCascadeSnapshotsExactlyOneVersionPerItem(t *testing.T) {
	fx := setupLinkedIssue(t)
	bulkSetLabelsChangedRule(t, fx, "c65t1 finisher",
		[]AutomationAction{{Type: "set_state", StateID: &fx.doneID}})
	second := c65t1SecondIssue(t, fx, "cascade second")
	labelID := createBulkSetLabel(t, fx, "c65t1-cascade-label")

	labelIDs := []string{labelID}
	if _, _, err := BulkSetIssues(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		[]string{fx.iss.ID, second}, BulkIssueSet{LabelIDs: &labelIDs}); err != nil {
		t.Fatalf("BulkSetIssues labels: %v", err)
	}

	for _, id := range []string{fx.iss.ID, second} {
		if got := versionCount(t, fx, id); got != 2 {
			t.Fatalf("versions = %d, want 2 (create + cascade, no double-snapshot)", got)
		}
		snap := latestVersionSnapshot(t, fx, id)
		if snap.StateID != fx.doneID {
			t.Fatalf("latest snapshot StateID = %s, want post-cascade %s", snap.StateID, fx.doneID)
		}
	}
}

// 6. Full no-op bulk (same priority, same label set) → no new versions
// and no webhooks — the existing no-op contract stands.
func TestC65T1BulkSetNoOpWritesNoVersionsNoWebhooks(t *testing.T) {
	fx := setupLinkedIssue(t)
	whID := c56t1Webhook(t, fx)
	labelID := createBulkSetLabel(t, fx, "c65t1-noop")
	labelIDs := []string{labelID}
	if _, _, err := BulkSetIssues(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		[]string{fx.iss.ID}, BulkIssueSet{Priority: autoIntPtr(2), LabelIDs: &labelIDs}); err != nil {
		t.Fatalf("BulkSetIssues setup: %v", err)
	}
	before := versionCount(t, fx, fx.iss.ID)
	watermark := outboxWatermark(t, fx.pool)

	if _, _, err := BulkSetIssues(fx.ctx, fx.pool, fx.slug, fx.ident, fx.actor,
		[]string{fx.iss.ID}, BulkIssueSet{Priority: autoIntPtr(2), LabelIDs: &labelIDs}); err != nil {
		t.Fatalf("BulkSetIssues no-op: %v", err)
	}
	if got := versionCount(t, fx, fx.iss.ID); got != before {
		t.Fatalf("versions = %d, want %d (no-op bulk must not snapshot)", got, before)
	}
	if rows := webhookOutboxRows(t, fx.pool, watermark, whID, EventIssueUpdated); len(rows) != 0 {
		t.Fatalf("issue.updated rows after no-op BulkSet = %d, want 0", len(rows))
	}
}
