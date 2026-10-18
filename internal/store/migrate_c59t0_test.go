package store

// C59T0 migration 000065 (webhook_deliveries history table) round-trip
// test: the real up/down SQL files are applied and rolled back against
// the test database. The up file is idempotent on re-application, the
// down file drops the table, and the CHECK constraint on status matches
// the dispatcher's three outcomes. Cleanup re-applies up so the shared
// database is left as the full migration set leaves it.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"glance/migrations"
)

func webhookDeliveriesColumns(ctx context.Context, pool *pgxpool.Pool, t *testing.T) map[string]string {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT column_name, data_type
		FROM information_schema.columns
		WHERE table_name = 'webhook_deliveries'`)
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

func TestMigration000065WebhookDeliveries(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	// Full migrate first so the base schema exists.
	if err := Migrate(ctx, pool, migrations.FS); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	up, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000065_webhook_deliveries.up.sql"))
	if err != nil {
		t.Fatalf("read up migration: %v", err)
	}
	down, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000065_webhook_deliveries.down.sql"))
	if err != nil {
		t.Fatalf("read down migration: %v", err)
	}

	// Leave the DB as the full migration set leaves it (table present).
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, string(up)); err != nil {
			t.Errorf("cleanup re-apply up: %v", err)
		}
	})

	// Down first: drop the table, proving the check below sees a
	// genuine post-down state.
	if _, err := pool.Exec(ctx, string(down)); err != nil {
		t.Fatalf("down apply: %v", err)
	}
	if got := webhookDeliveriesColumns(ctx, pool, t); len(got) != 0 {
		t.Fatalf("webhook_deliveries columns still present after down: %v", got)
	}

	// Up twice: idempotent, all columns present with the right types.
	for i := 0; i < 2; i++ {
		if _, err := pool.Exec(ctx, string(up)); err != nil {
			t.Fatalf("up apply %d: %v", i+1, err)
		}
	}
	cols := webhookDeliveriesColumns(ctx, pool, t)
	for col, wantType := range map[string]string{
		"id":            "bigint",
		"webhook_id":    "uuid",
		"outbox_row_id": "bigint",
		"event":         "text",
		"data":          "jsonb",
		"status":        "text",
		"http_status":   "integer",
		"error":         "text",
		"duration_ms":   "integer",
		"attempted_at":  "timestamp with time zone",
	} {
		if got, ok := cols[col]; !ok {
			t.Fatalf("column %q missing after up: %v", col, cols)
		} else if got != wantType {
			t.Fatalf("column %q type = %q, want %q", col, got, wantType)
		}
	}

	// The CHECK constraint pins the dispatcher's three outcomes.
	var checkClause string
	if err := pool.QueryRow(ctx, `SELECT pg_get_constraintdef(oid)
		FROM pg_constraint
		WHERE conrelid = 'webhook_deliveries'::regclass AND conname LIKE '%status%'`,
	).Scan(&checkClause); err != nil {
		t.Fatalf("check constraint: %v", err)
	}
	for _, outcome := range []string{"delivered", "retrying", "failed"} {
		if !strings.Contains(checkClause, outcome) {
			t.Fatalf("CHECK clause = %q, want it to include %q", checkClause, outcome)
		}
	}

	// The list-query index exists.
	var indexExists bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_indexes
		  WHERE tablename = 'webhook_deliveries' AND indexname = 'webhook_deliveries_webhook_idx')`,
	).Scan(&indexExists); err != nil {
		t.Fatalf("index exists: %v", err)
	}
	if !indexExists {
		t.Fatal("index webhook_deliveries_webhook_idx missing after up")
	}

	// Down twice: idempotent, table gone.
	for i := 0; i < 2; i++ {
		if _, err := pool.Exec(ctx, string(down)); err != nil {
			t.Fatalf("down apply %d: %v", i+1, err)
		}
	}
	if got := webhookDeliveriesColumns(ctx, pool, t); len(got) != 0 {
		t.Fatalf("webhook_deliveries columns still present after down x2: %v", got)
	}
}
