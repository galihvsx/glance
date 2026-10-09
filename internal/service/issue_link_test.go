package service

// Issue links (C4T0): directed dependency edges between issues — the
// Gantt backend contract.
//
// TDD: this file was written first, against the service API that did
// not exist yet (red), then internal/service/issue_link.go was written
// to satisfy it (green).

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func setupIssueLinkTest(t *testing.T) (context.Context, *pgxpool.Pool, string, string, string) {
	t.Helper()
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	actor := createTestUser(t, pool, uniqueTestEmail("link"))
	slug := uniqueTestSlug("link-ws")
	createTestWorkspace(t, pool, "Link Co", slug, actor)
	ident := uniqueTestIdentifier()
	createTestProject(t, pool, slug, actor, "Eng", ident)
	return ctx, pool, slug, ident, actor
}

func TestIssueLinkCreateDefaultKind(t *testing.T) {
	ctx, pool, slug, ident, actor := setupIssueLinkTest(t)
	a := createTestIssue(t, pool, slug, ident, actor, "A blocks B")
	b := createTestIssue(t, pool, slug, ident, actor, "B")

	// Kind omitted → default 'blocks'.
	l, err := CreateIssueLink(ctx, pool, slug, ident, a.ID, actor, b.ID, "")
	if err != nil {
		t.Fatalf("CreateIssueLink: %v", err)
	}
	if l.Kind != "blocks" {
		t.Fatalf("kind = %q, want blocks", l.Kind)
	}
	if l.IssueID != a.ID || l.TargetIssueID != b.ID {
		t.Fatalf("endpoints = %s -> %s, want %s -> %s", l.IssueID, l.TargetIssueID, a.ID, b.ID)
	}
	if l.ID == "" || l.CreatedAt.IsZero() {
		t.Fatalf("link missing id/created_at: %+v", l)
	}

	links, err := ListIssueLinks(ctx, pool, slug, ident, a.ID, actor)
	if err != nil {
		t.Fatalf("ListIssueLinks: %v", err)
	}
	if len(links) != 1 || links[0].ID != l.ID {
		t.Fatalf("list = %+v", links)
	}
	// Incoming direction is visible from the target side too.
	fromB, err := ListIssueLinks(ctx, pool, slug, ident, b.ID, actor)
	if err != nil {
		t.Fatalf("ListIssueLinks(b): %v", err)
	}
	if len(fromB) != 1 || fromB[0].Direction != "incoming" || fromB[0].ID != l.ID {
		t.Fatalf("list from B = %+v", fromB)
	}
	if links[0].Direction != "outgoing" {
		t.Fatalf("direction = %q, want outgoing", links[0].Direction)
	}
}

func TestIssueLinkSelfLoop409(t *testing.T) {
	ctx, pool, slug, ident, actor := setupIssueLinkTest(t)
	a := createTestIssue(t, pool, slug, ident, actor, "lonely")

	_, err := CreateIssueLink(ctx, pool, slug, ident, a.ID, actor, a.ID, "blocks")
	if !errors.Is(err, ErrIssueLinkSelf) {
		t.Fatalf("err = %v, want ErrIssueLinkSelf", err)
	}
}

func TestIssueLinkCrossProject409(t *testing.T) {
	ctx, pool, slug, ident, actor := setupIssueLinkTest(t)
	a := createTestIssue(t, pool, slug, ident, actor, "home issue")

	ident2 := uniqueTestIdentifier()
	createTestProject(t, pool, slug, actor, "Other", ident2)
	b := createTestIssue(t, pool, slug, ident2, actor, "foreign issue")

	_, err := CreateIssueLink(ctx, pool, slug, ident, a.ID, actor, b.ID, "blocks")
	if !errors.Is(err, ErrIssueLinkCrossProject) {
		t.Fatalf("err = %v, want ErrIssueLinkCrossProject", err)
	}
	// The reverse direction is rejected too.
	_, err = CreateIssueLink(ctx, pool, slug, ident2, b.ID, actor, a.ID, "blocks")
	if !errors.Is(err, ErrIssueLinkCrossProject) {
		t.Fatalf("reverse err = %v, want ErrIssueLinkCrossProject", err)
	}
}

func TestIssueLinkTargetNotFound404(t *testing.T) {
	ctx, pool, slug, ident, actor := setupIssueLinkTest(t)
	a := createTestIssue(t, pool, slug, ident, actor, "A")

	_, err := CreateIssueLink(ctx, pool, slug, ident, a.ID, actor, "00000000-0000-0000-0000-000000000000", "blocks")
	if !errors.Is(err, ErrIssueNotFound) {
		t.Fatalf("err = %v, want ErrIssueNotFound", err)
	}
	// A malformed UUID is a 404 too, never a 500 from a ::uuid cast.
	_, err = CreateIssueLink(ctx, pool, slug, ident, a.ID, actor, "not-a-uuid", "blocks")
	if !errors.Is(err, ErrIssueNotFound) {
		t.Fatalf("err = %v, want ErrIssueNotFound", err)
	}
}

