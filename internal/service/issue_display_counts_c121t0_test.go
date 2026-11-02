package service

// C121T0: batched per-row display counts on list rows —
// attachment_count, link_count, relation_count. ONE query per count
// source for the whole page, never per-row. Real test database, no
// skips. Counts mirror the detail endpoints' lists: links and
// relations count EITHER side, but only when the other endpoint is
// live and in the same project.

import (
	"context"
	"testing"
)

// setupDisplayCountsFixture builds: rich (2 attachments, 1 link,
// 1 relation in each direction), bare (nothing), plus link/relation
// rows pointing at a soft-deleted issue and a cross-project issue —
// both must NOT count.
func setupDisplayCountsFixture(t *testing.T) (listTestSetup, map[string]*Issue) {
	t.Helper()
	s := setupListTest(t)
	ctx := context.Background()

	rich := createTestIssue(t, s.pool, s.slug, s.ident, s.actor, "rich")
	bare := createTestIssue(t, s.pool, s.slug, s.ident, s.actor, "bare")
	other := createTestIssue(t, s.pool, s.slug, s.ident, s.actor, "other")
	third := createTestIssue(t, s.pool, s.slug, s.ident, s.actor, "third")

	// 2 attachments on rich (direct SQL: upload needs object storage).
	for _, name := range []string{"a.png", "b.png"} {
		if _, err := s.pool.Exec(ctx,
			`INSERT INTO attachments (issue_id, filename, content_type, size_bytes, stored_path, uploaded_by)
			 VALUES ($1::uuid, $2, 'image/png', 10, $3, $4::uuid)`,
			rich.ID, name, "test/"+name, s.actor); err != nil {
			t.Fatalf("insert attachment %s: %v", name, err)
		}
	}
	// 1 link: rich blocks other (outgoing from rich, incoming to other).
	if _, err := CreateIssueLink(ctx, s.pool, s.slug, s.ident, rich.ID, s.actor, other.ID, "blocks"); err != nil {
		t.Fatalf("CreateIssueLink rich->other: %v", err)
	}
	// 1 relation in each direction for rich: rich blocked_by other
	// (outgoing), third blocked_by rich (incoming to rich).
	if err := CreateRelation(ctx, s.pool, s.slug, s.ident, rich.ID, s.actor, other.ID, "blocked_by"); err != nil {
		t.Fatalf("CreateRelation rich blocked_by other: %v", err)
	}
	if err := CreateRelation(ctx, s.pool, s.slug, s.ident, third.ID, s.actor, rich.ID, "blocked_by"); err != nil {
		t.Fatalf("CreateRelation third blocked_by rich: %v", err)
	}

	// Soft-deleted issue + cross-project issue: edges to them are
	// inserted via SQL (the service creators rightly refuse dead /
	// foreign targets) and must NOT count.
	dead := createTestIssue(t, s.pool, s.slug, s.ident, s.actor, "dead")
	if _, err := s.pool.Exec(ctx,
		`UPDATE issues SET deleted_at = now() WHERE id = $1::uuid`, dead.ID); err != nil {
		t.Fatalf("soft-delete dead: %v", err)
	}
	foreignIdent := uniqueTestIdentifier()
	createTestProject(t, s.pool, s.slug, s.actor, "Foreign", foreignIdent)
	foreign := createTestIssue(t, s.pool, s.slug, foreignIdent, s.actor, "foreign")
	for _, target := range []string{dead.ID, foreign.ID} {
		if _, err := s.pool.Exec(ctx,
			`INSERT INTO issue_links (issue_id, target_issue_id, kind)
			 VALUES ($1::uuid, $2::uuid, 'blocks')`, rich.ID, target); err != nil {
			t.Fatalf("insert dead/foreign link: %v", err)
		}
		if _, err := s.pool.Exec(ctx,
			`INSERT INTO issue_relations (issue_id, related_issue_id, type, created_by)
			 VALUES ($1::uuid, $2::uuid, 'blocked_by', $3::uuid)`, rich.ID, target, s.actor); err != nil {
			t.Fatalf("insert dead/foreign relation: %v", err)
		}
	}

	return s, map[string]*Issue{
		"rich": rich, "bare": bare, "other": other, "third": third,
		"dead": dead, "foreign": foreign,
	}
}

func assertDisplayCounts(t *testing.T, label string, item IssueListItem, att, link, rel int) {
	t.Helper()
	if item.AttachmentCount != att || item.LinkCount != link || item.RelationCount != rel {
		t.Fatalf("%s: %s counts = (%d, %d, %d), want (%d, %d, %d)",
			label, item.Name, item.AttachmentCount, item.LinkCount, item.RelationCount, att, link, rel)
	}
}

func findListItem(items []IssueListItem, id string) (IssueListItem, bool) {
	for _, it := range items {
		if it.ID == id {
			return it, true
		}
	}
	return IssueListItem{}, false
}

func TestListIssuesDisplayCounts(t *testing.T) {
	s, iss := setupDisplayCountsFixture(t)
	ctx := context.Background()

	res, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor, ListIssuesInput{})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	// Dead is soft-deleted (out of the list); foreign lives in
	// another project (out of the list).
	rich, ok := findListItem(res.Issues, iss["rich"].ID)
	if !ok {
		t.Fatalf("rich missing from list")
	}
	assertDisplayCounts(t, "list rich", rich, 2, 1, 2)
	bare, ok := findListItem(res.Issues, iss["bare"].ID)
	if !ok {
		t.Fatalf("bare missing from list")
	}
	assertDisplayCounts(t, "list bare", bare, 0, 0, 0)
	other, ok := findListItem(res.Issues, iss["other"].ID)
	if !ok {
		t.Fatalf("other missing from list")
	}
	// Incoming link + incoming relation (rich blocked_by other reads
	// as "blocking" from other's side, still one relation row).
	assertDisplayCounts(t, "list other", other, 0, 1, 1)
	third, ok := findListItem(res.Issues, iss["third"].ID)
	if !ok {
		t.Fatalf("third missing from list")
	}
	assertDisplayCounts(t, "list third", third, 0, 0, 1)
}

