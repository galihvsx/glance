package store

import (
	"context"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// migrationsTable records which migrations have been applied.
const migrationsTable = "schema_migrations"

// Migrate applies pending *.up.sql migrations from embedFS in filename
// order. Each migration runs in its own transaction and is recorded in
// schema_migrations; already-applied migrations are skipped, so re-runs
// are idempotent.
//
// embedFS must be rooted at the directory containing the migration files
// (when embedding a parent directory, pass the result of fs.Sub).
// The table schema itself is defined by 000001_init; Migrate only ensures
// the bookkeeping table exists so the runner works on a fresh database.
func Migrate(ctx context.Context, pool *pgxpool.Pool, embedFS fs.FS) error {
	if err := ensureMigrationsTable(ctx, pool); err != nil {
		return err
	}
	files, err := migrationFiles(embedFS)
	if err != nil {
		return err
	}
	applied, err := appliedVersions(ctx, pool)
	if err != nil {
		return err
	}
	for _, name := range files {
		version := strings.TrimSuffix(name, ".up.sql")
		if applied[version] {
			continue
		}
		if err := applyMigration(ctx, pool, embedFS, name, version); err != nil {
			return fmt.Errorf("store: migration %s: %w", version, err)
		}
	}
	return nil
}

// ensureMigrationsTable creates the bookkeeping table if absent.
func ensureMigrationsTable(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS `+migrationsTable+` (
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
func appliedVersions(ctx context.Context, pool *pgxpool.Pool) (map[string]bool, error) {
	rows, err := pool.Query(ctx, `SELECT version FROM `+migrationsTable)
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

// applyMigration runs one migration file in its own transaction and records
// it. The SQL is executed without parameters, so pgx uses the simple
// protocol and multi-statement files work as-is.
func applyMigration(ctx context.Context, pool *pgxpool.Pool, embedFS fs.FS, name, version string) error {
	sql, err := fs.ReadFile(embedFS, name)
	if err != nil {
		return fmt.Errorf("read migration file: %w", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, string(sql)); err != nil {
		return fmt.Errorf("exec: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO `+migrationsTable+` (version) VALUES ($1)`, version); err != nil {
		return fmt.Errorf("record: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}
