package store

import (
	"context"
	"os"
	"testing"
	"testing/fstest"

	"github.com/jackc/pgx/v5/pgxpool"

	"glance/internal/config"
)

// testMigrations is a self-contained migration set used to exercise the
// runner without touching the real migrations/ directory. The .down.sql
// entry proves the runner ignores non-.up.sql files.
var testMigrations = fstest.MapFS{
	"000001_test_a.up.sql": {
		Data: []byte(`CREATE TABLE store_migrate_test_a (id bigint PRIMARY KEY);`),
	},
	"000002_test_b.up.sql": {
		Data: []byte(`CREATE TABLE store_migrate_test_b (id bigint PRIMARY KEY);`),
	},
	"000001_test_a.down.sql": {
		Data: []byte(`DROP TABLE store_migrate_test_a;`),
	},
}

const (
	testVersionA = "000001_test_a"
	testVersionB = "000002_test_b"
	testTableA   = "store_migrate_test_a"
	testTableB   = "store_migrate_test_b"
)

// newTestPool opens a real pool against TEST_DATABASE_URL, skipping when unset.
func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, err := NewPool(context.Background(), &config.Config{DatabaseURL: url})
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// resetTestState removes leftovers from a previous interrupted run so the
// test always starts from a known state. The DELETE may fail when
// schema_migrations does not exist yet on a fresh database; Migrate
// bootstraps the table, so that error is intentionally ignored.
func resetTestState(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx, `DROP TABLE IF EXISTS `+testTableA+`, `+testTableB); err != nil {
		t.Fatalf("reset drop tables: %v", err)
	}
	_, _ = pool.Exec(ctx, `DELETE FROM schema_migrations WHERE version IN ($1, $2)`, testVersionA, testVersionB)
}

func recordedCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	err := pool.QueryRow(ctx,
		`SELECT count(*) FROM schema_migrations WHERE version IN ($1, $2)`,
		testVersionA, testVersionB).Scan(&n)
	if err != nil {
		t.Fatalf("count recorded migrations: %v", err)
	}
	return n
}

func assertTableExists(t *testing.T, ctx context.Context, pool *pgxpool.Pool, table string) {
	t.Helper()
	var exists bool
	err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = $1)`,
		table).Scan(&exists)
	if err != nil {
		t.Fatalf("check table %s: %v", table, err)
	}
	if !exists {
		t.Fatalf("expected table %s to exist after migration", table)
	}
}

func TestMigrateAppliesPending(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	resetTestState(t, ctx, pool)
	t.Cleanup(func() { resetTestState(t, ctx, pool) })

	// First run: both pending migrations must apply.
	if err := Migrate(ctx, pool, testMigrations); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	if n := recordedCount(t, ctx, pool); n != 2 {
		t.Fatalf("expected 2 recorded migrations, got %d", n)
	}
	assertTableExists(t, ctx, pool, testTableA)
	assertTableExists(t, ctx, pool, testTableB)

	// Second run: idempotent — no error, no duplicate records.
	if err := Migrate(ctx, pool, testMigrations); err != nil {
		t.Fatalf("second Migrate (idempotent re-run): %v", err)
	}
	if n := recordedCount(t, ctx, pool); n != 2 {
		t.Fatalf("expected still 2 recorded migrations after re-run, got %d", n)
	}
}
