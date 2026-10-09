package service

// Duplicate detection on issue create (C8T7): FindSimilarIssues —
// pg_trgm similarity lookup over issue titles. Real test database,
// no skips. Reuses the harness from workspace_test.go (newTestPool,
// migrateTestDB, createTestUser, createTestWorkspace, createTestProject,
// createTestIssue, backlogStateID).

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// setupSimilarProject creates a user, workspace and project and returns
// the pool, slug, identifier and actor (owner) id.
func setupSimilarProject(t *testing.T, prefix string) (*pgxpool.Pool, string, string, string) {
	t.Helper()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	userID := createTestUser(t, pool, uniqueTestEmail(prefix))
	wsSlug := uniqueTestSlug(prefix)
	createTestWorkspace(t, pool, prefix+" Co", wsSlug, userID)
	identifier := uniqueTestIdentifier()
	createTestProject(t, pool, wsSlug, userID, prefix+" Proj", identifier)
	return pool, wsSlug, identifier, userID
}

func TestFindSimilarIssuesRanking(t *testing.T) {
	pool, slug, ident, actor := setupSimilarProject(t, "simrank")
	ctx := context.Background()

	// Closest paraphrase first, looser match later; unrelated excluded.
	createTestIssue(t, pool, slug, ident, actor, "Fix the login redirect loop")
	createTestIssue(t, pool, slug, ident, actor, "Login redirect loop fix")
	createTestIssue(t, pool, slug, ident, actor, "Completely unrelated billing report")

	got, err := FindSimilarIssues(ctx, pool, slug, ident, actor, "Fix login redirect loop")
	if err != nil {
		t.Fatalf("FindSimilarIssues: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2 (unrelated title excluded)", len(got))
	}
	// Most similar first.
	if got[0].Similarity < got[1].Similarity {
		t.Fatalf("not ranked most-similar-first: %v then %v", got[0].Similarity, got[1].Similarity)
	}
	for _, s := range got {
		if s.Similarity <= similarThreshold {
			t.Fatalf("result below threshold: %q sim=%v", s.Name, s.Similarity)
		}
		if s.DisplayID == "" || s.ID == "" {
			t.Fatalf("missing id/display_id: %+v", s)
		}
	}
}

func TestFindSimilarIssuesThreshold(t *testing.T) {
	pool, slug, ident, actor := setupSimilarProject(t, "simthresh")
	ctx := context.Background()

	createTestIssue(t, pool, slug, ident, actor, "Quarterly revenue forecast spreadsheet")

	// A query sharing almost no trigrams must return nothing, not a weak
	// guess: the threshold is a floor, not a suggestion.
	got, err := FindSimilarIssues(ctx, pool, slug, ident, actor, "Zebra xylophone quantum")
	if err != nil {
		t.Fatalf("FindSimilarIssues: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d results for a dissimilar query, want 0", len(got))
	}
}

func TestFindSimilarIssuesExcludesNonWorkingSet(t *testing.T) {
	pool, slug, ident, actor := setupSimilarProject(t, "simexcl")
	ctx := context.Background()

	live := createTestIssue(t, pool, slug, ident, actor, "Duplicate candidate login redirect")
	archived := createTestIssue(t, pool, slug, ident, actor, "Duplicate candidate login redirect archived copy")
	draft := createTestIssue(t, pool, slug, ident, actor, "Duplicate candidate login redirect draft copy")
	deleted := createTestIssue(t, pool, slug, ident, actor, "Duplicate candidate login redirect deleted copy")

	// Mark the copies: archived, draft, soft-deleted. The drafts flag is
	// set at creation for the draft; the rest via SQL keeps the test
	// independent of the PATCH surface.
	if _, err := pool.Exec(ctx, `UPDATE issues SET archived_at = now() WHERE id = $1::uuid`, archived.ID); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE issues SET is_draft = TRUE WHERE id = $1::uuid`, draft.ID); err != nil {
		t.Fatalf("draft: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE issues SET deleted_at = now() WHERE id = $1::uuid`, deleted.ID); err != nil {
		t.Fatalf("soft delete: %v", err)
	}

	got, err := FindSimilarIssues(ctx, pool, slug, ident, actor, "Duplicate candidate login redirect")
	if err != nil {
		t.Fatalf("FindSimilarIssues: %v", err)
	}
	if len(got) != 1 || got[0].ID != live.ID {
		t.Fatalf("got %d results, want exactly the live issue %s", len(got), live.ID)
	}
}

func TestFindSimilarIssuesScopedToProject(t *testing.T) {
	pool, slug, ident, actor := setupSimilarProject(t, "simscope")
	ctx := context.Background()

	createTestIssue(t, pool, slug, ident, actor, "Cross project duplicate login redirect")

	// Same workspace, different project: its issues must not leak in.
	otherIdent := uniqueTestIdentifier()
	createTestProject(t, pool, slug, actor, "Other Proj", otherIdent)
	createTestIssue(t, pool, slug, otherIdent, actor, "Cross project duplicate login redirect")

	got, err := FindSimilarIssues(ctx, pool, slug, ident, actor, "Cross project duplicate login redirect")
	if err != nil {
		t.Fatalf("FindSimilarIssues: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d results, want 1 (other project's issue leaked)", len(got))
	}
	if !strings.HasPrefix(got[0].DisplayID, ident+"-") {
		t.Fatalf("wrong project display id: %q", got[0].DisplayID)
	}
}

