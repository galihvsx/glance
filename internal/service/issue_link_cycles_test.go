package service

// Pre-existing blocks-cycle report (C94T1): service-level tests for
// ListIssueLinkCycles. Fixtures insert links DIRECTLY into issue_links
// (coordinator fixup on rebase over C94T0): the guard rejects NEW
// cycle-closing edges via CreateIssueLink, and stored cycles are
// exactly what the report must surface — so fixtures simulate
// pre-existing data, bypassing the guard the way a legacy row would.

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func linkCycle(t *testing.T, ctx context.Context, pool *pgxpool.Pool, slug, ident, actor, from, to, kind string) {
	t.Helper()
	// Direct INSERT, not CreateIssueLink: the C94T0 guard rejects
	// cycle-closing edges, and these fixtures ARE pre-existing cycles.
	if _, err := pool.Exec(ctx,
		`INSERT INTO issue_links (issue_id, target_issue_id, kind)
		 VALUES ($1::uuid, $2::uuid, $3)`, from, to, kind); err != nil {
		t.Fatalf("insert link %s -> %s (%s): %v", from, to, kind, err)
	}
}

func TestIssueLinkCyclesThreeCyclePlusDiamond(t *testing.T) {
	ctx, pool, slug, ident, actor := setupIssueLinkTest(t)
	a := createTestIssue(t, pool, slug, ident, actor, "A")
	b := createTestIssue(t, pool, slug, ident, actor, "B")
	c := createTestIssue(t, pool, slug, ident, actor, "C")
	// Clean diamond: d blocks e and f; e and f both block g. No loop.
	d := createTestIssue(t, pool, slug, ident, actor, "D")
	e := createTestIssue(t, pool, slug, ident, actor, "E")
	f := createTestIssue(t, pool, slug, ident, actor, "F")
	g := createTestIssue(t, pool, slug, ident, actor, "G")

	linkCycle(t, ctx, pool, slug, ident, actor, a.ID, b.ID, "blocks")
	linkCycle(t, ctx, pool, slug, ident, actor, b.ID, c.ID, "blocks")
	linkCycle(t, ctx, pool, slug, ident, actor, c.ID, a.ID, "blocks")
	linkCycle(t, ctx, pool, slug, ident, actor, d.ID, e.ID, "blocks")
	linkCycle(t, ctx, pool, slug, ident, actor, d.ID, f.ID, "blocks")
	linkCycle(t, ctx, pool, slug, ident, actor, e.ID, g.ID, "blocks")
	linkCycle(t, ctx, pool, slug, ident, actor, f.ID, g.ID, "blocks")

	cycles, err := ListIssueLinkCycles(ctx, pool, slug, ident, actor)
	if err != nil {
		t.Fatalf("ListIssueLinkCycles: %v", err)
	}
	want := [][]string{{a.DisplayID, b.DisplayID, c.DisplayID, a.DisplayID}}
	if !reflect.DeepEqual(cycles, want) {
		t.Fatalf("cycles = %v, want %v", cycles, want)
	}
}

func TestIssueLinkCyclesNonOrderingRingsIgnored(t *testing.T) {
	ctx, pool, slug, ident, actor := setupIssueLinkTest(t)
	a := createTestIssue(t, pool, slug, ident, actor, "A")
	b := createTestIssue(t, pool, slug, ident, actor, "B")
	c := createTestIssue(t, pool, slug, ident, actor, "C")
	d := createTestIssue(t, pool, slug, ident, actor, "D")
	e := createTestIssue(t, pool, slug, ident, actor, "E")

	// Mutual relates_to pair + duplicates ring: legitimate vocabulary,
	// never ordering cycles.
	linkCycle(t, ctx, pool, slug, ident, actor, a.ID, b.ID, "relates_to")
	linkCycle(t, ctx, pool, slug, ident, actor, b.ID, a.ID, "relates_to")
	linkCycle(t, ctx, pool, slug, ident, actor, c.ID, d.ID, "duplicates")
	linkCycle(t, ctx, pool, slug, ident, actor, d.ID, e.ID, "duplicates")
	linkCycle(t, ctx, pool, slug, ident, actor, e.ID, c.ID, "duplicates")

	cycles, err := ListIssueLinkCycles(ctx, pool, slug, ident, actor)
	if err != nil {
		t.Fatalf("ListIssueLinkCycles: %v", err)
	}
	if len(cycles) != 0 {
		t.Fatalf("cycles = %v, want empty", cycles)
	}
}

