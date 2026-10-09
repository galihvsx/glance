package api

// Share-link HTTP endpoint tests (C4T5): create/list/revoke through the
// member routes, the public endpoint's 200/404 behavior, and the PII
// audit (no emails or internal UUIDs in the public payload). Real test
// database, no skips.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func getPublic(t *testing.T, e interface {
	ServeHTTP(http.ResponseWriter, *http.Request)
}, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func TestShareLinkHTTP(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterSatelliteRoutes(e, &IssueHandler{Pool: pool})
	RegisterPublicRoutes(e, &IssueHandler{Pool: pool})

	cookie := loginTestUser(t, e, pool, uniqueEmail("share-http"), "test-agent", uniqueIP())
	slug := uniqueSlug("share-http")
	createWorkspaceHTTP(t, e, cookie, "Share Co", slug)
	ident := uniqueProjectIdentifier("SH")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	issueBase := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/issues"
	iss := createIssueHTTP(t, e, cookie, issueBase, "Shared issue")
	shareBase := issueBase + "/" + iss + "/share"

	// Create → 201 with token + url.
	rec := postAuthedJSON(t, e, http.MethodPost, shareBase, cookie, `{}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create share: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	var created struct {
		Token string `json:"token"`
		URL   string `json:"url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode share: %v", err)
	}
	if created.Token == "" || !strings.Contains(created.URL, "/s/"+created.Token) {
		t.Fatalf("created = %+v, want token + public url", created)
	}

	// List → 200 with the link.
	rec = getAuthed(t, e, http.MethodGet, shareBase, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list shares: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var listed struct {
		Links []struct {
			Token string `json:"token"`
		} `json:"links"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed.Links) != 1 || listed.Links[0].Token != created.Token {
		t.Fatalf("listed = %+v, want the one created", listed)
	}

	// Public fetch (no auth) → 200 with sanitized payload.
	rec = getPublic(t, e, "/api/v1/public/s/"+created.Token)
	if rec.Code != http.StatusOK {
		t.Fatalf("public fetch: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	var pub struct {
		Scope string `json:"scope"`
		Issue *struct {
			DisplayID string `json:"display_id"`
			Name      string `json:"name"`
		} `json:"issue"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &pub); err != nil {
		t.Fatalf("decode public: %v", err)
	}
	if pub.Scope != "issue" || pub.Issue == nil || pub.Issue.Name != "Shared issue" {
		t.Fatalf("public = %+v, want sanitized issue", pub)
	}
	// PII audit: no email addresses and no internal creator fields.
	if strings.Contains(body, "@") {
		t.Fatalf("public payload leaked an email-like string: %s", body)
	}
	if strings.Contains(body, `"created_by"`) || strings.Contains(body, `"author_id"`) ||
		strings.Contains(body, `"project_id"`) {
		t.Fatalf("public payload leaked internal fields: %s", body)
	}

	// Unknown token → 404 (not 403).
	rec = getPublic(t, e, "/api/v1/public/s/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown token: status = %d, want 404", rec.Code)
	}

	// Revoke → 204; public fetch → 404.
	rec = getAuthed(t, e, http.MethodDelete, shareBase+"/"+created.Token, cookie)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("revoke: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getPublic(t, e, "/api/v1/public/s/"+created.Token)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("revoked token: status = %d, want 404", rec.Code)
	}
}
