package service

// Issues CRUD + atomic sequence counter tests (Task 14): sequence
// assignment (1,2), 20-goroutine concurrency (unique sequence_ids, no
// gaps/dupes — Review Focus #2), per-field activity rows on PATCH,
// default backlog state, role gates, soft delete, state validation.
// All tests run against the real test database — no skips. Reuses the
// harness from workspace_test.go (newTestPool, migrateTestDB,
// createTestUser, createTestWorkspace, uniqueTestEmail, uniqueTestSlug,
// createTestProject, uniqueTestIdentifier, strPtr, addTestMember).

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func intPtr(i int) *int { return &i }

func createTestIssue(t *testing.T, pool *pgxpool.Pool, wsSlug, identifier, actorID, name string) *Issue {
	t.Helper()
	iss, err := CreateIssue(context.Background(), pool, wsSlug, identifier, actorID, CreateIssueInput{Name: name})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	return iss
}

// backlogStateID returns the id of the project's backlog-group state.
func backlogStateID(t *testing.T, pool *pgxpool.Pool, wsSlug, identifier, actorID string) string {
	t.Helper()
	states, err := ListStates(context.Background(), pool, wsSlug, identifier, actorID)
	if err != nil {
		t.Fatalf("ListStates: %v", err)
	}
	for _, s := range states {
		if s.Group == "backlog" {
			return s.ID
		}
	}
	t.Fatal("no backlog state seeded")
	return ""
}

func countActivities(t *testing.T, pool *pgxpool.Pool, issueID, field string) int {
	t.Helper()
	var n int
	q := `SELECT count(*) FROM issue_activities WHERE issue_id = $1::uuid`
	args := []any{issueID}
	if field != "" {
		q += ` AND field = $2`
		args = append(args, field)
	}
	if err := pool.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatalf("countActivities: %v", err)
	}
	return n
}

