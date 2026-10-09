// Package auth — OAuth (Google/GitHub) is glance's second passwordless login
// path (spec §7). The flow is the standard authorization-code flow via
// x/oauth2: login → 302 to the provider with an unguessable state value
// (HMAC-signed, stored in a single-use cookie) → callback → validate state →
// exchange code → fetch the verified identity → look up oauth_accounts by
// provider_uid → authenticate as the linked user (or find-or-create the user
// by verified email when the uid is new) → mint a session exactly like OTP.
//
// The provider_uid is the stable identity — once linked, the email is just
// a profile attribute that may change at the provider. The verified email
// is only the provisioning key for a uid never seen before. The provider's
// name/avatar are profile conveniences: they fill empty profile fields on
// first link and are never trusted for identity.
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	netmail "net/mail"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/oauth2"

	"glance/internal/config"
)

// Provider is an OAuth identity provider (spec §4: google/github).
type Provider string

const (
	ProviderGoogle Provider = "google"
	ProviderGitHub Provider = "github"
)

// ParseProvider maps a path segment to a Provider.
func ParseProvider(s string) (Provider, bool) {
	switch Provider(s) {
	case ProviderGoogle, ProviderGitHub:
		return Provider(s), true
	}
	return "", false
}

// OAuthUserInfo is the identity fetched from the provider.
type OAuthUserInfo struct {
	// ID is the provider's stable user id (Google sub / GitHub numeric id).
	ID string
	// Email is the VERIFIED email — the account link key.
	Email string
	// Name and AvatarURL are profile conveniences, never identity.
	Name      string
	AvatarURL string
	// VerifiedEmail is set by the fetchers; CompleteOAuthLogin refuses to
	// link an account without it.
	VerifiedEmail bool
}

var (
	// ErrOAuthNotConfigured is returned when a provider has no client
	// credentials. Handlers map it to 404 (never 500): an unconfigured
	// provider is "not here", not "broken".
	ErrOAuthNotConfigured = errors.New("auth: oauth provider not configured")
	// ErrOAuthEmailUnverified is returned when the provider does not vouch
	// for the email address. Without a verified email there is no link key.
	ErrOAuthEmailUnverified = errors.New("auth: oauth provider did not return a verified email")
	// ErrOAuthAlreadyLinked is returned when the provider_uid got linked to a
	// DIFFERENT user between our lookup and our insert — a concurrent-link
	// race (or a genuine takeover attempt racing a legitimate login). The
	// original link is kept and the transaction rolls back.
	ErrOAuthAlreadyLinked = errors.New("auth: oauth account is already linked to another user")
)

// oauthHTTPClient is the client for provider userinfo calls. Plain HTTP
// with the access token — no google.golang.org/api dependency (task: keep
// deps minimal, x/oauth2 only).
var oauthHTTPClient = &http.Client{Timeout: 10 * time.Second}

// oauth2Config builds the x/oauth2 config for a provider. The redirect URL
// must be identical in the login and callback steps (the provider enforces
// the match at token-exchange time).
func oauth2Config(cfg *config.Config, provider Provider, redirectURL string) (*oauth2.Config, error) {
	switch provider {
	case ProviderGoogle:
		if cfg.GoogleClientID == "" || cfg.GoogleClientSecret == "" {
			return nil, ErrOAuthNotConfigured
		}
		return &oauth2.Config{
			ClientID:     cfg.GoogleClientID,
			ClientSecret: cfg.GoogleClientSecret,
			RedirectURL:  redirectURL,
			Scopes:       []string{"openid", "email", "profile"},
			Endpoint: oauth2.Endpoint{
				AuthURL:  "https://accounts.google.com/o/oauth2/v2/auth",
				TokenURL: "https://oauth2.googleapis.com/token",
			},
		}, nil
	case ProviderGitHub:
		if cfg.GitHubClientID == "" || cfg.GitHubClientSecret == "" {
			return nil, ErrOAuthNotConfigured
		}
		return &oauth2.Config{
			ClientID:     cfg.GitHubClientID,
			ClientSecret: cfg.GitHubClientSecret,
			RedirectURL:  redirectURL,
			Scopes:       []string{"user:email"},
			Endpoint: oauth2.Endpoint{
				AuthURL:  "https://github.com/login/oauth/authorize",
				TokenURL: "https://github.com/login/oauth/access_token",
			},
		}, nil
	}
	return nil, ErrOAuthNotConfigured
}

