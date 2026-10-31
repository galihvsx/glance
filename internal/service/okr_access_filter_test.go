package service

// C93T1: OKR KR progress aggregates respect effective project access.
// A non-admin caller's issue-KR counts include only linked issues in
// projects they can reach (public, direct membership,
// teamspace-derived); a workspace Admin counts everything. Hidden
// issues leak no title/ID into the response, a KR with only hidden
// links reports 0 (not an error), and metric KRs are unchanged.

import (
	"context"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func approxOKR(t *testing.T, got, want float64, what string) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
}

// c93DoneStateID creates a completed-group state in the project.
func c93DoneStateID(t *testing.T, ctx context.Context, pool *pgxpool.Pool, slug, ident, actor string) string {
	t.Helper()
	st, err := CreateState(ctx, pool, slug, ident, actor, StateInput{Name: "Finished", Group: "completed", Color: "green"})
	if err != nil {
		t.Fatalf("CreateState done: %v", err)
	}
	return st.ID
}

func c93LinkedIDs(t *testing.T, kr KeyResult) map[string]bool {
	t.Helper()
	ids := map[string]bool{}
	for _, li := range kr.LinkedIssues {
		ids[li.ID] = true
		if li.Title == "" {
			t.Fatalf("linked issue %s has empty title", li.ID)
		}
	}
	return ids
}

