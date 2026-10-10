package api

// Blocker guard HTTP tests (C16T3): PATCH an issue into a completed
// state while it has an open blocker → 409 with code "open_blockers"
// and details naming the blocker display IDs; ignore_blockers=true
// bypasses.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"
)

func completedStateIDHTTP(t *testing.T, e *echo.Echo, cookie *http.Cookie, statesURL string) string {
	t.Helper()
	rec := getAuthed(t, e, http.MethodGet, statesURL, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list states: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var body struct {
		States []struct {
			ID    string `json:"id"`
			Group string `json:"group"`
		} `json:"states"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode states: %v", err)
	}
	for _, s := range body.States {
		if s.Group == "completed" {
			return s.ID
		}
	}
	t.Fatal("no completed state seeded")
	return ""
}

func TestIssueBlockerGuardHTTP(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterIssueLinkRoutes(e, &IssueHandler{Pool: pool})
	RegisterStateRoutes(e, &StateHandler{Pool: pool})

	cookie := loginTestUser(t, e, pool, uniqueEmail("block-http"), "test-agent", uniqueIP())
	slug := uniqueSlug("block-http")
	createWorkspaceHTTP(t, e, cookie, "Block Co", slug)
	ident := uniqueProjectIdentifier("BH")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	issueBase := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/issues"
	issA := createIssueHTTP(t, e, cookie, issueBase, "blocked")
	issB := createIssueHTTP(t, e, cookie, issueBase, "blocker")
	completed := completedStateIDHTTP(t, e, cookie,
		"/api/v1/workspaces/"+slug+"/projects/"+ident+"/states")

	// B blocks A (inbound edge on A).
	rec := postAuthedJSON(t, e, http.MethodPost,
		issueBase+"/"+issB+"/links", cookie,
		`{"target_issue_id":"`+issA+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create link: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}

	// A → completed → 409 open_blockers naming B's display ID.
	rec = postAuthedJSON(t, e, http.MethodPatch,
		issueBase+"/"+issA, cookie,
		`{"state_id":"`+completed+`"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("guard: status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"code":"open_blockers"`) {
		t.Fatalf("409 envelope missing open_blockers code: %s", body)
	}
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Details struct {
				Blockers []string `json:"blockers"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if len(envelope.Error.Details.Blockers) != 1 {
		t.Fatalf("blockers = %v, want exactly 1", envelope.Error.Details.Blockers)
	}
	if !strings.HasPrefix(envelope.Error.Details.Blockers[0], ident+"-") {
		t.Fatalf("blocker = %q, want display ID %s-N", envelope.Error.Details.Blockers[0], ident)
	}

	// ignore_blockers=true → 200, the issue completes.
	rec = postAuthedJSON(t, e, http.MethodPatch,
		issueBase+"/"+issA, cookie,
		`{"state_id":"`+completed+`","ignore_blockers":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("ignore_blockers: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var moved struct {
		StateID string `json:"state_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &moved); err != nil {
		t.Fatalf("decode issue: %v", err)
	}
	if moved.StateID != completed {
		t.Fatalf("state_id = %s, want completed", moved.StateID)
	}
}
