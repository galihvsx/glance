package service

// Pages (C3T3): project-scoped CRUD, hierarchy + move (cycle guard),
// member scoping, and version history (update snapshots + restore).

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func setupPageTest(t *testing.T) (context.Context, *pgxpool.Pool, string, string, string) {
	t.Helper()
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	actor := createTestUser(t, pool, uniqueTestEmail("page"))
	slug := uniqueTestSlug("page-ws")
	createTestWorkspace(t, pool, "Page Co", slug, actor)
	ident := uniqueTestIdentifier()
	createTestProject(t, pool, slug, actor, "Eng", ident)
	return ctx, pool, slug, ident, actor
}

func mustCreatePage(t *testing.T, ctx context.Context, pool *pgxpool.Pool, slug, ident, actor, title string, parentID *string) *Page {
	t.Helper()
	p, err := CreatePage(ctx, pool, slug, ident, actor, PageInput{Title: title, ParentID: parentID})
	if err != nil {
		t.Fatalf("CreatePage(%q): %v", title, err)
	}
	return p
}

func TestPageCRUD(t *testing.T) {
	ctx, pool, slug, ident, actor := setupPageTest(t)

	content := "# Hello"
	root := mustCreatePage(t, ctx, pool, slug, ident, actor, "Root", nil)
	if root.ParentID != nil {
		t.Fatalf("root parent = %v, want nil", root.ParentID)
	}
	if root.Position != 0 {
		t.Fatalf("root position = %d, want 0", root.Position)
	}
	// Content via pointer.
	p2, err := CreatePage(ctx, pool, slug, ident, actor, PageInput{Title: "Child", Content: &content, ParentID: &root.ID})
	if err != nil {
		t.Fatalf("CreatePage child: %v", err)
	}
	if p2.ParentID == nil || *p2.ParentID != root.ID {
		t.Fatalf("child parent = %v, want %s", p2.ParentID, root.ID)
	}
	if p2.Content != content {
		t.Fatalf("child content = %q, want %q", p2.Content, content)
	}

	// Empty title is 400.
	if _, err := CreatePage(ctx, pool, slug, ident, actor, PageInput{Title: "  "}); !errors.Is(err, ErrInvalidPage) {
		t.Fatalf("empty title: err = %v, want ErrInvalidPage", err)
	}
	// Foreign parent (well-formed UUID, not a page) is 400, not a leak.
	if _, err := CreatePage(ctx, pool, slug, ident, actor,
		PageInput{Title: "X", ParentID: strptr("00000000-0000-4000-8000-000000000000")}); !errors.Is(err, ErrInvalidPage) {
		t.Fatalf("foreign parent: err = %v, want ErrInvalidPage", err)
	}

	got, err := GetPage(ctx, pool, slug, ident, actor, root.ID)
	if err != nil {
		t.Fatalf("GetPage: %v", err)
	}
	if got.ID != root.ID || got.Title != "Root" {
		t.Fatalf("got = %+v", got)
	}

	list, err := ListPages(ctx, pool, slug, ident, actor)
	if err != nil {
		t.Fatalf("ListPages: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("list len = %d, want 2", len(list))
	}
	if list[0].ID != root.ID || list[1].ID != p2.ID {
		t.Fatalf("list order wrong: roots first, got %s then %s", list[0].ID, list[1].ID)
	}

	// Update title+content → revision of the pre-update state.
	newTitle, newContent := "Root v2", "# Hello v2"
	up, err := UpdatePage(ctx, pool, slug, ident, actor, root.ID,
		PagePatch{Title: &newTitle, Content: &newContent})
	if err != nil {
		t.Fatalf("UpdatePage: %v", err)
	}
	if up.Title != newTitle || up.Content != newContent {
		t.Fatalf("updated = %+v", up)
	}
	revs, err := ListPageRevisions(ctx, pool, slug, ident, actor, root.ID)
	if err != nil {
		t.Fatalf("ListPageRevisions: %v", err)
	}
	if len(revs) != 1 || revs[0].Title != "Root" || revs[0].Content != "" {
		t.Fatalf("revisions = %+v, want one pre-update snapshot", revs)
	}

	// Title-only change also snapshots.
	v3 := "Root v3"
	if _, err := UpdatePage(ctx, pool, slug, ident, actor, root.ID, PagePatch{Title: &v3}); err != nil {
		t.Fatalf("UpdatePage title-only: %v", err)
	}
	revs, err = ListPageRevisions(ctx, pool, slug, ident, actor, root.ID)
	if err != nil {
		t.Fatalf("ListPageRevisions: %v", err)
	}
	if len(revs) != 2 || revs[0].Title != newTitle {
		t.Fatalf("revisions = %+v, want newest first", revs)
	}

	// Nothing to update is an error.
	if _, err := UpdatePage(ctx, pool, slug, ident, actor, root.ID, PagePatch{}); !errors.Is(err, ErrNothingToUpdate) {
		t.Fatalf("empty patch: err = %v, want ErrNothingToUpdate", err)
	}

	// Delete root → subtree cascades.
	if err := DeletePage(ctx, pool, slug, ident, actor, root.ID); err != nil {
		t.Fatalf("DeletePage: %v", err)
	}
	if _, err := GetPage(ctx, pool, slug, ident, actor, p2.ID); !errors.Is(err, ErrPageNotFound) {
		t.Fatalf("child after parent delete: err = %v, want ErrPageNotFound", err)
	}
}

func TestPageScoping(t *testing.T) {
	ctx, pool, slug, ident, actor := setupPageTest(t)
	p := mustCreatePage(t, ctx, pool, slug, ident, actor, "Scoped", nil)

	// Outsider (no workspace membership): reads and mutations 404.
	outsider := createTestUser(t, pool, uniqueTestEmail("page-out"))
	for name, call := range map[string]func() error{
		"list": func() error { _, err := ListPages(ctx, pool, slug, ident, outsider); return err },
		"get":  func() error { _, err := GetPage(ctx, pool, slug, ident, outsider, p.ID); return err },
		"create": func() error {
			_, err := CreatePage(ctx, pool, slug, ident, outsider, PageInput{Title: "X"})
			return err
		},
		"update": func() error {
			_, err := UpdatePage(ctx, pool, slug, ident, outsider, p.ID, PagePatch{Title: strptr("Y")})
			return err
		},
		"delete": func() error { return DeletePage(ctx, pool, slug, ident, outsider, p.ID) },
		"move": func() error {
			_, err := MovePage(ctx, pool, slug, ident, outsider, p.ID, PageMoveInput{PositionSet: true, Position: 0})
			return err
		},
		"restore": func() error { _, err := RestorePageRevision(ctx, pool, slug, ident, outsider, p.ID, p.ID); return err },
	} {
		if err := call(); !errors.Is(err, ErrNotFound) {
			t.Fatalf("outsider %s: err = %v, want ErrNotFound", name, err)
		}
	}

	// Guest (role 5): reads OK, mutations forbidden.
	guest := createTestUser(t, pool, uniqueTestEmail("page-guest"))
	addTestMember(t, pool, slug, actor, guest, RoleGuest)
	if _, err := ListPages(ctx, pool, slug, ident, guest); err != nil {
		t.Fatalf("guest list: %v", err)
	}
	if _, err := GetPage(ctx, pool, slug, ident, guest, p.ID); err != nil {
		t.Fatalf("guest get: %v", err)
	}
	for name, call := range map[string]func() error{
		"create": func() error {
			_, err := CreatePage(ctx, pool, slug, ident, guest, PageInput{Title: "X"})
			return err
		},
		"update": func() error {
			_, err := UpdatePage(ctx, pool, slug, ident, guest, p.ID, PagePatch{Title: strptr("Y")})
			return err
		},
		"delete": func() error { return DeletePage(ctx, pool, slug, ident, guest, p.ID) },
		"move": func() error {
			_, err := MovePage(ctx, pool, slug, ident, guest, p.ID, PageMoveInput{PositionSet: true, Position: 0})
			return err
		},
	} {
		if err := call(); !errors.Is(err, ErrForbidden) {
			t.Fatalf("guest %s: err = %v, want ErrForbidden", name, err)
		}
	}

	// Cross-project isolation: a page id from another project is 404.
	ident2 := uniqueTestIdentifier()
	createTestProject(t, pool, slug, actor, "Eng2", ident2)
	if _, err := GetPage(ctx, pool, slug, ident2, actor, p.ID); !errors.Is(err, ErrPageNotFound) {
		t.Fatalf("cross-project get: err = %v, want ErrPageNotFound", err)
	}
}

func TestPageMove(t *testing.T) {
	ctx, pool, slug, ident, actor := setupPageTest(t)

	r1 := mustCreatePage(t, ctx, pool, slug, ident, actor, "R1", nil)
	r2 := mustCreatePage(t, ctx, pool, slug, ident, actor, "R2", nil)
	child := mustCreatePage(t, ctx, pool, slug, ident, actor, "C", &r1.ID)
	grandchild := mustCreatePage(t, ctx, pool, slug, ident, actor, "G", &child.ID)

	// Reparent G from C to R2.
	moved, err := MovePage(ctx, pool, slug, ident, actor, grandchild.ID,
		PageMoveInput{ParentIDSet: true, ParentID: &r2.ID})
	if err != nil {
		t.Fatalf("MovePage reparent: %v", err)
	}
	if moved.ParentID == nil || *moved.ParentID != r2.ID {
		t.Fatalf("moved parent = %v, want %s", moved.ParentID, r2.ID)
	}

	// Move to root (explicit null).
	moved, err = MovePage(ctx, pool, slug, ident, actor, grandchild.ID,
		PageMoveInput{ParentIDSet: true, ParentID: nil})
	if err != nil {
		t.Fatalf("MovePage to root: %v", err)
	}
	if moved.ParentID != nil {
		t.Fatalf("moved parent = %v, want nil (root)", moved.ParentID)
	}

	// Cycle guard: move R1 under its own descendant C.
	if _, err := MovePage(ctx, pool, slug, ident, actor, r1.ID,
		PageMoveInput{ParentIDSet: true, ParentID: &child.ID}); !errors.Is(err, ErrPageCycle) {
		t.Fatalf("cycle move: err = %v, want ErrPageCycle", err)
	}
	// Cycle guard: move a page under itself.
	if _, err := MovePage(ctx, pool, slug, ident, actor, r1.ID,
		PageMoveInput{ParentIDSet: true, ParentID: &r1.ID}); !errors.Is(err, ErrPageCycle) {
		t.Fatalf("self move: err = %v, want ErrPageCycle", err)
	}

	// Reorder: move R2 to position 0 among roots.
	moved, err = MovePage(ctx, pool, slug, ident, actor, r2.ID,
		PageMoveInput{PositionSet: true, Position: 0})
	if err != nil {
		t.Fatalf("MovePage reorder: %v", err)
	}
	if moved.Position != 0 {
		t.Fatalf("moved position = %d, want 0", moved.Position)
	}
	list, err := ListPages(ctx, pool, slug, ident, actor)
	if err != nil {
		t.Fatalf("ListPages: %v", err)
	}
	// Roots: R2 (pos 0), R1 (shifted to 1), G (root after explicit-null move).
	if len(list) < 3 || list[0].ID != r2.ID || list[1].ID != r1.ID {
		t.Fatalf("root order wrong: %+v", list)
	}

	// Negative position is 400.
	if _, err := MovePage(ctx, pool, slug, ident, actor, r2.ID,
		PageMoveInput{PositionSet: true, Position: -1}); !errors.Is(err, ErrInvalidPage) {
		t.Fatalf("negative position: err = %v, want ErrInvalidPage", err)
	}
	// Empty move is an error.
	if _, err := MovePage(ctx, pool, slug, ident, actor, r2.ID, PageMoveInput{}); !errors.Is(err, ErrNothingToUpdate) {
		t.Fatalf("empty move: err = %v, want ErrNothingToUpdate", err)
	}
	// Malformed page id is 400.
	if _, err := MovePage(ctx, pool, slug, ident, actor, "nope",
		PageMoveInput{PositionSet: true}); !errors.Is(err, ErrInvalidPageID) {
		t.Fatalf("malformed id: err = %v, want ErrInvalidPageID", err)
	}
}

func TestPageRestore(t *testing.T) {
	ctx, pool, slug, ident, actor := setupPageTest(t)

	p := mustCreatePage(t, ctx, pool, slug, ident, actor, "Doc", nil)
	v2, v2c := "Doc v2", "second body"
	if _, err := UpdatePage(ctx, pool, slug, ident, actor, p.ID,
		PagePatch{Title: &v2, Content: &v2c}); err != nil {
		t.Fatalf("UpdatePage: %v", err)
	}
	revs, err := ListPageRevisions(ctx, pool, slug, ident, actor, p.ID)
	if err != nil {
		t.Fatalf("ListPageRevisions: %v", err)
	}
	if len(revs) != 1 {
		t.Fatalf("revisions = %d, want 1", len(revs))
	}

	// Restore to v1: pre-restore state (v2) is snapshotted first.
	restored, err := RestorePageRevision(ctx, pool, slug, ident, actor, p.ID, revs[0].ID)
	if err != nil {
		t.Fatalf("RestorePageRevision: %v", err)
	}
	if restored.Title != "Doc" || restored.Content != "" {
		t.Fatalf("restored = %+v, want v1 state", restored)
	}
	revs, err = ListPageRevisions(ctx, pool, slug, ident, actor, p.ID)
	if err != nil {
		t.Fatalf("ListPageRevisions: %v", err)
	}
	if len(revs) != 2 || revs[0].Title != v2 || revs[0].Content != v2c {
		t.Fatalf("revisions after restore = %+v, want pre-restore snapshot newest", revs)
	}

	// Unknown revision is 404.
	if _, err := RestorePageRevision(ctx, pool, slug, ident, actor, p.ID,
		"00000000-0000-4000-8000-000000000000"); !errors.Is(err, ErrRevisionNotFound) {
		t.Fatalf("unknown revision: err = %v, want ErrRevisionNotFound", err)
	}
	// Malformed revision id is 400.
	if _, err := RestorePageRevision(ctx, pool, slug, ident, actor, p.ID, "nope"); !errors.Is(err, ErrInvalidPageID) {
		t.Fatalf("malformed revision: err = %v, want ErrInvalidPageID", err)
	}
}
