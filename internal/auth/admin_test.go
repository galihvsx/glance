package auth

// Admin seeding tests (C5T0): first-user-on-empty-table auto-admin,
// GLANCE_ADMIN_EMAILS bootstrap matching, boot-time SeedAdminEmails
// idempotency, and the registration-never-demotes rule.

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"glance/internal/config"
)

// rowQuerier is satisfied by both *pgxpool.Pool and pgx.Tx — the seeding
// tests assert against whichever holds the transaction under test.
type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func adminTestConfig(adminEmails ...string) *config.Config {
	cfg := testConfig()
	cfg.AdminEmails = adminEmails
	return cfg
}

// isAdminOf returns the is_admin flag for a user row.
func isAdminOf(t *testing.T, q rowQuerier, ctx context.Context, userID string) bool {
	t.Helper()
	var isAdmin bool
	if err := q.QueryRow(ctx, `SELECT is_admin FROM users WHERE id = $1::uuid`, userID).Scan(&isAdmin); err != nil {
		t.Fatalf("read is_admin: %v", err)
	}
	return isAdmin
}

func TestIsBootstrapAdminEmail(t *testing.T) {
	cfg := adminTestConfig("boss@example.com", "ops@example.com")
	for _, tc := range []struct {
		email string
		want  bool
	}{
		{"boss@example.com", true},
		{"BOSS@EXAMPLE.COM", true}, // case-insensitive
		{"  ops@example.com ", true},
		{"stranger@example.com", false},
		{"", false},
	} {
		if got := IsBootstrapAdminEmail(cfg, tc.email); got != tc.want {
			t.Errorf("IsBootstrapAdminEmail(%q) = %v, want %v", tc.email, got, tc.want)
		}
	}
	if IsBootstrapAdminEmail(testConfig(), "boss@example.com") {
		t.Error("empty AdminEmails must match nothing")
	}
}

