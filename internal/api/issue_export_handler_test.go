package api

// Issues export endpoint tests (C8T3): GET
// /api/v1/workspaces/{slug}/projects/{identifier}/issues/export?format=csv|json.
// Real test database, no skips.

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

// setupExportProject creates a user, workspace and project and returns the
// pool, server, session cookie, issues base path, slug and identifier.
func setupExportProject(t *testing.T, prefix string) (*pgxpool.Pool, *echo.Echo, *http.Cookie, string, string, string) {
	t.Helper()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testIssueServer(t, pool)

	cookie := loginTestUser(t, e, pool, uniqueEmail(prefix), "test-agent", uniqueIP())
	slug := uniqueSlug(prefix)
	createWorkspaceHTTP(t, e, cookie, "Export Co", slug)
	ident := uniqueProjectIdentifier(strings.ToUpper(prefix[:2]))
	createProjectHTTP(t, e, cookie, slug, "ExportProj", ident)
	base := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/issues"
	return pool, e, cookie, base, slug, ident
}

// seedExportRelations attaches two labels and one assignee to an issue via
// SQL (keeps the test independent of the taxonomy endpoints).
func seedExportRelations(t *testing.T, pool *pgxpool.Pool, slug, assigneeEmail, issueID string) {
	t.Helper()
	var wsID, userID string
	if err := pool.QueryRow(t.Context(), `SELECT id::text FROM workspaces WHERE slug = $1`, slug).Scan(&wsID); err != nil {
		t.Fatalf("workspace id: %v", err)
	}
	if err := pool.QueryRow(t.Context(), `SELECT id::text FROM users WHERE email = $1`, assigneeEmail).Scan(&userID); err != nil {
		t.Fatalf("user id: %v", err)
	}
	for _, name := range []string{"bug", "frontend"} {
		var labelID string
		if err := pool.QueryRow(t.Context(),
			`INSERT INTO labels (workspace_id, name, color) VALUES ($1::uuid, $2, '#ff0000') RETURNING id::text`,
			wsID, name).Scan(&labelID); err != nil {
			t.Fatalf("insert label %q: %v", name, err)
		}
		if _, err := pool.Exec(t.Context(),
			`INSERT INTO issue_labels (issue_id, label_id) VALUES ($1::uuid, $2::uuid)`, issueID, labelID); err != nil {
			t.Fatalf("attach label %q: %v", name, err)
		}
	}
	if _, err := pool.Exec(t.Context(),
		`INSERT INTO issue_assignees (issue_id, user_id) VALUES ($1::uuid, $2::uuid)`, issueID, userID); err != nil {
		t.Fatalf("attach assignee: %v", err)
	}
}

