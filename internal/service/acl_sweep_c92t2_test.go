package service

// C92T2: enforcement sweep — negative tests proving private-project
// invisibility on every surface the sweep touches. The fixture is
// T0's setupACLTest (workspace admin + member + guest, project
// ALPHA); each test seeds its rows while ALPHA is public, flips it
// private, then asserts the non-member view. A teamspace-derived
// member (Contributor access) and a direct guest are asserted where
// the surface distinguishes them.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// c92t2SeedIssue creates one issue in ALPHA, assigns it to the
// member and the admin, and returns its id. Seeded while public so
// assignment (a member+ write) succeeds before the flip.
func c92t2SeedIssue(t *testing.T, ctx context.Context, pool *pgxpool.Pool, slug, admin, member string) string {
	t.Helper()
	iss, err := CreateIssue(ctx, pool, slug, "ALPHA", admin, CreateIssueInput{Name: "secret-issue-alpha"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := AssignAssignee(ctx, pool, slug, "ALPHA", iss.ID, member, admin); err != nil {
		t.Fatalf("AssignAssignee member: %v", err)
	}
	if err := AssignAssignee(ctx, pool, slug, "ALPHA", iss.ID, admin, admin); err != nil {
		t.Fatalf("AssignAssignee admin: %v", err)
	}
	return iss.ID
}

// c92t2LinkTeamspace links ALPHA to a fresh teamspace and seats the
// member in it, granting teamspace-derived Contributor access.
func c92t2LinkTeamspace(t *testing.T, ctx context.Context, pool *pgxpool.Pool, slug, admin, member string) {
	t.Helper()
	ts := mustTeamspace(t, ctx, pool, slug, admin, "SweepTeam")
	if _, err := UpdateProject(ctx, pool, slug, "ALPHA", admin, ProjectPatch{
		TeamspaceID: PatchField[string]{Set: true, Value: &ts.ID},
	}); err != nil {
		t.Fatalf("link project: %v", err)
	}
	if _, err := AddTeamspaceMember(ctx, pool, slug, ts.ID, admin, member); err != nil {
		t.Fatalf("AddTeamspaceMember: %v", err)
	}
}

func TestC92T2MyWorkPrivateHidden(t *testing.T) {
	ctx, pool, slug, admin, member, _, _ := setupACLTest(t)
	c92t2SeedIssue(t, ctx, pool, slug, admin, member)
	setPrivate(t, ctx, pool, slug, admin, "ALPHA")

	items, err := ListMyWork(ctx, pool, slug, member, MyWorkAssigned, 100)
	if err != nil {
		t.Fatalf("ListMyWork member: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("member assigned my-work = %d rows, want 0 (private project)", len(items))
	}
	// Workspace Admin override: the admin still sees their assignment.
	items, err = ListMyWork(ctx, pool, slug, admin, MyWorkAssigned, 100)
	if err != nil {
		t.Fatalf("ListMyWork admin: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("admin assigned my-work = %d rows, want 1", len(items))
	}
	// Teamspace-derived member regains Contributor visibility.
	c92t2LinkTeamspace(t, ctx, pool, slug, admin, member)
	items, err = ListMyWork(ctx, pool, slug, member, MyWorkAssigned, 100)
	if err != nil {
		t.Fatalf("ListMyWork derived: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("derived member assigned my-work = %d rows, want 1", len(items))
	}
}

func TestC92T2SearchPrivateHidden(t *testing.T) {
	ctx, pool, slug, admin, member, _, _ := setupACLTest(t)
	c92t2SeedIssue(t, ctx, pool, slug, admin, member)
	setPrivate(t, ctx, pool, slug, admin, "ALPHA")

	res, err := SearchIssues(ctx, pool, member, "secret-issue-alpha", "", 10)
	if err != nil {
		t.Fatalf("SearchIssues member: %v", err)
	}
	if len(res.Results) != 0 {
		t.Fatalf("member search = %d hits, want 0", len(res.Results))
	}
	res, err = SearchIssues(ctx, pool, admin, "secret-issue-alpha", "", 10)
	if err != nil {
		t.Fatalf("SearchIssues admin: %v", err)
	}
	if len(res.Results) != 1 {
		t.Fatalf("admin search = %d hits, want 1", len(res.Results))
	}
}

func TestC92T2FavoritesPrivateHidden(t *testing.T) {
	ctx, pool, slug, admin, member, _, _ := setupACLTest(t)
	issID := c92t2SeedIssue(t, ctx, pool, slug, admin, member)
	if _, _, err := StarFavorite(ctx, pool, member, FavoriteIssue, issID); err != nil {
		t.Fatalf("StarFavorite issue (public): %v", err)
	}
	if _, _, err := StarFavorite(ctx, pool, member, FavoriteProject, mustProjectID(t, ctx, pool, slug, admin)); err != nil {
		t.Fatalf("StarFavorite project (public): %v", err)
	}
	setPrivate(t, ctx, pool, slug, admin, "ALPHA")

	// Starring a private-unreachable target is the same 404 as a
	// missing issue — never a leak, never a distinct error.
	if _, _, err := StarFavorite(ctx, pool, member, FavoriteIssue, issID); !errors.Is(err, ErrFavoriteTargetNotFound) {
		t.Fatalf("star private issue: err=%v, want ErrFavoriteTargetNotFound", err)
	}
	// Previously starred rows drop out of the list at read time.
	favs, err := ListFavorites(ctx, pool, member)
	if err != nil {
		t.Fatalf("ListFavorites: %v", err)
	}
	if len(favs.Issues) != 0 || len(favs.Projects) != 0 {
		t.Fatalf("ListFavorites = %d issues %d projects, want 0 0", len(favs.Issues), len(favs.Projects))
	}
	// Teamspace-derived member can star again.
	c92t2LinkTeamspace(t, ctx, pool, slug, admin, member)
	if _, _, err := StarFavorite(ctx, pool, member, FavoriteIssue, issID); err != nil {
		t.Fatalf("star derived: %v", err)
	}
}

func mustProjectID(t *testing.T, ctx context.Context, pool *pgxpool.Pool, slug, admin string) string {
	t.Helper()
	p, err := GetProject(ctx, pool, slug, "ALPHA", admin)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	return p.ID
}

func TestC92T2ActivityPrivateHidden(t *testing.T) {
	ctx, pool, slug, admin, member, _, _ := setupACLTest(t)
	issID := c92t2SeedIssue(t, ctx, pool, slug, admin, member)
	// A state change writes an activity row while the project is public.
	started := stateIDByGroup(t, pool, mustProjectID(t, ctx, pool, slug, admin), "started")
	if _, err := UpdateIssue(ctx, pool, slug, "ALPHA", issID, admin, IssuePatch{StateID: &started}); err != nil {
		t.Fatalf("UpdateIssue state: %v", err)
	}
	setPrivate(t, ctx, pool, slug, admin, "ALPHA")

	page, err := GetWorkspaceActivity(ctx, pool, slug, member, 50, "", nil, "")
	if err != nil {
		t.Fatalf("GetWorkspaceActivity member: %v", err)
	}
	if len(page.Entries) != 0 {
		t.Fatalf("member activity = %d entries, want 0", len(page.Entries))
	}
	page, err = GetWorkspaceActivity(ctx, pool, slug, admin, 50, "", nil, "")
	if err != nil {
		t.Fatalf("GetWorkspaceActivity admin: %v", err)
	}
	if len(page.Entries) == 0 {
		t.Fatal("admin activity empty, want the state-change row")
	}
}

func TestC92T2InitiativeProjectsPrivateHidden(t *testing.T) {
	ctx, pool, slug, admin, member, _, _ := setupACLTest(t)
	ini, err := CreateInitiative(ctx, pool, slug, admin, InitiativeInput{Name: "Sweep Program"})
	if err != nil {
		t.Fatalf("CreateInitiative: %v", err)
	}
	projID := mustProjectID(t, ctx, pool, slug, admin)
	if err := AddInitiativeProject(ctx, pool, slug, admin, ini.ID, projID); err != nil {
		t.Fatalf("AddInitiativeProject (public): %v", err)
	}
	c92t2SeedIssue(t, ctx, pool, slug, admin, member)
	setPrivate(t, ctx, pool, slug, admin, "ALPHA")

	projs, err := ListInitiativeProjects(ctx, pool, slug, member, ini.ID)
	if err != nil {
		t.Fatalf("ListInitiativeProjects member: %v", err)
	}
	if len(projs) != 0 {
		t.Fatalf("member initiative projects = %d, want 0", len(projs))
	}
	rollup, err := GetInitiativeRollup(ctx, pool, slug, member, ini.ID)
	if err != nil {
		t.Fatalf("GetInitiativeRollup member: %v", err)
	}
	if rollup.Total != 0 {
		t.Fatalf("member rollup total = %d, want 0", rollup.Total)
	}
	projs, err = ListInitiativeProjects(ctx, pool, slug, admin, ini.ID)
	if err != nil {
		t.Fatalf("ListInitiativeProjects admin: %v", err)
	}
	if len(projs) != 1 {
		t.Fatalf("admin initiative projects = %d, want 1", len(projs))
	}
}

func TestC92T2IssueViewsPrivateHidden(t *testing.T) {
	ctx, pool, slug, admin, member, guest, _ := setupACLTest(t)
	projID := mustProjectID(t, ctx, pool, slug, admin)
	setPrivate(t, ctx, pool, slug, admin, "ALPHA")

	if _, err := ListIssueViews(ctx, pool, member, projID); !errors.Is(err, ErrViewProjectNotFound) {
		t.Fatalf("list views member: err=%v, want ErrViewProjectNotFound", err)
	}
	if _, err := CreateIssueView(ctx, pool, member, projID, IssueViewInput{Name: "sneak", Filters: json.RawMessage("{}")}); !errors.Is(err, ErrViewProjectNotFound) {
		t.Fatalf("create view member: err=%v, want ErrViewProjectNotFound", err)
	}
	// Direct guest (read-only): may list views, but the view-creation
	// path stays open to guests as before — the gate is project
	// reachability, not the workspace ladder.
	if _, err := AddProjectMember(ctx, pool, slug, "ALPHA", admin, guest, RoleGuest); err != nil {
		t.Fatalf("AddProjectMember guest: %v", err)
	}
	if _, err := ListIssueViews(ctx, pool, guest, projID); err != nil {
		t.Fatalf("list views direct guest: %v", err)
	}
}

func TestC92T2DisplayIDPrivateHidden(t *testing.T) {
	ctx, pool, slug, admin, member, _, _ := setupACLTest(t)
	c92t2SeedIssue(t, ctx, pool, slug, admin, member)
	setPrivate(t, ctx, pool, slug, admin, "ALPHA")

	if _, err := GetIssueByDisplayID(ctx, pool, member, "ALPHA-1"); !errors.Is(err, ErrWorkItemNotFound) {
		t.Fatalf("display id member: err=%v, want ErrWorkItemNotFound", err)
	}
	if _, err := GetIssueByDisplayID(ctx, pool, admin, "ALPHA-1"); err != nil {
		t.Fatalf("display id admin: %v", err)
	}
}

func TestC92T2WorkloadRiskPrivateHidden(t *testing.T) {
	ctx, pool, slug, admin, member, _, _ := setupACLTest(t)
	c92t2SeedIssue(t, ctx, pool, slug, admin, member)
	projID := mustProjectID(t, ctx, pool, slug, admin)
	setPrivate(t, ctx, pool, slug, admin, "ALPHA")

	// Empty scope counts only reachable projects: the member's open
	// count drops to zero, and no unassigned rows leak.
	rep, err := GetWorkload(ctx, pool, mustWorkspaceUUID(t, ctx, pool, slug, admin), member, nil)
	if err != nil {
		t.Fatalf("GetWorkload member: %v", err)
	}
	for _, m := range rep.Members {
		if m.UserID == member && m.OpenCount != 0 {
			t.Fatalf("member open_count = %d, want 0", m.OpenCount)
		}
	}
	if len(rep.Unassigned) != 0 {
		t.Fatalf("unassigned = %d rows, want 0", len(rep.Unassigned))
	}
	// Explicitly requesting the private project is the same 404 a
	// foreign-workspace project already produced.
	if _, err := GetWorkload(ctx, pool, mustWorkspaceUUID(t, ctx, pool, slug, admin), member, []uuid.UUID{uuid.MustParse(projID)}); !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("workload explicit private: err=%v, want ErrProjectNotFound", err)
	}
	// Risk report: project absent for the member, present for admin.
	rrep, err := GetRiskReport(ctx, pool, mustWorkspaceUUID(t, ctx, pool, slug, admin), member)
	if err != nil {
		t.Fatalf("GetRiskReport member: %v", err)
	}
	if len(rrep.Projects) != 0 {
		t.Fatalf("member risk projects = %d, want 0", len(rrep.Projects))
	}
	rrep, err = GetRiskReport(ctx, pool, mustWorkspaceUUID(t, ctx, pool, slug, admin), admin)
	if err != nil {
		t.Fatalf("GetRiskReport admin: %v", err)
	}
	if len(rrep.Projects) != 1 {
		t.Fatalf("admin risk projects = %d, want 1", len(rrep.Projects))
	}
}

func mustWorkspaceUUID(t *testing.T, ctx context.Context, pool *pgxpool.Pool, slug, admin string) uuid.UUID {
	t.Helper()
	var id string
	if err := pool.QueryRow(ctx,
		`SELECT w.id::text FROM workspaces w
		  JOIN workspace_members m ON m.workspace_id = w.id
		 WHERE w.slug = $1 AND m.user_id = $2::uuid`, slug, admin).Scan(&id); err != nil {
		t.Fatalf("workspace id: %v", err)
	}
	u, err := uuid.Parse(id)
	if err != nil {
		t.Fatalf("parse workspace uuid: %v", err)
	}
	return u
}

func TestC92T2WidgetScopePrivateHidden(t *testing.T) {
	ctx, pool, slug, admin, member, _, _ := setupACLTest(t)
	c92t2SeedIssue(t, ctx, pool, slug, admin, member)
	projID := mustProjectID(t, ctx, pool, slug, admin)
	d, err := CreateDashboard(ctx, pool, slug, admin, "Sweep", nil, DashboardVisibilityPublic)
	if err != nil {
		t.Fatalf("CreateDashboard: %v", err)
	}
	cfg, _ := json.Marshal(map[string]any{"project_ids": []string{projID}})
	w, err := CreateWidget(ctx, pool, slug, d.ID, admin, CreateWidgetInput{
		Title: "count", WidgetType: WidgetTypeNumber, Config: cfg,
	})
	if err != nil {
		t.Fatalf("CreateWidget: %v", err)
	}
	setPrivate(t, ctx, pool, slug, admin, "ALPHA")

	data, err := GetWidgetData(ctx, pool, w.ID, member, 1)
	if err != nil {
		t.Fatalf("GetWidgetData member: %v", err)
	}
	if data.Value == nil || *data.Value != 0 {
		t.Fatalf("member widget value = %v, want 0", data.Value)
	}
	data, err = GetWidgetData(ctx, pool, w.ID, admin, 1)
	if err != nil {
		t.Fatalf("GetWidgetData admin: %v", err)
	}
	if data.Value == nil || *data.Value != 1 {
		t.Fatalf("admin widget value = %v, want 1", data.Value)
	}
}

func TestC92T2NotificationsPrivateHidden(t *testing.T) {
	ctx, pool, slug, admin, member, _, _ := setupACLTest(t)
	projID := mustProjectID(t, ctx, pool, slug, admin)
	insertNotif := func(userID, payload string) {
		t.Helper()
		if _, err := pool.Exec(ctx,
			`INSERT INTO notifications (user_id, type, title, payload)
			 VALUES ($1::uuid, 'issue.state_changed', 'moved ALPHA-1', $2::jsonb)`,
			userID, payload); err != nil {
			t.Fatalf("insert notification: %v", err)
		}
	}
	// project_id-carrying row + issue_id-only row (stale/reminder shape).
	issID := c92t2SeedIssue(t, ctx, pool, slug, admin, member)
	insertNotif(member, `{"project_id":"`+projID+`"}`)
	insertNotif(member, `{"issue_id":"`+issID+`"}`)
	insertNotif(admin, `{"project_id":"`+projID+`"}`)
	setPrivate(t, ctx, pool, slug, admin, "ALPHA")

	notifs, unread, err := ListNotifications(ctx, pool, member, false, 25)
	if err != nil {
		t.Fatalf("ListNotifications member: %v", err)
	}
	if len(notifs) != 0 || unread != 0 {
		t.Fatalf("member notifications = %d rows %d unread, want 0 0", len(notifs), unread)
	}
	notifs, unread, err = ListNotifications(ctx, pool, admin, false, 25)
	if err != nil {
		t.Fatalf("ListNotifications admin: %v", err)
	}
	if len(notifs) != 1 || unread != 1 {
		t.Fatalf("admin notifications = %d rows %d unread, want 1 1", len(notifs), unread)
	}
}

func TestC92T2OKRLinkGate(t *testing.T) {
	ctx, pool, slug, admin, member, _, _ := setupACLTest(t)
	issID := c92t2SeedIssue(t, ctx, pool, slug, admin, member)
	ob, err := CreateObjective(ctx, pool, slug, admin, ObjectiveInput{Title: "Sweep Obj"})
	if err != nil {
		t.Fatalf("CreateObjective: %v", err)
	}
	target := 10.0
	kr, err := CreateKeyResult(ctx, pool, slug, admin, ob.ID, KeyResultInput{Title: "kr", Kind: "issue", TargetValue: &target})
	if err != nil {
		t.Fatalf("CreateKeyResult: %v", err)
	}
	setPrivate(t, ctx, pool, slug, admin, "ALPHA")

	// A workspace member who cannot reach the private project cannot
	// pull its issues into a workspace-visible objective; the
	// private project reads as missing, never as forbidden-with-proof.
	if err := LinkIssueToKeyResult(ctx, pool, slug, member, ob.ID, kr.ID, issID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("link private issue: err=%v, want ErrNotFound", err)
	}
	if err := LinkIssueToKeyResult(ctx, pool, slug, admin, ob.ID, kr.ID, issID); err != nil {
		t.Fatalf("link admin: %v", err)
	}
	// Linked-issue titles stay hidden from the non-member in detail.
	got, err := GetObjective(ctx, pool, slug, member, ob.ID)
	if err != nil {
		t.Fatalf("GetObjective member: %v", err)
	}
	for _, k := range got.KeyResults {
		if len(k.LinkedIssues) != 0 {
			t.Fatalf("member linked issues = %d, want 0", len(k.LinkedIssues))
		}
	}
}

func TestC92T2SystemJobRecipientFilter(t *testing.T) {
	ctx, pool, slug, admin, member, _, _ := setupACLTest(t)
	issID := c92t2SeedIssue(t, ctx, pool, slug, admin, member)
	setPrivate(t, ctx, pool, slug, admin, "ALPHA")

	// Private + unreachable: the member drops out, the admin stays.
	kept, err := filterIssueRecipientsByAccess(ctx, pool, issID, []string{member, admin})
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if len(kept) != 1 || kept[0] != admin {
		t.Fatalf("kept = %v, want [admin]", kept)
	}
	// Teamspace-derived member is kept again.
	c92t2LinkTeamspace(t, ctx, pool, slug, admin, member)
	kept, err = filterIssueRecipientsByAccess(ctx, pool, issID, []string{member, admin})
	if err != nil {
		t.Fatalf("filter derived: %v", err)
	}
	if len(kept) != 2 {
		t.Fatalf("kept derived = %v, want both", kept)
	}
}
