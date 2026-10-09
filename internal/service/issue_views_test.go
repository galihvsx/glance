package service

// Saved views (C9T2): CRUD, per-user isolation, project scoping, duplicate
// name → ErrViewNameTaken, default-view exclusivity, cascade on project
// delete, and validation of name/filters. Real test database, no skips.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

type viewFixture struct {
	ctx       context.Context
	pool      *pgxpool.Pool
	projectID string
	actorA    string
	actorB    string
}

func setupViewTest(t *testing.T) *viewFixture {
	t.Helper()
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	actorA := createTestUser(t, pool, uniqueTestEmail("views"))
	actorB := createTestUser(t, pool, uniqueTestEmail("views"))
	slug := uniqueTestSlug("views-ws")
	createTestWorkspace(t, pool, "Views Co", slug, actorA)
	// actorB is a member of the same workspace too.
	addTestMember(t, pool, slug, actorA, actorB, RoleMember)
	ident := uniqueTestIdentifier()
	createTestProject(t, pool, slug, actorA, "Eng", ident)
	var projectID string
	if err := pool.QueryRow(ctx,
		`SELECT p.id::text FROM projects p
		 JOIN workspaces w ON w.id = p.workspace_id
		 WHERE w.slug = $1 AND p.identifier = $2`,
		slug, ident).Scan(&projectID); err != nil {
		t.Fatalf("project id: %v", err)
	}
	return &viewFixture{ctx: ctx, pool: pool, projectID: projectID, actorA: actorA, actorB: actorB}
}

func viewInput(name string) IssueViewInput {
	return IssueViewInput{
		Name:    name,
		Filters: json.RawMessage(`{"q":"bug","priorities":[1,3]}`),
		Display: json.RawMessage(`{"groupBy":"priority"}`),
	}
}

func mustCreateView(t *testing.T, fx *viewFixture, actor string, in IssueViewInput) *IssueView {
	t.Helper()
	v, err := CreateIssueView(fx.ctx, fx.pool, actor, fx.projectID, in)
	if err != nil {
		t.Fatalf("CreateIssueView(%q): %v", in.Name, err)
	}
	return v
}

