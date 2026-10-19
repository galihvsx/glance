package api

// Epic list rich-filter HTTP tests (C74T0): the query-param contract —
// status multi-value, q search, include_archived backward compat ("1"
// and "true"), and 400s for bad sort/order/date/uuid/status. One
// contract test asserting the handler passes parsed params through.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func listEpicNames(t *testing.T, rec *httptest.ResponseRecorder) []string {
	t.Helper()
	var body struct {
		Epics []struct {
			Name string `json:"name"`
		} `json:"epics"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	names := make([]string, 0, len(body.Epics))
	for _, e := range body.Epics {
		names = append(names, e.Name)
	}
	return names
}

func TestEpicListFiltersHTTP(t *testing.T) {
	pool := newTestPool(t)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterEpicRoutes(e, &IssueHandler{Pool: pool})

	cookie := loginTestUser(t, e, pool, uniqueEmail("epic-flt"), "test-agent", uniqueIP())
	slug := uniqueSlug("epic-flt")
	createWorkspaceHTTP(t, e, cookie, "Filter Co", slug)
	ident := uniqueProjectIdentifier("EF")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	base := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/epics"

	postAuthedJSON(t, e, http.MethodPost, base, cookie, `{"name":"Platform Revamp","description":"The long overhaul"}`)
	rec := postAuthedJSON(t, e, http.MethodPost, base, cookie, `{"name":"Old Thing"}`)
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	rec = postAuthedJSON(t, e, http.MethodPost, base+"/"+created.ID+"/archive", cookie, ``)
	if rec.Code != http.StatusOK {
		t.Fatalf("archive epic: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	postAuthedJSON(t, e, http.MethodPost, base, cookie, `{"name":"Mobile Push"}`)

	// status multi-value.
	rec = getAuthed(t, e, http.MethodGet, base+"?status=active&status=archived", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("status filter: %d (%s)", rec.Code, rec.Body.String())
	}
	if names := listEpicNames(t, rec); len(names) != 3 {
		t.Fatalf("status=active,archived = %v, want 3", names)
	}

	// status=archived only.
	rec = getAuthed(t, e, http.MethodGet, base+"?status=archived", cookie)
	if names := listEpicNames(t, rec); len(names) != 1 || names[0] != "Old Thing" {
		t.Fatalf("status=archived = %v, want [Old Thing]", names)
	}

	// q search.
	rec = getAuthed(t, e, http.MethodGet, base+"?q=revamp", cookie)
	if names := listEpicNames(t, rec); len(names) != 1 || names[0] != "Platform Revamp" {
		t.Fatalf("q=revamp = %v, want [Platform Revamp]", names)
	}

	// include_archived backward compat: both spellings, archived included.
	for _, v := range []string{"1", "true"} {
		rec = getAuthed(t, e, http.MethodGet, base+"?include_archived="+v, cookie)
		if rec.Code != http.StatusOK || strings.Count(rec.Body.String(), `"id"`) != 3 {
			t.Fatalf("include_archived=%s: status=%d body=%s", v, rec.Code, rec.Body.String())
		}
	}
	// Default still excludes archived.
	rec = getAuthed(t, e, http.MethodGet, base, cookie)
	if names := listEpicNames(t, rec); len(names) != 2 {
		t.Fatalf("default = %v, want 2 (no archived)", names)
	}

	// Explicit status wins over include_archived.
	rec = getAuthed(t, e, http.MethodGet, base+"?include_archived=1&status=active", cookie)
	if names := listEpicNames(t, rec); len(names) != 2 {
		t.Fatalf("status wins = %v, want 2 active", names)
	}

	// sort=name asc by default.
	rec = getAuthed(t, e, http.MethodGet, base+"?sort=name", cookie)
	if names := listEpicNames(t, rec); len(names) != 2 || names[0] != "Mobile Push" || names[1] != "Platform Revamp" {
		t.Fatalf("sort=name = %v, want [Mobile Push Platform Revamp]", names)
	}

	// 400s with clear messages.
	for q, want := range map[string]string{
		"?sort=bogus":              "unknown sort",
		"?order=sideways":          "unknown order",
		"?created_from=not-a-date": "unparseable date",
		"?created_by=xyz":          "malformed created_by",
		"?initiative_id=xyz":       "malformed initiative_id",
		"?status=bogus":            "unknown status",
		"?status=":                 "unknown status",
	} {
		rec := getAuthed(t, e, http.MethodGet, base+q, cookie)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400 (body: %s)", q, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("%s: body %q lacks %q", q, rec.Body.String(), want)
		}
	}

	// Empty result is [] — never null.
	rec = getAuthed(t, e, http.MethodGet, base+"?q=no-such-epic-xyz", cookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"epics":[]`) {
		t.Fatalf("empty: status=%d body=%s", rec.Code, rec.Body.String())
	}
}
