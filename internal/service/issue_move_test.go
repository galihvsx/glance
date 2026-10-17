package service

// MoveIssue tests (C24T0): moving an issue between projects of the same
// workspace — new display ID from the target project's issue_sequences,
// state remap by name (case-insensitive) with backlog fallback, label
// matching by name, estimate matching by value, whole-subtree moves,
// project-scoped membership/custom-value drops, intake + same-project
// 400s, and member(15)+ on both projects. All tests run against the real
// test database — no skips.

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// moveTestSetup builds a workspace + owner + two projects (source and
// target) for move tests.
func moveTestSetup(t *testing.T, pool *pgxpool.Pool, prefix string) (wsSlug, srcIdent, dstIdent, ownerID string) {
	t.Helper()
	owner := createTestUser(t, pool, uniqueTestEmail("move-"+prefix))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-move-"+prefix), owner)
	src := createTestProject(t, pool, ws.Slug, owner, "Source", uniqueTestIdentifier())
	dst := createTestProject(t, pool, ws.Slug, owner, "Target", uniqueTestIdentifier())
	return ws.Slug, src.Identifier, dst.Identifier, owner
}

func mustMove(t *testing.T, pool *pgxpool.Pool, wsSlug, srcIdent, issueID, actorID, dstIdent string) *MoveIssueResult {
	t.Helper()
	res, err := MoveIssue(context.Background(), pool, wsSlug, srcIdent, issueID, actorID, MoveIssueInput{TargetProjectIdentifier: dstIdent})
	if err != nil {
		t.Fatalf("MoveIssue: %v", err)
	}
	return res
}

// stateIDByName resolves a project state id by exact name.
func stateIDByName(t *testing.T, pool *pgxpool.Pool, wsSlug, ident, actorID, name string) string {
	t.Helper()
	states, err := ListStates(context.Background(), pool, wsSlug, ident, actorID)
	if err != nil {
		t.Fatalf("ListStates: %v", err)
	}
	for _, s := range states {
		if s.Name == name {
			return s.ID
		}
	}
	t.Fatalf("state %q not found in project %s", name, ident)
	return ""
}

func movedActivity(t *testing.T, pool *pgxpool.Pool, issueID string) map[string]any {
	t.Helper()
	var newVal []byte
	err := pool.QueryRow(context.Background(),
		`SELECT new_value FROM issue_activities WHERE issue_id = $1::uuid AND field = '_moved' ORDER BY created_at DESC LIMIT 1`,
		issueID).Scan(&newVal)
	if err != nil {
		t.Fatalf("moved activity: %v", err)
	}
	var v map[string]any
	if err := json.Unmarshal(newVal, &v); err != nil {
		t.Fatalf("decode moved activity: %v", err)
	}
	return v
}

