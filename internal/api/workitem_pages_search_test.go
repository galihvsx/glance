package api

// Pages search (C66T0): GET /api/v1/search returns page hits alongside
// issue hits. The envelope keeps `results`/`next_cursor` byte-for-byte
// and adds `page_results` + `page_next_cursor`. Written TDD.

import (
	"encoding/json"
	"net/http"
	"testing"

	"glance/internal/service"
)

func searchEnvelope(t *testing.T, body []byte) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	return m
}

func exactKeys(t *testing.T, m map[string]json.RawMessage, keys ...string) {
	t.Helper()
	for _, k := range keys {
		if _, ok := m[k]; !ok {
			t.Fatalf("envelope missing key %q (keys: %v)", k, rawKeys(m))
		}
	}
	if len(m) != len(keys) {
		t.Fatalf("envelope keys = %v, want exactly %v", rawKeys(m), keys)
	}
}

func rawKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func TestGlobalSearchPageResults(t *testing.T) {
	pool := newTestPool(t)
	e := testWorkItemServer(t, pool)

	cookie := loginTestUser(t, e, pool, uniqueEmail("gspg"), "test-agent", uniqueIP())
	myID := authedUserID(t, e, cookie)
	slug := uniqueSlug("gspg")
	createWorkspaceHTTP(t, e, cookie, "Page Search Co", slug)
	ident := uniqueProjectIdentifier("PG")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)

	ctx := t.Context()
	str := func(s string) *string { return &s }
	createPage := func(title, content string) string {
		p, err := service.CreatePage(ctx, pool, slug, ident, myID, service.PageInput{Title: title, Content: str(content)})
		if err != nil {
			t.Fatalf("CreatePage(%q): %v", title, err)
		}
		return p.ID
	}
	idTitle := createPage("rocket launch plan", "irrelevant body")
	idContent := createPage("engines doc", "the rocket engine needs fuel")
	createPage("unrelated", "nothing about propulsion here")

	rec := getAuthed(t, e, http.MethodGet, "/api/v1/search?q=rocket", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("search: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	env := searchEnvelope(t, rec.Body.Bytes())

	// Issues contract unchanged: results (empty array, not null) and no
	// next_cursor on a single short page; page fields added alongside.
	exactKeys(t, env, "results", "page_results")
	var issueResults []any
	if err := json.Unmarshal(env["results"], &issueResults); err != nil {
		t.Fatalf("decode results: %v", err)
	}
	if issueResults == nil {
		t.Fatal("results is null, want [] (byte-for-byte contract)")
	}

	var pages []json.RawMessage
	if err := json.Unmarshal(env["page_results"], &pages); err != nil {
		t.Fatalf("decode page_results: %v", err)
	}
	if len(pages) != 2 {
		t.Fatalf("page_results = %d, want 2", len(pages))
	}
	// Title-weighted hit ranks first; rank is unexported — never leaked.
	for _, p := range pages {
		var item map[string]json.RawMessage
		if err := json.Unmarshal(p, &item); err != nil {
			t.Fatalf("decode page hit: %v", err)
		}
		if _, ok := item["rank"]; ok {
			t.Fatal("page_results item leaks rank")
		}
	}
	var first struct {
		ID                string `json:"id"`
		Title             string `json:"title"`
		ProjectIdentifier string `json:"project_identifier"`
		WorkspaceSlug     string `json:"workspace_slug"`
	}
	if err := json.Unmarshal(pages[0], &first); err != nil {
		t.Fatalf("decode first page hit: %v", err)
	}
	if first.ID != idTitle || first.Title != "rocket launch plan" {
		t.Fatalf("first page hit = %+v, want the title-weighted match", first)
	}
	if first.ProjectIdentifier != ident || first.WorkspaceSlug != slug {
		t.Fatalf("page hit context wrong: %+v", first)
	}
	var second struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(pages[1], &second); err != nil {
		t.Fatalf("decode second page hit: %v", err)
	}
	if second.ID != idContent {
		t.Fatalf("second page hit id = %s, want %s (content-only match)", second.ID, idContent)
	}
}

func TestGlobalSearchPageCursorPagination(t *testing.T) {
	pool := newTestPool(t)
	e := testWorkItemServer(t, pool)

	cookie := loginTestUser(t, e, pool, uniqueEmail("gspg"), "test-agent", uniqueIP())
	myID := authedUserID(t, e, cookie)
	slug := uniqueSlug("gspgc")
	createWorkspaceHTTP(t, e, cookie, "Page Cursor Co", slug)
	ident := uniqueProjectIdentifier("PC")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)

	ctx := t.Context()
	str := func(s string) *string { return &s }
	for i := 0; i < 3; i++ {
		if _, err := service.CreatePage(ctx, pool, slug, ident, myID,
			service.PageInput{Title: "page cursor probe", Content: str("body")}); err != nil {
			t.Fatalf("CreatePage: %v", err)
		}
	}

	type hit struct {
		ID string `json:"id"`
	}
	var page1 struct {
		Results     []hit  `json:"results"`
		PageResults []hit  `json:"page_results"`
		PageCursor  string `json:"page_next_cursor"`
	}
	rec := getAuthed(t, e, http.MethodGet, "/api/v1/search?q=cursor&per_page=2", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("page 1: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page1); err != nil {
		t.Fatalf("decode page 1: %v", err)
	}
	if len(page1.PageResults) != 2 || page1.PageCursor == "" {
		t.Fatalf("page 1: got %d page results cursor=%q, want 2 + cursor", len(page1.PageResults), page1.PageCursor)
	}
	if len(page1.Results) != 0 {
		t.Fatalf("page 1: issue results = %d, want 0", len(page1.Results))
	}

	var page2 struct {
		PageResults []hit `json:"page_results"`
	}
	rec = getAuthed(t, e, http.MethodGet, "/api/v1/search?q=cursor&per_page=2&page_cursor="+page1.PageCursor, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("page 2: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	env := searchEnvelope(t, rec.Body.Bytes())
	if err := json.Unmarshal(env["page_results"], &page2.PageResults); err != nil {
		t.Fatalf("decode page 2: %v", err)
	}
	if len(page2.PageResults) != 1 {
		t.Fatalf("page 2: got %d page results, want 1", len(page2.PageResults))
	}
	if _, ok := env["page_next_cursor"]; ok {
		t.Fatal("page 2: page_next_cursor present on last page, want omitted")
	}
	seen := map[string]bool{}
	for _, r := range page1.PageResults {
		seen[r.ID] = true
	}
	for _, r := range page2.PageResults {
		if seen[r.ID] {
			t.Fatalf("duplicate page %s across pages", r.ID)
		}
	}

	// Garbage page_cursor → 400; issue cursor untouched.
	rec = getAuthed(t, e, http.MethodGet, "/api/v1/search?q=cursor&page_cursor=garbage", cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("garbage page_cursor: status = %d, want 400", rec.Code)
	}
}

func TestGlobalSearchPagePerPageIndependent(t *testing.T) {
	pool := newTestPool(t)
	e := testWorkItemServer(t, pool)

	cookie := loginTestUser(t, e, pool, uniqueEmail("gspg"), "test-agent", uniqueIP())
	myID := authedUserID(t, e, cookie)
	slug := uniqueSlug("gsppi")
	createWorkspaceHTTP(t, e, cookie, "Page PerPage Co", slug)
	ident := uniqueProjectIdentifier("PP")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)

	ctx := t.Context()
	str := func(s string) *string { return &s }
	for i := 0; i < 3; i++ {
		if _, err := service.CreatePage(ctx, pool, slug, ident, myID,
			service.PageInput{Title: "perpage probe", Content: str("body")}); err != nil {
			t.Fatalf("CreatePage: %v", err)
		}
	}

	// per_page=1 caps the page list; per_page stays valid for the shared
	// handler validation.
	var res struct {
		PageResults []struct {
			ID string `json:"id"`
		} `json:"page_results"`
		PageCursor string `json:"page_next_cursor"`
	}
	rec := getAuthed(t, e, http.MethodGet, "/api/v1/search?q=perpage&per_page=1", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("search: status = %d, want 200", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(res.PageResults) != 1 || res.PageCursor == "" {
		t.Fatalf("got %d page results cursor=%q, want 1 + cursor", len(res.PageResults), res.PageCursor)
	}
}
