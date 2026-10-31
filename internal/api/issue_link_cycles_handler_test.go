package api

// Cycle-report HTTP endpoint tests (C94T1): GET
// /api/v1/workspaces/{slug}/projects/{identifier}/links/cycles through
// the routes — one 3-cycle reported as a closed display-ID ring, a
// clean project returning {"cycles":[]}, and a non-member getting 404.
// Real test database, no skips.

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"
)

func getIssueDisplayIDHTTP(t *testing.T, e *echo.Echo, cookie *http.Cookie, issueBase, issueID string) string {
	t.Helper()
	rec := getAuthed(t, e, http.MethodGet, issueBase+"/"+issueID, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("get issue %s: status = %d (body: %s)", issueID, rec.Code, rec.Body.String())
	}
	var iss struct {
		DisplayID string `json:"display_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &iss); err != nil {
		t.Fatalf("decode issue: %v", err)
	}
	if iss.DisplayID == "" {
		t.Fatalf("issue %s has no display_id: %s", issueID, rec.Body.String())
	}
	return iss.DisplayID
}

func TestIssueLinkCyclesHTTP(t *testing.T) {
	pool := newTestPool(t)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterIssueLinkRoutes(e, &IssueHandler{Pool: pool})

	cookie := loginTestUser(t, e, pool, uniqueEmail("link-cycles"), "test-agent", uniqueIP())
	slug := uniqueSlug("link-cycles")
	createWorkspaceHTTP(t, e, cookie, "Link Co", slug)
	ident := uniqueProjectIdentifier("LC")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	issueBase := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/issues"
	cyclesURL := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/links/cycles"

	// Clean project → {"cycles":[]}, not null.
	rec := getAuthed(t, e, http.MethodGet, cyclesURL, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("clean cycles: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"cycles":[]`) {
		t.Fatalf("clean cycles body = %s, want {\"cycles\":[]}", rec.Body.String())
	}

	// Build a 3-cycle A → B → C → A via direct INSERTs (coordinator
	// fixup on rebase over C94T0: the guard would reject the closing
	// edge via the link endpoint; these rows simulate pre-existing data).
	issA := createIssueHTTP(t, e, cookie, issueBase, "A")
	issB := createIssueHTTP(t, e, cookie, issueBase, "B")
	issC := createIssueHTTP(t, e, cookie, issueBase, "C")
	ctx := context.Background()
	for _, link := range [][2]string{{issA, issB}, {issB, issC}, {issC, issA}} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO issue_links (issue_id, target_issue_id, kind)
			 VALUES ($1::uuid, $2::uuid, 'blocks')`, link[0], link[1]); err != nil {
			t.Fatalf("insert link %s->%s: %v", link[0], link[1], err)
		}
	}

	rec = getAuthed(t, e, http.MethodGet, cyclesURL, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("cycles: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var got struct {
		Cycles [][]string `json:"cycles"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode cycles: %v", err)
	}
	dA := getIssueDisplayIDHTTP(t, e, cookie, issueBase, issA)
	dB := getIssueDisplayIDHTTP(t, e, cookie, issueBase, issB)
	dC := getIssueDisplayIDHTTP(t, e, cookie, issueBase, issC)
	want := [][]string{{dA, dB, dC, dA}}
	if !reflect.DeepEqual(got.Cycles, want) {
		t.Fatalf("cycles = %v, want %v", got.Cycles, want)
	}

	// Non-member → 404 (tenancy, same as other satellite reads).
	outsider := loginTestUser(t, e, pool, uniqueEmail("link-cycles-out"), "test-agent", uniqueIP())
	rec = getAuthed(t, e, http.MethodGet, cyclesURL, outsider)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("outsider cycles: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"not_found"`) {
		t.Fatalf("outsider 404 envelope: body=%s", rec.Body.String())
	}
}
