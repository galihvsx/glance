package service

// Teamspace membership + visibility (C90T0): creator auto-membership,
// Admin-only add/remove, member-count on the teamspace JSON, and the
// visibility filter on ListTeamspaces (Admins see all, anyone else
// sees only teamspaces they belong to). Plus the 000077 backfill:
// the migration file must carry the INSERT ... SELECT, and the
// backfill statement's semantics are pinned against a teamspace row
// inserted directly (bypassing CreateTeamspace, so no creator row).

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func mustTeamspaceMemberIDs(t *testing.T, ctx context.Context, pool *pgxpool.Pool, slug, actor, teamspaceID string) []string {
	t.Helper()
	members, err := ListTeamspaceMembers(ctx, pool, slug, teamspaceID, actor)
	if err != nil {
		t.Fatalf("ListTeamspaceMembers: %v", err)
	}
	ids := []string{}
	for _, m := range members {
		ids = append(ids, m.ID)
	}
	return ids
}

func containsID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func TestTeamspaceCreateAutoAddsCreator(t *testing.T) {
	ctx, pool, slug, actor := setupTeamspaceTest(t)

	ts := mustTeamspace(t, ctx, pool, slug, actor, "Platform")
	if ts.MemberCount != 1 {
		t.Fatalf("created teamspace member_count = %d, want 1 (creator auto-member)", ts.MemberCount)
	}
	ids := mustTeamspaceMemberIDs(t, ctx, pool, slug, actor, ts.ID)
	if len(ids) != 1 || ids[0] != actor {
		t.Fatalf("members = %v, want [%s]", ids, actor)
	}
}

func TestTeamspaceAddMemberOKReflectedInList(t *testing.T) {
	ctx, pool, slug, actor := setupTeamspaceTest(t)
	member := setupTeamspaceMember(t, pool, slug, actor, RoleMember)

	ts := mustTeamspace(t, ctx, pool, slug, actor, "Platform")
	added, err := AddTeamspaceMember(ctx, pool, slug, ts.ID, actor, member)
	if err != nil {
		t.Fatalf("AddTeamspaceMember: %v", err)
	}
	if added.ID != member {
		t.Fatalf("added id = %q, want %q", added.ID, member)
	}
	ids := mustTeamspaceMemberIDs(t, ctx, pool, slug, actor, ts.ID)
	if len(ids) != 2 || !containsID(ids, actor) || !containsID(ids, member) {
		t.Fatalf("members = %v, want creator + added member", ids)
	}
	list, err := ListTeamspaces(ctx, pool, slug, actor)
	if err != nil {
		t.Fatalf("ListTeamspaces: %v", err)
	}
	if len(list) != 1 || list[0].MemberCount != 2 {
		t.Fatalf("list member_count = %v, want [2]", list)
	}
}

func TestTeamspaceAddMemberIdempotent(t *testing.T) {
	ctx, pool, slug, actor := setupTeamspaceTest(t)
	member := setupTeamspaceMember(t, pool, slug, actor, RoleMember)

	ts := mustTeamspace(t, ctx, pool, slug, actor, "Platform")
	if _, err := AddTeamspaceMember(ctx, pool, slug, ts.ID, actor, member); err != nil {
		t.Fatalf("first add: %v", err)
	}
	if _, err := AddTeamspaceMember(ctx, pool, slug, ts.ID, actor, member); err != nil {
		t.Fatalf("duplicate add: %v, want idempotent OK", err)
	}
	ids := mustTeamspaceMemberIDs(t, ctx, pool, slug, actor, ts.ID)
	if len(ids) != 2 {
		t.Fatalf("members = %d, want 2 after duplicate add", len(ids))
	}
}

func TestTeamspaceAddNonWorkspaceMemberErrors(t *testing.T) {
	ctx, pool, slug, actor := setupTeamspaceTest(t)
	// Existing user, never added to the workspace.
	outsider := createTestUser(t, pool, uniqueTestEmail("ts-outsider-member"))

	ts := mustTeamspace(t, ctx, pool, slug, actor, "Platform")
	if _, err := AddTeamspaceMember(ctx, pool, slug, ts.ID, actor, outsider); !errors.Is(err, ErrTeamspaceTargetNotMember) {
		t.Fatalf("add non-workspace-member: err = %v, want ErrTeamspaceTargetNotMember", err)
	}
	// A user id that matches nobody at all gets the SAME error: the
	// caller must not learn whether an arbitrary user id exists.
	ghost := "11111111-2222-3333-4444-555555555555"
	if _, err := AddTeamspaceMember(ctx, pool, slug, ts.ID, actor, ghost); !errors.Is(err, ErrTeamspaceTargetNotMember) {
		t.Fatalf("add unknown user: err = %v, want ErrTeamspaceTargetNotMember", err)
	}
	// Malformed target id is the same honest 400 shape, never a ::uuid 500.
	if _, err := AddTeamspaceMember(ctx, pool, slug, ts.ID, actor, "not-a-uuid"); !errors.Is(err, ErrTeamspaceTargetNotMember) {
		t.Fatalf("add malformed user id: err = %v, want ErrTeamspaceTargetNotMember", err)
	}
}

