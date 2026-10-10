package api

// Plane-native import HTTP endpoints (C14T4): analyze + execute.
//
//   POST .../imports/plane-import/analyze — multipart `file` = the Plane
//     JSON export (raw .json or .zip of one .json). Read-only: parses and
//     returns the unresolved inventory (PlaneImportAnalysis).
//   POST .../imports/plane-import/execute — multipart `file` +
//     `resolutions` (JSON PlaneImportResolutions) + optional `options`
//     (JSON PlaneImportExecuteOpts, e.g. strict_states). Imports in one
//     transaction; returns the PlaneImportReport.
//
// Real test database, no skips.

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"glance/internal/service"
)

func planeImportFixture(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "plane-export.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return raw
}

// planeImportMultipartBody builds a multipart body with the `file` part and
// optional text fields (resolutions, options are JSON strings).
func planeImportMultipartBody(t *testing.T, fileName string, content []byte, fields map[string]string) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("file", fileName)
	if err != nil {
		t.Fatalf("form file: %v", err)
	}
	if _, err := fw.Write(content); err != nil {
		t.Fatalf("write file: %v", err)
	}
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			t.Fatalf("write field %s: %v", k, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	return &buf, w.FormDataContentType()
}

func setupPlaneImportHTTP(t *testing.T) (*echo.Echo, *pgxpool.Pool, *http.Cookie, string, string) {
	t.Helper()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterImportRoutes(e, &ProjectHandler{Pool: pool})

	cookie := loginTestUser(t, e, pool, uniqueEmail("plane-imp"), "test-agent", uniqueIP())
	slug := uniqueSlug("plane-imp-ws")
	createWorkspaceHTTP(t, e, cookie, "Plane Import Co", slug)
	ident := strings.ToUpper(strings.ReplaceAll(uniqueSlug("plane-imp-p"), "-", ""))
	if len(ident) > 12 {
		ident = ident[:12]
	}
	createProjectHTTP(t, e, cookie, slug, "PlaneImportProj", ident)
	return e, pool, cookie, slug, ident
}

func postPlaneImport(t *testing.T, e *echo.Echo, slug, ident, route string, cookie *http.Cookie, buf *bytes.Buffer, ctype string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/workspaces/"+slug+"/projects/"+ident+"/imports/plane-import/"+route, buf)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

// planeImportIssueCount counts issues in the target project.
func planeImportIssueCount(t *testing.T, pool *pgxpool.Pool, slug, ident string) int {
	t.Helper()
	return countRows(t, pool,
		`SELECT count(*) FROM issues WHERE project_id = (
			SELECT p.id FROM projects p JOIN workspaces w ON w.id = p.workspace_id
			WHERE w.slug = $1 AND p.identifier = $2)`, slug, ident)
}

// addPlaneImportMember logs in a fresh user and adds them to the workspace
// at the given role; returns their session cookie.
func addPlaneImportMember(t *testing.T, e *echo.Echo, pool *pgxpool.Pool, slug string, role int) *http.Cookie {
	t.Helper()
	ctx := context.Background()
	email := uniqueEmail("plane-imp-m")
	cookie := loginTestUser(t, e, pool, email, "test-agent", uniqueIP())
	var userID, wsID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users WHERE email = $1`, email).Scan(&userID); err != nil {
		t.Fatalf("user id: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM workspaces WHERE slug = $1`, slug).Scan(&wsID); err != nil {
		t.Fatalf("workspace id: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO workspace_members (workspace_id, user_id, role) VALUES ($1::uuid, $2::uuid, $3)`,
		wsID, userID, role); err != nil {
		t.Fatalf("add member: %v", err)
	}
	return cookie
}

func planeImportAnalyzeCall(t *testing.T, e *echo.Echo, slug, ident string, cookie *http.Cookie, content []byte) *httptest.ResponseRecorder {
	t.Helper()
	buf, ctype := planeImportMultipartBody(t, "export.json", content, nil)
	return postPlaneImport(t, e, slug, ident, "analyze", cookie, buf, ctype)
}

func planeImportExecuteCall(t *testing.T, e *echo.Echo, slug, ident string, cookie *http.Cookie, content []byte, resolutions, options string) *httptest.ResponseRecorder {
	t.Helper()
	fields := map[string]string{"resolutions": resolutions}
	if options != "" {
		fields["options"] = options
	}
	buf, ctype := planeImportMultipartBody(t, "export.json", content, fields)
	return postPlaneImport(t, e, slug, ident, "execute", cookie, buf, ctype)
}

func containsAll(haystack []string, needles ...string) bool {
	set := map[string]bool{}
	for _, s := range haystack {
		set[s] = true
	}
	for _, n := range needles {
		if !set[n] {
			return false
		}
	}
	return true
}

// decodePlaneImportJSON unmarshals a handler response into the given type.
func decodePlaneImportJSON[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("response is not valid JSON: %v (body: %.300s)", err, rec.Body.String())
	}
	return v
}

