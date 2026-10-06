package api

// Satellite HTTP endpoint tests (Task 18): comment create/list over HTTP,
// vote toggle, relation create + reverse derivation through the API,
// history, versions + restore, and the spec §5 error envelope on a
// missing issue. Real test database, no skips.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"
)

func testSatelliteServer(t *testing.T, pool *pgxpool.Pool) *echo.Echo {
	t.Helper()
	e := testIssueServer(t, pool)
	RegisterSatelliteRoutes(e, &IssueHandler{Pool: pool})
	return e
}

func TestSatelliteHTTPEndpoints(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testSatelliteServer(t, pool)

	cookie := loginTestUser(t, e, pool, uniqueEmail("sat-http"), "test-agent", uniqueIP())
	slug := uniqueSlug("sat-http")
	createWorkspaceHTTP(t, e, cookie, "Satellite Co", slug)
	ident := uniqueProjectIdentifier("SAT")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	base := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/issues"

	// Two issues for the relation test.
	rec := postAuthedJSON(t, e, http.MethodPost, base, cookie, `{"name":"Alpha"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create Alpha: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var alpha struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &alpha); err != nil {
		t.Fatalf("decode Alpha: %v", err)
	}
	rec = postAuthedJSON(t, e, http.MethodPost, base, cookie, `{"name":"Beta"}`)
	var beta struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &beta); err != nil {
		t.Fatalf("decode Beta: %v", err)
	}
	aBase := base + "/" + alpha.ID

	// Comment create → 201; list nests the reply.
	rec = postAuthedJSON(t, e, http.MethodPost, aBase+"/comments", cookie,
		`{"content":{"type":"doc","content":[]}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create comment: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var top struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &top); err != nil {
		t.Fatalf("decode comment: %v", err)
	}
	rec = postAuthedJSON(t, e, http.MethodPost, aBase+"/comments", cookie,
		`{"content":{"type":"doc"},"parent_id":"`+top.ID+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create reply: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, aBase+"/comments", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list comments: status = %d", rec.Code)
	}
	var comments struct {
		Comments []struct {
			ID      string `json:"id"`
			Replies []struct {
				ID string `json:"id"`
			} `json:"replies"`
		} `json:"comments"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &comments); err != nil {
		t.Fatalf("decode comments: %v", err)
	}
	if len(comments.Comments) != 1 || len(comments.Comments[0].Replies) != 1 {
		t.Fatalf("comment tree = %+v, want 1 top-level with 1 reply", comments.Comments)
	}

	// Vote → 204; GET votes shows count 1 voted true.
	rec = postAuthedJSON(t, e, http.MethodPost, aBase+"/votes", cookie, `{}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("vote: status = %d, want 204", rec.Code)
	}
	rec = getAuthed(t, e, http.MethodGet, aBase+"/votes", cookie)
	var votes struct {
		Count int  `json:"count"`
		Voted bool `json:"voted"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &votes); err != nil {
		t.Fatalf("decode votes: %v", err)
	}
	if votes.Count != 1 || !votes.Voted {
		t.Fatalf("votes = %+v, want count 1 voted true", votes)
	}

	// Relation: Alpha blocked_by Beta → Beta's side shows blocking.
	rec = postAuthedJSON(t, e, http.MethodPost, aBase+"/relations", cookie,
		`{"related_issue_id":"`+beta.ID+`","type":"blocked_by"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("create relation: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, base+"/"+beta.ID+"/relations", cookie)
	var rels struct {
		Relations []struct {
			Type      string `json:"type"`
			Direction string `json:"direction"`
		} `json:"relations"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &rels); err != nil {
		t.Fatalf("decode relations: %v", err)
	}
	if len(rels.Relations) != 1 || rels.Relations[0].Type != "blocking" || rels.Relations[0].Direction != "incoming" {
		t.Fatalf("Beta relations = %+v, want one incoming blocking", rels.Relations)
	}

	// History → _created first, chronological.
	rec = getAuthed(t, e, http.MethodGet, aBase+"/history", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("history: status = %d", rec.Code)
	}
	var hist struct {
		History []struct {
			Field string `json:"field"`
			Actor struct {
				Email string `json:"email"`
			} `json:"actor"`
		} `json:"history"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &hist); err != nil {
		t.Fatalf("decode history: %v", err)
	}
	if len(hist.History) == 0 || hist.History[0].Field != "_created" {
		t.Fatalf("history = %+v, want _created first", hist.History)
	}
	if hist.History[0].Actor.Email == "" {
		t.Fatal("history actor has no email")
	}

	// Versions: create → v1; patch → v2; restore v1 → 200 and a v3.
	rec = getAuthed(t, e, http.MethodGet, aBase+"/versions", cookie)
	var vers struct {
		Versions []struct {
			VersionNo int `json:"version_no"`
		} `json:"versions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &vers); err != nil {
		t.Fatalf("decode versions: %v", err)
	}
	if len(vers.Versions) != 1 || vers.Versions[0].VersionNo != 1 {
		t.Fatalf("versions = %+v, want [v1]", vers.Versions)
	}
	rec = postAuthedJSON(t, e, http.MethodPatch, aBase, cookie, `{"name":"Alpha renamed"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: status = %d", rec.Code)
	}
	rec = postAuthedJSON(t, e, http.MethodPost, aBase+"/versions/1/restore", cookie, `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("restore: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var restored struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &restored); err != nil {
		t.Fatalf("decode restored: %v", err)
	}
	if restored.Name != "Alpha" {
		t.Fatalf("restored name = %q, want Alpha", restored.Name)
	}
	rec = getAuthed(t, e, http.MethodGet, aBase+"/versions", cookie)
	vers.Versions = nil
	if err := json.Unmarshal(rec.Body.Bytes(), &vers); err != nil {
		t.Fatalf("decode versions after restore: %v", err)
	}
	if len(vers.Versions) != 3 {
		t.Fatalf("versions after restore = %d, want 3", len(vers.Versions))
	}

	// Missing issue → 404 with the spec §5 envelope.
	rec = getAuthed(t, e, http.MethodGet, base+"/00000000-0000-0000-0000-000000000000/comments", cookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("bogus issue comments: status = %d, want 404", rec.Code)
	}
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if env.Error.Code != "not_found" || !strings.Contains(env.Error.Message, "issue not found") {
		t.Fatalf("envelope = %+v, want not_found / issue not found", env.Error)
	}
}
