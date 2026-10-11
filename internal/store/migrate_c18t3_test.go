package store

// C18T3 migration 000048 (projects.archived_at) idempotency test: the
// real up/down SQL files are applied twice each against the test
// database. The column is restored in cleanup so the shared database is
// left as the full migration set leaves it.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"glance/migrations"
)

func archiveColumnExists(ctx context.Context, pool *pgxpool.Pool) bool {
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_name = 'projects' AND column_name = 'archived_at')`).Scan(&exists); err != nil {
		return false
	}
	return exists
}

func TestMigration000048ProjectArchiveIdempotent(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	// Full migrate first so the projects table exists.
	if err := Migrate(ctx, pool, migrations.FS); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	up, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000048_project_archive.up.sql"))
	if err != nil {
		t.Fatalf("read up migration: %v", err)
	}
	down, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000048_project_archive.down.sql"))
	if err != nil {
		t.Fatalf("read down migration: %v", err)
	}

	// Leave the DB as the full migration set leaves it (column present).
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, string(up)); err != nil {
			t.Errorf("cleanup re-apply up: %v", err)
		}
	})

	// Up twice: idempotent, column present and nullable.
	for i := 0; i < 2; i++ {
		if _, err := pool.Exec(ctx, string(up)); err != nil {
			t.Fatalf("up apply %d: %v", i+1, err)
		}
	}
	if !archiveColumnExists(ctx, pool) {
		t.Fatalf("projects.archived_at missing after up")
	}
	var nullable string
	if err := pool.QueryRow(ctx, `SELECT is_nullable FROM information_schema.columns
		WHERE table_name = 'projects' AND column_name = 'archived_at'`).Scan(&nullable); err != nil {
		t.Fatalf("archived_at nullability: %v", err)
	}
	if nullable != "YES" {
		t.Fatalf("projects.archived_at is_nullable = %q, want YES", nullable)
	}
	var dataType string
	if err := pool.QueryRow(ctx, `SELECT data_type FROM information_schema.columns
		WHERE table_name = 'projects' AND column_name = 'archived_at'`).Scan(&dataType); err != nil {
		t.Fatalf("archived_at data type: %v", err)
	}
	if dataType != "timestamp with time zone" {
		t.Fatalf("projects.archived_at data_type = %q, want timestamp with time zone", dataType)
	}

	// Down twice: idempotent, column gone.
	for i := 0; i < 2; i++ {
		if _, err := pool.Exec(ctx, string(down)); err != nil {
			t.Fatalf("down apply %d: %v", i+1, err)
		}
	}
	if archiveColumnExists(ctx, pool) {
		t.Fatalf("projects.archived_at still present after down")
	}
}
