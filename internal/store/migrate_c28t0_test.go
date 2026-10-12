package store

// C28T0 migration 000051 (issue_schedules) round-trip test: the real
// up/down SQL files are applied and rolled back against the test
// database. The table is restored in cleanup so the shared database is
// left as the full migration set leaves it.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"glance/migrations"
)

func issueSchedulesTableExists(ctx context.Context, pool *pgxpool.Pool) bool {
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM information_schema.tables
		WHERE table_name = 'issue_schedules')`).Scan(&exists); err != nil {
		return false
	}
	return exists
}

func TestMigration000051IssueSchedulesRoundTrip(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	// Full migrate first so the workspaces/projects/users tables exist.
	if err := Migrate(ctx, pool, migrations.FS); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	up, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000051_issue_schedules.up.sql"))
	if err != nil {
		t.Fatalf("read up migration: %v", err)
	}
	down, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000051_issue_schedules.down.sql"))
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
	if !issueSchedulesTableExists(ctx, pool) {
		t.Fatalf("issue_schedules missing after up")
	}

	// Both required indexes exist.
	for _, idx := range []string{"issue_schedules_project_idx", "issue_schedules_active_next_run_idx"} {
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (
			SELECT 1 FROM pg_indexes WHERE indexname = $1)`, idx).Scan(&exists); err != nil || !exists {
			t.Fatalf("index %s missing (err=%v)", idx, err)
		}
	}

	// CHECKs reject a bad frequency and a bad hour. Build one fixture
	// row set explicitly so the test is self-contained.
	var wsID, projID, userID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email) VALUES ('sched-check@example.com')
		RETURNING id::text`).Scan(&userID); err != nil {
		t.Fatalf("fixture user: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO workspaces (name, slug) VALUES ('Sched Check', 'sched-check-ws')
		RETURNING id::text`).Scan(&wsID); err != nil {
		t.Fatalf("fixture workspace: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO projects (workspace_id, name, identifier) VALUES ($1::uuid, 'Sched Check', 'SCHK')
		RETURNING id::text`, wsID).Scan(&projID); err != nil {
		t.Fatalf("fixture project: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM workspaces WHERE id = $1::uuid`, wsID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1::uuid`, userID)
	})

	badInsert := func(freq string, hour int) error {
		_, err := pool.Exec(ctx, `INSERT INTO issue_schedules
			(workspace_id, project_id, name, frequency, interval, hour, starts_on, next_run_at, title_template, created_by)
			VALUES ($1::uuid, $2::uuid, 'x', $3, 1, $4, CURRENT_DATE, now(), 't', $5::uuid)`,
			wsID, projID, freq, hour, userID)
		return err
	}
	if err := badInsert("yearly", 9); err == nil {
		t.Fatalf("frequency CHECK not enforced")
	}
	if err := badInsert("daily", 25); err == nil {
		t.Fatalf("hour CHECK not enforced")
	}
	// ...and a good row inserts fine.
	if err := badInsert("daily", 9); err != nil {
		t.Fatalf("valid insert: %v", err)
	}

	// Down twice: idempotent, table gone.
	for i := 0; i < 2; i++ {
		if _, err := pool.Exec(ctx, string(down)); err != nil {
			t.Fatalf("down apply %d: %v", i+1, err)
		}
	}
	if issueSchedulesTableExists(ctx, pool) {
		t.Fatalf("issue_schedules still present after down")
	}
}
