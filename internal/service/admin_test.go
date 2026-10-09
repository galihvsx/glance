package service

// Admin service tests (C5T0): stats, user listing, promote/demote with
// the self-demote and last-admin guards, deactivate/reactivate with
// session invalidation, workspace overview, and typed-confirmation
// workspace deletion with FK cascades. Real test database, no skips.

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"glance/internal/auth"
	"glance/internal/config"
	"glance/internal/store"
	"glance/migrations"
)

// adminTestUser creates a user with the given admin flag and returns its id.
func adminTestUser(t *testing.T, pool *pgxpool.Pool, email string, isAdmin bool) string {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(),
		`INSERT INTO users (email, is_admin) VALUES ($1, $2) RETURNING id::text`,
		email, isAdmin).Scan(&id)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return id
}

// insertTestSession mints a live session row for userID and returns the
// raw token.
func insertTestSession(t *testing.T, pool *pgxpool.Pool, userID string) string {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	token, err := auth.CreateSessionTx(ctx, tx, userID, "test-agent", "127.0.0.1")
	if err != nil {
		tx.Rollback(ctx)
		t.Fatalf("mint session: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return token
}

// sessionRevoked reports whether every live session of userID is revoked.
func allSessionsRevoked(t *testing.T, pool *pgxpool.Pool, userID string) bool {
	t.Helper()
	var live int
	err := pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM sessions WHERE user_id = $1::uuid AND revoked_at IS NULL`,
		userID).Scan(&live)
	if err != nil {
		t.Fatalf("count live sessions: %v", err)
	}
	return live == 0
}

func TestGetAdminStats(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)

	admin := adminTestUser(t, pool, uniqueTestEmail("stats-admin"), true)
	ws := createTestWorkspace(t, pool, "Stats WS", uniqueTestSlug("stats-ws"), admin)
	proj := createTestProject(t, pool, ws.Slug, admin, "Stats Proj", uniqueTestIdentifier())
	iss := createTestIssue(t, pool, ws.Slug, proj.Identifier, admin, "stats issue")

	stats, err := GetAdminStats(ctx, pool)
	if err != nil {
		t.Fatalf("GetAdminStats: %v", err)
	}
	if stats.Users < 2 || stats.Workspaces < 1 || stats.Projects < 1 || stats.Issues < 1 {
		t.Errorf("stats = %+v, want at least the seeded rows", stats)
	}
	if stats.AttachmentBytes < 0 {
		t.Errorf("attachment_bytes = %d, want >= 0", stats.AttachmentBytes)
	}
	_ = iss
}

func TestListAdminUsersPagination(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)

	prefix := uniqueTestEmail("listu")
	var ids []string
	for i := 0; i < 5; i++ {
		ids = append(ids, adminTestUser(t, pool,
			prefix+"-"+string(rune('a'+i))+"@x.com", i%2 == 0))
	}
	// Give the first user a workspace so workspace_count is exercised.
	createTestWorkspace(t, pool, "ListU WS", uniqueTestSlug("listu-ws"), ids[0])

	users, total, err := ListAdminUsers(ctx, pool, 2, 0)
	if err != nil {
		t.Fatalf("ListAdminUsers: %v", err)
	}
	if total < 5 {
		t.Errorf("total = %d, want >= 5", total)
	}
	if len(users) != 2 {
		t.Fatalf("len(users) = %d, want 2 (limit)", len(users))
	}
	users2, _, err := ListAdminUsers(ctx, pool, 2, 2)
	if err != nil {
		t.Fatalf("ListAdminUsers page 2: %v", err)
	}
	if len(users2) != 2 {
		t.Fatalf("len(page 2) = %d, want 2", len(users2))
	}
	if users[0].ID == users2[0].ID {
		t.Error("pages overlap: offset not applied")
	}
	for _, u := range append(users, users2...) {
		if u.Email == "" || u.CreatedAt.IsZero() {
			t.Errorf("user row missing fields: %+v", u)
		}
	}
	// The workspace creator must show workspace_count >= 1 and is_admin
	// in the listing itself (this exercises the service's aggregation,
	// not just the raw rows). The shared test DB accumulates thousands
	// of users across runs and pages cap at 100 rows, so page through
	// until the seeded row is found or the total is exhausted.
	var creator *AdminUser
	pages := int(total/100) + 2
	for page := 1; page <= pages && creator == nil; page++ {
		batch, _, err := ListAdminUsers(ctx, pool, 100, (page-1)*100)
		if err != nil {
			t.Fatalf("ListAdminUsers page %d: %v", page, err)
		}
		if len(batch) == 0 {
			break
		}
		for i := range batch {
			if batch[i].ID == ids[0] {
				creator = &batch[i]
			}
		}
	}
	if creator == nil {
		t.Fatal("seeded user missing from listing")
	}
	if creator.WorkspaceCount < 1 {
		t.Errorf("listing workspace_count = %d for workspace creator, want >= 1", creator.WorkspaceCount)
	}
	if !creator.IsAdmin {
		t.Error("listing is_admin = false for seeded admin, want true")
	}
}

func TestSetUserAdminPromoteDemote(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)

	adminA := adminTestUser(t, pool, uniqueTestEmail("prom-a"), true)
	adminB := adminTestUser(t, pool, uniqueTestEmail("prom-b"), true)
	plain := adminTestUser(t, pool, uniqueTestEmail("prom-plain"), false)

	// Promote: plain user becomes admin.
	if err := SetUserAdmin(ctx, pool, adminA, plain, true); err != nil {
		t.Fatalf("promote: %v", err)
	}
	var isAdmin bool
	if err := pool.QueryRow(ctx, `SELECT is_admin FROM users WHERE id = $1::uuid`, plain).Scan(&isAdmin); err != nil {
		t.Fatalf("read flag: %v", err)
	}
	if !isAdmin {
		t.Error("promote: is_admin still false")
	}

	// Demote by another admin: allowed while a second admin remains.
	if err := SetUserAdmin(ctx, pool, adminA, adminB, false); err != nil {
		t.Fatalf("demote by peer: %v", err)
	}

	// Unknown user → ErrUserNotFound.
	if err := SetUserAdmin(ctx, pool, adminA, "00000000-0000-0000-0000-000000000000", true); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("unknown user: err = %v, want ErrUserNotFound", err)
	}
	// Malformed UUID → error (not a panic, not a 500-class surprise).
	if err := SetUserAdmin(ctx, pool, adminA, "not-a-uuid", true); err == nil {
		t.Error("malformed uuid: want error, got nil")
	}
}

func TestSetUserAdminSelfDemote409(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)

	admin := adminTestUser(t, pool, uniqueTestEmail("selfdemote"), true)
	if err := SetUserAdmin(ctx, pool, admin, admin, false); !errors.Is(err, ErrAdminSelfDemote) {
		t.Errorf("self-demote: err = %v, want ErrAdminSelfDemote", err)
	}
	// The flag must be untouched.
	var isAdmin bool
	if err := pool.QueryRow(ctx, `SELECT is_admin FROM users WHERE id = $1::uuid`, admin).Scan(&isAdmin); err != nil {
		t.Fatalf("read flag: %v", err)
	}
	if !isAdmin {
		t.Error("self-demote attempt flipped the flag")
	}
	// Self-promote (already admin) is a harmless no-op.
	if err := SetUserAdmin(ctx, pool, admin, admin, true); err != nil {
		t.Errorf("self-promote no-op: %v", err)
	}
}

// TestSetUserAdminLastAdminGuard: demoting the last remaining instance
// admin is refused. The guard is instance-global, so the test needs full
// control of the admin set — it runs against an isolated scratch database
// (same pattern as internal/store's migrate_race_test), not the shared
// test DB whose other admin rows would make "last admin" untestable.
func TestSetUserAdminLastAdminGuard(t *testing.T) {
	ctx := context.Background()
	pool := newIsolatedTestPool(t)

	keeper := adminTestUser(t, pool, "keeper@example.com", true)
	peer := adminTestUser(t, pool, "peer@example.com", false)

	// keeper is the only admin in this database: demoting them is refused.
	if err := SetUserAdmin(ctx, pool, peer, keeper, false); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("demote last admin: err = %v, want ErrLastAdmin", err)
	}
	var stillAdmin bool
	if err := pool.QueryRow(ctx, `SELECT is_admin FROM users WHERE id = $1::uuid`, keeper).Scan(&stillAdmin); err != nil {
		t.Fatalf("read flag: %v", err)
	}
	if !stillAdmin {
		t.Error("refused demote flipped the flag anyway")
	}

	// With a second admin present, the same demote is allowed.
	second := adminTestUser(t, pool, "second@example.com", true)
	if err := SetUserAdmin(ctx, pool, second, keeper, false); err != nil {
		t.Fatalf("demote with peer admin present: %v", err)
	}
}

// newIsolatedTestPool creates a scratch database, migrates it, and
// returns a pool to it. The database is dropped on test cleanup, so the
// test gets full control of global state (the admin set) without
// disturbing — or being disturbed by — other test packages sharing
// glance_test.
func newIsolatedTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Fatal("TEST_DATABASE_URL must be set; refusing to skip")
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL: %v", err)
	}
	dbName := fmt.Sprintf("glance_test_admin_%d_%d", os.Getpid(), testSeq.Add(1))
	adminURL := *u
	adminURL.Path = "/postgres"
	adminPool, err := pgxpool.New(context.Background(), adminURL.String())
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}
	defer adminPool.Close()
	qn := `"` + strings.ReplaceAll(dbName, `"`, `""`) + `"`
	if _, err := adminPool.Exec(context.Background(), `CREATE DATABASE `+qn+` OWNER glance`); err != nil {
		t.Fatalf("create scratch db: %v", err)
	}
	t.Cleanup(func() {
		p, err := pgxpool.New(context.Background(), adminURL.String())
		if err != nil {
			t.Fatalf("admin pool (cleanup): %v", err)
		}
		defer p.Close()
		if _, err := p.Exec(context.Background(), `DROP DATABASE IF EXISTS `+qn); err != nil {
			t.Fatalf("drop scratch db (cleanup): %v", err)
		}
	})
	scratchURL := *u
	scratchURL.Path = "/" + dbName
	pool, err := store.NewPool(context.Background(), &config.Config{DatabaseURL: scratchURL.String()})
	if err != nil {
		t.Fatalf("scratch pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := store.Migrate(context.Background(), pool, migrations.FS); err != nil {
		t.Fatalf("migrate scratch db: %v", err)
	}
	return pool
}

func TestDeactivateUser(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)

	admin := adminTestUser(t, pool, uniqueTestEmail("deact-admin"), true)
	victim := adminTestUser(t, pool, uniqueTestEmail("deact-victim"), false)
	token := insertTestSession(t, pool, victim)

	if err := DeactivateUser(ctx, pool, admin, victim); err != nil {
		t.Fatalf("DeactivateUser: %v", err)
	}

	var isActive bool
	if err := pool.QueryRow(ctx, `SELECT is_active FROM users WHERE id = $1::uuid`, victim).Scan(&isActive); err != nil {
		t.Fatalf("read is_active: %v", err)
	}
	if isActive {
		t.Error("is_active still true after deactivation")
	}
	if !allSessionsRevoked(t, pool, victim) {
		t.Error("live sessions remain after deactivation")
	}
	// The old token is dead: session auth rejects it.
	if _, err := auth.AuthenticateSession(ctx, pool, token); !errors.Is(err, auth.ErrSessionInvalid) {
		t.Errorf("AuthenticateSession after deactivation: err = %v, want ErrSessionInvalid", err)
	}

	// Unknown user → ErrUserNotFound.
	if err := DeactivateUser(ctx, pool, admin, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("unknown user: err = %v, want ErrUserNotFound", err)
	}
	// Deactivating an already-deactivated user is idempotent, not an error.
	if err := DeactivateUser(ctx, pool, admin, victim); err != nil {
		t.Errorf("second deactivate: %v, want nil (idempotent)", err)
	}
}

func TestDeactivateUserSelfRefused(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)

	admin := adminTestUser(t, pool, uniqueTestEmail("selfdeact"), true)
	if err := DeactivateUser(ctx, pool, admin, admin); !errors.Is(err, ErrAdminSelfDeactivate) {
		t.Errorf("self-deactivate: err = %v, want ErrAdminSelfDeactivate", err)
	}
}

func TestReactivateUser(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)

	admin := adminTestUser(t, pool, uniqueTestEmail("react-admin"), true)
	victim := adminTestUser(t, pool, uniqueTestEmail("react-victim"), false)
	if err := DeactivateUser(ctx, pool, admin, victim); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	if err := ReactivateUser(ctx, pool, victim); err != nil {
		t.Fatalf("ReactivateUser: %v", err)
	}
	var isActive bool
	if err := pool.QueryRow(ctx, `SELECT is_active FROM users WHERE id = $1::uuid`, victim).Scan(&isActive); err != nil {
		t.Fatalf("read is_active: %v", err)
	}
	if !isActive {
		t.Error("is_active still false after reactivation")
	}
	// Revoked sessions stay revoked: the user must log in fresh.
	if !allSessionsRevoked(t, pool, victim) {
		t.Error("reactivation resurrected revoked sessions")
	}
	// Idempotent on an active account.
	if err := ReactivateUser(ctx, pool, victim); err != nil {
		t.Errorf("second reactivate: %v, want nil", err)
	}
	// Unknown user → ErrUserNotFound.
	if err := ReactivateUser(ctx, pool, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("unknown user: err = %v, want ErrUserNotFound", err)
	}
}

