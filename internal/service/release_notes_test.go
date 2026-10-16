package service

// Release notes (C33T1): GET .../releases/{id}/notes — point-in-time
// Markdown grouped by state group, plus grouping counts. Read-only.

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestReleaseNotesGrouping(t *testing.T) {
	ctx, pool, slug, ident, actor := setupReleaseTest(t)

	desc := "Ships things."
	r, err := CreateRelease(ctx, pool, slug, ident, actor, ReleaseInput{
		Name:        "v9.0",
		Description: &desc,
		Status:      "planned",
		ReleaseDate: strptr("2026-12-01"),
	})
	if err != nil {
		t.Fatalf("CreateRelease: %v", err)
	}

	// One issue per bucket, created in section order: completed,
	// started, then the five Backlog groups (unstarted, cancelled,
	// triage, backlog) — plus the triage state is created explicitly
	// (not in the default seed).
	triage, err := CreateState(ctx, pool, slug, ident, actor, StateInput{
		Name: "Triage", Group: "triage", Color: "#999",
	})
	if err != nil {
		t.Fatalf("CreateState triage: %v", err)
	}
	var projectID string
	if err := pool.QueryRow(ctx,
		`SELECT p.id::text FROM projects p
		 JOIN workspaces w ON w.id = p.workspace_id
		 WHERE w.slug = $1 AND p.identifier = $2`, slug, ident).Scan(&projectID); err != nil {
		t.Fatalf("project id: %v", err)
	}
	move := func(issueID, group string) {
		t.Helper()
		stateID := stateIDByGroup(t, pool, projectID, group)
		if _, err := UpdateIssue(ctx, pool, slug, ident, issueID, actor,
			IssuePatch{StateID: &stateID}); err != nil {
			t.Fatalf("UpdateIssue to %s: %v", group, err)
		}
	}

	specs := []struct {
		name  string
		group string // "" = move via explicit state id (triage)
	}{
		{"Done thing", "completed"},
		{"Doing thing", "started"},
		{"Todo thing", "unstarted"},
		{"Cancelled thing", "cancelled"},
		{"Triaged thing", ""},
		{"Backlog thing", "backlog"},
	}
	ids := make([]string, 0, len(specs))
	for _, s := range specs {
		iss := createTestIssue(t, pool, slug, ident, actor, s.name)
		ids = append(ids, iss.ID)
		if s.group != "" {
			move(iss.ID, s.group)
		} else {
			if _, err := UpdateIssue(ctx, pool, slug, ident, iss.ID, actor,
				IssuePatch{StateID: &triage.ID}); err != nil {
				t.Fatalf("UpdateIssue to triage: %v", err)
			}
		}
	}
	if err := AssignReleaseIssues(ctx, pool, slug, ident, actor, r.ID, ids); err != nil {
		t.Fatalf("AssignReleaseIssues: %v", err)
	}

	notes, err := GetReleaseNotes(ctx, pool, slug, ident, actor, r.ID)
	if err != nil {
		t.Fatalf("GetReleaseNotes: %v", err)
	}
	if notes.Release.ID != r.ID || notes.Release.Name != "v9.0" ||
		notes.Release.Status != "planned" || notes.Release.Description != desc {
		t.Fatalf("release = %+v", notes.Release)
	}
	if notes.Release.ReleaseDate == nil || *notes.Release.ReleaseDate != "2026-12-01" {
		t.Fatalf("release_date = %v, want 2026-12-01", notes.Release.ReleaseDate)
	}
	wantCounts := ReleaseNotesCounts{Completed: 1, Started: 1, Unstarted: 4, Total: 6}
	if notes.Counts != wantCounts {
		t.Fatalf("counts = %+v, want %+v", notes.Counts, wantCounts)
	}

	// Exact Markdown shape: display ids are <IDENT>-<sequence>; the six
	// issues were created first in this project, so sequences are 1..6
	// in creation order.
	var want strings.Builder
	want.WriteString("# Release notes — v9.0\n\n")
	want.WriteString("Release date: 2026-12-01\n\n")
	want.WriteString("Ships things.\n")
	want.WriteString("\n## Completed (1)\n")
	want.WriteString("- " + ident + "-1 Done thing\n")
	want.WriteString("\n## In progress (1)\n")
	want.WriteString("- " + ident + "-2 Doing thing\n")
	want.WriteString("\n## Backlog (4)\n")
	want.WriteString("- " + ident + "-3 Todo thing\n")
	want.WriteString("- " + ident + "-4 Cancelled thing\n")
	want.WriteString("- " + ident + "-5 Triaged thing\n")
	want.WriteString("- " + ident + "-6 Backlog thing\n")
	if notes.Markdown != want.String() {
		t.Fatalf("markdown = %q\nwant %q", notes.Markdown, want.String())
	}
}

