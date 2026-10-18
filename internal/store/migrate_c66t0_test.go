package store

// C66T0 migration 000066 (pages search tsvector + GIN) round-trip test:
// the real up/down SQL files are applied and rolled back against the
// test database. The up file is idempotent on re-application (generated
// columns backfill automatically), the down file drops the column and
// index, and the generation expression weights title 'A' / content 'B'.
// Cleanup re-applies up so the shared database is left as the full
// migration set leaves it.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"glance/migrations"
)

func pagesSearchColumn(ctx context.Context, pool *pgxpool.Pool, t *testing.T) map[string]string {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT column_name, data_type
		FROM information_schema.columns
		WHERE table_name = 'pages'`)
	if err != nil {
		t.Fatalf("columns query: %v", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var name, typ string
		if err := rows.Scan(&name, &typ); err != nil {
			t.Fatalf("columns scan: %v", err)
		}
		out[name] = typ
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("columns iterate: %v", err)
	}
	return out
}

func TestMigration000066PagesSearch(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	// Full migrate first so the base schema exists.
	if err := Migrate(ctx, pool, migrations.FS); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	up, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000066_pages_search.up.sql"))
	if err != nil {
		t.Fatalf("read up migration: %v", err)
	}
	down, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000066_pages_search.down.sql"))
	if err != nil {
		t.Fatalf("read down migration: %v", err)
	}

	// Leave the DB as the full migration set leaves it (column present).
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, string(up)); err != nil {
			t.Errorf("cleanup re-apply up: %v", err)
		}
	})

	// Down first: drop the column + index, proving the checks below see
	// a genuine post-down state.
	if _, err := pool.Exec(ctx, string(down)); err != nil {
		t.Fatalf("down apply: %v", err)
	}
	if _, ok := pagesSearchColumn(ctx, pool, t)["search"]; ok {
		t.Fatal("pages.search still present after down")
	}
	var indexGone bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM pg_indexes WHERE indexname = 'pages_search_idx')`).Scan(&indexGone); err != nil {
		t.Fatalf("index probe: %v", err)
	}
	if indexGone {
		t.Fatal("pages_search_idx still present after down")
	}

	// Up twice: idempotent, search column present as generated tsvector.
	for i := 0; i < 2; i++ {
		if _, err := pool.Exec(ctx, string(up)); err != nil {
			t.Fatalf("up apply %d: %v", i+1, err)
		}
	}
	cols := pagesSearchColumn(ctx, pool, t)
	typ, ok := cols["search"]
	if !ok {
		t.Fatalf("pages.search missing after up: %v", cols)
	}
	if typ != "tsvector" {
		t.Fatalf("pages.search type = %q, want tsvector", typ)
	}
	var isGenerated string
	if err := pool.QueryRow(ctx, `SELECT is_generated FROM information_schema.columns
		WHERE table_name = 'pages' AND column_name = 'search'`).Scan(&isGenerated); err != nil {
		t.Fatalf("generated probe: %v", err)
	}
	if isGenerated != "ALWAYS" {
		t.Fatalf("pages.search is_generated = %q, want ALWAYS", isGenerated)
	}

	// The generation expression weights title 'A' and content 'B'.
	var genExpr string
	if err := pool.QueryRow(ctx, `SELECT pg_get_expr(adbin, adrelid)
		FROM pg_attrdef
		WHERE adrelid = 'pages'::regclass AND adnum = (
			SELECT attnum FROM pg_attribute
			WHERE attrelid = 'pages'::regclass AND attname = 'search')`).Scan(&genExpr); err != nil {
		t.Fatalf("generation expression: %v", err)
	}
	for _, want := range []string{"'A'", "'B'", "to_tsvector", "title", "content"} {
		if !strings.Contains(genExpr, want) {
			t.Fatalf("generation expression = %q, want it to include %q", genExpr, want)
		}
	}

	// The GIN index exists.
	var indexExists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM pg_indexes WHERE indexname = 'pages_search_idx')`).Scan(&indexExists); err != nil {
		t.Fatalf("index probe: %v", err)
	}
	if !indexExists {
		t.Fatal("pages_search_idx missing after up")
	}
}
