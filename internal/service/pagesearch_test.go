package service

// Pages search (C66T0): global full-text search over page titles and
// content, mirroring SearchIssues discipline (plainto_tsquery, ts_rank
// DESC + id tiebreak, keyset cursor on (rank,id), member-only authz,
// archived-project skip). Written TDD: red first, then SearchPages.

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func setupPageSearchTest(t *testing.T) (context.Context, *pgxpool.Pool, string, string, string) {
	t.Helper()
	ctx := context.Background()
	pool := newTestPool(t)
	actor := createTestUser(t, pool, uniqueTestEmail("pgs"))
	slug := uniqueTestSlug("pgs-ws")
	createTestWorkspace(t, pool, "PageSearch Co", slug, actor)
	ident := uniqueTestIdentifier()
	createTestProject(t, pool, slug, actor, "Eng", ident)
	return ctx, pool, slug, ident, actor
}

func mustSearchPage(t *testing.T, ctx context.Context, pool *pgxpool.Pool, slug, ident, actor, title, content string) *Page {
	t.Helper()
	p, err := CreatePage(ctx, pool, slug, ident, actor, PageInput{Title: title, Content: &content})
	if err != nil {
		t.Fatalf("CreatePage(%q): %v", title, err)
	}
	return p
}

func TestSearchPagesTitleMatch(t *testing.T) {
	ctx, pool, slug, ident, actor := setupPageSearchTest(t)
	want := mustSearchPage(t, ctx, pool, slug, ident, actor, "rocket launch plan", "irrelevant body")

	res, err := SearchPages(ctx, pool, actor, "rocket", "", 25)
	if err != nil {
		t.Fatalf("SearchPages: %v", err)
	}
	if len(res.Results) != 1 {
		t.Fatalf("results = %d, want 1", len(res.Results))
	}
	got := res.Results[0]
	if got.ID != want.ID {
		t.Fatalf("result id = %s, want %s", got.ID, want.ID)
	}
	if got.Title != "rocket launch plan" || got.ProjectIdentifier != ident || got.WorkspaceSlug != slug {
		t.Fatalf("result context wrong: %+v", got)
	}
	if res.NextCursor != "" {
		t.Fatalf("next_cursor = %q, want empty on last page", res.NextCursor)
	}
}

func TestSearchPagesContentMatch(t *testing.T) {
	ctx, pool, slug, ident, actor := setupPageSearchTest(t)
	mustSearchPage(t, ctx, pool, slug, ident, actor, "unrelated title", "the rocket engine needs fuel")
	mustSearchPage(t, ctx, pool, slug, ident, actor, "other doc", "nothing about propulsion here")

	res, err := SearchPages(ctx, pool, actor, "rocket", "", 25)
	if err != nil {
		t.Fatalf("SearchPages: %v", err)
	}
	if len(res.Results) != 1 {
		t.Fatalf("results = %d, want 1 (content-only match)", len(res.Results))
	}
	if res.Results[0].Title != "unrelated title" {
		t.Fatalf("result title = %q, want the content-only match", res.Results[0].Title)
	}
}

func TestSearchPagesTitleOutranksContent(t *testing.T) {
	ctx, pool, slug, ident, actor := setupPageSearchTest(t)
	mustSearchPage(t, ctx, pool, slug, ident, actor, "unrelated doc",
		"rocket with filler words to lengthen the document so the ranking separates cleanly")
	mustSearchPage(t, ctx, pool, slug, ident, actor, "rocket", "")

	res, err := SearchPages(ctx, pool, actor, "rocket", "", 25)
	if err != nil {
		t.Fatalf("SearchPages: %v", err)
	}
	if len(res.Results) != 2 {
		t.Fatalf("results = %d, want 2", len(res.Results))
	}
	if res.Results[0].Title != "rocket" {
		t.Fatalf("first result = %q, want the title-weighted match", res.Results[0].Title)
	}
}

