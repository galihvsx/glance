package service

// C5T8: atomic bulk-set tests. PATCH /issues/bulk applies ONE set to many
// issues in a single transaction: any invalid id or invalid set field
// aborts the whole batch (no partial application). Every issue gets the
// same activity + version rows a single-issue PATCH writes (via
// updateIssueTx). Real test database — no skips.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// bulkSetFixture builds a workspace with two issues and returns the
// fixture pieces a bulk-set test needs.
type bulkSetFixture struct {
	pool       *pgxpool.Pool
	slug       string
	identifier string
	actor      string
	issues     []string // issue ids
}

func newBulkSetFixture(t *testing.T) *bulkSetFixture {
	t.Helper()
	pool := newTestPool(t)
	migrateTestDB(t, pool)

	actor := createTestUser(t, pool, uniqueTestEmail("bulk-set"))
	ws := createTestWorkspace(t, pool, "Bulk Co", uniqueTestSlug("bulk-set"), actor)
	p := createTestProject(t, pool, ws.Slug, actor, "Engineering", uniqueTestIdentifier())
	a := createTestIssue(t, pool, ws.Slug, p.Identifier, actor, "First")
	b := createTestIssue(t, pool, ws.Slug, p.Identifier, actor, "Second")
	return &bulkSetFixture{
		pool:       pool,
		slug:       ws.Slug,
		identifier: p.Identifier,
		actor:      actor,
		issues:     []string{a.ID, b.ID},
	}
}

func todoStateID(t *testing.T, pool *pgxpool.Pool, wsSlug, identifier, actorID string) string {
	t.Helper()
	states, err := ListStates(context.Background(), pool, wsSlug, identifier, actorID)
	if err != nil {
		t.Fatalf("ListStates: %v", err)
	}
	for _, s := range states {
		if s.Name == "Todo" {
			return s.ID
		}
	}
	t.Fatal("no Todo state seeded")
	return ""
}

func issueStatePriority(t *testing.T, pool *pgxpool.Pool, issueID string) (string, int) {
	t.Helper()
	var sid string
	var pri int
	if err := pool.QueryRow(context.Background(),
		`SELECT state_id::text, priority FROM issues WHERE id = $1::uuid`, issueID).
		Scan(&sid, &pri); err != nil {
		t.Fatalf("issueStatePriority: %v", err)
	}
	return sid, pri
}

func issueLabelIDs(t *testing.T, pool *pgxpool.Pool, issueID string) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT label_id::text FROM issue_labels WHERE issue_id = $1::uuid ORDER BY label_id::text`, issueID)
	if err != nil {
		t.Fatalf("issueLabelIDs: %v", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("issueLabelIDs scan: %v", err)
		}
		ids = append(ids, id)
	}
	return ids
}

func issueAssigneeIDs(t *testing.T, pool *pgxpool.Pool, issueID string) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT user_id::text FROM issue_assignees WHERE issue_id = $1::uuid ORDER BY user_id::text`, issueID)
	if err != nil {
		t.Fatalf("issueAssigneeIDs: %v", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("issueAssigneeIDs scan: %v", err)
		}
		ids = append(ids, id)
	}
	return ids
}

