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
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
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

func TestPatchTriStateClear(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	creator := createTestUser(t, pool, uniqueTestEmail("issue-clear"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-clear"), creator)
	p := createTestProject(t, pool, ws.Slug, creator, "Engineering", uniqueTestIdentifier())

	// Set up: a child issue with parent_id and both dates populated.
	parent := createTestIssue(t, pool, ws.Slug, p.Identifier, creator, "Parent")
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	target := time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)
	iss, err := CreateIssue(ctx, pool, ws.Slug, p.Identifier, creator, CreateIssueInput{
		Name: "Child", ParentID: &parent.ID, StartDate: &start, TargetDate: &target,
	})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if iss.ParentID == nil || *iss.ParentID != parent.ID {
		t.Fatalf("child parent_id = %v, want %s", iss.ParentID, parent.ID)
	}
	if iss.StartDate == nil || iss.TargetDate == nil {
		t.Fatal("child dates not populated")
	}

	before := countActivities(t, pool, iss.ID, "")

	// Explicit null (Set=true, Value=nil) clears all three — this is the
	// subtle path: a typed-nil *string / *time.Time rides as the SET arg,
	// and pgx must encode it as SQL NULL, not 500.
	updated, err := UpdateIssue(ctx, pool, ws.Slug, p.Identifier, iss.ID, creator, IssuePatch{
		ParentID:   PatchField[string]{Set: true},
		StartDate:  PatchField[time.Time]{Set: true},
		TargetDate: PatchField[time.Time]{Set: true},
	})
	if err != nil {
		t.Fatalf("UpdateIssue clear: %v", err)
	}
	if updated.ParentID != nil || updated.StartDate != nil || updated.TargetDate != nil {
		t.Fatalf("after clear: parent=%v start=%v target=%v, want all nil",
			updated.ParentID, updated.StartDate, updated.TargetDate)
	}

	// The columns must be SQL NULL in the database — not empty strings,
	// not zero dates, not JSON null.
	var parentNull, startNull, targetNull bool
	if err := pool.QueryRow(ctx,
		`SELECT parent_id IS NULL, start_date IS NULL, target_date IS NULL
		 FROM issues WHERE id = $1::uuid`,
		iss.ID).Scan(&parentNull, &startNull, &targetNull); err != nil {
		t.Fatalf("read cleared columns: %v", err)
	}
	if !parentNull || !startNull || !targetNull {
		t.Fatalf("SQL NULL flags = %v/%v/%v, want true/true/true", parentNull, startNull, targetNull)
	}

	// One activity row per cleared field (3 new rows on top of _created),
	// and new_value is SQL NULL (the field records "cleared").
	if got := countActivities(t, pool, iss.ID, ""); got-before != 3 {
		t.Fatalf("new activity rows = %d, want 3", got-before)
	}
	for _, field := range []string{"parent_id", "start_date", "target_date"} {
		var newVal any
		if err := pool.QueryRow(ctx,
			`SELECT new_value FROM issue_activities
			 WHERE issue_id = $1::uuid AND field = $2`,
			iss.ID, field).Scan(&newVal); err != nil {
			t.Fatalf("read %s activity: %v", field, err)
		}
		if newVal != nil {
			t.Fatalf("%s activity new_value = %v, want SQL NULL", field, newVal)
		}
	}

	// Clearing an already-clear field changes nothing and logs nothing
	// (before/after compare).
	before = countActivities(t, pool, iss.ID, "")
	if _, err := UpdateIssue(ctx, pool, ws.Slug, p.Identifier, iss.ID, creator, IssuePatch{
		ParentID: PatchField[string]{Set: true},
	}); err != nil {
		t.Fatalf("re-clear UpdateIssue: %v", err)
	}
	if got := countActivities(t, pool, iss.ID, ""); got != before {
		t.Fatalf("re-clear logged %d rows, want 0", got-before)
	}
}

func TestCreateIssueSelfHealsMissingSequenceRow(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	creator := createTestUser(t, pool, uniqueTestEmail("issue-heal"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-heal"), creator)
	p := createTestProject(t, pool, ws.Slug, creator, "Engineering", uniqueTestIdentifier())

	// Simulate a hand-inserted project or a failed backfill: drop the
	// counter row entirely.
	if _, err := pool.Exec(ctx,
		`DELETE FROM issue_sequences WHERE project_id = $1::uuid`, p.ID); err != nil {
		t.Fatalf("delete sequence row: %v", err)
	}

	// Create must self-heal via the upsert: sequence starts at 1 instead
	// of 500ing every create for the project.
	a := createTestIssue(t, pool, ws.Slug, p.Identifier, creator, "First")
	b := createTestIssue(t, pool, ws.Slug, p.Identifier, creator, "Second")
	if a.SequenceID != 1 || b.SequenceID != 2 {
		t.Fatalf("sequence_ids = %d,%d, want 1,2", a.SequenceID, b.SequenceID)
	}
}

// ---------- Task 15: issue list — filters, cursor pagination, delta sync ----------

type listTestSetup struct {
	pool  *pgxpool.Pool
	slug  string
	ident string
	actor string
}

func setupListTest(t *testing.T) listTestSetup {
	t.Helper()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	actor := createTestUser(t, pool, uniqueTestEmail("issue-list"))
	slug := uniqueTestSlug("list-ws")
	createTestWorkspace(t, pool, "List Co", slug, actor)
	ident := uniqueTestIdentifier()
	createTestProject(t, pool, slug, actor, "Eng", ident)
	return listTestSetup{pool: pool, slug: slug, ident: ident, actor: actor}
}

func listIssueIDs(items []IssueListItem) []string {
	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.ID
	}
	return ids
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestListCursorStable: inserting issues mid-pagination must not cause
// duplicates or skips. Uses -sequence_id (strictly increasing, no ties)
// for the mid-insert scenario so the test is deterministic.
func TestListCursorStable(t *testing.T) {
	s := setupListTest(t)
	ctx := context.Background()

	for _, n := range []string{"A", "B", "C", "D", "E"} {
		createTestIssue(t, s.pool, s.slug, s.ident, s.actor, "issue-"+n)
	}

	// Page 1: the two highest sequence_ids.
	r1, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor,
		ListIssuesInput{OrderBy: "-sequence_id", PerPage: 2})
	if err != nil {
		t.Fatalf("ListIssues page 1: %v", err)
	}
	if len(r1.Issues) != 2 || r1.NextCursor == "" {
		t.Fatalf("page 1: got %d issues next=%q, want 2 + cursor", len(r1.Issues), r1.NextCursor)
	}
	if r1.Issues[0].SequenceID != 5 || r1.Issues[1].SequenceID != 4 {
		t.Fatalf("page 1 sequences = %d,%d, want 5,4",
			r1.Issues[0].SequenceID, r1.Issues[1].SequenceID)
	}
	// Every listed issue carries the derived display ID (Task 14 carry).
	for _, it := range r1.Issues {
		if want := s.ident + "-" + strconv.Itoa(it.SequenceID); it.DisplayID != want {
			t.Fatalf("display_id = %q, want %q", it.DisplayID, want)
		}
	}

	// Insert mid-pagination: strictly newer, sorts before the cursor.
	for _, n := range []string{"F", "G", "H"} {
		createTestIssue(t, s.pool, s.slug, s.ident, s.actor, "issue-"+n)
	}

	var got []string
	got = append(got, listIssueIDs(r1.Issues)...)
	cursor := r1.NextCursor
	for {
		r, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor,
			ListIssuesInput{OrderBy: "-sequence_id", PerPage: 2, Cursor: cursor})
		if err != nil {
			t.Fatalf("ListIssues cursor page: %v", err)
		}
		got = append(got, listIssueIDs(r.Issues)...)
		if r.NextCursor == "" {
			break
		}
		cursor = r.NextCursor
	}
	if len(got) != 5 {
		t.Fatalf("paginated ids = %d, want 5 (no dupes/skips): %v", len(got), got)
	}
	// The 5 pre-insert issues, in order, must be exactly what pagination
	// produced (inserts 6-8 sort before the cursor, never disturb it).
	full, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor,
		ListIssuesInput{OrderBy: "-sequence_id", PerPage: 100})
	if err != nil {
		t.Fatalf("ListIssues full: %v", err)
	}
	wantAll := listIssueIDs(full.Issues)[3:]
	if !equalStrings(got, wantAll) {
		t.Fatalf("paginated = %v, want %v", got, wantAll)
	}
}

