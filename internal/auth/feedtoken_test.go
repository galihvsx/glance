package auth

// Calendar feed token tests (C15T3): mint/authenticate/revoke round
// trips, prefix rejection, rotation revokes the old secret, and the
// status view never exposes the secret or its hash.

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"glance/migrations"
)

func feedTestUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (email) VALUES ($1) RETURNING id::text`, uniqueEmail("feed")).Scan(&id); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return id
}

func TestCreateFeedTokenRoundTrip(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	userID := feedTestUser(t, ctx, pool)

	created, err := CreateFeedToken(ctx, pool, userID)
	if err != nil {
		t.Fatalf("CreateFeedToken: %v", err)
	}
	if !strings.HasPrefix(created.Token, FeedTokenPrefix) {
		t.Fatalf("token = %q, want prefix %q", created.Token, FeedTokenPrefix)
	}
	if created.Token == "" || len(created.Token) <= len(FeedTokenPrefix) {
		t.Fatal("token has no random payload")
	}

	// The plaintext authenticates.
	user, err := AuthenticateFeedToken(ctx, pool, created.Token)
	if err != nil {
		t.Fatalf("AuthenticateFeedToken: %v", err)
	}
	if user.ID != userID {
		t.Fatalf("authenticated user = %q, want %q", user.ID, userID)
	}

	// Only the hash is stored — the plaintext never touches the table.
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM calendar_feed_tokens WHERE token_hash = $1`, created.Token).Scan(&n); err != nil {
		t.Fatalf("plaintext lookup: %v", err)
	}
	if n != 0 {
		t.Fatal("plaintext found in token_hash column — must store only the hash")
	}
}

func TestAuthenticateFeedTokenRejects(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	userID := feedTestUser(t, ctx, pool)

	created, err := CreateFeedToken(ctx, pool, userID)
	if err != nil {
		t.Fatalf("CreateFeedToken: %v", err)
	}

	for name, tok := range map[string]string{
		"empty":        "",
		"wrong prefix": "gl_" + created.Token[len(FeedTokenPrefix):],
		"unknown":      FeedTokenPrefix + "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"tampered":     created.Token[:len(created.Token)-2] + "xx",
	} {
		if _, err := AuthenticateFeedToken(ctx, pool, tok); err != ErrFeedTokenInvalid {
			t.Errorf("%s: err = %v, want ErrFeedTokenInvalid", name, err)
		}
	}

	// A feed token is not an API token: AuthenticateToken must reject it.
	if _, err := AuthenticateToken(ctx, pool, created.Token); err != ErrTokenInvalid {
		t.Errorf("AuthenticateToken(feed token): err = %v, want ErrTokenInvalid", err)
	}
}

func TestRevokeFeedToken(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	userID := feedTestUser(t, ctx, pool)

	created, err := CreateFeedToken(ctx, pool, userID)
	if err != nil {
		t.Fatalf("CreateFeedToken: %v", err)
	}
	ok, err := RevokeFeedToken(ctx, pool, userID)
	if err != nil {
		t.Fatalf("RevokeFeedToken: %v", err)
	}
	if !ok {
		t.Fatal("RevokeFeedToken = false, want true")
	}
	if _, err := AuthenticateFeedToken(ctx, pool, created.Token); err != ErrFeedTokenInvalid {
		t.Fatalf("revoked token authenticates: err = %v, want ErrFeedTokenInvalid", err)
	}
	// Second revoke: nothing live → false.
	ok, err = RevokeFeedToken(ctx, pool, userID)
	if err != nil {
		t.Fatalf("second RevokeFeedToken: %v", err)
	}
	if ok {
		t.Fatal("second RevokeFeedToken = true, want false")
	}
}

func TestCreateFeedTokenRotates(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	userID := feedTestUser(t, ctx, pool)

	first, err := CreateFeedToken(ctx, pool, userID)
	if err != nil {
		t.Fatalf("first CreateFeedToken: %v", err)
	}
	second, err := CreateFeedToken(ctx, pool, userID)
	if err != nil {
		t.Fatalf("second CreateFeedToken: %v", err)
	}
	if first.Token == second.Token {
		t.Fatal("rotation returned the same secret")
	}
	// Old secret is dead.
	if _, err := AuthenticateFeedToken(ctx, pool, first.Token); err != ErrFeedTokenInvalid {
		t.Fatalf("old token after rotation: err = %v, want ErrFeedTokenInvalid", err)
	}
	// New secret works.
	if _, err := AuthenticateFeedToken(ctx, pool, second.Token); err != nil {
		t.Fatalf("new token after rotation: %v", err)
	}
	// At most one live row per user.
	var live int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM calendar_feed_tokens WHERE user_id = $1::uuid AND revoked_at IS NULL`,
		userID).Scan(&live); err != nil {
		t.Fatalf("count live: %v", err)
	}
	if live != 1 {
		t.Fatalf("live tokens = %d, want 1", live)
	}
}

func TestGetFeedTokenStatus(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	userID := feedTestUser(t, ctx, pool)

	status, err := GetFeedTokenStatus(ctx, pool, userID)
	if err != nil {
		t.Fatalf("GetFeedTokenStatus: %v", err)
	}
	if status.Active {
		t.Fatal("status active before creation")
	}

	created, err := CreateFeedToken(ctx, pool, userID)
	if err != nil {
		t.Fatalf("CreateFeedToken: %v", err)
	}
	status, err = GetFeedTokenStatus(ctx, pool, userID)
	if err != nil {
		t.Fatalf("GetFeedTokenStatus: %v", err)
	}
	if !status.Active {
		t.Fatal("status inactive after creation")
	}
	if status.CreatedAt == nil || status.CreatedAt.IsZero() {
		t.Fatal("created_at missing")
	}
	_ = created

	if _, err := RevokeFeedToken(ctx, pool, userID); err != nil {
		t.Fatalf("RevokeFeedToken: %v", err)
	}
	status, err = GetFeedTokenStatus(ctx, pool, userID)
	if err != nil {
		t.Fatalf("GetFeedTokenStatus: %v", err)
	}
	if status.Active {
		t.Fatal("status active after revoke")
	}
}

func TestAuthenticateFeedTokenInactiveOwner(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	userID := feedTestUser(t, ctx, pool)

	created, err := CreateFeedToken(ctx, pool, userID)
	if err != nil {
		t.Fatalf("CreateFeedToken: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET is_active = false WHERE id = $1::uuid`, userID); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	if _, err := AuthenticateFeedToken(ctx, pool, created.Token); err != ErrFeedTokenInvalid {
		t.Fatalf("deactivated owner: err = %v, want ErrFeedTokenInvalid", err)
	}
}

// Ensure the migration for this package's table is part of the embedded
// migration set (a missed file would surface here, not in production).
func TestFeedTokenMigrationApplied(t *testing.T) {
	files, err := migrations.FS.ReadDir(".")
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}
	var found bool
	for _, f := range files {
		if f.Name() == "000042_calendar_feed_tokens.up.sql" {
			found = true
		}
	}
	if !found {
		t.Fatal("000042_calendar_feed_tokens.up.sql missing from embedded migrations")
	}
}
