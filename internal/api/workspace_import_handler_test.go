package api

// Workspace archive import tests (C11T0):
//   POST /api/v1/workspaces/{slug}/import
//
// Admin-only restore of a `glance-export/1` archive produced by
// GET /api/v1/workspaces/{slug}/export. The tests seed a source workspace,
// export it through the REAL exporter, and import the archive into a
// FRESH workspace: the golden round-trip asserts every row (and nested
// relation) survives, plus the version pin, the admin gate, idempotent
// re-import, unmapped-member handling, and attachment skipping.
//
// archiveImportReport is a test double mirroring the service's report
// shape; exportArchive (from the export tests) is reused to tamper
// archives.

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"
)

// archiveImportReport mirrors the import report JSON asserted here; it
// is a test double, not the service contract.
type archiveImportReport struct {
	Format          string         `json:"format"`
	SourceWorkspace string         `json:"source_workspace"`
	TargetWorkspace string         `json:"target_workspace"`
	Imported        map[string]int `json:"imported"`
	Skipped         struct {
		Count   int      `json:"count"`
		Reasons []string `json:"reasons"`
	} `json:"skipped"`
}

func postArchiveImport(t *testing.T, e *echo.Echo, slug string, cookie *http.Cookie, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("file", "archive.json")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write(body); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/"+slug+"/import", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func decodeImportReport(t *testing.T, rec *httptest.ResponseRecorder) archiveImportReport {
	t.Helper()
	var r archiveImportReport
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
		t.Fatalf("import report is not valid JSON: %v (body: %.300s)", err, rec.Body.String())
	}
	return r
}

