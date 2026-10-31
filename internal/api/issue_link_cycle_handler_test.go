package api

// Blocks-cycle guard over HTTP (C94T0): closing a 'blocks' loop
// answers 409 with code issue_link_cycle and the full display-ID path
// in details.path; a non-closing edge still answers 201.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestIssueLinkCycleHTTP(t *testing.T) {
	pool := newTestPool(t)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterIssueLinkRoutes(e, &IssueHandler{Pool: pool})

	cookie := loginTestUser(t, e, pool, uniqueEmail("link-cycle-http"), "test-agent", uniqueIP())
	slug := uniqueSlug("link-cycle-http")
	createWorkspaceHTTP(t, e, cookie, "Link Co", slug)
	ident := uniqueProjectIdentifier("LC")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	issueBase := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/issues"
	issA := createIssueHTTP(t, e, cookie, issueBase, "A")
	issB := createIssueHTTP(t, e, cookie, issueBase, "B")

	// Display IDs come from the issue detail read.
	displayOf := func(id string) string {
		t.Helper()
		rec := getAuthed(t, e, http.MethodGet, issueBase+"/"+id, cookie)
		if rec.Code != http.StatusOK {
			t.Fatalf("get issue %s: status = %d (body: %s)", id, rec.Code, rec.Body.String())
		}
		var iss struct {
			DisplayID string `json:"display_id"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &iss); err != nil {
			t.Fatalf("decode issue: %v", err)
		}
		if iss.DisplayID == "" {
			t.Fatalf("issue %s has no display_id: %s", id, rec.Body.String())
		}
		return iss.DisplayID
	}
	dispA, dispB := displayOf(issA), displayOf(issB)

	// A→B blocks: 201.
	rec := postAuthedJSON(t, e, http.MethodPost, issueBase+"/"+issA+"/links", cookie,
		`{"target_issue_id":"`+issB+`","kind":"blocks"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create a->b: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}

	// B→A blocks closes the cycle: 409 issue_link_cycle with the full
	// path B → A → B in details.
	rec = postAuthedJSON(t, e, http.MethodPost, issueBase+"/"+issB+"/links", cookie,
		`{"target_issue_id":"`+issA+`","kind":"blocks"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("closing b->a: status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"issue_link_cycle"`) {
		t.Fatalf("409 envelope missing issue_link_cycle code: %s", rec.Body.String())
	}
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Details struct {
				Path []string `json:"path"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode 409 envelope: %v", err)
	}
	want := []string{dispB, dispA, dispB}
	if len(env.Error.Details.Path) != len(want) {
		t.Fatalf("details.path = %v, want %v", env.Error.Details.Path, want)
	}
	for i := range want {
		if env.Error.Details.Path[i] != want[i] {
			t.Fatalf("details.path = %v, want %v", env.Error.Details.Path, want)
		}
	}
	if !strings.Contains(env.Error.Message, dispB) || !strings.Contains(env.Error.Message, dispA) {
		t.Fatalf("message %q must name the cycle path", env.Error.Message)
	}

	// A non-closing edge is unaffected: a third issue linked from B
	// still creates fine.
	issC := createIssueHTTP(t, e, cookie, issueBase, "C")
	rec = postAuthedJSON(t, e, http.MethodPost, issueBase+"/"+issB+"/links", cookie,
		`{"target_issue_id":"`+issC+`","kind":"blocks"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("non-closing b->c: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
}