func TestMoveIssueBasic(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	wsSlug, srcIdent, dstIdent, owner := moveTestSetup(t, pool, "basic")

	inProg := stateIDByName(t, pool, wsSlug, srcIdent, owner, "In Progress")
	iss, err := CreateIssue(ctx, pool, wsSlug, srcIdent, owner, CreateIssueInput{
		Name: "Move me", StateID: &inProg,
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	oldDisplay := iss.DisplayID

	res := mustMove(t, pool, wsSlug, srcIdent, iss.ID, owner, dstIdent)

	if res.NewDisplayID != dstIdent+"-1" {
		t.Fatalf("new_display_id = %q, want %q", res.NewDisplayID, dstIdent+"-1")
	}
	if res.Issue.DisplayID != dstIdent+"-1" {
		t.Fatalf("issue.display_id = %q, want %q", res.Issue.DisplayID, dstIdent+"-1")
	}
	if res.Issue.ProjectID == "" || res.Issue.ID != iss.ID {
		t.Fatalf("issue identity changed: %+v", res.Issue)
	}
	// UUID preserved; project changed.
	var projIdent string
	if err := pool.QueryRow(ctx,
		`SELECT p.identifier FROM issues i JOIN projects p ON p.id = i.project_id WHERE i.id = $1::uuid`,
		iss.ID).Scan(&projIdent); err != nil {
		t.Fatalf("project of moved issue: %v", err)
	}
	if projIdent != dstIdent {
		t.Fatalf("issue project = %q, want %q", projIdent, dstIdent)
	}
	// Source state name matched in target (same name, different row).
	dstInProg := stateIDByName(t, pool, wsSlug, dstIdent, owner, "In Progress")
	if res.Issue.StateID != dstInProg {
		t.Fatalf("state_id = %q, want target In Progress %q", res.Issue.StateID, dstInProg)
	}
	if res.StateRemapped {
		t.Fatal("state_remapped = true, want false (name matched)")
	}
	// Gone from the source project.
	if _, err := GetIssue(ctx, pool, wsSlug, srcIdent, iss.ID, owner); !errors.Is(err, ErrIssueNotFound) {
		t.Fatalf("GetIssue in source: err = %v, want ErrIssueNotFound", err)
	}
	// Activity entry "moved from OLD to NEW".
	act := movedActivity(t, pool, iss.ID)
	if act["from"] != oldDisplay || act["to"] != dstIdent+"-1" {
		t.Fatalf("moved activity = %v, want from=%q to=%q", act, oldDisplay, dstIdent+"-1")
	}
	if res.LabelsDropped == nil {
		t.Fatal("labels_dropped must serialize as [] not null")
	}
}

func TestMoveIssueStateRemapMatrix(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	wsSlug, srcIdent, dstIdent, owner := moveTestSetup(t, pool, "stateremap")

	// Source-only state: no name match in the target → backlog fallback.
	review, err := CreateState(ctx, pool, wsSlug, srcIdent, owner, StateInput{Name: "Review", Group: "unstarted", Color: "#fff"})
	if err != nil {
		t.Fatalf("CreateState: %v", err)
	}
	dstBacklog := stateIDByName(t, pool, wsSlug, dstIdent, owner, "Backlog")

	cases := []struct {
		name      string
		srcState  string // name of the source state to create the issue in
		wantName  string // expected target state name
		wantRemap bool
	}{
		{"exact match", "Todo", "Todo", false},
		{"case-insensitive match", "In Progress", "In Progress", false},
		{"fallback to backlog", "Review", "Backlog", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srcStateID := stateIDByName(t, pool, wsSlug, srcIdent, owner, tc.srcState)
			if tc.srcState == "Review" {
				srcStateID = review.ID
			}
			iss, err := CreateIssue(ctx, pool, wsSlug, srcIdent, owner, CreateIssueInput{
				Name: "remap " + tc.name, StateID: &srcStateID,
			})
			if err != nil {
				t.Fatalf("CreateIssue: %v", err)
			}
			res := mustMove(t, pool, wsSlug, srcIdent, iss.ID, owner, dstIdent)
			wantID := stateIDByName(t, pool, wsSlug, dstIdent, owner, tc.wantName)
			if tc.wantName == "Backlog" {
				wantID = dstBacklog
			}
			if res.Issue.StateID != wantID {
				t.Fatalf("state_id = %q, want %q (%s)", res.Issue.StateID, wantID, tc.wantName)
			}
			if res.StateRemapped != tc.wantRemap {
				t.Fatalf("state_remapped = %v, want %v", res.StateRemapped, tc.wantRemap)
			}
		})
	}
}

func TestMoveIssueSubtree(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	wsSlug, srcIdent, dstIdent, owner := moveTestSetup(t, pool, "subtree")

	parent := createTestIssue(t, pool, wsSlug, srcIdent, owner, "Parent")
	child, err := CreateIssue(ctx, pool, wsSlug, srcIdent, owner, CreateIssueInput{
		Name: "Child", ParentID: &parent.ID,
	})
	if err != nil {
		t.Fatalf("CreateIssue child: %v", err)
	}
	grand, err := CreateIssue(ctx, pool, wsSlug, srcIdent, owner, CreateIssueInput{
		Name: "Grandchild", ParentID: &child.ID,
	})
	if err != nil {
		t.Fatalf("CreateIssue grandchild: %v", err)
	}

	res := mustMove(t, pool, wsSlug, srcIdent, parent.ID, owner, dstIdent)
	if res.NewDisplayID != dstIdent+"-1" {
		t.Fatalf("root new_display_id = %q, want %q", res.NewDisplayID, dstIdent+"-1")
	}

	// Whole subtree moved, each with a fresh display ID, parent links
	// intact (UUIDs preserved).
	for i, id := range []string{parent.ID, child.ID, grand.ID} {
		got, err := GetIssue(ctx, pool, wsSlug, dstIdent, id, owner)
		if err != nil {
			t.Fatalf("GetIssue moved[%d]: %v", i, err)
		}
		if got.DisplayID != dstIdent+"-"+strconv.Itoa(i+1) {
			t.Fatalf("moved[%d].display_id = %q, want %q", i, got.DisplayID, dstIdent+"-"+strconv.Itoa(i+1))
		}
		act := movedActivity(t, pool, id)
		if act["to"] != got.DisplayID {
			t.Fatalf("moved[%d] activity to = %v, want %q", i, act["to"], got.DisplayID)
		}
	}
	gotChild, _ := GetIssue(ctx, pool, wsSlug, dstIdent, child.ID, owner)
	if gotChild.ParentID == nil || *gotChild.ParentID != parent.ID {
		t.Fatalf("child parent_id = %v, want %s", gotChild.ParentID, parent.ID)
	}
	gotGrand, _ := GetIssue(ctx, pool, wsSlug, dstIdent, grand.ID, owner)
	if gotGrand.ParentID == nil || *gotGrand.ParentID != child.ID {
		t.Fatalf("grandchild parent_id = %v, want %s", gotGrand.ParentID, child.ID)
	}
	// Nothing left behind in the source project.
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM issues WHERE project_id = (SELECT id FROM projects WHERE identifier = $1) AND deleted_at IS NULL`,
		srcIdent).Scan(&n); err != nil {
		t.Fatalf("count source issues: %v", err)
	}
	if n != 0 {
		t.Fatalf("source project still has %d live issues", n)
	}
}

func TestMoveIssueEstimateRemap(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	wsSlug, srcIdent, dstIdent, owner := moveTestSetup(t, pool, "estimate")

	srcEst, err := CreateEstimate(ctx, pool, wsSlug, srcIdent, owner, EstimateInput{
		Name: "Fib", Points: []EstimatePointInput{{Key: "S", Value: 1}, {Key: "M", Value: 2}},
	})
	if err != nil {
		t.Fatalf("CreateEstimate src: %v", err)
	}
	dstEst, err := CreateEstimate(ctx, pool, wsSlug, dstIdent, owner, EstimateInput{
		Name: "Points", Points: []EstimatePointInput{{Key: "Small", Value: 1}, {Key: "Large", Value: 5}},
	})
	if err != nil {
		t.Fatalf("CreateEstimate dst: %v", err)
	}
	var dstSmallID string
	for _, p := range dstEst.Points {
		if p.Value == 1 {
			dstSmallID = p.ID
		}
	}

	// Value match → remapped to the target scale point.
	sPoint := srcEst.Points[0].ID
	iss, err := CreateIssue(ctx, pool, wsSlug, srcIdent, owner, CreateIssueInput{
		Name: "est match", EstimatePointID: &sPoint,
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	res := mustMove(t, pool, wsSlug, srcIdent, iss.ID, owner, dstIdent)
	if res.Issue.EstimatePointID == nil || *res.Issue.EstimatePointID != dstSmallID {
		t.Fatalf("estimate_point_id = %v, want %s", res.Issue.EstimatePointID, dstSmallID)
	}
	if res.EstimateDropped {
		t.Fatal("estimate_dropped = true, want false (value matched)")
	}

	// No value match → null + estimate_dropped.
	mPoint := srcEst.Points[1].ID
	iss2, err := CreateIssue(ctx, pool, wsSlug, srcIdent, owner, CreateIssueInput{
		Name: "est drop", EstimatePointID: &mPoint,
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	res2 := mustMove(t, pool, wsSlug, srcIdent, iss2.ID, owner, dstIdent)
	if res2.Issue.EstimatePointID != nil {
		t.Fatalf("estimate_point_id = %v, want nil", res2.Issue.EstimatePointID)
	}
	if !res2.EstimateDropped {
		t.Fatal("estimate_dropped = false, want true")
	}
}

func TestMoveIssueDropReporting(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	wsSlug, srcIdent, dstIdent, owner := moveTestSetup(t, pool, "drops")

	iss := createTestIssue(t, pool, wsSlug, srcIdent, owner, "Drops")

	// Label (workspace-scoped): kept, never dropped.
	lbl, err := CreateLabel(ctx, pool, wsSlug, srcIdent, owner, LabelInput{Name: "bug", Color: strPtr("#ff0000")})
	if err != nil {
		t.Fatalf("CreateLabel: %v", err)
	}
	if err := AssignLabel(ctx, pool, wsSlug, srcIdent, iss.ID, lbl.ID, owner); err != nil {
		t.Fatalf("AssignLabel: %v", err)
	}

	// Custom field value (project-scoped): dropped.
	fld, err := CreateCustomField(ctx, pool, wsSlug, srcIdent, owner, CustomFieldInput{Name: "Severity", FieldType: CustomFieldText})
	if err != nil {
		t.Fatalf("CreateCustomField: %v", err)
	}
	if _, err := SetCustomValues(ctx, pool, wsSlug, srcIdent, owner, iss.ID,
		map[string]json.RawMessage{fld.ID: json.RawMessage(`"critical"`)}); err != nil {
		t.Fatalf("SetCustomValues: %v", err)
	}

	// Cycle + module + release memberships (project-scoped): dropped.
	cyc, err := CreateCycle(ctx, pool, wsSlug, srcIdent, owner, CycleInput{
		Name: "C1", StartDate: time.Now().Add(-24 * time.Hour), EndDate: time.Now().Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("CreateCycle: %v", err)
	}
	if err := AddCycleIssues(ctx, pool, wsSlug, srcIdent, owner, cyc.ID, []string{iss.ID}); err != nil {
		t.Fatalf("AddCycleIssues: %v", err)
	}
	mod, err := CreateModule(ctx, pool, wsSlug, srcIdent, owner, ModuleInput{Name: "M1"})
	if err != nil {
		t.Fatalf("CreateModule: %v", err)
	}
	if err := AddModuleIssues(ctx, pool, wsSlug, srcIdent, owner, mod.ID, []string{iss.ID}); err != nil {
		t.Fatalf("AddModuleIssues: %v", err)
	}
	rel, err := CreateRelease(ctx, pool, wsSlug, srcIdent, owner, ReleaseInput{Name: "R1"})
	if err != nil {
		t.Fatalf("CreateRelease: %v", err)
	}
	if err := AssignReleaseIssues(ctx, pool, wsSlug, srcIdent, owner, rel.ID, []string{iss.ID}); err != nil {
		t.Fatalf("AssignReleaseIssues: %v", err)
	}

	res := mustMove(t, pool, wsSlug, srcIdent, iss.ID, owner, dstIdent)

	if len(res.LabelsDropped) != 0 {
		t.Fatalf("labels_dropped = %v, want []", res.LabelsDropped)
	}
	var labelCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM issue_labels WHERE issue_id = $1::uuid`, iss.ID).Scan(&labelCount); err != nil {
		t.Fatalf("label count: %v", err)
	}
	if labelCount != 1 {
		t.Fatalf("issue_labels rows = %d, want 1 (kept)", labelCount)
	}
	if res.CustomValuesDropped != 1 {
		t.Fatalf("custom_values_dropped = %d, want 1", res.CustomValuesDropped)
	}
	var cvCount int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM issue_custom_values WHERE issue_id = $1::uuid`, iss.ID).Scan(&cvCount); err != nil {
		t.Fatalf("custom value count: %v", err)
	}
	if cvCount != 0 {
		t.Fatalf("issue_custom_values rows = %d, want 0", cvCount)
	}
	if res.MembershipsDropped.Cycles != 1 || res.MembershipsDropped.Modules != 1 || res.MembershipsDropped.Releases != 1 {
		t.Fatalf("memberships_dropped = %+v, want {1 1 1}", res.MembershipsDropped)
	}
	var relID *string
	if err := pool.QueryRow(ctx, `SELECT release_id::text FROM issues WHERE id = $1::uuid`, iss.ID).Scan(&relID); err != nil {
		t.Fatalf("release_id: %v", err)
	}
	if relID != nil {
		t.Fatalf("release_id = %v, want NULL after move", *relID)
	}
}

func TestMoveIssueKeepsSatellites(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	wsSlug, srcIdent, dstIdent, owner := moveTestSetup(t, pool, "satellites")

	iss := createTestIssue(t, pool, wsSlug, srcIdent, owner, "Satellites")
	watcher := createTestUser(t, pool, uniqueTestEmail("move-watcher"))
	addTestMember(t, pool, wsSlug, owner, watcher, RoleMember)

	// Comment, reaction, vote, assignee, subscriber, relation, link.
	content := json.RawMessage(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"keep me"}]}]}`)
	if _, err := CreateComment(ctx, pool, wsSlug, srcIdent, iss.ID, owner, content, nil); err != nil {
		t.Fatalf("CreateComment: %v", err)
	}
	if err := AddIssueReaction(ctx, pool, wsSlug, srcIdent, iss.ID, watcher, "👍"); err != nil {
		t.Fatalf("AddIssueReaction: %v", err)
	}
	if err := VoteIssue(ctx, pool, wsSlug, srcIdent, iss.ID, watcher); err != nil {
		t.Fatalf("VoteIssue: %v", err)
	}
	if err := AssignAssignee(ctx, pool, wsSlug, srcIdent, iss.ID, watcher, owner); err != nil {
		t.Fatalf("AssignAssignee: %v", err)
	}
	if err := SubscribeIssue(ctx, pool, wsSlug, srcIdent, iss.ID, watcher); err != nil {
		t.Fatalf("SubscribeIssue: %v", err)
	}
	other := createTestIssue(t, pool, wsSlug, srcIdent, owner, "Other")
	if err := CreateRelation(ctx, pool, wsSlug, srcIdent, iss.ID, owner, other.ID, "relates_to"); err != nil {
		t.Fatalf("CreateRelation: %v", err)
	}
	if _, err := CreateIssueLink(ctx, pool, wsSlug, srcIdent, iss.ID, owner, other.ID, "blocks"); err != nil {
		t.Fatalf("CreateIssueLink: %v", err)
	}
	// Attachment metadata row + time entry via direct SQL (metadata only).
	if _, err := pool.Exec(ctx,
		`INSERT INTO attachments (issue_id, filename, content_type, size_bytes, stored_path, uploaded_by)
		 VALUES ($1::uuid, 'a.txt', 'text/plain', 3, 'k', $2::uuid)`, iss.ID, owner); err != nil {
		t.Fatalf("insert attachment: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO time_entries (issue_id, user_id, started_at, ended_at) VALUES ($1::uuid, $2::uuid, now() - interval '1 hour', now())`,
		iss.ID, owner); err != nil {
		t.Fatalf("insert time entry: %v", err)
	}

	res := mustMove(t, pool, wsSlug, srcIdent, iss.ID, owner, dstIdent)
	_ = res

	checks := map[string]string{
		"comments":          `SELECT COUNT(*) FROM comments WHERE issue_id = $1::uuid`,
		"issue_reactions":   `SELECT COUNT(*) FROM issue_reactions WHERE issue_id = $1::uuid`,
		"issue_votes":       `SELECT COUNT(*) FROM issue_votes WHERE issue_id = $1::uuid`,
		"issue_assignees":   `SELECT COUNT(*) FROM issue_assignees WHERE issue_id = $1::uuid`,
		"issue_subscribers": `SELECT COUNT(*) FROM issue_subscribers WHERE issue_id = $1::uuid`,
		"issue_relations":   `SELECT COUNT(*) FROM issue_relations WHERE issue_id = $1::uuid OR related_issue_id = $1::uuid`,
		"issue_links":       `SELECT COUNT(*) FROM issue_links WHERE issue_id = $1::uuid OR target_issue_id = $1::uuid`,
		"attachments":       `SELECT COUNT(*) FROM attachments WHERE issue_id = $1::uuid`,
		"time_entries":      `SELECT COUNT(*) FROM time_entries WHERE issue_id = $1::uuid`,
	}
	for name, q := range checks {
		var n int
		if err := pool.QueryRow(ctx, q, iss.ID).Scan(&n); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if n == 0 {
			t.Fatalf("%s rows = 0 after move, want > 0", name)
		}
	}
}

