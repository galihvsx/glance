package service

// C93T2: privatizing a project revokes its outstanding share links,
// in the same transaction as the visibility flip. Revocation is
// permanent: re-publicizing does not un-revoke. Every other
// transition (private -> public, same-value, or a patch that does
// not touch visibility) revokes nothing, and the visibility authz
// gate (effective Admin 20) is unchanged.

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func c93MintIssueAndPageLinks(t *testing.T, ctx context.Context, pool *pgxpool.Pool, slug, ident, admin string) (issueTok, pageTok string) {
	t.Helper()
	iss := createTestIssue(t, pool, slug, ident, admin, "Shared issue")
	s, err := CreateShareLink(ctx, pool, slug, ident, iss.ID, admin, nil)
	if err != nil {
		t.Fatalf("CreateShareLink %s: %v", ident, err)
	}
	content := "shared page body"
	p, err := CreatePage(ctx, pool, slug, ident, admin, PageInput{Title: "Shared page", Content: &content})
	if err != nil {
		t.Fatalf("CreatePage %s: %v", ident, err)
	}
	ps, err := CreatePageShareLink(ctx, pool, slug, ident, p.ID, admin, nil)
	if err != nil {
		t.Fatalf("CreatePageShareLink %s: %v", ident, err)
	}
	return s.Token, ps.Token
}

func TestPrivatizeRevokesProjectLinksOnly(t *testing.T) {
	ctx, pool, slug, admin, _, _, _ := setupACLTest(t)
	// ALPHA is public (setupACLTest); BETA is a second public project.
	if _, err := CreateProject(ctx, pool, slug, admin, "Beta", "BETA"); err != nil {
		t.Fatalf("CreateProject BETA: %v", err)
	}
	alphaIssue, alphaPage := c93MintIssueAndPageLinks(t, ctx, pool, slug, "ALPHA", admin)
	betaIssue, _ := c93MintIssueAndPageLinks(t, ctx, pool, slug, "BETA", admin)

	// Both projects' links resolve before the flip.
	for _, tok := range []string{alphaIssue, alphaPage, betaIssue} {
		if _, err := GetPublicShare(ctx, pool, tok); err != nil {
			t.Fatalf("pre-flip GetPublicShare %q: %v", tok, err)
		}
	}

	priv := VisibilityPrivate
	if _, err := UpdateProject(ctx, pool, slug, "ALPHA", admin, ProjectPatch{Visibility: &priv}); err != nil {
		t.Fatalf("privatize ALPHA: %v", err)
	}

	// ALPHA's issue + page links now resolve to the shared 404.
	for _, tok := range []string{alphaIssue, alphaPage} {
		if _, err := GetPublicShare(ctx, pool, tok); !errors.Is(err, ErrShareNotFound) {
			t.Fatalf("revoked ALPHA token: err=%v, want ErrShareNotFound", err)
		}
	}
	// BETA's links are untouched.
	if _, err := GetPublicShare(ctx, pool, betaIssue); err != nil {
		t.Fatalf("BETA link after ALPHA privatize: %v", err)
	}
	// Already-revoked links stay revoked: listing shows none for ALPHA.
	// (ListShareLinks filters revoked_at IS NULL; verified via the
	// 404 above, which is the public contract.)
}

func TestPrivateToPublicRevokesNothing(t *testing.T) {
	ctx, pool, slug, admin, _, _, _ := setupACLTest(t)
	// ALPHA private first, then mint while private (admin reaches it).
	setPrivate(t, ctx, pool, slug, admin, "ALPHA")
	issueTok, pageTok := c93MintIssueAndPageLinks(t, ctx, pool, slug, "ALPHA", admin)

	pub := VisibilityPublic
	if _, err := UpdateProject(ctx, pool, slug, "ALPHA", admin, ProjectPatch{Visibility: &pub}); err != nil {
		t.Fatalf("re-publicize ALPHA: %v", err)
	}
	for _, tok := range []string{issueTok, pageTok} {
		if _, err := GetPublicShare(ctx, pool, tok); err != nil {
			t.Fatalf("link after private->public %q: %v", tok, err)
		}
	}
}

