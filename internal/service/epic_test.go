package service

// Epics (C71T0): project-scoped CRUD + archive, bulk issue membership via
// the epic_issues junction, the delete guard, state-group progress on
// detail, initiative links, and activity rows (epic_added/epic_removed).

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func setupEpicTest(t *testing.T) (context.Context, *pgxpool.Pool, string, string, string) {
	t.Helper()
	ctx := context.Background()
	pool := newTestPool(t)
	actor := createTestUser(t, pool, uniqueTestEmail("epic"))
	slug := uniqueTestSlug("epic-ws")
	createTestWorkspace(t, pool, "Epic Co", slug, actor)
	ident := uniqueTestIdentifier()
	createTestProject(t, pool, slug, actor, "Eng", ident)
	return ctx, pool, slug, ident, actor
}

// createTestEpicIssue creates an issue directly in a state of the given
// group (avoids the C68 transition allowlist that UpdateIssue enforces).
func createTestEpicIssue(t *testing.T, pool *pgxpool.Pool, slug, ident, actor, name, group string) *Issue {
	t.Helper()
	var projectID string
	if err := pool.QueryRow(context.Background(),
		`SELECT p.id::text FROM projects p JOIN workspaces w ON w.id = p.workspace_id
		  WHERE w.slug = $1 AND p.identifier = $2`, slug, ident).Scan(&projectID); err != nil {
		t.Fatalf("project lookup: %v", err)
	}
	var stateID string
	if err := pool.QueryRow(context.Background(),
		`SELECT id::text FROM states WHERE project_id = $1::uuid AND "group" = $2
		  ORDER BY sequence LIMIT 1`, projectID, group).Scan(&stateID); err != nil {
		t.Fatalf("state lookup group=%q: %v", group, err)
	}
	iss, err := CreateIssue(context.Background(), pool, slug, ident, actor,
		CreateIssueInput{Name: name, StateID: &stateID})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	return iss
}

func TestEpicCRUD(t *testing.T) {
	ctx, pool, slug, ident, actor := setupEpicTest(t)

	desc := "The long-running container"
	e, err := CreateEpic(ctx, pool, slug, ident, actor, EpicInput{
		Name:        "Platform",
		Description: &desc,
	})
	if err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	if e.Name != "Platform" || e.ProjectID == "" {
		t.Fatalf("got %+v", e)
	}
	if e.Description == nil || *e.Description != desc {
		t.Fatalf("description = %v", e.Description)
	}
	if e.ArchivedAt != nil {
		t.Fatalf("archived_at = %v, want nil", e.ArchivedAt)
	}
	if e.CreatedBy == nil || *e.CreatedBy != actor {
		t.Fatalf("created_by = %v, want actor", e.CreatedBy)
	}
	if e.IssueCount != 0 {
		t.Fatalf("issue_count = %d, want 0", e.IssueCount)
	}
	// Create does not recount (module convention): progress arrives on
	// the GetEpic detail read below.

	got, err := GetEpic(ctx, pool, slug, ident, actor, e.ID)
	if err != nil {
		t.Fatalf("GetEpic: %v", err)
	}
	if got.ID != e.ID || got.IssueCount != 0 || got.Progress == nil {
		t.Fatalf("got %+v", got)
	}

	list, err := ListEpics(ctx, pool, slug, ident, actor, false)
	if err != nil {
		t.Fatalf("ListEpics: %v", err)
	}
	if len(list) != 1 || list[0].ID != e.ID {
		t.Fatalf("list = %+v", list)
	}

	// Duplicate name in the project conflicts.
	if _, err := CreateEpic(ctx, pool, slug, ident, actor, EpicInput{Name: "Platform"}); !errors.Is(err, ErrEpicConflict) {
		t.Fatalf("dup name: err = %v, want ErrEpicConflict", err)
	}
	// Empty name is rejected.
	if _, err := CreateEpic(ctx, pool, slug, ident, actor, EpicInput{Name: "  "}); !errors.Is(err, ErrNameRequired) {
		t.Fatalf("empty name: err = %v, want ErrNameRequired", err)
	}

	// Partial update: rename only.
	newName := "Platform v2"
	upd, err := UpdateEpic(ctx, pool, slug, ident, actor, e.ID, EpicPatch{Name: &newName})
	if err != nil {
		t.Fatalf("UpdateEpic: %v", err)
	}
	if upd.Name != newName {
		t.Fatalf("updated = %+v", upd)
	}
	if upd.Description == nil || *upd.Description != desc {
		t.Fatalf("description should be untouched: %+v", upd)
	}

	// "" clears description (NULL).
	empty := ""
	cleared, err := UpdateEpic(ctx, pool, slug, ident, actor, e.ID, EpicPatch{Description: &empty})
	if err != nil {
		t.Fatalf("clear patch: %v", err)
	}
	if cleared.Description != nil {
		t.Fatalf("description should be cleared: %+v", cleared)
	}
	// Empty patch is rejected.
	if _, err := UpdateEpic(ctx, pool, slug, ident, actor, e.ID, EpicPatch{}); !errors.Is(err, ErrNothingToUpdate) {
		t.Fatalf("empty patch: err = %v, want ErrNothingToUpdate", err)
	}

	// Unknown epic is 404; malformed id is 400.
	if _, err := GetEpic(ctx, pool, slug, ident, actor, "00000000-0000-4000-8000-000000000000"); !errors.Is(err, ErrEpicNotFound) {
		t.Fatalf("get missing: err = %v, want ErrEpicNotFound", err)
	}
	if _, err := GetEpic(ctx, pool, slug, ident, actor, "not-a-uuid"); !errors.Is(err, ErrInvalidEpicID) {
		t.Fatalf("get malformed: err = %v, want ErrInvalidEpicID", err)
	}
}