func TestMoveIssueForbidden(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	wsSlug, srcIdent, dstIdent, owner := moveTestSetup(t, pool, "forbidden")

	guest := createTestUser(t, pool, uniqueTestEmail("move-guest"))
	addTestMember(t, pool, wsSlug, owner, guest, RoleGuest)

	iss := createTestIssue(t, pool, wsSlug, srcIdent, owner, "Guest move")
	_, err := MoveIssue(ctx, pool, wsSlug, srcIdent, iss.ID, guest, MoveIssueInput{TargetProjectIdentifier: dstIdent})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("MoveIssue guest: err = %v, want ErrForbidden", err)
	}
}

func TestMoveIssueIntake(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	wsSlug, srcIdent, dstIdent, owner := moveTestSetup(t, pool, "intake")

	iss, err := CreateIssue(ctx, pool, wsSlug, srcIdent, owner, CreateIssueInput{Name: "Intake item", Intake: true})
	if err != nil {
		t.Fatalf("CreateIssue intake: %v", err)
	}
	_, err = MoveIssue(ctx, pool, wsSlug, srcIdent, iss.ID, owner, MoveIssueInput{TargetProjectIdentifier: dstIdent})
	if !errors.Is(err, ErrMoveIntakeIssue) {
		t.Fatalf("MoveIssue intake: err = %v, want ErrMoveIntakeIssue", err)
	}
}