func getExport(t *testing.T, e *echo.Echo, path string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func parseCSVBody(t *testing.T, body string) [][]string {
	t.Helper()
	recs, err := csv.NewReader(strings.NewReader(body)).ReadAll()
	if err != nil {
		t.Fatalf("parse exported csv: %v", err)
	}
	return recs
}

func TestExportCSV(t *testing.T) {
	pool, e, cookie, base, slug, ident := setupExportProject(t, "exp-csv")

	assigneeEmail := uniqueEmail("exp-assignee")
	loginTestUser(t, e, pool, assigneeEmail, "test-agent", uniqueIP())
	var wsID, assigneeID string
	if err := pool.QueryRow(t.Context(), `SELECT id::text FROM workspaces WHERE slug = $1`, slug).Scan(&wsID); err != nil {
		t.Fatalf("workspace id: %v", err)
	}
	if err := pool.QueryRow(t.Context(), `SELECT id::text FROM users WHERE email = $1`, assigneeEmail).Scan(&assigneeID); err != nil {
		t.Fatalf("assignee id: %v", err)
	}
	if _, err := pool.Exec(t.Context(),
		`INSERT INTO workspace_members (workspace_id, user_id, role) VALUES ($1::uuid, $2::uuid, 15)`, wsID, assigneeID); err != nil {
		t.Fatalf("add assignee as member: %v", err)
	}

	trickyName := "Fix \"login\", now\non two lines"
	formulaName := "=HYPERLINK(\"http://evil.example\")"
	plainID := createIssueHTTP(t, e, cookie, base, "Plain issue")
	trickyID := createIssueHTTP(t, e, cookie, base, trickyName)
	formulaID := createIssueHTTP(t, e, cookie, base, formulaName)
	seedExportRelations(t, pool, slug, assigneeEmail, plainID)

	rec := getExport(t, e, base+"/export?format=csv", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("export csv: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Fatalf("content-type = %q, want text/csv", ct)
	}
	cd := rec.Header().Get("Content-Disposition")
	if !strings.HasPrefix(cd, "attachment;") {
		t.Fatalf("content-disposition = %q, want attachment", cd)
	}
	datePart := time.Now().UTC().Format("2006-01-02")
	wantFilename := fmt.Sprintf(`filename="glance-%s-issues-%s.csv"`, ident, datePart)
	if !strings.Contains(cd, wantFilename) {
		t.Fatalf("content-disposition = %q, want it to contain %q", cd, wantFilename)
	}

	recs := parseCSVBody(t, rec.Body.String())
	wantHeader := []string{"display_id", "name", "state", "priority", "assignee", "labels", "estimate", "due_date", "created_at", "updated_at"}
	if len(recs) != 4 {
		t.Fatalf("csv records = %d, want 4 (header + 3 issues)", len(recs))
	}
	for i, h := range wantHeader {
		if recs[0][i] != h {
			t.Fatalf("header[%d] = %q, want %q (header: %v)", i, recs[0][i], h, recs[0])
		}
	}
	byName := map[string][]string{}
	for _, r := range recs[1:] {
		byName[strings.TrimPrefix(r[1], "'")] = r
	}
	// Plain issue: relations surfaced.
	plain := byName["Plain issue"]
	if plain == nil {
		t.Fatalf("plain issue row missing (rows: %v)", recs[1:])
	}
	if !strings.HasPrefix(plain[0], ident+"-") {
		t.Fatalf("display_id = %q, want prefix %q-", plain[0], ident)
	}
	if plain[2] != "Backlog" {
		t.Fatalf("state = %q, want Backlog", plain[2])
	}
	if plain[3] != "none" {
		t.Fatalf("priority = %q, want none", plain[3])
	}
	if plain[4] != assigneeEmail {
		t.Fatalf("assignee = %q, want %q", plain[4], assigneeEmail)
	}
	if plain[5] != "bug;frontend" {
		t.Fatalf("labels = %q, want %q", plain[5], "bug;frontend")
	}
	// Tricky name: commas, quotes and newline must round-trip through
	// proper CSV quoting.
	tricky := byName[trickyName]
	if tricky == nil {
		t.Fatalf("tricky-name row missing")
	}
	if tricky[1] != trickyName {
		t.Fatalf("name round-trip = %q, want %q", tricky[1], trickyName)
	}
	// Formula injection: leading = must be neutralized.
	formula := byName[formulaName]
	if formula == nil {
		t.Fatalf("formula-name row missing")
	}
	if formula[1] != "'"+formulaName {
		t.Fatalf("formula name = %q, want single-quote-prefixed %q", formula[1], formulaName)
	}
	_ = trickyID
	_ = formulaID
}

func TestSanitizeCSVCell(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain", "plain"},
		{"", ""},
		{"=1+1", "'=1+1"},
		{"+1+1", "'+1+1"},
		{"-1+1", "'-1+1"},
		{"@SUM(A1:A2)", "'@SUM(A1:A2)"},
		{"\t=1+1", "'\t=1+1"},
		{"\r=1+1", "'\r=1+1"},
		// Not a trigger: email addresses and ordinary text pass through.
		{"teammate@example.com", "teammate@example.com"},
		{"a=b", "a=b"},
	}
	for _, tc := range cases {
		if got := sanitizeCSVCell(tc.in); got != tc.want {
			t.Fatalf("sanitizeCSVCell(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestExportCSVDueDateAndPriorityWords(t *testing.T) {
	_, e, cookie, base, _, _ := setupExportProject(t, "exp-csv2")

	rec := postAuthedJSON(t, e, http.MethodPost, base, cookie,
		`{"name":"dated","priority":4,"target_date":"2026-12-31"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getExport(t, e, base+"/export?format=csv", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("export: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	recs := parseCSVBody(t, rec.Body.String())
	if len(recs) != 2 {
		t.Fatalf("records = %d, want 2", len(recs))
	}
	row := recs[1]
	if row[3] != "urgent" {
		t.Fatalf("priority = %q, want urgent", row[3])
	}
	if row[7] != "2026-12-31" {
		t.Fatalf("due_date = %q, want 2026-12-31", row[7])
	}
	if row[8] == "" || row[9] == "" {
		t.Fatalf("created_at/updated_at must be set (row: %v)", row)
	}
}

func TestExportJSONShape(t *testing.T) {
	_, e, cookie, base, _, _ := setupExportProject(t, "exp-json")

	rec := postAuthedJSON(t, e, http.MethodPost, base, cookie,
		`{"name":"json issue","description":{"type":"doc","content":[]},"priority":2}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}

	rec = getExport(t, e, base+"/export?format=json", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("export json: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("content-type = %q, want application/json", ct)
	}
	if !strings.HasPrefix(rec.Header().Get("Content-Disposition"), "attachment;") {
		t.Fatalf("content-disposition = %q, want attachment", rec.Header().Get("Content-Disposition"))
	}
	var exported []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &exported); err != nil {
		t.Fatalf("decode exported json: %v", err)
	}
	if len(exported) != 1 {
		t.Fatalf("exported issues = %d, want 1", len(exported))
	}
	row := exported[0]
	if row["id"] != created.ID {
		t.Fatalf("id = %v, want %v", row["id"], created.ID)
	}
	if _, ok := row["description"]; !ok {
		t.Fatalf("exported json must include description (row keys: %v)", keysOf(row))
	}

	// Shape parity with the list endpoint (with description requested):
	// same keys, same values.
	lrec := getAuthed(t, e, http.MethodGet, base+"?fields=description&per_page=100", cookie)
	if lrec.Code != http.StatusOK {
		t.Fatalf("list: status = %d", lrec.Code)
	}
	var list struct {
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal(lrec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.Results) != 1 {
		t.Fatalf("list results = %d, want 1", len(list.Results))
	}
	listRow := list.Results[0]
	for k, v := range listRow {
		ev, ok := row[k]
		if !ok {
			t.Fatalf("exported row missing list key %q", k)
		}
		if fmt.Sprintf("%v", ev) != fmt.Sprintf("%v", v) {
			t.Fatalf("key %q: export = %v, list = %v", k, ev, v)
		}
	}
	for k := range row {
		if _, ok := listRow[k]; !ok {
			t.Fatalf("exported row has extra key %q not in list shape", k)
		}
	}
}

func keysOf(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}

func TestExportEmptySet(t *testing.T) {
	_, e, cookie, base, _, _ := setupExportProject(t, "exp-empty")

	rec := getExport(t, e, base+"/export?format=csv", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("empty csv: status = %d", rec.Code)
	}
	recs := parseCSVBody(t, rec.Body.String())
	if len(recs) != 1 {
		t.Fatalf("empty csv records = %d, want header only", len(recs))
	}

	rec = getExport(t, e, base+"/export?format=json", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("empty json: status = %d", rec.Code)
	}
	if strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("empty json body = %q, want []", rec.Body.String())
	}
}

func TestExportInvalidFormat(t *testing.T) {
	_, e, cookie, base, _, _ := setupExportProject(t, "exp-fmt")

	rec := getExport(t, e, base+"/export?format=xml", cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("format=xml: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}

	// Omitted format defaults to CSV.
	rec = getExport(t, e, base+"/export", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("no format: status = %d, want 200 csv default", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Fatalf("no format: content-type = %q, want text/csv", ct)
	}
}

func TestExportScoping(t *testing.T) {
	pool, e, cookie, base, slug, ident := setupExportProject(t, "exp-scope")
	createIssueHTTP(t, e, cookie, base, "scoped issue")

	// Non-member: same 404 the list endpoint returns.
	stranger := loginTestUser(t, e, pool, uniqueEmail("exp-stranger"), "test-agent", uniqueIP())
	rec := getExport(t, e, base+"/export?format=csv", stranger)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("non-member export: status = %d, want 404", rec.Code)
	}
	lrec := getAuthed(t, e, http.MethodGet, base, stranger)
	if lrec.Code != http.StatusNotFound {
		t.Fatalf("non-member list: status = %d, want 404 (parity)", lrec.Code)
	}

	// Guest member (role 5): read parity with the list endpoint.
	guest := loginTestUser(t, e, pool, uniqueEmail("exp-guest"), "test-agent", uniqueIP())
	var wsID, guestID string
	if err := pool.QueryRow(t.Context(), `SELECT id::text FROM workspaces WHERE slug = $1`, slug).Scan(&wsID); err != nil {
		t.Fatalf("workspace id: %v", err)
	}
	if err := pool.QueryRow(t.Context(), `SELECT id::text FROM users WHERE email = $1`, guestEmailOf(t, pool, guest)).Scan(&guestID); err != nil {
		t.Fatalf("guest id: %v", err)
	}
	if _, err := pool.Exec(t.Context(),
		`INSERT INTO workspace_members (workspace_id, user_id, role) VALUES ($1::uuid, $2::uuid, 5)`, wsID, guestID); err != nil {
		t.Fatalf("add guest: %v", err)
	}
	rec = getExport(t, e, base+"/export?format=csv", guest)
	if rec.Code != http.StatusOK {
		t.Fatalf("guest export: status = %d, want 200", rec.Code)
	}

	// Cross-project: a second project in the same workspace exports empty.
	ident2 := uniqueProjectIdentifier(strings.ToUpper("ex"))
	createProjectHTTP(t, e, cookie, slug, "OtherProj", ident2)
	otherBase := "/api/v1/workspaces/" + slug + "/projects/" + ident2 + "/issues"
	rec = getExport(t, e, otherBase+"/export?format=csv", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("other project export: status = %d", rec.Code)
	}
	if recs := parseCSVBody(t, rec.Body.String()); len(recs) != 1 {
		t.Fatalf("other project csv records = %d, want header only", len(recs))
	}
	_ = ident
}

// guestEmailOf resolves the email of a user from their session cookie via
// the sessions table.
func guestEmailOf(t *testing.T, pool *pgxpool.Pool, cookie *http.Cookie) string {
	t.Helper()
	var email string
	if err := pool.QueryRow(t.Context(),
		`SELECT u.email FROM users u JOIN sessions s ON s.user_id = u.id WHERE s.token_hash = $1`,
		sessionTokenHash(cookie.Value)).Scan(&email); err != nil {
		t.Fatalf("guest email: %v", err)
	}
	return email
}

func TestExportFiltersHonored(t *testing.T) {
	_, e, cookie, base, _, _ := setupExportProject(t, "exp-filter")

	alphaID := createIssueHTTP(t, e, cookie, base, "alpha login bug")
	_ = createIssueHTTP(t, e, cookie, base, "beta signup flow")
	// Archive alpha: exports default to the working set like the list.
	arec := postAuthedJSON(t, e, http.MethodPatch, base+"/"+alphaID, cookie, `{"archived":true}`)
	if arec.Code != http.StatusOK {
		t.Fatalf("archive: status = %d (body: %s)", arec.Code, arec.Body.String())
	}

	// q filter.
	rec := getExport(t, e, base+"/export?format=csv&q=signup", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("q export: status = %d", rec.Code)
	}
	recs := parseCSVBody(t, rec.Body.String())
	if len(recs) != 2 || !strings.Contains(recs[1][1], "beta") {
		t.Fatalf("q=signup rows = %v, want only beta", recs)
	}

	// archived=1 brings alpha back.
	rec = getExport(t, e, base+"/export?format=csv&archived=1", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("archived export: status = %d", rec.Code)
	}
	if recs := parseCSVBody(t, rec.Body.String()); len(recs) != 3 {
		t.Fatalf("archived=1 rows = %d, want 3 (header + 2)", len(recs))
	}

	// Bad filter values are 400, exactly like the list endpoint.
	rec = getExport(t, e, base+"/export?format=csv&priority=nope", cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad priority: status = %d, want 400", rec.Code)
	}
}

func TestExportRowCap(t *testing.T) {
	old := service.MaxExportRows
	service.MaxExportRows = 2
	defer func() { service.MaxExportRows = old }()

	_, e, cookie, base, _, _ := setupExportProject(t, "exp-cap")
	createIssueHTTP(t, e, cookie, base, "one")
	createIssueHTTP(t, e, cookie, base, "two")
	createIssueHTTP(t, e, cookie, base, "three")

	rec := getExport(t, e, base+"/export?format=csv", cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("over-cap export: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "exceed") {
		t.Fatalf("over-cap body = %q, want an honest row-count message", rec.Body.String())
	}
	// Under the cap still works.
	service.MaxExportRows = 3
	rec = getExport(t, e, base+"/export?format=csv", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("at-cap export: status = %d, want 200", rec.Code)
	}
}

func TestExportFilenameDateFormat(t *testing.T) {
	_, e, cookie, base, _, ident := setupExportProject(t, "exp-fn")
	rec := getExport(t, e, base+"/export?format=json", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("export: status = %d", rec.Code)
	}
	cd := rec.Header().Get("Content-Disposition")
	matched, _ := regexp.MatchString(
		`^attachment; filename="glance-`+regexp.QuoteMeta(ident)+`-issues-\d{4}-\d{2}-\d{2}\.json"$`, cd)
	if !matched {
		t.Fatalf("content-disposition = %q, want glance-<ident>-issues-<date>.json", cd)
	}
}
