package service

// Project status updates (C34T0) service tests: create/list ordering
// (newest first), invalid status + empty body rejection, author-only
// edit/delete, admin override, tenancy, and the role gates. All tests
// run against the real test database — no skips.

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func setupProjectUpdateTest(t *testing.T) (context.Context, *pgxpool.Pool, string, string, string) {
	t.Helper()
	ctx := context.Background()
	pool := newTestPool(t)
	actor := createTestUser(t, pool, uniqueTestEmail("projupd"))
	slug := uniqueTestSlug("projupd-ws")
	createTestWorkspace(t, pool, "ProjUpd Co", slug, actor)
	ident := uniqueTestIdentifier()
	createTestProject(t, pool, slug, actor, "Eng", ident)
	return ctx, pool, slug, ident, actor
}

func TestProjectUpdateCreateListOrdering(t *testing.T) {
	ctx, pool, slug, ident, actor := setupProjectUpdateTest(t)

	first, err := CreateProjectUpdate(ctx, pool, slug, ident, actor, ProjectUpdateOnTrack, "Kicked off")
	if err != nil {
		t.Fatalf("CreateProjectUpdate: %v", err)
	}
	if first.Status != ProjectUpdateOnTrack || first.Body != "Kicked off" {
		t.Fatalf("created = %+v, want on_track / Kicked off", first)
	}
	if first.Author == nil || first.Author.ID != actor {
		t.Fatalf("created author = %+v, want %s", first.Author, actor)
	}
	if first.ProjectID == "" {
		t.Fatalf("created project_id empty")
	}

	second, err := CreateProjectUpdate(ctx, pool, slug, ident, actor, ProjectUpdateAtRisk, "Blocked on vendor")
	if err != nil {
		t.Fatalf("CreateProjectUpdate 2: %v", err)
	}

	// Newest first.
	list, err := ListProjectUpdates(ctx, pool, slug, ident, actor)
	if err != nil {
		t.Fatalf("ListProjectUpdates: %v", err)
	}
	if len(list) != 2 || list[0].ID != second.ID || list[1].ID != first.ID {
		t.Fatalf("list order = %v, want [second first]", list)
	}
}

func TestProjectUpdateInvalidStatus(t *testing.T) {
	ctx, pool, slug, ident, actor := setupProjectUpdateTest(t)

	for _, status := range []string{"", "green", "ON_TRACK", "on-track", "unknown"} {
		if _, err := CreateProjectUpdate(ctx, pool, slug, ident, actor, status, "Body"); !errors.Is(err, ErrInvalidProjectUpdate) {
			t.Fatalf("create status %q: err = %v, want ErrInvalidProjectUpdate", status, err)
		}
	}

	u, err := CreateProjectUpdate(ctx, pool, slug, ident, actor, ProjectUpdateOnTrack, "OK")
	if err != nil {
		t.Fatalf("CreateProjectUpdate: %v", err)
	}
	bad := "not-a-status"
	if _, err := UpdateProjectUpdate(ctx, pool, slug, ident, actor, u.ID, ProjectUpdatePatch{Status: &bad}); !errors.Is(err, ErrInvalidProjectUpdate) {
		t.Fatalf("patch invalid status: err = %v, want ErrInvalidProjectUpdate", err)
	}
	// The failed PATCH left the row untouched.
	list, err := ListProjectUpdates(ctx, pool, slug, ident, actor)
	if err != nil {
		t.Fatalf("ListProjectUpdates: %v", err)
	}
	if len(list) != 1 || list[0].Status != ProjectUpdateOnTrack {
		t.Fatalf("list after failed patch = %+v, want unchanged on_track", list)
	}
}