func TestMoveIssueSameProject(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	wsSlug, srcIdent, _, owner := moveTestSetup(t, pool, "sameproject")

	iss := createTestIssue(t, pool, wsSlug, srcIdent, owner, "Same project")
	_, err := MoveIssue(ctx, pool, wsSlug, srcIdent, iss.ID, owner, MoveIssueInput{TargetProjectIdentifier: srcIdent})
	if !errors.Is(err, ErrMoveSameProject) {
		t.Fatalf("MoveIssue same project: err = %v, want ErrMoveSameProject", err)
	}
}

func TestMoveIssueErrors(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	wsSlug, srcIdent, dstIdent, owner := moveTestSetup(t, pool, "errors")

	// Unknown issue → 404.
	_, err := MoveIssue(ctx, pool, wsSlug, srcIdent, "00000000-0000-0000-0000-000000000000", owner,
		MoveIssueInput{TargetProjectIdentifier: dstIdent})
	if !errors.Is(err, ErrIssueNotFound) {
		t.Fatalf("unknown issue: err = %v, want ErrIssueNotFound", err)
	}
	// Unknown target project → 404.
	iss := createTestIssue(t, pool, wsSlug, srcIdent, owner, "Target 404")
	_, err = MoveIssue(ctx, pool, wsSlug, srcIdent, iss.ID, owner,
		MoveIssueInput{TargetProjectIdentifier: "NOPE404"})
	if !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("unknown target: err = %v, want ErrProjectNotFound", err)
	}
	// Malformed target identifier → 400.
	_, err = MoveIssue(ctx, pool, wsSlug, srcIdent, iss.ID, owner,
		MoveIssueInput{TargetProjectIdentifier: "bad ident!"})
	if !errors.Is(err, ErrInvalidIdentifier) {
		t.Fatalf("bad target identifier: err = %v, want ErrInvalidIdentifier", err)
	}
	// Non-member actor → 404 (tenancy boundary, like every other read).
	outsider := createTestUser(t, pool, uniqueTestEmail("move-outsider"))
	_, err = MoveIssue(ctx, pool, wsSlug, srcIdent, iss.ID, outsider,
		MoveIssueInput{TargetProjectIdentifier: dstIdent})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-member: err = %v, want ErrNotFound", err)
	}
	// Case-insensitive target identifier still resolves.
	res := mustMove(t, pool, wsSlug, srcIdent, iss.ID, owner, strings.ToLower(dstIdent))
	if res.NewDisplayID != dstIdent+"-1" {
		t.Fatalf("lowercase target: new_display_id = %q", res.NewDisplayID)
	}
}

