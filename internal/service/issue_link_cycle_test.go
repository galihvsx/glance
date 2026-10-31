package service

// Blocks-cycle guard (C94T0): creating a 'blocks' edge that closes a
// loop is rejected with IssueLinkCycleError carrying the full cycle
// path as display IDs; non-ordering kinds, diamonds without a loop,
// and paths broken by an archived or deleted intermediary stay allowed.

import (
	"errors"
	"testing"
)

// cycleOf asserts err is the C94T0 cycle rejection and returns the
// typed error carrying the display-ID path.
func cycleOf(t *testing.T, err error) *IssueLinkCycleError {
	t.Helper()
	if !errors.Is(err, ErrIssueLinkCycle) {
		t.Fatalf("err = %v, want ErrIssueLinkCycle", err)
	}
	var ce *IssueLinkCycleError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %T, want *IssueLinkCycleError", err)
	}
	return ce
}

func TestIssueLinkCycleTwoCycleRejected(t *testing.T) {
	ctx, pool, slug, ident, actor := setupIssueLinkTest(t)
	a := createTestIssue(t, pool, slug, ident, actor, "A")
	b := createTestIssue(t, pool, slug, ident, actor, "B")

	if _, err := CreateIssueLink(ctx, pool, slug, ident, a.ID, actor, b.ID, "blocks"); err != nil {
		t.Fatalf("link a->b: %v", err)
	}
	// Closing edge B→A: the guard walks FROM target A and reaches
	// source B via the existing A→B edge, so this closes A→B→A.
	_, err := CreateIssueLink(ctx, pool, slug, ident, b.ID, actor, a.ID, "blocks")
	ce := cycleOf(t, err)
	want := []string{b.DisplayID, a.DisplayID, b.DisplayID}
	if len(ce.Path) != len(want) {
		t.Fatalf("path = %v, want %v", ce.Path, want)
	}
	for i := range want {
		if ce.Path[i] != want[i] {
			t.Fatalf("path = %v, want %v", ce.Path, want)
		}
	}
	// The rejection wrote nothing: only the original edge remains.
	links, err := ListIssueLinks(ctx, pool, slug, ident, a.ID, actor)
	if err != nil {
		t.Fatalf("ListIssueLinks: %v", err)
	}
	if len(links) != 1 {
		t.Fatalf("links after rejected close = %+v, want 1", links)
	}
}

func TestIssueLinkCycleThreeCycleRejected(t *testing.T) {
	ctx, pool, slug, ident, actor := setupIssueLinkTest(t)
	a := createTestIssue(t, pool, slug, ident, actor, "A")
	b := createTestIssue(t, pool, slug, ident, actor, "B")
	c := createTestIssue(t, pool, slug, ident, actor, "C")

	for _, e := range [][2]string{{a.ID, b.ID}, {b.ID, c.ID}} {
		if _, err := CreateIssueLink(ctx, pool, slug, ident, e[0], actor, e[1], "blocks"); err != nil {
			t.Fatalf("link %s->%s: %v", e[0], e[1], err)
		}
	}
	// C→A closes A→B→C→A: walk from target A reaches source C.
	_, err := CreateIssueLink(ctx, pool, slug, ident, c.ID, actor, a.ID, "blocks")
	ce := cycleOf(t, err)
	want := []string{c.DisplayID, a.DisplayID, b.DisplayID, c.DisplayID}
	if len(ce.Path) != len(want) {
		t.Fatalf("path = %v, want %v", ce.Path, want)
	}
	for i := range want {
		if ce.Path[i] != want[i] {
			t.Fatalf("path = %v, want %v", ce.Path, want)
		}
	}
}

func TestIssueLinkCycleEmptyKindDefaultsToBlocks(t *testing.T) {
	ctx, pool, slug, ident, actor := setupIssueLinkTest(t)
	a := createTestIssue(t, pool, slug, ident, actor, "A")
	b := createTestIssue(t, pool, slug, ident, actor, "B")

	if _, err := CreateIssueLink(ctx, pool, slug, ident, a.ID, actor, b.ID, "blocks"); err != nil {
		t.Fatalf("link a->b: %v", err)
	}
	// Empty kind defaults to 'blocks', so the guard still fires.
	_, err := CreateIssueLink(ctx, pool, slug, ident, b.ID, actor, a.ID, "")
	cycleOf(t, err)
}