// queryCounter is a pgx.QueryTracer that counts executed queries — the
// N+1 detector for TestListNoNPlusOne.
type queryCounter struct{ n atomic.Int64 }

func (c *queryCounter) TraceQueryStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	return ctx
}

func (c *queryCounter) TraceQueryEnd(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryEndData) {
	c.n.Add(1)
}

func tracedTestPool(t *testing.T) (*pgxpool.Pool, *queryCounter) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Fatal("TEST_DATABASE_URL must be set; refusing to skip")
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	c := &queryCounter{}
	cfg.ConnConfig.Tracer = c
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewWithConfig: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, c
}

// TestListNoNPlusOne: the list must run a constant number of queries no
// matter how many issues exist — assignees/labels are aggregated in the
// single list query (Review Focus #3), never per-row lookups.
func TestListNoNPlusOne(t *testing.T) {
	s := setupListTest(t)
	ctx := context.Background()
	traced, counter := tracedTestPool(t)

	for i := 0; i < 50; i++ {
		createTestIssue(t, s.pool, s.slug, s.ident, s.actor, fmt.Sprintf("bulk-%d", i))
	}

	counter.n.Store(0)
	r1, err := ListIssues(ctx, traced, s.slug, s.ident, s.actor, ListIssuesInput{PerPage: 50})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	n1 := counter.n.Load()
	if len(r1.Issues) != 50 {
		t.Fatalf("got %d issues, want 50", len(r1.Issues))
	}
	// Assignees/labels come back as empty arrays here (no relations were
	// created for the bulk issues) — but the shape is stable and costs no
	// extra queries: aggregation happens inside the single list query.
	for _, it := range r1.Issues {
		if it.Assignees == nil || it.Labels == nil {
			t.Fatal("assignees/labels must be non-nil empty arrays")
		}
	}

	for i := 0; i < 50; i++ {
		createTestIssue(t, s.pool, s.slug, s.ident, s.actor, fmt.Sprintf("bulk2-%d", i))
	}

	counter.n.Store(0)
	r2, err := ListIssues(ctx, traced, s.slug, s.ident, s.actor, ListIssuesInput{PerPage: 50})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	n2 := counter.n.Load()
	if len(r2.Issues) != 50 || r2.NextCursor == "" {
		t.Fatalf("got %d issues next=%q, want 50 + cursor", len(r2.Issues), r2.NextCursor)
	}
	if n1 != n2 {
		t.Fatalf("query count grew with issue count: %d (50 issues) → %d (100 issues)", n1, n2)
	}
	if n1 > 5 {
		t.Fatalf("list took %d queries, want ≤ 5 (membership + project + single list query)", n1)
	}
	t.Logf("list query count constant at %d queries for 50 and 100 issues", n1)
}

