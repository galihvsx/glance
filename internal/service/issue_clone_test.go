package service

// CloneIssue tests (C8T4): the clone goes through the same CreateIssue
// validation/normalization path, copies content fields field-by-field,
// duplicates label links + custom field values, and deliberately does NOT
// copy comments, watchers, sub-issues, activity history, or attachments.
// All tests run against the real test database — no skips.

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// cloneTestSetup builds a workspace + project + member for clone tests.
func cloneTestSetup(t *testing.T, pool *pgxpool.Pool, prefix string) (wsSlug, ident, ownerID string) {
	t.Helper()
	owner := createTestUser(t, pool, uniqueTestEmail("clone-"+prefix))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-clone-"+prefix), owner)
	p := createTestProject(t, pool, ws.Slug, owner, "Engineering", uniqueTestIdentifier())
	return ws.Slug, p.Identifier, owner
}

func mustClone(t *testing.T, pool *pgxpool.Pool, wsSlug, ident, actorID, issueID string) *Issue {
	t.Helper()
	clone, err := CloneIssue(context.Background(), pool, wsSlug, ident, issueID, actorID)
	if err != nil {
		t.Fatalf("CloneIssue: %v", err)
	}
	return clone
}

func TestCloneIssueCopiesFields(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	wsSlug, ident, owner := cloneTestSetup(t, pool, "fields")

	cloner := createTestUser(t, pool, uniqueTestEmail("clone-cloner"))
	addTestMember(t, pool, wsSlug, owner, cloner, RoleMember)

	// A non-default state (clone must land in the same column, not backlog).
	states, err := ListStates(ctx, pool, wsSlug, ident, owner)
	if err != nil {
		t.Fatalf("ListStates: %v", err)
	}
	var todoID string
	for _, s := range states {
		if s.Group != "backlog" {
			todoID = s.ID
			break
		}
	}
	if todoID == "" {
		t.Fatal("no non-backlog state seeded")
	}

	// Estimate point.
	est, err := CreateEstimate(ctx, pool, wsSlug, ident, owner, EstimateInput{
		Name:   "Fib",
		Points: []EstimatePointInput{{Key: "S", Value: 1}},
	})
	if err != nil {
		t.Fatalf("CreateEstimate: %v", err)
	}

	// Parent issue: the clone must reference the SAME parent (sibling).
	parent := createTestIssue(t, pool, wsSlug, ident, owner, "Parent")

	desc := json.RawMessage(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"hello"}]}]}`)
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	target := time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)
	prio := 3
	src, err := CreateIssue(ctx, pool, wsSlug, ident, owner, CreateIssueInput{
		Name:            "Source issue",
		Description:     desc,
		Priority:        &prio,
		StateID:         &todoID,
		ParentID:        &parent.ID,
		StartDate:       &start,
		TargetDate:      &target,
		EstimatePointID: &est.Points[0].ID,
	})
	if err != nil {
		t.Fatalf("CreateIssue source: %v", err)
	}

	// Label link.
	lbl, err := CreateLabel(ctx, pool, wsSlug, ident, owner, LabelInput{Name: "bug", Color: strPtr("#ff0000")})
	if err != nil {
		t.Fatalf("CreateLabel: %v", err)
	}
	if err := AssignLabel(ctx, pool, wsSlug, ident, src.ID, lbl.ID, owner); err != nil {
		t.Fatalf("AssignLabel: %v", err)
	}

	// Custom field value.
	fld, err := CreateCustomField(ctx, pool, wsSlug, ident, owner, CustomFieldInput{Name: "Severity", FieldType: CustomFieldText})
	if err != nil {
		t.Fatalf("CreateCustomField: %v", err)
	}
	if _, err := SetCustomValues(ctx, pool, wsSlug, ident, owner, src.ID,
		map[string]json.RawMessage{fld.ID: json.RawMessage(`"critical"`)}); err != nil {
		t.Fatalf("SetCustomValues: %v", err)
	}

	clone := mustClone(t, pool, wsSlug, ident, cloner, src.ID)

	if clone.ID == src.ID {
		t.Fatal("clone has the same id as the source")
	}
	if clone.Name != src.Name {
		t.Fatalf("name = %q, want %q", clone.Name, src.Name)
	}
	if string(clone.Description) != string(src.Description) {
		t.Fatalf("description = %s, want %s", clone.Description, src.Description)
	}
	if clone.Priority != 3 {
		t.Fatalf("priority = %d, want 3", clone.Priority)
	}
	if clone.StateID != todoID {
		t.Fatalf("state_id = %q, want the source's state %q (not backlog)", clone.StateID, todoID)
	}
	if clone.ParentID == nil || *clone.ParentID != parent.ID {
		t.Fatalf("parent_id = %v, want %q", clone.ParentID, parent.ID)
	}
	if clone.StartDate == nil || !clone.StartDate.Equal(start) {
		t.Fatalf("start_date = %v, want %v", clone.StartDate, start)
	}
	if clone.TargetDate == nil || !clone.TargetDate.Equal(target) {
		t.Fatalf("target_date = %v, want %v", clone.TargetDate, target)
	}
	if clone.EstimatePointID == nil || *clone.EstimatePointID != est.Points[0].ID {
		t.Fatalf("estimate_point_id = %v, want %q", clone.EstimatePointID, est.Points[0].ID)
	}

	// Fresh sequence id: source was the 2nd issue (parent was 1st), clone is 3rd.
	if clone.SequenceID != src.SequenceID+1 {
		t.Fatalf("sequence_id = %d, want %d (fresh, not the source's)", clone.SequenceID, src.SequenceID+1)
	}
	if want := ident + "-" + strconv.Itoa(clone.SequenceID); clone.DisplayID != want {
		t.Fatalf("display_id = %q, want %q", clone.DisplayID, want)
	}
	if clone.CreatedBy != cloner {
		t.Fatalf("created_by = %q, want the cloning user %q", clone.CreatedBy, cloner)
	}
	if clone.IsDraft {
		t.Fatal("clone of a non-draft must not be a draft")
	}

	// Labels duplicated.
	var labelCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM issue_labels WHERE issue_id = $1::uuid`, clone.ID).Scan(&labelCount); err != nil {
		t.Fatalf("count clone labels: %v", err)
	}
	if labelCount != 1 {
		t.Fatalf("clone label links = %d, want 1", labelCount)
	}
	// Custom values duplicated.
	vals, err := GetIssueCustomValuesByProject(ctx, pool, src.ProjectID, clone.ID)
	if err != nil {
		t.Fatalf("GetIssueCustomValuesByProject clone: %v", err)
	}
	got, ok := vals[fld.ID]
	if !ok {
		t.Fatalf("clone custom values missing field %q: %v", fld.ID, vals)
	}
	if s, ok := got.Value.(string); !ok || s != "critical" {
		t.Fatalf("clone custom value = %v, want %q", got.Value, "critical")
	}
}

