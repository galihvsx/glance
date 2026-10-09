package service

// State management tests (C6T2): create / update / delete project states
// with the group vocabulary, duplicate-name conflicts, in-use and
// last-state delete guards, and member-only enforcement. Real test
// database, no skips.

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func setupStateProject(t *testing.T, pool *pgxpool.Pool) (wsSlug, identifier, creator string) {
	t.Helper()
	creator = createTestUser(t, pool, uniqueTestEmail("state-mgmt"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-state"), creator)
	p := createTestProject(t, pool, ws.Slug, creator, "Engineering", uniqueTestIdentifier())
	return ws.Slug, p.Identifier, creator
}

// TestCreateState: happy path, sequence auto-append, and validation.
func TestCreateState(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	slug, ident, creator := setupStateProject(t, pool)

	s, err := CreateState(ctx, pool, slug, ident, creator,
		StateInput{Name: "Review", Group: "started", Color: "#a78bfa"})
	if err != nil {
		t.Fatalf("CreateState: %v", err)
	}
	if s.Name != "Review" || s.Group != "started" || s.Color != "#a78bfa" {
		t.Fatalf("state = %+v, want name=Review group=started", s)
	}
	// Appended after the seeded max (5 states × 10000 spacing).
	if s.Sequence <= 50000 {
		t.Fatalf("sequence = %d, want > 50000 (appended after seeds)", s.Sequence)
	}

	// Duplicate name → 409-class sentinel.
	if _, err := CreateState(ctx, pool, slug, ident, creator,
		StateInput{Name: "Review", Group: "backlog"}); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("duplicate name: err = %v, want ErrStateConflict", err)
	}
	// Invalid group → 400-class sentinel.
	if _, err := CreateState(ctx, pool, slug, ident, creator,
		StateInput{Name: "Nope", Group: "limbo"}); !errors.Is(err, ErrInvalidStateGroup) {
		t.Fatalf("bad group: err = %v, want ErrInvalidStateGroup", err)
	}
	// Empty name → 400-class sentinel.
	if _, err := CreateState(ctx, pool, slug, ident, creator,
		StateInput{Name: "  ", Group: "backlog"}); !errors.Is(err, ErrNameRequired) {
		t.Fatalf("empty name: err = %v, want ErrNameRequired", err)
	}
}

// TestUpdateState: rename / regroup / recolor / resequence + conflicts.
func TestUpdateState(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	slug, ident, creator := setupStateProject(t, pool)

	s, err := CreateState(ctx, pool, slug, ident, creator,
		StateInput{Name: "QA", Group: "started"})
	if err != nil {
		t.Fatalf("CreateState: %v", err)
	}
	newName := "Quality Review"
	newColor := "#10b981"
	updated, err := UpdateState(ctx, pool, slug, ident, s.ID, creator,
		StatePatch{Name: &newName, Color: &newColor, Sequence: intPtr(15000)})
	if err != nil {
		t.Fatalf("UpdateState: %v", err)
	}
	if updated.Name != "Quality Review" || updated.Color != "#10b981" || updated.Sequence != 15000 {
		t.Fatalf("updated = %+v, want renamed/recolored/resequenced", updated)
	}
	// Rename onto an existing state name → conflict.
	dup := "Todo"
	if _, err := UpdateState(ctx, pool, slug, ident, s.ID, creator,
		StatePatch{Name: &dup}); !errors.Is(err, ErrStateConflict) {
		t.Fatalf("rename onto existing: err = %v, want ErrStateConflict", err)
	}
	// Bad group → 400-class.
	bad := "limbo"
	if _, err := UpdateState(ctx, pool, slug, ident, s.ID, creator,
		StatePatch{Group: &bad}); !errors.Is(err, ErrInvalidStateGroup) {
		t.Fatalf("bad group: err = %v, want ErrInvalidStateGroup", err)
	}
	// Empty patch → nothing to update.
	if _, err := UpdateState(ctx, pool, slug, ident, s.ID, creator,
		StatePatch{}); !errors.Is(err, ErrNothingToUpdate) {
		t.Fatalf("empty patch: err = %v, want ErrNothingToUpdate", err)
	}
	// Unknown state → 404-class.
	if _, err := UpdateState(ctx, pool, slug, ident, "00000000-0000-0000-0000-000000000000", creator,
		StatePatch{Name: &newName}); !errors.Is(err, ErrStateNotFound) {
		t.Fatalf("unknown state: err = %v, want ErrStateNotFound", err)
	}
}

// TestDeleteState: guards — in-use 409 without reassign_to, move+delete
// with reassign_to (live AND soft-deleted refs move), unknown 404s.
func TestDeleteState(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	slug, ident, creator := setupStateProject(t, pool)

	s, err := CreateState(ctx, pool, slug, ident, creator,
		StateInput{Name: "Doomed", Group: "backlog"})
	if err != nil {
		t.Fatalf("CreateState: %v", err)
	}
	// Put an issue in the state via direct update (CreateIssue lands in
	// the backlog state; move it explicitly).
	iss := createTestIssue(t, pool, slug, ident, creator, "doomed issue")
	if _, err := pool.Exec(ctx,
		`UPDATE issues SET state_id = $1::uuid WHERE id = $2::uuid`, s.ID, iss.ID); err != nil {
		t.Fatalf("move issue: %v", err)
	}
	if err := DeleteState(ctx, pool, slug, ident, s.ID, creator, nil); !errors.Is(err, ErrStateInUse) {
		t.Fatalf("delete in-use state: err = %v, want ErrStateInUse", err)
	}
	// Reassign to another state → issues move (live and soft-deleted),
	// state deletes.
	target, err := CreateState(ctx, pool, slug, ident, creator,
		StateInput{Name: "Refuge", Group: "backlog"})
	if err != nil {
		t.Fatalf("CreateState refuge: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE issues SET deleted_at = now() WHERE id = $1::uuid`, iss.ID); err != nil {
		t.Fatalf("soft-delete issue: %v", err)
	}
	if err := DeleteState(ctx, pool, slug, ident, s.ID, creator, &target.ID); err != nil {
		t.Fatalf("delete with reassign: %v", err)
	}
	var gotState string
	if err := pool.QueryRow(ctx,
		`SELECT state_id::text FROM issues WHERE id = $1::uuid`, iss.ID).Scan(&gotState); err != nil {
		t.Fatalf("refetch issue: %v", err)
	}
	if gotState != target.ID {
		t.Fatalf("soft-deleted issue state = %s, want reassigned %s", gotState, target.ID)
	}
	// Unknown → 404-class.
	if err := DeleteState(ctx, pool, slug, ident, s.ID, creator, nil); !errors.Is(err, ErrStateNotFound) {
		t.Fatalf("delete missing: err = %v, want ErrStateNotFound", err)
	}
	// Bad reassign target → 400-class. Re-create a state with an issue.
	s2, err := CreateState(ctx, pool, slug, ident, creator,
		StateInput{Name: "Doomed2", Group: "backlog"})
	if err != nil {
		t.Fatalf("CreateState s2: %v", err)
	}
	iss2 := createTestIssue(t, pool, slug, ident, creator, "doomed issue 2")
	if _, err := pool.Exec(ctx,
		`UPDATE issues SET state_id = $1::uuid WHERE id = $2::uuid`, s2.ID, iss2.ID); err != nil {
		t.Fatalf("move issue 2: %v", err)
	}
	bogus := "00000000-0000-0000-0000-000000000000"
	if err := DeleteState(ctx, pool, slug, ident, s2.ID, creator, &bogus); !errors.Is(err, ErrInvalidReassign) {
		t.Fatalf("bogus reassign: err = %v, want ErrInvalidReassign", err)
	}
	self := s2.ID
	if err := DeleteState(ctx, pool, slug, ident, s2.ID, creator, &self); !errors.Is(err, ErrInvalidReassign) {
		t.Fatalf("self reassign: err = %v, want ErrInvalidReassign", err)
	}
}

// TestDeleteLastState: a project must keep at least one state.
func TestDeleteLastState(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	slug, ident, creator := setupStateProject(t, pool)

	states, err := ListStates(ctx, pool, slug, ident, creator)
	if err != nil {
		t.Fatalf("ListStates: %v", err)
	}
	// Delete all but one (no issues reference them — fresh project).
	for _, s := range states[1:] {
		if err := DeleteState(ctx, pool, slug, ident, s.ID, creator, nil); err != nil {
			t.Fatalf("DeleteState %q: %v", s.Name, err)
		}
	}
	if err := DeleteState(ctx, pool, slug, ident, states[0].ID, creator, nil); !errors.Is(err, ErrLastState) {
		t.Fatalf("delete last state: err = %v, want ErrLastState", err)
	}
}

// TestStateMutationsForbiddenForGuests: guests (role 5) read but cannot
// mutate.
func TestStateMutationsForbiddenForGuests(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	slug, ident, creator := setupStateProject(t, pool)

	guest := createTestUser(t, pool, uniqueTestEmail("state-guest"))
	addTestMember(t, pool, slug, creator, guest, RoleGuest)

	if _, err := CreateState(ctx, pool, slug, ident, guest,
		StateInput{Name: "Sneaky", Group: "backlog"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest create: err = %v, want ErrForbidden", err)
	}
	states, err := ListStates(ctx, pool, slug, ident, guest)
	if err != nil {
		t.Fatalf("guest list: %v", err)
	}
	if len(states) == 0 {
		t.Fatal("guest list: want seeded states, got none")
	}
	if err := DeleteState(ctx, pool, slug, ident, states[0].ID, guest, nil); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest delete: err = %v, want ErrForbidden", err)
	}
}