// TestListOmitsDescriptionByDefault: sparse fieldsets — description is
// only selected when ?fields=description asks for it (spec §5).
func TestListOmitsDescriptionByDefault(t *testing.T) {
	s := setupListTest(t)
	ctx := context.Background()

	desc := json.RawMessage(`{"type":"doc","content":[]}`)
	if _, err := CreateIssue(ctx, s.pool, s.slug, s.ident, s.actor,
		CreateIssueInput{Name: "with desc", Description: desc}); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	r, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor, ListIssuesInput{})
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(r.Issues) != 1 {
		t.Fatalf("got %d issues, want 1", len(r.Issues))
	}
	if r.Issues[0].Description != nil {
		t.Fatalf("description present by default: %s", r.Issues[0].Description)
	}

	r2, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor,
		ListIssuesInput{Fields: []string{"description"}})
	if err != nil {
		t.Fatalf("ListIssues fields=description: %v", err)
	}
	if r2.Issues[0].Description == nil {
		t.Fatal("description missing when fields=description requested")
	}
}

// TestListDeltaSync: ?updated_after returns only issues touched since the
// timestamp — the polling primitive behind Task 24's SSE resync.
func TestListDeltaSync(t *testing.T) {
	s := setupListTest(t)
	ctx := context.Background()

	a := createTestIssue(t, s.pool, s.slug, s.ident, s.actor, "stale")
	marker := time.Now()
	b := createTestIssue(t, s.pool, s.slug, s.ident, s.actor, "fresh")

	r, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor,
		ListIssuesInput{UpdatedAfter: &marker})
	if err != nil {
		t.Fatalf("ListIssues updated_after: %v", err)
	}
	if len(r.Issues) != 1 || r.Issues[0].ID != b.ID {
		t.Fatalf("delta sync returned %d issues, want only the fresh one", len(r.Issues))
	}

	// Touching the stale issue (PATCH bumps updated_at) re-includes it.
	name := "stale-renamed"
	if _, err := UpdateIssue(ctx, s.pool, s.slug, s.ident, a.ID, s.actor,
		IssuePatch{Name: &name}); err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}
	r2, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor,
		ListIssuesInput{UpdatedAfter: &marker})
	if err != nil {
		t.Fatalf("ListIssues updated_after: %v", err)
	}
	if len(r2.Issues) != 2 {
		t.Fatalf("delta sync returned %d issues, want 2 after touch", len(r2.Issues))
	}
}

