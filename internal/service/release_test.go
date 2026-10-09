package service

// Releases (C4T6): project-scoped CRUD, bulk issue assignment via
// issues.release_id, and the delete guard (409 / ?reassign= / ?force=true).

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func setupReleaseTest(t *testing.T) (context.Context, *pgxpool.Pool, string, string, string) {
	t.Helper()
	ctx := context.Background()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	actor := createTestUser(t, pool, uniqueTestEmail("release"))
	slug := uniqueTestSlug("release-ws")
	createTestWorkspace(t, pool, "Release Co", slug, actor)
	ident := uniqueTestIdentifier()
	createTestProject(t, pool, slug, actor, "Eng", ident)
	return ctx, pool, slug, ident, actor
}

func TestReleaseCRUD(t *testing.T) {
	ctx, pool, slug, ident, actor := setupReleaseTest(t)

	desc := "First milestone"
	r, err := CreateRelease(ctx, pool, slug, ident, actor, ReleaseInput{
		Name:        "v1.0",
		Description: &desc,
		Status:      "planned",
		ReleaseDate: strptr("2026-12-01"),
	})
	if err != nil {
		t.Fatalf("CreateRelease: %v", err)
	}
	if r.Name != "v1.0" || r.Status != "planned" || r.Description != desc {
		t.Fatalf("got %+v", r)
	}
	if r.ReleaseDate == nil || r.ReleaseDate.Format("2006-01-02") != "2026-12-01" {
		t.Fatalf("release_date = %v", r.ReleaseDate)
	}
	if r.IssueCount != 0 {
		t.Fatalf("issue_count = %d, want 0", r.IssueCount)
	}

	// Default status is planned.
	r2, err := CreateRelease(ctx, pool, slug, ident, actor, ReleaseInput{Name: "v1.1"})
	if err != nil {
		t.Fatalf("CreateRelease default: %v", err)
	}
	if r2.Status != "planned" {
		t.Fatalf("status = %q, want planned", r2.Status)
	}

	// Duplicate name → conflict.
	if _, err := CreateRelease(ctx, pool, slug, ident, actor, ReleaseInput{Name: "v1.0"}); !errors.Is(err, ErrReleaseConflict) {
		t.Fatalf("dup name: err = %v, want ErrReleaseConflict", err)
	}

	// Bad status → 400.
	if _, err := CreateRelease(ctx, pool, slug, ident, actor, ReleaseInput{Name: "bad", Status: "shipped"}); !errors.Is(err, ErrInvalidRelease) {
		t.Fatalf("bad status: err = %v, want ErrInvalidRelease", err)
	}

	// Empty name → 400.
	if _, err := CreateRelease(ctx, pool, slug, ident, actor, ReleaseInput{Name: "  "}); !errors.Is(err, ErrNameRequired) {
		t.Fatalf("empty name: err = %v, want ErrNameRequired", err)
	}

	// List → both releases.
	list, err := ListReleases(ctx, pool, slug, ident, actor)
	if err != nil {
		t.Fatalf("ListReleases: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("len = %d, want 2", len(list))
	}

	// Get → 200 shape; unknown → 404; malformed → 400.
	got, err := GetRelease(ctx, pool, slug, ident, actor, r.ID)
	if err != nil || got.ID != r.ID {
		t.Fatalf("GetRelease: %v", err)
	}
	if _, err := GetRelease(ctx, pool, slug, ident, actor, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, ErrReleaseNotFound) {
		t.Fatalf("unknown: err = %v, want ErrReleaseNotFound", err)
	}
	if _, err := GetRelease(ctx, pool, slug, ident, actor, "nope"); !errors.Is(err, ErrInvalidReleaseID) {
		t.Fatalf("malformed: err = %v, want ErrInvalidReleaseID", err)
	}
}

func TestReleasePatch(t *testing.T) {
	ctx, pool, slug, ident, actor := setupReleaseTest(t)

	r, err := CreateRelease(ctx, pool, slug, ident, actor, ReleaseInput{
		Name: "v2.0", ReleaseDate: strptr("2026-11-01"),
	})
	if err != nil {
		t.Fatalf("CreateRelease: %v", err)
	}

	// Partial: status flip + clear the date.
	upd, err := UpdateRelease(ctx, pool, slug, ident, actor, r.ID, ReleasePatch{
		Status:      strptr("released"),
		ReleaseDate: strptr(""),
	})
	if err != nil {
		t.Fatalf("UpdateRelease: %v", err)
	}
	if upd.Status != "released" || upd.ReleaseDate != nil || upd.Name != "v2.0" {
		t.Fatalf("got %+v", upd)
	}

	// Rename to an existing name → conflict.
	r2, err := CreateRelease(ctx, pool, slug, ident, actor, ReleaseInput{Name: "v2.1"})
	if err != nil {
		t.Fatalf("CreateRelease: %v", err)
	}
	if _, err := UpdateRelease(ctx, pool, slug, ident, actor, r2.ID, ReleasePatch{Name: strptr("v2.0")}); !errors.Is(err, ErrReleaseConflict) {
		t.Fatalf("rename conflict: err = %v, want ErrReleaseConflict", err)
	}

	// Empty patch → 400.
	if _, err := UpdateRelease(ctx, pool, slug, ident, actor, r.ID, ReleasePatch{}); !errors.Is(err, ErrNothingToUpdate) {
		t.Fatalf("empty patch: err = %v, want ErrNothingToUpdate", err)
	}

	// Bad status → 400.
	if _, err := UpdateRelease(ctx, pool, slug, ident, actor, r.ID, ReleasePatch{Status: strptr("done")}); !errors.Is(err, ErrInvalidRelease) {
		t.Fatalf("bad status: err = %v, want ErrInvalidRelease", err)
	}
}

func TestReleaseScoping(t *testing.T) {
	ctx, pool, slug, ident, actor := setupReleaseTest(t)

	// Outsider (no membership) → 404, not a leak.
	outsider := createTestUser(t, pool, uniqueTestEmail("release-out"))
	if _, err := ListReleases(ctx, pool, slug, ident, outsider); !errors.Is(err, ErrNotFound) {
		t.Fatalf("outsider list: err = %v, want ErrNotFound", err)
	}
	if _, err := CreateRelease(ctx, pool, slug, ident, outsider, ReleaseInput{Name: "x"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("outsider create: err = %v, want ErrNotFound", err)
	}

	// Guest may read but not mutate.
	guest := createTestUser(t, pool, uniqueTestEmail("release-guest"))
	if err := UpsertMember(ctx, pool, slug, actor, guest, RoleGuest); err != nil {
		t.Fatalf("UpsertMember: %v", err)
	}
	if _, err := ListReleases(ctx, pool, slug, ident, guest); err != nil {
		t.Fatalf("guest list: %v", err)
	}
	if _, err := CreateRelease(ctx, pool, slug, ident, guest, ReleaseInput{Name: "g"}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest create: err = %v, want ErrForbidden", err)
	}

	// Cross-project release id → 404.
	ident2 := uniqueTestIdentifier()
	createTestProject(t, pool, slug, actor, "Eng2", ident2)
	r, err := CreateRelease(ctx, pool, slug, ident2, actor, ReleaseInput{Name: "other"})
	if err != nil {
		t.Fatalf("CreateRelease: %v", err)
	}
	if _, err := GetRelease(ctx, pool, slug, ident, actor, r.ID); !errors.Is(err, ErrReleaseNotFound) {
		t.Fatalf("cross-project: err = %v, want ErrReleaseNotFound", err)
	}
}

func TestReleaseIssueAssignment(t *testing.T) {
	ctx, pool, slug, ident, actor := setupReleaseTest(t)

	r, err := CreateRelease(ctx, pool, slug, ident, actor, ReleaseInput{Name: "v3.0"})
	if err != nil {
		t.Fatalf("CreateRelease: %v", err)
	}
	iss1 := createTestIssue(t, pool, slug, ident, actor, "Issue one")
	iss2 := createTestIssue(t, pool, slug, ident, actor, "Issue two")

	// Assign → issues listed under the release.
	if err := AssignReleaseIssues(ctx, pool, slug, ident, actor, r.ID, []string{iss1.ID, iss2.ID}); err != nil {
		t.Fatalf("AssignReleaseIssues: %v", err)
	}
	issues, err := ListReleaseIssues(ctx, pool, slug, ident, actor, r.ID)
	if err != nil {
		t.Fatalf("ListReleaseIssues: %v", err)
	}
	if len(issues) != 2 {
		t.Fatalf("len = %d, want 2", len(issues))
	}

	// Idempotent: re-assign is a no-op.
	if err := AssignReleaseIssues(ctx, pool, slug, ident, actor, r.ID, []string{iss1.ID}); err != nil {
		t.Fatalf("re-assign: %v", err)
	}

	// issue_count reflects the assignment.
	got, err := GetRelease(ctx, pool, slug, ident, actor, r.ID)
	if err != nil || got.IssueCount != 2 {
		t.Fatalf("issue_count = %d, err = %v", got.IssueCount, err)
	}

	// Foreign issue → 404.
	ident2 := uniqueTestIdentifier()
	createTestProject(t, pool, slug, actor, "Eng2", ident2)
	foreign := createTestIssue(t, pool, slug, ident2, actor, "Foreign")
	if err := AssignReleaseIssues(ctx, pool, slug, ident, actor, r.ID, []string{foreign.ID}); !errors.Is(err, ErrIssueNotFound) {
		t.Fatalf("foreign assign: err = %v, want ErrIssueNotFound", err)
	}

	// Malformed id → 400.
	if err := AssignReleaseIssues(ctx, pool, slug, ident, actor, r.ID, []string{"nope"}); !errors.Is(err, ErrInvalidReleaseID) {
		t.Fatalf("malformed: err = %v, want ErrInvalidReleaseID", err)
	}

	// Empty list → 400.
	if err := AssignReleaseIssues(ctx, pool, slug, ident, actor, r.ID, nil); !errors.Is(err, ErrBulkEmptyIDs) {
		t.Fatalf("empty: err = %v, want ErrBulkEmptyIDs", err)
	}

	// Unassign one → detached.
	if err := UnassignReleaseIssues(ctx, pool, slug, ident, actor, r.ID, []string{iss1.ID}); err != nil {
		t.Fatalf("UnassignReleaseIssues: %v", err)
	}
	issues, err = ListReleaseIssues(ctx, pool, slug, ident, actor, r.ID)
	if err != nil || len(issues) != 1 {
		t.Fatalf("after unassign: len = %d, err = %v", len(issues), err)
	}
}

func TestReleaseDeleteGuard(t *testing.T) {
	ctx, pool, slug, ident, actor := setupReleaseTest(t)

	r, err := CreateRelease(ctx, pool, slug, ident, actor, ReleaseInput{Name: "v4.0"})
	if err != nil {
		t.Fatalf("CreateRelease: %v", err)
	}
	iss := createTestIssue(t, pool, slug, ident, actor, "Guarded issue")
	if err := AssignReleaseIssues(ctx, pool, slug, ident, actor, r.ID, []string{iss.ID}); err != nil {
		t.Fatalf("AssignReleaseIssues: %v", err)
	}

	// No flag → 409, release survives.
	if err := DeleteRelease(ctx, pool, slug, ident, actor, r.ID, nil, false); !errors.Is(err, ErrReleaseHasIssues) {
		t.Fatalf("delete with issues: err = %v, want ErrReleaseHasIssues", err)
	}

	// ?force=true → detached (SET NULL), issue survives.
	if err := DeleteRelease(ctx, pool, slug, ident, actor, r.ID, nil, true); err != nil {
		t.Fatalf("force delete: %v", err)
	}
	var releaseID *string
	if err := pool.QueryRow(ctx,
		`SELECT release_id::text FROM issues WHERE id = $1::uuid`, iss.ID).Scan(&releaseID); err != nil {
		t.Fatalf("issue lookup: %v", err)
	}
	if releaseID != nil {
		t.Fatalf("release_id = %v, want NULL after force delete", *releaseID)
	}

	// ?reassign= moves issues to the target release.
	r1, err := CreateRelease(ctx, pool, slug, ident, actor, ReleaseInput{Name: "v4.1"})
	if err != nil {
		t.Fatalf("CreateRelease: %v", err)
	}
	r2, err := CreateRelease(ctx, pool, slug, ident, actor, ReleaseInput{Name: "v4.2"})
	if err != nil {
		t.Fatalf("CreateRelease: %v", err)
	}
	iss2 := createTestIssue(t, pool, slug, ident, actor, "Reassigned issue")
	if err := AssignReleaseIssues(ctx, pool, slug, ident, actor, r1.ID, []string{iss2.ID}); err != nil {
		t.Fatalf("AssignReleaseIssues: %v", err)
	}
	target := r2.ID
	if err := DeleteRelease(ctx, pool, slug, ident, actor, r1.ID, &target, false); err != nil {
		t.Fatalf("reassign delete: %v", err)
	}
	var gotTarget string
	if err := pool.QueryRow(ctx,
		`SELECT release_id::text FROM issues WHERE id = $1::uuid`, iss2.ID).Scan(&gotTarget); err != nil {
		t.Fatalf("issue lookup: %v", err)
	}
	if gotTarget != r2.ID {
		t.Fatalf("release_id = %v, want %v", gotTarget, r2.ID)
	}

	// Reassign to self → 400.
	self := r2.ID
	if err := DeleteRelease(ctx, pool, slug, ident, actor, r2.ID, &self, false); !errors.Is(err, ErrInvalidRelease) {
		t.Fatalf("reassign self: err = %v, want ErrInvalidRelease", err)
	}

	// Reassign to unknown release → 404.
	ghost := "00000000-0000-0000-0000-000000000000"
	if err := DeleteRelease(ctx, pool, slug, ident, actor, r2.ID, &ghost, false); !errors.Is(err, ErrReleaseNotFound) {
		t.Fatalf("reassign ghost: err = %v, want ErrReleaseNotFound", err)
	}

	// Empty release deletes cleanly with no flags.
	r3, err := CreateRelease(ctx, pool, slug, ident, actor, ReleaseInput{Name: "v4.3"})
	if err != nil {
		t.Fatalf("CreateRelease: %v", err)
	}
	if err := DeleteRelease(ctx, pool, slug, ident, actor, r3.ID, nil, false); err != nil {
		t.Fatalf("delete empty: %v", err)
	}
	if _, err := GetRelease(ctx, pool, slug, ident, actor, r3.ID); !errors.Is(err, ErrReleaseNotFound) {
		t.Fatalf("after delete: err = %v, want ErrReleaseNotFound", err)
	}
}