// stateIDActivity reads the single state_id activity row for an issue,
// failing unless there is exactly one. Values are the JSON-decoded
// old/new state ids.
func stateIDActivity(t *testing.T, pool *pgxpool.Pool, issueID string) (oldVal, newVal string) {
	t.Helper()
	var oldRaw, newRaw []byte
	rows, err := pool.Query(context.Background(),
		`SELECT old_value, new_value FROM issue_activities
		  WHERE issue_id = $1::uuid AND field = 'state_id' ORDER BY created_at, id`,
		issueID)
	if err != nil {
		t.Fatalf("read state_id activity: %v", err)
	}
	defer rows.Close()
	var pairs [][2]string
	for rows.Next() {
		if err := rows.Scan(&oldRaw, &newRaw); err != nil {
			t.Fatalf("scan state_id activity: %v", err)
		}
		var o, n string
		if err := json.Unmarshal(oldRaw, &o); err != nil {
			t.Fatalf("unmarshal old state_id: %v", err)
		}
		if err := json.Unmarshal(newRaw, &n); err != nil {
			t.Fatalf("unmarshal new state_id: %v", err)
		}
		pairs = append(pairs, [2]string{o, n})
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("state_id activity rows: %v", err)
	}
	if len(pairs) != 1 {
		t.Fatalf("state_id activity rows = %d, want exactly 1 (%v)", len(pairs), pairs)
	}
	return pairs[0][0], pairs[0][1]
}

