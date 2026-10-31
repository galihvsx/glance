package api

// PQL scope HTTP tests (C95T0): ?cycle= and ?module= survive PQL mode
// over HTTP — the scope params are parsed by parsePQLListInput and AND
// with the PQL predicate — while the visual filters stay ignored.
// The PQL predicate here (priority >= 0) matches every issue, so the
// scope alone decides the result.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"
)

func pqlScopeSortedNames(t *testing.T, got []string) []string {
	t.Helper()
	out := append([]string(nil), got...)
	sort.Strings(out)
	return out
}

func TestIssueListHTTPPQLScope(t *testing.T) {
	pool, e, cookie, base := setupBulkProject(t, "pqlscope")
	// testIssueServer registers issue routes only; the scope setup
	// below needs the cycle/module endpoints too.
	RegisterCycleRoutes(e, &IssueHandler{Pool: pool})
	RegisterModuleRoutes(e, &IssueHandler{Pool: pool})
	// base = /api/v1/workspaces/<slug>/projects/<ident>/issues
	parts := strings.Split(strings.Trim(base, "/"), "/")
	slug, ident := parts[3], parts[5]
	projBase := "/api/v1/workspaces/" + slug + "/projects/" + ident
	cycleBase := projBase + "/cycles"
	moduleBase := projBase + "/modules"

	inCycle := createIssueHTTP(t, e, cookie, base, "InCycle")
	inModule := createIssueHTTP(t, e, cookie, base, "InModule")
	createIssueHTTP(t, e, cookie, base, "Outside")

	now := time.Now()
	start := now.AddDate(0, 0, -1).Format("2006-01-02")
	end := now.AddDate(0, 0, 13).Format("2006-01-02")
	cycleID := createCycleHTTP(t, e, cookie, cycleBase, "Sprint Scope", start, end)

	rec := postAuthedJSON(t, e, http.MethodPost, moduleBase, cookie, `{"name":"Mod Scope"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create module: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var module struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &module); err != nil {
		t.Fatalf("decode module: %v", err)
	}

	add := func(url, id string) {
		rec := postAuthedJSON(t, e, http.MethodPost, url, cookie,
			fmt.Sprintf(`{"issue_ids":[%q]}`, id))
		if rec.Code != http.StatusOK {
			t.Fatalf("add %s: status = %d (body: %s)", url, rec.Code, rec.Body.String())
		}
	}
	add(cycleBase+"/"+cycleID+"/issues", inCycle)
	add(moduleBase+"/"+module.ID+"/issues", inModule)

	pql := "pql=" + url.QueryEscape("priority >= 0") + "&order_by=-sequence_id&per_page=100"
	want := func(got []string, names ...string) {
		t.Helper()
		sort.Strings(names)
		if strings.Join(pqlScopeSortedNames(t, got), "|") != strings.Join(names, "|") {
			t.Fatalf("got %v, want %v", pqlScopeSortedNames(t, got), names)
		}
	}

	// PQL alone: all three issues.
	want(pqlListNamesHTTP(t, e, cookie, base, pql), "InCycle", "InModule", "Outside")
	// PQL + cycle: only the cycle member.
	want(pqlListNamesHTTP(t, e, cookie, base, pql+"&cycle="+cycleID), "InCycle")
	// PQL + module: only the module member.
	want(pqlListNamesHTTP(t, e, cookie, base, pql+"&module="+module.ID), "InModule")
	// Module scope without PQL: exactly the module membership.
	want(pqlListNamesHTTP(t, e, cookie, base, "module="+module.ID+"&order_by=-sequence_id&per_page=100"), "InModule")
	// Cycle scope without PQL: exactly the cycle membership.
	want(pqlListNamesHTTP(t, e, cookie, base, "cycle="+cycleID+"&order_by=-sequence_id&per_page=100"), "InCycle")
	// Foreign scope + PQL: empty list, still 200.
	want(pqlListNamesHTTP(t, e, cookie, base, pql+"&module=11111111-1111-1111-1111-111111111111"))
	// Malformed module under PQL: 400, not a silent empty list.
	rec = getAuthed(t, e, http.MethodGet, base+"?"+pql+"&module=not-a-uuid", cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed module + pql: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
}