func TestTeamspaceAddByNonAdminForbidden(t *testing.T) {
	ctx, pool, slug, actor := setupTeamspaceTest(t)
	member := setupTeamspaceMember(t, pool, slug, actor, RoleMember)
	target := setupTeamspaceMember(t, pool, slug, actor, RoleMember)

	ts := mustTeamspace(t, ctx, pool, slug, actor, "Platform")
	if _, err := AddTeamspaceMember(ctx, pool, slug, ts.ID, member, target); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member add: err = %v, want ErrForbidden", err)
	}
}

func TestTeamspaceAddForeignTeamspaceNotFound(t *testing.T) {
	ctx, pool, slug, actor := setupTeamspaceTest(t)
	member := setupTeamspaceMember(t, pool, slug, actor, RoleMember)

	slug2 := uniqueTestSlug("ts-ws-foreign")
	createTestWorkspace(t, pool, "Foreign Co", slug2, actor)
	foreign := mustTeamspace(t, ctx, pool, slug2, actor, "Elsewhere")

	if _, err := AddTeamspaceMember(ctx, pool, slug, foreign.ID, actor, member); !errors.Is(err, ErrTeamspaceNotFound) {
		t.Fatalf("add to foreign teamspace: err = %v, want ErrTeamspaceNotFound", err)
	}
}

func TestTeamspaceRemoveMemberOKReflectedInList(t *testing.T) {
	ctx, pool, slug, actor := setupTeamspaceTest(t)
	member := setupTeamspaceMember(t, pool, slug, actor, RoleMember)

	ts := mustTeamspace(t, ctx, pool, slug, actor, "Platform")
	if _, err := AddTeamspaceMember(ctx, pool, slug, ts.ID, actor, member); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := RemoveTeamspaceMember(ctx, pool, slug, ts.ID, actor, member); err != nil {
		t.Fatalf("remove: %v", err)
	}
	ids := mustTeamspaceMemberIDs(t, ctx, pool, slug, actor, ts.ID)
	if len(ids) != 1 || ids[0] != actor {
		t.Fatalf("members after remove = %v, want creator only", ids)
	}
}

func TestTeamspaceRemoveNonMemberIdempotent(t *testing.T) {
	ctx, pool, slug, actor := setupTeamspaceTest(t)
	member := setupTeamspaceMember(t, pool, slug, actor, RoleMember)

	ts := mustTeamspace(t, ctx, pool, slug, actor, "Platform")
	// Never added to this teamspace: removing is a no-op success,
	// not a 404 — DELETE is idempotent by nature.
	if err := RemoveTeamspaceMember(ctx, pool, slug, ts.ID, actor, member); err != nil {
		t.Fatalf("remove non-member: %v, want idempotent OK", err)
	}
}

func TestTeamspaceRemoveByNonAdminForbidden(t *testing.T) {
	ctx, pool, slug, actor := setupTeamspaceTest(t)
	member := setupTeamspaceMember(t, pool, slug, actor, RoleMember)
	target := setupTeamspaceMember(t, pool, slug, actor, RoleMember)

	ts := mustTeamspace(t, ctx, pool, slug, actor, "Platform")
	if _, err := AddTeamspaceMember(ctx, pool, slug, ts.ID, actor, target); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := RemoveTeamspaceMember(ctx, pool, slug, ts.ID, member, target); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member remove: err = %v, want ErrForbidden", err)
	}
}