func countRows(t *testing.T, pool *pgxpool.Pool, query string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

// archiveSource holds the seeded source workspace's identity for the
// round-trip assertions.
type archiveSource struct {
	pool         *pgxpool.Pool
	e            *echo.Echo
	adminCookie  *http.Cookie
	adminEmail   string
	slug         string
	wsID         string
	proj1ID      string
	proj2ID      string
	ident1       string
	ident2       string
	issueAID     string
	assigneeMail string
}

// seedArchiveSource builds a source workspace exercising every archived
// section: two projects, issues with nested comments (threaded),
// custom values (text + number), assignees, labels (nested), an estimate
// with points linked to an issue, a cycle and module with memberships, a
// page tree with an author, a sub-issue, and one attachment's metadata.
func seedArchiveSource(t *testing.T, prefix string) archiveSource {
	t.Helper()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testIssueServer(t, pool)
	ctx := context.Background()

	adminEmail := uniqueEmail(prefix + "-admin")
	adminCookie := loginTestUser(t, e, pool, adminEmail, "test-agent", uniqueIP())
	slug := uniqueSlug(prefix)
	createWorkspaceHTTP(t, e, adminCookie, "Archive Co", slug)

	ident1 := uniqueProjectIdentifier(strings.ToUpper(prefix[:2]))
	ident2 := uniqueProjectIdentifier(strings.ToUpper(prefix[2:4]))
	createProjectHTTP(t, e, adminCookie, slug, "Proj One", ident1)
	createProjectHTTP(t, e, adminCookie, slug, "Proj Two", ident2)

	base1 := "/api/v1/workspaces/" + slug + "/projects/" + ident1 + "/issues"
	base2 := "/api/v1/workspaces/" + slug + "/projects/" + ident2 + "/issues"
	issueA := createIssueHTTP(t, e, adminCookie, base1, "Issue A")
	_ = createIssueHTTP(t, e, adminCookie, base2, "Issue B")
	// Sub-issue of A (parent link must survive the round-trip).
	issueA1 := createIssueHTTP(t, e, adminCookie, base1, "Issue A-1")
	if _, err := pool.Exec(ctx, `UPDATE issues SET parent_id = $1::uuid WHERE id = $2::uuid`, issueA, issueA1); err != nil {
		t.Fatalf("set sub-issue parent: %v", err)
	}

	var s archiveSource
	s.pool, s.e, s.adminCookie, s.adminEmail, s.slug = pool, e, adminCookie, adminEmail, slug
	s.ident1, s.ident2, s.issueAID = ident1, ident2, issueA
	mustID := func(what, query string, args ...any) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx, query, args...).Scan(&id); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		return id
	}
	var adminID string
	adminID = mustID("admin id", `SELECT id::text FROM users WHERE email = $1`, adminEmail)
	s.wsID = mustID("workspace id", `SELECT id::text FROM workspaces WHERE slug = $1`, slug)
	s.proj1ID = mustID("proj1 id", `SELECT id::text FROM projects WHERE workspace_id = $1::uuid AND identifier = $2`, s.wsID, ident1)
	s.proj2ID = mustID("proj2 id", `SELECT id::text FROM projects WHERE workspace_id = $1::uuid AND identifier = $2`, s.wsID, ident2)

	// Member assignee on issue A.
	assigneeEmail := uniqueEmail(prefix + "-member")
	loginTestUser(t, e, pool, assigneeEmail, "test-agent", uniqueIP())
	s.assigneeMail = assigneeEmail
	assigneeID := mustID("assignee id", `SELECT id::text FROM users WHERE email = $1`, assigneeEmail)
	exec := func(what, q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	exec("add member", `INSERT INTO workspace_members (workspace_id, user_id, role) VALUES ($1::uuid, $2::uuid, 15)`, s.wsID, assigneeID)
	exec("attach assignee", `INSERT INTO issue_assignees (issue_id, user_id) VALUES ($1::uuid, $2::uuid)`, issueA, assigneeID)

	// Nested labels: "bug" parent of "ui-bug", both on issue A.
	bugID := mustID("bug label", `INSERT INTO labels (workspace_id, name, color) VALUES ($1::uuid, 'bug', '#ff0000') RETURNING id::text`, s.wsID)
	uiBugID := mustID("ui-bug label", `INSERT INTO labels (workspace_id, parent_id, name, color) VALUES ($1::uuid, $2::uuid, 'ui-bug', '#00ff00') RETURNING id::text`, s.wsID, bugID)
	exec("attach labels", `INSERT INTO issue_labels (issue_id, label_id) VALUES ($1::uuid, $2::uuid), ($1::uuid, $3::uuid)`, issueA, bugID, uiBugID)

	// Threaded comments: admin top-level, assignee reply.
	c1 := mustID("comment 1", `INSERT INTO comments (issue_id, actor_id, content) VALUES ($1::uuid, $2::uuid, '{"text":"looks good"}'::jsonb) RETURNING id::text`, issueA, adminID)
	exec("comment reply", `INSERT INTO comments (issue_id, parent_id, actor_id, content) VALUES ($1::uuid, $2::uuid, $3::uuid, '{"text":"agreeing"}'::jsonb)`, issueA, c1, assigneeID)

	// Custom fields: select + number, both valued on issue A.
	sevID := mustID("severity field", `INSERT INTO custom_fields (project_id, name, field_type, options, required, position)
		VALUES ($1::uuid, 'Severity', 'select', '["low","high"]', false, 0) RETURNING id::text`, s.proj1ID)
	ptsID := mustID("points field", `INSERT INTO custom_fields (project_id, name, field_type, options, required, position)
		VALUES ($1::uuid, 'StoryPoints', 'number', '[]', false, 1) RETURNING id::text`, s.proj1ID)
	exec("custom values", `INSERT INTO issue_custom_values (issue_id, field_id, value_text) VALUES ($1::uuid, $2::uuid, 'high')`, issueA, sevID)
	exec("custom number", `INSERT INTO issue_custom_values (issue_id, field_id, value_number) VALUES ($1::uuid, $2::uuid, 3)`, issueA, ptsID)

	// Estimate with points; issue A references point "2".
	estID := mustID("estimate", `INSERT INTO estimates (project_id, name) VALUES ($1::uuid, 'Fibonacci') RETURNING id::text`, s.proj1ID)
	for _, p := range [][2]any{{"1", 1}, {"2", 2}, {"3", 3}} {
		exec("estimate point", `INSERT INTO estimate_points (estimate_id, key, value) VALUES ($1::uuid, $2, $3)`, estID, p[0], p[1])
	}
	pt2 := mustID("point 2", `SELECT id::text FROM estimate_points WHERE estimate_id = $1::uuid AND key = '2'`, estID)
	exec("issue estimate", `UPDATE issues SET estimate_point_id = $1::uuid WHERE id = $2::uuid`, pt2, issueA)

	// Cycle + module memberships; module lead = assignee.
	cycID := mustID("cycle", `INSERT INTO cycles (project_id, name, start_date, end_date, status)
		VALUES ($1::uuid, 'Sprint 1', CURRENT_DATE, CURRENT_DATE + 14, 'current') RETURNING id::text`, s.proj1ID)
	exec("cycle issue", `INSERT INTO cycle_issues (cycle_id, issue_id) VALUES ($1::uuid, $2::uuid)`, cycID, issueA)
	modID := mustID("module", `INSERT INTO modules (project_id, name, description, status, lead_id)
		VALUES ($1::uuid, 'Auth', 'auth work', 'active', $2::uuid) RETURNING id::text`, s.proj1ID, assigneeID)
	exec("module issue", `INSERT INTO module_issues (module_id, issue_id) VALUES ($1::uuid, $2::uuid)`, modID, issueA)

	// Page tree with author.
	specID := mustID("page", `INSERT INTO pages (project_id, title, content, position, author_id)
		VALUES ($1::uuid, 'Spec', 'the spec', 0, $2::uuid) RETURNING id::text`, s.proj1ID, adminID)
	exec("child page", `INSERT INTO pages (project_id, parent_id, title, content, position)
		VALUES ($1::uuid, $2::uuid, 'Details', 'details here', 1)`, s.proj1ID, specID)

	// Attachment metadata on issue A (binaries never exported).
	exec("attachment", `INSERT INTO attachments (issue_id, filename, content_type, size_bytes, stored_path, uploaded_by)
		VALUES ($1::uuid, 'shot.png', 'image/png', 1234, '/srv/glance/blobs/abc', $2::uuid)`, issueA, adminID)

	return s
}

