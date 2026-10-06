// Package perf verifies the v1 competitive performance budgets (Task 27):
// list p95 < 100ms, detail p95 < 300ms, search p95 < 200ms, measured at
// the service layer against a project with 500 seeded issues.
//
// Why the service layer: these endpoints are DB-bound — the service call
// is the query plus JSON aggregation, while the Echo routing +
// RequireAuth middleware above it is sub-millisecond (measured
// separately against the release binary; see docs/perf.md). Measuring
// here keeps the test deterministic and free of HTTP-client noise.
//
// All tests run against the real test database — no skips.
package perf

import (
	"context"
	"fmt"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"glance/internal/config"
	"glance/internal/service"
	"glance/internal/store"
	"glance/migrations"
)

const (
	// perfIssueCount is the seeded corpus size per the Task 27 brief.
	perfIssueCount = 500

	// Budgets (Task 27 brief). p95 over perfIters timed iterations.
	budgetListP95   = 100 * time.Millisecond
	budgetDetailP95 = 300 * time.Millisecond
	budgetSearchP95 = 200 * time.Millisecond

	perfIters  = 60
	perfWarmup = 5
)

type perfFixture struct {
	pool       *pgxpool.Pool
	wsSlug     string
	identifier string
	actorID    string
	issueIDs   []string
}

var (
	perfSetupOnce sync.Once
	perfFix       perfFixture
)

// mustPerfFixture seeds the corpus once per package run and hands every
// budget test the same fixture.
func mustPerfFixture(t *testing.T) perfFixture {
	t.Helper()
	perfSetupOnce.Do(func() { perfFix = seedPerfFixture(t) })
	return perfFix
}

func seedPerfFixture(t *testing.T) perfFixture {
	t.Helper()
	ctx := context.Background()

	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Fatal("TEST_DATABASE_URL must be set; refusing to skip")
	}
	pool, err := store.NewPool(ctx, &config.Config{DatabaseURL: url})
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	if err := store.Migrate(ctx, pool, migrations.FS); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	// Keys must be unique across runs: the test DB is never truncated.
	uniq := fmt.Sprintf("%d", os.Getpid())
	email := fmt.Sprintf("perf-%s@example.com", uniq)
	var actorID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email) VALUES ($1) RETURNING id::text`, email).Scan(&actorID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	ws, err := service.CreateWorkspace(ctx, pool, "Perf", "perf-"+uniq, actorID)
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	p, err := service.CreateProject(ctx, pool, ws.Slug, actorID, "Engineering", "PF"+uniq)
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// Seeded via the service layer (not raw SQL): the seed path is not
	// what we measure, and this exercises the real create path — sequence
	// upsert, activity row, search-vector maintenance — so the corpus
	// looks exactly like production data.
	ids := make([]string, 0, perfIssueCount)
	for i := 0; i < perfIssueCount; i++ {
		iss, err := service.CreateIssue(ctx, pool, ws.Slug, p.Identifier, actorID,
			service.CreateIssueInput{
				Name: fmt.Sprintf("perf item %03d login latency dashboard", i),
			})
		if err != nil {
			t.Fatalf("CreateIssue %d: %v", i, err)
		}
		ids = append(ids, iss.ID)
	}
	t.Logf("seeded %d issues in project %s", len(ids), p.Identifier)
	return perfFixture{pool: pool, wsSlug: ws.Slug, identifier: p.Identifier, actorID: actorID, issueIDs: ids}
}

// measureP50P95Max runs fn iters times and returns the sorted percentiles.
func measureP50P95Max(t *testing.T, iters int, fn func(i int) error) (p50, p95, max time.Duration) {
	t.Helper()
	durs := make([]time.Duration, 0, iters)
	for i := 0; i < iters; i++ {
		start := time.Now()
		if err := fn(i); err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
		durs = append(durs, time.Since(start))
	}
	sort.Slice(durs, func(i, j int) bool { return durs[i] < durs[j] })
	q := func(p float64) time.Duration { return durs[int(p*float64(len(durs)))-1] }
	return q(0.50), q(0.95), durs[len(durs)-1]
}

func warmList(t *testing.T, f perfFixture, in service.ListIssuesInput) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < perfWarmup; i++ {
		if _, err := service.ListIssues(ctx, f.pool, f.wsSlug, f.identifier, f.actorID, in); err != nil {
			t.Fatalf("warmup: %v", err)
		}
	}
}

// TestPerfListP95: default issue list (order -updated_at, 25/page) over
// 500 issues must answer p95 < 100ms.
func TestPerfListP95(t *testing.T) {
	f := mustPerfFixture(t)
	ctx := context.Background()
	in := service.ListIssuesInput{}
	warmList(t, f, in)

	p50, p95, max := measureP50P95Max(t, perfIters, func(i int) error {
		_, err := service.ListIssues(ctx, f.pool, f.wsSlug, f.identifier, f.actorID, in)
		return err
	})
	t.Logf("list: p50=%v p95=%v max=%v (iters=%d, issues=%d)", p50, p95, max, perfIters, perfIssueCount)
	if p95 > budgetListP95 {
		t.Fatalf("list p95 = %v, budget %v", p95, budgetListP95)
	}
}

// TestPerfDetailP95: single-issue detail (GetIssue: one row + json_agg
// assignees/labels + description) must answer p95 < 300ms.
func TestPerfDetailP95(t *testing.T) {
	f := mustPerfFixture(t)
	ctx := context.Background()
	for i := 0; i < perfWarmup; i++ {
		if _, err := service.GetIssue(ctx, f.pool, f.wsSlug, f.identifier, f.issueIDs[i], f.actorID); err != nil {
			t.Fatalf("warmup: %v", err)
		}
	}

	p50, p95, max := measureP50P95Max(t, perfIters, func(i int) error {
		// Cycle a handful of issues so the measurement isn't a single
		// hot buffer-cache line.
		id := f.issueIDs[i%10]
		_, err := service.GetIssue(ctx, f.pool, f.wsSlug, f.identifier, id, f.actorID)
		return err
	})
	t.Logf("detail: p50=%v p95=%v max=%v (iters=%d)", p50, p95, max, perfIters)
	if p95 > budgetDetailP95 {
		t.Fatalf("detail p95 = %v, budget %v", p95, budgetDetailP95)
	}
}

// TestPerfSearchP95: full-text search matching all 500 issues (worst
// case: every row matches the tsvector filter) must answer p95 < 200ms.
func TestPerfSearchP95(t *testing.T) {
	f := mustPerfFixture(t)
	ctx := context.Background()
	in := service.ListIssuesInput{Q: "latency"}
	warmList(t, f, in)

	var got int
	p50, p95, max := measureP50P95Max(t, perfIters, func(i int) error {
		res, err := service.ListIssues(ctx, f.pool, f.wsSlug, f.identifier, f.actorID, in)
		if err != nil {
			return err
		}
		got = len(res.Issues)
		return nil
	})
	t.Logf("search: p50=%v p95=%v max=%v (iters=%d, page_rows=%d)", p50, p95, max, perfIters, got)
	if got == 0 {
		t.Fatal("search matched 0 issues; fixture names must contain the query term")
	}
	if p95 > budgetSearchP95 {
		t.Fatalf("search p95 = %v, budget %v", p95, budgetSearchP95)
	}
}
