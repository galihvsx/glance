package service

// Teamspaces (C89T0): workspace-scoped project groups. CRUD, tenancy,
// name rules, delete-ungroups semantics, and the UpdateProject
// teamspace_id tri-state (absent = unchanged, null = ungroup, UUID =
// move) — including the foreign-workspace 400 and the archived-keeps-
// teamspace invariant.

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func setupTeamspaceTest(t *testing.T) (context.Context, *pgxpool.Pool, string, string) {
	t.Helper()
	ctx := context.Background()
	pool := newTestPool(t)
	actor := createTestUser(t, pool, uniqueTestEmail("ts"))
	slug := uniqueTestSlug("ts-ws")
	createTestWorkspace(t, pool, "Teamspace Co", slug, actor)
	return ctx, pool, slug, actor
}

func setupTeamspaceMember(t *testing.T, pool *pgxpool.Pool, slug, adminID string, role int) string {
	t.Helper()
	member := createTestUser(t, pool, uniqueTestEmail("ts-member"))
	if err := UpsertMember(context.Background(), pool, slug, adminID, member, role); err != nil {
		t.Fatalf("UpsertMember role %d: %v", role, err)
	}
	return member
}

func mustTeamspace(t *testing.T, ctx context.Context, pool *pgxpool.Pool, slug, actor, name string) *Teamspace {
	t.Helper()
	ts, err := CreateTeamspace(ctx, pool, slug, actor, name)
	if err != nil {
		t.Fatalf("CreateTeamspace %q: %v", name, err)
	}
	return ts
}

func moveProjectToTeamspace(t *testing.T, ctx context.Context, pool *pgxpool.Pool, slug, identifier, actor, teamspaceID string) *Project {
	t.Helper()
	p, err := UpdateProject(ctx, pool, slug, identifier, actor, ProjectPatch{
		TeamspaceID: PatchField[string]{Set: true, Value: &teamspaceID},
	})
	if err != nil {
		t.Fatalf("UpdateProject move %s -> %s: %v", identifier, teamspaceID, err)
	}
	return p
}

func TestTeamspaceCreateTrimsAndRejectsEmpty(t *testing.T) {
	ctx, pool, slug, actor := setupTeamspaceTest(t)

	ts := mustTeamspace(t, ctx, pool, slug, actor, "  Platform  ")
	if ts.Name != "Platform" {
		t.Fatalf("name = %q, want trimmed %q", ts.Name, "Platform")
	}
	if ts.ProjectCount != 0 {
		t.Fatalf("project_count = %d, want 0", ts.ProjectCount)
	}

	for _, bad := range []string{"", "   "} {
		if _, err := CreateTeamspace(ctx, pool, slug, actor, bad); !errors.Is(err, ErrNameRequired) {
			t.Fatalf("CreateTeamspace %q: err = %v, want ErrNameRequired", bad, err)
		}
	}
}

func TestTeamspaceDuplicateSameWorkspace(t *testing.T) {
	ctx, pool, slug, actor := setupTeamspaceTest(t)

	mustTeamspace(t, ctx, pool, slug, actor, "Platform")
	if _, err := CreateTeamspace(ctx, pool, slug, actor, "Platform"); !errors.Is(err, ErrTeamspaceConflict) {
		t.Fatalf("duplicate create: err = %v, want ErrTeamspaceConflict", err)
	}
	// Trimmed duplicates collide too: " Platform " is "Platform".
	if _, err := CreateTeamspace(ctx, pool, slug, actor, " Platform "); !errors.Is(err, ErrTeamspaceConflict) {
		t.Fatalf("trimmed duplicate create: err = %v, want ErrTeamspaceConflict", err)
	}
}