// ---------- analyze ----------

func TestPlaneImportAnalyzeHTTP(t *testing.T) {
	e, pool, cookie, slug, ident := setupPlaneImportHTTP(t)
	before := planeImportIssueCount(t, pool, slug, ident)

	rec := planeImportAnalyzeCall(t, e, slug, ident, cookie, planeImportFixture(t))
	if rec.Code != http.StatusOK {
		t.Fatalf("analyze = %d, want 200 (body: %.500s)", rec.Code, rec.Body.String())
	}
	analysis := decodePlaneImportJSON[service.PlaneImportAnalysis](t, rec)
	if analysis.ProjectIdentifier != "ACME" {
		t.Errorf("project_identifier = %q, want ACME", analysis.ProjectIdentifier)
	}
	if analysis.IssueCount != 2 {
		t.Errorf("issue_count = %d, want 2", analysis.IssueCount)
	}
	// The fixture's states ("In Progress", "Todo") match the project's
	// seeded defaults, so nothing is unresolved there.
	if len(analysis.Unresolved.States) != 0 {
		t.Errorf("unresolved states = %v, want none (fixture states match project defaults)", analysis.Unresolved.States)
	}
	if !containsAll(analysis.Unresolved.Labels, "bug") {
		t.Errorf("unresolved labels = %v, want bug", analysis.Unresolved.Labels)
	}
	if !containsAll(analysis.Unresolved.Cycles, "Sprint 12") {
		t.Errorf("unresolved cycles = %v, want Sprint 12", analysis.Unresolved.Cycles)
	}
	if !containsAll(analysis.Unresolved.Modules, "Mobile") {
		t.Errorf("unresolved modules = %v, want Mobile", analysis.Unresolved.Modules)
	}
	if !containsAll(analysis.Unresolved.People, "Ada Lovelace", "Alan Turing", "Grace Hopper") {
		t.Errorf("unresolved people = %v, want the fixture's display names", analysis.Unresolved.People)
	}
	// Analyze is read-only: no issue rows may appear.
	if after := planeImportIssueCount(t, pool, slug, ident); after != before {
		t.Errorf("analyze wrote issues: count %d -> %d, want unchanged", before, after)
	}
}

func TestPlaneImportAnalyzeMalformed400(t *testing.T) {
	e, _, cookie, slug, ident := setupPlaneImportHTTP(t)
	for name, content := range map[string][]byte{
		"garbage":    []byte("this is not json at all {{{"),
		"jsonObject": []byte(`{"not":"an array"}`),
	} {
		rec := planeImportAnalyzeCall(t, e, slug, ident, cookie, content)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s analyze = %d, want 400 (body: %.300s)", name, rec.Code, rec.Body.String())
		}
	}
}

func TestPlaneImportAnalyzeMissingFile400(t *testing.T) {
	e, _, cookie, slug, ident := setupPlaneImportHTTP(t)
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	rec := postPlaneImport(t, e, slug, ident, "analyze", cookie, &buf, w.FormDataContentType())
	if rec.Code != http.StatusBadRequest {
		t.Errorf("analyze without file = %d, want 400", rec.Code)
	}
}

