package api

// Workflow transition controls HTTP tests (C68T0): GET/PUT
// /api/v1/workspaces/{slug}/projects/{identifier}/workflow, the member-15+
// gate on PUT, validation 400s, and the 409 state_transition_not_allowed
// envelope (with the allowed next-state IDs) on a denied PATCH move.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"
)

func workflowTestServer(t *testing.T, pool *pgxpool.Pool) *echo.Echo {
	t.Helper()
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterWorkflowRoutes(e, &WorkflowHandler{Pool: pool})
	return e
}

func workflowStatesByGroup(t *testing.T, e *echo.Echo, cookie *http.Cookie, statesURL string) map[string]string {
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
	m := map[string]string{}
	for _, s := range body.States {
		m[s.Group] = s.ID
	}
	return m
}

func TestWorkflowHTTP(t *testing.T) {
	pool := newTestPool(t)
	e := workflowTestServer(t, pool)

	cookie := loginTestUser(t, e, pool, uniqueEmail("wf-http"), "test-agent", uniqueIP())
	slug := uniqueSlug("wf-http")
	createWorkspaceHTTP(t, e, cookie, "WF Co", slug)
	ident := uniqueProjectIdentifier("WH")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	base := "/api/v1/workspaces/" + slug + "/projects/" + ident
	states := workflowStatesByGroup(t, e, cookie, base+"/states")

	// GET workflow: 200, states + empty transitions.
	rec := getAuthed(t, e, http.MethodGet, base+"/workflow", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET workflow: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var wf struct {
		States []struct {
			ID string `json:"id"`
		} `json:"states"`
		Transitions []struct {
			FromStateID string `json:"from_state_id"`
			ToStateID   string `json:"to_state_id"`
		} `json:"transitions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &wf); err != nil {
		t.Fatalf("decode workflow: %v", err)
	}
	if len(wf.States) != 5 || len(wf.Transitions) != 0 {
		t.Fatalf("workflow = %d states %d transitions, want 5/0", len(wf.States), len(wf.Transitions))
	}

	// PUT as guest (role 5) → 403.
	guestCookie := loginTestUser(t, e, pool, uniqueEmail("wf-http-guest"), "test-agent", uniqueIP())
	guestID := authedUserID(t, e, guestCookie)
	rec = postAuthedJSON(t, e, http.MethodPost, "/api/v1/workspaces/"+slug+"/members", cookie,
		fmt.Sprintf(`{"user_id":%q,"role":5}`, guestID))
	if rec.Code != http.StatusOK {
		t.Fatalf("add guest: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodPut, base+"/workflow", guestCookie,
		fmt.Sprintf(`{"transitions":[{"from_state_id":%q,"to_state_id":%q}]}`,
			states["unstarted"], states["started"]))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("PUT as guest: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}
	// Guest CAN read.
	rec = getAuthed(t, e, http.MethodGet, base+"/workflow", guestCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET as guest: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	// PUT self-loop → 400.
	rec = postAuthedJSON(t, e, http.MethodPut, base+"/workflow", cookie,
		fmt.Sprintf(`{"transitions":[{"from_state_id":%q,"to_state_id":%q}]}`,
			states["unstarted"], states["unstarted"]))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT self-loop: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	// PUT unknown state → 400.
	rec = postAuthedJSON(t, e, http.MethodPut, base+"/workflow", cookie,
		`{"transitions":[{"from_state_id":"00000000-0000-0000-0000-000000000000","to_state_id":"00000000-0000-0000-0000-000000000001"}]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT unknown state: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}

	// PUT valid rules → 200 with the transitions echoed.
	rec = postAuthedJSON(t, e, http.MethodPut, base+"/workflow", cookie,
		fmt.Sprintf(`{"transitions":[
			{"from_state_id":%q,"to_state_id":%q},
			{"from_state_id":%q,"to_state_id":%q}]}`,
			states["backlog"], states["unstarted"],
			states["unstarted"], states["started"]))
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT rules: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var put struct {
		Transitions []struct {
			FromStateID string `json:"from_state_id"`
			ToStateID   string `json:"to_state_id"`
		} `json:"transitions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &put); err != nil {
		t.Fatalf("decode PUT: %v", err)
	}
	if len(put.Transitions) != 2 {
		t.Fatalf("PUT transitions = %d, want 2", len(put.Transitions))
	}

	// Denied PATCH move → 409 state_transition_not_allowed + allowed list.
	issueID := createIssueHTTP(t, e, cookie, base+"/issues", "governed-http")
	rec = postAuthedJSON(t, e, http.MethodPatch, base+"/issues/"+issueID, cookie,
		fmt.Sprintf(`{"state_id":%q}`, states["unstarted"]))
	if rec.Code != http.StatusOK {
		t.Fatalf("position in todo: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodPatch, base+"/issues/"+issueID, cookie,
		fmt.Sprintf(`{"state_id":%q}`, states["completed"]))
	if rec.Code != http.StatusConflict {
		t.Fatalf("denied move: status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"code":"state_transition_not_allowed"`) {
		t.Fatalf("409 envelope missing state_transition_not_allowed code: %s", body)
	}
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Details struct {
				Allowed []string `json:"allowed"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode 409: %v", err)
	}
	if len(envelope.Error.Details.Allowed) != 1 || envelope.Error.Details.Allowed[0] != states["started"] {
		t.Fatalf("allowed = %v, want [started %s]", envelope.Error.Details.Allowed, states["started"])
	}

	// Allowed PATCH move → 200.
	rec = postAuthedJSON(t, e, http.MethodPatch, base+"/issues/"+issueID, cookie,
		fmt.Sprintf(`{"state_id":%q}`, states["started"]))
	if rec.Code != http.StatusOK {
		t.Fatalf("allowed move: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
}

// TestWorkflowOpenAPIPin: the GET/PUT workflow response envelopes carry
// exactly the documented top-level keys (states, transitions).
func TestWorkflowOpenAPIPin(t *testing.T) {
	pool := newTestPool(t)
	e := workflowTestServer(t, pool)

	cookie := loginTestUser(t, e, pool, uniqueEmail("wf-pin"), "test-agent", uniqueIP())
	slug := uniqueSlug("wf-pin")
	createWorkspaceHTTP(t, e, cookie, "WF Pin Co", slug)
	ident := uniqueProjectIdentifier("WP")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	base := "/api/v1/workspaces/" + slug + "/projects/" + ident

	topKeys := func(rec *httptest.ResponseRecorder) map[string]json.RawMessage {
		t.Helper()
		var m map[string]json.RawMessage
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
			t.Fatalf("decode body %s: %v", rec.Body.String(), err)
		}
		return m
	}

	rec := getAuthed(t, e, http.MethodGet, base+"/workflow", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	keys := topKeys(rec)
	if len(keys) != 2 || keys["states"] == nil || keys["transitions"] == nil {
		t.Fatalf("GET keys = %v, want exactly [states transitions]", keys)
	}

	states := workflowStatesByGroup(t, e, cookie, base+"/states")
	rec = postAuthedJSON(t, e, http.MethodPut, base+"/workflow", cookie,
		fmt.Sprintf(`{"transitions":[{"from_state_id":%q,"to_state_id":%q}]}`,
			states["unstarted"], states["started"]))
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	keys = topKeys(rec)
	if len(keys) != 2 || keys["states"] == nil || keys["transitions"] == nil {
		t.Fatalf("PUT keys = %v, want exactly [states transitions]", keys)
	}
	// Transitions are never null — an empty rule set serializes as [].
	var shape struct {
		Transitions []struct {
			FromStateID string `json:"from_state_id"`
			ToStateID   string `json:"to_state_id"`
		} `json:"transitions"`
	}
	rec = postAuthedJSON(t, e, http.MethodPut, base+"/workflow", cookie, `{"transitions":[]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT clear: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &shape); err != nil {
		t.Fatalf("decode cleared: %v", err)
	}
	if shape.Transitions == nil {
		t.Fatal("transitions is null after clear, want []")
	}
}