func TestIssueViewCRUD(t *testing.T) {
	fx := setupViewTest(t)

	// Create.
	v := mustCreateView(t, fx, fx.actorA, viewInput("My bugs"))
	if v.ID == "" || v.Name != "My bugs" {
		t.Fatalf("created view = %+v, want name=My bugs with id", v)
	}
	if v.IsDefault {
		t.Fatalf("new view is_default = true, want false")
	}
	// JSONB normalizes whitespace on storage — compare semantically.
	var gotFilters map[string]any
	if err := json.Unmarshal(v.Filters, &gotFilters); err != nil {
		t.Fatalf("filters unmarshal: %v", err)
	}
	if gotFilters["q"] != "bug" {
		t.Fatalf("filters = %s, want q=bug", v.Filters)
	}
	var gotDisplay map[string]any
	if err := json.Unmarshal(v.Display, &gotDisplay); err != nil {
		t.Fatalf("display unmarshal: %v", err)
	}
	if gotDisplay["groupBy"] != "priority" {
		t.Fatalf("display = %s, want groupBy=priority", v.Display)
	}
	if v.CreatedAt.IsZero() {
		t.Fatalf("created_at is zero")
	}

	// Name is trimmed server-side.
	trimmed := mustCreateView(t, fx, fx.actorA, viewInput("  padded  "))
	if trimmed.Name != "padded" {
		t.Fatalf("name = %q, want trimmed", trimmed.Name)
	}

	// List: both present, oldest first.
	views, err := ListIssueViews(fx.ctx, fx.pool, fx.actorA, fx.projectID)
	if err != nil {
		t.Fatalf("ListIssueViews: %v", err)
	}
	if len(views) != 2 || views[0].ID != v.ID || views[1].ID != trimmed.ID {
		t.Fatalf("list = %d views, want [My bugs padded] in creation order", len(views))
	}

	// Rename via PATCH.
	newName := "Critical bugs"
	updated, err := UpdateIssueView(fx.ctx, fx.pool, fx.actorA, fx.projectID, v.ID,
		IssueViewPatch{Name: &newName})
	if err != nil {
		t.Fatalf("UpdateIssueView rename: %v", err)
	}
	if updated.Name != "Critical bugs" {
		t.Fatalf("renamed = %q, want Critical bugs", updated.Name)
	}

	// Empty PATCH is a validation error (handler turns {} into 400; the
	// service accepts nil patch as a no-op read).
	noop, err := UpdateIssueView(fx.ctx, fx.pool, fx.actorA, fx.projectID, v.ID, IssueViewPatch{})
	if err != nil {
		t.Fatalf("UpdateIssueView empty patch: %v", err)
	}
	if noop.Name != "Critical bugs" {
		t.Fatalf("empty patch changed name to %q", noop.Name)
	}

	// Delete → gone from list. Delete again → no error (idempotent).
	if err := DeleteIssueView(fx.ctx, fx.pool, fx.actorA, fx.projectID, v.ID); err != nil {
		t.Fatalf("DeleteIssueView: %v", err)
	}
	if err := DeleteIssueView(fx.ctx, fx.pool, fx.actorA, fx.projectID, v.ID); err != nil {
		t.Fatalf("DeleteIssueView again: %v", err)
	}
	views, err = ListIssueViews(fx.ctx, fx.pool, fx.actorA, fx.projectID)
	if err != nil {
		t.Fatalf("ListIssueViews after delete: %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("list after delete = %d views, want 1", len(views))
	}
}

func TestIssueViewValidation(t *testing.T) {
	fx := setupViewTest(t)

	for _, name := range []string{"", "   "} {
		if _, err := CreateIssueView(fx.ctx, fx.pool, fx.actorA, fx.projectID, viewInput(name)); !errors.Is(err, ErrInvalidViewName) {
			t.Fatalf("CreateIssueView(%q): err = %v, want ErrInvalidViewName", name, err)
		}
	}
	long := strings.Repeat("x", MaxIssueViewNameLen+1)
	if _, err := CreateIssueView(fx.ctx, fx.pool, fx.actorA, fx.projectID, viewInput(long)); !errors.Is(err, ErrInvalidViewName) {
		t.Fatalf("CreateIssueView(65 chars): err = %v, want ErrInvalidViewName", err)
	}

	for _, filters := range []json.RawMessage{
		nil,
		json.RawMessage(``),
		json.RawMessage(`[1,2]`),
		json.RawMessage(`"nope"`),
		json.RawMessage(`{bad json`),
	} {
		in := viewInput("valid name")
		in.Filters = filters
		if _, err := CreateIssueView(fx.ctx, fx.pool, fx.actorA, fx.projectID, in); !errors.Is(err, ErrInvalidViewFilters) {
			t.Fatalf("CreateIssueView(filters=%q): err = %v, want ErrInvalidViewFilters", filters, err)
		}
	}

	// NULL display is fine (spreadsheet saves filters only).
	in := viewInput("filters only")
	in.Display = nil
	v := mustCreateView(t, fx, fx.actorA, in)
	if v.Display != nil {
		t.Fatalf("display = %s, want nil", v.Display)
	}

	// Rename to blank is rejected.
	blank := "   "
	if _, err := UpdateIssueView(fx.ctx, fx.pool, fx.actorA, fx.projectID, v.ID, IssueViewPatch{Name: &blank}); !errors.Is(err, ErrInvalidViewName) {
		t.Fatalf("UpdateIssueView(blank): err = %v, want ErrInvalidViewName", err)
	}
}

func TestIssueViewDuplicateName(t *testing.T) {
	fx := setupViewTest(t)
	mustCreateView(t, fx, fx.actorA, viewInput("My bugs"))

	// Exact duplicate → ErrViewNameTaken.
	if _, err := CreateIssueView(fx.ctx, fx.pool, fx.actorA, fx.projectID, viewInput("My bugs")); !errors.Is(err, ErrViewNameTaken) {
		t.Fatalf("duplicate create: err = %v, want ErrViewNameTaken", err)
	}
	// Case-insensitive duplicate → ErrViewNameTaken (matches the
	// frontend's duplicate check).
	if _, err := CreateIssueView(fx.ctx, fx.pool, fx.actorA, fx.projectID, viewInput("my BUGS")); !errors.Is(err, ErrViewNameTaken) {
		t.Fatalf("case-insensitive duplicate: err = %v, want ErrViewNameTaken", err)
	}
	// Whitespace-padded duplicate → ErrViewNameTaken (trimmed first).
	if _, err := CreateIssueView(fx.ctx, fx.pool, fx.actorA, fx.projectID, viewInput("  My bugs ")); !errors.Is(err, ErrViewNameTaken) {
		t.Fatalf("padded duplicate: err = %v, want ErrViewNameTaken", err)
	}

	// Same name by a DIFFERENT user on the same project is fine
	// (per-user isolation).
	other := mustCreateView(t, fx, fx.actorB, viewInput("My bugs"))
	if other.Name != "My bugs" {
		t.Fatalf("other user's view name = %q", other.Name)
	}

	// Rename onto a taken name → ErrViewNameTaken.
	second := mustCreateView(t, fx, fx.actorA, viewInput("Second"))
	taken := "my bugs"
	if _, err := UpdateIssueView(fx.ctx, fx.pool, fx.actorA, fx.projectID, second.ID, IssueViewPatch{Name: &taken}); !errors.Is(err, ErrViewNameTaken) {
		t.Fatalf("rename onto taken: err = %v, want ErrViewNameTaken", err)
	}
}

func TestIssueViewUserIsolation(t *testing.T) {
	fx := setupViewTest(t)
	v := mustCreateView(t, fx, fx.actorA, viewInput("Mine"))

	// actorB's list does not include actorA's view.
	views, err := ListIssueViews(fx.ctx, fx.pool, fx.actorB, fx.projectID)
	if err != nil {
		t.Fatalf("ListIssueViews(actorB): %v", err)
	}
	if len(views) != 0 {
		t.Fatalf("actorB sees %d views, want 0", len(views))
	}

	// actorB cannot update or delete actorA's view: reads as not found /
	// no-op, never an error or a cross-user mutation.
	newName := "Hijacked"
	if _, err := UpdateIssueView(fx.ctx, fx.pool, fx.actorB, fx.projectID, v.ID, IssueViewPatch{Name: &newName}); !errors.Is(err, ErrViewNotFound) {
		t.Fatalf("cross-user update: err = %v, want ErrViewNotFound", err)
	}
	if err := DeleteIssueView(fx.ctx, fx.pool, fx.actorB, fx.projectID, v.ID); err != nil {
		t.Fatalf("cross-user delete: %v", err)
	}
	views, err = ListIssueViews(fx.ctx, fx.pool, fx.actorA, fx.projectID)
	if err != nil || len(views) != 1 || views[0].Name != "Mine" {
		t.Fatalf("actorA's view after cross-user ops: %v, err=%v", views, err)
	}
}

func TestIssueViewProjectScoping(t *testing.T) {
	fx := setupViewTest(t)

	// Unknown project → ErrViewProjectNotFound.
	if _, err := ListIssueViews(fx.ctx, fx.pool, fx.actorA, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, ErrViewProjectNotFound) {
		t.Fatalf("list unknown project: err = %v, want ErrViewProjectNotFound", err)
	}
	// Malformed project id → ErrViewProjectNotFound (never 500).
	if _, err := CreateIssueView(fx.ctx, fx.pool, fx.actorA, "not-a-uuid", viewInput("x")); !errors.Is(err, ErrViewProjectNotFound) {
		t.Fatalf("create malformed project: err = %v, want ErrViewProjectNotFound", err)
	}

	// A project in a workspace the actor is not a member of → same
	// sentinel (no existence leak).
	outsider := createTestUser(t, fx.pool, uniqueTestEmail("views-out"))
	slug := uniqueTestSlug("views-ws2")
	createTestWorkspace(t, fx.pool, "Other Co", slug, outsider)
	ident := uniqueTestIdentifier()
	createTestProject(t, fx.pool, slug, outsider, "Other", ident)
	var otherProject string
	if err := fx.pool.QueryRow(fx.ctx,
		`SELECT p.id::text FROM projects p
		 JOIN workspaces w ON w.id = p.workspace_id
		 WHERE w.slug = $1 AND p.identifier = $2`,
		slug, ident).Scan(&otherProject); err != nil {
		t.Fatalf("other project id: %v", err)
	}
	if _, err := ListIssueViews(fx.ctx, fx.pool, fx.actorA, otherProject); !errors.Is(err, ErrViewProjectNotFound) {
		t.Fatalf("list non-member project: err = %v, want ErrViewProjectNotFound", err)
	}

	// Views from a different project never leak into this project's list.
	if _, err := CreateIssueView(fx.ctx, fx.pool, outsider, otherProject, viewInput("Other view")); err != nil {
		t.Fatalf("CreateIssueView(other project): %v", err)
	}
	views, err := ListIssueViews(fx.ctx, fx.pool, fx.actorA, fx.projectID)
	if err != nil {
		t.Fatalf("ListIssueViews: %v", err)
	}
	if len(views) != 0 {
		t.Fatalf("project list leaked %d views from another project", len(views))
	}
}

func TestIssueViewDefaultExclusivity(t *testing.T) {
	fx := setupViewTest(t)
	a := mustCreateView(t, fx, fx.actorA, viewInput("A"))
	b := mustCreateView(t, fx, fx.actorA, viewInput("B"))

	isDefault := true
	if _, err := UpdateIssueView(fx.ctx, fx.pool, fx.actorA, fx.projectID, a.ID, IssueViewPatch{IsDefault: &isDefault}); err != nil {
		t.Fatalf("set default A: %v", err)
	}
	if _, err := UpdateIssueView(fx.ctx, fx.pool, fx.actorA, fx.projectID, b.ID, IssueViewPatch{IsDefault: &isDefault}); err != nil {
		t.Fatalf("set default B: %v", err)
	}
	views, err := ListIssueViews(fx.ctx, fx.pool, fx.actorA, fx.projectID)
	if err != nil {
		t.Fatalf("ListIssueViews: %v", err)
	}
	defaults := 0
	for _, v := range views {
		if v.IsDefault {
			defaults++
			if v.ID != b.ID {
				t.Fatalf("default = %q, want B (the last one set)", v.Name)
			}
		}
	}
	if defaults != 1 {
		t.Fatalf("defaults = %d, want exactly 1", defaults)
	}

	// Unsetting the default leaves zero defaults.
	unset := false
	if _, err := UpdateIssueView(fx.ctx, fx.pool, fx.actorA, fx.projectID, b.ID, IssueViewPatch{IsDefault: &unset}); err != nil {
		t.Fatalf("unset default: %v", err)
	}
	views, err = ListIssueViews(fx.ctx, fx.pool, fx.actorA, fx.projectID)
	if err != nil {
		t.Fatalf("ListIssueViews: %v", err)
	}
	for _, v := range views {
		if v.IsDefault {
			t.Fatalf("view %q still default after unset", v.Name)
		}
	}

	// actorB's default is independent (per-user).
	if _, err := UpdateIssueView(fx.ctx, fx.pool, fx.actorB, fx.projectID, mustCreateView(t, fx, fx.actorB, viewInput("B2")).ID, IssueViewPatch{IsDefault: &isDefault}); err != nil {
		t.Fatalf("actorB set default: %v", err)
	}
	views, err = ListIssueViews(fx.ctx, fx.pool, fx.actorA, fx.projectID)
	if err != nil {
		t.Fatalf("ListIssueViews: %v", err)
	}
	for _, v := range views {
		if v.IsDefault {
			t.Fatalf("actorB's default leaked into actorA's list")
		}
	}
}

func TestIssueViewCascadeOnProjectDelete(t *testing.T) {
	fx := setupViewTest(t)
	mustCreateView(t, fx, fx.actorA, viewInput("Doomed"))

	if _, err := fx.pool.Exec(fx.ctx,
		`DELETE FROM projects WHERE id = $1::uuid`, fx.projectID); err != nil {
		t.Fatalf("delete project: %v", err)
	}
	var n int
	if err := fx.pool.QueryRow(fx.ctx,
		`SELECT COUNT(*) FROM issue_views WHERE project_id = $1::uuid`, fx.projectID).Scan(&n); err != nil {
		t.Fatalf("count views: %v", err)
	}
	if n != 0 {
		t.Fatalf("issue_views rows after project delete = %d, want 0", n)
	}
}

func TestIssueViewMalformedViewID(t *testing.T) {
	fx := setupViewTest(t)
	// Malformed view ids never 500: update → ErrViewNotFound, delete → nil.
	if _, err := UpdateIssueView(fx.ctx, fx.pool, fx.actorA, fx.projectID, "nope", IssueViewPatch{Name: strPtr("x")}); !errors.Is(err, ErrViewNotFound) {
		t.Fatalf("update malformed view id: err = %v, want ErrViewNotFound", err)
	}
	if err := DeleteIssueView(fx.ctx, fx.pool, fx.actorA, fx.projectID, "nope"); err != nil {
		t.Fatalf("delete malformed view id: %v", err)
	}
}