func TestProjectUpdateEmptyBodyRejected(t *testing.T) {
	ctx, pool, slug, ident, actor := setupProjectUpdateTest(t)

	for _, body := range []string{"", "   ", "\n\t "} {
		if _, err := CreateProjectUpdate(ctx, pool, slug, ident, actor, ProjectUpdateOnTrack, body); !errors.Is(err, ErrInvalidProjectUpdate) {
			t.Fatalf("create body %q: err = %v, want ErrInvalidProjectUpdate", body, err)
		}
	}

	u, err := CreateProjectUpdate(ctx, pool, slug, ident, actor, ProjectUpdateOnTrack, "  Padded body  ")
	if err != nil {
		t.Fatalf("CreateProjectUpdate: %v", err)
	}
	if u.Body != "Padded body" {
		t.Fatalf("body = %q, want trimmed", u.Body)
	}

	empty := "   "
	if _, err := UpdateProjectUpdate(ctx, pool, slug, ident, actor, u.ID, ProjectUpdatePatch{Body: &empty}); !errors.Is(err, ErrInvalidProjectUpdate) {
		t.Fatalf("patch empty body: err = %v, want ErrInvalidProjectUpdate", err)
	}
	// A PATCH with neither key is invalid too.
	if _, err := UpdateProjectUpdate(ctx, pool, slug, ident, actor, u.ID, ProjectUpdatePatch{}); !errors.Is(err, ErrInvalidProjectUpdate) {
		t.Fatalf("patch no keys: err = %v, want ErrInvalidProjectUpdate", err)
	}
}

func TestProjectUpdatePatchTriState(t *testing.T) {
	ctx, pool, slug, ident, actor := setupProjectUpdateTest(t)

	u, err := CreateProjectUpdate(ctx, pool, slug, ident, actor, ProjectUpdateOnTrack, "Original")
	if err != nil {
		t.Fatalf("CreateProjectUpdate: %v", err)
	}
	// Status-only PATCH leaves the body untouched.
	status := ProjectUpdateOffTrack
	updated, err := UpdateProjectUpdate(ctx, pool, slug, ident, actor, u.ID, ProjectUpdatePatch{Status: &status})
	if err != nil {
		t.Fatalf("UpdateProjectUpdate status-only: %v", err)
	}
	if updated.Status != ProjectUpdateOffTrack || updated.Body != "Original" {
		t.Fatalf("status-only patch = %+v, want off_track / Original", updated)
	}
	// Body-only PATCH leaves the status untouched.
	body := "Revised"
	updated, err = UpdateProjectUpdate(ctx, pool, slug, ident, actor, u.ID, ProjectUpdatePatch{Body: &body})
	if err != nil {
		t.Fatalf("UpdateProjectUpdate body-only: %v", err)
	}
	if updated.Status != ProjectUpdateOffTrack || updated.Body != "Revised" {
		t.Fatalf("body-only patch = %+v, want off_track / Revised", updated)
	}
}

func TestProjectUpdateAuthorOnlyEditDelete(t *testing.T) {
	ctx, pool, slug, ident, actor := setupProjectUpdateTest(t)

	author := createTestUser(t, pool, uniqueTestEmail("projupd-author"))
	addTestMember(t, pool, slug, actor, author, RoleMember)
	other := createTestUser(t, pool, uniqueTestEmail("projupd-other"))
	addTestMember(t, pool, slug, actor, other, RoleMember)

	u, err := CreateProjectUpdate(ctx, pool, slug, ident, author, ProjectUpdateOnTrack, "Author's update")
	if err != nil {
		t.Fatalf("CreateProjectUpdate: %v", err)
	}

	// Another member (not the author, not admin) gets 403-equivalent
	// on edit and delete.
	body := "Hijacked"
	if _, err := UpdateProjectUpdate(ctx, pool, slug, ident, other, u.ID, ProjectUpdatePatch{Body: &body}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("other patch: err = %v, want ErrForbidden", err)
	}
	if err := DeleteProjectUpdate(ctx, pool, slug, ident, other, u.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("other delete: err = %v, want ErrForbidden", err)
	}

	// The author edits and deletes their own.
	if _, err := UpdateProjectUpdate(ctx, pool, slug, ident, author, u.ID, ProjectUpdatePatch{Body: &body}); err != nil {
		t.Fatalf("author patch: %v", err)
	}
	if err := DeleteProjectUpdate(ctx, pool, slug, ident, author, u.ID); err != nil {
		t.Fatalf("author delete: %v", err)
	}
	// Second delete is not-found.
	if err := DeleteProjectUpdate(ctx, pool, slug, ident, author, u.ID); !errors.Is(err, ErrProjectUpdateNotFound) {
		t.Fatalf("second delete: err = %v, want ErrProjectUpdateNotFound", err)
	}
}