// TestListFilters: state, priority, and full-text q filters narrow the
// result set.
func TestListFilters(t *testing.T) {
	s := setupListTest(t)
	ctx := context.Background()

	backlog := backlogStateID(t, s.pool, s.slug, s.ident, s.actor)
	states, err := ListStates(ctx, s.pool, s.slug, s.ident, s.actor)
	if err != nil {
		t.Fatalf("ListStates: %v", err)
	}
	var todoID string
	for _, st := range states {
		if st.Group == "unstarted" {
			todoID = st.ID
		}
	}
	if todoID == "" {
		t.Fatal("no unstarted state seeded")
	}

	hi := 3
	createTestIssue(t, s.pool, s.slug, s.ident, s.actor, "alpha login bug")
	if _, err := CreateIssue(ctx, s.pool, s.slug, s.ident, s.actor,
		CreateIssueInput{Name: "beta payment flow", Priority: &hi, StateID: &todoID}); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	createTestIssue(t, s.pool, s.slug, s.ident, s.actor, "gamma docs")

	byState, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor,
		ListIssuesInput{State: todoID})
	if err != nil {
		t.Fatalf("ListIssues state: %v", err)
	}
	if len(byState.Issues) != 1 || byState.Issues[0].Name != "beta payment flow" {
		t.Fatalf("state filter: got %d issues, want the todo one", len(byState.Issues))
	}

	byPrio, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor,
		ListIssuesInput{Priorities: []int{hi}})
	if err != nil {
		t.Fatalf("ListIssues priority: %v", err)
	}
	if len(byPrio.Issues) != 1 || byPrio.Issues[0].Name != "beta payment flow" {
		t.Fatalf("priority filter: got %d issues, want the urgent one", len(byPrio.Issues))
	}

	byQ, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor,
		ListIssuesInput{Q: "payment"})
	if err != nil {
		t.Fatalf("ListIssues q: %v", err)
	}
	if len(byQ.Issues) != 1 || byQ.Issues[0].Name != "beta payment flow" {
		t.Fatalf("q filter: got %d issues, want the payment one", len(byQ.Issues))
	}

	// The default backlog state holds the other two.
	byBacklog, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor,
		ListIssuesInput{State: backlog})
	if err != nil {
		t.Fatalf("ListIssues backlog state: %v", err)
	}
	if len(byBacklog.Issues) != 2 {
		t.Fatalf("backlog filter: got %d issues, want 2", len(byBacklog.Issues))
	}
}

// TestListRelationFiltersEmptyBeforeTaxonomy was the Task 15 contract
// pinning assignee=/label=/cycle= to empty results before the junction
// tables existed. It is superseded by TestListRelationFilters in
// label_test.go (Task 16), which asserts the real filter behavior for
// assignee= and label=, and by TestListCycleFilter in cycle_test.go
// (Task 22), which asserts the real behavior for cycle=.