func TestReleaseNotesEmpty(t *testing.T) {
	ctx, pool, slug, ident, actor := setupReleaseTest(t)

	// No date, no description, no issues.
	r, err := CreateRelease(ctx, pool, slug, ident, actor, ReleaseInput{Name: "vEmpty"})
	if err != nil {
		t.Fatalf("CreateRelease: %v", err)
	}
	notes, err := GetReleaseNotes(ctx, pool, slug, ident, actor, r.ID)
	if err != nil {
		t.Fatalf("GetReleaseNotes: %v", err)
	}
	if notes.Counts != (ReleaseNotesCounts{}) {
		t.Fatalf("counts = %+v, want zeros", notes.Counts)
	}
	if notes.Release.ReleaseDate != nil {
		t.Fatalf("release_date = %v, want nil", notes.Release.ReleaseDate)
	}
	if notes.Release.Description != "" {
		t.Fatalf("description = %q, want empty", notes.Release.Description)
	}
	want := "# Release notes — vEmpty\n\nRelease date: —\n\n_No issues in this release._\n"
	if notes.Markdown != want {
		t.Fatalf("markdown = %q, want %q", notes.Markdown, want)
	}
}

func TestReleaseNotesDescriptionAbsent(t *testing.T) {
	ctx, pool, slug, ident, actor := setupReleaseTest(t)

	// A dated release with one backlog issue but no description: the
	// description block is omitted, the Backlog section renders.
	r, err := CreateRelease(ctx, pool, slug, ident, actor, ReleaseInput{
		Name: "vNoDesc", ReleaseDate: strptr("2027-01-15"),
	})
	if err != nil {
		t.Fatalf("CreateRelease: %v", err)
	}
	iss := createTestIssue(t, pool, slug, ident, actor, "Only thing")
	if err := AssignReleaseIssues(ctx, pool, slug, ident, actor, r.ID, []string{iss.ID}); err != nil {
		t.Fatalf("AssignReleaseIssues: %v", err)
	}
	notes, err := GetReleaseNotes(ctx, pool, slug, ident, actor, r.ID)
	if err != nil {
		t.Fatalf("GetReleaseNotes: %v", err)
	}
	want := "# Release notes — vNoDesc\n\nRelease date: 2027-01-15\n\n## Backlog (1)\n- " + ident + "-1 Only thing\n"
	if notes.Markdown != want {
		t.Fatalf("markdown = %q, want %q", notes.Markdown, want)
	}
}

