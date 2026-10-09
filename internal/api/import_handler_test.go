package api

// CSV importer HTTP endpoint tests (C4T8): multipart import, preview,
// template download. Real test database, no skips.

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"
)

func importMultipartBody(t *testing.T, csvData, mapping string) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("file", "import.csv")
	if err != nil {
		t.Fatalf("form file: %v", err)
	}
	if _, err := fw.Write([]byte(csvData)); err != nil {
		t.Fatalf("write csv: %v", err)
	}
	if err := w.WriteField("mapping", mapping); err != nil {
		t.Fatalf("write mapping: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	return &buf, w.FormDataContentType()
}

func setupImportHTTP(t *testing.T) (*echo.Echo, *http.Cookie, string, string) {
	t.Helper()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterImportRoutes(e, &ProjectHandler{Pool: pool})

	cookie := loginTestUser(t, e, pool, uniqueEmail("imp-http"), "test-agent", uniqueIP())
	slug := uniqueSlug("imp-http-ws")
	createWorkspaceHTTP(t, e, cookie, "Import Co", slug)
	ident := strings.ToUpper(strings.ReplaceAll(uniqueSlug("imp-http-p"), "-", ""))
	if len(ident) > 12 {
		ident = ident[:12]
	}
	createProjectHTTP(t, e, cookie, slug, "ImportProj", ident)
	return e, cookie, slug, ident
}

func TestImportHTTP(t *testing.T) {
	e, cookie, slug, ident := setupImportHTTP(t)
	base := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/imports"
	mapping := `{"title":"title","priority":"priority"}`

	// Preview first: 1 good row, 1 bad row (missing title).
	csvData := "title,priority\nGood one,high\n,low\n"
	buf, ctype := importMultipartBody(t, csvData, mapping)
	req := httptest.NewRequest(http.MethodPost, base+"/preview", buf)
	req.Header.Set("Content-Type", ctype)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var prev struct {
		Rows   []map[string]any `json:"rows"`
		Errors []map[string]any `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &prev); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if len(prev.Rows) != 2 || len(prev.Errors) != 1 {
		t.Fatalf("preview rows=%d errors=%d, want 2 rows 1 error", len(prev.Rows), len(prev.Errors))
	}

	// Import: the bad row must not kill the good one.
	buf, ctype = importMultipartBody(t, csvData, mapping)
	req = httptest.NewRequest(http.MethodPost, base, buf)
	req.Header.Set("Content-Type", ctype)
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("import: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var res struct {
		Created int              `json:"created"`
		Failed  int              `json:"failed"`
		Errors  []map[string]any `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode import: %v", err)
	}
	if res.Created != 1 || res.Failed != 1 || len(res.Errors) != 1 {
		t.Fatalf("import = %+v, want created=1 failed=1", res)
	}
}

func TestImportTemplateHTTP(t *testing.T) {
	e, cookie, slug, ident := setupImportHTTP(t)
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/workspaces/"+slug+"/projects/"+ident+"/imports/template.csv", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("template: status = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.HasPrefix(body, "title,description,state,priority,labels,assignee_email,start_date,target_date") {
		t.Fatalf("template header = %q", body[:min(80, len(body))])
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/csv") {
		t.Fatalf("content-type = %q", ct)
	}
}

func TestImportUnauthenticated401(t *testing.T) {
	e, _, slug, ident := setupImportHTTP(t)
	buf, ctype := importMultipartBody(t, "title\nx\n", `{"title":"title"}`)
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/workspaces/"+slug+"/projects/"+ident+"/imports", buf)
	req.Header.Set("Content-Type", ctype)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
