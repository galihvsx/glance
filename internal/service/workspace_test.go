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
	"sync"
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

	if _, err := UpdateWorkspace(ctx, pool, updSlug, guest, "Hacked", nil); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest update: err = %v, want ErrForbidden", err)
	}

	if _, err := UpdateWorkspace(ctx, pool, updSlug, admin, "New Name", nil); err != nil {
		t.Fatalf("admin update: %v", err)
	}
	ws, _, err := GetWorkspace(ctx, pool, updSlug, admin)
	if err != nil {
		t.Fatalf("GetWorkspace: %v", err)
	}
	if ws.Name != "New Name" {
		t.Fatalf("name = %q, want %q", ws.Name, "New Name")
	}

	// Slug change: valid new slug persists; invalid and taken slugs fail.
	newSlug := uniqueTestSlug("ws-upd-new")
	ws2, err := UpdateWorkspace(ctx, pool, updSlug, admin, "New Name", &newSlug)
	if err != nil {
		t.Fatalf("slug update: %v", err)
	}
	if ws2.Slug != newSlug {
		t.Fatalf("slug = %q, want %q", ws2.Slug, newSlug)
	}
	bad := "Bad_Slug!!"
	if _, err := UpdateWorkspace(ctx, pool, newSlug, admin, "New Name", &bad); !errors.Is(err, ErrInvalidSlug) {
		t.Fatalf("invalid slug: err = %v, want ErrInvalidSlug", err)
	}
	taken := uniqueTestSlug("ws-upd-taken")
	createTestWorkspace(t, pool, "Taken", taken, admin)
	if _, err := UpdateWorkspace(ctx, pool, newSlug, admin, "New Name", &taken); !errors.Is(err, ErrSlugConflict) {
		t.Fatalf("taken slug: err = %v, want ErrSlugConflict", err)
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

func TestDeleteWorkspace(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	admin := createTestUser(t, pool, uniqueTestEmail("ws-del-admin"))
	guest := createTestUser(t, pool, uniqueTestEmail("ws-del-guest"))
	delSlug := uniqueTestSlug("ws-del")
	createTestWorkspace(t, pool, "Delete Me", delSlug, admin)
	if err := UpsertMember(ctx, pool, delSlug, admin, guest, RoleGuest); err != nil {
		t.Fatalf("UpsertMember: %v", err)
	}

	// Non-admin → forbidden; workspace survives.
	if err := DeleteWorkspace(ctx, pool, delSlug, guest); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest delete: err = %v, want ErrForbidden", err)
	}
	if _, _, err := GetWorkspace(ctx, pool, delSlug, admin); err != nil {
		t.Fatalf("workspace should survive guest delete: %v", err)
	}

	// Admin → gone; members cascade with it.
	if err := DeleteWorkspace(ctx, pool, delSlug, admin); err != nil {
		t.Fatalf("admin delete: %v", err)
	}
	if _, _, err := GetWorkspace(ctx, pool, delSlug, admin); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted workspace lookup: err = %v, want ErrNotFound", err)
	}
}