func TestIssueLinkCycleDiamondLoopRejected(t *testing.T) {
	ctx, pool, slug, ident, actor := setupIssueLinkTest(t)
	a := createTestIssue(t, pool, slug, ident, actor, "A")
	b := createTestIssue(t, pool, slug, ident, actor, "B")
	c := createTestIssue(t, pool, slug, ident, actor, "C")
	d := createTestIssue(t, pool, slug, ident, actor, "D")

	for _, e := range [][2]string{{a.ID, b.ID}, {a.ID, c.ID}, {b.ID, d.ID}, {c.ID, d.ID}} {
		if _, err := CreateIssueLink(ctx, pool, slug, ident, e[0], actor, e[1], "blocks"); err != nil {
			t.Fatalf("diamond link: %v", err)
		}
	}
	// D→A closes a loop through the diamond (D is reachable from A via
	// either branch). BFS visits A→B first (creation order), so the
	// reported path goes through B.
	_, err := CreateIssueLink(ctx, pool, slug, ident, d.ID, actor, a.ID, "blocks")
	ce := cycleOf(t, err)
	want := []string{d.DisplayID, a.DisplayID, b.DisplayID, d.DisplayID}
	if len(ce.Path) != len(want) {
		t.Fatalf("path = %v, want %v", ce.Path, want)
	}
	for i := range want {
		if ce.Path[i] != want[i] {
			t.Fatalf("path = %v, want %v", ce.Path, want)
		}
	}
}

func TestIssueLinkDiamondWithoutLoopAllowed(t *testing.T) {
	ctx, pool, slug, ident, actor := setupIssueLinkTest(t)
	a := createTestIssue(t, pool, slug, ident, actor, "A")
	b := createTestIssue(t, pool, slug, ident, actor, "B")
	c := createTestIssue(t, pool, slug, ident, actor, "C")
	d := createTestIssue(t, pool, slug, ident, actor, "D")

	// Shared descendants are not loops: every diamond edge is accepted,
	// and a direct A→D edge is accepted too even though A already
	// reaches D through both branches.
	for _, e := range [][2]string{{a.ID, b.ID}, {a.ID, c.ID}, {b.ID, d.ID}, {c.ID, d.ID}, {a.ID, d.ID}} {
		if _, err := CreateIssueLink(ctx, pool, slug, ident, e[0], actor, e[1], "blocks"); err != nil {
			t.Fatalf("diamond link %v: %v", e, err)
		}
	}
}

func TestIssueLinkNonOrderingCyclesAllowed(t *testing.T) {
	ctx, pool, slug, ident, actor := setupIssueLinkTest(t)
	a := createTestIssue(t, pool, slug, ident, actor, "A")
	b := createTestIssue(t, pool, slug, ident, actor, "B")

	// Mutual relates_to is legitimate vocabulary, not a cycle.
	for _, e := range [][2]string{{a.ID, b.ID}, {b.ID, a.ID}} {
		if _, err := CreateIssueLink(ctx, pool, slug, ident, e[0], actor, e[1], "relates_to"); err != nil {
			t.Fatalf("relates_to link: %v", err)
		}
	}
	// Same for duplicates — but note the unique (issue_id,
	// target_issue_id) backstop: a second edge on the same ordered pair
	// is a duplicate regardless of kind, so use fresh issues.
	c := createTestIssue(t, pool, slug, ident, actor, "C")
	d := createTestIssue(t, pool, slug, ident, actor, "D")
	for _, e := range [][2]string{{c.ID, d.ID}, {d.ID, c.ID}} {
		if _, err := CreateIssueLink(ctx, pool, slug, ident, e[0], actor, e[1], "duplicates"); err != nil {
			t.Fatalf("duplicates link: %v", err)
		}
	}
	// Mixed: a blocks edge one way + relates_to back is not a blocks
	// cycle either.
	e := createTestIssue(t, pool, slug, ident, actor, "E")
	f := createTestIssue(t, pool, slug, ident, actor, "F")
	if _, err := CreateIssueLink(ctx, pool, slug, ident, e.ID, actor, f.ID, "blocks"); err != nil {
		t.Fatalf("blocks link: %v", err)
	}
	if _, err := CreateIssueLink(ctx, pool, slug, ident, f.ID, actor, e.ID, "relates_to"); err != nil {
		t.Fatalf("relates_to back edge: %v", err)
	}
}

