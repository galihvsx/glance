package service

// Plane import analysis tests (C14T2): read-only unresolved inventory.
// Real test database, no skips. AnalyzePlaneImport must never write.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// planeAnalyzeSetup mirrors importTestSetup and additionally returns the
// project's uuid for direct seed inserts. The project lookup is scoped by
// workspace slug: project identifiers are truncated to 12 chars by
// importTestSetup, so two tests in one full-suite run can share an
// identifier in different workspaces (e.g. "imp-p-<pid>-1000" vs
// "imp-p-<pid>-10000" truncate identically) — a bare identifier lookup
// would return an arbitrary test's project.
func planeAnalyzeSetup(t *testing.T) (*pgxpool.Pool, string, string, string, string) {
	t.Helper()
	pool, slug, ident, admin := importTestSetup(t)
	var projectID string
	if err := pool.QueryRow(context.Background(),
		`SELECT p.id::text FROM projects p
		  JOIN workspaces w ON w.id = p.workspace_id
		 WHERE w.slug = $1 AND p.identifier = $2`, slug, ident).Scan(&projectID); err != nil {
		t.Fatalf("lookup project id: %v", err)
	}
	return pool, slug, ident, admin, projectID
}

func loadCanonicalFixture(t *testing.T) []PlaneIssueRow {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "plane-export.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	parsed, err := ParsePlaneImport(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	return parsed.Rows
}

// seedPlaneAnalysisTargets creates the label/cycle/module/estimate-scale
// rows the canonical fixture references, so the happy path resolves.
func seedPlaneAnalysisTargets(t *testing.T, pool *pgxpool.Pool, slug, ident, admin, projectID string) {
	t.Helper()
	ctx := context.Background()
	createTestLabel(t, pool, slug, ident, admin, "bug")
	if _, err := pool.Exec(ctx,
		`INSERT INTO cycles (project_id, name, start_date, end_date)
		 VALUES ($1::uuid, 'Sprint 12', '2026-01-01', '2026-01-14')`, projectID); err != nil {
		t.Fatalf("insert cycle: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO modules (project_id, name) VALUES ($1::uuid, 'Mobile')`, projectID); err != nil {
		t.Fatalf("insert module: %v", err)
	}
	_, err := CreateEstimate(ctx, pool, slug, ident, admin, EstimateInput{
		Name: "Fibonacci",
		Points: []EstimatePointInput{
			{Key: "1", Value: 1}, {Key: "2", Value: 2}, {Key: "3", Value: 3},
			{Key: "5", Value: 5}, {Key: "8", Value: 8},
		},
	})
	if err != nil {
		t.Fatalf("create estimate scale: %v", err)
	}
}

// addWorkspaceMember inserts a user with a display name as a workspace
// member at the given role.
func addWorkspaceMember(t *testing.T, pool *pgxpool.Pool, slug, email, displayName string, role int) string {
	t.Helper()
	ctx := context.Background()
	userID := createTestUser(t, pool, email)
	if displayName != "" {
		if _, err := pool.Exec(ctx, `UPDATE users SET name = $2 WHERE id = $1::uuid`, userID, displayName); err != nil {
			t.Fatalf("set user name: %v", err)
		}
	}
	var wsID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM workspaces WHERE slug = $1`, slug).Scan(&wsID); err != nil {
		t.Fatalf("lookup workspace: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO workspace_members (workspace_id, user_id, role) VALUES ($1::uuid, $2::uuid, $3)`,
		wsID, userID, role); err != nil {
		t.Fatalf("insert member: %v", err)
	}
	return userID
}

func containsStr(hay []string, needle string) bool {
	for _, s := range hay {
		if s == needle {
			return true
		}
	}
	return false
}

