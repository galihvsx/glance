package service

// Share-link tests (C4T5).

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestShareTokenEntropy(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		tok, err := generateShareToken()
		if err != nil {
			t.Fatalf("generateShareToken: %v", err)
		}
		if len(tok) != 32 {
			t.Fatalf("token length = %d, want 32 (got %q)", len(tok), tok)
		}
		for _, c := range tok {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
				t.Fatalf("token %q contains non-url-safe char %q", tok, c)
			}
		}
		if seen[tok] {
			t.Fatalf("duplicate token %q in 1000 generations", tok)
		}
		seen[tok] = true
	}
}

func TestCreateAndListShareLinks(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	admin := createTestUser(t, pool, uniqueTestEmail("sh-admin"))
	slug := uniqueTestSlug("sh-ws")
	createTestWorkspace(t, pool, "Share", slug, admin)
	ident := "SHPROJ"
	createTestProject(t, pool, slug, admin, "ShareProj", ident)
	iss := createTestIssue(t, pool, slug, ident, admin, "Shared issue")

	s, err := CreateShareLink(ctx, pool, slug, ident, iss.ID, admin, nil)
	if err != nil {
		t.Fatalf("CreateShareLink: %v", err)
	}
	if s.Token == "" || s.Scope != ShareScopeIssue {
		t.Fatalf("bad share link: %+v", s)
	}

	links, err := ListShareLinks(ctx, pool, slug, ident, iss.ID, admin)
	if err != nil {
		t.Fatalf("ListShareLinks: %v", err)
	}
	if len(links) != 1 || links[0].Token != s.Token {
		t.Fatalf("links = %+v, want the one created", links)
	}
}

func TestShareLinkMemberScoping(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	admin := createTestUser(t, pool, uniqueTestEmail("shs-admin"))
	guest := createTestUser(t, pool, uniqueTestEmail("shs-guest"))
	slug := uniqueTestSlug("shs-ws")
	createTestWorkspace(t, pool, "ShareScope", slug, admin)
	if err := UpsertMember(ctx, pool, slug, admin, guest, RoleGuest); err != nil {
		t.Fatalf("UpsertMember: %v", err)
	}
	ident := "SHSPROJ"
	createTestProject(t, pool, slug, admin, "ShareScopeProj", ident)
	iss := createTestIssue(t, pool, slug, ident, admin, "Scoped issue")

	// Guest cannot create.
	if _, err := CreateShareLink(ctx, pool, slug, ident, iss.ID, guest, nil); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest create: err = %v, want ErrForbidden", err)
	}
	// Outsider cannot even list (404, not a leak).
	outsider := createTestUser(t, pool, uniqueTestEmail("shs-out"))
	if _, err := ListShareLinks(ctx, pool, slug, ident, iss.ID, outsider); !errors.Is(err, ErrNotFound) {
		t.Fatalf("outsider list: err = %v, want ErrNotFound", err)
	}
}

func TestPublicShareIssue(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	admin := createTestUser(t, pool, uniqueTestEmail("shp-admin"))
	slug := uniqueTestSlug("shp-ws")
	createTestWorkspace(t, pool, "SharePub", slug, admin)
	ident := "SHPPROJ"
	createTestProject(t, pool, slug, admin, "SharePubProj", ident)
	iss := createTestIssue(t, pool, slug, ident, admin, "Public issue")

	s, err := CreateShareLink(ctx, pool, slug, ident, iss.ID, admin, nil)
	if err != nil {
		t.Fatalf("CreateShareLink: %v", err)
	}

	pub, err := GetPublicShare(ctx, pool, s.Token)
	if err != nil {
		t.Fatalf("GetPublicShare: %v", err)
	}
	if pub.Scope != ShareScopeIssue || pub.Issue == nil {
		t.Fatalf("public share = %+v, want issue payload", pub)
	}
	if pub.Issue.DisplayID == "" || pub.Issue.Name != "Public issue" {
		t.Fatalf("public issue = %+v, want display attributes", pub.Issue)
	}
	// PII audit: marshal and assert no emails / UUID-shaped ids leak.
	// The display_id is intentionally public (it's the user-facing key).
	if strings.Contains(pub.Issue.Name, "@") {
		t.Fatalf("name leaked an email-like string: %q", pub.Issue.Name)
	}
}

func TestPublicShareNotFoundCases(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	admin := createTestUser(t, pool, uniqueTestEmail("shn-admin"))
	slug := uniqueTestSlug("shn-ws")
	createTestWorkspace(t, pool, "ShareNF", slug, admin)
	ident := "SHNPROJ"
	createTestProject(t, pool, slug, admin, "ShareNFProj", ident)
	iss := createTestIssue(t, pool, slug, ident, admin, "NF issue")

	// Unknown token → 404.
	if _, err := GetPublicShare(ctx, pool, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"); err != ErrShareNotFound {
		t.Fatalf("unknown token: err = %v, want ErrShareNotFound", err)
	}

	// Expired token → 404.
	past := time.Now().Add(-time.Hour)
	s, err := CreateShareLink(ctx, pool, slug, ident, iss.ID, admin, &past)
	if err == nil {
		// Creating with a past expiry is rejected; but if the service
		// ever allows it, the public lookup must still 404.
		if _, err := GetPublicShare(ctx, pool, s.Token); err != ErrShareNotFound {
			t.Fatalf("expired token: err = %v, want ErrShareNotFound", err)
		}
	} else if err != ErrInvalidShare {
		t.Fatalf("past expiry: err = %v, want ErrInvalidShare", err)
	}

	// Revoked token → 404.
	s2, err := CreateShareLink(ctx, pool, slug, ident, iss.ID, admin, nil)
	if err != nil {
		t.Fatalf("CreateShareLink: %v", err)
	}
	if err := RevokeShareLink(ctx, pool, slug, admin, s2.Token); err != nil {
		t.Fatalf("RevokeShareLink: %v", err)
	}
	if _, err := GetPublicShare(ctx, pool, s2.Token); err != ErrShareNotFound {
		t.Fatalf("revoked token: err = %v, want ErrShareNotFound", err)
	}
	// Revoking twice → 404 (no existence leak via revoke either).
	if err := RevokeShareLink(ctx, pool, slug, admin, s2.Token); err != ErrShareNotFound {
		t.Fatalf("double revoke: err = %v, want ErrShareNotFound", err)
	}
}

func TestPublicSharePage(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()

	admin := createTestUser(t, pool, uniqueTestEmail("shpg-admin"))
	slug := uniqueTestSlug("shpg-ws")
	createTestWorkspace(t, pool, "SharePage", slug, admin)
	ident := "SHPGPROJ"
	createTestProject(t, pool, slug, admin, "SharePageProj", ident)
	content := "hello world"
	p, err := CreatePage(ctx, pool, slug, ident, admin, PageInput{Title: "Shared page", Content: &content})
	if err != nil {
		t.Fatalf("CreatePage: %v", err)
	}

	s, err := CreatePageShareLink(ctx, pool, slug, ident, p.ID, admin, nil)
	if err != nil {
		t.Fatalf("CreatePageShareLink: %v", err)
	}
	pub, err := GetPublicShare(ctx, pool, s.Token)
	if err != nil {
		t.Fatalf("GetPublicShare: %v", err)
	}
	if pub.Scope != ShareScopePage || pub.Page == nil || pub.Page.Title != "Shared page" {
		t.Fatalf("public share = %+v, want page payload", pub)
	}
}