func TestRePublicizeDoesNotUnRevoke(t *testing.T) {
	ctx, pool, slug, admin, _, _, _ := setupACLTest(t)
	issueTok, pageTok := c93MintIssueAndPageLinks(t, ctx, pool, slug, "ALPHA", admin)

	priv := VisibilityPrivate
	if _, err := UpdateProject(ctx, pool, slug, "ALPHA", admin, ProjectPatch{Visibility: &priv}); err != nil {
		t.Fatalf("privatize ALPHA: %v", err)
	}
	pub := VisibilityPublic
	if _, err := UpdateProject(ctx, pool, slug, "ALPHA", admin, ProjectPatch{Visibility: &pub}); err != nil {
		t.Fatalf("re-publicize ALPHA: %v", err)
	}
	// Revocation is the control: the links stay dead.
	for _, tok := range []string{issueTok, pageTok} {
		if _, err := GetPublicShare(ctx, pool, tok); !errors.Is(err, ErrShareNotFound) {
			t.Fatalf("re-publicized token: err=%v, want ErrShareNotFound", err)
		}
	}
}

func TestNonVisibilityAndSameValuePatchesRevokeNothing(t *testing.T) {
	ctx, pool, slug, admin, _, _, _ := setupACLTest(t)
	issueTok, _ := c93MintIssueAndPageLinks(t, ctx, pool, slug, "ALPHA", admin)

	// A patch that does not touch visibility revokes nothing.
	name := "Alpha Renamed"
	if _, err := UpdateProject(ctx, pool, slug, "ALPHA", admin, ProjectPatch{Name: &name}); err != nil {
		t.Fatalf("rename ALPHA: %v", err)
	}
	if _, err := GetPublicShare(ctx, pool, issueTok); err != nil {
		t.Fatalf("link after rename: %v", err)
	}
	// Same-value public -> public revokes nothing.
	pub := VisibilityPublic
	if _, err := UpdateProject(ctx, pool, slug, "ALPHA", admin, ProjectPatch{Visibility: &pub}); err != nil {
		t.Fatalf("same-value public patch: %v", err)
	}
	if _, err := GetPublicShare(ctx, pool, issueTok); err != nil {
		t.Fatalf("link after same-value public patch: %v", err)
	}
	// Same-value private -> private revokes nothing: privatize first
	// (which revokes the old link), mint fresh while private, re-apply.
	priv := VisibilityPrivate
	if _, err := UpdateProject(ctx, pool, slug, "ALPHA", admin, ProjectPatch{Visibility: &priv}); err != nil {
		t.Fatalf("privatize ALPHA: %v", err)
	}
	iss := createTestIssue(t, pool, slug, "ALPHA", admin, "Private issue")
	s, err := CreateShareLink(ctx, pool, slug, "ALPHA", iss.ID, admin, nil)
	if err != nil {
		t.Fatalf("CreateShareLink private: %v", err)
	}
	if _, err := UpdateProject(ctx, pool, slug, "ALPHA", admin, ProjectPatch{Visibility: &priv}); err != nil {
		t.Fatalf("same-value private patch: %v", err)
	}
	if _, err := GetPublicShare(ctx, pool, s.Token); err != nil {
		t.Fatalf("link after same-value private patch: %v", err)
	}
}

func TestNonAdminVisibilityChangeStill403AndRevokesNothing(t *testing.T) {
	ctx, pool, slug, admin, member, _, _ := setupACLTest(t)
	issueTok, _ := c93MintIssueAndPageLinks(t, ctx, pool, slug, "ALPHA", admin)

	priv := VisibilityPrivate
	if _, err := UpdateProject(ctx, pool, slug, "ALPHA", member, ProjectPatch{Visibility: &priv}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member visibility change: err=%v, want ErrForbidden", err)
	}
	// The rejected flip revoked nothing.
	if _, err := GetPublicShare(ctx, pool, issueTok); err != nil {
		t.Fatalf("link after rejected member flip: %v", err)
	}
}