func TestTeamspaceSameNameOtherWorkspaceOK(t *testing.T) {
	ctx, pool, slug, actor := setupTeamspaceTest(t)
	mustTeamspace(t, ctx, pool, slug, actor, "Platform")

	slug2 := uniqueTestSlug("ts-ws2")
	createTestWorkspace(t, pool, "Other Co", slug2, actor)
	if _, err := CreateTeamspace(ctx, pool, slug2, actor, "Platform"); err != nil {
		t.Fatalf("same name in another workspace: %v", err)
	}
}

func TestTeamspaceListOrderedByNameWithCounts(t *testing.T) {
	ctx, pool, slug, actor := setupTeamspaceTest(t)

	beta := mustTeamspace(t, ctx, pool, slug, actor, "Beta")
	alpha := mustTeamspace(t, ctx, pool, slug, actor, "Alpha")
	mustTeamspace(t, ctx, pool, slug, actor, "Gamma")

	p1 := createTestProject(t, pool, slug, actor, "P One", uniqueTestIdentifier())
	p2 := createTestProject(t, pool, slug, actor, "P Two", uniqueTestIdentifier())
	p3 := createTestProject(t, pool, slug, actor, "P Three", uniqueTestIdentifier())
	moveProjectToTeamspace(t, ctx, pool, slug, p1.Identifier, actor, beta.ID)
	moveProjectToTeamspace(t, ctx, pool, slug, p2.Identifier, actor, beta.ID)
	moveProjectToTeamspace(t, ctx, pool, slug, p3.Identifier, actor, alpha.ID)

	list, err := ListTeamspaces(ctx, pool, slug, actor)
	if err != nil {
		t.Fatalf("ListTeamspaces: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("len = %d, want 3", len(list))
	}
	want := []struct {
		name  string
		count int64
	}{
		{"Alpha", 1}, {"Beta", 2}, {"Gamma", 0},
	}
	for i, w := range want {
		if list[i].Name != w.name || list[i].ProjectCount != w.count {
			t.Fatalf("list[%d] = (%q, %d), want (%q, %d)", i, list[i].Name, list[i].ProjectCount, w.name, w.count)
		}
	}
}

func TestTeamspaceRenameRules(t *testing.T) {
	ctx, pool, slug, actor := setupTeamspaceTest(t)

	ts := mustTeamspace(t, ctx, pool, slug, actor, "Platform")
	mustTeamspace(t, ctx, pool, slug, actor, "Other")

	// Empty rename → error.
	empty := "   "
	if _, err := UpdateTeamspace(ctx, pool, slug, actor, ts.ID, TeamspacePatch{Name: &empty}); !errors.Is(err, ErrNameRequired) {
		t.Fatalf("rename to empty: err = %v, want ErrNameRequired", err)
	}
	// Rename to an existing name → conflict.
	other := "Other"
	if _, err := UpdateTeamspace(ctx, pool, slug, actor, ts.ID, TeamspacePatch{Name: &other}); !errors.Is(err, ErrTeamspaceConflict) {
		t.Fatalf("rename to existing: err = %v, want ErrTeamspaceConflict", err)
	}
	// No-op rename to the same name → success, same row.
	same := "Platform"
	got, err := UpdateTeamspace(ctx, pool, slug, actor, ts.ID, TeamspacePatch{Name: &same})
	if err != nil {
		t.Fatalf("no-op rename: %v", err)
	}
	if got.Name != "Platform" {
		t.Fatalf("no-op rename name = %q, want Platform", got.Name)
	}
	// Empty patch → nothing to update.
	if _, err := UpdateTeamspace(ctx, pool, slug, actor, ts.ID, TeamspacePatch{}); !errors.Is(err, ErrNothingToUpdate) {
		t.Fatalf("empty patch: err = %v, want ErrNothingToUpdate", err)
	}
	// Real rename trims.
	renamed := "  Platform Two  "
	got, err = UpdateTeamspace(ctx, pool, slug, actor, ts.ID, TeamspacePatch{Name: &renamed})
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	if got.Name != "Platform Two" {
		t.Fatalf("renamed name = %q, want Platform Two", got.Name)
	}
}

func TestTeamspaceDeleteUngroupsProjects(t *testing.T) {
	ctx, pool, slug, actor := setupTeamspaceTest(t)

	ts := mustTeamspace(t, ctx, pool, slug, actor, "Platform")
	p1 := createTestProject(t, pool, slug, actor, "P One", uniqueTestIdentifier())
	p2 := createTestProject(t, pool, slug, actor, "P Two", uniqueTestIdentifier())
	p3 := createTestProject(t, pool, slug, actor, "P Three", uniqueTestIdentifier())
	moveProjectToTeamspace(t, ctx, pool, slug, p1.Identifier, actor, ts.ID)
	moveProjectToTeamspace(t, ctx, pool, slug, p2.Identifier, actor, ts.ID)
	// p3 stays ungrouped and must not count.

	count, err := DeleteTeamspace(ctx, pool, slug, actor, ts.ID)
	if err != nil {
		t.Fatalf("DeleteTeamspace: %v", err)
	}
	if count != 2 {
		t.Fatalf("ungrouped count = %d, want 2", count)
	}

	// Projects still exist, now with NULL teamspace_id.
	for _, ident := range []string{p1.Identifier, p2.Identifier} {
		got, err := GetProject(ctx, pool, slug, ident, actor)
		if err != nil {
			t.Fatalf("GetProject %s: %v", ident, err)
		}
		if got.TeamspaceID != nil {
			t.Fatalf("project %s teamspace_id = %q, want nil", ident, *got.TeamspaceID)
		}
	}
	// The teamspace itself is gone.
	list, err := ListTeamspaces(ctx, pool, slug, actor)
	if err != nil {
		t.Fatalf("ListTeamspaces: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("list after delete = %d teamspaces, want 0", len(list))
	}
	renamed := "Back"
	if _, err := UpdateTeamspace(ctx, pool, slug, actor, ts.ID, TeamspacePatch{Name: &renamed}); !errors.Is(err, ErrTeamspaceNotFound) {
		t.Fatalf("update deleted teamspace: err = %v, want ErrTeamspaceNotFound", err)
	}
	// p3 untouched: still ungrouped, still listed.
	got, err := GetProject(ctx, pool, slug, p3.Identifier, actor)
	if err != nil {
		t.Fatalf("GetProject ungrouped: %v", err)
	}
	if got.TeamspaceID != nil {
		t.Fatalf("ungrouped project gained teamspace_id %q", *got.TeamspaceID)
	}
}

func TestTeamspaceDeleteEmptyOK(t *testing.T) {
	ctx, pool, slug, actor := setupTeamspaceTest(t)

	ts := mustTeamspace(t, ctx, pool, slug, actor, "Empty")
	count, err := DeleteTeamspace(ctx, pool, slug, actor, ts.ID)
	if err != nil {
		t.Fatalf("DeleteTeamspace empty: %v", err)
	}
	if count != 0 {
		t.Fatalf("ungrouped count = %d, want 0", count)
	}
}

func TestTeamspaceViewerForbidden(t *testing.T) {
	ctx, pool, slug, actor := setupTeamspaceTest(t)
	viewer := setupTeamspaceMember(t, pool, slug, actor, RoleViewer)

	// Viewer reads fine (initiative pattern: any member may read).
	if _, err := ListTeamspaces(ctx, pool, slug, viewer); err != nil {
		t.Fatalf("viewer list: %v", err)
	}
	// Viewer writes → 403 shape.
	if _, err := CreateTeamspace(ctx, pool, slug, viewer, "Nope"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer create: err = %v, want ErrForbidden", err)
	}
	ts := mustTeamspace(t, ctx, pool, slug, actor, "Platform")
	name := "Renamed"
	if _, err := UpdateTeamspace(ctx, pool, slug, viewer, ts.ID, TeamspacePatch{Name: &name}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer rename: err = %v, want ErrForbidden", err)
	}
	if _, err := DeleteTeamspace(ctx, pool, slug, viewer, ts.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer delete: err = %v, want ErrForbidden", err)
	}
}

func TestTeamspaceNonMember404(t *testing.T) {
	ctx, pool, slug, actor := setupTeamspaceTest(t)
	outsider := createTestUser(t, pool, uniqueTestEmail("ts-outsider"))

	// Non-member learns nothing: workspace-not-found, never a list.
	if _, err := ListTeamspaces(ctx, pool, slug, outsider); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-member list: err = %v, want ErrNotFound", err)
	}
	if _, err := CreateTeamspace(ctx, pool, slug, outsider, "Nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-member create: err = %v, want ErrNotFound", err)
	}
	// Same for a bad slug for a member: no existence leak either way.
	if _, err := ListTeamspaces(ctx, pool, "no-such-workspace", actor); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bad slug list: err = %v, want ErrNotFound", err)
	}
}

