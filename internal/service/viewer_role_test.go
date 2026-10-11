package service

// Viewer role tests (C17T5): RoleViewer (10) is read-only. Every write
// path gates on role < RoleMember via the pre-existing checks, so this
// test pins the contract: viewers get ErrForbidden on writes (the same
// 403 paths guests already hit) and full read access like any member.
// It also pins that admin/member/guest behavior is unchanged: assigning
// the viewer role works end-to-end (validRole + the 000045 CHECK
// constraint), invalid roles are still rejected, and members keep
// write access.
//
// All tests run against the real test database — no skips.

import (
	"context"
	"errors"
	"testing"
)

func TestViewerRoleGates(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	admin := createTestUser(t, pool, uniqueTestEmail("viewer-gate-admin"))
	member := createTestUser(t, pool, uniqueTestEmail("viewer-gate-member"))
	viewer := createTestUser(t, pool, uniqueTestEmail("viewer-gate-viewer"))
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-viewer"), admin)
	p := createTestProject(t, pool, ws.Slug, admin, "Engineering", uniqueTestIdentifier())

	// Admin can assign the viewer role: exercises validRole(10) and the
	// widened workspace_members CHECK constraint end-to-end (this insert
	// would fail on the pre-000045 role IN (5, 15, 20) constraint).
	if err := UpsertMember(ctx, pool, ws.Slug, admin, viewer, RoleViewer); err != nil {
		t.Fatalf("UpsertMember viewer: %v", err)
	}
	if err := UpsertMember(ctx, pool, ws.Slug, admin, member, RoleMember); err != nil {
		t.Fatalf("UpsertMember member: %v", err)
	}
	// Roles adjacent to 10 are still rejected.
	for _, role := range []int{9, 11} {
		if err := UpsertMember(ctx, pool, ws.Slug, admin, viewer, role); !errors.Is(err, ErrInvalidRole) {
			t.Fatalf("role %d: err = %v, want ErrInvalidRole", role, err)
		}
	}
	// Invite honours the viewer role too, and still defaults to member.
	targetEmail := uniqueTestEmail("viewer-gate-invite")
	target := createTestUser(t, pool, targetEmail)
	if _, err := InviteMembers(ctx, pool, ws.Slug, admin, []string{targetEmail}, RoleViewer); err != nil {
		t.Fatalf("InviteMembers viewer: %v", err)
	}
	var gotRole int
	if err := pool.QueryRow(ctx,
		`SELECT role FROM workspace_members WHERE workspace_id = $1::uuid AND user_id = $2::uuid`,
		ws.ID, target).Scan(&gotRole); err != nil {
		t.Fatalf("invite role scan: %v", err)
	}
	if gotRole != RoleViewer {
		t.Fatalf("invited role = %d, want %d (viewer)", gotRole, RoleViewer)
	}

	// Viewer cannot create an issue.
	if _, err := CreateIssue(ctx, pool, ws.Slug, p.Identifier, viewer, CreateIssueInput{Name: "x"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer create: err = %v, want ErrForbidden", err)
	}

	iss := createTestIssue(t, pool, ws.Slug, p.Identifier, admin, "Viewable")

	// Viewer CAN read everything a member can: detail, list, members,
	// comments (once one exists).
	if _, err := GetIssue(ctx, pool, ws.Slug, p.Identifier, iss.ID, viewer); err != nil {
		t.Fatalf("viewer read detail: %v", err)
	}
	if _, err := ListIssues(ctx, pool, ws.Slug, p.Identifier, viewer, ListIssuesInput{}); err != nil {
		t.Fatalf("viewer read list: %v", err)
	}
	if _, err := ListMembers(ctx, pool, ws.Slug, viewer); err != nil {
		t.Fatalf("viewer read members: %v", err)
	}

	// Viewer cannot update or delete the issue.
	if _, err := UpdateIssue(ctx, pool, ws.Slug, p.Identifier, iss.ID, viewer, IssuePatch{Name: strPtr("y")}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer update: err = %v, want ErrForbidden", err)
	}
	if err := DeleteIssue(ctx, pool, ws.Slug, p.Identifier, iss.ID, viewer); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer delete: err = %v, want ErrForbidden", err)
	}

	// Viewer cannot comment, but can read comments.
	if _, err := CreateComment(ctx, pool, ws.Slug, p.Identifier, iss.ID, viewer, tiptapComment("viewer says hi"), nil); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer comment: err = %v, want ErrForbidden", err)
	}
	if _, err := CreateComment(ctx, pool, ws.Slug, p.Identifier, iss.ID, admin, tiptapComment("admin says hi"), nil); err != nil {
		t.Fatalf("admin comment: %v", err)
	}
	if _, err := ListComments(ctx, pool, ws.Slug, p.Identifier, iss.ID, viewer); err != nil {
		t.Fatalf("viewer read comments: %v", err)
	}

	// Member behavior is unchanged: full write access on the same paths.
	miss, err := CreateIssue(ctx, pool, ws.Slug, p.Identifier, member, CreateIssueInput{Name: "Member issue"})
	if err != nil {
		t.Fatalf("member create: %v", err)
	}
	if _, err := UpdateIssue(ctx, pool, ws.Slug, p.Identifier, miss.ID, member, IssuePatch{Name: strPtr("Renamed")}); err != nil {
		t.Fatalf("member update: %v", err)
	}
	if _, err := CreateComment(ctx, pool, ws.Slug, p.Identifier, miss.ID, member, tiptapComment("member says hi"), nil); err != nil {
		t.Fatalf("member comment: %v", err)
	}
}

// TestViewerRoleSortOrder pins the role ordering contract the authz
// checks depend on: guest < viewer < member < admin. If these ever
// change, every `role < RoleMember` gate needs re-auditing.
func TestViewerRoleSortOrder(t *testing.T) {
	if !(RoleGuest < RoleViewer && RoleViewer < RoleMember && RoleMember < RoleAdmin) {
		t.Fatalf("role order broken: guest=%d viewer=%d member=%d admin=%d",
			RoleGuest, RoleViewer, RoleMember, RoleAdmin)
	}
	if RoleViewer != 10 {
		t.Fatalf("RoleViewer = %d, want 10", RoleViewer)
	}
}
