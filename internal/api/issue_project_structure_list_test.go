package api

// Batched issue project-structure tests (C21T2): the
// ?include_project_structure=1 flag on the issue LIST endpoint resolves
// the cycle/module/release membership for every returned row in ONE
// query — no per-row detail fetches. Real test database, no skips.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

type batchStructRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type batchStructRow struct {
	ID               string `json:"id"`
	ProjectStructure *struct {
		Cycle   *batchStructRef  `json:"cycle"`
		Modules []batchStructRef `json:"modules"`
		Release *batchStructRef  `json:"release"`
	} `json:"project_structure"`
}

func TestIssueListIncludeProjectStructure(t *testing.T) {
	pool := newTestPool(t)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterCycleRoutes(e, &IssueHandler{Pool: pool})
	RegisterModuleRoutes(e, &IssueHandler{Pool: pool})
	RegisterReleaseRoutes(e, &IssueHandler{Pool: pool})

	cookie := loginTestUser(t, e, pool, uniqueEmail("issue-structure-list"), "test-agent", uniqueIP())

	slug := uniqueSlug("issue-structure-list")
	createWorkspaceHTTP(t, e, cookie, "Structure Co", slug)
	ident := uniqueProjectIdentifier("SL")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	base := "/api/v1/workspaces/" + slug + "/projects/" + ident

	createIssue := func(body string) string {
		rec := postAuthedJSON(t, e, http.MethodPost, base+"/issues", cookie, body)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create issue: status = %d (body: %s)", rec.Code, rec.Body.String())
		}
		var issue struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &issue); err != nil {
			t.Fatalf("decode issue: %v", err)
		}
		return issue.ID
	}

	// Two issues in the working set: one with full membership, one bare.
	memberID := createIssue(`{"name":"Structured work"}`)
	bareID := createIssue(`{"name":"Bare work"}`)
	// One archived and one draft — both must stay out of the list.
	archivedID := createIssue(`{"name":"Archived work"}`)
	draftID := createIssue(`{"name":"Draft work","is_draft":true}`)

	rec := postAuthedJSON(t, e, http.MethodPatch, base+"/issues/"+archivedID, cookie, `{"archived":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("archive issue: status = %d (body: %s)", rec.Code, rec.Body.String())
	}

	// Two cycles: the batch must pick the first by NAME ("Sprint 1"),
	// mirroring the detail endpoint's LIMIT 1 ORDER BY name.
	// C78T0: the two cycles deliberately share a date range, so enable
	// the parallel-cycles toggle first (off by default since C78T0).
	rec = postAuthedJSON(t, e, http.MethodPatch, base, cookie, `{"parallel_cycles":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("enable parallel_cycles: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	start := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	end := time.Now().AddDate(0, 0, 13).Format("2006-01-02")
	var cycleIDs []string
	for _, name := range []string{"Sprint 2", "Sprint 1"} {
		rec = postAuthedJSON(t, e, http.MethodPost, base+"/cycles", cookie,
			`{"name":"`+name+`","start_date":"`+start+`","end_date":"`+end+`"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create cycle %s: status = %d (body: %s)", name, rec.Code, rec.Body.String())
		}
		var cyc struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &cyc); err != nil {
			t.Fatalf("decode cycle: %v", err)
		}
		cycleIDs = append(cycleIDs, cyc.ID)
	}
	// Two modules, created in reverse name order to prove the batch
	// orders by name like the detail endpoint does.
	var moduleIDs []string
	for _, name := range []string{"Zulu", "Alpha"} {
		rec = postAuthedJSON(t, e, http.MethodPost, base+"/modules", cookie, `{"name":"`+name+`"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create module %s: status = %d (body: %s)", name, rec.Code, rec.Body.String())
		}
		var mod struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &mod); err != nil {
			t.Fatalf("decode module: %v", err)
		}
		moduleIDs = append(moduleIDs, mod.ID)
	}
	rec = postAuthedJSON(t, e, http.MethodPost, base+"/releases", cookie, `{"name":"v0.4.0","status":"planned"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create release: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var release struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &release); err != nil {
		t.Fatalf("decode release: %v", err)
	}

	addTo := func(kind, id string) {
		rec = postAuthedJSON(t, e, http.MethodPost, base+"/"+kind+"/"+id+"/issues", cookie,
			`{"issue_ids":["`+memberID+`"]}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("add to %s %s: status = %d (body: %s)", kind, id, rec.Code, rec.Body.String())
		}
	}
	for _, id := range cycleIDs {
		addTo("cycles", id)
	}
	for _, id := range moduleIDs {
		addTo("modules", id)
	}
	rec = postAuthedJSON(t, e, http.MethodPatch, base+"/issues/"+memberID, cookie,
		`{"release_id":"`+release.ID+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch release_id: status = %d (body: %s)", rec.Code, rec.Body.String())
	}

	list := func(query string) []batchStructRow {
		rec = getAuthed(t, e, http.MethodGet, base+"/issues"+query, cookie)
		if rec.Code != http.StatusOK {
			t.Fatalf("list %s: status = %d (body: %s)", query, rec.Code, rec.Body.String())
		}
		var got struct {
			Results []batchStructRow `json:"results"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode list: %v", err)
		}
		return got.Results
	}
	byID := func(rows []batchStructRow) map[string]batchStructRow {
		m := map[string]batchStructRow{}
		for _, r := range rows {
			m[r.ID] = r
		}
		return m
	}

	// Flag on: every returned row carries project_structure in the SAME
	// shape as the detail endpoint; archived/draft issues are excluded.
	rows := byID(list("?include_project_structure=1&per_page=100"))
	if len(rows) != 2 {
		t.Fatalf("list rows = %d, want 2 (member + bare; archived/draft excluded)", len(rows))
	}
	if _, ok := rows[archivedID]; ok {
		t.Fatal("archived issue leaked into the list")
	}
	if _, ok := rows[draftID]; ok {
		t.Fatal("draft issue leaked into the list")
	}

	member, ok := rows[memberID]
	if !ok {
		t.Fatal("member issue missing from the list")
	}
	ps := member.ProjectStructure
	if ps == nil {
		t.Fatal("project_structure missing on the member row")
	}
	if ps.Cycle == nil || ps.Cycle.Name != "Sprint 1" {
		t.Fatalf("cycle = %+v, want the first-by-name Sprint 1", ps.Cycle)
	}
	if len(ps.Modules) != 2 || ps.Modules[0].Name != "Alpha" || ps.Modules[1].Name != "Zulu" {
		t.Fatalf("modules = %+v, want [Alpha Zulu] ordered by name", ps.Modules)
	}
	if ps.Release == nil || ps.Release.ID != release.ID || ps.Release.Name != "v0.4.0" {
		t.Fatalf("release = %+v, want v0.4.0", ps.Release)
	}

	bare, ok := rows[bareID]
	if !ok {
		t.Fatal("bare issue missing from the list")
	}
	bps := bare.ProjectStructure
	if bps == nil {
		t.Fatal("project_structure missing on the bare row (want {\"modules\":[]} with cycle/release omitted)")
	}
	if bps.Cycle != nil || bps.Release != nil {
		t.Fatalf("bare structure = %+v, want no cycle/release", bps)
	}
	if bps.Modules == nil || len(bps.Modules) != 0 {
		t.Fatalf("bare modules = %+v, want []", bps.Modules)
	}

	// Flag off: byte-identical shape to before — the key must not appear
	// anywhere in the body, and row count is unchanged.
	rec = getAuthed(t, e, http.MethodGet, base+"/issues?per_page=100", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list without flag: status = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "project_structure") {
		t.Fatalf("project_structure leaked without the flag: %s", rec.Body.String())
	}
	var plain struct {
		Results []batchStructRow `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &plain); err != nil {
		t.Fatalf("decode plain list: %v", err)
	}
	if len(plain.Results) != 2 {
		t.Fatalf("plain list rows = %d, want 2", len(plain.Results))
	}
	for _, r := range plain.Results {
		if r.ProjectStructure != nil {
			t.Fatalf("row %s carries project_structure without the flag", r.ID)
		}
	}
}