// TestMoveIssueWritesStateIDActivityOnRemap (C47T0): a move whose state
// remaps writes exactly one state_id activity row (old = source state,
// new = remapped state) alongside the untouched _moved row. Covers both
// remap shapes: same-name match (different row id in the target
// project) and backlog fallback (no name match).
func TestMoveIssueWritesStateIDActivityOnRemap(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	wsSlug, srcIdent, dstIdent, owner := moveTestSetup(t, pool, "stateact")

	// Same-name match: source "In Progress" → target "In Progress"
	// (different row id), so state_id still changes.
	srcInProg := stateIDByName(t, pool, wsSlug, srcIdent, owner, "In Progress")
	iss, err := CreateIssue(ctx, pool, wsSlug, srcIdent, owner, CreateIssueInput{
		Name: "Move me", StateID: &srcInProg,
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if n := countActivities(t, pool, iss.ID, "state_id"); n != 0 {
		t.Fatalf("state_id rows before move = %d, want 0", n)
	}
	res := mustMove(t, pool, wsSlug, srcIdent, iss.ID, owner, dstIdent)
	dstInProg := stateIDByName(t, pool, wsSlug, dstIdent, owner, "In Progress")
	oldVal, newVal := stateIDActivity(t, pool, iss.ID)
	if oldVal != srcInProg || newVal != dstInProg {
		t.Fatalf("state_id activity = %s → %s, want %s → %s", oldVal, newVal, srcInProg, dstInProg)
	}
	if res.Issue.StateID != dstInProg {
		t.Fatalf("issue state_id = %q, want %q", res.Issue.StateID, dstInProg)
	}
	if n := countActivities(t, pool, iss.ID, "_moved"); n != 1 {
		t.Fatalf("_moved rows = %d, want 1 (untouched)", n)
	}

	// Backlog fallback: source-only state → target backlog.
	review, err := CreateState(ctx, pool, wsSlug, srcIdent, owner, StateInput{Name: "Review", Group: "unstarted", Color: "#fff"})
	if err != nil {
		t.Fatalf("CreateState: %v", err)
	}
	iss2, err := CreateIssue(ctx, pool, wsSlug, srcIdent, owner, CreateIssueInput{
		Name: "Move me 2", StateID: &review.ID,
	})
	if err != nil {
		t.Fatalf("CreateIssue 2: %v", err)
	}
	res2 := mustMove(t, pool, wsSlug, srcIdent, iss2.ID, owner, dstIdent)
	if !res2.StateRemapped {
		t.Fatal("state_remapped = false, want true (fallback)")
	}
	dstBacklog := stateIDByName(t, pool, wsSlug, dstIdent, owner, "Backlog")
	oldVal, newVal = stateIDActivity(t, pool, iss2.ID)
	if oldVal != review.ID || newVal != dstBacklog {
		t.Fatalf("state_id activity = %s → %s, want %s → %s", oldVal, newVal, review.ID, dstBacklog)
	}
	if n := countActivities(t, pool, iss2.ID, "_moved"); n != 1 {
		t.Fatalf("_moved rows = %d, want 1 (untouched)", n)
	}
}

// TestMoveIssueFailedMoveWritesNoActivity (C47T0): a failed move
// (validation error → tx rollback) writes no activity rows at all.
func TestMoveIssueFailedMoveWritesNoActivity(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	wsSlug, srcIdent, _, owner := moveTestSetup(t, pool, "stateactfail")

	iss := createTestIssue(t, pool, wsSlug, srcIdent, owner, "Doomed move")
	before := countActivities(t, pool, iss.ID, "")
	_, err := MoveIssue(ctx, pool, wsSlug, srcIdent, iss.ID, owner,
		MoveIssueInput{TargetProjectIdentifier: "NOPE404"})
	if !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("unknown target: err = %v, want ErrProjectNotFound", err)
	}
	if got := countActivities(t, pool, iss.ID, ""); got != before {
		t.Fatalf("activity rows after failed move = %d, want %d (no partial writes)", got, before)
	}
	if n := countActivities(t, pool, iss.ID, "state_id"); n != 0 {
		t.Fatalf("state_id rows after failed move = %d, want 0", n)
	}
	if n := countActivities(t, pool, iss.ID, "_moved"); n != 0 {
		t.Fatalf("_moved rows after failed move = %d, want 0", n)
	}
	// The issue is still in the source project, untouched.
	if _, err := GetIssue(ctx, pool, wsSlug, srcIdent, iss.ID, owner); err != nil {
		t.Fatalf("GetIssue after failed move: %v", err)
	}
}
