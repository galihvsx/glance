package service

// Page↔issue links (C36T0): link/unlink in both directions, duplicate →
// 409, cross-project → 409, unknown page/issue/link → 404, role gates
// (guest 5+ reads, member 15+ writes), and the no-exclusion rule —
// archived and draft issues are linkable.

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func setupPageIssueLinkTest(t *testing.T) (context.Context, *pgxpool.Pool, string, string, string) {
	t.Helper()
	ctx := context.Background()
	pool := newTestPool(t)
	actor := createTestUser(t, pool, uniqueTestEmail("pil"))
	slug := uniqueTestSlug("pil-ws")
	createTestWorkspace(t, pool, "PageIssueLink Co", slug, actor)
	ident := uniqueTestIdentifier()
	createTestProject(t, pool, slug, actor, "Eng", ident)
	return ctx, pool, slug, ident, actor
}

func TestPageIssueLinkCreateAndBothLists(t *testing.T) {
	ctx, pool, slug, ident, actor := setupPageIssueLinkTest(t)
	page := mustCreatePage(t, ctx, pool, slug, ident, actor, "Spec", nil)
	iss := createTestIssue(t, pool, slug, ident, actor, "Implement spec")

	l, err := LinkPageIssue(ctx, pool, slug, ident, page.ID, actor, iss.ID)
	if err != nil {
		t.Fatalf("LinkPageIssue: %v", err)
	}
	if l.PageID != page.ID || l.IssueID != iss.ID || l.ID == "" || l.CreatedAt.IsZero() {
		t.Fatalf("link = %+v", l)
	}
	if l.CreatedBy == nil || *l.CreatedBy != actor {
		t.Fatalf("created_by = %v, want %s", l.CreatedBy, actor)
	}

	// Page side: display_id, title, state, priority.
	issues, err := ListPageIssues(ctx, pool, slug, ident, page.ID, actor)
	if err != nil {
		t.Fatalf("ListPageIssues: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("issues = %+v, want 1", issues)
	}
	got := issues[0]
	if got.ID != iss.ID || got.DisplayID != iss.DisplayID || got.Title != "Implement spec" {
		t.Fatalf("linked issue = %+v, want id %s display %s", got, iss.ID, iss.DisplayID)
	}
	if got.State == "" || got.StateID == "" {
		t.Fatalf("linked issue state missing: %+v", got)
	}
	if got.Priority != iss.Priority {
		t.Fatalf("priority = %d, want %d", got.Priority, iss.Priority)
	}

	// Issue side: title, updated_at.
	pages, err := ListIssuePages(ctx, pool, slug, ident, iss.ID, actor)
	if err != nil {
		t.Fatalf("ListIssuePages: %v", err)
	}
	if len(pages) != 1 || pages[0].ID != page.ID || pages[0].Title != "Spec" {
		t.Fatalf("pages = %+v, want page %s", pages, page.ID)
	}
	if pages[0].UpdatedAt.IsZero() {
		t.Fatalf("linked page updated_at missing: %+v", pages[0])
	}
}

func TestPageIssueLinkDuplicate409(t *testing.T) {
	ctx, pool, slug, ident, actor := setupPageIssueLinkTest(t)
	page := mustCreatePage(t, ctx, pool, slug, ident, actor, "Spec", nil)
	iss := createTestIssue(t, pool, slug, ident, actor, "Work")

	if _, err := LinkPageIssue(ctx, pool, slug, ident, page.ID, actor, iss.ID); err != nil {
		t.Fatalf("first LinkPageIssue: %v", err)
	}
	// Same pair from the issue direction is still a duplicate.
	if _, err := LinkPageIssue(ctx, pool, slug, ident, page.ID, actor, iss.ID); !errors.Is(err, ErrPageIssueLinkConflict) {
		t.Fatalf("repeat err = %v, want ErrPageIssueLinkConflict", err)
	}
}

func TestPageIssueLinkCrossProject409(t *testing.T) {
	ctx, pool, slug, ident, actor := setupPageIssueLinkTest(t)
	page := mustCreatePage(t, ctx, pool, slug, ident, actor, "Home page", nil)

	ident2 := uniqueTestIdentifier()
	createTestProject(t, pool, slug, actor, "Other", ident2)
	foreign := createTestIssue(t, pool, slug, ident2, actor, "foreign issue")

	if _, err := LinkPageIssue(ctx, pool, slug, ident, page.ID, actor, foreign.ID); !errors.Is(err, ErrPageIssueLinkCrossProject) {
		t.Fatalf("err = %v, want ErrPageIssueLinkCrossProject", err)
	}
	// A foreign page addressed from this project is a 404, not a leak.
	foreignPage := mustCreatePage(t, ctx, pool, slug, ident2, actor, "Foreign page", nil)
	home := createTestIssue(t, pool, slug, ident, actor, "home issue")
	if _, err := LinkPageIssue(ctx, pool, slug, ident, foreignPage.ID, actor, home.ID); !errors.Is(err, ErrPageNotFound) {
		t.Fatalf("foreign page err = %v, want ErrPageNotFound", err)
	}
}

func TestPageIssueLinkUnknown404(t *testing.T) {
	ctx, pool, slug, ident, actor := setupPageIssueLinkTest(t)
	page := mustCreatePage(t, ctx, pool, slug, ident, actor, "Spec", nil)
	iss := createTestIssue(t, pool, slug, ident, actor, "Work")
	zero := "00000000-0000-0000-0000-000000000000"

	// Unknown issue → ErrIssueNotFound; malformed issue id → same, never a 500.
	if _, err := LinkPageIssue(ctx, pool, slug, ident, page.ID, actor, zero); !errors.Is(err, ErrIssueNotFound) {
		t.Fatalf("unknown issue err = %v, want ErrIssueNotFound", err)
	}
	if _, err := LinkPageIssue(ctx, pool, slug, ident, page.ID, actor, "not-a-uuid"); !errors.Is(err, ErrIssueNotFound) {
		t.Fatalf("malformed issue err = %v, want ErrIssueNotFound", err)
	}
	// Unknown page → ErrPageNotFound.
	if _, err := LinkPageIssue(ctx, pool, slug, ident, zero, actor, iss.ID); !errors.Is(err, ErrPageNotFound) {
		t.Fatalf("unknown page err = %v, want ErrPageNotFound", err)
	}
	// Lists on unknown anchors → their own 404s.
	if _, err := ListPageIssues(ctx, pool, slug, ident, zero, actor); !errors.Is(err, ErrPageNotFound) {
		t.Fatalf("ListPageIssues unknown page err = %v, want ErrPageNotFound", err)
	}
	if _, err := ListIssuePages(ctx, pool, slug, ident, zero, actor); !errors.Is(err, ErrIssueNotFound) {
		t.Fatalf("ListIssuePages unknown issue err = %v, want ErrIssueNotFound", err)
	}
	// Unknown project → ErrProjectNotFound.
	if _, err := ListPageIssues(ctx, pool, slug, "NOPE", page.ID, actor); !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("bad project err = %v, want ErrProjectNotFound", err)
	}
}

