package service

// Workspace + members + roles tests (Task 11): CRUD, slug uniqueness
// (case-insensitive via CITEXT), role checks (admin 20 > member 15 >
// guest 5; only admin manages members), and the last-admin lockout
// guard. All tests run against the real test database — no skips.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"glance/internal/config"
	"glance/internal/store"
	"glance/migrations"
)

// testSeq hands out process-unique numbers: the test database persists
// across runs and is never truncated, so every key a test inserts must be
// unique across runs, not just within one.
var testSeq atomic.Int64

func uniqueTestEmail(prefix string) string {
	return fmt.Sprintf("%s-%d-%d@example.com", prefix, os.Getpid(), testSeq.Add(1))
}

func uniqueTestSlug(prefix string) string {
	return fmt.Sprintf("%s-%d-%d", prefix, os.Getpid(), testSeq.Add(1))
}

func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Fatal("TEST_DATABASE_URL must be set; refusing to skip")
	}
	pool, err := store.NewPool(context.Background(), &config.Config{DatabaseURL: url})
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func migrateTestDB(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if err := store.Migrate(context.Background(), pool, migrations.FS); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
}

// createTestUser inserts a user row directly and returns its id.
func createTestUser(t *testing.T, pool *pgxpool.Pool, email string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(),
		`INSERT INTO users (email) VALUES ($1) RETURNING id::text`, email).Scan(&id)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return id
}

// createTestWorkspace creates a workspace with the given user as admin.
func createTestWorkspace(t *testing.T, pool *pgxpool.Pool, name, slug, creatorID string) *Workspace {
	t.Helper()
	ws, err := CreateWorkspace(context.Background(), pool, name, slug, creatorID)
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	return ws
}

func TestCreateWorkspace(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	creator := createTestUser(t, pool, uniqueTestEmail("ws-creator"))
	wsSlug := uniqueTestSlug("acme")
	ws := createTestWorkspace(t, pool, "Acme Corp", wsSlug, creator)

	if ws.Slug != wsSlug || ws.Name != "Acme Corp" || ws.ID == "" {
		t.Fatalf("workspace = %+v, want slug=%s name='Acme Corp' with id", ws, wsSlug)
	}

	// Creator must be admin (role 20).
	_, role, err := GetWorkspace(ctx, pool, wsSlug, creator)
	if err != nil {
		t.Fatalf("GetWorkspace: %v", err)
	}
	if role != RoleAdmin {
		t.Fatalf("creator role = %d, want %d (admin)", role, RoleAdmin)
	}
}

func TestCreateWorkspaceSlugConflict(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	creator := createTestUser(t, pool, uniqueTestEmail("ws-conflict"))
	conflictSlug := uniqueTestSlug("acme-conflict")
	createTestWorkspace(t, pool, "Acme", conflictSlug, creator)

	_, err := CreateWorkspace(ctx, pool, "Acme Clone", conflictSlug, creator)
	if !errors.Is(err, ErrSlugConflict) {
		t.Fatalf("duplicate slug: err = %v, want ErrSlugConflict", err)
	}
}

func TestCreateWorkspaceSlugConflictCaseInsensitive(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	// Bypass slug validation with raw SQL: CITEXT uniqueness must catch
	// "ACME-CI" vs "acme-ci" even though the service only accepts
	// lowercase input.
	ciSlug := uniqueTestSlug("acme-ci")
	if _, err := pool.Exec(ctx,
		`INSERT INTO workspaces (slug, name) VALUES ($1, 'raw')`, strings.ToUpper(ciSlug)); err != nil {
		t.Fatalf("raw insert: %v", err)
	}
	creator := createTestUser(t, pool, uniqueTestEmail("ws-ci"))
	_, err := CreateWorkspace(ctx, pool, "Acme CI", ciSlug, creator)
	if !errors.Is(err, ErrSlugConflict) {
		t.Fatalf("case-insensitive duplicate: err = %v, want ErrSlugConflict", err)
	}
}

func TestCreateWorkspaceInvalidSlug(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	creator := createTestUser(t, pool, uniqueTestEmail("ws-badslug"))

	for _, slug := range []string{"", "ACME", "has space", "under_score", "-leading", "trailing-", "a--b", "UPPER"} {
		_, err := CreateWorkspace(ctx, pool, "Bad", slug, creator)
		if !errors.Is(err, ErrInvalidSlug) {
			t.Errorf("slug %q: err = %v, want ErrInvalidSlug", slug, err)
		}
	}
}