func TestProjectUpdateAdminOverride(t *testing.T) {
	ctx, pool, slug, ident, actor := setupProjectUpdateTest(t)

	author := createTestUser(t, pool, uniqueTestEmail("projupd-admin-author"))
	addTestMember(t, pool, slug, actor, author, RoleMember)

	u, err := CreateProjectUpdate(ctx, pool, slug, ident, author, ProjectUpdateOnTrack, "Needs admin fix")
	if err != nil {
		t.Fatalf("CreateProjectUpdate: %v", err)
	}

	// The workspace admin (creator) edits and deletes someone else's
	// update.
	body := "Fixed by admin"
	updated, err := UpdateProjectUpdate(ctx, pool, slug, ident, actor, u.ID, ProjectUpdatePatch{Body: &body})
	if err != nil {
		t.Fatalf("admin patch other's: %v", err)
	}
	if updated.Body != body {
		t.Fatalf("admin patch body = %q, want %q", updated.Body, body)
	}
	if err := DeleteProjectUpdate(ctx, pool, slug, ident, actor, u.ID); err != nil {
		t.Fatalf("admin delete other's: %v", err)
	}
}

func TestProjectUpdateRoleGatesAndTenancy(t *testing.T) {
	ctx, pool, slug, ident, actor := setupProjectUpdateTest(t)

	viewer := createTestUser(t, pool, uniqueTestEmail("projupd-viewer"))
	addTestMember(t, pool, slug, actor, viewer, RoleViewer)
	guest := createTestUser(t, pool, uniqueTestEmail("projupd-guest"))
	addTestMember(t, pool, slug, actor, guest, RoleGuest)
	outsider := createTestUser(t, pool, uniqueTestEmail("projupd-outsider"))

	u, err := CreateProjectUpdate(ctx, pool, slug, ident, actor, ProjectUpdateOnTrack, "Visible")
	if err != nil {
		t.Fatalf("CreateProjectUpdate: %v", err)
	}

	// Viewer reads, but may not create.
	if _, err := ListProjectUpdates(ctx, pool, slug, ident, viewer); err != nil {
		t.Fatalf("viewer list: %v", err)
	}
	if _, err := CreateProjectUpdate(ctx, pool, slug, ident, viewer, ProjectUpdateOnTrack, "Nope"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer create: err = %v, want ErrForbidden", err)
	}
	if _, err := UpdateProjectUpdate(ctx, pool, slug, ident, viewer, u.ID, ProjectUpdatePatch{Body: strptr("Nope")}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer patch: err = %v, want ErrForbidden", err)
	}
	if err := DeleteProjectUpdate(ctx, pool, slug, ident, viewer, u.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("viewer delete: err = %v, want ErrForbidden", err)
	}

	// Guest may not even read.
	if _, err := ListProjectUpdates(ctx, pool, slug, ident, guest); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest list: err = %v, want ErrForbidden", err)
	}

	// Non-member: tenancy boundary → ErrNotFound.
	if _, err := ListProjectUpdates(ctx, pool, slug, ident, outsider); !errors.Is(err, ErrNotFound) {
		t.Fatalf("outsider list: err = %v, want ErrNotFound", err)
	}

	// Unknown project → ErrProjectNotFound; unknown update → 404
	// sentinel.
	if _, err := ListProjectUpdates(ctx, pool, slug, "NOPE", actor); !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("bad project list: err = %v, want ErrProjectNotFound", err)
	}
	if err := DeleteProjectUpdate(ctx, pool, slug, ident, actor, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, ErrProjectUpdateNotFound) {
		t.Fatalf("unknown update delete: err = %v, want ErrProjectUpdateNotFound", err)
	}
	if _, err := UpdateProjectUpdate(ctx, pool, slug, ident, actor, "00000000-0000-0000-0000-000000000000", ProjectUpdatePatch{Body: strptr("x")}); !errors.Is(err, ErrProjectUpdateNotFound) {
		t.Fatalf("unknown update patch: err = %v, want ErrProjectUpdateNotFound", err)
	}
}
