package store

// C92T0 migration 000078 (project ACL + teamspace Lead) round-trip
// test: the real up/down SQL files are applied and rolled back
// against the test database. The up file is idempotent on
// re-application, the down file drops the table and the added
// columns, the (project_id, user_id) pair is the PK, both FKs are ON
// DELETE CASCADE, and the visibility CHECK constraint exists.
// Cleanup re-applies up so the shared database is left as the full
// migration set leaves it.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"glance/migrations"
)

func c92TableCols(ctx context.Context, pool *pgxpool.Pool, t *testing.T, table string) map[string]string {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT column_name, data_type
		FROM information_schema.columns
		WHERE table_name = $1`, table)
	if err != nil {
		t.Fatalf("columns query %s: %v", table, err)
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

func TestMigration000078ProjectACLLead(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	// Full migrate first so the base schema exists.
	if err := Migrate(ctx, pool, migrations.FS); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	up, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000078_project_acl_lead.up.sql"))
	if err != nil {
		t.Fatalf("read up migration: %v", err)
	}
	down, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000078_project_acl_lead.down.sql"))
	if err != nil {
		t.Fatalf("read down migration: %v", err)
	}

	// Leave the DB as the full migration set leaves it.
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, string(up)); err != nil {
			t.Errorf("cleanup re-apply up: %v", err)
		}
	})

	// Down first: table gone, columns gone.
	if _, err := pool.Exec(ctx, string(down)); err != nil {
		t.Fatalf("down apply: %v", err)
	}
	var tableGone bool
	if err := pool.QueryRow(ctx, `SELECT NOT EXISTS(
		SELECT 1 FROM information_schema.tables
		WHERE table_name = 'project_members')`).Scan(&tableGone); err != nil {
		t.Fatalf("table probe: %v", err)
	}
	if !tableGone {
		t.Fatal("project_members still present after down")
	}
	for table, col := range map[string]string{
		"projects":   "visibility",
		"teamspaces": "lead_user_id",
	} {
		cols := c92TableCols(ctx, pool, t, table)
		if _, ok := cols[col]; ok {
			t.Fatalf("%s.%s still present after down", table, col)
		}
	}

	// Up twice: idempotent.
	for i := 0; i < 2; i++ {
		if _, err := pool.Exec(ctx, string(up)); err != nil {
			t.Fatalf("up apply %d: %v", i+1, err)
		}
	}

	cols := c92TableCols(ctx, pool, t, "project_members")
	for _, want := range []string{"project_id", "user_id", "role"} {
		if _, ok := cols[want]; !ok {
			t.Fatalf("project_members missing column %s", want)
		}
	}
	if typ := c92TableCols(ctx, pool, t, "projects")["visibility"]; typ != "text" {
		t.Fatalf("projects.visibility type = %q, want text", typ)
	}
	if typ := c92TableCols(ctx, pool, t, "teamspaces")["lead_user_id"]; typ != "uuid" {
		t.Fatalf("teamspaces.lead_user_id type = %q, want uuid", typ)
	}

	// (project_id, user_id) is the PK.
	var pkCols int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.key_column_usage
		WHERE table_name = 'project_members'
		  AND constraint_name IN (SELECT constraint_name FROM information_schema.table_constraints
			WHERE table_name = 'project_members' AND constraint_type = 'PRIMARY KEY')`).Scan(&pkCols); err != nil {
		t.Fatalf("pk probe: %v", err)
	}
	if pkCols != 2 {
		t.Fatalf("project_members PK columns = %d, want 2", pkCols)
	}

	// Both FKs are ON DELETE CASCADE.
	var cascadeFKs int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_constraint
		WHERE conrelid = 'project_members'::regclass
		  AND contype = 'f' AND confdeltype = 'c'`).Scan(&cascadeFKs); err != nil {
		t.Fatalf("fk probe: %v", err)
	}
	if cascadeFKs != 2 {
		t.Fatalf("ON DELETE CASCADE FKs = %d, want 2 (project, user)", cascadeFKs)
	}

	// The visibility CHECK constraint exists.
	var checkExists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM pg_constraint
		WHERE conrelid = 'projects'::regclass
		  AND conname = 'projects_visibility_check'
		  AND contype = 'c')`).Scan(&checkExists); err != nil {
		t.Fatalf("check probe: %v", err)
	}
	if !checkExists {
		t.Fatal("projects_visibility_check constraint missing")
	}
}