func TestIssueLinkCyclesArchivedNodeBreaksRing(t *testing.T) {
	ctx, pool, slug, ident, actor := setupIssueLinkTest(t)
	a := createTestIssue(t, pool, slug, ident, actor, "A")
	b := createTestIssue(t, pool, slug, ident, actor, "B")
	c := createTestIssue(t, pool, slug, ident, actor, "C")
	linkCycle(t, ctx, pool, slug, ident, actor, a.ID, b.ID, "blocks")
	linkCycle(t, ctx, pool, slug, ident, actor, b.ID, c.ID, "blocks")
	linkCycle(t, ctx, pool, slug, ident, actor, c.ID, a.ID, "blocks")

	// Archive one ring member: the live graph no longer has a cycle.
	if _, err := pool.Exec(ctx,
		`UPDATE issues SET archived_at = now() WHERE id = $1::uuid`, b.ID); err != nil {
		t.Fatalf("archive B: %v", err)
	}
	cycles, err := ListIssueLinkCycles(ctx, pool, slug, ident, actor)
	if err != nil {
		t.Fatalf("ListIssueLinkCycles: %v", err)
	}
	if len(cycles) != 0 {
		t.Fatalf("cycles = %v, want empty", cycles)
	}
}

func TestIssueLinkCyclesDeletedNodeBreaksRing(t *testing.T) {
	ctx, pool, slug, ident, actor := setupIssueLinkTest(t)
	a := createTestIssue(t, pool, slug, ident, actor, "A")
	b := createTestIssue(t, pool, slug, ident, actor, "B")
	c := createTestIssue(t, pool, slug, ident, actor, "C")
	linkCycle(t, ctx, pool, slug, ident, actor, a.ID, b.ID, "blocks")
	linkCycle(t, ctx, pool, slug, ident, actor, b.ID, c.ID, "blocks")
	linkCycle(t, ctx, pool, slug, ident, actor, c.ID, a.ID, "blocks")

	if _, err := pool.Exec(ctx,
		`UPDATE issues SET deleted_at = now() WHERE id = $1::uuid`, b.ID); err != nil {
		t.Fatalf("delete B: %v", err)
	}
	cycles, err := ListIssueLinkCycles(ctx, pool, slug, ident, actor)
	if err != nil {
		t.Fatalf("ListIssueLinkCycles: %v", err)
	}
	if len(cycles) != 0 {
		t.Fatalf("cycles = %v, want empty", cycles)
	}
}

func TestIssueLinkCyclesEmptyProject(t *testing.T) {
	ctx, pool, slug, ident, actor := setupIssueLinkTest(t)
	cycles, err := ListIssueLinkCycles(ctx, pool, slug, ident, actor)
	if err != nil {
		t.Fatalf("ListIssueLinkCycles: %v", err)
	}
	if cycles == nil || len(cycles) != 0 {
		t.Fatalf("cycles = %v (nil=%v), want empty non-nil slice", cycles, cycles == nil)
	}
}

func TestIssueLinkCyclesNonMember404(t *testing.T) {
	ctx, pool, slug, ident, actor := setupIssueLinkTest(t)
	a := createTestIssue(t, pool, slug, ident, actor, "A")
	b := createTestIssue(t, pool, slug, ident, actor, "B")
	linkCycle(t, ctx, pool, slug, ident, actor, a.ID, b.ID, "blocks")
	linkCycle(t, ctx, pool, slug, ident, actor, b.ID, a.ID, "blocks")

	outsider := createTestUser(t, pool, uniqueTestEmail("link-cycle-outsider"))
	if _, err := ListIssueLinkCycles(ctx, pool, slug, ident, outsider); !errors.Is(err, ErrNotFound) {
		t.Fatalf("outsider err = %v, want ErrNotFound", err)
	}
}