func TestIssueLinkDuplicate409(t *testing.T) {
	ctx, pool, slug, ident, actor := setupIssueLinkTest(t)
	a := createTestIssue(t, pool, slug, ident, actor, "A")
	b := createTestIssue(t, pool, slug, ident, actor, "B")

	if _, err := CreateIssueLink(ctx, pool, slug, ident, a.ID, actor, b.ID, "blocks"); err != nil {
		t.Fatalf("first CreateIssueLink: %v", err)
	}
	// The unique (issue_id, target_issue_id) is the backstop: even if the
	// service pre-check raced, this is a conflict, not a row.
	_, err := CreateIssueLink(ctx, pool, slug, ident, a.ID, actor, b.ID, "relates_to")
	if !errors.Is(err, ErrIssueLinkConflict) {
		t.Fatalf("err = %v, want ErrIssueLinkConflict", err)
	}
}

func TestIssueLinkInvalidKind(t *testing.T) {
	ctx, pool, slug, ident, actor := setupIssueLinkTest(t)
	a := createTestIssue(t, pool, slug, ident, actor, "A")
	b := createTestIssue(t, pool, slug, ident, actor, "B")

	// Strict vocabulary: trimmed exact match, no case folding. "blocked_by"
	// is deliberately NOT a kind — the reverse relationship is the same
	// edge addressed from the other side (issue_id/target_issue_id
	// swapped), so it can never be a separate kind.
	for _, kind := range []string{"parent_of", "BLOCKS", "blocked_by", "start_before", "whatever"} {
		if _, err := CreateIssueLink(ctx, pool, slug, ident, a.ID, actor, b.ID, kind); !errors.Is(err, ErrInvalidIssueLink) {
			t.Fatalf("kind %q: err = %v, want ErrInvalidIssueLink", kind, err)
		}
	}
	// Accepted vocabulary: the first create succeeds; every repeat — any
	// valid kind — is a conflict (the kind is accepted, the edge exists).
	if _, err := CreateIssueLink(ctx, pool, slug, ident, a.ID, actor, b.ID, "blocks"); err != nil {
		t.Fatalf("first create: %v", err)
	}
	for _, kind := range []string{"blocks", "relates_to", "duplicates"} {
		if _, err := CreateIssueLink(ctx, pool, slug, ident, a.ID, actor, b.ID, kind); !errors.Is(err, ErrIssueLinkConflict) {
			t.Fatalf("kind %q: err = %v, want ErrIssueLinkConflict (dup, i.e. kind accepted)", kind, err)
		}
	}
}

func TestIssueLinkGuestReadOnly(t *testing.T) {
	ctx, pool, slug, ident, actor := setupIssueLinkTest(t)
	guest := createTestUser(t, pool, uniqueTestEmail("link-guest"))
	addTestMember(t, pool, slug, actor, guest, RoleGuest)

	a := createTestIssue(t, pool, slug, ident, actor, "A")
	b := createTestIssue(t, pool, slug, ident, actor, "B")
	if _, err := CreateIssueLink(ctx, pool, slug, ident, a.ID, actor, b.ID, "blocks"); err != nil {
		t.Fatalf("CreateIssueLink: %v", err)
	}

	// Guests may read links.
	if _, err := ListIssueLinks(ctx, pool, slug, ident, a.ID, guest); err != nil {
		t.Fatalf("guest ListIssueLinks: %v", err)
	}
	// Guests may not create.
	if _, err := CreateIssueLink(ctx, pool, slug, ident, b.ID, guest, a.ID, "blocks"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest create err = %v, want ErrForbidden", err)
	}
	// Guests may not delete.
	l, err := ListIssueLinks(ctx, pool, slug, ident, a.ID, actor)
	if err != nil {
		t.Fatalf("ListIssueLinks: %v", err)
	}
	if err := DeleteIssueLink(ctx, pool, slug, ident, a.ID, guest, l[0].ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest delete err = %v, want ErrForbidden", err)
	}
}