func TestEpicArchive(t *testing.T) {
	ctx, pool, slug, ident, actor := setupEpicTest(t)

	e, err := CreateEpic(ctx, pool, slug, ident, actor, EpicInput{Name: "Old"})
	if err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	i1 := createTestIssue(t, pool, slug, ident, actor, "member issue")

	if err := AddEpicIssues(ctx, pool, slug, ident, actor, e.ID, []string{i1.ID}); err != nil {
		t.Fatalf("AddEpicIssues: %v", err)
	}

	arch, err := ArchiveEpic(ctx, pool, slug, ident, actor, e.ID)
	if err != nil {
		t.Fatalf("ArchiveEpic: %v", err)
	}
	if arch.ArchivedAt == nil {
		t.Fatalf("archived_at = nil, want set")
	}

	// Archived hidden from the default list.
	list, err := ListEpics(ctx, pool, slug, ident, actor, false)
	if err != nil {
		t.Fatalf("ListEpics: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("list = %+v, want empty", list)
	}
	// ?include_archived=true shows it.
	all, err := ListEpics(ctx, pool, slug, ident, actor, true)
	if err != nil {
		t.Fatalf("ListEpics incl archived: %v", err)
	}
	if len(all) != 1 || all[0].ArchivedAt == nil {
		t.Fatalf("list incl = %+v", all)
	}
	// Detail still works when archived.
	got, err := GetEpic(ctx, pool, slug, ident, actor, e.ID)
	if err != nil {
		t.Fatalf("GetEpic archived: %v", err)
	}
	if got.ArchivedAt == nil {
		t.Fatalf("detail archived_at = nil")
	}
	// Archive wrote an activity row on the member issue.
	if n := countActivities(t, pool, i1.ID, "epic_archived"); n != 1 {
		t.Fatalf("epic_archived rows = %d, want 1", n)
	}

	unarch, err := UnarchiveEpic(ctx, pool, slug, ident, actor, e.ID)
	if err != nil {
		t.Fatalf("UnarchiveEpic: %v", err)
	}
	if unarch.ArchivedAt != nil {
		t.Fatalf("archived_at = %v, want nil after unarchive", unarch.ArchivedAt)
	}
	list, err = ListEpics(ctx, pool, slug, ident, actor, false)
	if err != nil {
		t.Fatalf("ListEpics after unarchive: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("list = %+v, want the epic back", list)
	}
	if n := countActivities(t, pool, i1.ID, "epic_unarchived"); n != 1 {
		t.Fatalf("epic_unarchived rows = %d, want 1", n)
	}

	// Archiving an unknown epic is 404.
	if _, err := ArchiveEpic(ctx, pool, slug, ident, actor, "00000000-0000-4000-8000-000000000000"); !errors.Is(err, ErrEpicNotFound) {
		t.Fatalf("archive missing: err = %v, want ErrEpicNotFound", err)
	}
}

func TestEpicIssues(t *testing.T) {
	ctx, pool, slug, ident, actor := setupEpicTest(t)

	e, err := CreateEpic(ctx, pool, slug, ident, actor, EpicInput{Name: "WithIssues"})
	if err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	i1 := createTestIssue(t, pool, slug, ident, actor, "issue one")
	i2 := createTestIssue(t, pool, slug, ident, actor, "issue two")

	if err := AddEpicIssues(ctx, pool, slug, ident, actor, e.ID, []string{i1.ID, i2.ID}); err != nil {
		t.Fatalf("AddEpicIssues: %v", err)
	}
	// Idempotent: re-adding is a no-op, never a 409.
	if err := AddEpicIssues(ctx, pool, slug, ident, actor, e.ID, []string{i1.ID}); err != nil {
		t.Fatalf("re-add: %v", err)
	}

	items, err := ListEpicIssues(ctx, pool, slug, ident, actor, e.ID)
	if err != nil {
		t.Fatalf("ListEpicIssues: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("len = %d, want 2", len(items))
	}
	for _, it := range items {
		if it.DisplayID == "" {
			t.Fatalf("missing display_id: %+v", it)
		}
	}

	got, err := GetEpic(ctx, pool, slug, ident, actor, e.ID)
	if err != nil {
		t.Fatalf("GetEpic: %v", err)
	}
	if got.IssueCount != 2 {
		t.Fatalf("issue_count = %d, want 2", got.IssueCount)
	}

	// add wrote exactly one epic_added row per issue (no dup on re-add).
	if n := countActivities(t, pool, i1.ID, "epic_added"); n != 1 {
		t.Fatalf("epic_added rows for i1 = %d, want 1", n)
	}
	if n := countActivities(t, pool, i2.ID, "epic_added"); n != 1 {
		t.Fatalf("epic_added rows for i2 = %d, want 1", n)
	}

	// Remove one; removing a non-member is a silent no-op.
	if err := RemoveEpicIssues(ctx, pool, slug, ident, actor, e.ID, []string{i1.ID}); err != nil {
		t.Fatalf("RemoveEpicIssues: %v", err)
	}
	if err := RemoveEpicIssues(ctx, pool, slug, ident, actor, e.ID, []string{i1.ID}); err != nil {
		t.Fatalf("re-remove: %v", err)
	}
	items, err = ListEpicIssues(ctx, pool, slug, ident, actor, e.ID)
	if err != nil {
		t.Fatalf("ListEpicIssues: %v", err)
	}
	if len(items) != 1 || items[0].ID != i2.ID {
		t.Fatalf("after remove: %+v", items)
	}
	if n := countActivities(t, pool, i1.ID, "epic_removed"); n != 1 {
		t.Fatalf("epic_removed rows for i1 = %d, want 1", n)
	}
	// i2 was never removed: no epic_removed row.
	if n := countActivities(t, pool, i2.ID, "epic_removed"); n != 0 {
		t.Fatalf("epic_removed rows for i2 = %d, want 0", n)
	}

	// Foreign issue (other project) is 404 — per-project scoping.
	otherSlug := uniqueTestSlug("epic-ws2")
	createTestWorkspace(t, pool, "Epic Co 2", otherSlug, actor)
	otherIdent := uniqueTestIdentifier()
	createTestProject(t, pool, otherSlug, actor, "Eng2", otherIdent)
	foreign := createTestIssue(t, pool, otherSlug, otherIdent, actor, "foreign")
	if err := AddEpicIssues(ctx, pool, slug, ident, actor, e.ID, []string{foreign.ID}); !errors.Is(err, ErrIssueNotFound) {
		t.Fatalf("foreign issue: err = %v, want ErrIssueNotFound", err)
	}
	// Deleted issues cannot attach.
	del := createTestIssue(t, pool, slug, ident, actor, "doomed")
	if err := DeleteIssue(ctx, pool, slug, ident, del.ID, actor); err != nil {
		t.Fatalf("DeleteIssue: %v", err)
	}
	if err := AddEpicIssues(ctx, pool, slug, ident, actor, e.ID, []string{del.ID}); !errors.Is(err, ErrIssueNotFound) {
		t.Fatalf("deleted issue: err = %v, want ErrIssueNotFound", err)
	}
	// Empty id list is rejected.
	if err := AddEpicIssues(ctx, pool, slug, ident, actor, e.ID, nil); !errors.Is(err, ErrBulkEmptyIDs) {
		t.Fatalf("empty add: err = %v, want ErrBulkEmptyIDs", err)
	}
	if err := RemoveEpicIssues(ctx, pool, slug, ident, actor, e.ID, nil); !errors.Is(err, ErrBulkEmptyIDs) {
		t.Fatalf("empty remove: err = %v, want ErrBulkEmptyIDs", err)
	}
	// Malformed ids are 400.
	if err := AddEpicIssues(ctx, pool, slug, ident, actor, e.ID, []string{"not-a-uuid"}); !errors.Is(err, ErrInvalidEpicID) {
		t.Fatalf("bad issue id: err = %v, want ErrInvalidEpicID", err)
	}
	if err := RemoveEpicIssues(ctx, pool, slug, ident, actor, e.ID, []string{"not-a-uuid"}); !errors.Is(err, ErrInvalidEpicID) {
		t.Fatalf("bad remove id: err = %v, want ErrInvalidEpicID", err)
	}
}

func TestEpicDeleteGuard(t *testing.T) {
	ctx, pool, slug, ident, actor := setupEpicTest(t)

	e, err := CreateEpic(ctx, pool, slug, ident, actor, EpicInput{Name: "Doomed"})
	if err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	i1 := createTestIssue(t, pool, slug, ident, actor, "doomed issue")
	if err := AddEpicIssues(ctx, pool, slug, ident, actor, e.ID, []string{i1.ID}); err != nil {
		t.Fatalf("AddEpicIssues: %v", err)
	}

	// Without force, an epic with issues is 409.
	if err := DeleteEpic(ctx, pool, slug, ident, actor, e.ID, false); !errors.Is(err, ErrEpicHasIssues) {
		t.Fatalf("delete guarded: err = %v, want ErrEpicHasIssues", err)
	}
	// The epic is still there.
	if _, err := GetEpic(ctx, pool, slug, ident, actor, e.ID); err != nil {
		t.Fatalf("epic should survive guarded delete: %v", err)
	}

	// With force the epic goes away but the issue survives (unassigned).
	if err := DeleteEpic(ctx, pool, slug, ident, actor, e.ID, true); err != nil {
		t.Fatalf("force delete: %v", err)
	}
	if _, err := GetEpic(ctx, pool, slug, ident, actor, e.ID); !errors.Is(err, ErrEpicNotFound) {
		t.Fatalf("get deleted: err = %v, want ErrEpicNotFound", err)
	}
	if _, err := GetIssue(ctx, pool, slug, ident, i1.ID, actor); err != nil {
		t.Fatalf("issue should survive epic delete: %v", err)
	}

	// Deleting an empty epic needs no force.
	e2, err := CreateEpic(ctx, pool, slug, ident, actor, EpicInput{Name: "Empty"})
	if err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	if err := DeleteEpic(ctx, pool, slug, ident, actor, e2.ID, false); err != nil {
		t.Fatalf("delete empty: %v", err)
	}
}

func TestEpicProgress(t *testing.T) {
	ctx, pool, slug, ident, actor := setupEpicTest(t)

	e, err := CreateEpic(ctx, pool, slug, ident, actor, EpicInput{Name: "Progress"})
	if err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	// 2 backlog, 1 started, 1 completed, 1 cancelled.
	ids := []string{
		createTestEpicIssue(t, pool, slug, ident, actor, "b1", "backlog").ID,
		createTestEpicIssue(t, pool, slug, ident, actor, "b2", "backlog").ID,
		createTestEpicIssue(t, pool, slug, ident, actor, "s1", "started").ID,
		createTestEpicIssue(t, pool, slug, ident, actor, "c1", "completed").ID,
		createTestEpicIssue(t, pool, slug, ident, actor, "x1", "cancelled").ID,
	}
	if err := AddEpicIssues(ctx, pool, slug, ident, actor, e.ID, ids); err != nil {
		t.Fatalf("AddEpicIssues: %v", err)
	}

	got, err := GetEpic(ctx, pool, slug, ident, actor, e.ID)
	if err != nil {
		t.Fatalf("GetEpic: %v", err)
	}
	if got.Progress == nil {
		t.Fatalf("progress = nil, want counts")
	}
	if got.Progress.Total != 5 {
		t.Fatalf("progress.total = %d, want 5", got.Progress.Total)
	}
	want := map[string]int64{"backlog": 2, "started": 1, "completed": 1, "cancelled": 1}
	for group, n := range want {
		if got.Progress.ByGroup[group] != n {
			t.Fatalf("progress.by_group[%q] = %d, want %d (by_group = %v)",
				group, got.Progress.ByGroup[group], n, got.Progress.ByGroup)
		}
	}
	// Removed issues leave the counts.
	if err := RemoveEpicIssues(ctx, pool, slug, ident, actor, e.ID, []string{ids[0]}); err != nil {
		t.Fatalf("RemoveEpicIssues: %v", err)
	}
	got, err = GetEpic(ctx, pool, slug, ident, actor, e.ID)
	if err != nil {
		t.Fatalf("GetEpic: %v", err)
	}
	if got.Progress.Total != 4 || got.Progress.ByGroup["backlog"] != 1 {
		t.Fatalf("after remove: progress = %+v", got.Progress)
	}
}

func TestInitiativeEpics(t *testing.T) {
	ctx, pool, slug, ident, actor := setupEpicTest(t)

	init, err := CreateInitiative(ctx, pool, slug, actor, InitiativeInput{Name: "Big Bet"})
	if err != nil {
		t.Fatalf("CreateInitiative: %v", err)
	}
	e1, err := CreateEpic(ctx, pool, slug, ident, actor, EpicInput{Name: "Epic One"})
	if err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	e2, err := CreateEpic(ctx, pool, slug, ident, actor, EpicInput{Name: "Epic Two"})
	if err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}

	if err := AddInitiativeEpic(ctx, pool, slug, actor, init.ID, e1.ID); err != nil {
		t.Fatalf("AddInitiativeEpic: %v", err)
	}
	// Idempotent: re-adding is a no-op, never a 409.
	if err := AddInitiativeEpic(ctx, pool, slug, actor, init.ID, e1.ID); err != nil {
		t.Fatalf("re-add: %v", err)
	}
	if err := AddInitiativeEpic(ctx, pool, slug, actor, init.ID, e2.ID); err != nil {
		t.Fatalf("AddInitiativeEpic 2: %v", err)
	}

	linked, err := ListInitiativeEpics(ctx, pool, slug, actor, init.ID)
	if err != nil {
		t.Fatalf("ListInitiativeEpics: %v", err)
	}
	if len(linked) != 2 {
		t.Fatalf("len = %d, want 2", len(linked))
	}
	if linked[0].EpicID != e1.ID || linked[0].EpicName != "Epic One" || linked[0].ProjectID == "" {
		t.Fatalf("linked[0] = %+v", linked[0])
	}

	if err := RemoveInitiativeEpic(ctx, pool, slug, actor, init.ID, e1.ID); err != nil {
		t.Fatalf("RemoveInitiativeEpic: %v", err)
	}
	// Removing a non-linked epic is a silent no-op.
	if err := RemoveInitiativeEpic(ctx, pool, slug, actor, init.ID, e1.ID); err != nil {
		t.Fatalf("re-remove: %v", err)
	}
	linked, err = ListInitiativeEpics(ctx, pool, slug, actor, init.ID)
	if err != nil {
		t.Fatalf("ListInitiativeEpics: %v", err)
	}
	if len(linked) != 1 || linked[0].EpicID != e2.ID {
		t.Fatalf("after remove: %+v", linked)
	}
	// The epic itself survives unlinking.
	if _, err := GetEpic(ctx, pool, slug, ident, actor, e1.ID); err != nil {
		t.Fatalf("epic should survive unlink: %v", err)
	}

	// An epic from another workspace is 404 (not a member of this one).
	otherSlug := uniqueTestSlug("epic-ws2")
	createTestWorkspace(t, pool, "Epic Co 2", otherSlug, actor)
	otherIdent := uniqueTestIdentifier()
	createTestProject(t, pool, otherSlug, actor, "Eng2", otherIdent)
	foreign, err := CreateEpic(ctx, pool, otherSlug, otherIdent, actor, EpicInput{Name: "Foreign"})
	if err != nil {
		t.Fatalf("CreateEpic foreign: %v", err)
	}
	if err := AddInitiativeEpic(ctx, pool, slug, actor, init.ID, foreign.ID); !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("foreign epic: err = %v, want ErrProjectNotFound", err)
	}
	// Malformed epic id is 400.
	if err := AddInitiativeEpic(ctx, pool, slug, actor, init.ID, "not-a-uuid"); !errors.Is(err, ErrInvalidEpicID) {
		t.Fatalf("bad epic id: err = %v, want ErrInvalidEpicID", err)
	}
	// Unknown initiative is 404.
	if err := AddInitiativeEpic(ctx, pool, slug, actor, "00000000-0000-4000-8000-000000000000", e2.ID); !errors.Is(err, ErrInitiativeNotFound) {
		t.Fatalf("missing initiative: err = %v, want ErrInitiativeNotFound", err)
	}
	// Unknown epic is 404.
	if err := AddInitiativeEpic(ctx, pool, slug, actor, init.ID, "00000000-0000-4000-8000-000000000000"); !errors.Is(err, ErrEpicNotFound) {
		t.Fatalf("missing epic: err = %v, want ErrEpicNotFound", err)
	}
}

func TestIssueDetailIncludesEpics(t *testing.T) {
	ctx, pool, slug, ident, actor := setupEpicTest(t)

	e1, err := CreateEpic(ctx, pool, slug, ident, actor, EpicInput{Name: "Detail Epic"})
	if err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	e2, err := CreateEpic(ctx, pool, slug, ident, actor, EpicInput{Name: "Archived Epic"})
	if err != nil {
		t.Fatalf("CreateEpic: %v", err)
	}
	if _, err := ArchiveEpic(ctx, pool, slug, ident, actor, e2.ID); err != nil {
		t.Fatalf("ArchiveEpic: %v", err)
	}
	iss := createTestIssue(t, pool, slug, ident, actor, "with epics")
	if err := AddEpicIssues(ctx, pool, slug, ident, actor, e1.ID, []string{iss.ID}); err != nil {
		t.Fatalf("AddEpicIssues: %v", err)
	}
	if err := AddEpicIssues(ctx, pool, slug, ident, actor, e2.ID, []string{iss.ID}); err != nil {
		t.Fatalf("AddEpicIssues archived: %v", err)
	}

	detail, err := GetIssue(ctx, pool, slug, ident, iss.ID, actor)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if len(detail.Epics) != 2 {
		t.Fatalf("epics = %+v, want 2", detail.Epics)
	}
	byName := map[string]bool{}
	for _, ref := range detail.Epics {
		if ref.ID == "" || ref.Name == "" {
			t.Fatalf("epic ref incomplete: %+v", ref)
		}
		byName[ref.Name] = true
		if ref.Name == "Archived Epic" && ref.ArchivedAt == nil {
			t.Fatalf("archived epic ref must carry archived_at: %+v", ref)
		}
		if ref.Name == "Detail Epic" && ref.ArchivedAt != nil {
			t.Fatalf("live epic ref must not carry archived_at: %+v", ref)
		}
	}
	if !byName["Detail Epic"] || !byName["Archived Epic"] {
		t.Fatalf("epic refs = %+v", detail.Epics)
	}

	// An issue with no epics gets an empty (non-nil) list.
	plain := createTestIssue(t, pool, slug, ident, actor, "plain")
	pd, err := GetIssue(ctx, pool, slug, ident, plain.ID, actor)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if pd.Epics == nil || len(pd.Epics) != 0 {
		t.Fatalf("epics = %+v, want empty", pd.Epics)
	}
}
