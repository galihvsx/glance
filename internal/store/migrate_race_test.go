package store

import (
	"context"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"glance/internal/config"
	"glance/migrations"
)

// raceDBName is the scratch database for the concurrent-migration test.
// It is separate from the shared glance_test database so parallel test
// packages cannot interfere with it.
const raceDBName = "glance_migrate_race"

// adminURL returns a connection URL for the postgres maintenance database,
// derived from TEST_DATABASE_URL.
func adminURL(t *testing.T) string {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Fatal("TEST_DATABASE_URL must be set; refusing to skip")
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL: %v", err)
	}
	u.Path = "/postgres"
	return u.String()
}

// raceDBURL returns the connection URL for the scratch race database.
func raceDBURL(t *testing.T) string {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Fatal("TEST_DATABASE_URL must be set; refusing to skip")
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL: %v", err)
	}
	u.Path = "/" + raceDBName
	return u.String()
}

// recreateRaceDB drops and recreates the scratch database from a clean slate.
func recreateRaceDB(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	pool, err := NewPool(ctx, &config.Config{DatabaseURL: adminURL(t)})
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}
	defer pool.Close()
	// Quote the identifier; the name is a constant, but QuoteIdentifier keeps
	// linters and future edits honest.
	qn := `"` + strings.ReplaceAll(raceDBName, `"`, `""`) + `"`
	if _, err := pool.Exec(ctx, `DROP DATABASE IF EXISTS `+qn); err != nil {
		t.Fatalf("drop race db: %v", err)
	}
	if _, err := pool.Exec(ctx, `CREATE DATABASE `+qn+` OWNER glance`); err != nil {
		t.Fatalf("create race db: %v", err)
	}
}

// TestMigrateConcurrent is a regression test for the parallel-test migration
// race: `go test ./...` runs test packages in parallel processes that share
// one database, and concurrent Migrate() calls used to collide with
// "duplicate key pg_type_typname_nsp_index" because two migrators both saw a
// migration as pending and both applied CREATE TABLE. Migrate must serialize
// concurrent runs (advisory lock) so every caller succeeds.
func TestMigrateConcurrent(t *testing.T) {
	recreateRaceDB(t)
	t.Cleanup(func() {
		ctx := context.Background()
		pool, err := NewPool(ctx, &config.Config{DatabaseURL: adminURL(t)})
		if err != nil {
			t.Fatalf("admin pool (cleanup): %v", err)
		}
		defer pool.Close()
		qn := `"` + strings.ReplaceAll(raceDBName, `"`, `""`) + `"`
		if _, err := pool.Exec(ctx, `DROP DATABASE IF EXISTS `+qn); err != nil {
			t.Fatalf("drop race db (cleanup): %v", err)
		}
	})

	const workers = 8
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx := context.Background()
			// One pool per worker: faithful to the real scenario where each
			// test package is a separate process with its own pool.
			pool, err := NewPool(ctx, &config.Config{DatabaseURL: raceDBURL(t)})
			if err != nil {
				errs[i] = fmt.Errorf("worker %d: NewPool: %w", i, err)
				return
			}
			defer pool.Close()
			if err := Migrate(ctx, pool, migrations.FS); err != nil {
				errs[i] = fmt.Errorf("worker %d: Migrate: %w", i, err)
			}
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent migrate failed: %v (worker %d)", err, i)
		}
	}

	// Every migration must be recorded exactly once, no matter how many
	// migrators raced. The expected count is derived from the migration
	// files themselves so adding 000018 doesn't break this test.
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}
	want := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".up.sql") {
			want++
		}
	}
	ctx := context.Background()
	pool, err := NewPool(ctx, &config.Config{DatabaseURL: raceDBURL(t)})
	if err != nil {
		t.Fatalf("verify pool: %v", err)
	}
	defer pool.Close()
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&n); err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	if n != want {
		t.Fatalf("expected %d recorded migrations, got %d", want, n)
	}
	var dup int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM (SELECT version FROM schema_migrations GROUP BY version HAVING count(*) > 1) d`,
	).Scan(&dup); err != nil {
		t.Fatalf("check duplicate records: %v", err)
	}
	if dup != 0 {
		t.Fatalf("found %d duplicated migration records", dup)
	}
}