// TestListInvalidParams: bad order_by, bad cursor, bad priority, and a
// cursor minted for a different order are all 400-class errors.
func TestListInvalidParams(t *testing.T) {
	s := setupListTest(t)
	ctx := context.Background()

	createTestIssue(t, s.pool, s.slug, s.ident, s.actor, "one")
	createTestIssue(t, s.pool, s.slug, s.ident, s.actor, "two")

	if _, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor,
		ListIssuesInput{OrderBy: "bogus"}); !errors.Is(err, ErrInvalidOrderBy) {
		t.Fatalf("bad order_by: err = %v, want ErrInvalidOrderBy", err)
	}
	if _, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor,
		ListIssuesInput{Cursor: "not-base64!!!"}); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("bad cursor: err = %v, want ErrInvalidCursor", err)
	}
	badJSON := base64.RawURLEncoding.EncodeToString([]byte(`{"oops":true}`))
	if _, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor,
		ListIssuesInput{Cursor: badJSON}); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("malformed cursor: err = %v, want ErrInvalidCursor", err)
	}

	// A cursor minted for created_at must not be honored under -updated_at.
	r, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor,
		ListIssuesInput{OrderBy: "created_at", PerPage: 1})
	if err != nil {
		t.Fatalf("ListIssues created_at: %v", err)
	}
	if _, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor,
		ListIssuesInput{Cursor: r.NextCursor}); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("cross-order cursor: err = %v, want ErrInvalidCursor", err)
	}

	badPrio := 99
	if _, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor,
		ListIssuesInput{Priorities: []int{badPrio}}); !errors.Is(err, ErrInvalidListFilter) {
		t.Fatalf("bad priority: err = %v, want ErrInvalidListFilter", err)
	}
	if _, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor,
		ListIssuesInput{State: "not-a-uuid"}); !errors.Is(err, ErrInvalidListFilter) {
		t.Fatalf("bad state uuid: err = %v, want ErrInvalidListFilter", err)
	}
}

