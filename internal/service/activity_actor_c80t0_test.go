package service

// C80T0: `actor` filter on the workspace activity feed
// (GetWorkspaceActivity's actorFilter param, ↔ ?actor= on GET
// /api/v1/workspaces/{slug}/activity). Rows carry only the actor's
// display name in the response, so the fixture pins distinct user
// names and asserts on them. Real test database, no skips.

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// setupActorActivity seeds a workspace with two members who each write
// audit rows, and returns their user ids. Timelines (newest last):
//
//	m1 "Member One": creates issueA (_created)
//	m2 "Member Two": creates issueB (_created), then bumps priority twice
func setupActorActivity(t *testing.T, pool *pgxpool.Pool) (ws, m1, m2 string) {
	t.Helper()
	ctx := context.Background()
	m1 = createTestUser(t, pool, uniqueTestEmail("actor-m1"))
	m2 = createTestUser(t, pool, uniqueTestEmail("actor-m2"))
	for id, name := range map[string]string{m1: "Member One", m2: "Member Two"} {
		if _, err := pool.Exec(ctx, `UPDATE users SET name = $1 WHERE id = $2::uuid`, name, id); err != nil {
			t.Fatalf("name %s: %v", id, err)
		}
	}
	ws = uniqueTestSlug("wsact-actor")
	createTestWorkspace(t, pool, "WS Actor", ws, m1)
	if err := UpsertMember(ctx, pool, ws, m1, m2, RoleMember); err != nil {
		t.Fatalf("add m2: %v", err)
	}
	ident := uniqueTestIdentifier()
	createTestProject(t, pool, ws, m1, "Eng", ident)
	createTestIssue(t, pool, ws, ident, m1, "M1 issue")
	b := createTestIssue(t, pool, ws, ident, m2, "M2 issue")
	for _, pri := range []int{2, 3} {
		pri := pri
		if _, err := UpdateIssue(ctx, pool, ws, ident, b.ID, m2, IssuePatch{Priority: &pri}); err != nil {
			t.Fatalf("bump priority: %v", err)
		}
	}
	return ws, m1, m2
}

// TestGetWorkspaceActivityActorFilter pins the actor filter: actor=m2
// returns only m2's rows (the issueB creation + both priority bumps),
// actor=m1 only m1's row, and no filter returns everything.
func TestGetWorkspaceActivityActorFilter(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	ws, m1, m2 := setupActorActivity(t, pool)

	actors := func(filter string) []string {
		page, err := GetWorkspaceActivity(ctx, pool, ws, m1, ActivityDefaultLimit, "", nil, filter)
		if err != nil {
			t.Fatalf("GetWorkspaceActivity actor=%q: %v", filter, err)
		}
		var out []string
		for _, e := range page.Entries {
			out = append(out, e.Actor)
		}
		return out
	}

	byM2 := actors(m2)
	if len(byM2) != 3 {
		t.Fatalf("actor=m2 rows = %d, want 3 (issueB _created + 2 priority bumps)", len(byM2))
	}
	for _, a := range byM2 {
		if a != "Member Two" {
			t.Errorf("actor=m2 row attributed to %q, want Member Two", a)
		}
	}

	byM1 := actors(m1)
	if len(byM1) != 1 || byM1[0] != "Member One" {
		t.Fatalf("actor=m1 rows = %v, want the single Member One row", byM1)
	}

	all := actors("")
	if len(all) != 4 {
		t.Fatalf("unfiltered rows = %d, want 4", len(all))
	}
}

// TestGetWorkspaceActivityActorFilterPagination pins that cursor
// pagination works under the actor filter: paging with limit=1
// collects exactly the actor's rows, newest first, with no skips or
// repeats, and exhausts cleanly.
func TestGetWorkspaceActivityActorFilterPagination(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	ws, m1, m2 := setupActorActivity(t, pool)

	var fields []string
	cursor := ""
	for {
		page, err := GetWorkspaceActivity(ctx, pool, ws, m1, 1, cursor, nil, m2)
		if err != nil {
			t.Fatalf("page: %v", err)
		}
		if len(page.Entries) != 1 {
			t.Fatalf("page entries = %d, want 1", len(page.Entries))
		}
		e := page.Entries[0]
		if e.Actor != "Member Two" {
			t.Fatalf("filtered page row attributed to %q, want Member Two", e.Actor)
		}
		fields = append(fields, e.Field)
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	// Newest first: priority, priority, _created.
	if len(fields) != 3 || fields[0] != "priority" || fields[1] != "priority" || fields[2] != "_created" {
		t.Fatalf("paged fields = %v, want [priority priority _created]", fields)
	}
}

// TestGetWorkspaceActivityActorInvalidUUID pins the malformed-UUID
// filter as ErrInvalidActivityActor (400 at the handler).
func TestGetWorkspaceActivityActorInvalidUUID(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	ws, m1, _ := setupActorActivity(t, pool)

	if _, err := GetWorkspaceActivity(ctx, pool, ws, m1, ActivityDefaultLimit, "", nil, "not-a-uuid"); !errors.Is(err, ErrInvalidActivityActor) {
		t.Fatalf("actor=garbage: err = %v, want ErrInvalidActivityActor", err)
	}
}

// TestGetWorkspaceActivityActorUnknownMember pins a well-formed UUID
// that matches no member of the workspace as ErrMemberNotFound (404 at
// the handler) — including a user who exists but belongs to another
// workspace (cross-workspace ids never leak into the feed).
func TestGetWorkspaceActivityActorUnknownMember(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	ws, m1, _ := setupActorActivity(t, pool)

	stranger := uuid.NewString()
	if _, err := GetWorkspaceActivity(ctx, pool, ws, m1, ActivityDefaultLimit, "", nil, stranger); !errors.Is(err, ErrMemberNotFound) {
		t.Fatalf("actor=unknown uuid: err = %v, want ErrMemberNotFound", err)
	}

	// A real user of a DIFFERENT workspace is still 404 on this feed.
	other := createTestUser(t, pool, uniqueTestEmail("actor-other-ws"))
	if _, err := GetWorkspaceActivity(ctx, pool, ws, m1, ActivityDefaultLimit, "", nil, other); !errors.Is(err, ErrMemberNotFound) {
		t.Fatalf("actor=cross-workspace user: err = %v, want ErrMemberNotFound", err)
	}
}

// TestGetWorkspaceActivityActorGuestForbidden pins that a guest caller
// with an actor filter still gets ErrForbidden (403): the caller's own
// role is checked before the filter target is resolved.
func TestGetWorkspaceActivityActorGuestForbidden(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	ws, m1, m2 := setupActorActivity(t, pool)

	guest := createTestUser(t, pool, uniqueTestEmail("actor-guest"))
	if err := UpsertMember(ctx, pool, ws, m1, guest, RoleGuest); err != nil {
		t.Fatalf("add guest: %v", err)
	}
	if _, err := GetWorkspaceActivity(ctx, pool, ws, guest, ActivityDefaultLimit, "", nil, m2); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest with actor filter: err = %v, want ErrForbidden", err)
	}
}
