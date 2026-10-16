package service

// Project overview releases (C38T0): upcoming (planned) releases on
// the overview — shape, planned-only filter, release_date nulls-last
// then name ordering, the cap of 5, live snapshots, and the empty
// (never nil) list for projects without planned releases.

import (
	"context"
	"testing"
)

func TestOverviewReleases(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	actor := createTestUser(t, pool, uniqueTestEmail("overview-rel"))
	slug := uniqueTestSlug("overview-rel-ws")
	createTestWorkspace(t, pool, "Overview Co", slug, actor)
	ident := uniqueTestIdentifier()
	createTestProject(t, pool, slug, actor, "Eng", ident)

	// Seven planned releases: six dated, one undated. The cap of 5
	// keeps the five earliest dated ones; the undated one sorts last
	// and is cut off.
	// One released release (dated earliest) must be excluded.
	mk := func(name, status string, date *string) string {
		t.Helper()
		r, err := CreateRelease(ctx, pool, slug, ident, actor, ReleaseInput{
			Name: name, Status: status, ReleaseDate: date,
		})
		if err != nil {
			t.Fatalf("CreateRelease %s: %v", name, err)
		}
		return r.ID
	}
	_ = mk("Old", "released", strptr("2026-01-01"))
	alphaID := mk("Alpha", "planned", strptr("2027-01-01"))
	mk("Zeta", "planned", strptr("2027-03-01"))
	mk("P1", "planned", strptr("2027-04-01"))
	mk("P2", "planned", strptr("2027-05-01"))
	mk("P3", "planned", strptr("2027-06-01"))
	mk("Beta", "planned", nil)
	mk("Gamma", "planned", nil)

	// Alpha gets two issues: one completed, one in the default
	// (backlog-group) state.
	iss1 := createTestIssue(t, pool, slug, ident, actor, "alpha one")
	iss2 := createTestIssue(t, pool, slug, ident, actor, "alpha two")
	doneID := stateIDForGroup(t, pool, slug, ident, actor, "completed")
	if _, err := UpdateIssue(ctx, pool, slug, ident, iss1.ID, actor,
		IssuePatch{StateID: &doneID}); err != nil {
		t.Fatalf("complete issue: %v", err)
	}
	if err := AssignReleaseIssues(ctx, pool, slug, ident, actor, alphaID, []string{iss1.ID, iss2.ID}); err != nil {
		t.Fatalf("AssignReleaseIssues: %v", err)
	}

	ov, err := GetProjectOverview(ctx, pool, slug, ident, actor)
	if err != nil {
		t.Fatalf("GetProjectOverview: %v", err)
	}
	if ov.Releases == nil {
		t.Fatal("releases is nil, want an empty-or-filled slice, never nil")
	}
	if len(ov.Releases) != 5 {
		t.Fatalf("len(releases) = %d, want 5 (cap)", len(ov.Releases))
	}
	wantOrder := []string{"Alpha", "Zeta", "P1", "P2", "P3"}
	for i, want := range wantOrder {
		got := ov.Releases[i]
		if got.Name != want {
			t.Fatalf("releases[%d].name = %q, want %q (full: %+v)", i, got.Name, want, ov.Releases)
		}
		if got.Status != "planned" {
			t.Fatalf("releases[%d].status = %q, want planned", i, got.Status)
		}
	}
	// Alpha: date, count, and snapshot.
	alpha := ov.Releases[0]
	if alpha.ID != alphaID {
		t.Fatalf("alpha id = %s, want %s", alpha.ID, alphaID)
	}
	if alpha.ReleaseDate == nil || alpha.ReleaseDate.Format("2006-01-02") != "2027-01-01" {
		t.Fatalf("alpha release_date = %v, want 2027-01-01", alpha.ReleaseDate)
	}
	if alpha.IssueCount != 2 {
		t.Fatalf("alpha issue_count = %d, want 2", alpha.IssueCount)
	}
	if alpha.ProgressSnapshot["completed"] != 1 || alpha.ProgressSnapshot["backlog"] != 1 {
		t.Fatalf("alpha snapshot = %+v, want completed 1 / backlog 1", alpha.ProgressSnapshot)
	}
	for _, g := range snapshotGroups {
		if _, ok := alpha.ProgressSnapshot[g]; !ok {
			t.Fatalf("alpha snapshot missing group %q", g)
		}
	}
	// The rest have no issues: all-zero snapshots, every group present.
	for _, rel := range ov.Releases[1:] {
		if rel.IssueCount != 0 {
			t.Fatalf("%s issue_count = %d, want 0", rel.Name, rel.IssueCount)
		}
		for _, g := range snapshotGroups {
			v, ok := rel.ProgressSnapshot[g]
			if !ok || v != 0 {
				t.Fatalf("%s snapshot[%s] = %d, present=%v; want 0 present", rel.Name, g, v, ok)
			}
		}
	}
}

func TestOverviewReleasesEmpty(t *testing.T) {
	ctx := context.Background()
	pool := newTestPool(t)
	actor := createTestUser(t, pool, uniqueTestEmail("overview-rel-empty"))
	slug := uniqueTestSlug("overview-rel-empty-ws")
	createTestWorkspace(t, pool, "Overview Co", slug, actor)
	ident := uniqueTestIdentifier()
	createTestProject(t, pool, slug, actor, "Eng", ident)

	// A released-only project has no upcoming releases.
	if _, err := CreateRelease(ctx, pool, slug, ident, actor, ReleaseInput{
		Name: "Shipped", Status: "released",
	}); err != nil {
		t.Fatalf("CreateRelease: %v", err)
	}
	ov, err := GetProjectOverview(ctx, pool, slug, ident, actor)
	if err != nil {
		t.Fatalf("GetProjectOverview: %v", err)
	}
	if ov.Releases == nil {
		t.Fatal("releases is nil, want an empty slice, never nil")
	}
	if len(ov.Releases) != 0 {
		t.Fatalf("len(releases) = %d, want 0 (released-only project)", len(ov.Releases))
	}
}