func TestCreateAssignsSequence(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)

	creator := createTestUser(t, pool, uniqueTestEmail("issue-seq"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-seq"), creator)
	p := createTestProject(t, pool, ws.Slug, creator, "Engineering", uniqueTestIdentifier())

	a := createTestIssue(t, pool, ws.Slug, p.Identifier, creator, "First")
	b := createTestIssue(t, pool, ws.Slug, p.Identifier, creator, "Second")

	if a.SequenceID != 1 || b.SequenceID != 2 {
		t.Fatalf("sequence_ids = %d,%d, want 1,2", a.SequenceID, b.SequenceID)
	}
	if want := p.Identifier + "-1"; a.DisplayID != want {
		t.Fatalf("DisplayID = %q, want %q", a.DisplayID, want)
	}
	if want := p.Identifier + "-2"; b.DisplayID != want {
		t.Fatalf("DisplayID = %q, want %q", b.DisplayID, want)
	}
	if a.StateID != backlogStateID(t, pool, ws.Slug, p.Identifier, creator) {
		t.Fatalf("default state = %q, want the backlog-group state", a.StateID)
	}
	if n := countActivities(t, pool, a.ID, "_created"); n != 1 {
		t.Fatalf("_created activity rows = %d, want 1", n)
	}

	// Sequences are per-project: a second project starts at 1.
	p2 := createTestProject(t, pool, ws.Slug, creator, "Design", uniqueTestIdentifier())
	c := createTestIssue(t, pool, ws.Slug, p2.Identifier, creator, "Other project")
	if c.SequenceID != 1 {
		t.Fatalf("second project sequence_id = %d, want 1", c.SequenceID)
	}
	if want := p2.Identifier + "-1"; c.DisplayID != want {
		t.Fatalf("DisplayID = %q, want %q", c.DisplayID, want)
	}
}

func TestConcurrentCreateNoDuplicateSequence(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)

	creator := createTestUser(t, pool, uniqueTestEmail("issue-conc"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-conc"), creator)
	p := createTestProject(t, pool, ws.Slug, creator, "Engineering", uniqueTestIdentifier())

	const n = 20
	seqs := make([]int, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			iss, err := CreateIssue(context.Background(), pool, ws.Slug, p.Identifier, creator,
				CreateIssueInput{Name: fmt.Sprintf("race-%d", i)})
			if err != nil {
				errs[i] = err
				return
			}
			seqs[i] = iss.SequenceID
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: CreateIssue: %v", i, err)
		}
	}
	seen := map[int]bool{}
	for _, s := range seqs {
		if s < 1 || s > n {
			t.Fatalf("sequence_id %d out of range 1..%d", s, n)
		}
		if seen[s] {
			t.Fatalf("duplicate sequence_id %d", s)
		}
		seen[s] = true
	}
	if len(seen) != n {
		t.Fatalf("%d unique sequence_ids, want %d (gapless 1..%d)", len(seen), n, n)
	}
}

func TestPatchWritesActivityRows(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	creator := createTestUser(t, pool, uniqueTestEmail("issue-patch"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-patch"), creator)
	p := createTestProject(t, pool, ws.Slug, creator, "Engineering", uniqueTestIdentifier())
	iss := createTestIssue(t, pool, ws.Slug, p.Identifier, creator, "Original")

	before := countActivities(t, pool, iss.ID, "")

	updated, err := UpdateIssue(ctx, pool, ws.Slug, p.Identifier, iss.ID, creator, IssuePatch{
		Name:     strPtr("Renamed"),
		Priority: intPtr(3),
	})
	if err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}
	if updated.Name != "Renamed" || updated.Priority != 3 {
		t.Fatalf("patched issue = %+v, want name=Renamed priority=3", updated)
	}
	if got := countActivities(t, pool, iss.ID, ""); got-before != 2 {
		t.Fatalf("new activity rows = %d, want 2 (one per changed field)", got-before)
	}
	if n := countActivities(t, pool, iss.ID, "name"); n != 1 {
		t.Fatalf("name activity rows = %d, want 1", n)
	}
	if n := countActivities(t, pool, iss.ID, "priority"); n != 1 {
		t.Fatalf("priority activity rows = %d, want 1", n)
	}

	// Verify old/new values on the name row.
	var oldVal, newVal string
	if err := pool.QueryRow(ctx,
		`SELECT old_value::text, new_value::text FROM issue_activities
		 WHERE issue_id = $1::uuid AND field = 'name'`,
		iss.ID).Scan(&oldVal, &newVal); err != nil {
		t.Fatalf("read name activity: %v", err)
	}
	if oldVal != `"Original"` || newVal != `"Renamed"` {
		t.Fatalf("name activity old/new = %s/%s, want \"Original\"/\"Renamed\"", oldVal, newVal)
	}

	// Re-applying identical values logs nothing (compare before/after).
	before = countActivities(t, pool, iss.ID, "")
	if _, err := UpdateIssue(ctx, pool, ws.Slug, p.Identifier, iss.ID, creator, IssuePatch{
		Name:     strPtr("Renamed"),
		Priority: intPtr(3),
	}); err != nil {
		t.Fatalf("idempotent UpdateIssue: %v", err)
	}
	if got := countActivities(t, pool, iss.ID, ""); got != before {
		t.Fatalf("unchanged patch logged %d rows, want 0", got-before)
	}

	// Empty patch is a client error, not a no-op success.
	if _, err := UpdateIssue(ctx, pool, ws.Slug, p.Identifier, iss.ID, creator, IssuePatch{}); !errors.Is(err, ErrNothingToUpdate) {
		t.Fatalf("empty patch: err = %v, want ErrNothingToUpdate", err)
	}
}

func TestIssueRoleGates(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	admin := createTestUser(t, pool, uniqueTestEmail("issue-gate-admin"))
	guest := createTestUser(t, pool, uniqueTestEmail("issue-gate-guest"))
	outsider := createTestUser(t, pool, uniqueTestEmail("issue-gate-out"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-gate"), admin)
	p := createTestProject(t, pool, ws.Slug, admin, "Engineering", uniqueTestIdentifier())
	addTestMember(t, pool, ws.Slug, admin, guest, RoleGuest)

	// Guest cannot create.
	if _, err := CreateIssue(ctx, pool, ws.Slug, p.Identifier, guest, CreateIssueInput{Name: "x"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest create: err = %v, want ErrForbidden", err)
	}
	iss := createTestIssue(t, pool, ws.Slug, p.Identifier, admin, "Gated")

	// Guest CAN read.
	if _, err := GetIssue(ctx, pool, ws.Slug, p.Identifier, iss.ID, guest); err != nil {
		t.Fatalf("guest read: %v", err)
	}
	// Guest cannot update or delete.
	if _, err := UpdateIssue(ctx, pool, ws.Slug, p.Identifier, iss.ID, guest, IssuePatch{Name: strPtr("y")}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest update: err = %v, want ErrForbidden", err)
	}
	if err := DeleteIssue(ctx, pool, ws.Slug, p.Identifier, iss.ID, guest); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest delete: err = %v, want ErrForbidden", err)
	}
	// Non-member sees nothing (ErrNotFound, not ErrForbidden).
	if _, err := GetIssue(ctx, pool, ws.Slug, p.Identifier, iss.ID, outsider); !errors.Is(err, ErrNotFound) {
		t.Fatalf("outsider read: err = %v, want ErrNotFound", err)
	}
}

func TestDeleteIssueSoft(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	creator := createTestUser(t, pool, uniqueTestEmail("issue-del"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-del"), creator)
	p := createTestProject(t, pool, ws.Slug, creator, "Engineering", uniqueTestIdentifier())
	iss := createTestIssue(t, pool, ws.Slug, p.Identifier, creator, "Doomed")

	if err := DeleteIssue(ctx, pool, ws.Slug, p.Identifier, iss.ID, creator); err != nil {
		t.Fatalf("DeleteIssue: %v", err)
	}
	if _, err := GetIssue(ctx, pool, ws.Slug, p.Identifier, iss.ID, creator); !errors.Is(err, ErrIssueNotFound) {
		t.Fatalf("get after delete: err = %v, want ErrIssueNotFound", err)
	}
	if n := countActivities(t, pool, iss.ID, "_deleted"); n != 1 {
		t.Fatalf("_deleted activity rows = %d, want 1", n)
	}
	// Double delete is a 404, not a silent success.
	if err := DeleteIssue(ctx, pool, ws.Slug, p.Identifier, iss.ID, creator); !errors.Is(err, ErrIssueNotFound) {
		t.Fatalf("second delete: err = %v, want ErrIssueNotFound", err)
	}
}

func TestCreateIssueStateValidation(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	creator := createTestUser(t, pool, uniqueTestEmail("issue-state"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-state"), creator)
	p1 := createTestProject(t, pool, ws.Slug, creator, "Engineering", uniqueTestIdentifier())
	p2 := createTestProject(t, pool, ws.Slug, creator, "Design", uniqueTestIdentifier())

	// A state from another project is rejected.
	otherState := backlogStateID(t, pool, ws.Slug, p2.Identifier, creator)
	if _, err := CreateIssue(ctx, pool, ws.Slug, p1.Identifier, creator,
		CreateIssueInput{Name: "x", StateID: &otherState}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("foreign state: err = %v, want ErrInvalidState", err)
	}
	// A nonexistent state is rejected.
	bogus := "00000000-0000-0000-0000-000000000000"
	if _, err := CreateIssue(ctx, pool, ws.Slug, p1.Identifier, creator,
		CreateIssueInput{Name: "x", StateID: &bogus}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("bogus state: err = %v, want ErrInvalidState", err)
	}
	// An explicit valid state is honored.
	backlog := backlogStateID(t, pool, ws.Slug, p1.Identifier, creator)
	iss := createTestIssue(t, pool, ws.Slug, p1.Identifier, creator, "Explicit")
	if iss.StateID != backlog {
		t.Fatalf("state = %q, want backlog %q", iss.StateID, backlog)
	}
}

func TestGetIssueNotFoundDistinct(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	creator := createTestUser(t, pool, uniqueTestEmail("issue-404"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-404"), creator)
	p := createTestProject(t, pool, ws.Slug, creator, "Engineering", uniqueTestIdentifier())

	// Missing issue in an existing project the caller belongs to:
	// ErrIssueNotFound (not ErrNotFound, not ErrProjectNotFound).
	if _, err := GetIssue(ctx, pool, ws.Slug, p.Identifier, "00000000-0000-0000-0000-000000000000", creator); !errors.Is(err, ErrIssueNotFound) {
		t.Fatalf("missing issue: err = %v, want ErrIssueNotFound", err)
	}
	// Missing project stays ErrProjectNotFound.
	if _, err := GetIssue(ctx, pool, ws.Slug, "NOPE", "00000000-0000-0000-0000-000000000000", creator); !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("missing project: err = %v, want ErrProjectNotFound", err)
	}
}