// ProviderConfigured reports whether the provider has client credentials.
func ProviderConfigured(cfg *config.Config, provider Provider) bool {
	_, err := oauth2Config(cfg, provider, "")
	return err == nil
}

// AuthorizationURL returns the provider's authorization URL for the login
// redirect (the handler 302s here).
func AuthorizationURL(cfg *config.Config, provider Provider, redirectURL, state string) (string, error) {
	ocfg, err := oauth2Config(cfg, provider, redirectURL)
	if err != nil {
		return "", err
	}
	return ocfg.AuthCodeURL(state, oauth2.AccessTypeOnline), nil
}

// exchangeCode swaps an authorization code for a token. A package variable
// (not a method) so tests can mock the provider without network access.
var exchangeCode = func(ctx context.Context, ocfg *oauth2.Config, code string) (*oauth2.Token, error) {
	return ocfg.Exchange(ctx, code)
}

// fetchUserInfo resolves the verified identity for a token. Mockable like
// exchangeCode.
var fetchUserInfo = func(ctx context.Context, p Provider, tok *oauth2.Token) (OAuthUserInfo, error) {
	switch p {
	case ProviderGoogle:
		return fetchGoogleUserInfo(ctx, tok)
	case ProviderGitHub:
		return fetchGitHubUserInfo(ctx, tok)
	}
	return OAuthUserInfo{}, fmt.Errorf("auth: unknown oauth provider %q", p)
}

