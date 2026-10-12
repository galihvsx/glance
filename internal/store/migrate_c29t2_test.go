package store

// C29T2 migration 000053 (capacity_alerts dedupe table) round-trip test:
// the real up/down SQL files are applied and rolled back against the
// test database. The table is restored in cleanup so the shared database
// is left as the full migration set leaves it.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"glance/migrations"
)

func capacityAlertsTableExists(ctx context.Context, pool *pgxpool.Pool) bool {
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM information_schema.tables
		WHERE table_name = 'capacity_alerts')`).Scan(&exists); err != nil {
		return false
	}
	return exists
}

func TestMigration000053CapacityAlertsRoundTrip(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	// Full migrate first so workspaces/users exist.
	if err := Migrate(ctx, pool, migrations.FS); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	up, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000053_capacity_alerts.up.sql"))
	if err != nil {
		t.Fatalf("read up migration: %v", err)
	}
	down, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000053_capacity_alerts.down.sql"))
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
	if !capacityAlertsTableExists(ctx, pool) {
		t.Fatalf("capacity_alerts missing after up")
	}

	// PK dedupe: two rows for the same (workspace, user, day) fail; a
	// different day succeeds.
	var userID, wsID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email) VALUES ('alert-dedupe@example.com')
		RETURNING id::text`).Scan(&userID); err != nil {
		t.Fatalf("fixture user: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO workspaces (name, slug) VALUES ('Alert Dedupe', 'alert-dedupe-ws')
		RETURNING id::text`).Scan(&wsID); err != nil {
		t.Fatalf("fixture workspace: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM workspaces WHERE id = $1::uuid`, wsID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1::uuid`, userID)
	})

	claim := func(day string) error {
		_, err := pool.Exec(ctx, `INSERT INTO capacity_alerts (workspace_id, user_id, alert_date)
			VALUES ($1::uuid, $2::uuid, $3::date)`, wsID, userID, day)
		return err
	}
	if err := claim("2026-10-13"); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if err := claim("2026-10-13"); err == nil {
		t.Fatalf("duplicate (workspace, user, day) claim succeeded")
	}
	if err := claim("2026-10-14"); err != nil {
		t.Fatalf("next-day claim: %v", err)
	}

	// Down twice: idempotent, table gone.
	for i := 0; i < 2; i++ {
		if _, err := pool.Exec(ctx, string(down)); err != nil {
			t.Fatalf("down apply %d: %v", i+1, err)
		}
	}
	if capacityAlertsTableExists(ctx, pool) {
		t.Fatalf("capacity_alerts still present after down")
	}
}
