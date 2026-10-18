package store

// C68T0 migration 000067 (project_state_transitions) round-trip test:
// the real up/down SQL files are applied and rolled back against the
// test database. The up file is idempotent on re-application, the down
// file drops the table, the (from,to) pair is unique, both state FKs
// are ON DELETE CASCADE, and the (project_id, from_state_id) index
// exists. Cleanup re-applies up so the shared database is left as the
// full migration set leaves it.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"glance/migrations"
)

func transitionTableCols(ctx context.Context, pool *pgxpool.Pool, t *testing.T) map[string]string {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT column_name, data_type
		FROM information_schema.columns
		WHERE table_name = 'project_state_transitions'`)
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

func TestMigration000067WorkflowTransitions(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	// Full migrate first so the base schema exists.
	if err := Migrate(ctx, pool, migrations.FS); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	up, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000067_workflow_transitions.up.sql"))
	if err != nil {
		t.Fatalf("read up migration: %v", err)
	}
	down, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000067_workflow_transitions.down.sql"))
	if err != nil {
		t.Fatalf("read down migration: %v", err)
	}

	// Leave the DB as the full migration set leaves it (table present).
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, string(up)); err != nil {
			t.Errorf("cleanup re-apply up: %v", err)
		}
	})

	// Down first: table gone, proving the checks below see a genuine
	// post-down state.
	if _, err := pool.Exec(ctx, string(down)); err != nil {
		t.Fatalf("down apply: %v", err)
	}
	var tableGone bool
	if err := pool.QueryRow(ctx, `SELECT NOT EXISTS(
		SELECT 1 FROM information_schema.tables
		WHERE table_name = 'project_state_transitions')`).Scan(&tableGone); err != nil {
		t.Fatalf("table probe: %v", err)
	}
	if !tableGone {
		t.Fatal("project_state_transitions still present after down")
	}

	// Up twice: idempotent.
	for i := 0; i < 2; i++ {
		if _, err := pool.Exec(ctx, string(up)); err != nil {
			t.Fatalf("up apply %d: %v", i+1, err)
		}
	}

	cols := transitionTableCols(ctx, pool, t)
	for _, want := range []string{"from_state_id", "to_state_id", "project_id"} {
		if typ, ok := cols[want]; !ok || typ != "uuid" {
			t.Fatalf("column %s = %q, want uuid", want, typ)
		}
	}

	// UNIQUE(from_state_id, to_state_id).
	var uniqueExists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM pg_constraint
		WHERE conrelid = 'project_state_transitions'::regclass
		  AND contype = 'u')`).Scan(&uniqueExists); err != nil {
		t.Fatalf("unique probe: %v", err)
	}
	if !uniqueExists {
		t.Fatal("no UNIQUE constraint on project_state_transitions")
	}

	// Both state FKs are ON DELETE CASCADE.
	var cascadeFKs int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_constraint
		WHERE conrelid = 'project_state_transitions'::regclass
		  AND contype = 'f' AND confdeltype = 'c'`).Scan(&cascadeFKs); err != nil {
		t.Fatalf("fk probe: %v", err)
	}
	if cascadeFKs != 3 {
		t.Fatalf("ON DELETE CASCADE FKs = %d, want 3 (from, to, project)", cascadeFKs)
	}

	// The (project_id, from_state_id) index exists.
	var indexExists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM pg_indexes
		WHERE indexname = 'project_state_transitions_project_idx')`).Scan(&indexExists); err != nil {
		t.Fatalf("index probe: %v", err)
	}
	if !indexExists {
		t.Fatal("project_state_transitions_project_idx missing after up")
	}
}
