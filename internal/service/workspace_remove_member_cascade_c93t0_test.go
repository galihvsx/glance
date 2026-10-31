package service

// Workspace-removal membership cascade (C93T0): removing a workspace
// member ends EVERYTHING derived from that membership in the same
// transaction — the workspace_members row (existing), the member's
// teamspace_members rows in every teamspace of that workspace (NEW —
// the C90 quirk), and their Lead designations (existing, kept).
// Migration 000079 repairs rows that already dangle.
//
// Honest framing (do not over-claim): a dangling row alone could NOT
// regain project access — EffectiveProjectAccess requires a
// workspace_members row first — so this is a roster-integrity +
// defense-in-depth fix, not a live privilege-escalation hole.

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRemoveMemberCascadesTeamspaceMemberships(t *testing.T) {
	ctx, pool, slug, admin := setupTeamspaceTest(t)
	member := setupTeamspaceMember(t, pool, slug, admin, RoleMember)
	bystander := setupTeamspaceMember(t, pool, slug, admin, RoleMember)

	tsA := mustTeamspace(t, ctx, pool, slug, admin, "Alpha")
	tsB := mustTeamspace(t, ctx, pool, slug, admin, "Beta")
	for _, ts := range []*Teamspace{tsA, tsB} {
		if _, err := AddTeamspaceMember(ctx, pool, slug, ts.ID, admin, member); err != nil {
			t.Fatalf("add member to %s: %v", ts.Name, err)
		}
		if _, err := AddTeamspaceMember(ctx, pool, slug, ts.ID, admin, bystander); err != nil {
			t.Fatalf("add bystander to %s: %v", ts.Name, err)
		}
	}
	// Member is Lead of Alpha: removal must clear the designation.
	if _, err := SetTeamspaceLead(ctx, pool, slug, admin, tsA.ID, &member); err != nil {
		t.Fatalf("SetTeamspaceLead: %v", err)
	}

	// Same user is a member of a SECOND workspace with its own
	// teamspace: removal from the first workspace must leave the
	// second workspace's roster untouched (cascade is scoped by
	// workspace_id, not by user alone).
	slug2 := uniqueTestSlug("ts-ws-cascade-other")
	createTestWorkspace(t, pool, "Other Co", slug2, admin)
	if err := UpsertMember(ctx, pool, slug2, admin, member, RoleMember); err != nil {
		t.Fatalf("UpsertMember other workspace: %v", err)
	}
	tsOther := mustTeamspace(t, ctx, pool, slug2, admin, "Elsewhere")
	if _, err := AddTeamspaceMember(ctx, pool, slug2, tsOther.ID, admin, member); err != nil {
		t.Fatalf("add member to other teamspace: %v", err)
	}

	if err := RemoveMember(ctx, pool, slug, admin, member); err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}

	// Roster of EVERY teamspace in the removed-from workspace no
	// longer lists the member; the creator and the bystander remain.
	for _, ts := range []*Teamspace{tsA, tsB} {
		ids := mustTeamspaceMemberIDs(t, ctx, pool, slug, admin, ts.ID)
		if containsID(ids, member) {
			t.Fatalf("teamspace %s roster = %v, still lists removed member", ts.Name, ids)
		}
		if !containsID(ids, admin) || !containsID(ids, bystander) {
			t.Fatalf("teamspace %s roster = %v, want creator + bystander", ts.Name, ids)
		}
	}

	// Lead designation cleared in the same transaction.
	var lead *string
	if err := pool.QueryRow(ctx, `SELECT lead_user_id::text FROM teamspaces WHERE id = $1::uuid`, tsA.ID).Scan(&lead); err != nil {
		t.Fatalf("lead probe: %v", err)
	}
	if lead != nil {
		t.Fatalf("lead_user_id = %q, want NULL after member removal", *lead)
	}

	// Other workspace's roster is untouched.
	otherIDs := mustTeamspaceMemberIDs(t, ctx, pool, slug2, admin, tsOther.ID)
	if !containsID(otherIDs, member) {
		t.Fatalf("other workspace roster = %v, want removed-from-first-workspace member still present", otherIDs)
	}
}

// TestMigration000079DeletesDanglingRows pins the repair migration's
// semantics: a teamspace_members row whose user holds no
// workspace_members row in that teamspace's workspace is deleted by
// the up file (applied twice: idempotent), while a row for a user
// who IS still a workspace member survives. The dangling row is built
// by deleting the workspace_members row directly — the exact state a
// pre-C93T0 RemoveMember left behind, since the service layer never
// creates it anymore.
func TestMigration000079DeletesDanglingRows(t *testing.T) {
	ctx, pool, slug, admin := setupTeamspaceTest(t)
	dangling := setupTeamspaceMember(t, pool, slug, admin, RoleMember)
	keeper := setupTeamspaceMember(t, pool, slug, admin, RoleMember)

	ts := mustTeamspace(t, ctx, pool, slug, admin, "Platform")
	for _, u := range []string{dangling, keeper} {
		if _, err := AddTeamspaceMember(ctx, pool, slug, ts.ID, admin, u); err != nil {
			t.Fatalf("add %s: %v", u, err)
		}
	}

	// Simulate the pre-C93T0 removal: workspace row gone, teamspace
	// row left dangling.
	wsID := workspaceIDOf(t, ctx, pool, slug)
	if _, err := pool.Exec(ctx, `DELETE FROM workspace_members WHERE workspace_id = $1::uuid AND user_id = $2::uuid`, wsID, dangling); err != nil {
		t.Fatalf("direct workspace_members delete: %v", err)
	}
	if n := teamspaceMemberCount(t, ctx, pool, ts.ID, dangling); n != 1 {
		t.Fatalf("dangling rows before migrate = %d, want 1", n)
	}

	sql, err := os.ReadFile("../../migrations/000079_membership_cascade.up.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("up apply %d: %v", i+1, err)
		}
	}

	if n := teamspaceMemberCount(t, ctx, pool, ts.ID, dangling); n != 0 {
		t.Fatalf("dangling rows after migrate = %d, want 0", n)
	}
	if n := teamspaceMemberCount(t, ctx, pool, ts.ID, keeper); n != 1 {
		t.Fatalf("keeper rows after migrate = %d, want 1 (still a workspace member)", n)
	}
}

func workspaceIDOf(t *testing.T, ctx context.Context, pool *pgxpool.Pool, slug string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM workspaces WHERE slug = $1`, slug).Scan(&id); err != nil {
		t.Fatalf("workspace id probe: %v", err)
	}
	return id
}

func teamspaceMemberCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, teamspaceID, userID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM teamspace_members WHERE teamspace_id = $1::uuid AND user_id = $2::uuid`, teamspaceID, userID).Scan(&n); err != nil {
		t.Fatalf("teamspace_members count probe: %v", err)
	}
	return n
}
