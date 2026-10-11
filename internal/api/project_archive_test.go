package api

// Project archive/delete HTTP endpoint tests (C18T3): archive/unarchive
// (admin-only, issues archived in the same transaction), list/search
// hiding, delete authz (member 403), the confirm guard, the live-issue
// 409 blocker, force delete, FK cascade coverage, and the defensive
// global-search filter when projects.archived_at is absent. Real test
// database, no skips.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"
)

// archiveTestServer registers the routes the archive/delete surface
// needs: workspaces, projects, issues, and the global work-item/search
// endpoints.
func archiveTestServer(t *testing.T, pool *pgxpool.Pool) *echo.Echo {
	t.Helper()
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterWorkItemRoutes(e, &IssueHandler{Pool: pool})
	return e
}

// archiveFixture is one workspace + project with an admin, a member, a
// guest, and an outsider cookie.
type archiveFixture struct {
	pool          *pgxpool.Pool
	e             *echo.Echo
	adminCookie   *http.Cookie
	memberCookie  *http.Cookie
	guestCookie   *http.Cookie
	outsideCookie *http.Cookie
	slug          string
	ident         string
	projBase      string // /api/v1/workspaces/{slug}/projects/{ident}
	issueBase     string // .../issues
}

func newArchiveFixture(t *testing.T, prefix string) *archiveFixture {
	t.Helper()
	pool := newTestPool(t)
	e := archiveTestServer(t, pool)

	admin := loginTestUser(t, e, pool, uniqueEmail(prefix+"-admin"), "test-agent/1.0", uniqueIP())
	member := loginTestUser(t, e, pool, uniqueEmail(prefix+"-member"), "test-agent/1.0", uniqueIP())
	guest := loginTestUser(t, e, pool, uniqueEmail(prefix+"-guest"), "test-agent/1.0", uniqueIP())
	outsider := loginTestUser(t, e, pool, uniqueEmail(prefix+"-out"), "test-agent/1.0", uniqueIP())

	slug := uniqueSlug(prefix)
	createWorkspaceHTTP(t, e, admin, "Archive Co", slug)
	for _, tc := range []struct {
		cookie *http.Cookie
		role   int
	}{{member, 15}, {guest, 5}} {
		id := authedUserID(t, e, tc.cookie)
		rec := postAuthedJSON(t, e, http.MethodPost, "/api/v1/workspaces/"+slug+"/members",
			admin, fmt.Sprintf(`{"user_id":%q,"role":%d}`, id, tc.role))
		if rec.Code != http.StatusOK {
			t.Fatalf("add member role %d: status = %d (body: %s)", tc.role, rec.Code, rec.Body.String())
		}
	}

	ident := uniqueProjectIdentifier(strings.ToUpper(prefix[:2]))
	createProjectHTTP(t, e, admin, slug, "Engineering", ident)
	projBase := "/api/v1/workspaces/" + slug + "/projects/" + ident
	return &archiveFixture{
		pool: pool, e: e,
		adminCookie: admin, memberCookie: member, guestCookie: guest, outsideCookie: outsider,
		slug: slug, ident: ident,
		projBase: projBase, issueBase: projBase + "/issues",
	}
}