func TestPageIssueLinkUnlink(t *testing.T) {
	ctx, pool, slug, ident, actor := setupPageIssueLinkTest(t)
	page := mustCreatePage(t, ctx, pool, slug, ident, actor, "Spec", nil)
	iss := createTestIssue(t, pool, slug, ident, actor, "Work")

	if _, err := LinkPageIssue(ctx, pool, slug, ident, page.ID, actor, iss.ID); err != nil {
		t.Fatalf("LinkPageIssue: %v", err)
	}
	if err := UnlinkPageIssue(ctx, pool, slug, ident, page.ID, actor, iss.ID); err != nil {
		t.Fatalf("UnlinkPageIssue: %v", err)
	}
	// Both lists are empty again.
	issues, err := ListPageIssues(ctx, pool, slug, ident, page.ID, actor)
	if err != nil || len(issues) != 0 {
		t.Fatalf("issues after unlink = %+v, err = %v", issues, err)
	}
	pages, err := ListIssuePages(ctx, pool, slug, ident, iss.ID, actor)
	if err != nil || len(pages) != 0 {
		t.Fatalf("pages after unlink = %+v, err = %v", pages, err)
	}
	// A second unlink is a 404, not a silent no-op.
	if err := UnlinkPageIssue(ctx, pool, slug, ident, page.ID, actor, iss.ID); !errors.Is(err, ErrPageIssueLinkNotFound) {
		t.Fatalf("re-unlink err = %v, want ErrPageIssueLinkNotFound", err)
	}
	// Unlinking a pair that was never linked is a 404 too.
	other := createTestIssue(t, pool, slug, ident, actor, "Other")
	if err := UnlinkPageIssue(ctx, pool, slug, ident, page.ID, actor, other.ID); !errors.Is(err, ErrPageIssueLinkNotFound) {
		t.Fatalf("never-linked err = %v, want ErrPageIssueLinkNotFound", err)
	}
	// Malformed issue id → 400, never a 500 from a ::uuid cast.
	if err := UnlinkPageIssue(ctx, pool, slug, ident, page.ID, actor, "not-a-uuid"); !errors.Is(err, ErrInvalidPageIssueLink) {
		t.Fatalf("malformed unlink err = %v, want ErrInvalidPageIssueLink", err)
	}
}