func TestReleaseNotesNoWrite(t *testing.T) {
	ctx, pool, slug, ident, actor := setupReleaseTest(t)

	r, err := CreateRelease(ctx, pool, slug, ident, actor, ReleaseInput{
		Name: "vStable", ReleaseDate: strptr("2026-12-01"),
	})
	if err != nil {
		t.Fatalf("CreateRelease: %v", err)
	}
	iss := createTestIssue(t, pool, slug, ident, actor, "Stable thing")
	if err := AssignReleaseIssues(ctx, pool, slug, ident, actor, r.ID, []string{iss.ID}); err != nil {
		t.Fatalf("AssignReleaseIssues: %v", err)
	}

	snapshot := func() (releases, issues int, relUpdated, issUpdated time.Time) {
		t.Helper()
		if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM releases`).Scan(&releases); err != nil {
			t.Fatalf("count releases: %v", err)
		}
		if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM issues`).Scan(&issues); err != nil {
			t.Fatalf("count issues: %v", err)
		}
		if err := pool.QueryRow(ctx,
			`SELECT updated_at FROM releases WHERE id = $1::uuid`, r.ID).Scan(&relUpdated); err != nil {
			t.Fatalf("release updated_at: %v", err)
		}
		if err := pool.QueryRow(ctx,
			`SELECT updated_at FROM issues WHERE id = $1::uuid`, iss.ID).Scan(&issUpdated); err != nil {
			t.Fatalf("issue updated_at: %v", err)
		}
		return releases, issues, relUpdated, issUpdated
	}
	rBefore, iBefore, relBefore, issBefore := snapshot()
	if _, err := GetReleaseNotes(ctx, pool, slug, ident, actor, r.ID); err != nil {
		t.Fatalf("GetReleaseNotes: %v", err)
	}
	rAfter, iAfter, relAfter, issAfter := snapshot()
	if rBefore != rAfter || iBefore != iAfter {
		t.Fatalf("row counts changed: releases %d→%d, issues %d→%d",
			rBefore, rAfter, iBefore, iAfter)
	}
	if !relBefore.Equal(relAfter) || !issBefore.Equal(issAfter) {
		t.Fatalf("updated_at moved: release %v→%v, issue %v→%v",
			relBefore, relAfter, issBefore, issAfter)
	}
}

func TestReleaseNotesTenancy(t *testing.T) {
	ctx, pool, slug, ident, actor := setupReleaseTest(t)

	r, err := CreateRelease(ctx, pool, slug, ident, actor, ReleaseInput{Name: "vTen"})
	if err != nil {
		t.Fatalf("CreateRelease: %v", err)
	}

	// Outsider (no membership) → 404, same as the sibling read gate.
	outsider := createTestUser(t, pool, uniqueTestEmail("notes-out"))
	if _, err := GetReleaseNotes(ctx, pool, slug, ident, outsider, r.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("outsider: err = %v, want ErrNotFound", err)
	}

	// Guest may read.
	guest := createTestUser(t, pool, uniqueTestEmail("notes-guest"))
	if err := UpsertMember(ctx, pool, slug, actor, guest, RoleGuest); err != nil {
		t.Fatalf("UpsertMember: %v", err)
	}
	if _, err := GetReleaseNotes(ctx, pool, slug, ident, guest, r.ID); err != nil {
		t.Fatalf("guest read: %v", err)
	}

	// Cross-project release id → 404 (ErrReleaseNotFound).
	ident2 := uniqueTestIdentifier()
	createTestProject(t, pool, slug, actor, "Eng2", ident2)
	other, err := CreateRelease(ctx, pool, slug, ident2, actor, ReleaseInput{Name: "other"})
	if err != nil {
		t.Fatalf("CreateRelease other: %v", err)
	}
	if _, err := GetReleaseNotes(ctx, pool, slug, ident, actor, other.ID); !errors.Is(err, ErrReleaseNotFound) {
		t.Fatalf("cross-project: err = %v, want ErrReleaseNotFound", err)
	}

	// Cross-workspace (unknown slug) → 404.
	if _, err := GetReleaseNotes(ctx, pool, "no-such-ws", ident, actor, r.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-workspace: err = %v, want ErrNotFound", err)
	}

	// Unknown release → 404; malformed id → 400.
	if _, err := GetReleaseNotes(ctx, pool, slug, ident, actor, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, ErrReleaseNotFound) {
		t.Fatalf("unknown: err = %v, want ErrReleaseNotFound", err)
	}
	if _, err := GetReleaseNotes(ctx, pool, slug, ident, actor, "nope"); !errors.Is(err, ErrInvalidReleaseID) {
		t.Fatalf("malformed: err = %v, want ErrInvalidReleaseID", err)
	}
}

