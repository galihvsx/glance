package api

// Workspace data export tests (C10T3):
//   GET /api/v1/workspaces/{slug}/export
//
// Admin-only full-workspace JSON archive (format "glance-export/1"),
// streamed with chunked encoding. Real test database, no skips.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"
)

// exportArchive mirrors the archive schema asserted here; it is a test
// double, not the service contract (the service streams, it never builds
// this whole shape in memory).
type exportArchive struct {
	Format     string `json:"format"`
	ExportedAt string `json:"exported_at"`
	SchemaNote string `json:"schema_note"`
	Workspace  struct {
		ID   string `json:"id"`
		Slug string `json:"slug"`
		Name string `json:"name"`
	} `json:"workspace"`
	Members      []map[string]any `json:"members"`
	Projects     []map[string]any `json:"projects"`
	States       []map[string]any `json:"states"`
	Labels       []map[string]any `json:"labels"`
	Estimates    []map[string]any `json:"estimates"`
	CustomFields []map[string]any `json:"custom_fields"`
	Cycles       []map[string]any `json:"cycles"`
	Modules      []map[string]any `json:"modules"`
	Pages        []map[string]any `json:"pages"`
	Issues       []exportIssue    `json:"issues"`
}

type exportIssue struct {
	ID           string           `json:"id"`
	ProjectID    string           `json:"project_id"`
	DisplayID    string           `json:"display_id"`
	Name         string           `json:"name"`
	Assignees    []map[string]any `json:"assignees"`
	Labels       []map[string]any `json:"labels"`
	CustomValues []map[string]any `json:"custom_values"`
	Comments     []map[string]any `json:"comments"`
	Attachments  []map[string]any `json:"attachments"`
}

