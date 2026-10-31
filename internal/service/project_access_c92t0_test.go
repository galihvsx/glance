package service

// C92T0: project ACL via teamspace membership + teamspace Lead.
// Resolver branch matrix (pure order function + DB-backed effective
// access), project members CRUD authZ, visibility gating, and Lead
// designation + Lead-scoped gates.

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func c92IntPtr(v int) *int { return &v }

func TestApplyAccessOrderBranches(t *testing.T) {
	cases := []struct {
		name     string
		wsRole   int
		direct   *int
		team     bool
		vis      string
		wantRole int
	}{
		{"admin override beats direct guest", RoleAdmin, c92IntPtr(RoleGuest), true, VisibilityPrivate, RoleAdmin},
		{"admin override on private with nothing", RoleAdmin, nil, false, VisibilityPrivate, RoleAdmin},
		{"direct guest first over teamspace", RoleMember, c92IntPtr(RoleGuest), true, VisibilityPrivate, RoleGuest},
		{"direct admin first over teamspace", RoleMember, c92IntPtr(RoleAdmin), true, VisibilityPrivate, RoleAdmin},
		{"direct member on private", RoleGuest, c92IntPtr(RoleMember), false, VisibilityPrivate, RoleMember},
		{"teamspace derived for guest", RoleGuest, nil, true, VisibilityPrivate, RoleMember},
		{"teamspace derived for member", RoleMember, nil, true, VisibilityPrivate, RoleMember},
		{"public fallback guest", RoleGuest, nil, false, VisibilityPublic, RoleGuest},
		{"public fallback viewer", RoleViewer, nil, false, VisibilityPublic, RoleViewer},
		{"public fallback member", RoleMember, nil, false, VisibilityPublic, RoleMember},
		{"private 404 member", RoleMember, nil, false, VisibilityPrivate, -1},
		{"private 404 guest", RoleGuest, nil, false, VisibilityPrivate, -1},
	}
	for _, tc := range cases {
		if got := applyAccessOrder(tc.wsRole, tc.direct, tc.team, tc.vis); got != tc.wantRole {
			t.Errorf("%s: got %d, want %d", tc.name, got, tc.wantRole)
		}
	}
}

// setupACLTest builds a workspace with an admin, a member, and a
// guest, plus one public project. The project starts public (the
// migration backfill shape); tests flip it private via the admin.
func setupACLTest(t *testing.T) (context.Context, *pgxpool.Pool, string, string, string, string, *Project) {
	t.Helper()
	ctx := context.Background()
	pool := newTestPool(t)
	admin := createTestUser(t, pool, uniqueTestEmail("acl-admin"))
	slug := uniqueTestSlug("acl-ws")
	createTestWorkspace(t, pool, "ACL Co", slug, admin)
	member := createTestUser(t, pool, uniqueTestEmail("acl-member"))
	if err := UpsertMember(ctx, pool, slug, admin, member, RoleMember); err != nil {
		t.Fatalf("UpsertMember member: %v", err)
	}
	guest := createTestUser(t, pool, uniqueTestEmail("acl-guest"))
	if err := UpsertMember(ctx, pool, slug, admin, guest, RoleGuest); err != nil {
		t.Fatalf("UpsertMember guest: %v", err)
	}
	p, err := CreateProject(ctx, pool, slug, admin, "Alpha", "ALPHA")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if p.Visibility != VisibilityPublic {
		t.Fatalf("new project visibility = %q, want public", p.Visibility)
	}
	return ctx, pool, slug, admin, member, guest, p
}

func setPrivate(t *testing.T, ctx context.Context, pool *pgxpool.Pool, slug, admin, ident string) {
	t.Helper()
	priv := VisibilityPrivate
	if _, err := UpdateProject(ctx, pool, slug, ident, admin, ProjectPatch{Visibility: &priv}); err != nil {
		t.Fatalf("set private: %v", err)
	}
}