func TestListWorkspacesOnlyOwn(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	u1 := createTestUser(t, pool, uniqueTestEmail("ws-list1"))
	u2 := createTestUser(t, pool, uniqueTestEmail("ws-list2"))
	u1Slug := uniqueTestSlug("u1-space")
	createTestWorkspace(t, pool, "U1 Space", u1Slug, u1)

	got, err := ListWorkspaces(ctx, pool, u1)
	if err != nil {
		t.Fatalf("ListWorkspaces: %v", err)
	}
	if len(got) != 1 || got[0].Slug != u1Slug {
		t.Fatalf("u1 workspaces = %+v, want [%s]", got, u1Slug)
	}

	got, err = ListWorkspaces(ctx, pool, u2)
	if err != nil {
		t.Fatalf("ListWorkspaces: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("u2 workspaces = %+v, want [] (not a member)", got)
	}
}

func TestGetWorkspaceNotMember(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	u1 := createTestUser(t, pool, uniqueTestEmail("ws-get1"))
	u2 := createTestUser(t, pool, uniqueTestEmail("ws-get2"))
	privSlug := uniqueTestSlug("ws-private")
	createTestWorkspace(t, pool, "Private", privSlug, u1)

	// Non-member gets 404-shaped ErrNotFound — never a hint the workspace exists.
	if _, _, err := GetWorkspace(ctx, pool, privSlug, u2); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-member get: err = %v, want ErrNotFound", err)
	}
	if _, _, err := GetWorkspace(ctx, pool, "no-such-ws", u1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing ws get: err = %v, want ErrNotFound", err)
	}
}

func TestUpdateWorkspaceAdminOnly(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	admin := createTestUser(t, pool, uniqueTestEmail("ws-upd-admin"))
	guest := createTestUser(t, pool, uniqueTestEmail("ws-upd-guest"))
	updSlug := uniqueTestSlug("ws-upd")
	createTestWorkspace(t, pool, "Old Name", updSlug, admin)
	if err := UpsertMember(ctx, pool, updSlug, admin, guest, RoleGuest); err != nil {
		t.Fatalf("UpsertMember: %v", err)
	}

	if _, err := UpdateWorkspace(ctx, pool, updSlug, guest, "Hacked"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest update: err = %v, want ErrForbidden", err)
	}

	if _, err := UpdateWorkspace(ctx, pool, updSlug, admin, "New Name"); err != nil {
		t.Fatalf("admin update: %v", err)
	}
	ws, _, err := GetWorkspace(ctx, pool, updSlug, admin)
	if err != nil {
		t.Fatalf("GetWorkspace: %v", err)
	}
	if ws.Name != "New Name" {
		t.Fatalf("name = %q, want %q", ws.Name, "New Name")
	}
}

func TestGuestCannotManageMembers(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	admin := createTestUser(t, pool, uniqueTestEmail("ws-mem-admin"))
	guest := createTestUser(t, pool, uniqueTestEmail("ws-mem-guest"))
	target := createTestUser(t, pool, uniqueTestEmail("ws-mem-target"))
	memSlug := uniqueTestSlug("ws-members")
	createTestWorkspace(t, pool, "Members", memSlug, admin)
	if err := UpsertMember(ctx, pool, memSlug, admin, guest, RoleGuest); err != nil {
		t.Fatalf("UpsertMember: %v", err)
	}

	if err := UpsertMember(ctx, pool, memSlug, guest, target, RoleMember); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest add member: err = %v, want ErrForbidden", err)
	}
	if err := RemoveMember(ctx, pool, memSlug, guest, admin); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest remove member: err = %v, want ErrForbidden", err)
	}

	// Member (15) cannot manage members either — only admin (20).
	member := createTestUser(t, pool, uniqueTestEmail("ws-mem-member"))
	if err := UpsertMember(ctx, pool, memSlug, admin, member, RoleMember); err != nil {
		t.Fatalf("UpsertMember: %v", err)
	}
	if err := UpsertMember(ctx, pool, memSlug, member, target, RoleMember); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member add member: err = %v, want ErrForbidden", err)
	}
}