func TestTeamspaceListMembersAsMemberOK(t *testing.T) {
	ctx, pool, slug, actor := setupTeamspaceTest(t)
	member := setupTeamspaceMember(t, pool, slug, actor, RoleMember)

	ts := mustTeamspace(t, ctx, pool, slug, actor, "Platform")
	if _, err := AddTeamspaceMember(ctx, pool, slug, ts.ID, actor, member); err != nil {
		t.Fatalf("add: %v", err)
	}
	ids := mustTeamspaceMemberIDs(t, ctx, pool, slug, member, ts.ID)
	if len(ids) != 2 || !containsID(ids, actor) || !containsID(ids, member) {
		t.Fatalf("member list = %v, want creator + member", ids)
	}
}

func TestTeamspaceListMembersNonMemberNonAdmin404(t *testing.T) {
	ctx, pool, slug, actor := setupTeamspaceTest(t)
	nonMember := setupTeamspaceMember(t, pool, slug, actor, RoleMember)

	ts := mustTeamspace(t, ctx, pool, slug, actor, "Platform")
	// A workspace member who is not in this teamspace learns nothing:
	// teamspace-not-found, never the roster.
	if _, err := ListTeamspaceMembers(ctx, pool, slug, ts.ID, nonMember); !errors.Is(err, ErrTeamspaceNotFound) {
		t.Fatalf("non-member list: err = %v, want ErrTeamspaceNotFound", err)
	}
}

func TestTeamspaceListMembersAsAdminOK(t *testing.T) {
	ctx, pool, slug, actor := setupTeamspaceTest(t)
	// A second admin who was never added to the teamspace may still
	// read the roster: Admins bypass membership.
	admin2 := setupTeamspaceMember(t, pool, slug, actor, RoleAdmin)

	ts := mustTeamspace(t, ctx, pool, slug, actor, "Platform")
	ids := mustTeamspaceMemberIDs(t, ctx, pool, slug, admin2, ts.ID)
	if len(ids) != 1 || ids[0] != actor {
		t.Fatalf("admin list = %v, want creator only", ids)
	}
}

func TestTeamspaceListVisibilityMemberSeesOnlyOwn(t *testing.T) {
	ctx, pool, slug, actor := setupTeamspaceTest(t)
	member := setupTeamspaceMember(t, pool, slug, actor, RoleMember)

	own := mustTeamspace(t, ctx, pool, slug, actor, "Own")
	mustTeamspace(t, ctx, pool, slug, actor, "Hidden")
	if _, err := AddTeamspaceMember(ctx, pool, slug, own.ID, actor, member); err != nil {
		t.Fatalf("add: %v", err)
	}
	list, err := ListTeamspaces(ctx, pool, slug, member)
	if err != nil {
		t.Fatalf("ListTeamspaces as member: %v", err)
	}
	if len(list) != 1 || list[0].ID != own.ID {
		t.Fatalf("member list = %v, want only the teamspace they belong to", list)
	}
}