func TestEffectiveAccessDBBranches(t *testing.T) {
	ctx, pool, slug, admin, member, guest, p := setupACLTest(t)

	// Public fallback: member and guest both reach it with their
	// workspace roles.
	if _, role, err := EffectiveProjectAccess(ctx, pool, slug, "ALPHA", member); err != nil || role != RoleMember {
		t.Fatalf("public member: role=%d err=%v, want 15 nil", role, err)
	}
	if _, role, err := EffectiveProjectAccess(ctx, pool, slug, "ALPHA", guest); err != nil || role != RoleGuest {
		t.Fatalf("public guest: role=%d err=%v, want 5 nil", role, err)
	}
	// Project-ID addressing works too.
	if _, role, err := EffectiveProjectAccess(ctx, pool, slug, p.ID, member); err != nil || role != RoleMember {
		t.Fatalf("public member by id: role=%d err=%v", role, err)
	}

	setPrivate(t, ctx, pool, slug, admin, "ALPHA")

	// Private + nothing: shared 404, not a project error.
	if _, _, err := EffectiveProjectAccess(ctx, pool, slug, "ALPHA", member); !errors.Is(err, ErrNotFound) {
		t.Fatalf("private plain member: err=%v, want ErrNotFound", err)
	}
	// Admin override on private.
	if _, role, err := EffectiveProjectAccess(ctx, pool, slug, "ALPHA", admin); err != nil || role != RoleAdmin {
		t.Fatalf("private admin: role=%d err=%v, want 20 nil", role, err)
	}
	// Direct guest row on private: guest access, and direct-first
	// ordering (member would otherwise fall back to... nothing on
	// private, so this also proves the direct branch grants access).
	if _, err := AddProjectMember(ctx, pool, slug, "ALPHA", admin, guest, RoleGuest); err != nil {
		t.Fatalf("AddProjectMember guest: %v", err)
	}
	if _, role, err := EffectiveProjectAccess(ctx, pool, slug, "ALPHA", guest); err != nil || role != RoleGuest {
		t.Fatalf("private direct guest: role=%d err=%v, want 5 nil", role, err)
	}
	// Teamspace-derived: link the project, add the member to the
	// teamspace, and the member (who has no direct row) gets 15.
	ts := mustTeamspace(t, ctx, pool, slug, admin, "Platform")
	if _, err := UpdateProject(ctx, pool, slug, "ALPHA", admin, ProjectPatch{
		TeamspaceID: PatchField[string]{Set: true, Value: &ts.ID},
	}); err != nil {
		t.Fatalf("link project: %v", err)
	}
	if _, err := AddTeamspaceMember(ctx, pool, slug, ts.ID, admin, member); err != nil {
		t.Fatalf("AddTeamspaceMember: %v", err)
	}
	if _, role, err := EffectiveProjectAccess(ctx, pool, slug, "ALPHA", member); err != nil || role != RoleMember {
		t.Fatalf("private teamspace-derived: role=%d err=%v, want 15 nil", role, err)
	}
	// Direct-first ordering on a private project: give the member a
	// direct guest row; the teamspace seat must NOT promote them.
	if _, err := AddProjectMember(ctx, pool, slug, "ALPHA", admin, member, RoleGuest); err != nil {
		t.Fatalf("AddProjectMember member-as-guest: %v", err)
	}
	if _, role, err := EffectiveProjectAccess(ctx, pool, slug, "ALPHA", member); err != nil || role != RoleGuest {
		t.Fatalf("direct-first: role=%d err=%v, want 5 nil", role, err)
	}
	// Link-move changes derived access immediately: move the project
	// to a teamspace the member does NOT belong to. The member keeps
	// the direct guest row, so they stay at 5 (direct-first); remove
	// the direct row and they must fall to 404 — the old link no
	// longer grants anything.
	ts2 := mustTeamspace(t, ctx, pool, slug, admin, "Other")
	if _, err := UpdateProject(ctx, pool, slug, "ALPHA", admin, ProjectPatch{
		TeamspaceID: PatchField[string]{Set: true, Value: &ts2.ID},
	}); err != nil {
		t.Fatalf("move project: %v", err)
	}
	if err := RemoveProjectMember(ctx, pool, slug, "ALPHA", admin, member); err != nil {
		t.Fatalf("RemoveProjectMember: %v", err)
	}
	if _, _, err := EffectiveProjectAccess(ctx, pool, slug, "ALPHA", member); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after link-move: err=%v, want ErrNotFound", err)
	}
	// Non-member of the workspace: ErrNotFound, as today.
	outsider := createTestUser(t, pool, uniqueTestEmail("acl-out"))
	if _, _, err := EffectiveProjectAccess(ctx, pool, slug, "ALPHA", outsider); !errors.Is(err, ErrNotFound) {
		t.Fatalf("outsider: err=%v, want ErrNotFound", err)
	}
	// Missing project for a confirmed member: ErrProjectNotFound.
	if _, _, err := EffectiveProjectAccess(ctx, pool, slug, "NOPE", admin); !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("missing project: err=%v, want ErrProjectNotFound", err)
	}
}