// TestListFiltersMulti: C2T5 multi-value filters (priorities, labels,
// assignees incl. "none", estimate points incl. "none"), date ranges, and
// the subscribed filter narrow the result set; malformed values 400.
func TestListFiltersMulti(t *testing.T) {
	s := setupListTest(t)
	ctx := context.Background()

	p1, p3, p4 := 1, 3, 4
	issA := createTestIssue(t, s.pool, s.slug, s.ident, s.actor, "multi alpha")
	if _, err := CreateIssue(ctx, s.pool, s.slug, s.ident, s.actor,
		CreateIssueInput{Name: "multi beta", Priority: &p3}); err != nil {
		t.Fatalf("CreateIssue beta: %v", err)
	}
	due := time.Now().Add(48 * time.Hour).Truncate(24 * time.Hour)
	if _, err := CreateIssue(ctx, s.pool, s.slug, s.ident, s.actor,
		CreateIssueInput{Name: "multi gamma", Priority: &p4, TargetDate: &due}); err != nil {
		t.Fatalf("CreateIssue gamma: %v", err)
	}
	if _, err := CreateIssue(ctx, s.pool, s.slug, s.ident, s.actor,
		CreateIssueInput{Name: "multi delta", Priority: &p1}); err != nil {
		t.Fatalf("CreateIssue delta: %v", err)
	}

	// Multi-priority: 3 and 4 of the four issues.
	byPrios, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor,
		ListIssuesInput{Priorities: []int{3, 4}})
	if err != nil {
		t.Fatalf("ListIssues priorities: %v", err)
	}
	if len(byPrios.Issues) != 2 {
		t.Fatalf("priorities filter: got %d issues, want 2", len(byPrios.Issues))
	}

	// Labels: two labels on different issues; multi filter OR-matches.
	lblBug, err := CreateLabel(ctx, s.pool, s.slug, s.ident, s.actor, LabelInput{Name: "bug"})
	if err != nil {
		t.Fatalf("CreateLabel bug: %v", err)
	}
	lblFeat, err := CreateLabel(ctx, s.pool, s.slug, s.ident, s.actor, LabelInput{Name: "feature"})
	if err != nil {
		t.Fatalf("CreateLabel feature: %v", err)
	}
	if err := AssignLabel(ctx, s.pool, s.slug, s.ident, issA.ID, lblBug.ID, s.actor); err != nil {
		t.Fatalf("AssignLabel: %v", err)
	}
	byLabels, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor,
		ListIssuesInput{Labels: []string{lblBug.ID, lblFeat.ID}})
	if err != nil {
		t.Fatalf("ListIssues labels: %v", err)
	}
	if len(byLabels.Issues) != 1 || byLabels.Issues[0].ID != issA.ID {
		t.Fatalf("labels filter: got %d issues, want only multi alpha", len(byLabels.Issues))
	}

	// Assignees: actor on alpha; "none" matches the rest.
	if err := AssignAssignee(ctx, s.pool, s.slug, s.ident, issA.ID, s.actor, s.actor); err != nil {
		t.Fatalf("AssignAssignee: %v", err)
	}
	byAssignee, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor,
		ListIssuesInput{Assignees: []string{s.actor}})
	if err != nil {
		t.Fatalf("ListIssues assignees: %v", err)
	}
	if len(byAssignee.Issues) != 1 || byAssignee.Issues[0].ID != issA.ID {
		t.Fatalf("assignee filter: got %d issues, want only multi alpha", len(byAssignee.Issues))
	}
	byNone, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor,
		ListIssuesInput{Assignees: []string{"none"}})
	if err != nil {
		t.Fatalf("ListIssues assignee=none: %v", err)
	}
	if len(byNone.Issues) != 3 {
		t.Fatalf("assignee=none filter: got %d issues, want 3", len(byNone.Issues))
	}

	// Estimates: scale with two points; point on beta, "none" matches rest.
	est, err := CreateEstimate(ctx, s.pool, s.slug, s.ident, s.actor, EstimateInput{
		Name: "Fibonacci",
		Points: []EstimatePointInput{
			{Key: "1", Value: 1},
			{Key: "3", Value: 3},
		},
	})
	if err != nil {
		t.Fatalf("CreateEstimate: %v", err)
	}
	if len(est.Points) != 2 {
		t.Fatalf("CreateEstimate: got %d points, want 2", len(est.Points))
	}
	betaID := ""
	for _, it := range byPrios.Issues {
		if it.Name == "multi beta" {
			betaID = it.ID
		}
	}
	if betaID == "" {
		t.Fatal("multi beta not found")
	}
	if _, err := UpdateIssue(ctx, s.pool, s.slug, s.ident, betaID, s.actor, IssuePatch{
		EstimatePointID: PatchField[string]{Set: true, Value: &est.Points[0].ID},
	}); err != nil {
		t.Fatalf("UpdateIssue estimate: %v", err)
	}
	byEst, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor,
		ListIssuesInput{EstimatePoints: []string{est.Points[0].ID}})
	if err != nil {
		t.Fatalf("ListIssues estimate: %v", err)
	}
	if len(byEst.Issues) != 1 || byEst.Issues[0].ID != betaID {
		t.Fatalf("estimate filter: got %d issues, want only multi beta", len(byEst.Issues))
	}
	byEstNone, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor,
		ListIssuesInput{EstimatePoints: []string{"none"}})
	if err != nil {
		t.Fatalf("ListIssues estimate=none: %v", err)
	}
	if len(byEstNone.Issues) != 3 {
		t.Fatalf("estimate=none filter: got %d issues, want 3", len(byEstNone.Issues))
	}

	// Date ranges: created_before far future matches all; created_after far
	// future matches none. Due range brackets gamma's target date.
	future := time.Now().Add(time.Hour)
	byCreated, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor,
		ListIssuesInput{CreatedBefore: &future})
	if err != nil {
		t.Fatalf("ListIssues created_before: %v", err)
	}
	if len(byCreated.Issues) != 4 {
		t.Fatalf("created_before filter: got %d issues, want 4", len(byCreated.Issues))
	}
	byCreatedNone, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor,
		ListIssuesInput{CreatedAfter: &future})
	if err != nil {
		t.Fatalf("ListIssues created_after: %v", err)
	}
	if len(byCreatedNone.Issues) != 0 {
		t.Fatalf("created_after filter: got %d issues, want 0", len(byCreatedNone.Issues))
	}
	dueAfter := due.Add(-24 * time.Hour)
	dueBefore := due.Add(24 * time.Hour)
	byDue, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor,
		ListIssuesInput{DueAfter: &dueAfter, DueBefore: &dueBefore})
	if err != nil {
		t.Fatalf("ListIssues due range: %v", err)
	}
	if len(byDue.Issues) != 1 || byDue.Issues[0].Name != "multi gamma" {
		t.Fatalf("due range filter: got %d issues, want only multi gamma", len(byDue.Issues))
	}

	// Subscribed: only the subscribed issue matches.
	if err := SubscribeIssue(ctx, s.pool, s.slug, s.ident, issA.ID, s.actor); err != nil {
		t.Fatalf("SubscribeIssue: %v", err)
	}
	bySub, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor,
		ListIssuesInput{Subscribed: true})
	if err != nil {
		t.Fatalf("ListIssues subscribed: %v", err)
	}
	if len(bySub.Issues) != 1 || bySub.Issues[0].ID != issA.ID {
		t.Fatalf("subscribed filter: got %d issues, want only multi alpha", len(bySub.Issues))
	}

	// Malformed values are ErrInvalidListFilter.
	for _, in := range []ListIssuesInput{
		{Labels: []string{"not-a-uuid"}},
		{Assignees: []string{"not-a-uuid"}},
		{EstimatePoints: []string{"not-a-uuid"}},
		{Priorities: []int{42}},
	} {
		if _, err := ListIssues(ctx, s.pool, s.slug, s.ident, s.actor, in); !errors.Is(err, ErrInvalidListFilter) {
			t.Fatalf("bad filter %+v: err = %v, want ErrInvalidListFilter", in, err)
		}
	}
}