func TestPageIssueLinkGuestReadOnly(t *testing.T) {
	ctx, pool, slug, ident, actor := setupPageIssueLinkTest(t)
	guest := createTestUser(t, pool, uniqueTestEmail("pil-guest"))
	addTestMember(t, pool, slug, actor, guest, RoleGuest)

	page := mustCreatePage(t, ctx, pool, slug, ident, actor, "Spec", nil)
	iss := createTestIssue(t, pool, slug, ident, actor, "Work")
	if _, err := LinkPageIssue(ctx, pool, slug, ident, page.ID, actor, iss.ID); err != nil {
		t.Fatalf("LinkPageIssue: %v", err)
	}

	// Guests may read both directions.
	if _, err := ListPageIssues(ctx, pool, slug, ident, page.ID, guest); err != nil {
		t.Fatalf("guest ListPageIssues: %v", err)
	}
	if _, err := ListIssuePages(ctx, pool, slug, ident, iss.ID, guest); err != nil {
		t.Fatalf("guest ListIssuePages: %v", err)
	}
	// Guests may not link or unlink.
	other := createTestIssue(t, pool, slug, ident, actor, "Other")
	if _, err := LinkPageIssue(ctx, pool, slug, ident, page.ID, guest, other.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest link err = %v, want ErrForbidden", err)
	}
	if err := UnlinkPageIssue(ctx, pool, slug, ident, page.ID, guest, iss.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest unlink err = %v, want ErrForbidden", err)
	}
	// Non-members get the tenancy 404, not a forbidden.
	stranger := createTestUser(t, pool, uniqueTestEmail("pil-stranger"))
	if _, err := ListPageIssues(ctx, pool, slug, ident, page.ID, stranger); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-member err = %v, want ErrNotFound", err)
	}
}

func TestPageIssueLinkArchivedAndDraftLinkable(t *testing.T) {
	ctx, pool, slug, ident, actor := setupPageIssueLinkTest(t)
	page := mustCreatePage(t, ctx, pool, slug, ident, actor, "Spec", nil)

	archived := createTestIssue(t, pool, slug, ident, actor, "Archived work")
	if _, err := pool.Exec(ctx, `UPDATE issues SET archived_at = now() WHERE id = $1::uuid`, archived.ID); err != nil {
		t.Fatalf("archive issue: %v", err)
	}
	draft := createTestIssue(t, pool, slug, ident, actor, "Draft work")
	if _, err := pool.Exec(ctx, `UPDATE issues SET is_draft = true WHERE id = $1::uuid`, draft.ID); err != nil {
		t.Fatalf("draft issue: %v", err)
	}

	// No exclusion is invented for either: both link fine and list.
	if _, err := LinkPageIssue(ctx, pool, slug, ident, page.ID, actor, archived.ID); err != nil {
		t.Fatalf("link archived: %v", err)
	}
	if _, err := LinkPageIssue(ctx, pool, slug, ident, page.ID, actor, draft.ID); err != nil {
		t.Fatalf("link draft: %v", err)
	}
	issues, err := ListPageIssues(ctx, pool, slug, ident, page.ID, actor)
	if err != nil {
		t.Fatalf("ListPageIssues: %v", err)
	}
	if len(issues) != 2 {
		t.Fatalf("issues = %+v, want archived + draft", issues)
	}
}