func TestIssueLinkCycleArchivedIntermediaryBreaksPath(t *testing.T) {
	ctx, pool, slug, ident, actor := setupIssueLinkTest(t)
	a := createTestIssue(t, pool, slug, ident, actor, "A")
	b := createTestIssue(t, pool, slug, ident, actor, "B")
	m := createTestIssue(t, pool, slug, ident, actor, "M")

	// Path B→M→A would make A→B a cycle — until M is archived.
	if _, err := CreateIssueLink(ctx, pool, slug, ident, b.ID, actor, m.ID, "blocks"); err != nil {
		t.Fatalf("link b->m: %v", err)
	}
	if _, err := CreateIssueLink(ctx, pool, slug, ident, m.ID, actor, a.ID, "blocks"); err != nil {
		t.Fatalf("link m->a: %v", err)
	}
	if _, err := CreateIssueLink(ctx, pool, slug, ident, a.ID, actor, b.ID, "blocks"); !errors.Is(err, ErrIssueLinkCycle) {
		t.Fatalf("pre-archive close err = %v, want ErrIssueLinkCycle", err)
	}
	archived := true
	if _, err := UpdateIssue(ctx, pool, slug, ident, m.ID, actor, IssuePatch{Archived: &archived}); err != nil {
		t.Fatalf("archive intermediary: %v", err)
	}
	if _, err := CreateIssueLink(ctx, pool, slug, ident, a.ID, actor, b.ID, "blocks"); err != nil {
		t.Fatalf("close after archive: %v", err)
	}
}

func TestIssueLinkCycleDeletedIntermediaryBreaksPath(t *testing.T) {
	ctx, pool, slug, ident, actor := setupIssueLinkTest(t)
	a := createTestIssue(t, pool, slug, ident, actor, "A")
	b := createTestIssue(t, pool, slug, ident, actor, "B")
	m := createTestIssue(t, pool, slug, ident, actor, "M")

	if _, err := CreateIssueLink(ctx, pool, slug, ident, b.ID, actor, m.ID, "blocks"); err != nil {
		t.Fatalf("link b->m: %v", err)
	}
	if _, err := CreateIssueLink(ctx, pool, slug, ident, m.ID, actor, a.ID, "blocks"); err != nil {
		t.Fatalf("link m->a: %v", err)
	}
	if _, err := CreateIssueLink(ctx, pool, slug, ident, a.ID, actor, b.ID, "blocks"); !errors.Is(err, ErrIssueLinkCycle) {
		t.Fatalf("pre-delete close err = %v, want ErrIssueLinkCycle", err)
	}
	if err := DeleteIssue(ctx, pool, slug, ident, m.ID, actor); err != nil {
		t.Fatalf("delete intermediary: %v", err)
	}
	if _, err := CreateIssueLink(ctx, pool, slug, ident, a.ID, actor, b.ID, "blocks"); err != nil {
		t.Fatalf("close after delete: %v", err)
	}
}

func TestIssueLinkCycleSelfDuplicateCrossProjectUnchanged(t *testing.T) {
	ctx, pool, slug, ident, actor := setupIssueLinkTest(t)
	a := createTestIssue(t, pool, slug, ident, actor, "A")
	b := createTestIssue(t, pool, slug, ident, actor, "B")

	// Self-loop still ErrIssueLinkSelf, not the cycle error.
	if _, err := CreateIssueLink(ctx, pool, slug, ident, a.ID, actor, a.ID, "blocks"); !errors.Is(err, ErrIssueLinkSelf) {
		t.Fatalf("self err = %v, want ErrIssueLinkSelf", err)
	}
	// Duplicate still ErrIssueLinkConflict (the duplicate pre-check
	// runs before the guard, so a repeat edge keeps its old error even
	// in a legacy graph that already contains a cycle).
	if _, err := CreateIssueLink(ctx, pool, slug, ident, a.ID, actor, b.ID, "blocks"); err != nil {
		t.Fatalf("link a->b: %v", err)
	}
	if _, err := CreateIssueLink(ctx, pool, slug, ident, a.ID, actor, b.ID, "blocks"); !errors.Is(err, ErrIssueLinkConflict) {
		t.Fatalf("dup err = %v, want ErrIssueLinkConflict", err)
	}
	// Cross-project still ErrIssueLinkCrossProject.
	ident2 := uniqueTestIdentifier()
	createTestProject(t, pool, slug, actor, "Other", ident2)
	x := createTestIssue(t, pool, slug, ident2, actor, "X")
	if _, err := CreateIssueLink(ctx, pool, slug, ident, a.ID, actor, x.ID, "blocks"); !errors.Is(err, ErrIssueLinkCrossProject) {
		t.Fatalf("cross-project err = %v, want ErrIssueLinkCrossProject", err)
	}
}