func TestTeamspaceUnknownAndMalformedIDs(t *testing.T) {
	ctx, pool, slug, actor := setupTeamspaceTest(t)

	unknown := "00000000-0000-0000-0000-000000000000"
	name := "X"
	if _, err := UpdateTeamspace(ctx, pool, slug, actor, unknown, TeamspacePatch{Name: &name}); !errors.Is(err, ErrTeamspaceNotFound) {
		t.Fatalf("update unknown: err = %v, want ErrTeamspaceNotFound", err)
	}
	if _, err := DeleteTeamspace(ctx, pool, slug, actor, unknown); !errors.Is(err, ErrTeamspaceNotFound) {
		t.Fatalf("delete unknown: err = %v, want ErrTeamspaceNotFound", err)
	}
	if _, err := UpdateTeamspace(ctx, pool, slug, actor, "not-a-uuid", TeamspacePatch{Name: &name}); !errors.Is(err, ErrInvalidTeamspaceID) {
		t.Fatalf("update malformed: err = %v, want ErrInvalidTeamspaceID", err)
	}
	if _, err := DeleteTeamspace(ctx, pool, slug, actor, "not-a-uuid"); !errors.Is(err, ErrInvalidTeamspaceID) {
		t.Fatalf("delete malformed: err = %v, want ErrInvalidTeamspaceID", err)
	}
}