func TestPageIssueLinkListOrdering(t *testing.T) {
	ctx, pool, slug, ident, actor := setupPageIssueLinkTest(t)
	page := mustCreatePage(t, ctx, pool, slug, ident, actor, "Spec", nil)
	first := createTestIssue(t, pool, slug, ident, actor, "First created")
	second := createTestIssue(t, pool, slug, ident, actor, "Second created")

	// Link in reverse creation order: the list must still follow
	// issue created_at, not link order.
	if _, err := LinkPageIssue(ctx, pool, slug, ident, page.ID, actor, second.ID); err != nil {
		t.Fatalf("link second: %v", err)
	}
	if _, err := LinkPageIssue(ctx, pool, slug, ident, page.ID, actor, first.ID); err != nil {
		t.Fatalf("link first: %v", err)
	}
	issues, err := ListPageIssues(ctx, pool, slug, ident, page.ID, actor)
	if err != nil {
		t.Fatalf("ListPageIssues: %v", err)
	}
	if len(issues) != 2 || issues[0].ID != first.ID || issues[1].ID != second.ID {
		t.Fatalf("issues order = %+v, want first then second", issues)
	}

	// Issue side: newest-updated page first. Touch the older page so
	// its updated_at passes the other.
	p1 := mustCreatePage(t, ctx, pool, slug, ident, actor, "Older", nil)
	p2 := mustCreatePage(t, ctx, pool, slug, ident, actor, "Newer", nil)
	iss := createTestIssue(t, pool, slug, ident, actor, "Work")
	if _, err := LinkPageIssue(ctx, pool, slug, ident, p1.ID, actor, iss.ID); err != nil {
		t.Fatalf("link p1: %v", err)
	}
	if _, err := LinkPageIssue(ctx, pool, slug, ident, p2.ID, actor, iss.ID); err != nil {
		t.Fatalf("link p2: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE pages SET updated_at = now() + interval '1 hour' WHERE id = $1::uuid`, p1.ID); err != nil {
		t.Fatalf("touch p1: %v", err)
	}
	pages, err := ListIssuePages(ctx, pool, slug, ident, iss.ID, actor)
	if err != nil {
		t.Fatalf("ListIssuePages: %v", err)
	}
	if len(pages) != 2 || pages[0].ID != p1.ID || pages[1].ID != p2.ID {
		t.Fatalf("pages order = %+v, want p1 (touched) then p2", pages)
	}
}

func TestPageIssueLinkDeletedIssueHidden(t *testing.T) {
	ctx, pool, slug, ident, actor := setupPageIssueLinkTest(t)
	page := mustCreatePage(t, ctx, pool, slug, ident, actor, "Spec", nil)
	iss := createTestIssue(t, pool, slug, ident, actor, "Doomed")
	if _, err := LinkPageIssue(ctx, pool, slug, ident, page.ID, actor, iss.ID); err != nil {
		t.Fatalf("LinkPageIssue: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE issues SET deleted_at = now() WHERE id = $1::uuid`, iss.ID); err != nil {
		t.Fatalf("soft-delete issue: %v", err)
	}
	issues, err := ListPageIssues(ctx, pool, slug, ident, page.ID, actor)
	if err != nil {
		t.Fatalf("ListPageIssues: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("issues = %+v, want soft-deleted issue hidden", issues)
	}
	// Linking a soft-deleted issue is a 404, like GetIssue.
	if _, err := LinkPageIssue(ctx, pool, slug, ident, page.ID, actor, iss.ID); !errors.Is(err, ErrIssueNotFound) {
		t.Fatalf("link deleted err = %v, want ErrIssueNotFound", err)
	}
}
