package service

// Initiatives (C27T0): workspace-scoped program containers grouping
// projects. CRUD, tenancy, project membership, rollup math, and archived
// filtering.

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func setupInitiativeTest(t *testing.T) (context.Context, *pgxpool.Pool, string, string) {
	t.Helper()
	ctx := context.Background()
	pool := newTestPool(t)
	actor := createTestUser(t, pool, uniqueTestEmail("init"))
	slug := uniqueTestSlug("init-ws")
	createTestWorkspace(t, pool, "Init Co", slug, actor)
	return ctx, pool, slug, actor
}

func setupInitiativeGuest(t *testing.T, pool *pgxpool.Pool, slug, adminID string) string {
	t.Helper()
	guest := createTestUser(t, pool, uniqueTestEmail("init-guest"))
	if err := UpsertMember(context.Background(), pool, slug, adminID, guest, RoleGuest); err != nil {
		t.Fatalf("UpsertMember guest: %v", err)
	}
	return guest
}

func TestInitiativeCRUD(t *testing.T) {
	ctx, pool, slug, actor := setupInitiativeTest(t)

	desc := "Platform overhaul"
	init, err := CreateInitiative(ctx, pool, slug, actor, InitiativeInput{
		Name:        "Overhaul 2026",
		Description: &desc,
	})
	if err != nil {
		t.Fatalf("CreateInitiative: %v", err)
	}
	if init.Name != "Overhaul 2026" || init.Status != "active" {
		t.Fatalf("got %+v", init)
	}
	if init.Description == nil || *init.Description != desc {
		t.Fatalf("description = %v", init.Description)
	}
	if init.WorkspaceID == "" || init.CreatedBy == nil || *init.CreatedBy != actor {
		t.Fatalf("tenancy/created_by = %+v", init)
	}
	if init.ProjectCount != 0 {
		t.Fatalf("project_count = %d, want 0", init.ProjectCount)
	}

	got, err := GetInitiative(ctx, pool, slug, actor, init.ID)
	if err != nil {
		t.Fatalf("GetInitiative: %v", err)
	}
	if got.ID != init.ID {
		t.Fatalf("got %+v", got)
	}

	list, err := ListInitiatives(ctx, pool, slug, actor, false)
	if err != nil {
		t.Fatalf("ListInitiatives: %v", err)
	}
	if len(list) != 1 || list[0].ID != init.ID {
		t.Fatalf("list = %+v", list)
	}

	// PATCH: name + status.
	updated, err := UpdateInitiative(ctx, pool, slug, actor, init.ID, InitiativePatch{
		Name:   strptr("Overhaul 2027"),
		Status: strptr("completed"),
	})
	if err != nil {
		t.Fatalf("UpdateInitiative: %v", err)
	}
	if updated.Name != "Overhaul 2027" || updated.Status != "completed" {
		t.Fatalf("updated = %+v", updated)
	}
	if updated.Description == nil || *updated.Description != desc {
		t.Fatalf("description lost = %v", updated.Description)
	}

	// Clear description with "".
	updated, err = UpdateInitiative(ctx, pool, slug, actor, init.ID, InitiativePatch{
		Description: strptr(""),
	})
	if err != nil {
		t.Fatalf("UpdateInitiative clear desc: %v", err)
	}
	if updated.Description != nil {
		t.Fatalf("description = %v, want nil", *updated.Description)
	}

	// Bad status vocabulary is rejected.
	if _, err := UpdateInitiative(ctx, pool, slug, actor, init.ID, InitiativePatch{Status: strptr("flying")}); !errors.Is(err, ErrInvalidInitiative) {
		t.Fatalf("bad status err = %v, want ErrInvalidInitiative", err)
	}

	// Empty patch is ErrNothingToUpdate.
	if _, err := UpdateInitiative(ctx, pool, slug, actor, init.ID, InitiativePatch{}); !errors.Is(err, ErrNothingToUpdate) {
		t.Fatalf("empty patch err = %v, want ErrNothingToUpdate", err)
	}

	// Name required on create; conflict on duplicate name in workspace.
	if _, err := CreateInitiative(ctx, pool, slug, actor, InitiativeInput{Name: "  "}); !errors.Is(err, ErrNameRequired) {
		t.Fatalf("blank name err = %v, want ErrNameRequired", err)
	}
	if _, err := CreateInitiative(ctx, pool, slug, actor, InitiativeInput{Name: "Overhaul 2027"}); !errors.Is(err, ErrInitiativeConflict) {
		t.Fatalf("dup name err = %v, want ErrInitiativeConflict", err)
	}

	if err := DeleteInitiative(ctx, pool, slug, actor, init.ID); err != nil {
		t.Fatalf("DeleteInitiative: %v", err)
	}
	if _, err := GetInitiative(ctx, pool, slug, actor, init.ID); !errors.Is(err, ErrInitiativeNotFound) {
		t.Fatalf("after delete err = %v, want ErrInitiativeNotFound", err)
	}
}