// exportSourceArchive exports the source workspace via the real exporter
// and returns the raw archive bytes.
func exportSourceArchive(t *testing.T, s archiveSource) []byte {
	t.Helper()
	rec := getWorkspaceExport(t, s.e, s.slug, s.adminCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("export = %d, want 200 (body: %.200s)", rec.Code, rec.Body.String())
	}
	return rec.Body.Bytes()
}

// freshTargetWorkspace creates an empty workspace for the same admin and
// returns its slug.
func freshTargetWorkspace(t *testing.T, s archiveSource, prefix string) string {
	t.Helper()
	slug := uniqueSlug(prefix)
	createWorkspaceHTTP(t, s.e, s.adminCookie, "Target Co", slug)
	return slug
}

// workspaceRowCounts snapshots per-table row counts for one workspace.
func workspaceRowCounts(t *testing.T, pool *pgxpool.Pool, wsID string) map[string]int {
	t.Helper()
	ctx := context.Background()
	ws := "p.workspace_id = $1::uuid"
	counts := map[string]int{}
	queries := map[string]string{
		"projects":            `SELECT count(*) FROM projects p WHERE ` + ws,
		"states":              `SELECT count(*) FROM states s JOIN projects p ON p.id = s.project_id WHERE ` + ws,
		"labels":              `SELECT count(*) FROM labels WHERE workspace_id = $1::uuid`,
		"estimates":           `SELECT count(*) FROM estimates e JOIN projects p ON p.id = e.project_id WHERE ` + ws,
		"estimate_points":     `SELECT count(*) FROM estimate_points ep JOIN estimates e ON e.id = ep.estimate_id JOIN projects p ON p.id = e.project_id WHERE ` + ws,
		"custom_fields":       `SELECT count(*) FROM custom_fields cf JOIN projects p ON p.id = cf.project_id WHERE ` + ws,
		"cycles":              `SELECT count(*) FROM cycles c JOIN projects p ON p.id = c.project_id WHERE ` + ws,
		"cycle_issues":        `SELECT count(*) FROM cycle_issues ci JOIN cycles c ON c.id = ci.cycle_id JOIN projects p ON p.id = c.project_id WHERE ` + ws,
		"modules":             `SELECT count(*) FROM modules m JOIN projects p ON p.id = m.project_id WHERE ` + ws,
		"module_issues":       `SELECT count(*) FROM module_issues mi JOIN modules m ON m.id = mi.module_id JOIN projects p ON p.id = m.project_id WHERE ` + ws,
		"pages":               `SELECT count(*) FROM pages pg JOIN projects p ON p.id = pg.project_id WHERE ` + ws,
		"issues":              `SELECT count(*) FROM issues i JOIN projects p ON p.id = i.project_id WHERE ` + ws,
		"issue_assignees":     `SELECT count(*) FROM issue_assignees ia JOIN issues i ON i.id = ia.issue_id JOIN projects p ON p.id = i.project_id WHERE ` + ws,
		"issue_labels":        `SELECT count(*) FROM issue_labels il JOIN issues i ON i.id = il.issue_id JOIN projects p ON p.id = i.project_id WHERE ` + ws,
		"issue_custom_values": `SELECT count(*) FROM issue_custom_values icv JOIN issues i ON i.id = icv.issue_id JOIN projects p ON p.id = i.project_id WHERE ` + ws,
		"comments":            `SELECT count(*) FROM comments c JOIN issues i ON i.id = c.issue_id JOIN projects p ON p.id = i.project_id WHERE ` + ws,
		"attachments":         `SELECT count(*) FROM attachments a JOIN issues i ON i.id = a.issue_id JOIN projects p ON p.id = i.project_id WHERE ` + ws,
	}
	for name, q := range queries {
		var n int
		if err := pool.QueryRow(ctx, q, wsID).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", name, err)
		}
		counts[name] = n
	}
	return counts
}

