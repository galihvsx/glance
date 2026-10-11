package store

// C18T2 migration 000047 (page_comments) idempotency test: the real
// up/down SQL files are applied twice each against the test database.
// The table is restored in cleanup so the shared database is left as
// the full migration set leaves it.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"glance/migrations"
)

func pageCommentsTableExists(ctx context.Context, pool *pgxpool.Pool) bool {
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM information_schema.tables
		WHERE table_name = 'page_comments')`).Scan(&exists); err != nil {
		return false
	}
	return exists
}

func TestMigration000047PageCommentsIdempotent(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	// Full migrate first so the pages and users tables exist.
	if err := Migrate(ctx, pool, migrations.FS); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	up, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000047_page_comments.up.sql"))
	if err != nil {
		t.Fatalf("read up migration: %v", err)
	}
	down, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000047_page_comments.down.sql"))
	if err != nil {
		t.Fatalf("read down migration: %v", err)
	}

	// Leave the DB as the full migration set leaves it (table present).
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, string(up)); err != nil {
			t.Errorf("cleanup re-apply up: %v", err)
		}
	})

	// Up twice: idempotent, table present.
	for i := 0; i < 2; i++ {
		if _, err := pool.Exec(ctx, string(up)); err != nil {
			t.Fatalf("up apply %d: %v", i+1, err)
		}
	}
	if !pageCommentsTableExists(ctx, pool) {
		t.Fatalf("page_comments missing after up")
	}

	// FK direction: page_id cascades, author_id nulls.
	var pageDel, authorDel string
	if err := pool.QueryRow(ctx, `SELECT delete_rule FROM information_schema.referential_constraints
		WHERE constraint_name = 'page_comments_page_id_fkey'`).Scan(&pageDel); err != nil {
		t.Fatalf("page_id fk: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT delete_rule FROM information_schema.referential_constraints
		WHERE constraint_name = 'page_comments_author_id_fkey'`).Scan(&authorDel); err != nil {
		t.Fatalf("author_id fk: %v", err)
	}
	if pageDel != "CASCADE" {
		t.Fatalf("page_comments.page_id delete_rule = %q, want CASCADE", pageDel)
	}
	if authorDel != "SET NULL" {
		t.Fatalf("page_comments.author_id delete_rule = %q, want SET NULL", authorDel)
	}

	// Down twice: idempotent, table gone.
	for i := 0; i < 2; i++ {
		if _, err := pool.Exec(ctx, string(down)); err != nil {
			t.Fatalf("down apply %d: %v", i+1, err)
		}
	}
	if pageCommentsTableExists(ctx, pool) {
		t.Fatalf("page_comments still present after down")
	}
}