func TestListIssuesDisplayCountsPagination(t *testing.T) {
	s, iss := setupDisplayCountsFixture(t)
	ctx := context.Background()

	// Page through the whole list at 2 rows per page: every page's
	// rows carry their own counts, including the rich row wherever
	// it lands and the bare row's zeros.
	seen := map[string]IssueListItem{}
	cursor := ""
	for {
		res, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor,
			ListIssuesInput{PerPage: 2, Cursor: cursor})
		if err != nil {
			t.Fatalf("ListIssues page: %v", err)
		}
		for _, it := range res.Issues {
			seen[it.ID] = it
		}
		if res.NextCursor == "" {
			break
		}
		cursor = res.NextCursor
	}
	if len(seen) != 4 {
		t.Fatalf("paged list: got %d issues, want 4 (rich, bare, other, third)", len(seen))
	}
	assertDisplayCounts(t, "paged rich", seen[iss["rich"].ID], 2, 1, 2)
	assertDisplayCounts(t, "paged bare", seen[iss["bare"].ID], 0, 0, 0)
	assertDisplayCounts(t, "paged other", seen[iss["other"].ID], 0, 1, 1)
	assertDisplayCounts(t, "paged third", seen[iss["third"].ID], 0, 0, 1)
}

func TestListIssuesDisplayCountsWithModuleScope(t *testing.T) {
	s, iss := setupDisplayCountsFixture(t)
	ctx := context.Background()

	// Detail-page scope (C120 precedent): counts AND with the module
	// scope container — the scoped row keeps its counts, and rows
	// outside the scope stay out.
	mod, err := CreateModule(ctx, s.pool, s.slug, s.ident, s.actor, ModuleInput{Name: "Scope Mod"})
	if err != nil {
		t.Fatalf("CreateModule: %v", err)
	}
	if err := AddModuleIssues(ctx, s.pool, s.slug, s.ident, s.actor, mod.ID, []string{iss["rich"].ID}); err != nil {
		t.Fatalf("AddModuleIssues: %v", err)
	}
	res, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor, ListIssuesInput{Module: mod.ID})
	if err != nil {
		t.Fatalf("ListIssues scoped: %v", err)
	}
	if len(res.Issues) != 1 || res.Issues[0].ID != iss["rich"].ID {
		t.Fatalf("scoped list: got %d issues, want exactly rich", len(res.Issues))
	}
	assertDisplayCounts(t, "scoped rich", res.Issues[0], 2, 1, 2)
}

func TestGetIssueDisplayCounts(t *testing.T) {
	s, iss := setupDisplayCountsFixture(t)
	ctx := context.Background()

	// The detail response shares the list serializer: it carries the
	// same real counts, not a silent zero.
	item, err := GetIssue(ctx, s.pool, s.slug, s.ident, iss["rich"].ID, s.actor)
	if err != nil {
		t.Fatalf("GetIssue rich: %v", err)
	}
	assertDisplayCounts(t, "detail rich", *item, 2, 1, 2)

	bare, err := GetIssue(ctx, s.pool, s.slug, s.ident, iss["bare"].ID, s.actor)
	if err != nil {
		t.Fatalf("GetIssue bare: %v", err)
	}
	assertDisplayCounts(t, "detail bare", *bare, 0, 0, 0)
}

func TestListDeletedIssuesDisplayCounts(t *testing.T) {
	s, iss := setupDisplayCountsFixture(t)
	ctx := context.Background()

	// Soft-delete the rich issue: the trash row still carries its
	// counts (attachments survive; links/relations to live issues
	// still count).
	if err := DeleteIssue(ctx, s.pool, s.slug, s.ident, iss["rich"].ID, s.actor); err != nil {
		t.Fatalf("DeleteIssue rich: %v", err)
	}
	res, err := ListDeletedIssues(ctx, s.pool, s.slug, s.ident, s.actor, "", 25)
	if err != nil {
		t.Fatalf("ListDeletedIssues: %v", err)
	}
	rich, ok := findListItem(res.Issues, iss["rich"].ID)
	if !ok {
		t.Fatalf("rich missing from trash list")
	}
	assertDisplayCounts(t, "trash rich", rich, 2, 1, 2)
}

func TestListModuleIssuesDisplayCounts(t *testing.T) {
	s, iss := setupDisplayCountsFixture(t)
	ctx := context.Background()

	mod, err := CreateModule(ctx, s.pool, s.slug, s.ident, s.actor, ModuleInput{Name: "Count Mod"})
	if err != nil {
		t.Fatalf("CreateModule: %v", err)
	}
	if err := AddModuleIssues(ctx, s.pool, s.slug, s.ident, s.actor, mod.ID,
		[]string{iss["rich"].ID, iss["bare"].ID}); err != nil {
		t.Fatalf("AddModuleIssues: %v", err)
	}
	items, err := ListModuleIssues(ctx, s.pool, s.slug, s.ident, s.actor, mod.ID)
	if err != nil {
		t.Fatalf("ListModuleIssues: %v", err)
	}
	rich, ok := findListItem(items, iss["rich"].ID)
	if !ok {
		t.Fatalf("rich missing from module list")
	}
	assertDisplayCounts(t, "module rich", rich, 2, 1, 2)
	bare, ok := findListItem(items, iss["bare"].ID)
	if !ok {
		t.Fatalf("bare missing from module list")
	}
	assertDisplayCounts(t, "module bare", bare, 0, 0, 0)
}