// TestRemoveMemberConcurrentLastAdmin (C3T8): 20 parallel removals
// targeting both admins of a 2-admin workspace must never leave the
// workspace with zero admins. Without the FOR UPDATE lock on the
// membership rows, two concurrent check-then-delete pairs could each
// observe 2 admins and remove both.
func TestRemoveMemberConcurrentLastAdmin(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	adminA := createTestUser(t, pool, uniqueTestEmail("c3t8-raceA"))
	adminB := createTestUser(t, pool, uniqueTestEmail("c3t8-raceB"))
	raceSlug := uniqueTestSlug("c3t8-race")
	createTestWorkspace(t, pool, "Race", raceSlug, adminA)
	if err := UpsertMember(ctx, pool, raceSlug, adminA, adminB, RoleAdmin); err != nil {
		t.Fatalf("UpsertMember adminB: %v", err)
	}

	const n = 20
	var wg sync.WaitGroup
	var succeeded atomic.Int64
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			target := adminA
			if i%2 == 1 {
				target = adminB
			}
			// Actor stays adminA. Once adminA removes themself, their
			// later calls fail with ErrNotFound — expected; the
			// assertion is only that the workspace never ends up
			// with zero admins.
			if err := RemoveMember(ctx, pool, raceSlug, adminA, target); err == nil {
				succeeded.Add(1)
			}
		}(i)
	}
	wg.Wait()

	if succeeded.Load() == 0 {
		t.Fatal("no removal succeeded; the test did not exercise the race")
	}

	var admins int
	err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM workspace_members m
		 JOIN workspaces w ON w.id = m.workspace_id
		 WHERE w.slug = $1 AND m.role = $2`,
		raceSlug, RoleAdmin).Scan(&admins)
	if err != nil {
		t.Fatalf("final admin count: %v", err)
	}
	if admins < 1 {
		t.Fatalf("workspace left with %d admins; the last-admin guard was defeated by the race", admins)
	}
}
func inviteResultsByEmail(results []InviteResult) map[string]string {
	m := make(map[string]string, len(results))
	for _, r := range results {
		m[r.Email] = r.Status
	}
	return m
}

func TestInviteMembersHappyPath(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	adminEmail := uniqueTestEmail("ws-inv-admin")
	admin := createTestUser(t, pool, adminEmail)
	targetEmail := uniqueTestEmail("ws-inv-target")
	createTestUser(t, pool, targetEmail)
	invSlug := uniqueTestSlug("ws-invite")
	createTestWorkspace(t, pool, "Invite", invSlug, admin)

	results, err := InviteMembers(ctx, pool, invSlug, admin, []string{targetEmail}, 0)
	if err != nil {
		t.Fatalf("InviteMembers: %v", err)
	}
	if got := inviteResultsByEmail(results)[targetEmail]; got != InviteStatusInvited {
		t.Fatalf("status = %q, want %q", got, InviteStatusInvited)
	}

	// The target is now a member with the default (member) role.
	var role int
	if err := pool.QueryRow(ctx,
		`SELECT m.role FROM workspace_members m
		 JOIN workspaces w ON w.id = m.workspace_id
		 JOIN users u ON u.id = m.user_id
		 WHERE w.slug = $1 AND u.email = $2`,
		invSlug, targetEmail).Scan(&role); err != nil {
		t.Fatalf("member row: %v", err)
	}
	if role != RoleMember {
		t.Fatalf("role = %d, want %d (default member)", role, RoleMember)
	}
}

func TestInviteMembersNotRegistered(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	admin := createTestUser(t, pool, uniqueTestEmail("ws-invnr-admin"))
	targetEmail := uniqueTestEmail("ws-invnr-target")
	createTestUser(t, pool, targetEmail)
	nrSlug := uniqueTestSlug("ws-invitenr")
	createTestWorkspace(t, pool, "InviteNR", nrSlug, admin)

	ghost := uniqueTestEmail("ws-invnr-ghost")
	results, err := InviteMembers(ctx, pool, nrSlug, admin, []string{targetEmail, ghost}, RoleGuest)
	if err != nil {
		t.Fatalf("InviteMembers: %v", err)
	}
	byEmail := inviteResultsByEmail(results)
	if byEmail[targetEmail] != InviteStatusInvited {
		t.Fatalf("registered: status = %q, want invited", byEmail[targetEmail])
	}
	if byEmail[ghost] != InviteStatusNotRegistered {
		t.Fatalf("unknown: status = %q, want not-registered", byEmail[ghost])
	}

	// The unknown email must not have created any user or member row.
	var n int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE email = $1`, ghost).Scan(&n); err != nil {
		t.Fatalf("user count: %v", err)
	}
	if n != 0 {
		t.Fatalf("unknown email created %d user rows, want 0", n)
	}
}