func TestWorkspaceArchiveImportRoundTrip(t *testing.T) {
	src := seedArchiveSource(t, "arcrt")
	archive := exportSourceArchive(t, src)
	targetSlug := freshTargetWorkspace(t, src, "arctgt")

	rec := postArchiveImport(t, src.e, targetSlug, src.adminCookie, archive)
	if rec.Code != http.StatusOK {
		t.Fatalf("import = %d, want 200 (body: %.500s)", rec.Code, rec.Body.String())
	}
	rep := decodeImportReport(t, rec)

	if rep.Format != "glance-export/1" {
		t.Errorf("report format = %q, want glance-export/1", rep.Format)
	}
	if rep.TargetWorkspace != targetSlug {
		t.Errorf("report target = %q, want %q", rep.TargetWorkspace, targetSlug)
	}

	// Every source row must exist in the target (attachments: skipped).
	want := workspaceRowCounts(t, src.pool, src.wsID)
	var targetWSID string
	if err := src.pool.QueryRow(context.Background(),
		`SELECT id::text FROM workspaces WHERE slug = $1`, targetSlug).Scan(&targetWSID); err != nil {
		t.Fatalf("target workspace id: %v", err)
	}
	got := workspaceRowCounts(t, src.pool, targetWSID)
	for table, n := range want {
		wantN := n
		if table == "attachments" {
			wantN = 0 // metadata skipped by design; binaries were never exported
		}
		if got[table] != wantN {
			t.Errorf("target %s = %d, want %d (source had %d)", table, got[table], wantN, n)
		}
	}

	// Report counters mirror the restored rows.
	for _, key := range []string{"projects", "issues", "comments", "states", "labels",
		"estimates", "estimate_points", "custom_fields", "cycles", "cycle_issues",
		"modules", "module_issues", "pages", "issue_assignees", "issue_labels", "issue_custom_values"} {
		if rep.Imported[key] != want[key] {
			t.Errorf("report imported.%s = %d, want %d", key, rep.Imported[key], want[key])
		}
	}
	if rep.Imported["members"] != 1 {
		t.Errorf("report imported.members = %d, want 1 (the assignee; the importing admin is already a member)", rep.Imported["members"])
	}

	// Attachments are counted as skipped, never silently dropped.
	if rep.Skipped.Count == 0 {
		t.Error("report skipped.count = 0, want > 0 (the attachment must be reported)")
	}
	joined := strings.Join(rep.Skipped.Reasons, "\n")
	if !strings.Contains(strings.ToLower(joined), "attachment") {
		t.Errorf("skipped reasons do not mention attachments: %q", joined)
	}

	// Nested content spot-checks in the target workspace.
	ctx := context.Background()
	var commentText string
	if err := src.pool.QueryRow(ctx, `SELECT c.content::text FROM comments c
		JOIN issues i ON i.id = c.issue_id JOIN projects p ON p.id = i.project_id
		WHERE p.workspace_id = $1::uuid AND c.parent_id IS NULL`, targetWSID).Scan(&commentText); err != nil {
		t.Fatalf("target comment: %v", err)
	}
	if !strings.Contains(commentText, "looks good") {
		t.Errorf("target comment content = %q, want it to contain 'looks good'", commentText)
	}
	var replyParent, replyText string
	if err := src.pool.QueryRow(ctx, `SELECT c.parent_id::text, c.content::text FROM comments c
		JOIN issues i ON i.id = c.issue_id JOIN projects p ON p.id = i.project_id
		WHERE p.workspace_id = $1::uuid AND c.parent_id IS NOT NULL`, targetWSID).Scan(&replyParent, &replyText); err != nil {
		t.Fatalf("target reply comment: %v", err)
	}
	if !strings.Contains(replyText, "agreeing") {
		t.Errorf("target reply content = %q", replyText)
	}
	customValue := func(fieldName, column string) string {
		t.Helper()
		var v string
		if err := src.pool.QueryRow(ctx, `SELECT `+column+`
			FROM issue_custom_values icv JOIN custom_fields cf ON cf.id = icv.field_id
			JOIN issues i ON i.id = icv.issue_id JOIN projects p ON p.id = i.project_id
			WHERE p.workspace_id = $1::uuid AND cf.name = $2`, targetWSID, fieldName).Scan(&v); err != nil {
			t.Fatalf("target custom value %s: %v", fieldName, err)
		}
		return v
	}
	if v := customValue("Severity", "icv.value_text"); v != "high" {
		t.Errorf("target Severity value = %q, want high", v)
	}
	if v := customValue("StoryPoints", "icv.value_number::text"); v != "3" {
		t.Errorf("target StoryPoints value = %q, want 3", v)
	}

	// Assignee email + label names + label nesting survive.
	var assigneeEmail string
	if err := src.pool.QueryRow(ctx, `SELECT u.email::text FROM issue_assignees ia
		JOIN users u ON u.id = ia.user_id JOIN issues i ON i.id = ia.issue_id
		JOIN projects p ON p.id = i.project_id WHERE p.workspace_id = $1::uuid`, targetWSID).Scan(&assigneeEmail); err != nil {
		t.Fatalf("target assignee: %v", err)
	}
	if assigneeEmail != src.assigneeMail {
		t.Errorf("target assignee = %q, want %q", assigneeEmail, src.assigneeMail)
	}
	var childLabel, parentLabel string
	if err := src.pool.QueryRow(ctx, `SELECT c.name, p.name FROM labels c JOIN labels p ON p.id = c.parent_id
		WHERE c.workspace_id = $1::uuid`, targetWSID).Scan(&childLabel, &parentLabel); err != nil {
		t.Fatalf("target nested label: %v", err)
	}
	if childLabel != "ui-bug" || parentLabel != "bug" {
		t.Errorf("target nested label = %q/%q, want ui-bug/bug", childLabel, parentLabel)
	}

	// Sub-issue parent link survives (UUIDs preserved).
	var subParent string
	if err := src.pool.QueryRow(ctx, `SELECT par.name FROM issues i JOIN issues par ON par.id = i.parent_id
		JOIN projects p ON p.id = i.project_id
		WHERE p.workspace_id = $1::uuid AND i.name = 'Issue A-1'`, targetWSID).Scan(&subParent); err != nil {
		t.Fatalf("target sub-issue parent: %v", err)
	}
	if subParent != "Issue A" {
		t.Errorf("target sub-issue parent = %q, want 'Issue A'", subParent)
	}

	// Estimate point link on the issue survives.
	var ptKey string
	if err := src.pool.QueryRow(ctx, `SELECT ep.key FROM issues i JOIN estimate_points ep ON ep.id = i.estimate_point_id
		JOIN projects p ON p.id = i.project_id
		WHERE p.workspace_id = $1::uuid AND i.name = 'Issue A'`, targetWSID).Scan(&ptKey); err != nil {
		t.Fatalf("target estimate point: %v", err)
	}
	if ptKey != "2" {
		t.Errorf("target estimate point key = %q, want 2", ptKey)
	}

	// issue_sequences advanced past the restored sequence ids so future
	// issue creation cannot collide.
	var lastVal, maxSeq int
	if err := src.pool.QueryRow(ctx, `SELECT s.last_value FROM issue_sequences s
		JOIN projects p ON p.id = s.project_id
		WHERE p.workspace_id = $1::uuid AND p.identifier = $2`, targetWSID, src.ident1).Scan(&lastVal); err != nil {
		t.Fatalf("issue_sequences: %v", err)
	}
	if err := src.pool.QueryRow(ctx, `SELECT max(sequence_id) FROM issues i
		JOIN projects p ON p.id = i.project_id
		WHERE p.workspace_id = $1::uuid AND p.identifier = $2`, targetWSID, src.ident1).Scan(&maxSeq); err != nil {
		t.Fatalf("max sequence: %v", err)
	}
	if lastVal < maxSeq {
		t.Errorf("issue_sequences last_value = %d < max restored sequence %d", lastVal, maxSeq)
	}
}

