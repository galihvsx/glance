package store

import (
	"context"
	"fmt"
	"hash/fnv"
	"io/fs"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// migrationsTable records which migrations have been applied.
const migrationsTable = "schema_migrations"

// migrationAdvisoryLockKey is the fixed 64-bit advisory-lock key that
// serializes concurrent Migrate runs against the same database. It is the
// FNV-1a hash of a stable name, so every glance process computes the same
// key without coordination.
var migrationAdvisoryLockKey = int64(fnv64("glance/schema_migrations"))

func fnv64(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}

// dbtx is the query surface Migrate needs. Both *pgxpool.Pool and pgx.Tx
// satisfy it, so the helpers run unchanged on either.
type dbtx interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// Migrate applies pending *.up.sql migrations from embedFS in filename
// order. The whole run executes inside a single transaction guarded by a
// transaction-scoped advisory lock (pg_advisory_xact_lock), so concurrent
// migrators — e.g. parallel `go test` packages sharing one database —
// serialize instead of racing: the loser blocks on the lock, then sees
// every migration already applied and commits an empty transaction.
//
// The lock is transaction-scoped, not session-scoped: it releases
// automatically at commit/rollback, so a crashed migrator can never wedge
// later runs. Applied migrations are recorded in schema_migrations, making
// re-runs idempotent and the run all-or-nothing.
//
// embedFS must be rooted at the directory containing the migration files
// (when embedding a parent directory, pass the result of fs.Sub).
// The table schema itself is defined by 000001_init; Migrate only ensures
// the bookkeeping table exists so the runner works on a fresh database.
func Migrate(ctx context.Context, pool *pgxpool.Pool, embedFS fs.FS) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: begin migration transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, migrationAdvisoryLockKey); err != nil {
		return fmt.Errorf("store: acquire migration lock: %w", err)
	}

	if err := ensureMigrationsTable(ctx, tx); err != nil {
		return err
	}
	files, err := migrationFiles(embedFS)
	if err != nil {
		return err
	}
	applied, err := appliedVersions(ctx, tx)
	if err != nil {
		return err
	}
	for _, name := range files {
		version := strings.TrimSuffix(name, ".up.sql")
		if applied[version] {
			continue
		}
		if err := applyMigration(ctx, tx, embedFS, name, version); err != nil {
			return fmt.Errorf("store: migration %s: %w", version, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("store: commit migrations: %w", err)
	}
	return nil
}

// ensureMigrationsTable creates the bookkeeping table if absent.
func ensureMigrationsTable(ctx context.Context, db dbtx) error {
	_, err := db.Exec(ctx, `CREATE TABLE IF NOT EXISTS `+migrationsTable+` (
		version    TEXT PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`)
	if err != nil {
		return fmt.Errorf("store: ensure migrations table: %w", err)
	}
	return nil
}

// migrationFiles lists *.up.sql files in embedFS, sorted by filename.
func migrationFiles(embedFS fs.FS) ([]string, error) {
	entries, err := fs.ReadDir(embedFS, ".")
	if err != nil {
		return nil, fmt.Errorf("store: read migrations: %w", err)
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".up.sql") {
			continue
		}
		files = append(files, e.Name())
	}
	sort.Strings(files)
	return files, nil
}

// appliedVersions returns the set of recorded migration versions.
func appliedVersions(ctx context.Context, db dbtx) (map[string]bool, error) {
	rows, err := db.Query(ctx, `SELECT version FROM `+migrationsTable)
	if err != nil {
		return nil, fmt.Errorf("store: list applied migrations: %w", err)
	}
	applied := make(map[string]bool)
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			rows.Close()
			return nil, fmt.Errorf("store: scan applied migration: %w", err)
		}
		applied[version] = true
	}
	rows.Close()
	return applied, rows.Err()
}

// applyMigration runs one migration file inside the caller's transaction and
// records it. The SQL is executed without parameters, so pgx uses the simple
// protocol and multi-statement files work as-is.
func applyMigration(ctx context.Context, db dbtx, embedFS fs.FS, name, version string) error {
	sql, err := fs.ReadFile(embedFS, name)
	if err != nil {
		return fmt.Errorf("read migration file: %w", err)
	}
	if _, err := db.Exec(ctx, string(sql)); err != nil {
		return fmt.Errorf("exec: %w", err)
	}
	if _, err := db.Exec(ctx,
		`INSERT INTO `+migrationsTable+` (version) VALUES ($1)`, version); err != nil {
		return fmt.Errorf("record: %w", err)
	}
	return nil
}