func TestListProjectsPrivateVanishes(t *testing.T) {
	ctx, pool, slug, admin, member, _, _ := setupACLTest(t)
	setPrivate(t, ctx, pool, slug, admin, "ALPHA")

	projects, err := ListProjects(ctx, pool, slug, member, false, nil)
	if err != nil {
		t.Fatalf("ListProjects member: %v", err)
	}
	for _, p := range projects {
		if p.Identifier == "ALPHA" {
			t.Fatal("private project listed for non-member")
		}
	}
	projects, err = ListProjects(ctx, pool, slug, admin, false, nil)
	if err != nil {
		t.Fatalf("ListProjects admin: %v", err)
	}
	found := false
	for _, p := range projects {
		if p.Identifier == "ALPHA" {
			found = true
		}
	}
	if !found {
		t.Fatal("private project missing from admin list")
	}
	// GetProject mirrors the list: 404 for the non-member.
	if _, err := GetProject(ctx, pool, slug, "ALPHA", member); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetProject non-member: err=%v, want ErrNotFound", err)
	}
}

func TestProjectMembersCRUD(t *testing.T) {
	ctx, pool, slug, admin, member, guest, _ := setupACLTest(t)

	// Management by a plain member: forbidden.
	if _, err := AddProjectMember(ctx, pool, slug, "ALPHA", member, guest, RoleMember); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member add: err=%v, want ErrForbidden", err)
	}
	// Add target must be a workspace member.
	outsider := createTestUser(t, pool, uniqueTestEmail("acl-out2"))
	if _, err := AddProjectMember(ctx, pool, slug, "ALPHA", admin, outsider, RoleMember); !errors.Is(err, ErrProjectTargetNotMember) {
		t.Fatalf("outsider add: err=%v, want ErrProjectTargetNotMember", err)
	}
	// Invalid role rejected.
	if _, err := AddProjectMember(ctx, pool, slug, "ALPHA", admin, guest, 99); !errors.Is(err, ErrProjectMemberRoleInvalid) {
		t.Fatalf("bad role add: err=%v, want ErrProjectMemberRoleInvalid", err)
	}
	// Add + duplicate add is idempotent and does NOT overwrite role.
	m, err := AddProjectMember(ctx, pool, slug, "ALPHA", admin, guest, RoleGuest)
	if err != nil || m.Role != RoleGuest {
		t.Fatalf("add guest: %+v err=%v", m, err)
	}
	m, err = AddProjectMember(ctx, pool, slug, "ALPHA", admin, guest, RoleMember)
	if err != nil || m.Role != RoleGuest {
		t.Fatalf("duplicate add overwrote role: %+v err=%v", m, err)
	}
	// Change role works; a direct admin can then manage too.
	m, err = ChangeProjectMemberRole(ctx, pool, slug, "ALPHA", admin, guest, RoleAdmin)
	if err != nil || m.Role != RoleAdmin {
		t.Fatalf("change role: %+v err=%v", m, err)
	}
	if _, err := AddProjectMember(ctx, pool, slug, "ALPHA", guest, member, RoleMember); err != nil {
		t.Fatalf("direct admin add: %v", err)
	}
	// Last-admin guards: guest is an admin, member is a member.
	// Promote the member to admin (a duplicate add would NOT
	// overwrite the role — that is the idempotent-add contract),
	// then removing one of two admins succeeds.
	if _, err := ChangeProjectMemberRole(ctx, pool, slug, "ALPHA", admin, member, RoleAdmin); err != nil {
		t.Fatalf("promote member to admin: %v", err)
	}
	if err := RemoveProjectMember(ctx, pool, slug, "ALPHA", admin, guest); err != nil {
		t.Fatalf("remove one of two admins: %v", err)
	}
	// Now member is the sole direct admin: removing them fails, and
	// demoting them fails.
	if err := RemoveProjectMember(ctx, pool, slug, "ALPHA", admin, member); !errors.Is(err, ErrLastProjectAdmin) {
		t.Fatalf("remove last admin: err=%v, want ErrLastProjectAdmin", err)
	}
	if _, err := ChangeProjectMemberRole(ctx, pool, slug, "ALPHA", admin, member, RoleMember); !errors.Is(err, ErrLastProjectAdmin) {
		t.Fatalf("demote last admin: err=%v, want ErrLastProjectAdmin", err)
	}
	// Removing a non-member is idempotent OK.
	if err := RemoveProjectMember(ctx, pool, slug, "ALPHA", admin, guest); err != nil {
		t.Fatalf("remove non-member: %v", err)
	}
	// Changing a non-member is 404.
	if _, err := ChangeProjectMemberRole(ctx, pool, slug, "ALPHA", admin, guest, RoleMember); !errors.Is(err, ErrProjectMemberNotFound) {
		t.Fatalf("change non-member: err=%v, want ErrProjectMemberNotFound", err)
	}
	// Roster readable by a direct member (effective access).
	roster, err := ListProjectMembers(ctx, pool, slug, "ALPHA", member)
	if err != nil {
		t.Fatalf("ListProjectMembers: %v", err)
	}
	if len(roster) != 1 || roster[0].UserID != member {
		t.Fatalf("roster = %+v, want only the member", roster)
	}
}