func TestTeamspaceMoveProjectOKReflectedInList(t *testing.T) {
	ctx, pool, slug, actor := setupTeamspaceTest(t)

	ts := mustTeamspace(t, ctx, pool, slug, actor, "Platform")
	p := createTestProject(t, pool, slug, actor, "P One", uniqueTestIdentifier())
	if p.TeamspaceID != nil {
		t.Fatalf("new project teamspace_id = %q, want nil", *p.TeamspaceID)
	}

	moved := moveProjectToTeamspace(t, ctx, pool, slug, p.Identifier, actor, ts.ID)
	if moved.TeamspaceID == nil || *moved.TeamspaceID != ts.ID {
		t.Fatalf("moved teamspace_id = %v, want %s", moved.TeamspaceID, ts.ID)
	}

	projects, err := ListProjects(ctx, pool, slug, actor, false, nil)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projects) != 1 || projects[0].TeamspaceID == nil || *projects[0].TeamspaceID != ts.ID {
		t.Fatalf("ListProjects teamspace_id = %v, want %s", projects, ts.ID)
	}
	// Default ordering unchanged: still ordered by name (single row
	// here; multi-row order is covered by the list-order test below
	// via the name sort of the projects page contract).
}

func TestTeamspaceMoveForeignAndUnknown400(t *testing.T) {
	ctx, pool, slug, actor := setupTeamspaceTest(t)

	slug2 := uniqueTestSlug("ts-ws2")
	createTestWorkspace(t, pool, "Other Co", slug2, actor)
	foreign := mustTeamspace(t, ctx, pool, slug2, actor, "Foreign")

	p := createTestProject(t, pool, slug, actor, "P One", uniqueTestIdentifier())

	// Foreign-workspace teamspace → honest 400, never a silent link.
	if _, err := UpdateProject(ctx, pool, slug, p.Identifier, actor, ProjectPatch{
		TeamspaceID: PatchField[string]{Set: true, Value: &foreign.ID},
	}); !errors.Is(err, ErrInvalidTeamspace) {
		t.Fatalf("foreign move: err = %v, want ErrInvalidTeamspace", err)
	}
	// Unknown UUID → same 400 (no cross-workspace existence leak).
	unknown := "00000000-0000-0000-0000-000000000000"
	if _, err := UpdateProject(ctx, pool, slug, p.Identifier, actor, ProjectPatch{
		TeamspaceID: PatchField[string]{Set: true, Value: &unknown},
	}); !errors.Is(err, ErrInvalidTeamspace) {
		t.Fatalf("unknown move: err = %v, want ErrInvalidTeamspace", err)
	}
	// Malformed id → same 400, never a ::uuid 500.
	malformed := "not-a-uuid"
	if _, err := UpdateProject(ctx, pool, slug, p.Identifier, actor, ProjectPatch{
		TeamspaceID: PatchField[string]{Set: true, Value: &malformed},
	}); !errors.Is(err, ErrInvalidTeamspace) {
		t.Fatalf("malformed move: err = %v, want ErrInvalidTeamspace", err)
	}
	// Failed moves left the project ungrouped.
	got, err := GetProject(ctx, pool, slug, p.Identifier, actor)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if got.TeamspaceID != nil {
		t.Fatalf("failed move changed teamspace_id to %q", *got.TeamspaceID)
	}
}