// getProjectArchivedAt fetches the project's archived_at via the API.
func getProjectArchivedAt(t *testing.T, f *archiveFixture, cookie *http.Cookie) *string {
	t.Helper()
	rec := getAuthed(t, f.e, http.MethodGet, f.projBase, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("get project: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var p struct {
		ArchivedAt *string `json:"archived_at"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode project: %v", err)
	}
	return p.ArchivedAt
}

// listProjectIdentifiers returns the identifiers from GET .../projects{qs}.
func listProjectIdentifiers(t *testing.T, f *archiveFixture, cookie *http.Cookie, qs string) []string {
	t.Helper()
	rec := getAuthed(t, f.e, http.MethodGet,
		"/api/v1/workspaces/"+f.slug+"/projects"+qs, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list projects %q: status = %d (body: %s)", qs, rec.Code, rec.Body.String())
	}
	var out struct {
		Projects []struct {
			Identifier string  `json:"identifier"`
			ArchivedAt *string `json:"archived_at"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	idents := make([]string, 0, len(out.Projects))
	for _, p := range out.Projects {
		idents = append(idents, p.Identifier)
	}
	return idents
}

func containsIdent(idents []string, want string) bool {
	for _, id := range idents {
		if id == want {
			return true
		}
	}
	return false
}

// searchHitCount runs the global search and counts hits.
func searchHitCount(t *testing.T, f *archiveFixture, cookie *http.Cookie, q string) int {
	t.Helper()
	rec := getAuthed(t, f.e, http.MethodGet, "/api/v1/search?q="+q+"&per_page=100", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("search %q: status = %d (body: %s)", q, rec.Code, rec.Body.String())
	}
	var out struct {
		Results []struct {
			ProjectIdentifier string `json:"project_identifier"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode search: %v", err)
	}
	return len(out.Results)
}

// issueArchivedStates returns name->archived for every issue of the
// fixture project, straight from the database.
func issueArchivedStates(t *testing.T, f *archiveFixture) map[string]bool {
	t.Helper()
	rows, err := f.pool.Query(context.Background(),
		`SELECT i.name, i.archived_at IS NOT NULL
		 FROM issues i JOIN projects p ON p.id = i.project_id
		 WHERE p.workspace_id = (SELECT id FROM workspaces WHERE slug = $1)
		   AND p.identifier = $2 AND i.deleted_at IS NULL`,
		f.slug, f.ident)
	if err != nil {
		t.Fatalf("query issue archived states: %v", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var name string
		var archived bool
		if err := rows.Scan(&name, &archived); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out[name] = archived
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out
}

func TestProjectArchiveFlow(t *testing.T) {
	f := newArchiveFixture(t, "parch-flow")

	// Two live issues plus one individually archived BEFORE the project
	// archive — unarchiving the project must not resurrect it.
	idA := createIssueHTTP(t, f.e, f.adminCookie, f.issueBase, "quixoticarchiveprobe alpha")
	if idA == "" {
		t.Fatalf("create issue returned empty id")
	}
	createIssueHTTP(t, f.e, f.adminCookie, f.issueBase, "quixoticarchiveprobe beta")
	preID := createIssueHTTP(t, f.e, f.adminCookie, f.issueBase, "quixoticarchiveprobe gamma")
	rec := postAuthedJSON(t, f.e, http.MethodPatch,
		f.issueBase+"/"+preID, f.adminCookie, `{"archived":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("pre-archive issue: status = %d (body: %s)", rec.Code, rec.Body.String())
	}

	if got := searchHitCount(t, f, f.adminCookie, "quixoticarchiveprobe"); got != 3 {
		t.Fatalf("search before archive: hits = %d, want 3", got)
	}

	// Archive.
	rec = postAuthedJSON(t, f.e, http.MethodPost, f.projBase+"/archive", f.adminCookie, `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("archive: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if got := getProjectArchivedAt(t, f, f.adminCookie); got == nil {
		t.Fatalf("archived_at not set after archive")
	}

	// All issues archived in the same transaction.
	states := issueArchivedStates(t, f)
	for name, archived := range states {
		if !archived {
			t.Fatalf("issue %q not archived by project archive", name)
		}
	}
	if len(states) != 3 {
		t.Fatalf("issues after archive = %d, want 3", len(states))
	}

	// Hidden from the project list (default) and global search; visible
	// with ?archived=true.
	if idents := listProjectIdentifiers(t, f, f.adminCookie, ""); containsIdent(idents, f.ident) {
		t.Fatalf("archived project %q still in default list", f.ident)
	}
	if idents := listProjectIdentifiers(t, f, f.adminCookie, "?archived=true"); !containsIdent(idents, f.ident) {
		t.Fatalf("archived project %q missing from ?archived=true list", f.ident)
	}
	if got := searchHitCount(t, f, f.adminCookie, "quixoticarchiveprobe"); got != 0 {
		t.Fatalf("search after archive: hits = %d, want 0", got)
	}

	// Idempotent: archiving again is a 200 no-op.
	rec = postAuthedJSON(t, f.e, http.MethodPost, f.projBase+"/archive", f.adminCookie, `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("re-archive: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	// Unarchive: project live again, project-archived issues restored,
	// the individually-archived one stays archived.
	rec = postAuthedJSON(t, f.e, http.MethodPost, f.projBase+"/unarchive", f.adminCookie, `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("unarchive: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if got := getProjectArchivedAt(t, f, f.adminCookie); got != nil {
		t.Fatalf("archived_at = %q after unarchive, want null", *got)
	}
	states = issueArchivedStates(t, f)
	if states["quixoticarchiveprobe alpha"] || states["quixoticarchiveprobe beta"] {
		t.Fatalf("project-archived issues not restored by unarchive: %v", states)
	}
	if !states["quixoticarchiveprobe gamma"] {
		t.Fatalf("individually-archived issue was resurrected by unarchive: %v", states)
	}
	if idents := listProjectIdentifiers(t, f, f.adminCookie, ""); !containsIdent(idents, f.ident) {
		t.Fatalf("unarchived project %q missing from default list", f.ident)
	}

	// Idempotent: unarchiving a live project is a 200 no-op.
	rec = postAuthedJSON(t, f.e, http.MethodPost, f.projBase+"/unarchive", f.adminCookie, `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("re-unarchive: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestProjectArchiveDeleteAuthz(t *testing.T) {
	f := newArchiveFixture(t, "parch-authz")

	cases := []struct {
		name   string
		cookie *http.Cookie
		method string
		path   string
		body   string
		want   int
	}{
		{"member archive", f.memberCookie, http.MethodPost, f.projBase + "/archive", `{}`, http.StatusForbidden},
		{"member unarchive", f.memberCookie, http.MethodPost, f.projBase + "/unarchive", `{}`, http.StatusForbidden},
		{"member delete", f.memberCookie, http.MethodDelete, f.projBase + "?confirm=" + f.ident + "&force=true", "", http.StatusForbidden},
		{"guest archive", f.guestCookie, http.MethodPost, f.projBase + "/archive", `{}`, http.StatusForbidden},
		{"guest delete", f.guestCookie, http.MethodDelete, f.projBase + "?confirm=" + f.ident, "", http.StatusForbidden},
		{"outsider archive", f.outsideCookie, http.MethodPost, f.projBase + "/archive", `{}`, http.StatusNotFound},
		{"outsider delete", f.outsideCookie, http.MethodDelete, f.projBase + "?confirm=" + f.ident, "", http.StatusNotFound},
		// Authz-first: a member with a bad confirm still gets 403, not 400.
		{"member delete bad confirm", f.memberCookie, http.MethodDelete, f.projBase + "?confirm=NOPE", "", http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var rec *httptest.ResponseRecorder
			if tc.method == http.MethodPost {
				rec = postAuthedJSON(t, f.e, tc.method, tc.path, tc.cookie, tc.body)
			} else {
				rec = getAuthed(t, f.e, tc.method, tc.path, tc.cookie)
			}
			if rec.Code != tc.want {
				t.Fatalf("%s %s: status = %d, want %d (body: %s)",
					tc.method, tc.path, rec.Code, tc.want, rec.Body.String())
			}
		})
	}

	// Unknown project → 404 for the admin too.
	rec := postAuthedJSON(t, f.e, http.MethodPost,
		"/api/v1/workspaces/"+f.slug+"/projects/NOPE12/archive", f.adminCookie, `{}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("archive unknown project: status = %d, want 404", rec.Code)
	}
	rec = getAuthed(t, f.e, http.MethodDelete,
		"/api/v1/workspaces/"+f.slug+"/projects/NOPE12?confirm=NOPE12", f.adminCookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("delete unknown project: status = %d, want 404", rec.Code)
	}

	// Malformed identifier → 400.
	rec = postAuthedJSON(t, f.e, http.MethodPost,
		"/api/v1/workspaces/"+f.slug+"/projects/!!!/archive", f.adminCookie, `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("archive bad identifier: status = %d, want 400", rec.Code)
	}
}

// decodeAPIError pulls the spec §5 envelope out of a response body.
func decodeAPIError(t *testing.T, rec *httptest.ResponseRecorder) (code, message string, details map[string]any) {
	t.Helper()
	var env struct {
		Error struct {
			Code    string         `json:"code"`
			Message string         `json:"message"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope: %v (body: %s)", err, rec.Body.String())
	}
	return env.Error.Code, env.Error.Message, env.Error.Details
}

func TestProjectDeleteGuards(t *testing.T) {
	f := newArchiveFixture(t, "pdel-guard")
	createIssueHTTP(t, f.e, f.adminCookie, f.issueBase, "doomed issue one")
	createIssueHTTP(t, f.e, f.adminCookie, f.issueBase, "doomed issue two")

	// Missing confirm → 400 naming the interlock.
	rec := getAuthed(t, f.e, http.MethodDelete, f.projBase, f.adminCookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("delete without confirm: status = %d, want 400", rec.Code)
	}
	code, msg, _ := decodeAPIError(t, rec)
	if code != "bad_request" || !strings.Contains(msg, "confirm") {
		t.Fatalf("delete without confirm: code=%q msg=%q, want bad_request naming confirm", code, msg)
	}

	// Wrong confirm → 400.
	rec = getAuthed(t, f.e, http.MethodDelete, f.projBase+"?confirm=WRONG01", f.adminCookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("delete wrong confirm: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}

	// Live issues, no force → 409 naming the blocker count.
	rec = getAuthed(t, f.e, http.MethodDelete, f.projBase+"?confirm="+f.ident, f.adminCookie)
	if rec.Code != http.StatusConflict {
		t.Fatalf("delete with live issues: status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	code, msg, details := decodeAPIError(t, rec)
	if code != "conflict" {
		t.Fatalf("blocker code = %q, want conflict", code)
	}
	if !strings.Contains(msg, "non-archived issues") {
		t.Fatalf("blocker message %q does not name the blocker", msg)
	}
	if n, ok := details["non_archived_issues"].(float64); !ok || int(n) != 2 {
		t.Fatalf("blocker details = %v, want non_archived_issues=2", details)
	}

	// The refused delete changed nothing.
	rec = getAuthed(t, f.e, http.MethodGet, f.projBase, f.adminCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("project gone after refused delete: status = %d", rec.Code)
	}

	// Archiving the project archives its issues → the guard passes.
	rec = postAuthedJSON(t, f.e, http.MethodPost, f.projBase+"/archive", f.adminCookie, `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("archive before delete: status = %d", rec.Code)
	}
	rec = getAuthed(t, f.e, http.MethodDelete, f.projBase+"?confirm="+f.ident, f.adminCookie)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete archived project: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, f.e, http.MethodGet, f.projBase, f.adminCookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("project still visible after delete: status = %d", rec.Code)
	}
}

func TestProjectDeleteForce(t *testing.T) {
	f := newArchiveFixture(t, "pdel-force")
	createIssueHTTP(t, f.e, f.adminCookie, f.issueBase, "force me")

	// Lowercase confirm is normalized the same way identifiers are.
	rec := getAuthed(t, f.e, http.MethodDelete,
		f.projBase+"?confirm="+strings.ToLower(f.ident)+"&force=true", f.adminCookie)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("force delete: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, f.e, http.MethodGet, f.projBase, f.adminCookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("project still visible after force delete: status = %d", rec.Code)
	}
}

// seedProjectChildren inserts one row per project-scoped child table via
// SQL (the cascade test asserts they all disappear with the project).
func seedProjectChildren(t *testing.T, f *archiveFixture, userID string) (projectID string) {
	t.Helper()
	ctx := context.Background()
	if err := f.pool.QueryRow(ctx,
		`SELECT id::text FROM projects WHERE workspace_id = (SELECT id FROM workspaces WHERE slug = $1)
		 AND identifier = $2`, f.slug, f.ident).Scan(&projectID); err != nil {
		t.Fatalf("resolve project id: %v", err)
	}
	stmts := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO cycles (project_id, name, start_date, end_date, status) VALUES ($1::uuid, 'Sprint 1', CURRENT_DATE, CURRENT_DATE + 14, 'upcoming')`,
			[]any{projectID}},
		{`INSERT INTO modules (project_id, name) VALUES ($1::uuid, 'Epic 1')`,
			[]any{projectID}},
		{`INSERT INTO pages (project_id, title) VALUES ($1::uuid, 'Spec')`,
			[]any{projectID}},
		{`INSERT INTO issue_views (user_id, project_id, name, filters) VALUES ($1::uuid, $2::uuid, 'Mine', '{}')`,
			[]any{userID, projectID}},
	}
	for _, s := range stmts {
		if _, err := f.pool.Exec(ctx, s.sql, s.args...); err != nil {
			t.Fatalf("seed child: %v", err)
		}
	}
	// A share link scoped to the project.
	var wsID string
	if err := f.pool.QueryRow(ctx, `SELECT id::text FROM workspaces WHERE slug = $1`, f.slug).Scan(&wsID); err != nil {
		t.Fatalf("resolve workspace id: %v", err)
	}
	if _, err := f.pool.Exec(ctx,
		`INSERT INTO share_links (token, scope, resource_id, workspace_id, project_id, created_by)
		 VALUES ('tok-cascade-1', 'issue', $1::uuid, $2::uuid, $1::uuid, $3::uuid)`,
		projectID, wsID, userID); err != nil {
		t.Fatalf("seed share link: %v", err)
	}
	return projectID
}

func countProjectChildren(t *testing.T, f *archiveFixture, projectID string) map[string]int {
	t.Helper()
	ctx := context.Background()
	queries := map[string]string{
		"projects":         `SELECT count(*) FROM projects WHERE id = $1::uuid`,
		"issues":           `SELECT count(*) FROM issues WHERE project_id = $1::uuid`,
		"issue_sequences":  `SELECT count(*) FROM issue_sequences WHERE project_id = $1::uuid`,
		"states":           `SELECT count(*) FROM states WHERE project_id = $1::uuid`,
		"cycles":           `SELECT count(*) FROM cycles WHERE project_id = $1::uuid`,
		"modules":          `SELECT count(*) FROM modules WHERE project_id = $1::uuid`,
		"pages":            `SELECT count(*) FROM pages WHERE project_id = $1::uuid`,
		"issue_views":      `SELECT count(*) FROM issue_views WHERE project_id = $1::uuid`,
		"share_links":      `SELECT count(*) FROM share_links WHERE project_id = $1::uuid`,
		"issue_activities": `SELECT count(*) FROM issue_activities WHERE issue_id IN (SELECT id FROM issues WHERE project_id = $1::uuid)`,
	}
	out := map[string]int{}
	for name, q := range queries {
		var n int
		if err := f.pool.QueryRow(ctx, q, projectID).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", name, err)
		}
		out[name] = n
	}
	return out
}

func TestProjectDeleteCascade(t *testing.T) {
	f := newArchiveFixture(t, "pdel-cascade")
	userID := authedUserID(t, f.e, f.adminCookie)
	projectID := seedProjectChildren(t, f, userID)
	issueID := createIssueHTTP(t, f.e, f.adminCookie, f.issueBase, "cascade me")
	_ = issueID

	before := countProjectChildren(t, f, projectID)
	for _, name := range []string{"issues", "states", "cycles", "modules", "pages", "issue_views", "share_links", "issue_sequences"} {
		if before[name] == 0 {
			t.Fatalf("seed %s = 0 before delete", name)
		}
	}

	rec := getAuthed(t, f.e, http.MethodDelete,
		f.projBase+"?confirm="+f.ident+"&force=true", f.adminCookie)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("force delete: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}

	after := countProjectChildren(t, f, projectID)
	for name, n := range after {
		if n != 0 {
			t.Fatalf("cascade leak: %s = %d after project delete", name, n)
		}
	}
}

func TestSearchToleratesMissingArchivedColumn(t *testing.T) {
	f := newArchiveFixture(t, "psearch-def")
	createIssueHTTP(t, f.e, f.adminCookie, f.issueBase, "defensivecolumnprobe")
	if got := searchHitCount(t, f, f.adminCookie, "defensivecolumnprobe"); got != 1 {
		t.Fatalf("search with column: hits = %d, want 1", got)
	}

	ctx := context.Background()
	// Simulate a database migrated before 000048: the column is gone and
	// the search must still work (no unknown-column error).
	if _, err := f.pool.Exec(ctx, `ALTER TABLE projects DROP COLUMN archived_at`); err != nil {
		t.Fatalf("drop archived_at: %v", err)
	}
	t.Cleanup(func() {
		if _, err := f.pool.Exec(ctx, `ALTER TABLE projects ADD COLUMN IF NOT EXISTS archived_at TIMESTAMPTZ NULL`); err != nil {
			t.Errorf("restore archived_at: %v", err)
		}
	})
	if got := searchHitCount(t, f, f.adminCookie, "defensivecolumnprobe"); got != 1 {
		t.Fatalf("search without column: hits = %d, want 1 (query must tolerate the absent column)", got)
	}

	// Restore the column, archive the project, and confirm the filter now
	// applies.
	if _, err := f.pool.Exec(ctx, `ALTER TABLE projects ADD COLUMN archived_at TIMESTAMPTZ NULL`); err != nil {
		t.Fatalf("re-add archived_at: %v", err)
	}
	rec := postAuthedJSON(t, f.e, http.MethodPost, f.projBase+"/archive", f.adminCookie, `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("archive: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	if got := searchHitCount(t, f, f.adminCookie, "defensivecolumnprobe"); got != 0 {
		t.Fatalf("search after archive: hits = %d, want 0", got)
	}
}
