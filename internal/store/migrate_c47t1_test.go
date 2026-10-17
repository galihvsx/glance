package store

// C47T1 migration 000062 (key_result_issues.created_at) round-trip
// test: the real up/down SQL files are applied and rolled back
// against the test database. The backfill sets each pre-migration
// link's created_at to GREATEST(kr.created_at, i.created_at) — the
// earliest the link could possibly have happened (a lower bound,
// not the true link time). Both GREATEST branches are covered, the
// up file is idempotent on re-application, and the down file drops
// the column. Cleanup re-applies up so the shared database is left
// as the full migration set leaves it.

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"glance/migrations"
)

func linkCreatedAtColumnExists(ctx context.Context, pool *pgxpool.Pool) bool {
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_name = 'key_result_issues' AND column_name = 'created_at')`).Scan(&exists); err != nil {
		return false
	}
	return exists
}

func TestMigration000062LinkCreatedAtBackfill(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	// Full migrate first so the base schema exists.
	if err := Migrate(ctx, pool, migrations.FS); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	up, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000062_kr_link_created_at.up.sql"))
	if err != nil {
		t.Fatalf("read up migration: %v", err)
	}
	down, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000062_kr_link_created_at.down.sql"))
	if err != nil {
		t.Fatalf("read down migration: %v", err)
	}

	// Leave the DB as the full migration set leaves it (column present).
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, string(up)); err != nil {
			t.Errorf("cleanup re-apply up: %v", err)
		}
	})

	// Fixture: one user/workspace/project/state, two objectives'
	// KRs and two issues with staggered created_at so each
	// GREATEST branch is exercised:
	//   link A: KR 2026-01-10, issue 2026-01-05 → backfill 2026-01-10 (KR wins)
	//   link B: KR 2026-01-05, issue 2026-01-10 → backfill 2026-01-10 (issue wins)
	// The fixture is inserted AFTER the down migration (pre-migration
	// shape: no created_at column on the link rows).
	mustID := func(what, q string, args ...any) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx, q, args...).Scan(&id); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		return id
	}
	var authorID, wsID, projID, stateID, obID, krA, krB, issA, issB string
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM workspaces WHERE id = $1::uuid`, wsID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1::uuid`, authorID)
	})

	// Down first: drop the column, proving the fixture below is a
	// genuine pre-migration row.
	if _, err := pool.Exec(ctx, string(down)); err != nil {
		t.Fatalf("down apply: %v", err)
	}
	if linkCreatedAtColumnExists(ctx, pool) {
		t.Fatal("created_at column still present after down, want gone")
	}

	authorID = mustID("author", `INSERT INTO users (email) VALUES ('c47-link-backfill-author@example.com') RETURNING id::text`)
	wsID = mustID("workspace", `INSERT INTO workspaces (name, slug) VALUES ('Link Backfill', 'c47-link-backfill') RETURNING id::text`)
	projID = mustID("project", `INSERT INTO projects (workspace_id, identifier, name) VALUES ($1::uuid, 'LBF', 'Link Backfill') RETURNING id::text`, wsID)
	stateID = mustID("state", `INSERT INTO states (project_id, name, "group", sequence) VALUES ($1::uuid, 'Backlog', 'backlog', 0) RETURNING id::text`, projID)
	krAtA := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	issAtA := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)
	krAtB := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)
	issAtB := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	obID = mustID("objective", `INSERT INTO objectives (workspace_id, title, status) VALUES ($1::uuid, 'Backfill', 'active') RETURNING id::text`, wsID)
	krA = mustID("kr A", `INSERT INTO key_results (objective_id, title, kind, target_value, created_at) VALUES ($1::uuid, 'A', 'issue', 100, $2) RETURNING id::text`, obID, krAtA)
	krB = mustID("kr B", `INSERT INTO key_results (objective_id, title, kind, target_value, created_at) VALUES ($1::uuid, 'B', 'issue', 100, $2) RETURNING id::text`, obID, krAtB)
	issA = mustID("issue A", `INSERT INTO issues (project_id, sequence_id, name, state_id, created_by, created_at) VALUES ($1::uuid, 1, 'A', $2::uuid, $3::uuid, $4) RETURNING id::text`, projID, stateID, authorID, issAtA)
	issB = mustID("issue B", `INSERT INTO issues (project_id, sequence_id, name, state_id, created_by, created_at) VALUES ($1::uuid, 2, 'B', $2::uuid, $3::uuid, $4) RETURNING id::text`, projID, stateID, authorID, issAtB)
	if _, err := pool.Exec(ctx, `INSERT INTO key_result_issues (key_result_id, issue_id) VALUES ($1::uuid, $2::uuid)`, krA, issA); err != nil {
		t.Fatalf("insert link A (pre-migration shape): %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO key_result_issues (key_result_id, issue_id) VALUES ($1::uuid, $2::uuid)`, krB, issB); err != nil {
		t.Fatalf("insert link B (pre-migration shape): %v", err)
	}

	// Up twice: idempotent, and the backfill lands on GREATEST.
	for i := 0; i < 2; i++ {
		if _, err := pool.Exec(ctx, string(up)); err != nil {
			t.Fatalf("up apply %d: %v", i+1, err)
		}
	}
	if !linkCreatedAtColumnExists(ctx, pool) {
		t.Fatal("created_at column missing after up, want present")
	}
	want := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, kr, iss string
	}{{"link A", krA, issA}, {"link B", krB, issB}} {
		var got time.Time
		if err := pool.QueryRow(ctx,
			`SELECT created_at FROM key_result_issues
			 WHERE key_result_id = $1::uuid AND issue_id = $2::uuid`, tc.kr, tc.iss,
		).Scan(&got); err != nil {
			t.Fatalf("%s created_at: %v", tc.name, err)
		}
		if !got.Equal(want) {
			t.Fatalf("%s created_at = %v, want GREATEST = %v", tc.name, got, want)
		}
	}

	// A post-migration link keeps its true insert time: inserting
	// now must NOT be rewritten by a re-applied up (the backfill
	// touches only NULL rows).
	krC := mustID("kr C", `INSERT INTO key_results (objective_id, title, kind, target_value, created_at) VALUES ($1::uuid, 'C', 'issue', 100, $2) RETURNING id::text`, obID, krAtB)
	issC := mustID("issue C", `INSERT INTO issues (project_id, sequence_id, name, state_id, created_by, created_at) VALUES ($1::uuid, 3, 'C', $2::uuid, $3::uuid, $4) RETURNING id::text`, projID, stateID, authorID, issAtB)
	if _, err := pool.Exec(ctx, `INSERT INTO key_result_issues (key_result_id, issue_id) VALUES ($1::uuid, $2::uuid)`, krC, issC); err != nil {
		t.Fatalf("insert link C: %v", err)
	}
	var before time.Time
	if err := pool.QueryRow(ctx,
		`SELECT created_at FROM key_result_issues WHERE key_result_id = $1::uuid`, krC,
	).Scan(&before); err != nil {
		t.Fatalf("link C created_at: %v", err)
	}
	if _, err := pool.Exec(ctx, string(up)); err != nil {
		t.Fatalf("up re-apply (link C guard): %v", err)
	}
	var after time.Time
	if err := pool.QueryRow(ctx,
		`SELECT created_at FROM key_result_issues WHERE key_result_id = $1::uuid`, krC,
	).Scan(&after); err != nil {
		t.Fatalf("link C created_at after re-apply: %v", err)
	}
	if !after.Equal(before) {
		t.Fatalf("link C created_at rewritten by up re-apply: %v → %v, want unchanged", before, after)
	}

	// Down twice: idempotent, column gone.
	for i := 0; i < 2; i++ {
		if _, err := pool.Exec(ctx, string(down)); err != nil {
			t.Fatalf("down apply %d: %v", i+1, err)
		}
	}
	if linkCreatedAtColumnExists(ctx, pool) {
		t.Fatal("created_at column still present after down, want gone")
	}
}