func TestBucketReleaseNoteRows(t *testing.T) {
	completed, started, backlog := bucketReleaseNoteRows("ENG", []releaseNoteRow{
		{SequenceID: 1, Title: "Done", Group: "completed"},
		{SequenceID: 2, Title: "Doing", Group: "started"},
		{SequenceID: 3, Title: "Todo", Group: "unstarted"},
		{SequenceID: 4, Title: "Cancelled", Group: "cancelled"},
		{SequenceID: 5, Title: "Triaged", Group: "triage"},
		{SequenceID: 6, Title: "Backlogged", Group: "backlog"},
		// Unknown / missing groups land in Backlog — the states CHECK
		// constraint + state FK make these unreachable from the live
		// DB, but the bucketing must stay total regardless.
		{SequenceID: 7, Title: "Mystery", Group: "mystery"},
		{SequenceID: 8, Title: "Orphan", Group: ""},
	})
	if len(completed) != 1 || completed[0].DisplayID != "ENG-1" || completed[0].Title != "Done" {
		t.Fatalf("completed = %+v", completed)
	}
	if len(started) != 1 || started[0].DisplayID != "ENG-2" {
		t.Fatalf("started = %+v", started)
	}
	if len(backlog) != 6 {
		t.Fatalf("backlog len = %d, want 6", len(backlog))
	}
	wantIDs := []string{"ENG-3", "ENG-4", "ENG-5", "ENG-6", "ENG-7", "ENG-8"}
	for i, want := range wantIDs {
		if backlog[i].DisplayID != want {
			t.Fatalf("backlog[%d] = %q, want %q", i, backlog[i].DisplayID, want)
		}
	}
}

func TestBuildReleaseNotesMarkdown(t *testing.T) {
	date := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		date    *time.Time
		desc    string
		comp    []releaseNoteItem
		started []releaseNoteItem
		backlog []releaseNoteItem
		want    string
	}{
		{
			name:    "full",
			date:    &date,
			desc:    "Ships things.",
			comp:    []releaseNoteItem{{"ENG-1", "Done"}},
			started: []releaseNoteItem{{"ENG-2", "Doing"}},
			backlog: []releaseNoteItem{{"ENG-3", "Later"}},
			want: "# Release notes — v1.0\n\nRelease date: 2026-12-01\n\nShips things.\n\n" +
				"## Completed (1)\n- ENG-1 Done\n\n" +
				"## In progress (1)\n- ENG-2 Doing\n\n" +
				"## Backlog (1)\n- ENG-3 Later\n",
		},
		{
			name:    "no date, no description, backlog only",
			backlog: []releaseNoteItem{{"ENG-9", "Only"}},
			want: "# Release notes — v1.0\n\nRelease date: —\n\n" +
				"## Backlog (1)\n- ENG-9 Only\n",
		},
		{
			name: "empty with description",
			desc: "Nothing shipped.",
			want: "# Release notes — v1.0\n\nRelease date: —\n\nNothing shipped.\n\n_No issues in this release._\n",
		},
		{
			name: "empty without description",
			want: "# Release notes — v1.0\n\nRelease date: —\n\n_No issues in this release._\n",
		},
		{
			name: "whitespace-only description omitted",
			desc: "   \n  ",
			want: "# Release notes — v1.0\n\nRelease date: —\n\n_No issues in this release._\n",
		},
	}
	for _, tc := range cases {
		got := buildReleaseNotesMarkdown("v1.0", tc.date, tc.desc, tc.comp, tc.started, tc.backlog)
		if got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}