// getJSON performs an authenticated GET against a provider API and decodes
// the JSON body into v. Non-2xx responses are errors — provider error bodies
// are never trusted for identity, only for debugging.
func getJSON(ctx context.Context, url, accessToken string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("auth: oauth userinfo request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	resp, err := oauthHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("auth: oauth userinfo fetch: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("auth: oauth userinfo read: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("auth: oauth userinfo: provider returned %d", resp.StatusCode)
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("auth: oauth userinfo decode: %w", err)
	}
	return nil
}

// fetchGoogleUserInfo reads the OpenID Connect userinfo endpoint. Only an
// email with email_verified=true is accepted.
func fetchGoogleUserInfo(ctx context.Context, tok *oauth2.Token) (OAuthUserInfo, error) {
	var body struct {
		Sub           string `json:"sub"`
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Name          string `json:"name"`
		Picture       string `json:"picture"`
	}
	if err := getJSON(ctx, "https://openidconnect.googleapis.com/v1/userinfo", tok.AccessToken, &body); err != nil {
		return OAuthUserInfo{}, err
	}
	if body.Sub == "" || body.Email == "" || !body.EmailVerified {
		return OAuthUserInfo{}, ErrOAuthEmailUnverified
	}
	return OAuthUserInfo{
		ID:            body.Sub,
		Email:         body.Email,
		Name:          body.Name,
		AvatarURL:     body.Picture,
		VerifiedEmail: true,
	}, nil
}

// fetchGitHubUserInfo resolves the verified primary email via /user/emails
// (the /user profile endpoint does not vouch for the email) and fills
// name/avatar from /user on a best-effort basis.
func fetchGitHubUserInfo(ctx context.Context, tok *oauth2.Token) (OAuthUserInfo, error) {
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := getJSON(ctx, "https://api.github.com/user/emails", tok.AccessToken, &emails); err != nil {
		return OAuthUserInfo{}, err
	}
	var chosen string
	for _, e := range emails {
		if e.Primary && e.Verified {
			chosen = e.Email
			break
		}
	}
	if chosen == "" {
		for _, e := range emails {
			if e.Verified {
				chosen = e.Email
				break
			}
		}
	}
	if chosen == "" {
		return OAuthUserInfo{}, ErrOAuthEmailUnverified
	}
	info := OAuthUserInfo{Email: chosen, VerifiedEmail: true}
	var profile struct {
		ID        int64  `json:"id"`
		Name      string `json:"name"`
		AvatarURL string `json:"avatar_url"`
	}
	// Profile is cosmetic — a failure here must not fail the login.
	if err := getJSON(ctx, "https://api.github.com/user", tok.AccessToken, &profile); err == nil && profile.ID != 0 {
		info.ID = fmt.Sprintf("%d", profile.ID)
		info.Name = profile.Name
		info.AvatarURL = profile.AvatarURL
	}
	if info.ID == "" {
		return OAuthUserInfo{}, fmt.Errorf("auth: github did not return a user id")
	}
	return info, nil
}

// NewOAuthState generates an unguessable state value and its HMAC signature.
// The raw value is sent to the provider (and returned via the query string);
// the signed form is stored in a single-use cookie so the callback can verify
// the round trip wasn't forged. Fails fast when OAUTH_STATE_SECRET is empty —
// signing with a nil key would be theater.
func NewOAuthState(cfg *config.Config) (raw, signed string, err error) {
	if cfg.OAuthStateSecret == "" {
		return "", "", errors.New("auth: OAUTH_STATE_SECRET is not set")
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("auth: generate oauth state: %w", err)
	}
	raw = hex.EncodeToString(b)
	mac := hmac.New(sha256.New, []byte(cfg.OAuthStateSecret))
	mac.Write([]byte(raw))
	signed = raw + "." + hex.EncodeToString(mac.Sum(nil))
	return raw, signed, nil
}

// VerifyOAuthState checks the signed cookie value against the raw state from
// the query string. The MAC check and the equality check both use
// constant-time comparison.
func VerifyOAuthState(cfg *config.Config, signed, raw string) bool {
	if cfg.OAuthStateSecret == "" || signed == "" || raw == "" {
		return false
	}
	parts := strings.Split(signed, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(cfg.OAuthStateSecret))
	mac.Write([]byte(parts[0]))
	want := hex.EncodeToString(mac.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(parts[1]), []byte(want)) != 1 {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(parts[0]), []byte(raw)) == 1
}

// CompleteOAuthLogin runs the callback half of the flow: exchange the code,
// fetch the verified identity, then authenticate. The provider_uid is the
// stable identity: if it is already linked, the linked user logs in (their
// email is synced only when the new verified email is unclaimed). A uid
// never seen before falls through to find-or-create-by-verified-email and
// then links the oauth_accounts row. Everything runs in one transaction —
// a partial link (user without oauth row, or vice versa) can never be
// committed.
func CompleteOAuthLogin(ctx context.Context, pool *pgxpool.Pool, cfg *config.Config, provider Provider, code, redirectURL, userAgent, ip string) (string, error) {
	ocfg, err := oauth2Config(cfg, provider, redirectURL)
	if err != nil {
		return "", err
	}
	tok, err := exchangeCode(ctx, ocfg, code)
	if err != nil {
		return "", fmt.Errorf("auth: oauth code exchange: %w", err)
	}
	info, err := fetchUserInfo(ctx, provider, tok)
	if err != nil {
		return "", err
	}
	if !info.VerifiedEmail {
		return "", ErrOAuthEmailUnverified
	}
	email := strings.ToLower(strings.TrimSpace(info.Email))
	if _, err := netmail.ParseAddress(email); err != nil {
		return "", ErrOAuthEmailUnverified
	}
	if info.ID == "" {
		return "", fmt.Errorf("auth: oauth provider returned an empty user id")
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("auth: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	// provider_uid first: it is the stable identity. A uid linked to a user
	// authenticates AS that user even when the provider reports a different
	// verified email than before (provider-side email change) — the email
	// is a profile attribute, not the identity.
	var userID string
	linked := true
	if err := tx.QueryRow(ctx,
		`SELECT user_id FROM oauth_accounts WHERE provider_uid = $1`, info.ID,
	).Scan(&userID); err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("auth: lookup oauth account: %w", err)
		}
		linked = false
	}

	if linked {
		// Authenticate as the linked user. Touch last_login_at; sync the
		// email onto the user row only when it is unclaimed — overwriting
		// an email that belongs to another user would merge two accounts.
		var currentEmail string
		if err := tx.QueryRow(ctx,
			`SELECT email FROM users WHERE id = $1`, userID,
		).Scan(&currentEmail); err != nil {
			return "", fmt.Errorf("auth: load linked user: %w", err)
		}
		if !strings.EqualFold(currentEmail, email) {
			var claimedBy string
			err := tx.QueryRow(ctx,
				`SELECT id FROM users WHERE email = $1 AND id <> $2`, email, userID,
			).Scan(&claimedBy)
			switch {
			case err == nil:
				// Claimed by someone else: keep the linked user's email.
				// The uid decides identity, never the email.
			case errors.Is(err, pgx.ErrNoRows):
				if _, err := tx.Exec(ctx,
					`UPDATE users SET email = $1, updated_at = now() WHERE id = $2`,
					email, userID,
				); err != nil {
					return "", fmt.Errorf("auth: sync oauth email: %w", err)
				}
			default:
				return "", fmt.Errorf("auth: check email claim: %w", err)
			}
		}
		if _, err := tx.Exec(ctx,
			`UPDATE users SET last_login_at = now(), updated_at = now() WHERE id = $1`,
			userID,
		); err != nil {
			return "", fmt.Errorf("auth: touch last_login: %w", err)
		}
	} else {
		// New uid: find-or-create the user by verified email — the
		// provisioning key (spec §7). Provider name/avatar fill the
		// profile only when empty; they are never identity and never
		// overwrite values the user already has. Admin seeding is shared
		// with the OTP path (see provisionUserTx).
		userID, _, err = provisionUserTx(ctx, tx, cfg, email,
			strPtr(info.Name), strPtr(info.AvatarURL), true)
		if err != nil {
			return "", err
		}

		// Link the oauth account. provider_uid is globally unique: if a
		// concurrent transaction linked this uid first, the INSERT's DO
		// UPDATE keeps the original user_id, the mismatch is detected
		// below, and the transaction rolls back with the link intact.
		//
		// tokens stays '{}': v1 makes no provider API calls after login,
		// so there is nothing worth storing — and no secret worth leaking.
		var linkedUserID string
		if err := tx.QueryRow(ctx, `
			INSERT INTO oauth_accounts (user_id, provider, provider_uid, tokens)
			VALUES ($1, $2, $3, '{}'::jsonb)
			ON CONFLICT (provider_uid) DO UPDATE SET tokens = EXCLUDED.tokens
			RETURNING user_id`,
			userID, string(provider), info.ID,
		).Scan(&linkedUserID); err != nil {
			return "", fmt.Errorf("auth: link oauth account: %w", err)
		}
		if linkedUserID != userID {
			return "", ErrOAuthAlreadyLinked
		}
	}

	// Same session minting as OTP (internal/auth/session.go).
	// Deactivated accounts cannot log back in; existing sessions are already
	// dead via AuthenticateSession's is_active enforcement.
	var isActive bool
	if err := tx.QueryRow(ctx,
		`SELECT is_active FROM users WHERE id = $1`, userID,
	).Scan(&isActive); err != nil {
		return "", fmt.Errorf("auth: check user active: %w", err)
	}
	if !isActive {
		return "", ErrUserDeactivated
	}
	token, err := CreateSessionTx(ctx, tx, userID, userAgent, ip)
	if err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("auth: commit: %w", err)
	}
	return token, nil
}
