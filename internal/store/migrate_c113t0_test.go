package store

// C113T0 migration 000081 (workspace-scoped saved views) round-trip
// test: the real up/down SQL files are applied and rolled back
// against the test database. Project views created before the
// round trip survive it with their rows intact, the project-scope
// name uniqueness survives as a partial unique index, and the new
// workspace-scope partial unique index enforces per-(user, workspace)
// uniqueness. Workspace-scoped rows are deleted by the down migration
// (they have no project to restore to — documented downgrade loss).
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

func c113IndexDef(t *testing.T, ctx context.Context, pool *pgxpool.Pool, name string) (string, bool) {
	t.Helper()
	var def string
	err := pool.QueryRow(ctx,
		`SELECT indexdef FROM pg_indexes
		  WHERE schemaname = 'public' AND tablename = 'issue_views'
		    AND indexname = $1`, name).Scan(&def)
	if err != nil {
		return "", false
	}
	return def, true
}

func c113ColumnNullable(t *testing.T, ctx context.Context, pool *pgxpool.Pool, column string) (nullable bool, exists bool) {
	t.Helper()
	var isNullable string
	err := pool.QueryRow(ctx,
		`SELECT is_nullable FROM information_schema.columns
		  WHERE table_schema = 'public' AND table_name = 'issue_views'
		    AND column_name = $1`, column).Scan(&isNullable)
	if err != nil {
		return false, false
	}
	return isNullable == "YES", true
}