func TestCloneIssueDoesNotCopy(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	wsSlug, ident, owner := cloneTestSetup(t, pool, "nocopy")

	other := createTestUser(t, pool, uniqueTestEmail("clone-other"))
	addTestMember(t, pool, wsSlug, owner, other, RoleMember)

	src := createTestIssue(t, pool, wsSlug, ident, owner, "Source")

	// Comment.
	if _, err := CreateComment(ctx, pool, wsSlug, ident, src.ID, owner,
		json.RawMessage(`"a comment"`), nil); err != nil {
		t.Fatalf("CreateComment: %v", err)
	}
	// Watcher.
	if err := SubscribeIssue(ctx, pool, wsSlug, ident, src.ID, other); err != nil {
		t.Fatalf("SubscribeIssue: %v", err)
	}
	// Sub-issue: the clone must NOT inherit the source's children.
	if _, err := CreateIssue(ctx, pool, wsSlug, ident, owner, CreateIssueInput{
		Name: "Child", ParentID: &src.ID,
	}); err != nil {
		t.Fatalf("CreateIssue child: %v", err)
	}
	// Extra activity history beyond _created (PATCH writes an activity row).
	if _, err := UpdateIssue(ctx, pool, wsSlug, ident, src.ID, owner, IssuePatch{
		Name: strPtr("Source renamed"),
	}); err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}

	clone := mustClone(t, pool, wsSlug, ident, owner, src.ID)

	var n int
	checks := []struct {
		name string
		q    string
		args []any
		want int
	}{
		{"comments", `SELECT count(*) FROM comments WHERE issue_id = $1::uuid`, []any{clone.ID}, 0},
		{"subscribers", `SELECT count(*) FROM issue_subscribers WHERE issue_id = $1::uuid`, []any{clone.ID}, 0},
		{"children", `SELECT count(*) FROM issues WHERE parent_id = $1::uuid AND deleted_at IS NULL`, []any{clone.ID}, 0},
		{"activity", `SELECT count(*) FROM issue_activities WHERE issue_id = $1::uuid`, []any{clone.ID}, 1},
		{"attachments", `SELECT count(*) FROM attachments WHERE issue_id = $1::uuid`, []any{clone.ID}, 0},
	}
	for _, c := range checks {
		if err := pool.QueryRow(ctx, c.q, c.args...).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", c.name, err)
		}
		if n != c.want {
			t.Fatalf("%s on clone = %d, want %d", c.name, n, c.want)
		}
	}
}