func TestPlaneImportAnalyzeUnauthenticated401(t *testing.T) {
	e, _, _, slug, ident := setupPlaneImportHTTP(t)
	rec := planeImportAnalyzeCall(t, e, slug, ident, nil, planeImportFixture(t))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated analyze = %d, want 401", rec.Code)
	}
}

func TestPlaneImportAnalyzeForbiddenForGuest(t *testing.T) {
	e, pool, _, slug, ident := setupPlaneImportHTTP(t)
	guestCookie := addPlaneImportMember(t, e, pool, slug, 5)
	rec := planeImportAnalyzeCall(t, e, slug, ident, guestCookie, planeImportFixture(t))
	if rec.Code != http.StatusForbidden {
		t.Errorf("guest analyze = %d, want 403", rec.Code)
	}
}

func TestPlaneImportAnalyzeNotFoundForNonMember(t *testing.T) {
	e, pool, _, slug, ident := setupPlaneImportHTTP(t)
	outsider := loginTestUser(t, e, pool, uniqueEmail("plane-imp-out"), "test-agent", uniqueIP())
	rec := planeImportAnalyzeCall(t, e, slug, ident, outsider, planeImportFixture(t))
	if rec.Code != http.StatusNotFound {
		t.Errorf("non-member analyze = %d, want 404", rec.Code)
	}
}

// ---------- execute ----------

const planeImportEmptyResolutions = `{"people":{},"states":{},"labels":{},"cycles":{},"modules":{}}`

func TestPlaneImportExecuteHTTP(t *testing.T) {
	e, pool, cookie, slug, ident := setupPlaneImportHTTP(t)

	rec := planeImportExecuteCall(t, e, slug, ident, cookie, planeImportFixture(t), planeImportEmptyResolutions, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("execute = %d, want 200 (body: %.500s)", rec.Code, rec.Body.String())
	}
	report := decodePlaneImportJSON[service.PlaneImportReport](t, rec)
	if report.ProjectIdentifier != "ACME" {
		t.Errorf("project_identifier = %q, want ACME", report.ProjectIdentifier)
	}
	if report.Created != 2 || report.Skipped != 0 || report.Failed != 0 {
		t.Errorf("report = created=%d skipped=%d failed=%d, want 2/0/0 (errors=%v)",
			report.Created, report.Skipped, report.Failed, report.Errors)
	}
	if len(report.Errors) != 0 {
		t.Errorf("errors = %v, want none", report.Errors)
	}
	// The fixture's states match the project's seeded defaults, so no
	// states are created.
	if len(report.StatesCreated) != 0 {
		t.Errorf("states_created = %v, want none (fixture states match project defaults)", report.StatesCreated)
	}
	if !containsAll(report.LabelsCreated, "bug") {
		t.Errorf("labels_created = %v, want bug", report.LabelsCreated)
	}
	// Created cycles are reported as "name (start → end)" with the default
	// today → +30d window, so only the name prefix is pinned.
	if len(report.CyclesCreated) != 1 || !strings.HasPrefix(report.CyclesCreated[0], "Sprint 12 (") {
		t.Errorf("cycles_created = %v, want one entry starting with \"Sprint 12 (\"", report.CyclesCreated)
	}
	if !containsAll(report.ModulesCreated, "Mobile") {
		t.Errorf("modules_created = %v, want Mobile", report.ModulesCreated)
	}
	if n := planeImportIssueCount(t, pool, slug, ident); n != 2 {
		t.Errorf("issues in db = %d, want 2", n)
	}
}

// planeImportUnknownStateFixture rewrites the fixture so every row uses
// a state name that matches nothing in a fresh project (strict_states
// only rejects unknown states).
func planeImportUnknownStateFixture(t *testing.T) []byte {
	t.Helper()
	var rows []map[string]any
	if err := json.Unmarshal(planeImportFixture(t), &rows); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	for _, r := range rows {
		r["state_name"] = "Uncharted Territory"
	}
	raw, err := json.Marshal(rows)
	if err != nil {
		t.Fatalf("remarshal fixture: %v", err)
	}
	return raw
}