func TestOKRProgressRespectsProjectAccess(t *testing.T) {
	ctx, pool, slug, admin := setupOKRTest(t)
	caller := createTestUser(t, pool, uniqueTestEmail("okr-acc"))
	addTestMember(t, pool, slug, admin, caller, RoleMember)

	// (a) public project, (b) private + direct membership for the
	// caller, (c) private with no access for the caller.
	pubIdent := uniqueTestIdentifier()
	createTestProject(t, pool, slug, admin, "Pub", pubIdent)
	directIdent := uniqueTestIdentifier()
	createTestProject(t, pool, slug, admin, "Direct", directIdent)
	setPrivate(t, ctx, pool, slug, admin, directIdent)
	hiddenIdent := uniqueTestIdentifier()
	createTestProject(t, pool, slug, admin, "Hidden", hiddenIdent)
	setPrivate(t, ctx, pool, slug, admin, hiddenIdent)

	if _, err := AddProjectMember(ctx, pool, slug, directIdent, admin, caller, RoleMember); err != nil {
		t.Fatalf("AddProjectMember direct: %v", err)
	}

	pubDone, err := CreateIssue(ctx, pool, slug, pubIdent, admin, CreateIssueInput{Name: "pub-done", StateID: strptr(c93DoneStateID(t, ctx, pool, slug, pubIdent, admin))})
	if err != nil {
		t.Fatalf("create pub issue: %v", err)
	}
	directOpen, err := CreateIssue(ctx, pool, slug, directIdent, admin, CreateIssueInput{Name: "direct-open", StateID: strptr(backlogStateID(t, pool, slug, directIdent, admin))})
	if err != nil {
		t.Fatalf("create direct issue: %v", err)
	}
	hiddenTitle := "hidden-secret-issue"
	hiddenDone, err := CreateIssue(ctx, pool, slug, hiddenIdent, admin, CreateIssueInput{Name: hiddenTitle, StateID: strptr(c93DoneStateID(t, ctx, pool, slug, hiddenIdent, admin))})
	if err != nil {
		t.Fatalf("create hidden issue: %v", err)
	}

	ob, err := CreateObjective(ctx, pool, slug, admin, ObjectiveInput{Title: "Access"})
	if err != nil {
		t.Fatalf("CreateObjective: %v", err)
	}
	kr, err := CreateKeyResult(ctx, pool, slug, admin, ob.ID, KeyResultInput{
		Title: "Ship issues", Kind: "issue", TargetValue: fptr(100),
	})
	if err != nil {
		t.Fatalf("CreateKeyResult issue: %v", err)
	}
	// Admin links all three (admin reaches every project).
	for _, iss := range []string{pubDone.ID, directOpen.ID, hiddenDone.ID} {
		if err := LinkIssueToKeyResult(ctx, pool, slug, admin, ob.ID, kr.ID, iss); err != nil {
			t.Fatalf("link %s: %v", iss, err)
		}
	}
	mkr, err := CreateKeyResult(ctx, pool, slug, admin, ob.ID, KeyResultInput{
		Title: "Metric", Kind: "metric", TargetValue: fptr(10), CurrentValue: fptr(5),
	})
	if err != nil {
		t.Fatalf("CreateKeyResult metric: %v", err)
	}
	if mkr.Progress != 0.5 {
		t.Fatalf("metric KR progress at creation = %v, want 0.5", mkr.Progress)
	}

	// Non-admin caller: only (a)+(b) count → 1 done of 2 → 0.5.
	got, err := GetObjective(ctx, pool, slug, caller, ob.ID)
	if err != nil {
		t.Fatalf("GetObjective caller: %v", err)
	}
	if len(got.KeyResults) != 2 {
		t.Fatalf("caller KRs = %d, want 2", len(got.KeyResults))
	}
	byTitle := map[string]KeyResult{}
	for _, k := range got.KeyResults {
		byTitle[k.Title] = k
	}
	approxOKR(t, byTitle["Ship issues"].Progress, 0.5, "caller issue KR progress")
	approxOKR(t, byTitle["Metric"].Progress, 0.5, "caller metric KR progress")
	approxOKR(t, got.Progress, 0.5, "caller objective progress")
	linked := c93LinkedIDs(t, byTitle["Ship issues"])
	if len(linked) != 2 || !linked[pubDone.ID] || !linked[directOpen.ID] || linked[hiddenDone.ID] {
		t.Fatalf("caller linked = %v, want exactly pub + direct", linked)
	}
	dump := fmt.Sprintf("%+v", got)
	if strings.Contains(dump, hiddenDone.ID) || strings.Contains(dump, hiddenTitle) {
		t.Fatalf("caller response leaks hidden issue: %s", dump)
	}

	// ListObjectives agrees with the detail view for the caller.
	list, err := ListObjectives(ctx, pool, slug, caller)
	if err != nil {
		t.Fatalf("ListObjectives caller: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("caller list = %d objectives, want 1", len(list))
	}
	approxOKR(t, list[0].Progress, 0.5, "caller list objective progress")

	// The 1457 path for a non-admin caller: UpdateKeyResult returns
	// the access-filtered progress, not the unfiltered 2/3.
	updated, err := UpdateKeyResult(ctx, pool, slug, caller, ob.ID, kr.ID, KeyResultPatch{Title: strptr("Ship issues")})
	if err != nil {
		t.Fatalf("UpdateKeyResult caller: %v", err)
	}
	approxOKR(t, updated.Progress, 0.5, "caller updated issue KR progress")

	// Admin caller: all three count → 2 done of 3.
	adminGot, err := GetObjective(ctx, pool, slug, admin, ob.ID)
	if err != nil {
		t.Fatalf("GetObjective admin: %v", err)
	}
	adminByTitle := map[string]KeyResult{}
	for _, k := range adminGot.KeyResults {
		adminByTitle[k.Title] = k
	}
	approxOKR(t, adminByTitle["Ship issues"].Progress, 2.0/3.0, "admin issue KR progress")
	approxOKR(t, adminByTitle["Metric"].Progress, 0.5, "admin metric KR progress")
	approxOKR(t, adminGot.Progress, (2.0/3.0+0.5)/2.0, "admin objective progress")
	if len(c93LinkedIDs(t, adminByTitle["Ship issues"])) != 3 {
		t.Fatalf("admin linked = %d, want 3", len(c93LinkedIDs(t, adminByTitle["Ship issues"])))
	}

	// A KR whose links are ALL inaccessible: progress 0, no error,
	// no linked issues, no leak.
	ob2, err := CreateObjective(ctx, pool, slug, admin, ObjectiveInput{Title: "Hidden only"})
	if err != nil {
		t.Fatalf("CreateObjective ob2: %v", err)
	}
	kr2, err := CreateKeyResult(ctx, pool, slug, admin, ob2.ID, KeyResultInput{
		Title: "Hidden issues", Kind: "issue", TargetValue: fptr(100),
	})
	if err != nil {
		t.Fatalf("CreateKeyResult kr2: %v", err)
	}
	if err := LinkIssueToKeyResult(ctx, pool, slug, admin, ob2.ID, kr2.ID, hiddenDone.ID); err != nil {
		t.Fatalf("link hidden to kr2: %v", err)
	}
	got2, err := GetObjective(ctx, pool, slug, caller, ob2.ID)
	if err != nil {
		t.Fatalf("GetObjective caller ob2: %v", err)
	}
	if len(got2.KeyResults) != 1 || got2.KeyResults[0].Progress != 0 || got2.Progress != 0 {
		t.Fatalf("all-hidden KR/objective progress = %v / %v, want 0 / 0", got2.KeyResults[0].Progress, got2.Progress)
	}
	if len(got2.KeyResults[0].LinkedIssues) != 0 {
		t.Fatalf("all-hidden linked = %d, want 0", len(got2.KeyResults[0].LinkedIssues))
	}
	dump2 := fmt.Sprintf("%+v", got2)
	if strings.Contains(dump2, hiddenDone.ID) || strings.Contains(dump2, hiddenTitle) {
		t.Fatalf("all-hidden response leaks hidden issue: %s", dump2)
	}
}

func TestOKRProgressTeamspaceDerivedCounts(t *testing.T) {
	ctx, pool, slug, admin := setupOKRTest(t)
	caller := createTestUser(t, pool, uniqueTestEmail("okr-acc-ts"))
	addTestMember(t, pool, slug, admin, caller, RoleMember)

	teamIdent := uniqueTestIdentifier()
	createTestProject(t, pool, slug, admin, "Team", teamIdent)
	setPrivate(t, ctx, pool, slug, admin, teamIdent)
	ts := mustTeamspace(t, ctx, pool, slug, admin, "Platform")
	if _, err := UpdateProject(ctx, pool, slug, teamIdent, admin, ProjectPatch{
		TeamspaceID: PatchField[string]{Set: true, Value: &ts.ID},
	}); err != nil {
		t.Fatalf("link project to teamspace: %v", err)
	}
	if _, err := AddTeamspaceMember(ctx, pool, slug, ts.ID, admin, caller); err != nil {
		t.Fatalf("AddTeamspaceMember: %v", err)
	}
	hiddenIdent := uniqueTestIdentifier()
	createTestProject(t, pool, slug, admin, "HiddenTS", hiddenIdent)
	setPrivate(t, ctx, pool, slug, admin, hiddenIdent)

	teamOpen, err := CreateIssue(ctx, pool, slug, teamIdent, admin, CreateIssueInput{Name: "team-open", StateID: strptr(backlogStateID(t, pool, slug, teamIdent, admin))})
	if err != nil {
		t.Fatalf("create team issue: %v", err)
	}
	hiddenDone, err := CreateIssue(ctx, pool, slug, hiddenIdent, admin, CreateIssueInput{Name: "hidden-ts-done", StateID: strptr(c93DoneStateID(t, ctx, pool, slug, hiddenIdent, admin))})
	if err != nil {
		t.Fatalf("create hidden issue: %v", err)
	}

	ob, err := CreateObjective(ctx, pool, slug, admin, ObjectiveInput{Title: "Team access"})
	if err != nil {
		t.Fatalf("CreateObjective: %v", err)
	}
	kr, err := CreateKeyResult(ctx, pool, slug, admin, ob.ID, KeyResultInput{
		Title: "Ship issues", Kind: "issue", TargetValue: fptr(100),
	})
	if err != nil {
		t.Fatalf("CreateKeyResult: %v", err)
	}
	for _, iss := range []string{teamOpen.ID, hiddenDone.ID} {
		if err := LinkIssueToKeyResult(ctx, pool, slug, admin, ob.ID, kr.ID, iss); err != nil {
			t.Fatalf("link %s: %v", iss, err)
		}
	}

	// Teamspace-derived access counts; the hidden done issue does not.
	got, err := GetObjective(ctx, pool, slug, caller, ob.ID)
	if err != nil {
		t.Fatalf("GetObjective caller: %v", err)
	}
	if len(got.KeyResults) != 1 || got.KeyResults[0].Progress != 0 {
		t.Fatalf("caller progress = %+v, want 0 (0 done of 1 counted)", got.KeyResults)
	}
	linked := c93LinkedIDs(t, got.KeyResults[0])
	if len(linked) != 1 || !linked[teamOpen.ID] {
		t.Fatalf("caller linked = %v, want only team issue", linked)
	}
	adminGot, err := GetObjective(ctx, pool, slug, admin, ob.ID)
	if err != nil {
		t.Fatalf("GetObjective admin: %v", err)
	}
	approxOKR(t, adminGot.KeyResults[0].Progress, 0.5, "admin teamspace KR progress")
}