func TestVisibilityGating(t *testing.T) {
	ctx, pool, slug, admin, member, guest, _ := setupACLTest(t)

	// A plain member cannot flip visibility (valid value -> 403);
	// an invalid value is 400 even for a non-admin, because value
	// validation runs before authz — the same shape as the icon
	// validation in UpdateProject.
	bad := "secret"
	priv := VisibilityPrivate
	if _, err := UpdateProject(ctx, pool, slug, "ALPHA", member, ProjectPatch{Visibility: &priv}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member visibility change: err=%v, want ErrForbidden", err)
	}
	if _, err := UpdateProject(ctx, pool, slug, "ALPHA", admin, ProjectPatch{Visibility: &bad}); !errors.Is(err, ErrInvalidProjectVisibility) {
		t.Fatalf("bad visibility: err=%v, want ErrInvalidProjectVisibility", err)
	}
	// A direct project admin CAN flip visibility (effective admin).
	if _, err := AddProjectMember(ctx, pool, slug, "ALPHA", admin, guest, RoleAdmin); err != nil {
		t.Fatalf("add guest admin: %v", err)
	}
	p, err := UpdateProject(ctx, pool, slug, "ALPHA", guest, ProjectPatch{Visibility: &priv})
	if err != nil || p.Visibility != VisibilityPrivate {
		t.Fatalf("direct admin visibility change: %+v err=%v", p, err)
	}
	// A teamspace-derived member (15) may update the name but may NOT
	// flip visibility back.
	ts := mustTeamspace(t, ctx, pool, slug, admin, "Platform")
	if _, err := AddTeamspaceMember(ctx, pool, slug, ts.ID, admin, member); err != nil {
		t.Fatalf("AddTeamspaceMember: %v", err)
	}
	if _, err := UpdateProject(ctx, pool, slug, "ALPHA", admin, ProjectPatch{
		TeamspaceID: PatchField[string]{Set: true, Value: &ts.ID},
	}); err != nil {
		t.Fatalf("link project: %v", err)
	}
	name := "Alpha Renamed"
	if _, err := UpdateProject(ctx, pool, slug, "ALPHA", member, ProjectPatch{Name: &name}); err != nil {
		t.Fatalf("derived member rename: %v", err)
	}
	pub := VisibilityPublic
	if _, err := UpdateProject(ctx, pool, slug, "ALPHA", member, ProjectPatch{Visibility: &pub}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("derived member visibility change: err=%v, want ErrForbidden", err)
	}
	// A direct project admin CAN delete (effective admin) and a
	// derived member cannot.
	if err := DeleteProject(ctx, pool, slug, "ALPHA", member, "ALPHA", true); !errors.Is(err, ErrForbidden) {
		t.Fatalf("derived member delete: err=%v, want ErrForbidden", err)
	}
}