func TestPlaneImportExecuteStrictStates(t *testing.T) {
	e, pool, cookie, slug, ident := setupPlaneImportHTTP(t)

	rec := planeImportExecuteCall(t, e, slug, ident, cookie, planeImportUnknownStateFixture(t),
		planeImportEmptyResolutions, `{"strict_states":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("execute strict_states = %d, want 200 (body: %.500s)", rec.Code, rec.Body.String())
	}
	report := decodePlaneImportJSON[service.PlaneImportReport](t, rec)
	if report.Created != 0 || report.Failed != 2 {
		t.Errorf("report = created=%d failed=%d, want 0/2 (unknown states rejected, none created)",
			report.Created, report.Failed)
	}
	if len(report.StatesCreated) != 0 {
		t.Errorf("states_created = %v, want none under strict_states", report.StatesCreated)
	}
	if n := planeImportIssueCount(t, pool, slug, ident); n != 0 {
		t.Errorf("issues in db = %d, want 0 (no partial state)", n)
	}
}

func TestPlaneImportExecuteMalformed400(t *testing.T) {
	e, _, cookie, slug, ident := setupPlaneImportHTTP(t)
	rec := planeImportExecuteCall(t, e, slug, ident, cookie,
		[]byte("definitely not a plane export"), planeImportEmptyResolutions, "")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("execute garbage = %d, want 400 (body: %.300s)", rec.Code, rec.Body.String())
	}
}

func TestPlaneImportExecuteBadResolutions400(t *testing.T) {
	e, _, cookie, slug, ident := setupPlaneImportHTTP(t)
	rec := planeImportExecuteCall(t, e, slug, ident, cookie,
		planeImportFixture(t), "not-json", "")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("execute bad resolutions = %d, want 400 (body: %.300s)", rec.Code, rec.Body.String())
	}
}

func TestPlaneImportExecuteMissingFile400(t *testing.T) {
	e, _, cookie, slug, ident := setupPlaneImportHTTP(t)
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if err := w.WriteField("resolutions", planeImportEmptyResolutions); err != nil {
		t.Fatalf("write field: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	rec := postPlaneImport(t, e, slug, ident, "execute", cookie, &buf, w.FormDataContentType())
	if rec.Code != http.StatusBadRequest {
		t.Errorf("execute without file = %d, want 400", rec.Code)
	}
}

func TestPlaneImportExecuteUnauthenticated401(t *testing.T) {
	e, _, _, slug, ident := setupPlaneImportHTTP(t)
	rec := planeImportExecuteCall(t, e, slug, ident, nil, planeImportFixture(t), planeImportEmptyResolutions, "")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated execute = %d, want 401", rec.Code)
	}
}

func TestPlaneImportExecuteForbiddenForGuest(t *testing.T) {
	e, pool, _, slug, ident := setupPlaneImportHTTP(t)
	guestCookie := addPlaneImportMember(t, e, pool, slug, 5)
	rec := planeImportExecuteCall(t, e, slug, ident, guestCookie, planeImportFixture(t), planeImportEmptyResolutions, "")
	if rec.Code != http.StatusForbidden {
		t.Errorf("guest execute = %d, want 403", rec.Code)
	}
	if n := planeImportIssueCount(t, pool, slug, ident); n != 0 {
		t.Errorf("guest execute imported %d issues, want 0", n)
	}
}

func TestPlaneImportExecuteNotFoundForNonMember(t *testing.T) {
	e, pool, _, slug, ident := setupPlaneImportHTTP(t)
	outsider := loginTestUser(t, e, pool, uniqueEmail("plane-imp-out"), "test-agent", uniqueIP())
	rec := planeImportExecuteCall(t, e, slug, ident, outsider, planeImportFixture(t), planeImportEmptyResolutions, "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("non-member execute = %d, want 404", rec.Code)
	}
}