func TestInitiativeTenancy(t *testing.T) {
	ctx, pool, slug, actor := setupInitiativeTest(t)
	guest := setupInitiativeGuest(t, pool, slug, actor)

	otherActor := createTestUser(t, pool, uniqueTestEmail("init-other"))
	otherSlug := uniqueTestSlug("init-ws-b")
	createTestWorkspace(t, pool, "Other Co", otherSlug, otherActor)

	init, err := CreateInitiative(ctx, pool, slug, actor, InitiativeInput{Name: "Secret"})
	if err != nil {
		t.Fatalf("CreateInitiative: %v", err)
	}

	// Guest can read but cannot write.
	if _, err := GetInitiative(ctx, pool, slug, guest, init.ID); err != nil {
		t.Fatalf("guest GetInitiative: %v", err)
	}
	if _, err := CreateInitiative(ctx, pool, slug, guest, InitiativeInput{Name: "Nope"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest create err = %v, want ErrForbidden", err)
	}
	if _, err := UpdateInitiative(ctx, pool, slug, guest, init.ID, InitiativePatch{Name: strptr("Nope")}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest update err = %v, want ErrForbidden", err)
	}
	if err := DeleteInitiative(ctx, pool, slug, guest, init.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest delete err = %v, want ErrForbidden", err)
	}
	if err := AddInitiativeProject(ctx, pool, slug, guest, init.ID, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest add-project err = %v, want ErrForbidden", err)
	}

	// Workspace B cannot see workspace A's initiative — 404, not 403.
	// otherActor is a member of workspace B, so the resolution succeeds
	// and the lookup misses: ErrInitiativeNotFound, which maps to 404.
	if _, err := GetInitiative(ctx, pool, otherSlug, otherActor, init.ID); !errors.Is(err, ErrInitiativeNotFound) {
		t.Fatalf("cross-ws get err = %v, want ErrInitiativeNotFound", err)
	}
	if _, err := UpdateInitiative(ctx, pool, otherSlug, otherActor, init.ID, InitiativePatch{Name: strptr("x")}); !errors.Is(err, ErrInitiativeNotFound) {
		t.Fatalf("cross-ws update err = %v, want ErrInitiativeNotFound", err)
	}
	if err := DeleteInitiative(ctx, pool, otherSlug, otherActor, init.ID); !errors.Is(err, ErrInitiativeNotFound) {
		t.Fatalf("cross-ws delete err = %v, want ErrInitiativeNotFound", err)
	}
	if err := AddInitiativeProject(ctx, pool, otherSlug, otherActor, init.ID, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, ErrInitiativeNotFound) {
		t.Fatalf("cross-ws add-project err = %v, want ErrInitiativeNotFound", err)
	}
	if err := RemoveInitiativeProject(ctx, pool, otherSlug, otherActor, init.ID, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, ErrInitiativeNotFound) {
		t.Fatalf("cross-ws remove-project err = %v, want ErrInitiativeNotFound", err)
	}
	if _, err := GetInitiativeRollup(ctx, pool, otherSlug, otherActor, init.ID); !errors.Is(err, ErrInitiativeNotFound) {
		t.Fatalf("cross-ws rollup err = %v, want ErrInitiativeNotFound", err)
	}
	list, err := ListInitiatives(ctx, pool, otherSlug, otherActor, false)
	if err != nil {
		t.Fatalf("cross-ws list: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("cross-ws list = %+v, want empty", list)
	}

	// Unknown and malformed ids.
	if _, err := GetInitiative(ctx, pool, slug, actor, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, ErrInitiativeNotFound) {
		t.Fatalf("unknown id err = %v, want ErrInitiativeNotFound", err)
	}
	if _, err := GetInitiative(ctx, pool, slug, actor, "not-a-uuid"); !errors.Is(err, ErrInvalidInitiativeID) {
		t.Fatalf("malformed id err = %v, want ErrInvalidInitiativeID", err)
	}
	if _, err := UpdateInitiative(ctx, pool, slug, actor, "00000000-0000-0000-0000-000000000000", InitiativePatch{Name: strptr("x")}); !errors.Is(err, ErrInitiativeNotFound) {
		t.Fatalf("update unknown err = %v, want ErrInitiativeNotFound", err)
	}
}

func TestInitiativeArchivedFiltering(t *testing.T) {
	ctx, pool, slug, actor := setupInitiativeTest(t)

	a, err := CreateInitiative(ctx, pool, slug, actor, InitiativeInput{Name: "Active"})
	if err != nil {
		t.Fatalf("create a: %v", err)
	}
	b, err := CreateInitiative(ctx, pool, slug, actor, InitiativeInput{Name: "Old", Status: "archived"})
	if err != nil {
		t.Fatalf("create b: %v", err)
	}

	list, err := ListInitiatives(ctx, pool, slug, actor, false)
	if err != nil {
		t.Fatalf("ListInitiatives: %v", err)
	}
	if len(list) != 1 || list[0].ID != a.ID {
		t.Fatalf("default list = %+v, want only active", list)
	}

	list, err = ListInitiatives(ctx, pool, slug, actor, true)
	if err != nil {
		t.Fatalf("ListInitiatives include: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("include_archived list = %d, want 2", len(list))
	}
	_ = b

	// Archiving via PATCH also drops it from the default list.
	if _, err := UpdateInitiative(ctx, pool, slug, actor, a.ID, InitiativePatch{Status: strptr("archived")}); err != nil {
		t.Fatalf("archive: %v", err)
	}
	list, err = ListInitiatives(ctx, pool, slug, actor, false)
	if err != nil {
		t.Fatalf("ListInitiatives after archive: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("default list = %d after archive, want 0", len(list))
	}
}

func TestInitiativeProjectMembership(t *testing.T) {
	ctx, pool, slug, actor := setupInitiativeTest(t)

	identA := uniqueTestIdentifier()
	createTestProject(t, pool, slug, actor, "Proj A", identA)
	identB := uniqueTestIdentifier()
	createTestProject(t, pool, slug, actor, "Proj B", identB)

	otherSlug := uniqueTestSlug("init-ws-c")
	otherActor := createTestUser(t, pool, uniqueTestEmail("init-c"))
	createTestWorkspace(t, pool, "Other", otherSlug, otherActor)
	foreignIdent := uniqueTestIdentifier()
	createTestProject(t, pool, otherSlug, otherActor, "Foreign", foreignIdent)

	init, err := CreateInitiative(ctx, pool, slug, actor, InitiativeInput{Name: "Prog"})
	if err != nil {
		t.Fatalf("CreateInitiative: %v", err)
	}

	projectIDA := projectIDByIdent(t, pool, slug, identA)
	projectIDB := projectIDByIdent(t, pool, slug, identB)

	if err := AddInitiativeProject(ctx, pool, slug, actor, init.ID, projectIDA); err != nil {
		t.Fatalf("add A: %v", err)
	}
	// Idempotent: repeat add is a no-op.
	if err := AddInitiativeProject(ctx, pool, slug, actor, init.ID, projectIDA); err != nil {
		t.Fatalf("repeat add A: %v", err)
	}
	if err := AddInitiativeProject(ctx, pool, slug, actor, init.ID, projectIDB); err != nil {
		t.Fatalf("add B: %v", err)
	}

	got, err := GetInitiative(ctx, pool, slug, actor, init.ID)
	if err != nil {
		t.Fatalf("GetInitiative: %v", err)
	}
	if got.ProjectCount != 2 {
		t.Fatalf("project_count = %d, want 2", got.ProjectCount)
	}

	members, err := ListInitiativeProjects(ctx, pool, slug, actor, init.ID)
	if err != nil {
		t.Fatalf("ListInitiativeProjects: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("members = %+v", members)
	}

	// Malformed project id → 400-class; unknown → project-not-found;
	// foreign-workspace project → project-not-found.
	if err := AddInitiativeProject(ctx, pool, slug, actor, init.ID, "nope"); !errors.Is(err, ErrInvalidInitiativeID) {
		t.Fatalf("malformed project err = %v, want ErrInvalidInitiativeID", err)
	}
	if err := AddInitiativeProject(ctx, pool, slug, actor, init.ID, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("unknown project err = %v, want ErrProjectNotFound", err)
	}
	foreignID := projectIDByIdent(t, pool, otherSlug, foreignIdent)
	if err := AddInitiativeProject(ctx, pool, slug, actor, init.ID, foreignID); !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("foreign project err = %v, want ErrProjectNotFound", err)
	}

	// Remove is idempotent.
	if err := RemoveInitiativeProject(ctx, pool, slug, actor, init.ID, projectIDA); err != nil {
		t.Fatalf("remove A: %v", err)
	}
	if err := RemoveInitiativeProject(ctx, pool, slug, actor, init.ID, projectIDA); err != nil {
		t.Fatalf("repeat remove A: %v", err)
	}
	got, err = GetInitiative(ctx, pool, slug, actor, init.ID)
	if err != nil {
		t.Fatalf("GetInitiative: %v", err)
	}
	if got.ProjectCount != 1 {
		t.Fatalf("project_count = %d, want 1", got.ProjectCount)
	}

	// Delete cascades membership rows; projects themselves are untouched.
	if err := DeleteInitiative(ctx, pool, slug, actor, init.ID); err != nil {
		t.Fatalf("DeleteInitiative: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM initiative_projects WHERE initiative_id = $1::uuid`, init.ID).Scan(&n); err != nil {
		t.Fatalf("count membership: %v", err)
	}
	if n != 0 {
		t.Fatalf("membership rows after delete = %d, want 0", n)
	}
	var projectExists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM projects WHERE id = $1::uuid)`, projectIDB).Scan(&projectExists); err != nil {
		t.Fatalf("project exists: %v", err)
	}
	if !projectExists {
		t.Fatal("initiative delete removed a project row")
	}
}

func TestInitiativeRollup(t *testing.T) {
	ctx, pool, slug, actor := setupInitiativeTest(t)

	identA := uniqueTestIdentifier()
	createTestProject(t, pool, slug, actor, "Alpha", identA)
	identB := uniqueTestIdentifier()
	createTestProject(t, pool, slug, actor, "Beta", identB) // zero issues

	// States with distinct groups in project Alpha.
	started, err := CreateState(ctx, pool, slug, identA, actor, StateInput{Name: "Doing", Group: "started", Color: "blue"})
	if err != nil {
		t.Fatalf("CreateState started: %v", err)
	}
	done, err := CreateState(ctx, pool, slug, identA, actor, StateInput{Name: "Finished", Group: "completed", Color: "green"})
	if err != nil {
		t.Fatalf("CreateState done: %v", err)
	}
	cancelled, err := CreateState(ctx, pool, slug, identA, actor, StateInput{Name: "Scrapped", Group: "cancelled", Color: "red"})
	if err != nil {
		t.Fatalf("CreateState cancelled: %v", err)
	}

	backlogID := backlogStateID(t, pool, slug, identA, actor)

	// Alpha: 1 backlog, 1 started, 1 completed, 1 cancelled.
	if _, err := CreateIssue(ctx, pool, slug, identA, actor, CreateIssueInput{Name: "a1", StateID: &backlogID}); err != nil {
		t.Fatalf("create a1: %v", err)
	}
	if _, err := CreateIssue(ctx, pool, slug, identA, actor, CreateIssueInput{Name: "a2", StateID: &started.ID}); err != nil {
		t.Fatalf("create a2: %v", err)
	}
	if _, err := CreateIssue(ctx, pool, slug, identA, actor, CreateIssueInput{Name: "a3", StateID: &done.ID}); err != nil {
		t.Fatalf("create a3: %v", err)
	}
	if _, err := CreateIssue(ctx, pool, slug, identA, actor, CreateIssueInput{Name: "a4", StateID: &cancelled.ID}); err != nil {
		t.Fatalf("create a4: %v", err)
	}

	init, err := CreateInitiative(ctx, pool, slug, actor, InitiativeInput{Name: "Roll"})
	if err != nil {
		t.Fatalf("CreateInitiative: %v", err)
	}
	if err := AddInitiativeProject(ctx, pool, slug, actor, init.ID, projectIDByIdent(t, pool, slug, identA)); err != nil {
		t.Fatalf("add alpha: %v", err)
	}
	if err := AddInitiativeProject(ctx, pool, slug, actor, init.ID, projectIDByIdent(t, pool, slug, identB)); err != nil {
		t.Fatalf("add beta: %v", err)
	}

	roll, err := GetInitiativeRollup(ctx, pool, slug, actor, init.ID)
	if err != nil {
		t.Fatalf("GetInitiativeRollup: %v", err)
	}
	if roll.Total != 4 {
		t.Fatalf("total = %d, want 4", roll.Total)
	}
	if roll.ByGroup["backlog"] != 1 || roll.ByGroup["started"] != 1 ||
		roll.ByGroup["completed"] != 1 || roll.ByGroup["cancelled"] != 1 {
		t.Fatalf("by_group = %+v", roll.ByGroup)
	}
	// done groups = completed + cancelled → 2 of 4 = 50%.
	if roll.DoneCount != 2 {
		t.Fatalf("done = %d, want 2", roll.DoneCount)
	}
	if roll.CompletionPct != 50 {
		t.Fatalf("completion_pct = %v, want 50", roll.CompletionPct)
	}

	if len(roll.Projects) != 2 {
		t.Fatalf("projects = %d, want 2", len(roll.Projects))
	}
	var alpha, beta *InitiativeProjectRollup
	for i := range roll.Projects {
		switch roll.Projects[i].ProjectName {
		case "Alpha":
			alpha = &roll.Projects[i]
		case "Beta":
			beta = &roll.Projects[i]
		}
	}
	if alpha == nil || beta == nil {
		t.Fatalf("projects = %+v", roll.Projects)
	}
	if alpha.Total != 4 || alpha.DoneCount != 2 || alpha.CompletionPct != 50 {
		t.Fatalf("alpha = %+v", alpha)
	}
	// Zero-issue project appears with zero counts.
	if beta.Total != 0 || beta.DoneCount != 0 || beta.CompletionPct != 0 {
		t.Fatalf("beta = %+v, want all zeros", beta)
	}
	if len(beta.ByGroup) != 0 {
		t.Fatalf("beta by_group = %+v, want empty", beta.ByGroup)
	}

	// Read gate: guest may read the rollup.
	guest := setupInitiativeGuest(t, pool, slug, actor)
	if _, err := GetInitiativeRollup(ctx, pool, slug, guest, init.ID); err != nil {
		t.Fatalf("guest rollup: %v", err)
	}

	// Tenancy: other workspace gets 404.
	otherActor := createTestUser(t, pool, uniqueTestEmail("init-r"))
	otherSlug := uniqueTestSlug("init-ws-r")
	createTestWorkspace(t, pool, "R Co", otherSlug, otherActor)
	if _, err := GetInitiativeRollup(ctx, pool, otherSlug, otherActor, init.ID); !errors.Is(err, ErrInitiativeNotFound) {
		t.Fatalf("cross-workspace rollup err = %v, want ErrInitiativeNotFound", err)
	}
}

// projectIDByIdent resolves the project id for tests that need the raw
// UUID (the service never exposes one from the identifier directly).
func projectIDByIdent(t *testing.T, pool *pgxpool.Pool, wsSlug, ident string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(),
		`SELECT p.id::text FROM projects p
		  JOIN workspaces w ON w.id = p.workspace_id
		 WHERE w.slug = $1 AND p.identifier = $2`, wsSlug, ident).Scan(&id)
	if err != nil {
		t.Fatalf("projectIDByIdent: %v", err)
	}
	return id
}