func TestFindSimilarIssuesLimit(t *testing.T) {
	pool, slug, ident, actor := setupSimilarProject(t, "simlimit")
	ctx := context.Background()

	// 8 near-identical titles; the endpoint caps at similarLimit.
	for i := 0; i < 8; i++ {
		createTestIssue(t, pool, slug, ident, actor, "Rate limit exceeded on checkout API")
	}
	got, err := FindSimilarIssues(ctx, pool, slug, ident, actor, "Rate limit exceeded on checkout API")
	if err != nil {
		t.Fatalf("FindSimilarIssues: %v", err)
	}
	if len(got) != similarLimit {
		t.Fatalf("got %d results, want cap of %d", len(got), similarLimit)
	}
}

func TestFindSimilarIssuesEmptyQuery(t *testing.T) {
	pool, slug, ident, actor := setupSimilarProject(t, "simempty")
	ctx := context.Background()

	for _, q := range []string{"", "   "} {
		if _, err := FindSimilarIssues(ctx, pool, slug, ident, actor, q); !errors.Is(err, ErrEmptySimilarQuery) {
			t.Fatalf("q=%q: got err %v, want ErrEmptySimilarQuery", q, err)
		}
	}
}

func TestFindSimilarIssuesLongQueryTruncated(t *testing.T) {
	pool, slug, ident, actor := setupSimilarProject(t, "simlong")
	ctx := context.Background()

	// 500-char query must not blow up: it is truncated to
	// similarQueryMaxLen and still returns (here: nothing similar).
	q := strings.Repeat("a", 500)
	if _, err := FindSimilarIssues(ctx, pool, slug, ident, actor, q); err != nil {
		t.Fatalf("long q: %v", err)
	}
}

func TestFindSimilarIssuesNonMember(t *testing.T) {
	pool, slug, ident, actor := setupSimilarProject(t, "simmem")
	ctx := context.Background()

	outsider := createTestUser(t, pool, uniqueTestEmail("simoutsider"))
	if _, err := FindSimilarIssues(ctx, pool, slug, ident, outsider, "anything at all here"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-member: got err %v, want ErrNotFound", err)
	}
	_ = actor
}

func TestFindSimilarIssuesPerf(t *testing.T) {
	pool, slug, ident, actor := setupSimilarProject(t, "simperf")
	ctx := context.Background()

	// Bulk-seed 1000 issues with one INSERT ... SELECT (sequence_ids from
	// 1000 up to avoid the fixture counter's low range). 1000 rows keeps
	// the per-row cost measurable against the 50ms budget on this shared
	// VM (a larger fixture just measures VM contention, not the query).
	var stateID string
	if err := pool.QueryRow(ctx,
		`SELECT s.id::text FROM states s JOIN projects p ON p.id = s.project_id WHERE p.identifier = $1 AND s."group" = 'backlog' LIMIT 1`,
		ident).Scan(&stateID); err != nil {
		t.Fatalf("backlog state: %v", err)
	}
	var projectID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM projects WHERE identifier = $1`, ident).Scan(&projectID); err != nil {
		t.Fatalf("project id: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO issues (project_id, state_id, sequence_id, name, created_by)
		SELECT $1::uuid, $2::uuid, 1000 + g,
		       'Perf seed issue number ' || g || ' about login redirect handling',
		       $3::uuid
		FROM generate_series(1, 1000) g`,
		projectID, stateID, actor); err != nil {
		t.Fatalf("bulk seed: %v", err)
	}
	// The shared glance_test database persists across runs: clean the
	// seeded rows up so this test always measures exactly 1000 rows and
	// never slows down future runs.
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(),
			`DELETE FROM issues WHERE project_id = $1::uuid AND sequence_id >= 1000`,
			projectID); err != nil {
			t.Errorf("perf seed cleanup: %v", err)
		}
	})

	// One warm-up (plan + page cache), then 5 timed iterations: this VM
	// is shared with other agents, so a single sample is noise-dominated.
	// The median filters contention spikes; the 50ms budget applies to
	// the steady-state query, which is what the budget is really about.
	if _, err := FindSimilarIssues(ctx, pool, slug, ident, actor, "login redirect handling problem"); err != nil {
		t.Fatalf("warm-up: %v", err)
	}
	durs := make([]time.Duration, 0, 5)
	var got []SimilarIssue
	for i := 0; i < 5; i++ {
		start := time.Now()
		var err error
		got, err = FindSimilarIssues(ctx, pool, slug, ident, actor, "login redirect handling problem")
		durs = append(durs, time.Since(start))
		if err != nil {
			t.Fatalf("FindSimilarIssues: %v", err)
		}
	}
	if len(got) == 0 {
		t.Fatal("expected matches among the seeded rows")
	}
	// Budget: 50ms median for 1000 rows on this VM. If the VM cannot make
	// this meaningful it is documented, not skipped — the assertion stays.
	// insertion sort of 5 samples, then the median
	for i := 1; i < len(durs); i++ {
		for j := i; j > 0 && durs[j] < durs[j-1]; j-- {
			durs[j], durs[j-1] = durs[j-1], durs[j]
		}
	}
	median := durs[len(durs)/2]
	if median >= 50*time.Millisecond {
		t.Fatalf("similar query median %v over 1000 rows, budget 50ms (samples: %v)", median, durs)
	}
	t.Logf("similar query over 1000 rows: median %v (%d results, samples %v)", median, len(got), durs)
}
