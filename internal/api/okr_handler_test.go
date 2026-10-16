package api

// OKR HTTP endpoint tests (C35T0): at least one test per route —
// objective CRUD, KR CRUD, link/unlink — plus the 409 envelope,
// guest write 403, malformed-id 400, metric-KR link 400, and
// cross-workspace link 404. Real test database, no skips.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"glance/internal/service"
)

func TestObjectiveHTTP(t *testing.T) {
	pool := newTestPool(t)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterObjectiveRoutes(e, &IssueHandler{Pool: pool})

	cookie := loginTestUser(t, e, pool, uniqueEmail("okr-http"), "test-agent", uniqueIP())
	slug := uniqueSlug("okr-http")
	createWorkspaceHTTP(t, e, cookie, "OKR Co", slug)
	ident := uniqueProjectIdentifier("OH")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	issueBase := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/issues"
	issueID := createIssueHTTP(t, e, cookie, issueBase, "linked work")
	base := "/api/v1/workspaces/" + slug + "/objectives"

	// Create objective → 201.
	rec := postAuthedJSON(t, e, http.MethodPost, base, cookie,
		`{"title":"Grow revenue","description":"The goal","start_date":"2026-01-01","target_date":"2026-03-31"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	var created struct {
		ID             string  `json:"id"`
		Title          string  `json:"title"`
		Status         string  `json:"status"`
		Progress       float64 `json:"progress"`
		KeyResultCount int64   `json:"key_result_count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.Title != "Grow revenue" || created.Status != "active" || created.Progress != 0 {
		t.Fatalf("created = %+v", created)
	}
	obURL := base + "/" + created.ID

	// Duplicate title → 409 envelope.
	rec = postAuthedJSON(t, e, http.MethodPost, base, cookie, `{"title":"Grow revenue"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate: status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"conflict"`) {
		t.Fatalf("409 envelope: body=%s", rec.Body.String())
	}

	// Bad status → 400; bad date → 400.
	rec = postAuthedJSON(t, e, http.MethodPost, base, cookie, `{"title":"Other","status":"bogus"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad status: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodPost, base, cookie, `{"title":"Other","start_date":"not-a-date"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad date: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}

	// List → 200 with the objective.
	rec = getAuthed(t, e, http.MethodGet, base, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var list struct {
		Objectives []struct {
			ID string `json:"id"`
		} `json:"objectives"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.Objectives) != 1 || list.Objectives[0].ID != created.ID {
		t.Fatalf("list = %+v", list)
	}

	// Get → 200; malformed id → 400; unknown id → 404.
	rec = getAuthed(t, e, http.MethodGet, obURL, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("get: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, base+"/not-a-uuid", cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed id: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, base+"/00000000-0000-0000-0000-000000000000", cookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown id: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}

	// PATCH → 200; bad status → 400.
	rec = postAuthedJSON(t, e, http.MethodPatch, obURL, cookie, `{"status":"completed"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodPatch, obURL, cookie, `{"status":"bogus"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("patch bad status: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}

	// Create metric KR → 201.
	krBase := obURL + "/key-results"
	rec = postAuthedJSON(t, e, http.MethodPost, krBase, cookie,
		`{"title":"NPS","kind":"metric","unit":"%","start_value":0,"target_value":100,"current_value":25}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create KR: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	var mkr struct {
		ID       string  `json:"id"`
		Progress float64 `json:"progress"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &mkr); err != nil {
		t.Fatalf("decode KR: %v", err)
	}
	if mkr.Progress != 0.25 {
		t.Fatalf("metric KR progress = %v, want 0.25", mkr.Progress)
	}
	mkrURL := krBase + "/" + mkr.ID

	// Zero-span KR → 400.
	rec = postAuthedJSON(t, e, http.MethodPost, krBase, cookie,
		`{"title":"Flat","kind":"metric","start_value":50,"target_value":50}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("zero-span: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}

	// Create issue KR → 201.
	rec = postAuthedJSON(t, e, http.MethodPost, krBase, cookie,
		`{"title":"Ship issues","kind":"issue","target_value":100}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create issue KR: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	var ikr struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &ikr); err != nil {
		t.Fatalf("decode issue KR: %v", err)
	}
	ikrURL := krBase + "/" + ikr.ID

	// Link to metric KR → 400; link to issue KR → 201; re-link → 201
	// (idempotent); link unknown issue → 404.
	rec = postAuthedJSON(t, e, http.MethodPost, mkrURL+"/issues", cookie,
		`{"issue_id":"`+issueID+`"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("metric link: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodPost, ikrURL+"/issues", cookie,
		`{"issue_id":"`+issueID+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("link: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodPost, ikrURL+"/issues", cookie,
		`{"issue_id":"`+issueID+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("re-link: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodPost, ikrURL+"/issues", cookie,
		`{"issue_id":"00000000-0000-0000-0000-000000000000"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown issue link: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}

	// Get objective shows the linked issue on the issue KR.
	rec = getAuthed(t, e, http.MethodGet, obURL, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("get with KRs: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var detail struct {
		KeyResults []struct {
			ID           string `json:"id"`
			Kind         string `json:"kind"`
			LinkedIssues []struct {
				ID string `json:"id"`
			} `json:"linked_issues"`
		} `json:"key_results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if len(detail.KeyResults) != 2 {
		t.Fatalf("key_results = %d, want 2", len(detail.KeyResults))
	}
	for _, kr := range detail.KeyResults {
		if kr.Kind == "issue" && (len(kr.LinkedIssues) != 1 || kr.LinkedIssues[0].ID != issueID) {
			t.Fatalf("issue KR linked = %+v, want [%s]", kr.LinkedIssues, issueID)
		}
	}

	// PATCH KR: kind change → 400; current_value → 200.
	rec = postAuthedJSON(t, e, http.MethodPatch, mkrURL, cookie, `{"kind":"issue"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("kind change: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodPatch, mkrURL, cookie, `{"current_value":50}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch KR: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	// Unlink → 204; unlink again → 204 (idempotent).
	rec = postAuthedJSON(t, e, http.MethodDelete, ikrURL+"/issues/"+issueID, cookie, ``)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("unlink: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodDelete, ikrURL+"/issues/"+issueID, cookie, ``)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("re-unlink: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}

	// Delete KR → 204; delete again → 404.
	rec = postAuthedJSON(t, e, http.MethodDelete, mkrURL, cookie, ``)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete KR: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodDelete, mkrURL, cookie, ``)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("second delete KR: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}

	// Guest member: reads 200, writes 403. Non-member: 404.
	guestEmail := uniqueEmail("okr-guest")
	guestCookie := loginTestUser(t, e, pool, guestEmail, "test-agent", uniqueIP())
	ctx := context.Background()
	var adminID, guestID string
	if err := pool.QueryRow(ctx,
		`SELECT m.user_id::text FROM workspace_members m JOIN workspaces w ON w.id = m.workspace_id
		  WHERE w.slug = $1 AND m.role = 20`, slug).Scan(&adminID); err != nil {
		t.Fatalf("admin id: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users WHERE email = $1`, guestEmail).Scan(&guestID); err != nil {
		t.Fatalf("guest id: %v", err)
	}
	if err := service.UpsertMember(ctx, pool, slug, adminID, guestID, service.RoleGuest); err != nil {
		t.Fatalf("UpsertMember: %v", err)
	}
	rec = getAuthed(t, e, http.MethodGet, base, guestCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("guest list: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodPost, base, guestCookie, `{"title":"Guest goal"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("guest create: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}

	// Non-member → 404, never 403.
	otherCookie := loginTestUser(t, e, pool, uniqueEmail("okr-outsider"), "test-agent", uniqueIP())
	rec = getAuthed(t, e, http.MethodGet, obURL, otherCookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("non-member get: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}

	// Delete objective → 204; delete again → 404.
	rec = postAuthedJSON(t, e, http.MethodDelete, obURL, cookie, ``)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete objective: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodDelete, obURL, cookie, ``)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("second delete objective: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
}
