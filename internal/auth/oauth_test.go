package auth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"golang.org/x/oauth2"
)

// TestOAuthCallbackLinksAccount is the TDD RED test for Task 8: the OAuth
// callback path must create the user, link the oauth_accounts row, and mint
// a session — with the token exchange and userinfo fetch fully mocked so no
// real provider is ever contacted.
func TestOAuthCallbackLinksAccount(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	// Swap the network-touching seams for mocks; restore afterwards.
	oldExchange, oldFetch := exchangeCode, fetchUserInfo
	defer func() { exchangeCode, fetchUserInfo = oldExchange, oldFetch }()

	email := uniqueEmail("oauth")
	// provider_uid must be unique per run: the test database is shared
	// across runs, and a stale row from a previous run would otherwise
	// trigger ErrOAuthAlreadyLinked against a different user.
	uid := fmt.Sprintf("google-uid-%d-%d", os.Getpid(), testSeq.Add(1))
	exchangeCode = func(ctx context.Context, cfg *oauth2.Config, code string) (*oauth2.Token, error) {
		if code != "test-auth-code" {
			return nil, errors.New("oauth2: bad code")
		}
		return &oauth2.Token{AccessToken: "test-access-token"}, nil
	}
	fetchUserInfo = func(ctx context.Context, p Provider, tok *oauth2.Token) (OAuthUserInfo, error) {
		if tok.AccessToken != "test-access-token" {
			return OAuthUserInfo{}, errors.New("unexpected token")
		}
		return OAuthUserInfo{
			ID:            uid,
			Email:         email,
			Name:          "OAuth Test User",
			AvatarURL:     "https://example.com/avatar.png",
			VerifiedEmail: true,
		}, nil
	}

	cfg := testConfig()
	cfg.GoogleClientID = "test-google-client-id"
	cfg.GoogleClientSecret = "test-google-client-secret"

	token, err := CompleteOAuthLogin(ctx, pool, cfg, ProviderGoogle, "test-auth-code",
		"https://app.example/callback", "test-agent", "10.0.0.9")
	if err != nil {
		t.Fatalf("CompleteOAuthLogin: %v", err)
	}
	if token == "" {
		t.Fatal("CompleteOAuthLogin returned an empty session token")
	}

	// The user row must exist…
	var userID string
	var name, avatar string
	err = pool.QueryRow(ctx,
		`SELECT id, COALESCE(name,''), COALESCE(avatar_url,'') FROM users WHERE email = $1`, email,
	).Scan(&userID, &name, &avatar)
	if err != nil {
		t.Fatalf("user row missing: %v", err)
	}
	if name != "OAuth Test User" {
		t.Errorf("user name = %q, want provider name on first link", name)
	}

	// …the oauth_accounts row must link provider_uid → that user…
	var linkedUserID, provider, gotUID string
	err = pool.QueryRow(ctx,
		`SELECT user_id, provider, provider_uid FROM oauth_accounts WHERE provider_uid = $1`,
		uid,
	).Scan(&linkedUserID, &provider, &gotUID)
	if err != nil {
		t.Fatalf("oauth_accounts row missing: %v", err)
	}
	if linkedUserID != userID || provider != "google" || gotUID != uid {
		t.Errorf("oauth_accounts = (%s,%s,%s), want (%s,google,%s)",
			linkedUserID, provider, gotUID, userID, uid)
	}

	// …and the returned token must be a live session for that user.
	var sessionUserID string
	err = pool.QueryRow(ctx,
		`SELECT user_id FROM sessions WHERE token_hash = encode(sha256($1::bytea), 'hex')`,
		token,
	).Scan(&sessionUserID)
	if err != nil {
		t.Fatalf("session row missing for returned token: %v", err)
	}
	if sessionUserID != userID {
		t.Errorf("session user = %s, want %s", sessionUserID, userID)
	}

	// A second login with the same provider_uid must reuse the account —
	// no duplicate user row, no duplicate oauth_accounts row.
	token2, err := CompleteOAuthLogin(ctx, pool, cfg, ProviderGoogle, "test-auth-code",
		"https://app.example/callback", "test-agent", "10.0.0.9")
	if err != nil {
		t.Fatalf("second CompleteOAuthLogin: %v", err)
	}
	if token2 == "" || token2 == token {
		t.Errorf("second login must mint a fresh session token")
	}
	var userCount, linkCount int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE email = $1`, email).Scan(&userCount)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM oauth_accounts WHERE provider_uid = $1`, uid).Scan(&linkCount)
	if userCount != 1 || linkCount != 1 {
		t.Errorf("users=%d links=%d, want exactly 1 each (no duplicates)", userCount, linkCount)
	}
}