// TestListIssuesArchivedFilter (C2T7): archived issues are excluded by
// default and included with Archived:true. (No archive action exists
// yet — archived_at is set directly, the way a future archive endpoint
// would.)
func TestListIssuesArchivedFilter(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	creator := createTestUser(t, pool, uniqueTestEmail("arch-filt"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("arch-filt"), creator)
	p := createTestProject(t, pool, ws.Slug, creator, "Engineering", uniqueTestIdentifier())

	live := createTestIssue(t, pool, ws.Slug, p.Identifier, creator, "live issue")
	arch := createTestIssue(t, pool, ws.Slug, p.Identifier, creator, "archived issue")
	if _, err := pool.Exec(ctx,
		`UPDATE issues SET archived_at = now() WHERE id = $1::uuid`, arch.ID); err != nil {
		t.Fatalf("archive: %v", err)
	}

	ids := func(res *ListIssuesResult) map[string]bool {
		m := map[string]bool{}
		for _, it := range res.Issues {
			m[it.ID] = true
		}
		return m
	}

	// Default: archived hidden.
	def, err := ListIssues(ctx, pool, ws.Slug, p.Identifier, creator, ListIssuesInput{})
	if err != nil {
		t.Fatalf("ListIssues default: %v", err)
	}
	dm := ids(def)
	if !dm[live.ID] {
		t.Errorf("default list missing live issue")
	}
	if dm[arch.ID] {
		t.Errorf("default list leaked archived issue")
	}

	// Archived:true: both visible.
	all, err := ListIssues(ctx, pool, ws.Slug, p.Identifier, creator, ListIssuesInput{Archived: true})
	if err != nil {
		t.Fatalf("ListIssues archived: %v", err)
	}
	am := ids(all)
	if !am[live.ID] || !am[arch.ID] {
		t.Errorf("archived list missing rows: live=%v archived=%v", am[live.ID], am[arch.ID])
	}
	// The archived row still carries its archived_at.
	for _, it := range all.Issues {
		if it.ID == arch.ID && it.ArchivedAt == nil {
			t.Errorf("archived row lost its archived_at")
		}
	}
}