func TestLeadDesignation(t *testing.T) {
	ctx, pool, slug, admin, member, guest, _ := setupACLTest(t)
	ts := mustTeamspace(t, ctx, pool, slug, admin, "Platform")

	// Non-admin cannot set a Lead.
	if _, err := SetTeamspaceLead(ctx, pool, slug, member, ts.ID, &member); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member set lead: err=%v, want ErrForbidden", err)
	}
	// Target must be a teamspace member: the guest is a workspace
	// member but NOT a teamspace member yet.
	if _, err := SetTeamspaceLead(ctx, pool, slug, admin, ts.ID, &guest); !errors.Is(err, ErrTeamspaceLeadNotMember) {
		t.Fatalf("non-member lead: err=%v, want ErrTeamspaceLeadNotMember", err)
	}
	// Malformed target id: same error, never a 500.
	badID := "not-a-uuid"
	if _, err := SetTeamspaceLead(ctx, pool, slug, admin, ts.ID, &badID); !errors.Is(err, ErrTeamspaceLeadNotMember) {
		t.Fatalf("malformed lead: err=%v, want ErrTeamspaceLeadNotMember", err)
	}
	// Add the guest to the teamspace, set them Lead, replace, clear.
	if _, err := AddTeamspaceMember(ctx, pool, slug, ts.ID, admin, guest); err != nil {
		t.Fatalf("AddTeamspaceMember guest: %v", err)
	}
	lead, err := SetTeamspaceLead(ctx, pool, slug, admin, ts.ID, &guest)
	if err != nil || lead.LeadUserID == nil || *lead.LeadUserID != guest {
		t.Fatalf("set lead: %+v err=%v", lead, err)
	}
	// Lead appears in the list payload too.
	teamspaces, err := ListTeamspaces(ctx, pool, slug, admin)
	if err != nil {
		t.Fatalf("ListTeamspaces: %v", err)
	}
	found := false
	for _, row := range teamspaces {
		if row.ID == ts.ID && row.LeadUserID != nil && *row.LeadUserID == guest {
			found = true
		}
	}
	if !found {
		t.Fatalf("lead missing from list payload: %+v", teamspaces)
	}
	// Setting a new Lead replaces the old one.
	if _, err := AddTeamspaceMember(ctx, pool, slug, ts.ID, admin, member); err != nil {
		t.Fatalf("AddTeamspaceMember member: %v", err)
	}
	lead, err = SetTeamspaceLead(ctx, pool, slug, admin, ts.ID, &member)
	if err != nil || lead.LeadUserID == nil || *lead.LeadUserID != member {
		t.Fatalf("replace lead: %+v err=%v", lead, err)
	}
	// Clearing is idempotent OK.
	lead, err = SetTeamspaceLead(ctx, pool, slug, admin, ts.ID, nil)
	if err != nil || lead.LeadUserID != nil {
		t.Fatalf("clear lead: %+v err=%v", lead, err)
	}
	// Removing the Lead's membership clears the designation in the
	// same transaction.
	if _, err := SetTeamspaceLead(ctx, pool, slug, admin, ts.ID, &member); err != nil {
		t.Fatalf("re-set lead: %v", err)
	}
	if err := RemoveTeamspaceMember(ctx, pool, slug, ts.ID, admin, member); err != nil {
		t.Fatalf("remove lead member: %v", err)
	}
	after, err := resolveTeamspace(ctx, pool, mustWorkspaceID(t, ctx, pool, slug, admin), ts.ID)
	if err != nil {
		t.Fatalf("resolve after remove: %v", err)
	}
	if after.LeadUserID != nil {
		t.Fatalf("lead not cleared on membership removal: %q", *after.LeadUserID)
	}
}