func TestCannotRemoveLastAdmin(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	admin := createTestUser(t, pool, uniqueTestEmail("ws-lock-admin"))
	other := createTestUser(t, pool, uniqueTestEmail("ws-lock-other"))
	lockSlug := uniqueTestSlug("ws-lockout")
	createTestWorkspace(t, pool, "Lockout", lockSlug, admin)

	// Sole admin cannot remove themselves.
	if err := RemoveMember(ctx, pool, lockSlug, admin, admin); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("remove sole admin: err = %v, want ErrLastAdmin", err)
	}
	// Sole admin cannot demote themselves either.
	if err := UpsertMember(ctx, pool, lockSlug, admin, admin, RoleMember); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("demote sole admin: err = %v, want ErrLastAdmin", err)
	}

	// With a second admin, self-removal is allowed.
	if err := UpsertMember(ctx, pool, lockSlug, admin, other, RoleAdmin); err != nil {
		t.Fatalf("UpsertMember second admin: %v", err)
	}
	if err := RemoveMember(ctx, pool, lockSlug, admin, admin); err != nil {
		t.Fatalf("remove self with second admin: %v", err)
	}
	if _, _, err := GetWorkspace(ctx, pool, lockSlug, admin); !errors.Is(err, ErrNotFound) {
		t.Fatalf("removed admin get: err = %v, want ErrNotFound", err)
	}
}

func TestUpsertMemberInvalidRole(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	admin := createTestUser(t, pool, uniqueTestEmail("ws-role-admin"))
	target := createTestUser(t, pool, uniqueTestEmail("ws-role-target"))
	roleSlug := uniqueTestSlug("ws-roles")
	createTestWorkspace(t, pool, "Roles", roleSlug, admin)

	for _, role := range []int{0, 6, 19, 21, 100} {
		if err := UpsertMember(ctx, pool, roleSlug, admin, target, role); !errors.Is(err, ErrInvalidRole) {
			t.Errorf("role %d: err = %v, want ErrInvalidRole", role, err)
		}
	}
}

// randomAbsentUserID returns a UUID guaranteed to match no user row —
// gen_random_uuid never collides with an inserted id in practice.
func randomAbsentUserID(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `SELECT gen_random_uuid()::text`).Scan(&id); err != nil {
		t.Fatalf("gen_random_uuid: %v", err)
	}
	return id
}

func TestUpsertMemberUnknownUser(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	admin := createTestUser(t, pool, uniqueTestEmail("ws-nouser-admin"))
	nfSlug := uniqueTestSlug("ws-nouser")
	createTestWorkspace(t, pool, "NoUser", nfSlug, admin)

	// A confirmed admin of a confirmed-existing workspace typoing a
	// user_id must get ErrUserNotFound — never the workspace-shaped
	// ErrNotFound.
	unknown := randomAbsentUserID(t, pool)
	err := UpsertMember(ctx, pool, nfSlug, admin, unknown, RoleMember)
	if !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("unknown user upsert: err = %v, want ErrUserNotFound", err)
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown user upsert: err = %v, must not be ErrNotFound", err)
	}
}

func TestRemoveMemberNonMember(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	admin := createTestUser(t, pool, uniqueTestEmail("ws-nomem-admin"))
	outsider := createTestUser(t, pool, uniqueTestEmail("ws-nomem-outsider"))
	nmSlug := uniqueTestSlug("ws-nomember")
	createTestWorkspace(t, pool, "NoMember", nmSlug, admin)

	// Existing user, not a member → ErrMemberNotFound.
	err := RemoveMember(ctx, pool, nmSlug, admin, outsider)
	if !errors.Is(err, ErrMemberNotFound) {
		t.Fatalf("remove non-member: err = %v, want ErrMemberNotFound", err)
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("remove non-member: err = %v, must not be ErrNotFound", err)
	}

	// Nonexistent user entirely → also ErrMemberNotFound (from the admin's
	// view they tried to remove a member that isn't there).
	err = RemoveMember(ctx, pool, nmSlug, admin, randomAbsentUserID(t, pool))
	if !errors.Is(err, ErrMemberNotFound) {
		t.Fatalf("remove unknown user: err = %v, want ErrMemberNotFound", err)
	}
}