func TestListAdminWorkspaces(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)

	admin := adminTestUser(t, pool, uniqueTestEmail("wso-admin"), true)
	member := adminTestUser(t, pool, uniqueTestEmail("wso-member"), false)
	ws := createTestWorkspace(t, pool, "Usage WS", uniqueTestSlug("usage-ws"), admin)
	if err := UpsertMember(ctx, pool, ws.Slug, admin, member, RoleMember); err != nil {
		t.Fatalf("add member: %v", err)
	}
	proj := createTestProject(t, pool, ws.Slug, admin, "Usage Proj", uniqueTestIdentifier())
	createTestIssue(t, pool, ws.Slug, proj.Identifier, admin, "usage issue 1")
	createTestIssue(t, pool, ws.Slug, proj.Identifier, admin, "usage issue 2")

	_, total, err := ListAdminWorkspaces(ctx, pool, 100, 0)
	if err != nil {
		t.Fatalf("ListAdminWorkspaces: %v", err)
	}
	if total < 1 {
		t.Fatalf("total = %d, want >= 1", total)
	}
	var found *AdminWorkspace
	// The listing orders by created_at ASC, so a freshly created workspace
	// sits on the LAST page. The shared test DB accumulates workspaces
	// across runs (well past 50 pages), so scan the last two pages (the
	// second guards against created_at ties with concurrent test rows).
	lastPage := int(total-1)/100 + 1
	for page := lastPage - 1; page <= lastPage && found == nil; page++ {
		if page < 1 {
			continue
		}
		batch, _, err := ListAdminWorkspaces(ctx, pool, 100, (page-1)*100)
		if err != nil {
			t.Fatalf("ListAdminWorkspaces page %d: %v", page, err)
		}
		for i := range batch {
			if batch[i].ID == ws.ID {
				found = &batch[i]
			}
		}
	}
	if found == nil {
		t.Fatalf("workspace %s missing from listing", ws.Slug)
	}
	if found.MemberCount != 2 {
		t.Errorf("member_count = %d, want 2", found.MemberCount)
	}
	if found.ProjectCount != 1 {
		t.Errorf("project_count = %d, want 1", found.ProjectCount)
	}
	if found.IssueCount != 2 {
		t.Errorf("issue_count = %d, want 2", found.IssueCount)
	}
	if found.Slug != ws.Slug || found.Name != "Usage WS" || found.CreatedAt.IsZero() {
		t.Errorf("workspace fields wrong: %+v", found)
	}
}

