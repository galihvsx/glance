package store

// C32T0 migration 000054 (comments.resolved_at / resolved_by)
// round-trip test: the real up/down SQL files are applied and rolled
// back against the test database. The columns are restored in cleanup
// so the shared database is left as the full migration set leaves it.
// The FK behavior is verified too: deleting the resolver nulls
// resolved_by (ON DELETE SET NULL) while keeping resolved_at and the
// comment row.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"glance/migrations"
)

func commentResolveColumnsExist(ctx context.Context, pool *pgxpool.Pool) (resolvedAt, resolvedBy bool) {
	if err := pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_name = 'comments' AND column_name = 'resolved_at')`).Scan(&resolvedAt); err != nil {
		return false, false
	}
	if err := pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_name = 'comments' AND column_name = 'resolved_by')`).Scan(&resolvedBy); err != nil {
		return false, false
	}
	return resolvedAt, resolvedBy
}

func TestMigration000054CommentResolveRoundTrip(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	// Full migrate first so workspaces/users exist.
	if err := Migrate(ctx, pool, migrations.FS); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	up, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000054_comment_resolve.up.sql"))
	if err != nil {
		t.Fatalf("read up migration: %v", err)
	}
	down, err := os.ReadFile(filepath.Join("..", "..", "migrations", "000054_comment_resolve.down.sql"))
	if err != nil {
		t.Fatalf("read down migration: %v", err)
	}

	// Leave the DB as the full migration set leaves it (columns present).
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, string(up)); err != nil {
			t.Errorf("cleanup re-apply up: %v", err)
		}
	})

	// Up twice: idempotent, columns present.
	for i := 0; i < 2; i++ {
		if _, err := pool.Exec(ctx, string(up)); err != nil {
			t.Fatalf("up apply %d: %v", i+1, err)
		}
	}
	if at, by := commentResolveColumnsExist(ctx, pool); !at || !by {
		t.Fatalf("resolved columns after up: resolved_at=%v resolved_by=%v, want true/true", at, by)
	}

	// Fixture: author owns the comment; resolver is referenced ONLY
	// via resolved_by, so deleting the resolver isolates the FK rule.
	var authorID, resolverID, wsID, projID, stateID, issueID, commentID string
	mustID := func(what, q string, args ...any) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx, q, args...).Scan(&id); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		return id
	}
	authorID = mustID("author", `INSERT INTO users (email) VALUES ('c32-resolve-author@example.com') RETURNING id::text`)
	resolverID = mustID("resolver", `INSERT INTO users (email) VALUES ('c32-resolve-resolver@example.com') RETURNING id::text`)
	wsID = mustID("workspace", `INSERT INTO workspaces (name, slug) VALUES ('Resolve Mig', 'c32-resolve-mig') RETURNING id::text`)
	projID = mustID("project", `INSERT INTO projects (workspace_id, identifier, name) VALUES ($1::uuid, 'RES', 'Resolve') RETURNING id::text`, wsID)
	stateID = mustID("state", `INSERT INTO states (project_id, name, "group", sequence) VALUES ($1::uuid, 'Backlog', 'backlog', 0) RETURNING id::text`, projID)
	issueID = mustID("issue", `INSERT INTO issues (project_id, sequence_id, name, state_id, created_by) VALUES ($1::uuid, 1, 'Resolve mig', $2::uuid, $3::uuid) RETURNING id::text`, projID, stateID, authorID)
	commentID = mustID("comment", `INSERT INTO comments (issue_id, actor_id, content, resolved_at, resolved_by)
		VALUES ($1::uuid, $2::uuid, '{}'::jsonb, now(), $3::uuid) RETURNING id::text`, issueID, authorID, resolverID)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM workspaces WHERE id = $1::uuid`, wsID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1::uuid`, authorID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1::uuid`, resolverID)
	})

	// Deleting the resolver nulls resolved_by but keeps resolved_at
	// and the comment row (ON DELETE SET NULL, not CASCADE).
	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1::uuid`, resolverID); err != nil {
		t.Fatalf("delete resolver: %v", err)
	}
	var resolvedBy *string
	var resolvedAt *string
	var commentExists bool
	if err := pool.QueryRow(ctx, `SELECT resolved_by::text, resolved_at::text,
		EXISTS (SELECT 1 FROM comments WHERE id = $1::uuid) FROM comments WHERE id = $1::uuid`,
		commentID).Scan(&resolvedBy, &resolvedAt, &commentExists); err != nil {
		t.Fatalf("comment after resolver delete: %v", err)
	}
	if !commentExists {
		t.Fatal("comment row gone after resolver delete, want it kept")
	}
	if resolvedBy != nil {
		t.Fatalf("resolved_by = %q after resolver delete, want NULL", *resolvedBy)
	}
	if resolvedAt == nil {
		t.Fatal("resolved_at is NULL after resolver delete, want it kept")
	}

	// Down twice: idempotent, columns gone.
	for i := 0; i < 2; i++ {
		if _, err := pool.Exec(ctx, string(down)); err != nil {
			t.Fatalf("down apply %d: %v", i+1, err)
		}
	}
	if at, by := commentResolveColumnsExist(ctx, pool); at || by {
		t.Fatalf("resolved columns after down: resolved_at=%v resolved_by=%v, want false/false", at, by)
	}
}
