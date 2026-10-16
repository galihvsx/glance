package api

// Workspace audit-log HTTP tests (C37T0): GET /api/v1/workspaces/{slug}/audit-log
// (admin-only list with filters) and GET .../audit-log/export?format=csv
// (CSV shape, Content-Disposition filename, X-Truncated header via a
// lowered cap). Gates: non-member 404, member non-admin 403,
// unauthenticated 401, malformed filters 400. Real test database, no skips.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"
)

// auditLogFixture seeds a workspace (creator = admin) with two
// settings_updated audit rows via HTTP PATCH, plus a member (role 15)
// and an outsider. Returns the server, cookies, and slug.
func auditLogFixture(t *testing.T, pool *pgxpool.Pool) (*echo.Echo, *http.Cookie, *http.Cookie, *http.Cookie, string) {
	t.Helper()
	e := testWorkspaceServer(t, pool)

	adminEmail := uniqueEmail("audit-http-admin")
	adminCookie := loginTestUser(t, e, pool, adminEmail, "test-agent", "10.0.0.1")
	slug := uniqueSlug("audit-http")
	createWorkspaceHTTP(t, e, adminCookie, "Audit HTTP WS", slug)

	// Two renames → two workspace.settings_updated rows (plus one
	// member.added from the member add below: 3 rows total).
	for _, name := range []string{"Audit HTTP WS v2", "Audit HTTP WS v3"} {
		rec := postAuthedJSON(t, e, http.MethodPatch, "/api/v1/workspaces/"+slug, adminCookie,
			fmt.Sprintf(`{"name":%q}`, name))
		if rec.Code != http.StatusOK {
			t.Fatalf("PATCH rename to %q: status = %d, want 200 (body: %s)", name, rec.Code, rec.Body.String())
		}
	}

	// Member (role 15): second user added by the admin.
	memberCookie := loginTestUser(t, e, pool, uniqueEmail("audit-http-member"), "test-agent", "10.0.0.2")
	memberID := authedUserID(t, e, memberCookie)
	rec := postAuthedJSON(t, e, http.MethodPost, "/api/v1/workspaces/"+slug+"/members", adminCookie,
		fmt.Sprintf(`{"user_id":%q,"role":15}`, memberID))
	if rec.Code != http.StatusOK {
		t.Fatalf("add member: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	outsiderCookie := loginTestUser(t, e, pool, uniqueEmail("audit-http-out"), "test-agent", "10.0.0.3")
	return e, adminCookie, memberCookie, outsiderCookie, slug
}

// auditLogList decodes a list response into items + total.
func auditLogList(t *testing.T, rec *httptest.ResponseRecorder) ([]map[string]any, int64) {
	t.Helper()
	var body struct {
		Items []map[string]any `json:"items"`
		Total int64            `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode audit-log list: %v (body: %s)", err, rec.Body.String())
	}
	return body.Items, body.Total
}

func TestWorkspaceAuditLogHTTPList(t *testing.T) {
	pool := newTestPool(t)
	e, adminCookie, _, _, slug := auditLogFixture(t, pool)

	rec := getAuthed(t, e, http.MethodGet, "/api/v1/workspaces/"+slug+"/audit-log", adminCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	items, total := auditLogList(t, rec)
	if total != 3 || len(items) != 3 {
		t.Fatalf("list: total=%d items=%d, want 3", total, len(items))
	}
	// Newest first: the member add (the fixture adds the member
	// after the two renames).
	if items[0]["action"] != "member.added" {
		t.Errorf("newest action = %v, want member.added", items[0]["action"])
	}
	if items[0]["entity_type"] != "member" {
		t.Errorf("newest entity_type = %v, want member", items[0]["entity_type"])
	}
	// The settings_updated rows carry changed_fields ["name"].
	var settingsMeta map[string]any
	for _, it := range items {
		if it["action"] == "workspace.settings_updated" {
			settingsMeta, _ = it["meta"].(map[string]any)
			break
		}
	}
	if settingsMeta == nil {
		t.Fatalf("no workspace.settings_updated row found")
	}
	meta := settingsMeta
	fields, ok := meta["changed_fields"].([]any)
	if !ok || len(fields) != 1 || fields[0] != "name" {
		t.Errorf("changed_fields = %v, want [name]", meta["changed_fields"])
	}

	// Action filter: no member rows exist.
	rec = getAuthed(t, e, http.MethodGet, "/api/v1/workspaces/"+slug+"/audit-log?action=member.invited", adminCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("action filter: status = %d, want 200", rec.Code)
	}
	if _, total := auditLogList(t, rec); total != 0 {
		t.Errorf("action filter total = %d, want 0", total)
	}

	// Actor filter: substring of the admin's email matches all rows.
	rec = getAuthed(t, e, http.MethodGet, "/api/v1/workspaces/"+slug+"/audit-log?actor=audit-http-admin", adminCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("actor filter: status = %d, want 200", rec.Code)
	}
	if _, total := auditLogList(t, rec); total != 3 {
		t.Errorf("actor filter total = %d, want 3", total)
	}

	// Since after the rows → empty.
	rec = getAuthed(t, e, http.MethodGet, "/api/v1/workspaces/"+slug+"/audit-log?since=2999-01-01T00:00:00Z", adminCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("since filter: status = %d, want 200", rec.Code)
	}
	if _, total := auditLogList(t, rec); total != 0 {
		t.Errorf("since filter total = %d, want 0", total)
	}
}

func TestWorkspaceAuditLogHTTPGates(t *testing.T) {
	pool := newTestPool(t)
	e, adminCookie, memberCookie, outsiderCookie, slug := auditLogFixture(t, pool)
	path := "/api/v1/workspaces/" + slug + "/audit-log"

	// Member non-admin → 403.
	rec := getAuthed(t, e, http.MethodGet, path, memberCookie)
	if rec.Code != http.StatusForbidden {
		t.Errorf("member list: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}
	// Non-member → 404.
	rec = getAuthed(t, e, http.MethodGet, path, outsiderCookie)
	if rec.Code != http.StatusNotFound {
		t.Errorf("outsider list: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
	// Unknown slug → 404.
	rec = getAuthed(t, e, http.MethodGet, "/api/v1/workspaces/no-such-ws/audit-log", adminCookie)
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown slug list: status = %d, want 404", rec.Code)
	}
	// Unauthenticated → 401.
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec2 := httptest.NewRecorder()
	e.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated list: status = %d, want 401", rec2.Code)
	}
	// Malformed filters → 400.
	for _, q := range []string{"?since=not-a-date", "?until=yesterday", "?actor_id=not-a-uuid"} {
		rec := getAuthed(t, e, http.MethodGet, path+q, adminCookie)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("list %s: status = %d, want 400 (body: %s)", q, rec.Code, rec.Body.String())
		}
	}
	// Export gates mirror the list gates.
	exportPath := "/api/v1/workspaces/" + slug + "/audit-log/export?format=csv"
	rec = getAuthed(t, e, http.MethodGet, exportPath, memberCookie)
	if rec.Code != http.StatusForbidden {
		t.Errorf("member export: status = %d, want 403", rec.Code)
	}
	rec = getAuthed(t, e, http.MethodGet, exportPath, outsiderCookie)
	if rec.Code != http.StatusNotFound {
		t.Errorf("outsider export: status = %d, want 404", rec.Code)
	}
}

func TestWorkspaceAuditLogHTTPExport(t *testing.T) {
	pool := newTestPool(t)
	e, adminCookie, _, _, slug := auditLogFixture(t, pool)
	path := "/api/v1/workspaces/" + slug + "/audit-log/export?format=csv"

	rec := getAuthed(t, e, http.MethodGet, path, adminCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("export: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get(echo.HeaderContentType); !strings.Contains(ct, "text/csv") {
		t.Errorf("export Content-Type = %q, want text/csv", ct)
	}
	wantPrefix := `attachment; filename="audit-log-` + slug + `-`
	if disp := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(disp, wantPrefix) || !strings.HasSuffix(disp, `.csv"`) {
		t.Errorf("export Content-Disposition = %q, want prefix %q + .csv", disp, wantPrefix)
	}
	if trunc := rec.Header().Get("X-Truncated"); trunc != "" {
		t.Errorf("export X-Truncated = %q, want absent (3 rows < cap)", trunc)
	}
	lines := strings.Split(strings.TrimRight(rec.Body.String(), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("export lines = %d, want 4 (header + 3 rows):\n%s", len(lines), rec.Body.String())
	}
	if lines[0] != "at,actor_email,action,entity_type,entity_id,ip,meta" {
		t.Errorf("export header = %q", lines[0])
	}
	settingsRows, addedRows := 0, 0
	for _, line := range lines[1:] {
		switch {
		case strings.Contains(line, "workspace.settings_updated,workspace,"):
			settingsRows++
		case strings.Contains(line, "member.added,member,"):
			addedRows++
		default:
			t.Errorf("export row = %q, want a settings_updated or member.added row", line)
		}
	}
	if settingsRows != 2 || addedRows != 1 {
		t.Errorf("export rows: settings_updated=%d member.added=%d, want 2 and 1", settingsRows, addedRows)
	}

	// Non-CSV format → 400.
	rec = getAuthed(t, e, http.MethodGet, "/api/v1/workspaces/"+slug+"/audit-log/export?format=json", adminCookie)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("export format=json: status = %d, want 400", rec.Code)
	}

	// Truncation: lower the cap to 1 — the export keeps 1 row and
	// sets X-Truncated: true.
	origCap := workspaceAuditExportCap
	workspaceAuditExportCap = 1
	t.Cleanup(func() { workspaceAuditExportCap = origCap })
	rec = getAuthed(t, e, http.MethodGet, path, adminCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("truncated export: status = %d, want 200", rec.Code)
	}
	if trunc := rec.Header().Get("X-Truncated"); trunc != "true" {
		t.Errorf("truncated export X-Truncated = %q, want true", trunc)
	}
	lines = strings.Split(strings.TrimRight(rec.Body.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("truncated export lines = %d, want 2 (header + 1 row)", len(lines))
	}
}