func containsSub(hay []string, sub string) bool {
	for _, s := range hay {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func TestAnalyzePlaneImportHappyPath(t *testing.T) {
	pool, slug, ident, admin, projectID := planeAnalyzeSetup(t)
	seedPlaneAnalysisTargets(t, pool, slug, ident, admin, projectID)
	rows := loadCanonicalFixture(t)

	a, err := AnalyzePlaneImport(context.Background(), pool, slug, ident, admin, rows)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if a.ProjectIdentifier != "ACME" {
		t.Fatalf("project_identifier = %q, want ACME", a.ProjectIdentifier)
	}
	if a.IssueCount != 2 {
		t.Fatalf("issue_count = %d, want 2", a.IssueCount)
	}
	// Everything the fixture references resolves (states are seeded by
	// createTestProject; label/cycle/module/estimate seeded above).
	if len(a.Unresolved.States) != 0 || len(a.Unresolved.Labels) != 0 ||
		len(a.Unresolved.Cycles) != 0 || len(a.Unresolved.Modules) != 0 {
		t.Fatalf("unresolved = %+v, want all empty", a.Unresolved)
	}
	// People are never auto-resolved, even when nothing matches.
	for _, want := range []string{"Ada Lovelace", "Alan Turing", "Grace Hopper"} {
		if !containsStr(a.Unresolved.People, want) {
			t.Fatalf("people = %v, missing %q", a.Unresolved.People, want)
		}
	}
	if len(a.Unresolved.People) != 3 {
		t.Fatalf("people = %v, want exactly 3", a.Unresolved.People)
	}
	// Honest-loss warnings.
	if !containsSub(a.Warnings, "descriptions are never present in Plane exports") {
		t.Fatalf("warnings = %v, missing descriptions warning", a.Warnings)
	}
	if !containsSub(a.Warnings, "2 issue relation") || !containsSub(a.Warnings, "dropped") {
		t.Fatalf("warnings = %v, missing relations warning", a.Warnings)
	}
	if !containsSub(a.Warnings, "attachment") {
		t.Fatalf("warnings = %v, missing attachments warning", a.Warnings)
	}
	// No false alarms: no duplicates, no estimate mismatches here.
	if containsSub(a.Warnings, "duplicate") || containsSub(a.Warnings, "estimate") {
		t.Fatalf("warnings = %v, want no duplicate/estimate warnings", a.Warnings)
	}
}

func TestAnalyzePlaneImportUnresolvedInventory(t *testing.T) {
	pool, slug, ident, admin, projectID := planeAnalyzeSetup(t)
	seedPlaneAnalysisTargets(t, pool, slug, ident, admin, projectID)
	rows := loadCanonicalFixture(t)
	// Add a row with unknown references and differently-cased matches.
	rows = append(rows, PlaneIssueRow{
		Identifier:        "ACME-44",
		Name:              "Mystery issue",
		ProjectIdentifier: "ACME",
		StateName:         "In Review",                 // not seeded -> unresolved
		Labels:            []string{"frontend", "BUG"}, // frontend unknown, BUG resolves ci
		Cycles:            []string{"Sprint 99"},       // unknown
		Modules:           []string{"MOBILE"},          // resolves ci
	})

	a, err := AnalyzePlaneImport(context.Background(), pool, slug, ident, admin, rows)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if !containsStr(a.Unresolved.States, "In Review") {
		t.Fatalf("states = %v, missing In Review", a.Unresolved.States)
	}
	if !containsStr(a.Unresolved.Labels, "frontend") {
		t.Fatalf("labels = %v, missing frontend", a.Unresolved.Labels)
	}
	if containsStr(a.Unresolved.Labels, "BUG") || containsStr(a.Unresolved.Labels, "bug") {
		t.Fatalf("labels = %v, BUG should resolve case-insensitively", a.Unresolved.Labels)
	}
	if !containsStr(a.Unresolved.Cycles, "Sprint 99") {
		t.Fatalf("cycles = %v, missing Sprint 99", a.Unresolved.Cycles)
	}
	if len(a.Unresolved.Modules) != 0 {
		t.Fatalf("modules = %v, MOBILE should resolve case-insensitively", a.Unresolved.Modules)
	}
}

func TestAnalyzePlaneImportPeopleNeverAutoResolved(t *testing.T) {
	pool, slug, ident, admin, _ := planeAnalyzeSetup(t)
	ctx := context.Background()
	// A workspace member whose display name EXACTLY matches a Plane name
	// must still surface as unresolved: Plane exports names only.
	addWorkspaceMember(t, pool, slug, uniqueTestEmail("pa-ada"), "Ada Lovelace", RoleMember)
	// Two members sharing one display name -> ambiguous warning.
	addWorkspaceMember(t, pool, slug, uniqueTestEmail("pa-sam1"), "Sam Carter", RoleMember)
	addWorkspaceMember(t, pool, slug, uniqueTestEmail("pa-sam2"), "Sam Carter", RoleMember)
	rows := []PlaneIssueRow{
		{
			Identifier:        "ACME-50",
			Name:              "People row",
			ProjectIdentifier: "ACME",
			Assignees:         []string{"Ada Lovelace", "Sam Carter"},
			CreatedByName:     "Alan Turing",
			Subscribers:       []string{"Grace Hopper"},
			Comments: []PlaneIssueComment{
				{Comment: "hi", CreatedAt: "2026-10-05 14:22:10", CreatedBy: "Ada Lovelace"},
			},
		},
	}

	a, err := AnalyzePlaneImport(ctx, pool, slug, ident, admin, rows)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	for _, want := range []string{"Ada Lovelace", "Sam Carter", "Alan Turing", "Grace Hopper"} {
		if !containsStr(a.Unresolved.People, want) {
			t.Fatalf("people = %v, missing %q", a.Unresolved.People, want)
		}
	}
	if !containsSub(a.Warnings, "Sam Carter") || !containsSub(a.Warnings, "ambiguous") {
		t.Fatalf("warnings = %v, missing ambiguity warning for Sam Carter", a.Warnings)
	}
	if containsSub(a.Warnings, "Ada Lovelace") && containsSub(a.Warnings, "ambiguous") {
		t.Fatalf("warnings = %v, Ada Lovelace is unique and must not be flagged ambiguous", a.Warnings)
	}
}

func TestAnalyzePlaneImportEstimateMismatch(t *testing.T) {
	pool, slug, ident, admin, projectID := planeAnalyzeSetup(t)
	seedPlaneAnalysisTargets(t, pool, slug, ident, admin, projectID)
	rows := []PlaneIssueRow{
		{Identifier: "ACME-60", Name: "a", ProjectIdentifier: "ACME",
			Estimate: json.RawMessage(`3`)}, // matches the Fibonacci scale
		{Identifier: "ACME-61", Name: "b", ProjectIdentifier: "ACME",
			Estimate: json.RawMessage(`99`)}, // no matching point
		{Identifier: "ACME-62", Name: "c", ProjectIdentifier: "ACME",
			Estimate: json.RawMessage(`""`)}, // unset — never a warning
	}

	a, err := AnalyzePlaneImport(context.Background(), pool, slug, ident, admin, rows)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if !containsSub(a.Warnings, "99") {
		t.Fatalf("warnings = %v, missing mismatch warning for estimate 99", a.Warnings)
	}
	if containsSub(a.Warnings, `"3"`) || containsSub(a.Warnings, "estimate value 3 ") {
		t.Fatalf("warnings = %v, estimate 3 matches and must not warn", a.Warnings)
	}
}

func TestAnalyzePlaneImportNoEstimateScales(t *testing.T) {
	pool, slug, ident, admin, _ := planeAnalyzeSetup(t)
	// No estimate scales on this project at all.
	rows := []PlaneIssueRow{
		{Identifier: "ACME-70", Name: "a", ProjectIdentifier: "ACME",
			Estimate: json.RawMessage(`3`)},
	}

	a, err := AnalyzePlaneImport(context.Background(), pool, slug, ident, admin, rows)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if !containsSub(a.Warnings, "no estimate scales") {
		t.Fatalf("warnings = %v, missing no-scales warning", a.Warnings)
	}
}

func TestAnalyzePlaneImportDuplicateIdentifiers(t *testing.T) {
	pool, slug, ident, admin, _ := planeAnalyzeSetup(t)
	rows := []PlaneIssueRow{
		{Identifier: "ACME-80", Name: "a", ProjectIdentifier: "ACME"},
		{Identifier: "ACME-80", Name: "b", ProjectIdentifier: "ACME"},
		{Identifier: "ACME-81", Name: "c", ProjectIdentifier: "ACME"},
	}

	a, err := AnalyzePlaneImport(context.Background(), pool, slug, ident, admin, rows)
	if err != nil {
		t.Fatalf("analyze: duplicates must be tolerated, got %v", err)
	}
	if a.IssueCount != 3 {
		t.Fatalf("issue_count = %d, want 3", a.IssueCount)
	}
	if !containsSub(a.Warnings, "ACME-80") || !containsSub(a.Warnings, "duplicate") {
		t.Fatalf("warnings = %v, missing duplicate warning for ACME-80", a.Warnings)
	}
}

func TestAnalyzePlaneImportEmpty(t *testing.T) {
	pool, slug, ident, admin, _ := planeAnalyzeSetup(t)
	if _, err := AnalyzePlaneImport(context.Background(), pool, slug, ident, admin, nil); err != ErrPlaneImportEmpty {
		t.Fatalf("err = %v, want ErrPlaneImportEmpty", err)
	}
}

func TestAnalyzePlaneImportAuth(t *testing.T) {
	pool, slug, ident, admin, _ := planeAnalyzeSetup(t)
	ctx := context.Background()
	rows := loadCanonicalFixture(t)

	// Guest (role 5): analysis needs member (15)+ like the CSV importer.
	guest := addWorkspaceMember(t, pool, slug, uniqueTestEmail("pa-guest"), "Guest User", RoleGuest)
	if _, err := AnalyzePlaneImport(ctx, pool, slug, ident, guest, rows); err != ErrForbidden {
		t.Fatalf("guest: err = %v, want ErrForbidden", err)
	}
	// Non-member: project not visible.
	outsider := createTestUser(t, pool, uniqueTestEmail("pa-out"))
	if _, err := AnalyzePlaneImport(ctx, pool, slug, ident, outsider, rows); err == nil {
		t.Fatalf("outsider: expected an error, got nil")
	}
	// Unknown project identifier.
	if _, err := AnalyzePlaneImport(ctx, pool, slug, "NOPE", admin, rows); err == nil {
		t.Fatalf("bad project: expected an error, got nil")
	}
}

func TestAnalyzePlaneImportNoWrites(t *testing.T) {
	pool, slug, ident, admin, projectID := planeAnalyzeSetup(t)
	seedPlaneAnalysisTargets(t, pool, slug, ident, admin, projectID)
	rows := loadCanonicalFixture(t)
	ctx := context.Background()
	count := func(q string, args ...any) int {
		var n int
		if err := pool.QueryRow(ctx, q, args...).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		return n
	}
	before := map[string]int{
		"issues":  count(`SELECT COUNT(*) FROM issues`),
		"states":  count(`SELECT COUNT(*) FROM states`),
		"labels":  count(`SELECT COUNT(*) FROM labels`),
		"cycles":  count(`SELECT COUNT(*) FROM cycles`),
		"modules": count(`SELECT COUNT(*) FROM modules`),
	}
	if _, err := AnalyzePlaneImport(ctx, pool, slug, ident, admin, rows); err != nil {
		t.Fatalf("analyze: %v", err)
	}
	for table, n := range before {
		if got := count(`SELECT COUNT(*) FROM ` + table); got != n {
			t.Fatalf("table %s changed: %d -> %d (analysis must not write)", table, n, got)
		}
	}
}
