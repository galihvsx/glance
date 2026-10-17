package store

// C57T1 migration 000063 (outbox prune partial index) round-trip
// test: the real up/down SQL files are applied and rolled back
// against the test database. The up file is idempotent on
// re-application, the down file drops the index, and the index
// definition matches the prune predicate (status IN
// ('done','failed') over processed_at). Cleanup re-applies up so
// the shared database is left as the full migration set leaves it.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"glance/migrations"
)

func outboxPruneIndexExists(ctx context.Context, pool *pgxpool.Pool, t *testing.T) bool {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM pg_indexes
		WHERE schemaname = 'public' AND tablename = 'outbox'
		  AND indexname = 'outbox_prune_idx')`).Scan(&exists); err != nil {
		t.Fatalf("index exists check: %v", err)
	}
	return exists
}

func TestMigration000063OutboxPruneIndex(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	// Full migrate first so the base schema exists.
	if err := Migrate(ctx, pool, migrations.FS); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	up, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000063_outbox_retention_idx.up.sql"))
	if err != nil {
		t.Fatalf("read up migration: %v", err)
	}
	down, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000063_outbox_retention_idx.down.sql"))
	if err != nil {
		t.Fatalf("read down migration: %v", err)
	}

	// Leave the DB as the full migration set leaves it (index present).
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, string(up)); err != nil {
			t.Errorf("cleanup re-apply up: %v", err)
		}
	})

	// Down first: drop the index, proving the check below sees a
	// genuine post-down state.
	if _, err := pool.Exec(ctx, string(down)); err != nil {
		t.Fatalf("down apply: %v", err)
	}
	if outboxPruneIndexExists(ctx, pool, t) {
		t.Fatal("outbox_prune_idx still present after down, want gone")
	}

	// Up twice: idempotent, index present with the prune predicate.
	for i := 0; i < 2; i++ {
		if _, err := pool.Exec(ctx, string(up)); err != nil {
			t.Fatalf("up apply %d: %v", i+1, err)
		}
	}
	if !outboxPruneIndexExists(ctx, pool, t) {
		t.Fatal("outbox_prune_idx missing after up, want present")
	}
	var def string
	if err := pool.QueryRow(ctx,
		`SELECT indexdef FROM pg_indexes
		  WHERE schemaname = 'public' AND tablename = 'outbox'
		    AND indexname = 'outbox_prune_idx'`).Scan(&def); err != nil {
		t.Fatalf("indexdef: %v", err)
	}
	if !strings.Contains(def, "processed_at") ||
		!strings.Contains(def, "done") ||
		!strings.Contains(def, "failed") {
		t.Fatalf("indexdef = %q, want partial index on processed_at WHERE status IN ('done','failed')", def)
	}

	// Down twice: idempotent, index gone.
	for i := 0; i < 2; i++ {
		if _, err := pool.Exec(ctx, string(down)); err != nil {
			t.Fatalf("down apply %d: %v", i+1, err)
		}
	}
	if outboxPruneIndexExists(ctx, pool, t) {
		t.Fatal("outbox_prune_idx still present after down, want gone")
	}
}
