package service

// CSV importer tests (C4T8): parser edge cases, preview-then-import
// round-trip, per-row errors. Real test database, no skips.

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func importTestSetup(t *testing.T) (*pgxpool.Pool, string, string, string) {
	t.Helper()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	_ = ctx

	admin := createTestUser(t, pool, uniqueTestEmail("imp-admin"))
	slug := uniqueTestSlug("imp-ws")
	createTestWorkspace(t, pool, "Import Co", slug, admin)
	// Project identifiers: 1-12 uppercase alnum. Build from the unique
	// slug, stripping hyphens.
	ident := strings.ToUpper(strings.ReplaceAll(uniqueTestSlug("imp-p"), "-", ""))
	if len(ident) > 12 {
		ident = ident[:12]
	}
	createTestProject(t, pool, slug, admin, "ImportProj", ident)
	return pool, slug, ident, admin
}

func importMapping() ImportMapping {
	return ImportMapping{
		Title:         "title",
		Description:   "desc",
		State:         "state",
		Priority:      "priority",
		Labels:        "labels",
		AssigneeEmail: "assignee",
		StartDate:     "start",
		TargetDate:    "due",
	}
}

func TestParseImportCSVEdgeCases(t *testing.T) {
	// BOM + CRLF + quoted commas + blank lines.
	raw := "\ufefftitle,desc\r\n\"a, b\",\"x\"\"y\"\r\n\r\nplain,\r\n"
	header, records, err := parseImportCSV(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(header) != 2 || header[0] != "title" {
		t.Fatalf("header = %v, want [title desc] without BOM", header)
	}
	// Blank line is skipped by encoding/csv.
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2 (blank line skipped)", len(records))
	}
	if records[0][0] != "a, b" || records[0][1] != `x"y` {
		t.Fatalf("quoted = %v", records[0])
	}

	// Empty input.
	if _, _, err := parseImportCSV(strings.NewReader("")); err != ErrImportEmpty {
		t.Fatalf("empty: err = %v, want ErrImportEmpty", err)
	}
}

func TestPreviewImportNoWrites(t *testing.T) {
	pool, slug, ident, admin := importTestSetup(t)
	ctx := context.Background()

	csvData := "title,desc,state\nGood one,hello,Backlog\n,bad row\n"
	prev, err := PreviewImport(ctx, pool, slug, ident, admin, strings.NewReader(csvData), ImportMapping{Title: "title", Description: "desc", State: "state"})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if len(prev.Rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(prev.Rows))
	}
	if len(prev.Errors) != 1 || prev.Errors[0].Row != 3 {
		t.Fatalf("errors = %+v, want one at row 3", prev.Errors)
	}
	// No writes: the issues table must be empty for this project.
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM issues i JOIN projects p ON p.id = i.project_id
		 WHERE p.identifier = $1`, ident).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("preview wrote %d issues, want 0", n)
	}
}

func TestImportIssuesRoundTrip(t *testing.T) {
	pool, slug, ident, admin := importTestSetup(t)
	ctx := context.Background()

	// Labels must exist — import never creates them.
	for _, ln := range []string{"bug", "ui"} {
		if _, err := CreateLabel(ctx, pool, slug, ident, admin, LabelInput{Name: ln}); err != nil {
			t.Fatalf("CreateLabel %s: %v", ln, err)
		}
	}

	csvData := "title,desc,priority,labels,start,due,assignee\n" +
		"First,hello world,high,\"bug, ui\",2026-01-05,2026-01-12,\n" +
		"Second,,2,,,\n"
	m := ImportMapping{Title: "title", Description: "desc", Priority: "priority", Labels: "labels", StartDate: "start", TargetDate: "due", AssigneeEmail: "assignee"}
	res, err := ImportIssues(ctx, pool, slug, ident, admin, strings.NewReader(csvData), m)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.Created != 2 || res.Failed != 0 {
		t.Fatalf("result = %+v, want created=2 failed=0", res)
	}
	// Spot-check the first issue: state resolved, labels attached.
	var seqID int
	var stateName string
	if err := pool.QueryRow(ctx,
		`SELECT i.sequence_id, s.name FROM issues i
		 JOIN projects p ON p.id = i.project_id
		 JOIN states s ON s.id = i.state_id
		 WHERE p.identifier = $1 AND i.name = 'First'`, ident).Scan(&seqID, &stateName); err != nil {
		t.Fatalf("lookup: %v", err)
	}
	var labelCount int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM issue_labels il
		 JOIN issues i ON i.id = il.issue_id
		 JOIN projects p ON p.id = i.project_id
		 WHERE p.identifier = $1 AND i.name = 'First'`, ident).Scan(&labelCount); err != nil {
		t.Fatalf("labels: %v", err)
	}
	if labelCount != 2 {
		t.Fatalf("label count = %d, want 2", labelCount)
	}
}

func TestImportIssuesPerRowErrors(t *testing.T) {
	pool, slug, ident, admin := importTestSetup(t)
	ctx := context.Background()

	// Row 2 ok, row 3 missing title, row 4 bad priority, row 5 bad date.
	// One bad row must not kill the batch.
	csvData := "title,priority,due\n" +
		"Good,low,2026-01-10\n" +
		",low,\n" +
		"Bad pri,bogus,\n" +
		"Bad date,low,not-a-date\n"
	res, err := ImportIssues(ctx, pool, slug, ident, admin,
		strings.NewReader(csvData),
		ImportMapping{Title: "title", Priority: "priority", TargetDate: "due"})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.Created != 1 {
		t.Fatalf("created = %d, want 1", res.Created)
	}
	if res.Failed != 3 {
		t.Fatalf("failed = %d, want 3", res.Failed)
	}
	if len(res.Errors) != 3 {
		t.Fatalf("errors = %+v, want 3 entries", res.Errors)
	}
	// Row numbers are 1-based with header = 1.
	wantRows := []int{3, 4, 5}
	for i, e := range res.Errors {
		if e.Row != wantRows[i] {
			t.Fatalf("errors[%d].Row = %d, want %d", i, e.Row, wantRows[i])
		}
	}
	// The good row really landed.
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM issues i JOIN projects p ON p.id = i.project_id
		 WHERE p.identifier = $1 AND i.name = 'Good'`, ident).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("good row count = %d, want 1", n)
	}
}

func TestImportMappingValidation(t *testing.T) {
	pool, slug, ident, admin := importTestSetup(t)
	ctx := context.Background()

	// Missing title column → whole request fails (400-class), not per-row.
	_, err := ImportIssues(ctx, pool, slug, ident, admin,
		strings.NewReader("name\nfoo\n"),
		ImportMapping{Title: "title"})
	if err == nil {
		t.Fatalf("expected ErrImportMapping, got nil")
	}
	// Unknown mapped column → same.
	_, err = ImportIssues(ctx, pool, slug, ident, admin,
		strings.NewReader("title\nfoo\n"),
		ImportMapping{Title: "title", State: "nope"})
	if err == nil {
		t.Fatalf("expected ErrImportMapping for unknown column, got nil")
	}
}