func TestSeedAdminEmails(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)

	plain := uniqueEmail("seed-plain")
	listed := uniqueEmail("seed-listed")
	already := uniqueEmail("seed-already")
	for _, tc := range []struct {
		email   string
		isAdmin bool
	}{
		{plain, false},
		{listed, false},
		{already, true},
	} {
		if _, err := pool.Exec(ctx, `INSERT INTO users (email, is_admin) VALUES ($1, $2)`,
			strings.ToLower(tc.email), tc.isAdmin); err != nil {
			t.Fatalf("insert user: %v", err)
		}
	}

	// First run: promotes the listed user only.
	n, err := SeedAdminEmails(ctx, pool, []string{strings.ToLower(listed)})
	if err != nil {
		t.Fatalf("SeedAdminEmails: %v", err)
	}
	if n != 1 {
		t.Errorf("promoted = %d, want 1", n)
	}
	var plainAdmin, listedAdmin, alreadyAdmin bool
	if err := pool.QueryRow(ctx, `SELECT is_admin FROM users WHERE email = $1`, strings.ToLower(plain)).Scan(&plainAdmin); err != nil {
		t.Fatalf("read plain: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT is_admin FROM users WHERE email = $1`, strings.ToLower(listed)).Scan(&listedAdmin); err != nil {
		t.Fatalf("read listed: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT is_admin FROM users WHERE email = $1`, strings.ToLower(already)).Scan(&alreadyAdmin); err != nil {
		t.Fatalf("read already: %v", err)
	}
	if plainAdmin {
		t.Error("non-listed user was promoted")
	}
	if !listedAdmin {
		t.Error("listed user was not promoted")
	}
	if !alreadyAdmin {
		t.Error("already-admin user was demoted")
	}

	// Second run: idempotent — nothing left to promote.
	n, err = SeedAdminEmails(ctx, pool, []string{strings.ToLower(listed)})
	if err != nil {
		t.Fatalf("SeedAdminEmails (2nd): %v", err)
	}
	if n != 0 {
		t.Errorf("second run promoted = %d, want 0 (idempotent)", n)
	}

	// Empty list: no-op, no error.
	if n, err := SeedAdminEmails(ctx, pool, nil); err != nil || n != 0 {
		t.Errorf("empty list: n = %d, err = %v; want 0, nil", n, err)
	}
}

// TestFirstUserProvisionedIsAdmin simulates a fresh instance inside one
// transaction: TRUNCATE ... CASCADE empties the users table (and its
// dependents) transaction-locally, the provisioning runs, then everything
// rolls back. TRUNCATE takes a brief ACCESS EXCLUSIVE lock, so the tx is
// kept as short as possible — provision, assert, roll back.
func TestFirstUserProvisionedIsAdmin(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	cfg := adminTestConfig()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.Exec(ctx, `TRUNCATE users CASCADE`); err != nil {
		tx.Rollback(ctx)
		t.Fatalf("truncate: %v", err)
	}
	userID, isActive, err := provisionUserTx(ctx, tx, cfg, uniqueEmail("first-user"), nil, nil, false)
	if err != nil {
		tx.Rollback(ctx)
		t.Fatalf("provisionUserTx: %v", err)
	}
	if !isAdminOf(t, tx, ctx, userID) {
		tx.Rollback(ctx)
		t.Error("first user on empty table: is_admin = false, want true")
	}
	if !isActive {
		tx.Rollback(ctx)
		t.Error("provisioned user: is_active = false, want true")
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback: %v", err)
	}
}

// TestBootstrapEmailProvisionedIsAdmin: table is NOT empty, but the email
// is on GLANCE_ADMIN_EMAILS — the new user still starts as admin.
func TestBootstrapEmailProvisionedIsAdmin(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	listed := uniqueEmail("listed-user")
	cfg := adminTestConfig(strings.ToLower(listed))

	// A pre-existing ordinary user, so the table is not empty.
	if _, err := pool.Exec(ctx, `INSERT INTO users (email) VALUES ($1)`, uniqueEmail("existing")); err != nil {
		t.Fatalf("insert existing user: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)
	name := "Listed User"
	userID, _, err := provisionUserTx(ctx, tx, cfg, listed, &name, nil, true)
	if err != nil {
		t.Fatalf("provisionUserTx: %v", err)
	}
	if !isAdminOf(t, tx, ctx, userID) {
		t.Error("bootstrap-listed email: is_admin = false, want true")
	}
}

// TestLaterUserNotAdmin: ordinary registration after the first user stays
// a non-admin.
func TestLaterUserNotAdmin(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	cfg := adminTestConfig()

	if _, err := pool.Exec(ctx, `INSERT INTO users (email, is_admin) VALUES ($1, true)`, uniqueEmail("first-admin")); err != nil {
		t.Fatalf("insert first admin: %v", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)
	userID, _, err := provisionUserTx(ctx, tx, cfg, uniqueEmail("later-user"), nil, nil, false)
	if err != nil {
		t.Fatalf("provisionUserTx: %v", err)
	}
	if isAdminOf(t, tx, ctx, userID) {
		t.Error("later non-listed user: is_admin = true, want false")
	}
}

// TestProvisionNeverChangesExistingAdminFlag: the ON CONFLICT branch must
// never flip is_admin — a re-login by a listed email does not promote a
// non-admin, and a re-login with an empty bootstrap list does not demote
// an admin.
func TestProvisionNeverChangesExistingAdminFlag(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)

	nonAdmin := uniqueEmail("stay-plain")
	admin := uniqueEmail("stay-admin")
	if _, err := pool.Exec(ctx, `INSERT INTO users (email, is_admin) VALUES ($1, false)`, nonAdmin); err != nil {
		t.Fatalf("insert non-admin: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO users (email, is_admin) VALUES ($1, true)`, admin); err != nil {
		t.Fatalf("insert admin: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)

	// Non-admin re-registers while listed on the bootstrap list: stays
	// non-admin (registration never promotes).
	listedCfg := adminTestConfig(nonAdmin)
	if _, _, err := provisionUserTx(ctx, tx, listedCfg, nonAdmin, nil, nil, false); err != nil {
		t.Fatalf("provisionUserTx (non-admin): %v", err)
	}
	var stillPlain bool
	if err := tx.QueryRow(ctx, `SELECT is_admin FROM users WHERE email = $1`, nonAdmin).Scan(&stillPlain); err != nil {
		t.Fatalf("read non-admin: %v", err)
	}
	if stillPlain {
		t.Error("re-registration promoted a non-admin via bootstrap list")
	}

	// Admin re-registers with no bootstrap list: stays admin
	// (registration never demotes).
	if _, _, err := provisionUserTx(ctx, tx, adminTestConfig(), admin, nil, nil, false); err != nil {
		t.Fatalf("provisionUserTx (admin): %v", err)
	}
	var stillAdmin bool
	if err := tx.QueryRow(ctx, `SELECT is_admin FROM users WHERE email = $1`, admin).Scan(&stillAdmin); err != nil {
		t.Fatalf("read admin: %v", err)
	}
	if !stillAdmin {
		t.Error("re-registration demoted an admin")
	}
}

// TestProvisionUserTxMergesProfile: the OAuth path (mergeProfile=true)
// fills empty name/avatar but never overwrites existing values —
// behavior parity with the pre-C5T0 SQL.
func TestProvisionUserTxMergesProfile(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	cfg := adminTestConfig()

	if _, err := pool.Exec(ctx, `INSERT INTO users (email, is_admin) VALUES ($1, true)`, uniqueEmail("seed")); err != nil {
		t.Fatalf("seed: %v", err)
	}
	email := uniqueEmail("oauth-user")
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)

	name, avatar := "Oauth Name", "https://example.com/a.png"
	if _, _, err := provisionUserTx(ctx, tx, cfg, email, &name, &avatar, true); err != nil {
		t.Fatalf("provision (new): %v", err)
	}
	var gotName, gotAvatar *string
	if err := tx.QueryRow(ctx, `SELECT name, avatar_url FROM users WHERE email = $1`, email).Scan(&gotName, &gotAvatar); err != nil {
		t.Fatalf("read profile: %v", err)
	}
	if gotName == nil || *gotName != name || gotAvatar == nil || *gotAvatar != avatar {
		t.Errorf("profile = (%v, %v), want (%q, %q)", strVal(gotName), strVal(gotAvatar), name, avatar)
	}

	// Re-provision with different profile data: existing values win.
	name2, avatar2 := "New Name", "https://example.com/b.png"
	if _, _, err := provisionUserTx(ctx, tx, cfg, email, &name2, &avatar2, true); err != nil {
		t.Fatalf("provision (conflict): %v", err)
	}
	if err := tx.QueryRow(ctx, `SELECT name, avatar_url FROM users WHERE email = $1`, email).Scan(&gotName, &gotAvatar); err != nil {
		t.Fatalf("read profile: %v", err)
	}
	if gotName == nil || *gotName != name || gotAvatar == nil || *gotAvatar != avatar {
		t.Errorf("after conflict: profile = (%v, %v), want original (%q, %q)", strVal(gotName), strVal(gotAvatar), name, avatar)
	}
}

func strVal(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}