func TestWorkspaceArchiveImportVersionPin(t *testing.T) {
	src := seedArchiveSource(t, "arcver")
	archive := exportSourceArchive(t, src)
	tampered := bytes.Replace(archive, []byte(`"format":"glance-export/1"`), []byte(`"format":"glance-export/2"`), 1)
	if bytes.Equal(tampered, archive) {
		t.Fatal("tamper failed: format marker not found in archive")
	}
	targetSlug := freshTargetWorkspace(t, src, "arcvert")

	rec := postArchiveImport(t, src.e, targetSlug, src.adminCookie, tampered)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("import tampered format = %d, want 400 (body: %.300s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "glance-export/1") {
		t.Errorf("400 body does not name the supported version: %.300s", rec.Body.String())
	}
	// Nothing may be imported on a version rejection.
	var targetWSID string
	if err := src.pool.QueryRow(context.Background(),
		`SELECT id::text FROM workspaces WHERE slug = $1`, targetSlug).Scan(&targetWSID); err != nil {
		t.Fatalf("target workspace id: %v", err)
	}
	if n := countRows(t, src.pool, `SELECT count(*) FROM projects WHERE workspace_id = $1::uuid`, targetWSID); n != 0 {
		t.Errorf("projects imported despite version rejection: %d", n)
	}
}