func TestTeamspaceUngroupViaExplicitNull(t *testing.T) {
	ctx, pool, slug, actor := setupTeamspaceTest(t)

	ts := mustTeamspace(t, ctx, pool, slug, actor, "Platform")
	p := createTestProject(t, pool, slug, actor, "P One", uniqueTestIdentifier())
	moveProjectToTeamspace(t, ctx, pool, slug, p.Identifier, actor, ts.ID)

	ungrouped, err := UpdateProject(ctx, pool, slug, p.Identifier, actor, ProjectPatch{
		TeamspaceID: PatchField[string]{Set: true, Value: nil},
	})
	if err != nil {
		t.Fatalf("ungroup: %v", err)
	}
	if ungrouped.TeamspaceID != nil {
		t.Fatalf("ungrouped teamspace_id = %q, want nil", *ungrouped.TeamspaceID)
	}
}

func TestTeamspacePatchAbsentLeavesAssignmentUnchanged(t *testing.T) {
	ctx, pool, slug, actor := setupTeamspaceTest(t)

	ts := mustTeamspace(t, ctx, pool, slug, actor, "Platform")
	p := createTestProject(t, pool, slug, actor, "P One", uniqueTestIdentifier())
	moveProjectToTeamspace(t, ctx, pool, slug, p.Identifier, actor, ts.ID)

	// A PATCH that touches only the name must not disturb the
	// assignment — TeamspaceID.Set stays false.
	renamed := "P One Renamed"
	got, err := UpdateProject(ctx, pool, slug, p.Identifier, actor, ProjectPatch{Name: &renamed})
	if err != nil {
		t.Fatalf("name-only patch: %v", err)
	}
	if got.TeamspaceID == nil || *got.TeamspaceID != ts.ID {
		t.Fatalf("name-only patch teamspace_id = %v, want %s", got.TeamspaceID, ts.ID)
	}
}