func getWorkspaceExport(t *testing.T, e *echo.Echo, slug string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/"+slug+"/export", nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

// seedExportWorkspace builds a workspace with two projects, issues with
// nested relations (comment, custom value, assignee, labels), plus one
// cycle, module, page and attachment. Returns pool, server, admin cookie,
// admin email, slug and the two project identifiers.
func seedExportWorkspace(t *testing.T, prefix string) (*pgxpool.Pool, *echo.Echo, *http.Cookie, string, string, string, string) {
	t.Helper()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testIssueServer(t, pool)
	ctx := context.Background()

	adminEmail := uniqueEmail(prefix + "-admin")
	adminCookie := loginTestUser(t, e, pool, adminEmail, "test-agent", uniqueIP())
	slug := uniqueSlug(prefix)
	createWorkspaceHTTP(t, e, adminCookie, "Export Co", slug)

	ident1 := uniqueProjectIdentifier(strings.ToUpper(prefix[:2]))
	ident2 := uniqueProjectIdentifier(strings.ToUpper(prefix[2:4]))
	createProjectHTTP(t, e, adminCookie, slug, "Proj One", ident1)
	createProjectHTTP(t, e, adminCookie, slug, "Proj Two", ident2)

	base1 := "/api/v1/workspaces/" + slug + "/projects/" + ident1 + "/issues"
	base2 := "/api/v1/workspaces/" + slug + "/projects/" + ident2 + "/issues"
	issueA := createIssueHTTP(t, e, adminCookie, base1, "Issue A")
	_ = createIssueHTTP(t, e, adminCookie, base2, "Issue B")

	var adminID, wsID, proj1ID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users WHERE email = $1`, adminEmail).Scan(&adminID); err != nil {
		t.Fatalf("admin id: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM workspaces WHERE slug = $1`, slug).Scan(&wsID); err != nil {
		t.Fatalf("workspace id: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM projects WHERE workspace_id = $1::uuid AND identifier = $2`, wsID, ident1).Scan(&proj1ID); err != nil {
		t.Fatalf("project id: %v", err)
	}

	// Assignee: second user added as member, attached to issue A.
	assigneeEmail := uniqueEmail(prefix + "-member")
	loginTestUser(t, e, pool, assigneeEmail, "test-agent", uniqueIP())
	var assigneeID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users WHERE email = $1`, assigneeEmail).Scan(&assigneeID); err != nil {
		t.Fatalf("assignee id: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO workspace_members (workspace_id, user_id, role) VALUES ($1::uuid, $2::uuid, 15)`, wsID, assigneeID); err != nil {
		t.Fatalf("add member: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO issue_assignees (issue_id, user_id) VALUES ($1::uuid, $2::uuid)`, issueA, assigneeID); err != nil {
		t.Fatalf("attach assignee: %v", err)
	}

	// Labels on issue A.
	for _, name := range []string{"bug", "frontend"} {
		var labelID string
		if err := pool.QueryRow(ctx, `INSERT INTO labels (workspace_id, name, color) VALUES ($1::uuid, $2, '#ff0000') RETURNING id::text`, wsID, name).Scan(&labelID); err != nil {
			t.Fatalf("insert label %q: %v", name, err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO issue_labels (issue_id, label_id) VALUES ($1::uuid, $2::uuid)`, issueA, labelID); err != nil {
			t.Fatalf("attach label %q: %v", name, err)
		}
	}

	// Comment on issue A.
	if _, err := pool.Exec(ctx, `INSERT INTO comments (issue_id, actor_id, content) VALUES ($1::uuid, $2::uuid, $3::jsonb)`,
		issueA, adminID, `{"text":"looks good to me"}`); err != nil {
		t.Fatalf("insert comment: %v", err)
	}

	// Custom field + value on issue A.
	var fieldID string
	if err := pool.QueryRow(ctx, `INSERT INTO custom_fields (project_id, name, field_type, options, required, position)
		VALUES ($1::uuid, 'Severity', 'select', '["low","high"]', false, 0) RETURNING id::text`, proj1ID).Scan(&fieldID); err != nil {
		t.Fatalf("insert custom field: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO issue_custom_values (issue_id, field_id, value_text) VALUES ($1::uuid, $2::uuid, 'high')`, issueA, fieldID); err != nil {
		t.Fatalf("insert custom value: %v", err)
	}

	// Cycle + module membership for issue A.
	var cycleID, moduleID string
	if err := pool.QueryRow(ctx, `INSERT INTO cycles (project_id, name, start_date, end_date, status)
		VALUES ($1::uuid, 'Sprint 1', CURRENT_DATE, CURRENT_DATE + 14, 'current') RETURNING id::text`, proj1ID).Scan(&cycleID); err != nil {
		t.Fatalf("insert cycle: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO cycle_issues (cycle_id, issue_id) VALUES ($1::uuid, $2::uuid)`, cycleID, issueA); err != nil {
		t.Fatalf("cycle issue: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO modules (project_id, name, description, status)
		VALUES ($1::uuid, 'Auth', 'auth work', 'active') RETURNING id::text`, proj1ID).Scan(&moduleID); err != nil {
		t.Fatalf("insert module: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO module_issues (module_id, issue_id) VALUES ($1::uuid, $2::uuid)`, moduleID, issueA); err != nil {
		t.Fatalf("module issue: %v", err)
	}

	// Page in project one.
	if _, err := pool.Exec(ctx, `INSERT INTO pages (project_id, title, content, position) VALUES ($1::uuid, 'Spec', 'the spec', 0)`, proj1ID); err != nil {
		t.Fatalf("insert page: %v", err)
	}

	// Attachment metadata on issue A (binaries live on disk; the archive
	// must carry metadata only).
	if _, err := pool.Exec(ctx, `INSERT INTO attachments (issue_id, filename, content_type, size_bytes, stored_path, uploaded_by)
		VALUES ($1::uuid, 'shot.png', 'image/png', 1234, '/srv/glance/blobs/abc', $2::uuid)`, issueA, adminID); err != nil {
		t.Fatalf("insert attachment: %v", err)
	}

	return pool, e, adminCookie, adminEmail, slug, ident1, ident2
}

func decodeExportArchive(t *testing.T, rec *httptest.ResponseRecorder) exportArchive {
	t.Helper()
	var a exportArchive
	if err := json.Unmarshal(rec.Body.Bytes(), &a); err != nil {
		t.Fatalf("export body is not valid JSON: %v (body: %.200s)", err, rec.Body.String())
	}
	return a
}

func TestWorkspaceExportUnauthenticated401(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testIssueServer(t, pool)

	rec := getWorkspaceExport(t, e, "nope", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestWorkspaceExportForbiddenForMember(t *testing.T) {
	pool, e, _, _, slug, _, _ := seedExportWorkspace(t, "wx403a")
	ctx := context.Background()

	// A workspace member (role 15, non-admin) must get 403, not the archive.
	memberEmail := uniqueEmail("wx403a-plain")
	memberCookie := loginTestUser(t, e, pool, memberEmail, "test-agent", uniqueIP())
	var wsID, memberID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM workspaces WHERE slug = $1`, slug).Scan(&wsID); err != nil {
		t.Fatalf("workspace id: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users WHERE email = $1`, memberEmail).Scan(&memberID); err != nil {
		t.Fatalf("member id: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO workspace_members (workspace_id, user_id, role) VALUES ($1::uuid, $2::uuid, 15)`, wsID, memberID); err != nil {
		t.Fatalf("add member: %v", err)
	}

	rec := getWorkspaceExport(t, e, slug, memberCookie)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("member export: status = %d, want 403 (body: %.200s)", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"format":"glance-export/1"`) {
		t.Fatal("403 response leaks archive bytes — gating must precede streaming")
	}

	// A guest (role 5) is equally forbidden.
	guestEmail := uniqueEmail("wx403a-guest")
	guestCookie := loginTestUser(t, e, pool, guestEmail, "test-agent", uniqueIP())
	var guestID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users WHERE email = $1`, guestEmail).Scan(&guestID); err != nil {
		t.Fatalf("guest id: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO workspace_members (workspace_id, user_id, role) VALUES ($1::uuid, $2::uuid, 5)`, wsID, guestID); err != nil {
		t.Fatalf("add guest: %v", err)
	}
	if rec := getWorkspaceExport(t, e, slug, guestCookie); rec.Code != http.StatusForbidden {
		t.Fatalf("guest export: status = %d, want 403", rec.Code)
	}
}

func TestWorkspaceExportContents(t *testing.T) {
	_, e, adminCookie, adminEmail, slug, ident1, ident2 := seedExportWorkspace(t, "wxfull")

	rec := getWorkspaceExport(t, e, slug, adminCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("export: status = %d, want 200 (body: %.300s)", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get(echo.HeaderContentType); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("content-type = %q, want application/json", ct)
	}
	cd := rec.Header().Get("Content-Disposition")
	if !strings.HasPrefix(cd, "attachment;") {
		t.Fatalf("content-disposition = %q, want attachment", cd)
	}
	wantFilename := fmt.Sprintf(`filename="glance-%s-export-%s.json"`, slug, time.Now().UTC().Format("2006-01-02"))
	if !strings.Contains(cd, wantFilename) {
		t.Fatalf("content-disposition = %q, want it to contain %q", cd, wantFilename)
	}

	a := decodeExportArchive(t, rec)
	if a.Format != "glance-export/1" {
		t.Fatalf("format = %q, want %q", a.Format, "glance-export/1")
	}
	if a.ExportedAt == "" {
		t.Fatal("exported_at is empty")
	}
	if _, err := time.Parse(time.RFC3339, a.ExportedAt); err != nil {
		t.Fatalf("exported_at = %q is not RFC3339: %v", a.ExportedAt, err)
	}
	if a.SchemaNote == "" {
		t.Fatal("schema_note is empty — the archive must document its contract")
	}
	if a.Workspace.Slug != slug {
		t.Fatalf("workspace.slug = %q, want %q", a.Workspace.Slug, slug)
	}
	if len(a.Projects) != 2 {
		t.Fatalf("projects = %d, want 2", len(a.Projects))
	}
	if len(a.States) == 0 {
		t.Fatal("states is empty — project creation seeds default states")
	}
	for _, s := range a.States {
		if s["project_id"] == nil || s["project_id"] == "" {
			t.Fatalf("state missing project_id: %v", s)
		}
	}
	if len(a.Labels) != 2 {
		t.Fatalf("labels = %d, want 2", len(a.Labels))
	}
	if len(a.CustomFields) != 1 {
		t.Fatalf("custom_fields = %d, want 1", len(a.CustomFields))
	}
	if len(a.Cycles) != 1 {
		t.Fatalf("cycles = %d, want 1", len(a.Cycles))
	}
	if len(a.Modules) != 1 {
		t.Fatalf("modules = %d, want 1", len(a.Modules))
	}
	if len(a.Pages) != 1 {
		t.Fatalf("pages = %d, want 1", len(a.Pages))
	}
	if a.Estimates == nil {
		t.Fatal("estimates is null — must be an array (possibly empty)")
	}

	// Members: admin (20) + the seeded member (15).
	roles := map[string]float64{}
	for _, m := range a.Members {
		email, _ := m["email"].(string)
		role, _ := m["role"].(float64)
		roles[email] = role
	}
	if roles[adminEmail] != 20 {
		t.Fatalf("admin role = %v, want 20 (members: %v)", roles[adminEmail], roles)
	}
	if len(roles) != 2 {
		t.Fatalf("members = %d, want 2 (admin + member)", len(roles))
	}

	// Both projects' issues, with nested relations on issue A.
	if len(a.Issues) != 2 {
		t.Fatalf("issues = %d, want 2 (one per project)", len(a.Issues))
	}
	byName := map[string]exportIssue{}
	for _, is := range a.Issues {
		byName[is.Name] = is
	}
	issueA, ok := byName["Issue A"]
	if !ok {
		t.Fatalf("Issue A missing from export (issues: %v)", a.Issues)
	}
	if !strings.HasPrefix(issueA.DisplayID, ident1+"-") {
		t.Fatalf("Issue A display_id = %q, want prefix %q-", issueA.DisplayID, ident1)
	}
	issueB, ok := byName["Issue B"]
	if !ok {
		t.Fatal("Issue B missing from export")
	}
	if !strings.HasPrefix(issueB.DisplayID, ident2+"-") {
		t.Fatalf("Issue B display_id = %q, want prefix %q-", issueB.DisplayID, ident2)
	}

	if len(issueA.Comments) != 1 {
		t.Fatalf("Issue A comments = %d, want 1", len(issueA.Comments))
	}
	if issueA.Comments[0]["actor_email"] != adminEmail {
		t.Fatalf("comment actor_email = %v, want %q", issueA.Comments[0]["actor_email"], adminEmail)
	}
	content, _ := json.Marshal(issueA.Comments[0]["content"])
	if !strings.Contains(string(content), "looks good to me") {
		t.Fatalf("comment content = %s, want it to contain the seeded text", content)
	}

	if len(issueA.CustomValues) != 1 {
		t.Fatalf("Issue A custom_values = %d, want 1", len(issueA.CustomValues))
	}
	cv := issueA.CustomValues[0]
	if cv["field_name"] != "Severity" || cv["value"] != "high" {
		t.Fatalf("custom value = %v, want field_name=Severity value=high", cv)
	}

	if len(issueA.Assignees) != 1 {
		t.Fatalf("Issue A assignees = %d, want 1", len(issueA.Assignees))
	}
	if len(issueA.Labels) != 2 {
		t.Fatalf("Issue A labels = %d, want 2", len(issueA.Labels))
	}
	labelNames := map[string]bool{}
	for _, l := range issueA.Labels {
		name, _ := l["name"].(string)
		labelNames[name] = true
	}
	if !labelNames["bug"] || !labelNames["frontend"] {
		t.Fatalf("issue labels = %v, want bug + frontend", labelNames)
	}

	// Attachments: metadata only — never binaries or server paths.
	if len(issueA.Attachments) != 1 {
		t.Fatalf("Issue A attachments = %d, want 1", len(issueA.Attachments))
	}
	att := issueA.Attachments[0]
	if att["filename"] != "shot.png" {
		t.Fatalf("attachment filename = %v, want shot.png", att["filename"])
	}
	if att["size_bytes"] != float64(1234) {
		t.Fatalf("attachment size_bytes = %v, want 1234", att["size_bytes"])
	}
	for _, forbidden := range []string{"stored_path", "data", "base64", "bytes", "blob"} {
		if _, present := att[forbidden]; present {
			t.Fatalf("attachment carries %q — binaries/paths must never be exported", forbidden)
		}
	}

	// Cycle/module reference the issue; page carries its content.
	cycleIssueIDs, _ := json.Marshal(a.Cycles[0]["issue_ids"])
	if !strings.Contains(string(cycleIssueIDs), issueA.ID) {
		t.Fatalf("cycle issue_ids = %s, want it to contain %s", cycleIssueIDs, issueA.ID)
	}
	moduleIssueIDs, _ := json.Marshal(a.Modules[0]["issue_ids"])
	if !strings.Contains(string(moduleIssueIDs), issueA.ID) {
		t.Fatalf("module issue_ids = %s, want it to contain %s", moduleIssueIDs, issueA.ID)
	}
	if a.Pages[0]["title"] != "Spec" || a.Pages[0]["content"] != "the spec" {
		t.Fatalf("page = %v, want title=Spec content='the spec'", a.Pages[0])
	}
}

func TestWorkspaceExportEmptyWorkspace(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testIssueServer(t, pool)

	cookie := loginTestUser(t, e, pool, uniqueEmail("wxempty"), "test-agent", uniqueIP())
	slug := uniqueSlug("wxempty")
	createWorkspaceHTTP(t, e, cookie, "Empty Co", slug)

	rec := getWorkspaceExport(t, e, slug, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("export: status = %d, want 200 (body: %.300s)", rec.Code, rec.Body.String())
	}
	a := decodeExportArchive(t, rec)
	if a.Format != "glance-export/1" {
		t.Fatalf("format = %q, want glance-export/1", a.Format)
	}
	for name, n := range map[string]int{
		"projects": len(a.Projects), "states": len(a.States), "labels": len(a.Labels),
		"estimates": len(a.Estimates), "custom_fields": len(a.CustomFields),
		"cycles": len(a.Cycles), "modules": len(a.Modules), "pages": len(a.Pages),
		"issues": len(a.Issues),
	} {
		if n != 0 {
			t.Fatalf("%s = %d, want 0 for an empty workspace", name, n)
		}
	}
	// Sections must be [] not null — a future importer should not need
	// nil guards.
	for _, section := range []string{`"projects":[]`, `"issues":[]`, `"states":[]`, `"labels":[]`} {
		if !strings.Contains(rec.Body.String(), section) {
			t.Fatalf("body missing %s — empty sections must encode as []", section)
		}
	}
	if len(a.Members) != 1 {
		t.Fatalf("members = %d, want 1 (the admin)", len(a.Members))
	}
}

func TestWorkspaceExportNonMember404(t *testing.T) {
	pool, e, _, _, slug, _, _ := seedExportWorkspace(t, "wx404")

	outsiderCookie := loginTestUser(t, e, pool, uniqueEmail("wx404-out"), "test-agent", uniqueIP())
	rec := getWorkspaceExport(t, e, slug, outsiderCookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (actor-resolution contract: non-members learn nothing)", rec.Code)
	}
}
