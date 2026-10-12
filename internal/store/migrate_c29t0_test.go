package store

// C29T0 migration 000052 (workspace_members.weekly_capacity_points)
// round-trip test: the real up/down SQL files are applied and rolled
// back against the test database. The column is restored in cleanup so
// the shared database is left as the full migration set leaves it.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"glance/migrations"
)

func memberCapacityColumnExists(ctx context.Context, pool *pgxpool.Pool) bool {
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_name = 'workspace_members'
		  AND column_name = 'weekly_capacity_points')`).Scan(&exists); err != nil {
		return false
	}
	return exists
}

func TestMigration000052MemberCapacityRoundTrip(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	// Full migrate first so the workspace_members table exists.
	if err := Migrate(ctx, pool, migrations.FS); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	up, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000052_member_capacity.up.sql"))
	if err != nil {
		t.Fatalf("read up migration: %v", err)
	}
	down, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000052_member_capacity.down.sql"))
	if err != nil {
		t.Fatalf("read down migration: %v", err)
	}

	// Leave the DB as the full migration set leaves it (column present).
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, string(up)); err != nil {
			t.Errorf("cleanup re-apply up: %v", err)
		}
	})

	// Up twice: idempotent, column present.
	for i := 0; i < 2; i++ {
		if _, err := pool.Exec(ctx, string(up)); err != nil {
			t.Fatalf("up apply %d: %v", i+1, err)
		}
	}
	if !memberCapacityColumnExists(ctx, pool) {
		t.Fatalf("weekly_capacity_points missing after up")
	}

	// CHECK enforces the 0..999 bounds on a fixture membership; NULL
	// stays legal (unset capacity).
	var userID, wsID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email) VALUES ('cap-check@example.com')
		RETURNING id::text`).Scan(&userID); err != nil {
		t.Fatalf("fixture user: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO workspaces (name, slug) VALUES ('Cap Check', 'cap-check-ws')
		RETURNING id::text`).Scan(&wsID); err != nil {
		t.Fatalf("fixture workspace: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO workspace_members (workspace_id, user_id, role)
		VALUES ($1::uuid, $2::uuid, 15)`, wsID, userID); err != nil {
		t.Fatalf("fixture membership: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM workspaces WHERE id = $1::uuid`, wsID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1::uuid`, userID)
	})

	setCap := func(v any) error {
		_, err := pool.Exec(ctx, `UPDATE workspace_members
			SET weekly_capacity_points = $1
			WHERE workspace_id = $2::uuid AND user_id = $3::uuid`,
			v, wsID, userID)
		return err
	}
	if err := setCap(1000); err == nil {
		t.Fatalf("capacity CHECK did not reject 1000")
	}
	if err := setCap(-1); err == nil {
		t.Fatalf("capacity CHECK did not reject -1")
	}
	if err := setCap(999); err != nil {
		t.Fatalf("valid capacity 999: %v", err)
	}
	if err := setCap(0); err != nil {
		t.Fatalf("valid capacity 0: %v", err)
	}
	if err := setCap(nil); err != nil {
		t.Fatalf("NULL capacity (unset): %v", err)
	}

	// Down twice: idempotent, column gone.
	for i := 0; i < 2; i++ {
		if _, err := pool.Exec(ctx, string(down)); err != nil {
			t.Fatalf("down apply %d: %v", i+1, err)
		}
	}
	if memberCapacityColumnExists(ctx, pool) {
		t.Fatalf("weekly_capacity_points still present after down")
	}
}