func TestTeamspaceListVisibilityAdminSeesAll(t *testing.T) {
	ctx, pool, slug, actor := setupTeamspaceTest(t)
	mustTeamspace(t, ctx, pool, slug, actor, "Alpha")
	mustTeamspace(t, ctx, pool, slug, actor, "Beta")

	list, err := ListTeamspaces(ctx, pool, slug, actor)
	if err != nil {
		t.Fatalf("ListTeamspaces as admin: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("admin list = %d teamspaces, want 2", len(list))
	}
}

func TestTeamspaceMigration077ContainsBackfill(t *testing.T) {
	sql, err := os.ReadFile("../../migrations/000077_teamspace_members.up.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	for _, want := range []string{
		"CREATE TABLE IF NOT EXISTS teamspace_members",
		"INSERT INTO teamspace_members",
		"FROM teamspaces t",
		"JOIN workspace_members m ON m.workspace_id = t.workspace_id",
		"ON CONFLICT DO NOTHING",
		"teamspace_members_user_idx",
	} {
		if !strings.Contains(string(sql), want) {
			t.Fatalf("migration missing %q", want)
		}
	}
}

func TestTeamspaceBackfillSemantics(t *testing.T) {
	ctx, pool, slug, actor := setupTeamspaceTest(t)
	member := setupTeamspaceMember(t, pool, slug, actor, RoleMember)

	// Insert a teamspace row directly, bypassing CreateTeamspace, so
	// it carries NO membership rows — the state the 000077 backfill
	// meets on a pre-existing workspace.
	var tsID string
	err := pool.QueryRow(ctx,
		`INSERT INTO teamspaces (workspace_id, name)
		 SELECT w.id, 'Legacy' FROM workspaces w WHERE w.slug = $1
		 RETURNING id::text`, slug).Scan(&tsID)
	if err != nil {
		t.Fatalf("insert legacy teamspace: %v", err)
	}
	// The backfill statement, verbatim from migration 000077.
	if _, err := pool.Exec(ctx,
		`INSERT INTO teamspace_members (id, teamspace_id, user_id)
		 SELECT gen_random_uuid(), t.id, m.user_id
		 FROM teamspaces t
		 JOIN workspace_members m ON m.workspace_id = t.workspace_id
		 ON CONFLICT DO NOTHING`); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	// Both workspace members now see the legacy teamspace.
	for _, viewer := range []string{actor, member} {
		list, err := ListTeamspaces(ctx, pool, slug, viewer)
		if err != nil {
			t.Fatalf("ListTeamspaces as %s: %v", viewer, err)
		}
		if len(list) != 1 || list[0].ID != tsID {
			t.Fatalf("list as %s = %v, want the backfilled teamspace", viewer, list)
		}
	}
}

func TestTeamspaceDeleteRemovesMemberships(t *testing.T) {
	ctx, pool, slug, actor := setupTeamspaceTest(t)
	member := setupTeamspaceMember(t, pool, slug, actor, RoleMember)

	ts := mustTeamspace(t, ctx, pool, slug, actor, "Platform")
	if _, err := AddTeamspaceMember(ctx, pool, slug, ts.ID, actor, member); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := DeleteTeamspace(ctx, pool, slug, actor, ts.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	// The roster endpoint now reports the teamspace itself as gone.
	if _, err := ListTeamspaceMembers(ctx, pool, slug, ts.ID, actor); !errors.Is(err, ErrTeamspaceNotFound) {
		t.Fatalf("members after delete: err = %v, want ErrTeamspaceNotFound", err)
	}
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM teamspace_members WHERE teamspace_id = $1::uuid`, ts.ID).Scan(&n); err != nil {
		t.Fatalf("count memberships: %v", err)
	}
	if n != 0 {
		t.Fatalf("membership rows after delete = %d, want 0 (ON DELETE CASCADE)", n)
	}
}

// TestTeamspaceListMembersOrderedByName pins the workspace listMembers
// ordering precedent (COALESCE(name, email)) on the teamspace roster.
func TestTeamspaceListMembersOrderedByName(t *testing.T) {
	ctx, pool, slug, actor := setupTeamspaceTest(t)

	// Named users sort by name, not by insertion order or email.
	zeta := createTestUser(t, pool, uniqueTestEmail("ts-zeta"))
	if _, err := pool.Exec(ctx, `UPDATE users SET name = 'Zeta' WHERE id = $1::uuid`, zeta); err != nil {
		t.Fatalf("name zeta: %v", err)
	}
	alpha := createTestUser(t, pool, uniqueTestEmail("ts-alpha"))
	if _, err := pool.Exec(ctx, `UPDATE users SET name = 'Alpha' WHERE id = $1::uuid`, alpha); err != nil {
		t.Fatalf("name alpha: %v", err)
	}
	for _, u := range []string{zeta, alpha} {
		if err := UpsertMember(ctx, pool, slug, actor, u, RoleMember); err != nil {
			t.Fatalf("UpsertMember: %v", err)
		}
	}
	ts := mustTeamspace(t, ctx, pool, slug, actor, "Platform")
	for _, u := range []string{zeta, alpha} {
		if _, err := AddTeamspaceMember(ctx, pool, slug, ts.ID, actor, u); err != nil {
			t.Fatalf("add: %v", err)
		}
	}
	members, err := ListTeamspaceMembers(ctx, pool, slug, ts.ID, actor)
	if err != nil {
		t.Fatalf("ListTeamspaceMembers: %v", err)
	}
	if len(members) != 3 {
		t.Fatalf("members = %d, want 3", len(members))
	}
	// Creator has no display name: sorts by email (COALESCE fallback),
	// which is a ts-* address — after the two named rows, since
	// "Alpha"/"Zeta" sort before... assert only the named pair order:
	// Alpha must precede Zeta wherever the unnamed creator lands.
	alphaAt, zetaAt := -1, -1
	for i, m := range members {
		if m.ID == alpha {
			alphaAt = i
		}
		if m.ID == zeta {
			zetaAt = i
		}
	}
	if alphaAt == -1 || zetaAt == -1 || alphaAt > zetaAt {
		t.Fatalf("named order wrong: alpha@%d zeta@%d members=%v", alphaAt, zetaAt, members)
	}
}