func countVersions(t *testing.T, pool *pgxpool.Pool, issueID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM issue_versions WHERE issue_id = $1::uuid`, issueID).Scan(&n); err != nil {
		t.Fatalf("countVersions: %v", err)
	}
	return n
}

// TestBulkSetIssuesAppliesAllFields: all four set-fields land on every
// issue, with per-issue activity rows and a version snapshot each.
func TestBulkSetIssuesAppliesAllFields(t *testing.T) {
	f := newBulkSetFixture(t)
	ctx := context.Background()

	lbl, err := CreateLabel(ctx, f.pool, f.slug, f.identifier, f.actor, LabelInput{Name: "bulk-label"})
	if err != nil {
		t.Fatalf("CreateLabel: %v", err)
	}
	member := createTestUser(t, f.pool, uniqueTestEmail("bulk-assignee"))
	addTestMember(t, f.pool, f.slug, f.actor, member, RoleMember)
	todo := todoStateID(t, f.pool, f.slug, f.identifier, f.actor)

	labelIDs := []string{lbl.ID}
	updated, ids, err := BulkSetIssues(ctx, f.pool, f.slug, f.identifier, f.actor, f.issues, BulkIssueSet{
		StateID:    &todo,
		Priority:   intPtr(4),
		LabelIDs:   &labelIDs,
		AssigneeID: PatchField[string]{Set: true, Value: &member},
	})
	if err != nil {
		t.Fatalf("BulkSetIssues: %v", err)
	}
	if updated != 2 || len(ids) != 2 {
		t.Fatalf("updated = %d, ids = %v; want 2 and both issue ids", updated, ids)
	}
	for i, id := range f.issues {
		if ids[i] != id {
			t.Fatalf("issue_ids[%d] = %q, want %q", i, ids[i], id)
		}
		sid, pri := issueStatePriority(t, f.pool, id)
		if sid != todo || pri != 4 {
			t.Fatalf("issue %s: state=%q pri=%d, want state=%q pri=4", id, sid, pri, todo)
		}
		if got := issueLabelIDs(t, f.pool, id); len(got) != 1 || got[0] != lbl.ID {
			t.Fatalf("issue %s: labels = %v, want [%s]", id, got, lbl.ID)
		}
		if got := issueAssigneeIDs(t, f.pool, id); len(got) != 1 || got[0] != member {
			t.Fatalf("issue %s: assignees = %v, want [%s]", id, got, member)
		}
		// Activity rows mirror a single-issue PATCH: one per changed
		// field.
		for _, field := range []string{"state_id", "priority", "labels", "assignees"} {
			if n := countActivities(t, f.pool, id, field); n != 1 {
				t.Fatalf("issue %s: %q activity rows = %d, want 1", id, field, n)
			}
		}
		// One version snapshot per issue (created + bulk update).
		if n := countVersions(t, f.pool, id); n < 2 {
			t.Fatalf("issue %s: version rows = %d, want >= 2", id, n)
		}
	}
}

// TestBulkSetIssuesAtomicRollbackOnBadID: one unknown id aborts the whole
// batch — the valid issues are untouched (state + no new activity rows).
func TestBulkSetIssuesAtomicRollbackOnBadID(t *testing.T) {
	f := newBulkSetFixture(t)
	ctx := context.Background()

	todo := todoStateID(t, f.pool, f.slug, f.identifier, f.actor)
	before := map[string]int{}
	for _, id := range f.issues {
		before[id] = countActivities(t, f.pool, id, "")
	}

	ids := append([]string{}, f.issues...)
	ids = append(ids, "00000000-0000-0000-0000-000000000000")
	_, _, err := BulkSetIssues(ctx, f.pool, f.slug, f.identifier, f.actor, ids, BulkIssueSet{
		StateID:  &todo,
		Priority: intPtr(3),
	})
	if !errors.Is(err, ErrIssueNotFound) {
		t.Fatalf("err = %v, want ErrIssueNotFound", err)
	}
	backlog := backlogStateID(t, f.pool, f.slug, f.identifier, f.actor)
	for _, id := range f.issues {
		sid, pri := issueStatePriority(t, f.pool, id)
		if sid != backlog || pri != 0 {
			t.Fatalf("issue %s: state=%q pri=%d after abort, want untouched (state=%q pri=0)", id, sid, pri, backlog)
		}
		if n := countActivities(t, f.pool, id, ""); n != before[id] {
			t.Fatalf("issue %s: activity rows = %d after abort, want %d", id, n, before[id])
		}
	}
}

// TestBulkSetIssuesInvalidSetFieldAborts: a bad state_id aborts before
// any issue is touched.
func TestBulkSetIssuesInvalidSetFieldAborts(t *testing.T) {
	f := newBulkSetFixture(t)
	ctx := context.Background()

	bogus := "00000000-0000-0000-0000-000000000000"
	_, _, err := BulkSetIssues(ctx, f.pool, f.slug, f.identifier, f.actor, f.issues, BulkIssueSet{
		StateID:  &bogus,
		Priority: intPtr(2),
	})
	if !errors.Is(err, ErrInvalidState) {
		t.Fatalf("err = %v, want ErrInvalidState", err)
	}
	backlog := backlogStateID(t, f.pool, f.slug, f.identifier, f.actor)
	for _, id := range f.issues {
		sid, _ := issueStatePriority(t, f.pool, id)
		if sid != backlog {
			t.Fatalf("issue %s moved despite abort", id)
		}
	}
}

// TestBulkSetIssuesInvalidLabelAborts: an unknown label id aborts the
// whole batch.
func TestBulkSetIssuesInvalidLabelAborts(t *testing.T) {
	f := newBulkSetFixture(t)
	ctx := context.Background()

	bogus := []string{"00000000-0000-0000-0000-000000000000"}
	todo := todoStateID(t, f.pool, f.slug, f.identifier, f.actor)
	_, _, err := BulkSetIssues(ctx, f.pool, f.slug, f.identifier, f.actor, f.issues, BulkIssueSet{
		StateID:  &todo,
		LabelIDs: &bogus,
	})
	if !errors.Is(err, ErrLabelNotFound) {
		t.Fatalf("err = %v, want ErrLabelNotFound", err)
	}
	backlog := backlogStateID(t, f.pool, f.slug, f.identifier, f.actor)
	for _, id := range f.issues {
		sid, _ := issueStatePriority(t, f.pool, id)
		if sid != backlog {
			t.Fatalf("issue %s moved despite label abort", id)
		}
	}
}

// TestBulkSetIssuesAssigneeNotMemberAborts: assigning a non-member
// aborts the whole batch.
func TestBulkSetIssuesAssigneeNotMemberAborts(t *testing.T) {
	f := newBulkSetFixture(t)
	ctx := context.Background()

	outsider := createTestUser(t, f.pool, uniqueTestEmail("bulk-outsider"))
	todo := todoStateID(t, f.pool, f.slug, f.identifier, f.actor)
	_, _, err := BulkSetIssues(ctx, f.pool, f.slug, f.identifier, f.actor, f.issues, BulkIssueSet{
		StateID:    &todo,
		AssigneeID: PatchField[string]{Set: true, Value: &outsider},
	})
	if !errors.Is(err, ErrAssigneeNotMember) {
		t.Fatalf("err = %v, want ErrAssigneeNotMember", err)
	}
	backlog := backlogStateID(t, f.pool, f.slug, f.identifier, f.actor)
	for _, id := range f.issues {
		sid, _ := issueStatePriority(t, f.pool, id)
		if sid != backlog {
			t.Fatalf("issue %s moved despite assignee abort", id)
		}
	}
}

// TestBulkSetIssuesBadPriorityAborts: out-of-range priority aborts.
func TestBulkSetIssuesBadPriorityAborts(t *testing.T) {
	f := newBulkSetFixture(t)
	ctx := context.Background()

	_, _, err := BulkSetIssues(ctx, f.pool, f.slug, f.identifier, f.actor, f.issues, BulkIssueSet{
		Priority: intPtr(9),
	})
	if !errors.Is(err, ErrInvalidPriority) {
		t.Fatalf("err = %v, want ErrInvalidPriority", err)
	}
}

// TestBulkSetIssuesTooManyIDs: 101 ids → ErrBulkTooManyIDs (the DoS cap).
func TestBulkSetIssuesTooManyIDs(t *testing.T) {
	f := newBulkSetFixture(t)
	ctx := context.Background()

	ids := make([]string, 101)
	for i := range ids {
		ids[i] = f.issues[0]
	}
	_, _, err := BulkSetIssues(ctx, f.pool, f.slug, f.identifier, f.actor, ids, BulkIssueSet{
		Priority: intPtr(1),
	})
	if !errors.Is(err, ErrBulkTooManyIDs) {
		t.Fatalf("err = %v, want ErrBulkTooManyIDs", err)
	}
}

// TestBulkSetIssuesEmptyIDs: no ids → ErrBulkEmptyIDs.
func TestBulkSetIssuesEmptyIDs(t *testing.T) {
	f := newBulkSetFixture(t)
	ctx := context.Background()

	_, _, err := BulkSetIssues(ctx, f.pool, f.slug, f.identifier, f.actor, nil, BulkIssueSet{
		Priority: intPtr(1),
	})
	if !errors.Is(err, ErrBulkEmptyIDs) {
		t.Fatalf("err = %v, want ErrBulkEmptyIDs", err)
	}
}

// TestBulkSetIssuesNothingToSet: an empty set → ErrNothingToUpdate.
func TestBulkSetIssuesNothingToSet(t *testing.T) {
	f := newBulkSetFixture(t)
	ctx := context.Background()

	_, _, err := BulkSetIssues(ctx, f.pool, f.slug, f.identifier, f.actor, f.issues, BulkIssueSet{})
	if !errors.Is(err, ErrNothingToUpdate) {
		t.Fatalf("err = %v, want ErrNothingToUpdate", err)
	}
}

// TestBulkSetIssuesGuestForbidden: guests may not bulk-set.
func TestBulkSetIssuesGuestForbidden(t *testing.T) {
	f := newBulkSetFixture(t)
	ctx := context.Background()

	guest := createTestUser(t, f.pool, uniqueTestEmail("bulk-guest"))
	addTestMember(t, f.pool, f.slug, f.actor, guest, RoleGuest)
	_, _, err := BulkSetIssues(ctx, f.pool, f.slug, f.identifier, guest, f.issues, BulkIssueSet{
		Priority: intPtr(1),
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
}

// TestBulkSetIssuesClearAssignee: explicit null assignee_id clears every
// assignee on the issue.
func TestBulkSetIssuesClearAssignee(t *testing.T) {
	f := newBulkSetFixture(t)
	ctx := context.Background()

	member := createTestUser(t, f.pool, uniqueTestEmail("bulk-clear-assignee"))
	addTestMember(t, f.pool, f.slug, f.actor, member, RoleMember)
	for _, id := range f.issues {
		if err := AssignAssignee(ctx, f.pool, f.slug, f.identifier, id, member, f.actor); err != nil {
			t.Fatalf("AssignAssignee: %v", err)
		}
	}

	updated, _, err := BulkSetIssues(ctx, f.pool, f.slug, f.identifier, f.actor, f.issues, BulkIssueSet{
		AssigneeID: PatchField[string]{Set: true, Value: nil},
	})
	if err != nil {
		t.Fatalf("BulkSetIssues: %v", err)
	}
	if updated != 2 {
		t.Fatalf("updated = %d, want 2", updated)
	}
	for _, id := range f.issues {
		if got := issueAssigneeIDs(t, f.pool, id); len(got) != 0 {
			t.Fatalf("issue %s: assignees = %v, want cleared", id, got)
		}
		if n := countActivities(t, f.pool, id, "assignees"); n != 1 {
			t.Fatalf("issue %s: assignees activity rows = %d, want 1", id, n)
		}
	}
}

// TestBulkSetIssuesReplaceLabels: a second bulk-set replaces the whole
// label set (empty array clears), writing the diff as activity rows.
func TestBulkSetIssuesReplaceLabels(t *testing.T) {
	f := newBulkSetFixture(t)
	ctx := context.Background()

	one, err := CreateLabel(ctx, f.pool, f.slug, f.identifier, f.actor, LabelInput{Name: "bulk-one"})
	if err != nil {
		t.Fatalf("CreateLabel one: %v", err)
	}
	two, err := CreateLabel(ctx, f.pool, f.slug, f.identifier, f.actor, LabelInput{Name: "bulk-two"})
	if err != nil {
		t.Fatalf("CreateLabel two: %v", err)
	}

	first := []string{one.ID, two.ID}
	if _, _, err := BulkSetIssues(ctx, f.pool, f.slug, f.identifier, f.actor, f.issues[:1], BulkIssueSet{LabelIDs: &first}); err != nil {
		t.Fatalf("BulkSetIssues labels: %v", err)
	}
	if got := issueLabelIDs(t, f.pool, f.issues[0]); len(got) != 2 {
		t.Fatalf("labels = %v, want 2", got)
	}
	// Activity row carries the old/new id arrays as JSON.
	var oldRaw, newRaw []byte
	if err := f.pool.QueryRow(ctx,
		`SELECT old_value, new_value FROM issue_activities
		  WHERE issue_id = $1::uuid AND field = 'labels'`, f.issues[0]).
		Scan(&oldRaw, &newRaw); err != nil {
		t.Fatalf("read labels activity: %v", err)
	}
	var oldIDs, newIDs []string
	if err := json.Unmarshal(oldRaw, &oldIDs); err != nil {
		t.Fatalf("unmarshal old labels: %v", err)
	}
	if err := json.Unmarshal(newRaw, &newIDs); err != nil {
		t.Fatalf("unmarshal new labels: %v", err)
	}
	if len(oldIDs) != 0 || len(newIDs) != 2 {
		t.Fatalf("labels activity old=%v new=%v, want empty → 2", oldIDs, newIDs)
	}

	empty := []string{}
	if _, _, err := BulkSetIssues(ctx, f.pool, f.slug, f.identifier, f.actor, f.issues[:1], BulkIssueSet{LabelIDs: &empty}); err != nil {
		t.Fatalf("BulkSetIssues clear labels: %v", err)
	}
	if got := issueLabelIDs(t, f.pool, f.issues[0]); len(got) != 0 {
		t.Fatalf("labels = %v, want cleared", got)
	}
}

// TestBulkSetIssuesInvalidUUIDAborts: a malformed id aborts like an
// unknown one — no partial application.
func TestBulkSetIssuesInvalidUUIDAborts(t *testing.T) {
	f := newBulkSetFixture(t)
	ctx := context.Background()

	_, _, err := BulkSetIssues(ctx, f.pool, f.slug, f.identifier, f.actor,
		[]string{f.issues[0], "not-a-uuid"}, BulkIssueSet{Priority: intPtr(2)})
	if !errors.Is(err, ErrIssueNotFound) {
		t.Fatalf("err = %v, want ErrIssueNotFound", err)
	}
	_, pri := issueStatePriority(t, f.pool, f.issues[0])
	if pri != 0 {
		t.Fatalf("issue priority = %d after abort, want 0", pri)
	}
}

// TestBulkSetIssuesCrossProjectAborts: an id from another project is not
// in this project's batch → whole batch aborts.
func TestBulkSetIssuesCrossProjectAborts(t *testing.T) {
	f := newBulkSetFixture(t)
	ctx := context.Background()

	p2 := createTestProject(t, f.pool, f.slug, f.actor, "Other", uniqueTestIdentifier())
	other := createTestIssue(t, f.pool, f.slug, p2.Identifier, f.actor, "Other project issue")

	_, _, err := BulkSetIssues(ctx, f.pool, f.slug, f.identifier, f.actor,
		[]string{f.issues[0], other.ID}, BulkIssueSet{Priority: intPtr(2)})
	if !errors.Is(err, ErrIssueNotFound) {
		t.Fatalf("err = %v, want ErrIssueNotFound", err)
	}
	_, pri := issueStatePriority(t, f.pool, f.issues[0])
	if pri != 0 {
		t.Fatalf("issue priority = %d after abort, want 0", pri)
	}
}

// TestBulkSetIssuesDeletedIDAborts: a soft-deleted id reads as missing.
func TestBulkSetIssuesDeletedIDAborts(t *testing.T) {
	f := newBulkSetFixture(t)
	ctx := context.Background()

	if err := DeleteIssue(ctx, f.pool, f.slug, f.identifier, f.issues[1], f.actor); err != nil {
		t.Fatalf("DeleteIssue: %v", err)
	}
	_, _, err := BulkSetIssues(ctx, f.pool, f.slug, f.identifier, f.actor, f.issues, BulkIssueSet{Priority: intPtr(2)})
	if !errors.Is(err, ErrIssueNotFound) {
		t.Fatalf("err = %v, want ErrIssueNotFound", err)
	}
	_, pri := issueStatePriority(t, f.pool, f.issues[0])
	if pri != 0 {
		t.Fatalf("issue priority = %d after abort, want 0", pri)
	}
}

// TestBulkSetIssuesDuplicateIDs: duplicate ids in the batch are
// deduplicated — the issue is updated once, reported once.
func TestBulkSetIssuesDuplicateIDs(t *testing.T) {
	f := newBulkSetFixture(t)
	ctx := context.Background()

	updated, ids, err := BulkSetIssues(ctx, f.pool, f.slug, f.identifier, f.actor,
		[]string{f.issues[0], f.issues[0]}, BulkIssueSet{Priority: intPtr(2)})
	if err != nil {
		t.Fatalf("BulkSetIssues: %v", err)
	}
	if updated != 1 || len(ids) != 1 || ids[0] != f.issues[0] {
		t.Fatalf("updated = %d, ids = %v; want 1 and the single issue", updated, ids)
	}
	if _, pri := issueStatePriority(t, f.pool, f.issues[0]); pri != 2 {
		t.Fatalf("priority = %d, want 2", pri)
	}
	// Single activity row, not two.
	if n := countActivities(t, f.pool, f.issues[0], "priority"); n != 1 {
		t.Fatalf("priority activity rows = %d, want 1", n)
	}
}

// TestBulkSetIssuesUntouchedFields: fields omitted from `set` keep their
// values and produce no activity rows.
func TestBulkSetIssuesUntouchedFields(t *testing.T) {
	f := newBulkSetFixture(t)
	ctx := context.Background()

	lbl, err := CreateLabel(ctx, f.pool, f.slug, f.identifier, f.actor, LabelInput{Name: "bulk-keep"})
	if err != nil {
		t.Fatalf("CreateLabel: %v", err)
	}
	if err := AssignLabel(ctx, f.pool, f.slug, f.identifier, f.issues[0], lbl.ID, f.actor); err != nil {
		t.Fatalf("AssignLabel: %v", err)
	}

	if _, _, err := BulkSetIssues(ctx, f.pool, f.slug, f.identifier, f.actor,
		f.issues[:1], BulkIssueSet{Priority: intPtr(1)}); err != nil {
		t.Fatalf("BulkSetIssues: %v", err)
	}
	if got := issueLabelIDs(t, f.pool, f.issues[0]); len(got) != 1 || got[0] != lbl.ID {
		t.Fatalf("labels = %v, want the pre-existing label untouched", got)
	}
	if n := countActivities(t, f.pool, f.issues[0], "labels"); n != 0 {
		t.Fatalf("labels activity rows = %d, want 0", n)
	}
}