func TestTeamspaceArchivedKeepsTeamspace(t *testing.T) {
	ctx, pool, slug, actor := setupTeamspaceTest(t)

	ts := mustTeamspace(t, ctx, pool, slug, actor, "Platform")
	p := createTestProject(t, pool, slug, actor, "P One", uniqueTestIdentifier())
	moveProjectToTeamspace(t, ctx, pool, slug, p.Identifier, actor, ts.ID)

	if _, err := ArchiveProject(ctx, pool, slug, p.Identifier, actor, true); err != nil {
		t.Fatalf("ArchiveProject: %v", err)
	}
	// Archiving does not ungroup: the archived project still carries
	// its teamspace_id (visible via the archived-inclusive list).
	projects, err := ListProjects(ctx, pool, slug, actor, true, nil)
	if err != nil {
		t.Fatalf("ListProjects archived: %v", err)
	}
	if len(projects) != 1 || projects[0].TeamspaceID == nil || *projects[0].TeamspaceID != ts.ID {
		t.Fatalf("archived project teamspace_id = %v, want %s", projects, ts.ID)
	}
	// ...and it is excluded from the live list, as before.
	live, err := ListProjects(ctx, pool, slug, actor, false, nil)
	if err != nil {
		t.Fatalf("ListProjects live: %v", err)
	}
	if len(live) != 0 {
		t.Fatalf("live list = %d projects, want 0", len(live))
	}
}

func TestTeamspaceListCountExcludesArchivedDeleteCountsAll(t *testing.T) {
	ctx, pool, slug, actor := setupTeamspaceTest(t)

	ts := mustTeamspace(t, ctx, pool, slug, actor, "Platform")
	live := createTestProject(t, pool, slug, actor, "Live", uniqueTestIdentifier())
	arch := createTestProject(t, pool, slug, actor, "Arch", uniqueTestIdentifier())
	moveProjectToTeamspace(t, ctx, pool, slug, live.Identifier, actor, ts.ID)
	moveProjectToTeamspace(t, ctx, pool, slug, arch.Identifier, actor, ts.ID)
	if _, err := ArchiveProject(ctx, pool, slug, arch.Identifier, actor, true); err != nil {
		t.Fatalf("ArchiveProject: %v", err)
	}

	// List count = live only (the sidebar header over live rows).
	list, err := ListTeamspaces(ctx, pool, slug, actor)
	if err != nil {
		t.Fatalf("ListTeamspaces: %v", err)
	}
	if len(list) != 1 || list[0].ProjectCount != 1 {
		t.Fatalf("list count = %v, want 1 live project", list)
	}

	// Delete count = every row the FK ungroups, archived included.
	count, err := DeleteTeamspace(ctx, pool, slug, actor, ts.ID)
	if err != nil {
		t.Fatalf("DeleteTeamspace: %v", err)
	}
	if count != 2 {
		t.Fatalf("delete ungrouped count = %d, want 2", count)
	}
	// The archived project is ungrouped too, still archived.
	projects, err := ListProjects(ctx, pool, slug, actor, true, nil)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	for _, p := range projects {
		if p.TeamspaceID != nil {
			t.Fatalf("project %s still grouped after delete", p.Identifier)
		}
	}
	_ = live
}

func TestTeamspaceProjectListOrderingUnchanged(t *testing.T) {
	ctx, pool, slug, actor := setupTeamspaceTest(t)

	ts := mustTeamspace(t, ctx, pool, slug, actor, "Platform")
	// Names deliberately out of creation order; grouping must not
	// change the list's name ordering.
	createTestProject(t, pool, slug, actor, "Zulu", uniqueTestIdentifier())
	beta := createTestProject(t, pool, slug, actor, "Beta", uniqueTestIdentifier())
	createTestProject(t, pool, slug, actor, "Alpha", uniqueTestIdentifier())
	moveProjectToTeamspace(t, ctx, pool, slug, beta.Identifier, actor, ts.ID)

	projects, err := ListProjects(ctx, pool, slug, actor, false, nil)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	want := []string{"Alpha", "Beta", "Zulu"}
	if len(projects) != len(want) {
		t.Fatalf("len = %d, want %d", len(projects), len(want))
	}
	for i, name := range want {
		if projects[i].Name != name {
			t.Fatalf("projects[%d] = %q, want %q", i, projects[i].Name, name)
		}
	}
}