func TestDeleteWorkspaceAsAdmin(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)

	admin := adminTestUser(t, pool, uniqueTestEmail("delws-admin"), true)
	ws := createTestWorkspace(t, pool, "Delete Me WS", uniqueTestSlug("delws"), admin)
	proj := createTestProject(t, pool, ws.Slug, admin, "Delete Me Proj", uniqueTestIdentifier())
	iss := createTestIssue(t, pool, ws.Slug, proj.Identifier, admin, "doomed issue")

	// Wrong confirmation → ErrAdminConfirmMismatch, workspace survives.
	if err := DeleteWorkspaceAsAdmin(ctx, pool, ws.Slug, "Wrong Name"); !errors.Is(err, ErrAdminConfirmMismatch) {
		t.Fatalf("wrong confirm: err = %v, want ErrAdminConfirmMismatch", err)
	}
	// Missing confirmation (empty) never matches a real name.
	if err := DeleteWorkspaceAsAdmin(ctx, pool, ws.Slug, ""); !errors.Is(err, ErrAdminConfirmMismatch) {
		t.Fatalf("empty confirm: err = %v, want ErrAdminConfirmMismatch", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM workspaces WHERE id = $1::uuid`, ws.ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("workspace should survive failed confirmations (n=%d, err=%v)", n, err)
	}

	// Unknown workspace → ErrNotFound.
	if err := DeleteWorkspaceAsAdmin(ctx, pool, uniqueTestSlug("nope"), "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown workspace: err = %v, want ErrNotFound", err)
	}

	// Correct confirmation by slug: deletes, and the FK graph cascades.
	if err := DeleteWorkspaceAsAdmin(ctx, pool, ws.Slug, "Delete Me WS"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	for _, tc := range []struct {
		table, col, val string
	}{
		{"workspaces", "id", ws.ID},
		{"projects", "id", proj.ID},
		{"issues", "id", iss.ID},
		{"workspace_members", "workspace_id", ws.ID},
	} {
		var count int
		q := `SELECT COUNT(*) FROM ` + tc.table + ` WHERE ` + tc.col + ` = $1::uuid`
		if err := pool.QueryRow(ctx, q, tc.val).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", tc.table, err)
		}
		if count != 0 {
			t.Errorf("%s rows remaining = %d, want 0 (cascade)", tc.table, count)
		}
	}

	// Resolution by id works too.
	ws2 := createTestWorkspace(t, pool, "Delete Me Too", uniqueTestSlug("delws2"), admin)
	if err := DeleteWorkspaceAsAdmin(ctx, pool, ws2.ID, "Delete Me Too"); err != nil {
		t.Fatalf("delete by id: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM workspaces WHERE id = $1::uuid`, ws2.ID).Scan(&n); err != nil || n != 0 {
		t.Errorf("workspace by id should be gone (n=%d, err=%v)", n, err)
	}
}
