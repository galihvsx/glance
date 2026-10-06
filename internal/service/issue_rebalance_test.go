package service

// RebalanceSortOrder tests (Task 23): re-spacing of sort_order values
// within one state (the kanban board's density escape hatch). All tests
// run against the real test database — no skips. Reuses the harness from
// workspace_test.go (newTestPool, migrateTestDB, createTestUser,
// createTestWorkspace, uniqueTestEmail, uniqueTestSlug, createTestProject,
// uniqueTestIdentifier, addTestMember) and issue_test.go (createTestIssue).

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func setIssueSortOrder(t *testing.T, pool *pgxpool.Pool, issueID string, v float64) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`UPDATE issues SET sort_order = $1 WHERE id = $2::uuid`, v, issueID); err != nil {
		t.Fatalf("setIssueSortOrder: %v", err)
	}
}

func issueSortOrders(t *testing.T, pool *pgxpool.Pool, stateID string) []float64 {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT sort_order FROM issues
		  WHERE state_id = $1::uuid AND deleted_at IS NULL
		  ORDER BY sort_order`,
		stateID)
	if err != nil {
		t.Fatalf("issueSortOrders: %v", err)
	}
	defer rows.Close()
	var out []float64
	for rows.Next() {
		var v float64
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("issueSortOrders scan: %v", err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("issueSortOrders rows: %v", err)
	}
	return out
}

func TestRebalanceRespaces(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)

	creator := createTestUser(t, pool, uniqueTestEmail("rebalance"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-reb"), creator)
	p := createTestProject(t, pool, ws.Slug, creator, "Engineering", uniqueTestIdentifier())

	// Three issues land in the default (backlog) state; crush their
	// sort_orders into a collapsed cluster the midpoint util can't split.
	a := createTestIssue(t, pool, ws.Slug, p.Identifier, creator, "A")
	b := createTestIssue(t, pool, ws.Slug, p.Identifier, creator, "B")
	c := createTestIssue(t, pool, ws.Slug, p.Identifier, creator, "C")
	setIssueSortOrder(t, pool, a.ID, 1.0000001)
	setIssueSortOrder(t, pool, b.ID, 1.0000002)
	setIssueSortOrder(t, pool, c.ID, 1.0000003)

	n, err := RebalanceSortOrder(context.Background(), pool, ws.Slug, p.Identifier, creator, a.StateID)
	if err != nil {
		t.Fatalf("RebalanceSortOrder: %v", err)
	}
	if n != 3 {
		t.Fatalf("rebalanced = %d, want 3", n)
	}

	got := issueSortOrders(t, pool, a.StateID)
	want := []float64{1024, 2048, 3072}
	if len(got) != len(want) {
		t.Fatalf("sort_orders = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sort_orders = %v, want %v", got, want)
		}
	}
}

func TestRebalanceRoleGates(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)

	creator := createTestUser(t, pool, uniqueTestEmail("rebalance-g"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-rebg"), creator)
	p := createTestProject(t, pool, ws.Slug, creator, "Engineering", uniqueTestIdentifier())
	a := createTestIssue(t, pool, ws.Slug, p.Identifier, creator, "A")

	guest := createTestUser(t, pool, uniqueTestEmail("rebalance-guest"))
	addTestMember(t, pool, ws.Slug, creator, guest, RoleGuest)
	if _, err := RebalanceSortOrder(context.Background(), pool, ws.Slug, p.Identifier, guest, a.StateID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest rebalance: err = %v, want ErrForbidden", err)
	}

	outsider := createTestUser(t, pool, uniqueTestEmail("rebalance-out"))
	if _, err := RebalanceSortOrder(context.Background(), pool, ws.Slug, p.Identifier, outsider, a.StateID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("outsider rebalance: err = %v, want ErrNotFound", err)
	}

	if _, err := RebalanceSortOrder(context.Background(), pool, ws.Slug, p.Identifier, creator, "not-a-uuid"); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("bad state rebalance: err = %v, want ErrInvalidState", err)
	}

	if _, err := RebalanceSortOrder(context.Background(), pool, ws.Slug, "NOPE", creator, a.StateID); !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("bad project rebalance: err = %v, want ErrProjectNotFound", err)
	}
}

func TestRebalanceEmptyState(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)

	creator := createTestUser(t, pool, uniqueTestEmail("rebalance-e"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-rebe"), creator)
	p := createTestProject(t, pool, ws.Slug, creator, "Engineering", uniqueTestIdentifier())

	// No issues in the backlog state: a no-op, not an error.
	states, err := ListStates(context.Background(), pool, ws.Slug, p.Identifier, creator)
	if err != nil {
		t.Fatalf("ListStates: %v", err)
	}
	n, err := RebalanceSortOrder(context.Background(), pool, ws.Slug, p.Identifier, creator, states[0].ID)
	if err != nil {
		t.Fatalf("RebalanceSortOrder: %v", err)
	}
	if n != 0 {
		t.Fatalf("rebalanced = %d, want 0", n)
	}
}
