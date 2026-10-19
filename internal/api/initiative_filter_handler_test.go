package api

// Initiative list rich-filter HTTP tests (C73T0): the query-param
// contract — status multi-value, q search, include_archived backward
// compat ("1" and "true"), and 400s for bad sort/order/date/uuid.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func listInitiativeNames(t *testing.T, rec *httptest.ResponseRecorder) []string {
	t.Helper()
	var body struct {
		Initiatives []struct {
			Name string `json:"name"`
		} `json:"initiatives"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	names := make([]string, 0, len(body.Initiatives))
	for _, in := range body.Initiatives {
		names = append(names, in.Name)
	}
	return names
}

func TestInitiativeListFiltersHTTP(t *testing.T) {
	pool := newTestPool(t)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterInitiativeRoutes(e, &IssueHandler{Pool: pool})

	cookie := loginTestUser(t, e, pool, uniqueEmail("init-flt"), "test-agent", uniqueIP())
	slug := uniqueSlug("init-flt")
	createWorkspaceHTTP(t, e, cookie, "Filter Co", slug)
	base := "/api/v1/workspaces/" + slug + "/initiatives"

	postAuthedJSON(t, e, http.MethodPost, base, cookie, `{"name":"Platform Revamp"}`)
	postAuthedJSON(t, e, http.MethodPost, base, cookie, `{"name":"Done Thing","status":"completed"}`)
	postAuthedJSON(t, e, http.MethodPost, base, cookie, `{"name":"Old Thing","status":"archived"}`)

	// status multi-value.
	rec := getAuthed(t, e, http.MethodGet, base+"?status=active&status=completed", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("status filter: %d", rec.Code)
	}
	names := listInitiativeNames(t, rec)
	if len(names) != 2 {
		t.Fatalf("status=active,completed = %v, want 2", names)
	}

	// q search.
	rec = getAuthed(t, e, http.MethodGet, base+"?q=revamp", cookie)
	if names := listInitiativeNames(t, rec); len(names) != 1 || names[0] != "Platform Revamp" {
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
	if names := listInitiativeNames(t, rec); len(names) != 2 {
		t.Fatalf("default = %v, want 2 (no archived)", names)
	}

	// sort=name asc by default.
	rec = getAuthed(t, e, http.MethodGet, base+"?status=active&status=completed&sort=name", cookie)
	if names := listInitiativeNames(t, rec); len(names) != 2 || names[0] != "Done Thing" || names[1] != "Platform Revamp" {
		t.Fatalf("sort=name = %v, want [Done Thing Platform Revamp]", names)
	}

	// 400s with clear messages.
	for q, want := range map[string]string{
		"?sort=bogus":              "unknown sort",
		"?order=sideways":          "unknown order",
		"?created_from=not-a-date": "unparseable date",
		"?created_by=xyz":          "malformed created_by",
		"?project_id=xyz":          "malformed project_id",
		"?status=bogus":            "unknown status",
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
	rec = getAuthed(t, e, http.MethodGet, base+"?q=no-such-initiative-xyz", cookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"initiatives":[]`) {
		t.Fatalf("empty: status=%d body=%s", rec.Code, rec.Body.String())
	}
}
