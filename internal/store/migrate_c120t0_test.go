package store

// C120T0 migration 000082 (persisted @-mention index) round-trip
// test: the real up/down SQL files are applied and rolled back
// against the test database. Up asserts the table, its
// three-column PK, and both indexes; down asserts the table is
// gone. Cleanup re-applies up so the shared database is left as
// the full migration set leaves it. Follows the
// migrate_c113t0_test.go shape.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"glance/migrations"
)

func TestMigration000082IssueMentions(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	// Full migrate first so the base schema (including 000082) exists.
	if err := Migrate(ctx, pool, migrations.FS); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	up, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000082_issue_mentions.up.sql"))
	if err != nil {
		t.Fatalf("read up migration: %v", err)
	}
	down, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000082_issue_mentions.down.sql"))
	if err != nil {
		t.Fatalf("read down migration: %v", err)
	}

	// Leave the DB as the full migration set leaves it (up applied).
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, string(up)); err != nil {
			t.Errorf("cleanup re-apply up: %v", err)
		}
	})

	tableExists := func() bool {
		var exists bool
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables
			 WHERE table_schema = 'public' AND table_name = 'issue_mentions')`).Scan(&exists); err != nil {
			t.Fatalf("table exists check: %v", err)
		}
		return exists
	}
	indexDef := func(name string) (string, bool) {
		var def string
		err := pool.QueryRow(ctx,
			`SELECT indexdef FROM pg_indexes
			  WHERE schemaname = 'public' AND tablename = 'issue_mentions'
			    AND indexname = $1`, name).Scan(&def)
		if err != nil {
			return "", false
		}
		return def, true
	}
	pkColumns := func() string {
		rows, err := pool.Query(ctx,
			`SELECT kcu.column_name
			   FROM information_schema.table_constraints tc
			   JOIN information_schema.key_column_usage kcu
			     ON tc.constraint_name = kcu.constraint_name
			    AND tc.table_schema = kcu.table_schema
			  WHERE tc.table_schema = 'public' AND tc.table_name = 'issue_mentions'
			    AND tc.constraint_type = 'PRIMARY KEY'
			  ORDER BY kcu.ordinal_position`)
		if err != nil {
			t.Fatalf("pk columns: %v", err)
		}
		defer rows.Close()
		var cols []string
		for rows.Next() {
			var c string
			if err := rows.Scan(&c); err != nil {
				t.Fatalf("pk scan: %v", err)
			}
			cols = append(cols, c)
		}
		return strings.Join(cols, ",")
	}

	// Post-up shape (full migrate already applied 000082), and
	// up is idempotent on re-apply.
	if _, err := pool.Exec(ctx, string(up)); err != nil {
		t.Fatalf("up re-apply: %v", err)
	}
	if !tableExists() {
		t.Fatal("issue_mentions table missing after up")
	}
	if got := pkColumns(); got != "issue_id,user_id,comment_id" {
		t.Fatalf("PK columns = %q, want issue_id,user_id,comment_id", got)
	}
	if def, ok := indexDef("idx_issue_mentions_issue_user"); !ok || !strings.Contains(def, "issue_id") || !strings.Contains(def, "user_id") {
		t.Fatalf("filter-path index = %q (present=%v), want (issue_id, user_id)", def, ok)
	}
	if _, ok := indexDef("idx_issue_mentions_user"); !ok {
		t.Fatal("per-user index idx_issue_mentions_user missing after up")
	}

	// Down twice: idempotent, table gone.
	for i := 0; i < 2; i++ {
		if _, err := pool.Exec(ctx, string(down)); err != nil {
			t.Fatalf("down apply %d: %v", i+1, err)
		}
	}
	if tableExists() {
		t.Fatal("issue_mentions table still present after down, want gone")
	}

	// Up twice: idempotent, shape back.
	for i := 0; i < 2; i++ {
		if _, err := pool.Exec(ctx, string(up)); err != nil {
			t.Fatalf("up apply %d: %v", i+1, err)
		}
	}
	if !tableExists() || pkColumns() != "issue_id,user_id,comment_id" {
		t.Fatal("issue_mentions shape not restored after up re-apply")
	}
}