// TestOAuthUnverifiedEmailRejected: a provider identity WITHOUT a verified
// email must never create or link an account — the verified email is the
// link key (spec §7, task brief rule 6).
func TestOAuthUnverifiedEmailRejected(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	oldExchange, oldFetch := exchangeCode, fetchUserInfo
	defer func() { exchangeCode, fetchUserInfo = oldExchange, oldFetch }()
	exchangeCode = func(ctx context.Context, cfg *oauth2.Config, code string) (*oauth2.Token, error) {
		return &oauth2.Token{AccessToken: "tok"}, nil
	}
	fetchUserInfo = func(ctx context.Context, p Provider, tok *oauth2.Token) (OAuthUserInfo, error) {
		return OAuthUserInfo{}, ErrOAuthEmailUnverified
	}

	cfg := testConfig()
	cfg.GitHubClientID = "test-gh-id"
	cfg.GitHubClientSecret = "test-gh-secret"

	_, err := CompleteOAuthLogin(ctx, pool, cfg, ProviderGitHub, "code",
		"https://app.example/callback", "ua", "10.0.0.10")
	if !errors.Is(err, ErrOAuthEmailUnverified) {
		t.Fatalf("err = %v, want ErrOAuthEmailUnverified", err)
	}
}

// TestOAuthUnconfiguredProvider: no client credentials →
// ErrOAuthNotConfigured (the handler maps this to 404, never 500).
func TestOAuthUnconfiguredProvider(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)

	_, err := CompleteOAuthLogin(context.Background(), pool, testConfig(),
		ProviderGoogle, "code", "https://app.example/callback", "ua", "10.0.0.11")
	if !errors.Is(err, ErrOAuthNotConfigured) {
		t.Fatalf("err = %v, want ErrOAuthNotConfigured", err)
	}
}

// TestOAuthStateRoundTrip: sign → verify accepts; tampered signature,
// wrong raw value, or empty secret must all fail. Constant-time compare is
// exercised implicitly — the security property is "no forgery accepted".
func TestOAuthStateRoundTrip(t *testing.T) {
	cfg := testConfig()
	cfg.OAuthStateSecret = "test-state-secret-do-not-use-in-prod"

	raw, signed, err := NewOAuthState(cfg)
	if err != nil {
		t.Fatalf("newOAuthState: %v", err)
	}
	if !VerifyOAuthState(cfg, signed, raw) {
		t.Error("valid state rejected")
	}
	if VerifyOAuthState(cfg, signed+"tampered", raw) {
		t.Error("tampered signature accepted")
	}
	if VerifyOAuthState(cfg, signed, "wrong-raw-value") {
		t.Error("wrong raw state accepted")
	}
	if VerifyOAuthState(cfg, "not-a-signed-value", raw) {
		t.Error("malformed signed value accepted")
	}

	// Empty secret must fail fast — never sign with a nil key.
	emptyCfg := testConfig()
	if _, _, err := NewOAuthState(emptyCfg); err == nil {
		t.Error("newOAuthState with empty secret must fail")
	}
	if VerifyOAuthState(emptyCfg, signed, raw) {
		t.Error("verifyOAuthState with empty secret must fail")
	}
}

// TestOAuthAccountHijackRejected: a provider_uid already linked to user A
// must NOT be re-linkable to user B (verified-email collision across
// accounts). The link is sticky to the first user.
func TestOAuthAccountHijackRejected(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	oldExchange, oldFetch := exchangeCode, fetchUserInfo
	defer func() { exchangeCode, fetchUserInfo = oldExchange, oldFetch }()
	exchangeCode = func(ctx context.Context, cfg *oauth2.Config, code string) (*oauth2.Token, error) {
		return &oauth2.Token{AccessToken: "tok"}, nil
	}
	victimEmail := uniqueEmail("oauth-victim")
	attackerEmail := uniqueEmail("oauth-attacker")
	// Unique per run — see the note in TestOAuthCallbackLinksAccount.
	sharedUID := fmt.Sprintf("shared-uid-%d-%d", os.Getpid(), testSeq.Add(1))
	currentEmail := victimEmail
	fetchUserInfo = func(ctx context.Context, p Provider, tok *oauth2.Token) (OAuthUserInfo, error) {
		return OAuthUserInfo{ID: sharedUID, Email: currentEmail, VerifiedEmail: true}, nil
	}

	cfg := testConfig()
	cfg.GoogleClientID = "id"
	cfg.GoogleClientSecret = "secret"

	if _, err := CompleteOAuthLogin(ctx, pool, cfg, ProviderGoogle, "c",
		"https://app.example/cb", "ua", "10.0.0.12"); err != nil {
		t.Fatalf("victim link: %v", err)
	}
	// Attacker presents the same provider_uid but a different verified email.
	currentEmail = attackerEmail
	_, err := CompleteOAuthLogin(ctx, pool, cfg, ProviderGoogle, "c",
		"https://app.example/cb", "ua", "10.0.0.12")
	if !errors.Is(err, ErrOAuthAlreadyLinked) {
		t.Fatalf("err = %v, want ErrOAuthAlreadyLinked", err)
	}
	// The oauth_accounts row must still point at the victim.
	var linkedEmail string
	err = pool.QueryRow(ctx,
		`SELECT u.email FROM oauth_accounts oa JOIN users u ON u.id = oa.user_id
		 WHERE oa.provider_uid = $1`, sharedUID,
	).Scan(&linkedEmail)
	if err != nil {
		t.Fatalf("link lookup: %v", err)
	}
	if linkedEmail != victimEmail {
		t.Errorf("link moved to %s, must stay with %s", linkedEmail, victimEmail)
	}
}