func TestInviteMembersIdempotent(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	admin := createTestUser(t, pool, uniqueTestEmail("ws-invid-admin"))
	targetEmail := uniqueTestEmail("ws-invid-target")
	createTestUser(t, pool, targetEmail)
	idSlug := uniqueTestSlug("ws-inviteid")
	createTestWorkspace(t, pool, "InviteID", idSlug, admin)

	first, err := InviteMembers(ctx, pool, idSlug, admin, []string{targetEmail}, RoleMember)
	if err != nil {
		t.Fatalf("first invite: %v", err)
	}
	if inviteResultsByEmail(first)[targetEmail] != InviteStatusInvited {
		t.Fatalf("first: status = %q, want invited", inviteResultsByEmail(first)[targetEmail])
	}
	second, err := InviteMembers(ctx, pool, idSlug, admin, []string{targetEmail}, RoleMember)
	if err != nil {
		t.Fatalf("second invite: %v", err)
	}
	if inviteResultsByEmail(second)[targetEmail] != InviteStatusAlreadyMember {
		t.Fatalf("second: status = %q, want already-member", inviteResultsByEmail(second)[targetEmail])
	}

	var n int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM workspace_members m
		 JOIN workspaces w ON w.id = m.workspace_id
		 JOIN users u ON u.id = m.user_id
		 WHERE w.slug = $1 AND u.email = $2`,
		idSlug, targetEmail).Scan(&n); err != nil {
		t.Fatalf("member count: %v", err)
	}
	if n != 1 {
		t.Fatalf("member rows = %d, want 1 (no duplicates)", n)
	}
}

func TestInviteMembersRoleChecks(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	admin := createTestUser(t, pool, uniqueTestEmail("ws-invrc-admin"))
	member := createTestUser(t, pool, uniqueTestEmail("ws-invrc-member"))
	targetEmail := uniqueTestEmail("ws-invrc-target")
	createTestUser(t, pool, targetEmail)
	rcSlug := uniqueTestSlug("ws-inviterc")
	createTestWorkspace(t, pool, "InviteRC", rcSlug, admin)
	if err := UpsertMember(ctx, pool, rcSlug, admin, member, RoleMember); err != nil {
		t.Fatalf("add member: %v", err)
	}

	// Non-admin member → ErrForbidden (not ErrNotFound: the workspace
	// exists and the caller is a member).
	if _, err := InviteMembers(ctx, pool, rcSlug, member, []string{targetEmail}, 0); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member invite: err = %v, want ErrForbidden", err)
	}
	// Outsider → ErrNotFound (no workspace existence hint).
	outsider := createTestUser(t, pool, uniqueTestEmail("ws-invrc-outsider"))
	if _, err := InviteMembers(ctx, pool, rcSlug, outsider, []string{targetEmail}, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("outsider invite: err = %v, want ErrNotFound", err)
	}
	// Bad slug → ErrNotFound.
	if _, err := InviteMembers(ctx, pool, uniqueTestSlug("ws-invrc-nosuch"), admin, []string{targetEmail}, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bad slug invite: err = %v, want ErrNotFound", err)
	}
	// Invalid role → ErrInvalidRole.
	if _, err := InviteMembers(ctx, pool, rcSlug, admin, []string{targetEmail}, 99); !errors.Is(err, ErrInvalidRole) {
		t.Fatalf("bad role invite: err = %v, want ErrInvalidRole", err)
	}
}

func TestInviteMembersNormalization(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	admin := createTestUser(t, pool, uniqueTestEmail("ws-invnm-admin"))
	targetEmail := uniqueTestEmail("ws-invnm-target")
	createTestUser(t, pool, targetEmail)
	nmSlug := uniqueTestSlug("ws-invitenm")
	createTestWorkspace(t, pool, "InviteNM", nmSlug, admin)

	// Mixed case, whitespace, duplicates, and one malformed address.
	inputs := []string{
		"  " + strings.ToUpper(targetEmail) + " ",
		targetEmail,
		"not-an-email",
		"",
	}
	results, err := InviteMembers(ctx, pool, nmSlug, admin, inputs, 0)
	if err != nil {
		t.Fatalf("InviteMembers: %v", err)
	}
	// 4 inputs, 3 unique normalized: target (invited), "not-an-email"
	// (invalid), "" (invalid).
	if len(results) != 3 {
		t.Fatalf("results = %d, want 3 (deduped)", len(results))
	}
	byEmail := inviteResultsByEmail(results)
	if byEmail[targetEmail] != InviteStatusInvited {
		t.Fatalf("normalized target: status = %q, want invited", byEmail[targetEmail])
	}
	if byEmail["not-an-email"] != InviteStatusInvalid {
		t.Fatalf("malformed: status = %q, want invalid", byEmail["not-an-email"])
	}
	if byEmail[""] != InviteStatusInvalid {
		t.Fatalf("empty: status = %q, want invalid", byEmail[""])
	}
}
