package api

// Page↔issue link HTTP endpoint tests (C36T0): all six routes through
// the real router — create from both directions, duplicate → 409,
// cross-project → 409, unknown page/issue/link → 404, malformed ids →
// 400, delete round-trips. Real test database, no skips.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"
)

func setupPageIssueLinkHTTP(t *testing.T, prefix string) (*echo.Echo, *http.Cookie, string, string) {
	t.Helper()
	pool := newTestPool(t)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterPageRoutes(e, &IssueHandler{Pool: pool})
	RegisterPageIssueLinkRoutes(e, &IssueHandler{Pool: pool})

	cookie := loginTestUser(t, e, pool, uniqueEmail(prefix), "test-agent", uniqueIP())
	slug := uniqueSlug(prefix)
	createWorkspaceHTTP(t, e, cookie, "PageIssueLink Co", slug)
	ident := uniqueProjectIdentifier("PL")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	base := "/api/v1/workspaces/" + slug + "/projects/" + ident
	return e, cookie, base + "/pages", base + "/issues"
}

func TestPageIssueLinkHTTP(t *testing.T) {
	e, cookie, pageBase, issueBase := setupPageIssueLinkHTTP(t, "pil-http")
	pageID := createPageHTTP(t, e, cookie, pageBase, `{"title":"Spec"}`)
	issID := createIssueHTTP(t, e, cookie, issueBase, "Implement spec")
	pageIssues := pageBase + "/" + pageID + "/issues"
	issuePages := issueBase + "/" + issID + "/pages"

	// Empty lists on both sides → 200 with empty arrays.
	rec := getAuthed(t, e, http.MethodGet, pageIssues, cookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"issues":[]`) {
		t.Fatalf("empty page issues: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, issuePages, cookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"pages":[]`) {
		t.Fatalf("empty issue pages: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// POST from the page side → 201 link.
	rec = postAuthedJSON(t, e, http.MethodPost, pageIssues, cookie,
		`{"issue_id":"`+issID+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("link from page: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	var created struct {
		ID      string `json:"id"`
		PageID  string `json:"page_id"`
		IssueID string `json:"issue_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode link: %v", err)
	}
	if created.PageID != pageID || created.IssueID != issID || created.ID == "" {
		t.Fatalf("created = %+v", created)
	}

	// Duplicate from the issue side → 409 envelope (same link).
	rec = postAuthedJSON(t, e, http.MethodPost, issuePages, cookie,
		`{"page_id":"`+pageID+`"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate link: status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"conflict"`) {
		t.Fatalf("409 envelope: body=%s", rec.Body.String())
	}

	// Page side list → 200 with display_id/title/state/priority.
	rec = getAuthed(t, e, http.MethodGet, pageIssues, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list page issues: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var listed struct {
		Issues []struct {
			ID        string `json:"id"`
			DisplayID string `json:"display_id"`
			Title     string `json:"title"`
			State     string `json:"state"`
			Priority  int    `json:"priority"`
		} `json:"issues"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed.Issues) != 1 || listed.Issues[0].ID != issID ||
		listed.Issues[0].Title != "Implement spec" || listed.Issues[0].DisplayID == "" ||
		listed.Issues[0].State == "" {
		t.Fatalf("listed issues = %+v", listed.Issues)
	}

	// Issue side list → 200 with the page title.
	rec = getAuthed(t, e, http.MethodGet, issuePages, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list issue pages: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var listedPages struct {
		Pages []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"pages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listedPages); err != nil {
		t.Fatalf("decode pages: %v", err)
	}
	if len(listedPages.Pages) != 1 || listedPages.Pages[0].ID != pageID ||
		listedPages.Pages[0].Title != "Spec" {
		t.Fatalf("listed pages = %+v", listedPages.Pages)
	}

	// DELETE from the issue side → 204; again → 404 envelope.
	rec = postAuthedJSON(t, e, http.MethodDelete, issuePages+"/"+pageID, cookie, ``)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("unlink from issue: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodDelete, issuePages+"/"+pageID, cookie, ``)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("re-unlink: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"not_found"`) {
		t.Fatalf("404 envelope: body=%s", rec.Body.String())
	}

	// POST from the issue side → 201; DELETE from the page side → 204.
	rec = postAuthedJSON(t, e, http.MethodPost, issuePages, cookie,
		`{"page_id":"`+pageID+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("link from issue: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodDelete, pageIssues+"/"+issID, cookie, ``)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("unlink from page: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestPageIssueLinkHTTPErrorCases(t *testing.T) {
	e, cookie, pageBase, issueBase := setupPageIssueLinkHTTP(t, "pil-http-err")
	pageID := createPageHTTP(t, e, cookie, pageBase, `{"title":"Spec"}`)
	issID := createIssueHTTP(t, e, cookie, issueBase, "Work")
	pageIssues := pageBase + "/" + pageID + "/issues"
	issuePages := issueBase + "/" + issID + "/pages"
	zero := "00000000-0000-0000-0000-000000000000"

	// Unknown issue in the page-side body → 404.
	rec := postAuthedJSON(t, e, http.MethodPost, pageIssues, cookie,
		`{"issue_id":"`+zero+`"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown issue: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
	// Unknown page in the issue-side body → 404.
	rec = postAuthedJSON(t, e, http.MethodPost, issuePages, cookie,
		`{"page_id":"`+zero+`"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown page: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
	// Unknown page in the path → 404.
	rec = getAuthed(t, e, http.MethodGet, pageBase+"/"+zero+"/issues", cookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown page path: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
	// Unknown issue in the path → 404.
	rec = getAuthed(t, e, http.MethodGet, issueBase+"/"+zero+"/pages", cookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown issue path: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
	// Malformed page uuid in path → 400.
	rec = getAuthed(t, e, http.MethodGet, pageBase+"/not-a-uuid/issues", cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed page: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	// Malformed issue uuid in path → 400.
	rec = getAuthed(t, e, http.MethodGet, issueBase+"/not-a-uuid/pages", cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed issue: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	// Malformed issue id in the unlink path → 400.
	rec = postAuthedJSON(t, e, http.MethodDelete, pageIssues+"/not-a-uuid", cookie, ``)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed unlink issue: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	// Malformed issue id in the link body → 404 (matches issue-link
	// behaviour: a malformed target matches nothing).
	rec = postAuthedJSON(t, e, http.MethodPost, pageIssues, cookie,
		`{"issue_id":"not-a-uuid"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("malformed body issue: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestPageIssueLinkHTTPCrossProject(t *testing.T) {
	e, cookie, pageBase, _ := setupPageIssueLinkHTTP(t, "pil-http-x")
	pageID := createPageHTTP(t, e, cookie, pageBase, `{"title":"Home"}`)

	// Second project in the same workspace.
	slug := strings.Split(pageBase, "/")[4]
	ident2 := uniqueProjectIdentifier("PX")
	createProjectHTTP(t, e, cookie, slug, "Other", ident2)
	issueBase2 := "/api/v1/workspaces/" + slug + "/projects/" + ident2 + "/issues"
	foreign := createIssueHTTP(t, e, cookie, issueBase2, "foreign")

	rec := postAuthedJSON(t, e, http.MethodPost, pageBase+"/"+pageID+"/issues", cookie,
		`{"issue_id":"`+foreign+`"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("cross-project link: status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"conflict"`) {
		t.Fatalf("409 envelope: body=%s", rec.Body.String())
	}
}