func TestWorkspaceArchiveImportMissingFormat(t *testing.T) {
	src := seedArchiveSource(t, "arcfmt")
	targetSlug := freshTargetWorkspace(t, src, "arcfmtt")
	rec := postArchiveImport(t, src.e, targetSlug, src.adminCookie, []byte(`{"workspace":{}}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("import without format = %d, want 400 (body: %.300s)", rec.Code, rec.Body.String())
	}
}

func TestWorkspaceArchiveImportForbiddenForMember(t *testing.T) {
	src := seedArchiveSource(t, "arcfbd")
	archive := exportSourceArchive(t, src)
	targetSlug := freshTargetWorkspace(t, src, "arcfbdt")
	ctx := context.Background()

	// Member (role 15) and guest (role 5) of the TARGET workspace.
	for _, tc := range []struct {
		name string
		role int
	}{
		{"member", 15},
		{"guest", 5},
	} {
		email := uniqueEmail("arcfbd-" + tc.name)
		cookie := loginTestUser(t, src.e, src.pool, email, "test-agent", uniqueIP())
		var userID, targetWSID string
		if err := src.pool.QueryRow(ctx, `SELECT id::text FROM users WHERE email = $1`, email).Scan(&userID); err != nil {
			t.Fatalf("user id: %v", err)
		}
		if err := src.pool.QueryRow(ctx, `SELECT id::text FROM workspaces WHERE slug = $1`, targetSlug).Scan(&targetWSID); err != nil {
			t.Fatalf("target workspace id: %v", err)
		}
		if _, err := src.pool.Exec(ctx, `INSERT INTO workspace_members (workspace_id, user_id, role) VALUES ($1::uuid, $2::uuid, $3)`,
			targetWSID, userID, tc.role); err != nil {
			t.Fatalf("add %s: %v", tc.name, err)
		}
		rec := postArchiveImport(t, src.e, targetSlug, cookie, archive)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s import = %d, want 403 (body: %.200s)", tc.name, rec.Code, rec.Body.String())
		}
	}
	// Non-member gets 404 (never a hint the workspace exists).
	outsiderCookie := loginTestUser(t, src.e, src.pool, uniqueEmail("arcfbd-out"), "test-agent", uniqueIP())
	if rec := postArchiveImport(t, src.e, targetSlug, outsiderCookie, archive); rec.Code != http.StatusNotFound {
		t.Errorf("non-member import = %d, want 404", rec.Code)
	}
	// The failed attempts imported nothing.
	var targetWSID string
	if err := src.pool.QueryRow(ctx, `SELECT id::text FROM workspaces WHERE slug = $1`, targetSlug).Scan(&targetWSID); err != nil {
		t.Fatalf("target workspace id: %v", err)
	}
	if n := countRows(t, src.pool, `SELECT count(*) FROM projects WHERE workspace_id = $1::uuid`, targetWSID); n != 0 {
		t.Errorf("projects imported despite 403s: %d", n)
	}
}

func TestWorkspaceArchiveImportUnauthenticated401(t *testing.T) {
	src := seedArchiveSource(t, "arc401")
	targetSlug := freshTargetWorkspace(t, src, "arc401t")
	if rec := postArchiveImport(t, src.e, targetSlug, nil, []byte(`{}`)); rec.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated import = %d, want 401", rec.Code)
	}
}

func TestWorkspaceArchiveImportIdempotent(t *testing.T) {
	src := seedArchiveSource(t, "arcidem")
	archive := exportSourceArchive(t, src)
	targetSlug := freshTargetWorkspace(t, src, "arctgt2")

	first := postArchiveImport(t, src.e, targetSlug, src.adminCookie, archive)
	if first.Code != http.StatusOK {
		t.Fatalf("first import = %d, want 200 (body: %.300s)", first.Code, first.Body.String())
	}
	var targetWSID string
	if err := src.pool.QueryRow(context.Background(),
		`SELECT id::text FROM workspaces WHERE slug = $1`, targetSlug).Scan(&targetWSID); err != nil {
		t.Fatalf("target workspace id: %v", err)
	}
	before := workspaceRowCounts(t, src.pool, targetWSID)

	second := postArchiveImport(t, src.e, targetSlug, src.adminCookie, archive)
	if second.Code != http.StatusOK {
		t.Fatalf("second import = %d, want 200 (body: %.300s)", second.Code, second.Body.String())
	}
	rep := decodeImportReport(t, second)

	// Re-import creates zero new rows anywhere.
	for key, n := range rep.Imported {
		if n != 0 {
			t.Errorf("re-import imported.%s = %d, want 0", key, n)
		}
	}
	after := workspaceRowCounts(t, src.pool, targetWSID)
	for table, n := range before {
		if after[table] != n {
			t.Errorf("table %s changed on re-import: %d -> %d", table, n, after[table])
		}
	}
	// ...and the report says so honestly.
	if rep.Skipped.Count == 0 {
		t.Error("re-import skipped.count = 0, want > 0 (every row already exists)")
	}
	if len(rep.Skipped.Reasons) == 0 {
		t.Error("re-import skipped.reasons is empty, want the skip reasons reported")
	}
}

// tamperArchiveMembers rewrites the raw archive JSON so the seeded
// member's user id and email (everywhere they appear: the members
// section, assignees, comment actors, module lead) point at a user that
// does not exist in this instance. Byte replacement keeps every other
// field intact (the exportArchive test double is lossy and cannot be
// used for a rewrite round-trip).
func tamperArchiveMembers(t *testing.T, archive []byte, src archiveSource) []byte {
	t.Helper()
	ctx := context.Background()
	var assigneeID string
	if err := src.pool.QueryRow(ctx,
		`SELECT id::text FROM users WHERE email = $1`, src.assigneeMail).Scan(&assigneeID); err != nil {
		t.Fatalf("assignee id: %v", err)
	}
	ghostMail := uniqueEmail("ghost")
	// Fixed UUID that no instance user can hold (user ids are
	// gen_random_uuid()): the ghost references must resolve to "no
	// such user" both by email and by id.
	ghostID := "ffffffff-ffff-ffff-ffff-ffffffffffff"
	out := bytes.ReplaceAll(archive,
		[]byte(`"`+assigneeID+`"`), []byte(`"`+ghostID+`"`))
	out = bytes.ReplaceAll(out,
		[]byte(src.assigneeMail), []byte(ghostMail))
	if bytes.Equal(out, archive) {
		t.Fatal("tamper failed: assignee id/email not found in archive")
	}
	return out
}

func TestWorkspaceArchiveImportUnmappedMember(t *testing.T) {
	src := seedArchiveSource(t, "arcunmap")
	archive := tamperArchiveMembers(t, exportSourceArchive(t, src), src)
	targetSlug := freshTargetWorkspace(t, src, "arctgt3")

	rec := postArchiveImport(t, src.e, targetSlug, src.adminCookie, archive)
	if rec.Code != http.StatusOK {
		t.Fatalf("import = %d, want 200 (body: %.500s)", rec.Code, rec.Body.String())
	}
	rep := decodeImportReport(t, rec)
	ctx := context.Background()
	var targetWSID string
	if err := src.pool.QueryRow(ctx,
		`SELECT id::text FROM workspaces WHERE slug = $1`, targetSlug).Scan(&targetWSID); err != nil {
		t.Fatalf("target workspace id: %v", err)
	}

	// The ghost member is not added to the workspace and is reported.
	if n := countRows(t, src.pool, `SELECT count(*) FROM workspace_members wm
		JOIN users u ON u.id = wm.user_id
		WHERE wm.workspace_id = $1::uuid AND u.email LIKE 'ghost-%'`, targetWSID); n != 0 {
		t.Errorf("ghost member added to workspace: %d", n)
	}
	joined := strings.Join(rep.Skipped.Reasons, "\n")
	if !strings.Contains(joined, "ghost-") {
		t.Errorf("skipped reasons do not name the unmapped member email: %q", joined)
	}

	// Their assignee link is dropped (issue becomes unassigned), the
	// comment they authored is skipped, the module lead is NULL — while
	// the issue itself still imports (created_by falls back to importer).
	if n := countRows(t, src.pool, `SELECT count(*) FROM issue_assignees ia
		JOIN issues i ON i.id = ia.issue_id JOIN projects p ON p.id = i.project_id
		WHERE p.workspace_id = $1::uuid`, targetWSID); n != 0 {
		t.Errorf("assignee links imported for unmapped member: %d", n)
	}
	if n := countRows(t, src.pool, `SELECT count(*) FROM comments c
		JOIN issues i ON i.id = c.issue_id JOIN projects p ON p.id = i.project_id
		WHERE p.workspace_id = $1::uuid`, targetWSID); n != 1 {
		t.Errorf("target comments = %d, want 1 (ghost's reply skipped)", n)
	}
	var leadNull bool
	if err := src.pool.QueryRow(ctx, `SELECT m.lead_id IS NULL FROM modules m
		JOIN projects p ON p.id = m.project_id WHERE p.workspace_id = $1::uuid`, targetWSID).Scan(&leadNull); err != nil {
		t.Fatalf("module lead: %v", err)
	}
	if !leadNull {
		t.Error("module lead_id is not NULL after unmapped lead skipped")
	}
	if n := countRows(t, src.pool, `SELECT count(*) FROM issues i
		JOIN projects p ON p.id = i.project_id WHERE p.workspace_id = $1::uuid`, targetWSID); n != 3 {
		t.Errorf("target issues = %d, want 3 (issues import despite unmapped member)", n)
	}
	// The admin-authored comment still links to the mapped admin.
	var actorEmail string
	if err := src.pool.QueryRow(ctx, `SELECT u.email::text FROM comments c JOIN users u ON u.id = c.actor_id
		JOIN issues i ON i.id = c.issue_id JOIN projects p ON p.id = i.project_id
		WHERE p.workspace_id = $1::uuid`, targetWSID).Scan(&actorEmail); err != nil {
		t.Fatalf("comment actor: %v", err)
	}
	if actorEmail != src.adminEmail {
		t.Errorf("comment actor = %q, want %q", actorEmail, src.adminEmail)
	}
}

func TestWorkspaceArchiveImportMalformedJSON(t *testing.T) {
	src := seedArchiveSource(t, "arcmal")
	targetSlug := freshTargetWorkspace(t, src, "arcmalt")
	rec := postArchiveImport(t, src.e, targetSlug, src.adminCookie, []byte(`{"format": "glance-export/1", "members": [`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed archive = %d, want 400 (body: %.300s)", rec.Code, rec.Body.String())
	}
}

func TestWorkspaceArchiveImportMissingFile(t *testing.T) {
	src := seedArchiveSource(t, "arcnofile")
	targetSlug := freshTargetWorkspace(t, src, "arcnofilet")
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/"+targetSlug+"/import", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.AddCookie(src.adminCookie)
	rec := httptest.NewRecorder()
	src.e.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing file = %d, want 400", rec.Code)
	}
}
