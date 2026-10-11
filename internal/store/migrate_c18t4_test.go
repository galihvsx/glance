package store

// C18T4 migration 000046 (projects.icon) idempotency test: the real
// up/down SQL files are applied twice each against the test database.
// The column is restored in cleanup so the shared database is left as
// the full migration set leaves it.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"glance/migrations"
)

func iconColumnExists(ctx context.Context, pool *pgxpool.Pool) bool {
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_name = 'projects' AND column_name = 'icon')`).Scan(&exists); err != nil {
		return false
	}
	return exists
}

func TestMigration000046ProjectIconIdempotent(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	// Full migrate first so the projects table exists.
	if err := Migrate(ctx, pool, migrations.FS); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	up, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000046_project_icon.up.sql"))
	if err != nil {
		t.Fatalf("read up migration: %v", err)
	}
	down, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000046_project_icon.down.sql"))
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
	if !iconColumnExists(ctx, pool) {
		t.Fatalf("projects.icon missing after up")
	}
	var nullable string
	if err := pool.QueryRow(ctx, `SELECT is_nullable FROM information_schema.columns
		WHERE table_name = 'projects' AND column_name = 'icon'`).Scan(&nullable); err != nil {
		t.Fatalf("icon nullability: %v", err)
	}
	if nullable != "YES" {
		t.Fatalf("projects.icon is_nullable = %q, want YES", nullable)
	}

	// Down twice: idempotent, column gone.
	for i := 0; i < 2; i++ {
		if _, err := pool.Exec(ctx, string(down)); err != nil {
			t.Fatalf("down apply %d: %v", i+1, err)
		}
	}
	if iconColumnExists(ctx, pool) {
		t.Fatalf("projects.icon still present after down")
	}
}
