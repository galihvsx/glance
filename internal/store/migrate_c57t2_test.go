package store

// C57T2 migration 000064 (webhook last-delivery outcome columns)
// round-trip test: the real up/down SQL files are applied and rolled
// back against the test database. The up file is idempotent on
// re-application, the down file drops the three columns, and the
// CHECK constraint on last_delivery_status matches the dispatcher's
// three outcomes. Cleanup re-applies up so the shared database is left
// as the full migration set leaves it.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"glance/migrations"
)

func webhookDeliveryColumns(ctx context.Context, pool *pgxpool.Pool, t *testing.T) map[string]bool {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT column_name, data_type
		FROM information_schema.columns
		WHERE table_name = 'webhooks'
		  AND column_name IN ('last_delivery_at', 'last_delivery_status', 'last_error')`)
	if err != nil {
		t.Fatalf("columns query: %v", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	types := map[string]string{}
	for rows.Next() {
		var name, typ string
		if err := rows.Scan(&name, &typ); err != nil {
			t.Fatalf("columns scan: %v", err)
		}
		out[name] = true
		types[name] = typ
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("columns iterate: %v", err)
	}
	if out["last_delivery_at"] && types["last_delivery_at"] != "timestamp with time zone" {
		t.Fatalf("last_delivery_at type = %q, want timestamp with time zone", types["last_delivery_at"])
	}
	if out["last_delivery_status"] && types["last_delivery_status"] != "text" {
		t.Fatalf("last_delivery_status type = %q, want text", types["last_delivery_status"])
	}
	if out["last_error"] && types["last_error"] != "text" {
		t.Fatalf("last_error type = %q, want text", types["last_error"])
	}
	return out
}

func TestMigration000064WebhookDeliveryStatus(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	// Full migrate first so the base schema exists.
	if err := Migrate(ctx, pool, migrations.FS); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	up, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000064_webhook_delivery_status.up.sql"))
	if err != nil {
		t.Fatalf("read up migration: %v", err)
	}
	down, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000064_webhook_delivery_status.down.sql"))
	if err != nil {
		t.Fatalf("read down migration: %v", err)
	}

	// Leave the DB as the full migration set leaves it (columns present).
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, string(up)); err != nil {
			t.Errorf("cleanup re-apply up: %v", err)
		}
	})

	// Down first: drop the columns, proving the check below sees a
	// genuine post-down state.
	if _, err := pool.Exec(ctx, string(down)); err != nil {
		t.Fatalf("down apply: %v", err)
	}
	if got := webhookDeliveryColumns(ctx, pool, t); len(got) != 0 {
		t.Fatalf("delivery columns still present after down: %v", got)
	}

	// Up twice: idempotent, all three columns present.
	for i := 0; i < 2; i++ {
		if _, err := pool.Exec(ctx, string(up)); err != nil {
			t.Fatalf("up apply %d: %v", i+1, err)
		}
	}
	if got := webhookDeliveryColumns(ctx, pool, t); len(got) != 3 {
		t.Fatalf("delivery columns after up = %v, want all 3", got)
	}

	// The CHECK constraint pins the dispatcher's three outcomes.
	var checkClause string
	if err := pool.QueryRow(ctx, `SELECT pg_get_constraintdef(oid)
		FROM pg_constraint
		WHERE conrelid = 'webhooks'::regclass AND conname LIKE '%last_delivery_status%'`,
	).Scan(&checkClause); err != nil {
		t.Fatalf("check constraint: %v", err)
	}
	for _, outcome := range []string{"delivered", "retrying", "failed"} {
		if !strings.Contains(checkClause, outcome) {
			t.Fatalf("CHECK clause = %q, want it to include %q", checkClause, outcome)
		}
	}

	// Down twice: idempotent, columns gone.
	for i := 0; i < 2; i++ {
		if _, err := pool.Exec(ctx, string(down)); err != nil {
			t.Fatalf("down apply %d: %v", i+1, err)
		}
	}
	if got := webhookDeliveryColumns(ctx, pool, t); len(got) != 0 {
		t.Fatalf("delivery columns still present after down x2: %v", got)
	}
}