func TestMigration000081WorkspaceScopedViews(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	// Full migrate first so the base schema (including 000081) exists.
	if err := Migrate(ctx, pool, migrations.FS); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	up, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000081_issue_views_workspace_scope.up.sql"))
	if err != nil {
		t.Fatalf("read up migration: %v", err)
	}
	down, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000081_issue_views_workspace_scope.down.sql"))
	if err != nil {
		t.Fatalf("read down migration: %v", err)
	}

	// Leave the DB as the full migration set leaves it (up applied).
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, string(up)); err != nil {
			t.Errorf("cleanup re-apply up: %v", err)
		}
	})

	// Fixture rows: one user, one workspace, one project, one
	// project-scoped view and one workspace-scoped view.
	var userID, wsID, projectID, projectViewID, wsViewID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email) VALUES ('c113-mig@example.com') RETURNING id::text`).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO workspaces (slug, name) VALUES ('c113-mig-ws', 'C113 Mig') RETURNING id::text`).Scan(&wsID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO projects (workspace_id, identifier, name) VALUES ($1::uuid, 'C113', 'C113') RETURNING id::text`, wsID).Scan(&projectID); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO issue_views (user_id, project_id, name, filters) VALUES ($1::uuid, $2::uuid, 'Proj view', '{"q":"bug"}') RETURNING id::text`,
		userID, projectID).Scan(&projectViewID); err != nil {
		t.Fatalf("insert project view: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO issue_views (user_id, workspace_id, name, filters) VALUES ($1::uuid, $2::uuid, 'WS view', '{"pql":"x"}') RETURNING id::text`,
		userID, wsID).Scan(&wsViewID); err != nil {
		t.Fatalf("insert workspace view: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM issue_views WHERE id IN ($1::uuid, $2::uuid)`, projectViewID, wsViewID)
		_, _ = pool.Exec(ctx, `DELETE FROM projects WHERE id = $1::uuid`, projectID)
		_, _ = pool.Exec(ctx, `DELETE FROM workspaces WHERE id = $1::uuid`, wsID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1::uuid`, userID)
	})

	// Post-up shape (full migrate already applied 000081): both
	// partial unique indexes, the list index, the CHECK, and a
	// nullable project_id.
	if def, ok := c113IndexDef(t, ctx, pool, "idx_issue_views_user_project_name"); !ok || !strings.Contains(def, "project_id IS NOT NULL") {
		t.Fatalf("project partial index = %q (present=%v), want partial on project_id IS NOT NULL", def, ok)
	}
	if def, ok := c113IndexDef(t, ctx, pool, "idx_issue_views_user_workspace_name"); !ok || !strings.Contains(def, "workspace_id IS NOT NULL") {
		t.Fatalf("workspace partial index = %q (present=%v), want partial on workspace_id IS NOT NULL", def, ok)
	}
	if _, ok := c113IndexDef(t, ctx, pool, "idx_issue_views_workspace_user"); !ok {
		t.Fatal("workspace list index missing after up")
	}
	var checkExists bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_issue_views_exactly_one_scope')`).Scan(&checkExists); err != nil || !checkExists {
		t.Fatalf("exactly-one-scope CHECK missing after up (err=%v)", err)
	}
	if nullable, exists := c113ColumnNullable(t, ctx, pool, "project_id"); !exists || !nullable {
		t.Fatalf("project_id nullable=%v exists=%v after up, want nullable", nullable, exists)
	}
	// A both-scopes row and a neither-scope row both violate the CHECK.
	if _, err := pool.Exec(ctx,
		`INSERT INTO issue_views (user_id, project_id, workspace_id, name, filters) VALUES ($1::uuid, $2::uuid, $3::uuid, 'Both', '{}')`,
		userID, projectID, wsID); err == nil {
		t.Fatal("both-scopes insert succeeded, want CHECK violation")
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO issue_views (user_id, name, filters) VALUES ($1::uuid, 'Neither', '{}')`,
		userID); err == nil {
		t.Fatal("neither-scope insert succeeded, want CHECK violation")
	}

	// Down twice: idempotent, project view survives, workspace view
	// is deleted (documented downgrade loss), workspace column gone,
	// project_id NOT NULL again, original plain unique index back.
	for i := 0; i < 2; i++ {
		if _, err := pool.Exec(ctx, string(down)); err != nil {
			t.Fatalf("down apply %d: %v", i+1, err)
		}
	}
	if _, exists := c113ColumnNullable(t, ctx, pool, "workspace_id"); exists {
		t.Fatal("workspace_id column still present after down, want gone")
	}
	if nullable, exists := c113ColumnNullable(t, ctx, pool, "project_id"); !exists || nullable {
		t.Fatalf("project_id nullable=%v exists=%v after down, want NOT NULL", nullable, exists)
	}
	if def, ok := c113IndexDef(t, ctx, pool, "idx_issue_views_user_project_name"); !ok || strings.Contains(def, "WHERE") {
		t.Fatalf("project index after down = %q (present=%v), want the original plain unique index", def, ok)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM issue_views WHERE id = $1::uuid`, projectViewID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("project view rows after down = %d, err=%v; want 1", n, err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM issue_views WHERE id = $1::uuid`, wsViewID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("workspace view rows after down = %d, err=%v; want 0 (downgrade loss)", n, err)
	}

	// Up twice: idempotent, project view still there, and the
	// workspace partial unique index enforces uniqueness again.
	for i := 0; i < 2; i++ {
		if _, err := pool.Exec(ctx, string(up)); err != nil {
			t.Fatalf("up apply %d: %v", i+1, err)
		}
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM issue_views WHERE id = $1::uuid`, projectViewID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("project view rows after up = %d, err=%v; want 1", n, err)
	}
	// Duplicate project name (case-insensitive) rejected by the
	// partial index.
	if _, err := pool.Exec(ctx,
		`INSERT INTO issue_views (user_id, project_id, name, filters) VALUES ($1::uuid, $2::uuid, 'PROJ VIEW', '{}')`,
		userID, projectID); err == nil {
		t.Fatal("duplicate project view name succeeded after up, want unique violation")
	}
	// Workspace views work again, and their own uniqueness holds.
	var wsViewID2 string
	if err := pool.QueryRow(ctx,
		`INSERT INTO issue_views (user_id, workspace_id, name, filters) VALUES ($1::uuid, $2::uuid, 'WS view', '{"pql":"x"}') RETURNING id::text`,
		userID, wsID).Scan(&wsViewID2); err != nil {
		t.Fatalf("re-insert workspace view after up: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO issue_views (user_id, workspace_id, name, filters) VALUES ($1::uuid, $2::uuid, 'ws VIEW', '{}')`,
		userID, wsID); err == nil {
		t.Fatal("duplicate workspace view name succeeded after up, want unique violation")
	}
	_, _ = pool.Exec(ctx, `DELETE FROM issue_views WHERE id = $1::uuid`, wsViewID2)
}