func mustWorkspaceID(t *testing.T, ctx context.Context, pool *pgxpool.Pool, slug, actor string) string {
	t.Helper()
	wsID, _, err := workspaceIDForActor(pool.QueryRow(ctx,
		`SELECT w.id::text, m.role
		 FROM workspaces w
		 JOIN workspace_members m ON m.workspace_id = w.id
		 WHERE w.slug = $1 AND m.user_id = $2::uuid`,
		slug, actor))
	if err != nil {
		t.Fatalf("workspaceIDForActor: %v", err)
	}
	return wsID
}

func TestLeadScopedGates(t *testing.T) {
	ctx, pool, slug, admin, member, guest, _ := setupACLTest(t)
	ts := mustTeamspace(t, ctx, pool, slug, admin, "Platform")
	// The guest becomes a teamspace member and its Lead. As a
	// workspace guest (5) they fail every member+ gate today — the
	// Lead gates must pass them anyway, scoped to this teamspace.
	if _, err := AddTeamspaceMember(ctx, pool, slug, ts.ID, admin, guest); err != nil {
		t.Fatalf("AddTeamspaceMember guest: %v", err)
	}
	if _, err := SetTeamspaceLead(ctx, pool, slug, admin, ts.ID, &guest); err != nil {
		t.Fatalf("SetTeamspaceLead: %v", err)
	}
	// Lead manages members of THEIR teamspace...
	if _, err := AddTeamspaceMember(ctx, pool, slug, ts.ID, guest, member); err != nil {
		t.Fatalf("lead add member: %v", err)
	}
	// ...edits its settings...
	newName := "Platform Renamed"
	if _, err := UpdateTeamspace(ctx, pool, slug, guest, ts.ID, TeamspacePatch{Name: &newName}); err != nil {
		t.Fatalf("lead rename: %v", err)
	}
	// ...and links a project to it (guest Lead + ungrouped project:
	// the Lead link path, since the guest's effective role is 5).
	if _, err := UpdateProject(ctx, pool, slug, "ALPHA", guest, ProjectPatch{
		TeamspaceID: PatchField[string]{Set: true, Value: &ts.ID},
	}); err != nil {
		t.Fatalf("lead link project: %v", err)
	}
	// ...but NOT another teamspace's members.
	ts2 := mustTeamspace(t, ctx, pool, slug, admin, "Other")
	if _, err := AddTeamspaceMember(ctx, pool, slug, ts2.ID, guest, member); !errors.Is(err, ErrForbidden) {
		t.Fatalf("lead add to other teamspace: err=%v, want ErrForbidden", err)
	}
	// ...and NOT workspace-level gates (workspace member management
	// still requires a workspace Admin).
	other := createTestUser(t, pool, uniqueTestEmail("acl-other"))
	if err := UpsertMember(ctx, pool, slug, guest, other, RoleMember); !errors.Is(err, ErrForbidden) {
		t.Fatalf("lead workspace upsert: err=%v, want ErrForbidden", err)
	}
	// Lead delete of their teamspace works (ungroups the project,
	// never deletes it).
	count, err := DeleteTeamspace(ctx, pool, slug, guest, ts.ID)
	if err != nil {
		t.Fatalf("lead delete teamspace: %v", err)
	}
	if count != 1 {
		t.Fatalf("lead delete ungrouped = %d, want 1", count)
	}
	if _, err := GetProject(ctx, pool, slug, "ALPHA", admin); err != nil {
		t.Fatalf("project survived teamspace delete: %v", err)
	}
}