func TestCloneIssueDraftStaysDraft(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	wsSlug, ident, owner := cloneTestSetup(t, pool, "draft")

	src, err := CreateIssue(ctx, pool, wsSlug, ident, owner, CreateIssueInput{
		Name: "Draft idea", IsDraft: true,
	})
	if err != nil {
		t.Fatalf("CreateIssue draft: %v", err)
	}
	clone := mustClone(t, pool, wsSlug, ident, owner, src.ID)
	if !clone.IsDraft {
		t.Fatal("clone of a draft must itself be a draft")
	}
}

func TestCloneIssueRoleGates(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	wsSlug, ident, owner := cloneTestSetup(t, pool, "gates")

	guest := createTestUser(t, pool, uniqueTestEmail("clone-guest"))
	addTestMember(t, pool, wsSlug, owner, guest, RoleGuest)
	stranger := createTestUser(t, pool, uniqueTestEmail("clone-stranger"))

	src := createTestIssue(t, pool, wsSlug, ident, owner, "Source")

	// Guest (5) is below the member (15) gate: same as issue create.
	if _, err := CloneIssue(ctx, pool, wsSlug, ident, src.ID, guest); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest clone err = %v, want ErrForbidden", err)
	}
	// Non-member: workspace not found boundary.
	if _, err := CloneIssue(ctx, pool, wsSlug, ident, src.ID, stranger); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stranger clone err = %v, want ErrNotFound", err)
	}
	// Unknown issue: 404 even for a member.
	if _, err := CloneIssue(ctx, pool, wsSlug, ident, "00000000-0000-0000-0000-000000000000", owner); !errors.Is(err, ErrIssueNotFound) {
		t.Fatalf("unknown issue clone err = %v, want ErrIssueNotFound", err)
	}
	// Cross-workspace: an issue from another workspace is invisible here.
	ws2 := createTestWorkspace(t, pool, "Other", uniqueTestSlug("acme-clone-ws2"), owner)
	p2 := createTestProject(t, pool, ws2.Slug, owner, "Eng", uniqueTestIdentifier())
	foreign := createTestIssue(t, pool, ws2.Slug, p2.Identifier, owner, "Foreign")
	if _, err := CloneIssue(ctx, pool, wsSlug, ident, foreign.ID, owner); !errors.Is(err, ErrIssueNotFound) {
		t.Fatalf("cross-workspace clone err = %v, want ErrIssueNotFound", err)
	}
}
