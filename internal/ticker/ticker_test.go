package ticker

// Rollover ticker tests (Task 22): the clock is faked — RunOnce takes an
// explicit now, so no test ever sleeps.

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"glance/internal/config"
	"glance/internal/service"
	"glance/internal/store"
	migrations "glance/migrations"
)

var tickerTestSeq atomic.Int64

func tickerTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Fatal("TEST_DATABASE_URL must be set; refusing to skip")
	}
	pool, err := store.NewPool(context.Background(), &config.Config{DatabaseURL: url})
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := store.Migrate(context.Background(), pool, migrations.FS); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return pool
}

func tickerTestUser(t *testing.T, pool *pgxpool.Pool, email string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO users (email) VALUES ($1) RETURNING id::text`, email).Scan(&id); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return id
}

func tickerTestSetup(t *testing.T, prefix string) (context.Context, *pgxpool.Pool, string, string, string, string) {
	t.Helper()
	ctx := context.Background()
	pool := tickerTestPool(t)
	n := tickerTestSeq.Add(1)
	actor := tickerTestUser(t, pool, fmt.Sprintf("%s-%d-%d@example.com", prefix, os.Getpid(), n))
	slug := fmt.Sprintf("%s-ws-%d-%d", prefix, os.Getpid(), n)
	if _, err := service.CreateWorkspace(ctx, pool, "Ticker Co", slug, actor); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	ident := fmt.Sprintf("T%d", n%100000)
	proj, err := service.CreateProject(ctx, pool, slug, actor, "Eng", ident)
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	return ctx, pool, slug, ident, actor, proj.ID
}

// stateByGroup returns the project's default state id for a group.
func stateByGroup(t *testing.T, pool *pgxpool.Pool, projectID, group string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`SELECT id::text FROM states WHERE project_id = $1::uuid AND "group" = $2 ORDER BY sequence LIMIT 1`,
		projectID, group).Scan(&id); err != nil {
		t.Fatalf("state group %s: %v", group, err)
	}
	return id
}

func TestTickerCompletesEndedCycle(t *testing.T) {
	ctx, pool, slug, ident, actor, projectID := tickerTestSetup(t, "ticker-done")

	now := time.Now()
	ended := now.AddDate(0, 0, -1)
	c, err := service.CreateCycle(ctx, pool, slug, ident, actor, service.CycleInput{
		Name: "Ended", StartDate: ended.AddDate(0, 0, -14), EndDate: ended,
	})
	if err != nil {
		t.Fatalf("CreateCycle: %v", err)
	}

	done := stateByGroup(t, pool, projectID, "completed")
	started := stateByGroup(t, pool, projectID, "started")
	finished := mustCreateIssue(t, ctx, pool, slug, ident, actor, "finished")
	open := mustCreateIssue(t, ctx, pool, slug, ident, actor, "open")
	if _, err := service.UpdateIssue(ctx, pool, slug, ident, finished.ID, actor,
		service.IssuePatch{StateID: &done}); err != nil {
		t.Fatalf("finish issue: %v", err)
	}
	if _, err := service.UpdateIssue(ctx, pool, slug, ident, open.ID, actor,
		service.IssuePatch{StateID: &started}); err != nil {
		t.Fatalf("start issue: %v", err)
	}
	if err := service.AddCycleIssues(ctx, pool, slug, ident, actor, c.ID,
		[]string{finished.ID, open.ID}); err != nil {
		t.Fatalf("AddCycleIssues: %v", err)
	}

	// A queued next cycle receives the incomplete issue.
	next, err := service.CreateCycle(ctx, pool, slug, ident, actor, service.CycleInput{
		Name: "Next", StartDate: now.AddDate(0, 0, 1), EndDate: now.AddDate(0, 0, 14),
	})
	if err != nil {
		t.Fatalf("CreateCycle next: %v", err)
	}

	tk := &Ticker{Pool: pool}
	if err := tk.RunOnce(ctx, now); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	got, err := service.GetCycle(ctx, pool, slug, ident, actor, c.ID)
	if err != nil {
		t.Fatalf("GetCycle: %v", err)
	}
	if got.Status != "completed" {
		t.Fatalf("status = %q, want completed", got.Status)
	}
	// Snapshot frozen at completion: 1 completed, 1 started.
	if got.ProgressSnapshot["completed"] != 1 || got.ProgressSnapshot["started"] != 1 {
		t.Fatalf("snapshot = %+v, want completed=1 started=1", got.ProgressSnapshot)
	}

	// The finished issue stays in the completed cycle; the open one moved
	// to the next cycle.
	var inOld, inNew int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM cycle_issues WHERE cycle_id = $1::uuid AND issue_id = $2::uuid`,
		c.ID, open.ID).Scan(&inOld); err != nil {
		t.Fatalf("count old: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM cycle_issues WHERE cycle_id = $1::uuid AND issue_id = $2::uuid`,
		next.ID, open.ID).Scan(&inNew); err != nil {
		t.Fatalf("count new: %v", err)
	}
	if inOld != 0 || inNew != 1 {
		t.Fatalf("open issue: inOld=%d inNew=%d, want 0/1 (transferred)", inOld, inNew)
	}
	var fin int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM cycle_issues WHERE cycle_id = $1::uuid AND issue_id = $2::uuid`,
		c.ID, finished.ID).Scan(&fin); err != nil {
		t.Fatalf("count finished: %v", err)
	}
	if fin != 1 {
		t.Fatalf("finished issue left the completed cycle: count=%d", fin)
	}
}

func TestTickerDetachesAfterCloseInDays(t *testing.T) {
	ctx, pool, slug, ident, actor, _ := tickerTestSetup(t, "ticker-close")

	now := time.Now()
	ended := now.AddDate(0, 0, -10)
	c, err := service.CreateCycle(ctx, pool, slug, ident, actor, service.CycleInput{
		Name: "Old", StartDate: ended.AddDate(0, 0, -14), EndDate: ended,
	})
	if err != nil {
		t.Fatalf("CreateCycle: %v", err)
	}
	open := mustCreateIssue(t, ctx, pool, slug, ident, actor, "open")
	if err := service.AddCycleIssues(ctx, pool, slug, ident, actor, c.ID,
		[]string{open.ID}); err != nil {
		t.Fatalf("AddCycleIssues: %v", err)
	}

	// No next cycle queued. close_in_days is NULL on this project → the
	// documented default is 0: detach the incomplete issue at completion.
	tk := &Ticker{Pool: pool}
	if err := tk.RunOnce(ctx, now); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM cycle_issues WHERE cycle_id = $1::uuid`, c.ID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("cycle_issues = %d, want 0 (detached, close_in_days NULL = 0)", n)
	}

	// With close_in_days = 30 the incomplete issue survives in the
	// completed cycle until the grace period passes.
	ended2 := now.AddDate(0, 0, -5)
	c2, err := service.CreateCycle(ctx, pool, slug, ident, actor, service.CycleInput{
		Name: "Old2", StartDate: ended2.AddDate(0, 0, -14), EndDate: ended2,
	})
	if err != nil {
		t.Fatalf("CreateCycle Old2: %v", err)
	}
	open2 := mustCreateIssue(t, ctx, pool, slug, ident, actor, "open2")
	if err := service.AddCycleIssues(ctx, pool, slug, ident, actor, c2.ID,
		[]string{open2.ID}); err != nil {
		t.Fatalf("AddCycleIssues: %v", err)
	}
	closeIn := 30
	if _, err := service.UpdateProject(ctx, pool, slug, ident, actor,
		service.ProjectPatch{CloseInDays: &closeIn}); err != nil {
		t.Fatalf("set close_in_days: %v", err)
	}
	if err := tk.RunOnce(ctx, now); err != nil {
		t.Fatalf("RunOnce 2: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM cycle_issues WHERE cycle_id = $1::uuid`, c2.ID).Scan(&n); err != nil {
		t.Fatalf("count 2: %v", err)
	}
	if n != 1 {
		t.Fatalf("cycle_issues = %d, want 1 (grace period not yet passed)", n)
	}

	// The sweep detaches it once the grace passes — no next cycle was
	// ever queued.
	if err := tk.RunOnce(ctx, now.AddDate(0, 0, 31)); err != nil {
		t.Fatalf("RunOnce 3: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM cycle_issues WHERE cycle_id = $1::uuid`, c2.ID).Scan(&n); err != nil {
		t.Fatalf("count 3: %v", err)
	}
	if n != 0 {
		t.Fatalf("cycle_issues = %d, want 0 (swept after grace)", n)
	}
}

func TestTickerActivatesUpcoming(t *testing.T) {
	ctx, pool, slug, ident, actor, _ := tickerTestSetup(t, "ticker-act")

	now := time.Now()
	c, err := service.CreateCycle(ctx, pool, slug, ident, actor, service.CycleInput{
		Name: "Future", StartDate: now.AddDate(0, 0, 2), EndDate: now.AddDate(0, 0, 15),
	})
	if err != nil {
		t.Fatalf("CreateCycle: %v", err)
	}
	if c.Status != "upcoming" {
		t.Fatalf("status = %q, want upcoming", c.Status)
	}

	// Two days later the ticker flips it to current.
	tk := &Ticker{Pool: pool}
	if err := tk.RunOnce(ctx, now.AddDate(0, 0, 3)); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	got, err := service.GetCycle(ctx, pool, slug, ident, actor, c.ID)
	if err != nil {
		t.Fatalf("GetCycle: %v", err)
	}
	if got.Status != "current" {
		t.Fatalf("status = %q, want current", got.Status)
	}
}

// mustCreateIssue creates an issue via the exported service API, failing
// the test on error. (The service package's createTestIssue helper is
// unexported and cannot be reached from this package.)
func mustCreateIssue(t *testing.T, ctx context.Context, pool *pgxpool.Pool, slug, ident, actor, name string) *service.Issue {
	t.Helper()
	iss, err := service.CreateIssue(ctx, pool, slug, ident, actor, service.CreateIssueInput{Name: name})
	if err != nil {
		t.Fatalf("CreateIssue %s: %v", name, err)
	}
	return iss
}