func TestSearchPagesSkipsArchivedProject(t *testing.T) {
	ctx, pool, slug, ident, actor := setupPageSearchTest(t)
	mustSearchPage(t, ctx, pool, slug, ident, actor, "rocket manual", "how to fly")

	if _, err := ArchiveProject(ctx, pool, slug, ident, actor, true); err != nil {
		t.Fatalf("ArchiveProject: %v", err)
	}
	res, err := SearchPages(ctx, pool, actor, "rocket", "", 25)
	if err != nil {
		t.Fatalf("SearchPages: %v", err)
	}
	if len(res.Results) != 0 {
		t.Fatalf("results = %d after archive, want 0", len(res.Results))
	}
}

func TestSearchPagesNonMemberGetsNothing(t *testing.T) {
	ctx, pool, slug, ident, actor := setupPageSearchTest(t)
	mustSearchPage(t, ctx, pool, slug, ident, actor, "rocket manual", "how to fly")
	outsider := createTestUser(t, pool, uniqueTestEmail("pgs-out"))

	res, err := SearchPages(ctx, pool, outsider, "rocket", "", 25)
	if err != nil {
		t.Fatalf("SearchPages: %v", err)
	}
	if len(res.Results) != 0 {
		t.Fatalf("non-member results = %d, want 0 (no leak)", len(res.Results))
	}
}

func TestSearchPagesCursorPagination(t *testing.T) {
	ctx, pool, slug, ident, actor := setupPageSearchTest(t)
	for i := 0; i < 3; i++ {
		mustSearchPage(t, ctx, pool, slug, ident, actor, "pagination probe", "body "+string(rune('a'+i)))
	}

	page1, err := SearchPages(ctx, pool, actor, "pagination", "", 2)
	if err != nil {
		t.Fatalf("SearchPages page 1: %v", err)
	}
	if len(page1.Results) != 2 || page1.NextCursor == "" {
		t.Fatalf("page 1: got %d results cursor=%q, want 2 + cursor", len(page1.Results), page1.NextCursor)
	}

	page2, err := SearchPages(ctx, pool, actor, "pagination", page1.NextCursor, 2)
	if err != nil {
		t.Fatalf("SearchPages page 2: %v", err)
	}
	if len(page2.Results) != 1 || page2.NextCursor != "" {
		t.Fatalf("page 2: got %d results cursor=%q, want 1 + no cursor", len(page2.Results), page2.NextCursor)
	}
	seen := map[string]bool{}
	for _, r := range page1.Results {
		seen[r.ID] = true
	}
	for _, r := range page2.Results {
		if seen[r.ID] {
			t.Fatalf("duplicate page %s across pages", r.ID)
		}
	}
}

func TestSearchPagesBadCursor(t *testing.T) {
	ctx, pool, slug, ident, actor := setupPageSearchTest(t)
	mustSearchPage(t, ctx, pool, slug, ident, actor, "rocket manual", "how to fly")

	if _, err := SearchPages(ctx, pool, actor, "rocket", "garbage", 25); err != ErrInvalidCursor {
		t.Fatalf("garbage cursor: err = %v, want ErrInvalidCursor", err)
	}
}

func TestSearchPagesEmptyQuery(t *testing.T) {
	ctx, pool, _, _, actor := setupPageSearchTest(t)
	for _, q := range []string{"", "   "} {
		if _, err := SearchPages(ctx, pool, actor, q, "", 25); err != ErrQueryRequired {
			t.Fatalf("q=%q: err = %v, want ErrQueryRequired", q, err)
		}
	}
}

func TestSearchPagesInvalidPerPage(t *testing.T) {
	ctx, pool, _, _, actor := setupPageSearchTest(t)
	if _, err := SearchPages(ctx, pool, actor, "rocket", "", -1); err != ErrInvalidListFilter {
		t.Fatalf("perPage=-1: err = %v, want ErrInvalidListFilter", err)
	}
}