func TestIssueLinkDelete(t *testing.T) {
	ctx, pool, slug, ident, actor := setupIssueLinkTest(t)
	a := createTestIssue(t, pool, slug, ident, actor, "A")
	b := createTestIssue(t, pool, slug, ident, actor, "B")

	l, err := CreateIssueLink(ctx, pool, slug, ident, a.ID, actor, b.ID, "blocks")
	if err != nil {
		t.Fatalf("CreateIssueLink: %v", err)
	}
	if err := DeleteIssueLink(ctx, pool, slug, ident, a.ID, actor, l.ID); err != nil {
		t.Fatalf("DeleteIssueLink: %v", err)
	}
	links, err := ListIssueLinks(ctx, pool, slug, ident, a.ID, actor)
	if err != nil {
		t.Fatalf("ListIssueLinks: %v", err)
	}
	if len(links) != 0 {
		t.Fatalf("links after delete = %+v", links)
	}

	// Deleting again is 404.
	if err := DeleteIssueLink(ctx, pool, slug, ident, a.ID, actor, l.ID); !errors.Is(err, ErrIssueLinkNotFound) {
		t.Fatalf("re-delete err = %v, want ErrIssueLinkNotFound", err)
	}
	// A link id that belongs to another issue is NOT visible from here
	// (scope enforcement), even though it exists.
	c := createTestIssue(t, pool, slug, ident, actor, "C")
	l2, err := CreateIssueLink(ctx, pool, slug, ident, c.ID, actor, b.ID, "blocks")
	if err != nil {
		t.Fatalf("CreateIssueLink: %v", err)
	}
	if err := DeleteIssueLink(ctx, pool, slug, ident, a.ID, actor, l2.ID); !errors.Is(err, ErrIssueLinkNotFound) {
		t.Fatalf("cross-issue delete err = %v, want ErrIssueLinkNotFound", err)
	}
	// An incoming edge IS deletable from the target's route.
	l3, err := CreateIssueLink(ctx, pool, slug, ident, b.ID, actor, a.ID, "blocks")
	if err != nil {
		t.Fatalf("CreateIssueLink b->a: %v", err)
	}
	if err := DeleteIssueLink(ctx, pool, slug, ident, a.ID, actor, l3.ID); err != nil {
		t.Fatalf("incoming delete from target route: %v", err)
	}
	// But NOT from an unrelated issue's route.
	l4, err := CreateIssueLink(ctx, pool, slug, ident, b.ID, actor, c.ID, "blocks")
	if err != nil {
		t.Fatalf("CreateIssueLink b->c: %v", err)
	}
	if err := DeleteIssueLink(ctx, pool, slug, ident, a.ID, actor, l4.ID); !errors.Is(err, ErrIssueLinkNotFound) {
		t.Fatalf("unrelated delete err = %v, want ErrIssueLinkNotFound", err)
	}
	// Malformed link id is 400, never a 500.
	if err := DeleteIssueLink(ctx, pool, slug, ident, a.ID, actor, "nope"); !errors.Is(err, ErrInvalidIssueLink) {
		t.Fatalf("bad id delete err = %v, want ErrInvalidIssueLink", err)
	}
}

func TestIssueLinksForIssuesBatch(t *testing.T) {
	ctx, pool, slug, ident, actor := setupIssueLinkTest(t)
	a := createTestIssue(t, pool, slug, ident, actor, "A")
	b := createTestIssue(t, pool, slug, ident, actor, "B")
	c := createTestIssue(t, pool, slug, ident, actor, "C")
	d := createTestIssue(t, pool, slug, ident, actor, "D (unlinked)")

	if _, err := CreateIssueLink(ctx, pool, slug, ident, a.ID, actor, b.ID, "blocks"); err != nil {
		t.Fatalf("link a->b: %v", err)
	}
	if _, err := CreateIssueLink(ctx, pool, slug, ident, c.ID, actor, a.ID, "relates_to"); err != nil {
		t.Fatalf("link c->a: %v", err)
	}

	// One call for the whole set: outgoing AND incoming edges of every
	// listed issue (the Gantt draws both), nothing for the unlinked one.
	got, err := ListIssueLinksForIssues(ctx, pool, slug, ident, actor, []string{a.ID, b.ID, c.ID, d.ID})
	if err != nil {
		t.Fatalf("ListIssueLinksForIssues: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d links, want 2: %+v", len(got), got)
	}
	seen := map[string]bool{}
	for _, l := range got {
		seen[l.IssueID+"->"+l.TargetIssueID] = true
	}
	if !seen[a.ID+"->"+b.ID] || !seen[c.ID+"->"+a.ID] {
		t.Fatalf("batch missing edges: %+v", got)
	}

	// Empty input → empty output, no query fireworks.
	got, err = ListIssueLinksForIssues(ctx, pool, slug, ident, actor, nil)
	if err != nil {
		t.Fatalf("empty batch: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("empty batch = %+v", got)
	}

	// The batch is project-scoped: a link in another project never leaks.
	ident2 := uniqueTestIdentifier()
	createTestProject(t, pool, slug, actor, "Other", ident2)
	x := createTestIssue(t, pool, slug, ident2, actor, "X")
	y := createTestIssue(t, pool, slug, ident2, actor, "Y")
	if _, err := CreateIssueLink(ctx, pool, slug, ident2, x.ID, actor, y.ID, "blocks"); err != nil {
		t.Fatalf("foreign link: %v", err)
	}
	got, err = ListIssueLinksForIssues(ctx, pool, slug, ident, actor, []string{x.ID, y.ID})
	if err != nil {
		t.Fatalf("foreign batch: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("cross-project leak = %+v", got)
	}

	// Malformed id → 400-class sentinel, never a 500 from ANY($2::uuid[]).
	if _, err := ListIssueLinksForIssues(ctx, pool, slug, ident, actor, []string{"nope"}); !errors.Is(err, ErrInvalidIssueLink) {
		t.Fatalf("bad id batch err = %v, want ErrInvalidIssueLink", err)
	}
}