func TestLabelAndInitiativeRouting(t *testing.T) {
	ctx, pool, slug, admin, member, _, _ := setupACLTest(t)
	setPrivate(t, ctx, pool, slug, admin, "ALPHA")

	// Workspace label taxonomy: create a label as admin first.
	lbl, err := CreateProjectLabel(ctx, pool, slug, admin, ProjectLabelInput{Name: "Urgent"})
	if err != nil {
		t.Fatalf("CreateProjectLabel: %v", err)
	}
	// Private project + non-member: assignment 404s (no oracle).
	if err := AssignProjectLabel(ctx, pool, slug, "ALPHA", lbl.ID, member); !errors.Is(err, ErrNotFound) {
		t.Fatalf("AssignProjectLabel non-member: err=%v, want ErrNotFound", err)
	}
	// Teamspace-derived member (effective 15) may assign.
	ts := mustTeamspace(t, ctx, pool, slug, admin, "Platform")
	if _, err := AddTeamspaceMember(ctx, pool, slug, ts.ID, admin, member); err != nil {
		t.Fatalf("AddTeamspaceMember: %v", err)
	}
	if _, err := UpdateProject(ctx, pool, slug, "ALPHA", admin, ProjectPatch{
		TeamspaceID: PatchField[string]{Set: true, Value: &ts.ID},
	}); err != nil {
		t.Fatalf("link project: %v", err)
	}
	if err := AssignProjectLabel(ctx, pool, slug, "ALPHA", lbl.ID, member); err != nil {
		t.Fatalf("AssignProjectLabel derived member: %v", err)
	}
	// Initiative link: same gate — non-member cannot link, derived
	// member can.
	init, err := CreateInitiative(ctx, pool, slug, admin, InitiativeInput{Name: "Init One"})
	if err != nil {
		t.Fatalf("CreateInitiative: %v", err)
	}
	proj, err := GetProject(ctx, pool, slug, "ALPHA", admin)
	if err != nil {
		t.Fatalf("GetProject admin: %v", err)
	}
	// A second plain member (no teamspace seat) cannot link.
	plain := createTestUser(t, pool, uniqueTestEmail("acl-plain"))
	if err := UpsertMember(ctx, pool, slug, admin, plain, RoleMember); err != nil {
		t.Fatalf("UpsertMember plain: %v", err)
	}
	if err := AddInitiativeProject(ctx, pool, slug, plain, init.ID, proj.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("AddInitiativeProject non-member: err=%v, want ErrNotFound", err)
	}
	if err := AddInitiativeProject(ctx, pool, slug, member, init.ID, proj.ID); err != nil {
		t.Fatalf("AddInitiativeProject derived member: %v", err)
	}
	if err := RemoveInitiativeProject(ctx, pool, slug, member, init.ID, proj.ID); err != nil {
		t.Fatalf("RemoveInitiativeProject derived member: %v", err)
	}
}

func TestIssueSurfaceThroughResolver(t *testing.T) {
	ctx, pool, slug, admin, member, _, _ := setupACLTest(t)
	setPrivate(t, ctx, pool, slug, admin, "ALPHA")

	// Plain member: the issue list surface 404s on the private
	// project (resolveIssueProject routes through effective access).
	if _, err := ListIssues(ctx, pool, slug, "ALPHA", member, ListIssuesInput{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ListIssues private non-member: err=%v, want ErrNotFound", err)
	}
	// Teamspace-derived member: creates an issue (effective 15
	// passes the member+ write gate).
	ts := mustTeamspace(t, ctx, pool, slug, admin, "Platform")
	if _, err := AddTeamspaceMember(ctx, pool, slug, ts.ID, admin, member); err != nil {
		t.Fatalf("AddTeamspaceMember: %v", err)
	}
	if _, err := UpdateProject(ctx, pool, slug, "ALPHA", admin, ProjectPatch{
		TeamspaceID: PatchField[string]{Set: true, Value: &ts.ID},
	}); err != nil {
		t.Fatalf("link project: %v", err)
	}
	if _, err := CreateIssue(ctx, pool, slug, "ALPHA", member, CreateIssueInput{Name: "Derived can write"}); err != nil {
		t.Fatalf("derived member create issue: %v", err)
	}
}
