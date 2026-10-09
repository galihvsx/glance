package service

// ListMyWork tests (C6T3): assigned / created / watched filters, default
// filter, workspace isolation, and non-member 404. Real test database,
// no skips.

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func setupMyWork(t *testing.T, pool *pgxpool.Pool) (slug, ident, alice, bob string) {
	t.Helper()
	alice = createTestUser(t, pool, uniqueTestEmail("mywork-alice"))
	bob = createTestUser(t, pool, uniqueTestEmail("mywork-bob"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-mywork"), alice)
	addTestMember(t, pool, ws.Slug, alice, bob, RoleMember)
	p := createTestProject(t, pool, ws.Slug, alice, "Engineering", uniqueTestIdentifier())
	return ws.Slug, p.Identifier, alice, bob
}

func assignIssueTo(t *testing.T, pool *pgxpool.Pool, issueID, userID string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO issue_assignees (issue_id, user_id) VALUES ($1::uuid, $2::uuid) ON CONFLICT DO NOTHING`,
		issueID, userID); err != nil {
		t.Fatalf("assign: %v", err)
	}
}

func subscribeUser(t *testing.T, pool *pgxpool.Pool, issueID, userID string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO issue_subscribers (issue_id, user_id) VALUES ($1::uuid, $2::uuid) ON CONFLICT DO NOTHING`,
		issueID, userID); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
}

func TestListMyWorkFilters(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	slug, ident, alice, bob := setupMyWork(t, pool)

	// Alice creates an issue assigned to Bob; Bob subscribes; Alice creates
	// another unassigned issue.
	iss1 := createTestIssue(t, pool, slug, ident, alice, "bob's task")
	assignIssueTo(t, pool, iss1.ID, bob)
	subscribeUser(t, pool, iss1.ID, bob)
	iss2 := createTestIssue(t, pool, slug, ident, alice, "alice's own")

	// Bob / assigned → iss1 only.
	items, err := ListMyWork(ctx, pool, slug, bob, MyWorkAssigned, 100)
	if err != nil {
		t.Fatalf("assigned: %v", err)
	}
	if len(items) != 1 || items[0].ID != iss1.ID {
		t.Fatalf("assigned = %v, want [iss1]", ids(items))
	}
	if items[0].DisplayID == "" || items[0].StateName == "" || items[0].ProjectIdentifier == "" {
		t.Fatalf("item = %+v, want display/state/project populated", items[0])
	}

	// Bob / created → none (he created nothing).
	items, err = ListMyWork(ctx, pool, slug, bob, MyWorkCreated, 100)
	if err != nil {
		t.Fatalf("created: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("bob created = %d, want 0", len(items))
	}

	// Alice / created → both.
	items, err = ListMyWork(ctx, pool, slug, alice, MyWorkCreated, 100)
	if err != nil {
		t.Fatalf("alice created: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("alice created = %d, want 2", len(items))
	}

	// Bob / watched → iss1.
	items, err = ListMyWork(ctx, pool, slug, bob, MyWorkWatched, 100)
	if err != nil {
		t.Fatalf("watched: %v", err)
	}
	if len(items) != 1 || items[0].ID != iss1.ID {
		t.Fatalf("watched = %v, want [iss1]", ids(items))
	}

	// Default filter = assigned.
	items, err = ListMyWork(ctx, pool, slug, bob, "", 100)
	if err != nil {
		t.Fatalf("default: %v", err)
	}
	if len(items) != 1 || items[0].ID != iss1.ID {
		t.Fatalf("default = %v, want [iss1] (assigned)", ids(items))
	}
	_ = iss2

	// Unknown filter → 400-class.
	if _, err := ListMyWork(ctx, pool, slug, bob, "bogus", 100); !errors.Is(err, ErrInvalidMyWorkFilter) {
		t.Fatalf("bogus filter: err = %v, want ErrInvalidMyWorkFilter", err)
	}
}

// TestListMyWorkIsolation: another workspace's issues never leak; a
// non-member gets 404.
func TestListMyWorkIsolation(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	slug, ident, alice, bob := setupMyWork(t, pool)

	other := createTestUser(t, pool, uniqueTestEmail("mywork-other"))
	ws2 := createTestWorkspace(t, pool, "Other", uniqueTestSlug("other-mywork"), other)
	p2 := createTestProject(t, pool, ws2.Slug, other, "Secret", uniqueTestIdentifier())
	secret := createTestIssue(t, pool, ws2.Slug, p2.Identifier, other, "secret task")
	assignIssueTo(t, pool, secret.ID, other)

	// Bob is not a member of ws2 → 404 (tenancy, not empty list).
	if _, err := ListMyWork(ctx, pool, ws2.Slug, bob, MyWorkAssigned, 100); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-member: err = %v, want ErrNotFound", err)
	}

	// Alice's workspace view contains only ws1 issues.
	iss := createTestIssue(t, pool, slug, ident, alice, "visible")
	items, err := ListMyWork(ctx, pool, slug, alice, MyWorkCreated, 100)
	if err != nil {
		t.Fatalf("created: %v", err)
	}
	for _, it := range items {
		if it.ID == secret.ID {
			t.Fatal("cross-workspace leak: ws2 issue visible in ws1")
		}
	}
	found := false
	for _, it := range items {
		if it.ID == iss.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("own issue missing from created filter")
	}
}

func ids(items []MyWorkItem) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.ID)
	}
	return out
}
