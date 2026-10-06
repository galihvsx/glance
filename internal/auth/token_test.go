package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// createUserRow inserts a user row directly (no OTP dance) and returns
// its id. Tokens belong to users, not to sessions, so the auth package
// tests can stay at the SQL level.
func createUserRow(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(),
		`INSERT INTO users (email) VALUES ($1) RETURNING id::text`, uniqueEmail("tok")).Scan(&id)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return id
}

func TestGenerateTokenFormat(t *testing.T) {
	tok, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	if !strings.HasPrefix(tok, TokenPrefix) {
		t.Fatalf("token %q does not start with %q", tok, TokenPrefix)
	}
	// gl_ + base64url(32 bytes) = 3 + 43 chars.
	if len(tok) != len(TokenPrefix)+43 {
		t.Fatalf("token length = %d, want %d", len(tok), len(TokenPrefix)+43)
	}
	other, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	if tok == other {
		t.Fatal("two generated tokens are identical — RNG broken?")
	}
}

func TestCreateAndAuthenticateToken(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	userID := createUserRow(t, pool)
	created, err := CreateToken(ctx, pool, userID, "ci", []string{ScopeRead, ScopeWrite}, nil)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	if !strings.HasPrefix(created.Plaintext, TokenPrefix) {
		t.Fatalf("plaintext %q missing prefix", created.Plaintext)
	}

	authd, err := AuthenticateToken(ctx, pool, created.Plaintext)
	if err != nil {
		t.Fatalf("AuthenticateToken: %v", err)
	}
	if authd.User.ID != userID {
		t.Fatalf("user = %q, want %q", authd.User.ID, userID)
	}
	if !authd.HasScope(ScopeWrite) {
		t.Fatal("token created with write scope does not have write")
	}

	// Unknown token → ErrTokenInvalid.
	if _, err := AuthenticateToken(ctx, pool, TokenPrefix+"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"); err != ErrTokenInvalid {
		t.Fatalf("unknown token: err = %v, want ErrTokenInvalid", err)
	}
	// Not a glance token at all → ErrTokenInvalid.
	if _, err := AuthenticateToken(ctx, pool, "Bearer nope"); err != ErrTokenInvalid {
		t.Fatalf("garbage: err = %v, want ErrTokenInvalid", err)
	}
}

func TestTokenPlaintextNeverStored(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	userID := createUserRow(t, pool)
	created, err := CreateToken(ctx, pool, userID, "ci", []string{ScopeRead}, nil)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM api_tokens WHERE token_hash = $1`, created.Plaintext).Scan(&n); err != nil {
		t.Fatalf("query: %v", err)
	}
	if n != 0 {
		t.Fatal("plaintext token found in api_tokens.token_hash — must store only the hash")
	}
}

func TestCreateTokenRejectsPastExpiry(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	userID := createUserRow(t, pool)
	past := time.Now().Add(-time.Hour)
	if _, err := CreateToken(ctx, pool, userID, "ci", []string{ScopeRead}, &past); !errors.Is(err, ErrInvalidScope) {
		t.Fatalf("CreateToken with past expires_at: err = %v, want ErrInvalidScope", err)
	}
	// Exactly-now is not "in the future" either.
	now := time.Now()
	if _, err := CreateToken(ctx, pool, userID, "ci", []string{ScopeRead}, &now); !errors.Is(err, ErrInvalidScope) {
		t.Fatalf("CreateToken with now expires_at: err = %v, want ErrInvalidScope", err)
	}
}

func TestExpiredTokenRejected(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	userID := createUserRow(t, pool)
	created, err := CreateToken(ctx, pool, userID, "ci", []string{ScopeRead}, nil)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	// Sanity: live token authenticates.
	if _, err := AuthenticateToken(ctx, pool, created.Plaintext); err != nil {
		t.Fatalf("AuthenticateToken before expiry: %v", err)
	}
	// Expire it directly — deterministic, no clock games.
	if _, err := pool.Exec(ctx,
		`UPDATE api_tokens SET expires_at = now() - interval '1 second' WHERE id = $1::uuid`, created.ID); err != nil {
		t.Fatalf("expire token: %v", err)
	}
	// Expired token → ErrTokenInvalid, indistinguishable from unknown.
	if _, err := AuthenticateToken(ctx, pool, created.Plaintext); err != ErrTokenInvalid {
		t.Fatalf("expired token: err = %v, want ErrTokenInvalid", err)
	}
}

func TestRevokeToken(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	userID := createUserRow(t, pool)
	created, err := CreateToken(ctx, pool, userID, "ci", []string{ScopeRead}, nil)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	ok, err := RevokeToken(ctx, pool, userID, created.ID)
	if err != nil {
		t.Fatalf("RevokeToken: %v", err)
	}
	if !ok {
		t.Fatal("RevokeToken returned false for an owned token")
	}
	if _, err := AuthenticateToken(ctx, pool, created.Plaintext); err != ErrTokenInvalid {
		t.Fatalf("revoked token: err = %v, want ErrTokenInvalid", err)
	}
	// Second revoke is a no-op, not an error.
	if _, err := RevokeToken(ctx, pool, userID, created.ID); err != nil {
		t.Fatalf("second RevokeToken: %v", err)
	}
	// Revoking someone else's token reports false (callers map to 404).
	other := createUserRow(t, pool)
	if ok, err := RevokeToken(ctx, pool, other, created.ID); err != nil || ok {
		t.Fatalf("cross-user revoke: ok=%v err=%v, want ok=false", ok, err)
	}
}

func TestCreateTokenValidatesScopes(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	userID := createUserRow(t, pool)
	if _, err := CreateToken(ctx, pool, userID, "bad", []string{"admin"}, nil); err == nil {
		t.Fatal("CreateToken accepted unknown scope, want error")
	}
	if _, err := CreateToken(ctx, pool, userID, "bad", nil, nil); err == nil {
		t.Fatal("CreateToken accepted empty scopes, want error")
	}
	if _, err := CreateToken(ctx, pool, userID, "", []string{ScopeRead}, nil); err == nil {
		t.Fatal("CreateToken accepted empty name, want error")
	}
}

func TestScopeImplication(t *testing.T) {
	// write implies read — a write-scoped token may read.
	w := &TokenAuth{Scopes: []string{ScopeWrite}}
	if !w.HasScope(ScopeRead) {
		t.Fatal("write-scoped token lacks read")
	}
	if !w.HasScope(ScopeWrite) {
		t.Fatal("write-scoped token lacks write")
	}
	r := &TokenAuth{Scopes: []string{ScopeRead}}
	if r.HasScope(ScopeWrite) {
		t.Fatal("read-scoped token has write")
	}
}

func TestTokenRateLimit(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	// Unique key: the rate_limits table persists across runs.
	key := "token:test-" + uniqueEmail("rl")
	for i := 0; i < 2; i++ {
		if err := checkTokenRateLimit(ctx, pool, key, 2, time.Minute); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if err := checkTokenRateLimit(ctx, pool, key, 2, time.Minute); err != ErrTokenRateLimited {
		t.Fatalf("over budget: err = %v, want ErrTokenRateLimited", err)
	}
}
