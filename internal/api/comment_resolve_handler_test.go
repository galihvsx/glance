package api

// Comment resolve threads (C32T0) HTTP tests:
// POST .../comments/{id}/resolve and .../reopen (200 Comment),
// the ?resolved=all|open|resolved list filter, and the HTTP error
// mapping: reply → 400, unknown comment → 404, guest member → 403,
// non-member → 404, non-author member (15+) may resolve. Real test
// database, no skips.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

type resolveCommentJSON struct {
	ID         string  `json:"id"`
	ResolvedAt *string `json:"resolved_at"`
	ResolvedBy *struct {
		ID string `json:"id"`
	} `json:"resolved_by"`
}

func decodeResolveComment(t *testing.T, recBody []byte) resolveCommentJSON {
	t.Helper()
	var c resolveCommentJSON
	if err := json.Unmarshal(recBody, &c); err != nil {
		t.Fatalf("decode comment: %v (body: %.300s)", err, recBody)
	}
	return c
}

func TestCommentResolveHTTPEndpoints(t *testing.T) {
	pool := newTestPool(t)
	e := testSatelliteServer(t, pool)
	ctx := context.Background()

	adminEmail := uniqueEmail("cres-admin")
	cookie := loginTestUser(t, e, pool, adminEmail, "test-agent", uniqueIP())
	slug := uniqueSlug("cres")
	createWorkspaceHTTP(t, e, cookie, "Resolve Co", slug)
	ident := uniqueProjectIdentifier("CR")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	base := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/issues"

	rec := postAuthedJSON(t, e, http.MethodPost, base, cookie, `{"name":"Resolve host"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create issue: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var issue struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &issue); err != nil {
		t.Fatalf("decode issue: %v", err)
	}
	commentsBase := base + "/" + issue.ID + "/comments"

	var adminID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users WHERE email = $1`, adminEmail).Scan(&adminID); err != nil {
		t.Fatalf("admin id: %v", err)
	}

	// Root comment + reply.
	rec = postAuthedJSON(t, e, http.MethodPost, commentsBase, cookie, `{"content":{"type":"doc","content":[]}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create root: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	root := decodeResolveComment(t, rec.Body.Bytes())
	if root.ResolvedAt != nil || root.ResolvedBy != nil {
		t.Fatalf("new comment resolved_at = %v resolved_by = %v, want nil/nil", root.ResolvedAt, root.ResolvedBy)
	}
	rec = postAuthedJSON(t, e, http.MethodPost, commentsBase, cookie, `{"content":{"type":"doc"},"parent_id":"`+root.ID+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create reply: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	reply := decodeResolveComment(t, rec.Body.Bytes())

	// Resolve on a reply → 400.
	rec = postAuthedJSON(t, e, http.MethodPost, commentsBase+"/"+reply.ID+"/resolve", cookie, ``)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("resolve reply: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}

	// Unknown comment → 404.
	rec = postAuthedJSON(t, e, http.MethodPost, commentsBase+"/00000000-0000-0000-0000-000000000000/resolve", cookie, ``)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("resolve unknown: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}

	// Resolve the root → 200, resolved_by is the caller.
	rec = postAuthedJSON(t, e, http.MethodPost, commentsBase+"/"+root.ID+"/resolve", cookie, ``)
	if rec.Code != http.StatusOK {
		t.Fatalf("resolve root: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	resolved := decodeResolveComment(t, rec.Body.Bytes())
	if resolved.ResolvedAt == nil || *resolved.ResolvedAt == "" {
		t.Fatal("resolved_at empty after resolve")
	}
	if resolved.ResolvedBy == nil || resolved.ResolvedBy.ID != adminID {
		t.Fatalf("resolved_by = %+v, want admin %s", resolved.ResolvedBy, adminID)
	}

	// List filter: resolved → the root (with its reply); open → none.
	rec = getAuthed(t, e, http.MethodGet, commentsBase+"?resolved=resolved", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list resolved: status = %d", rec.Code)
	}
	var listed struct {
		Comments []struct {
			ID      string `json:"id"`
			Replies []struct {
				ID string `json:"id"`
			} `json:"replies"`
		} `json:"comments"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list resolved: %v", err)
	}
	if len(listed.Comments) != 1 || listed.Comments[0].ID != root.ID {
		t.Fatalf("list resolved = %v, want [%s]", listed.Comments, root.ID)
	}
	if len(listed.Comments[0].Replies) != 1 || listed.Comments[0].Replies[0].ID != reply.ID {
		t.Fatalf("list resolved replies = %v, want [%s]", listed.Comments[0].Replies, reply.ID)
	}
	rec = getAuthed(t, e, http.MethodGet, commentsBase+"?resolved=open", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list open: status = %d", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list open: %v", err)
	}
	if len(listed.Comments) != 0 {
		t.Fatalf("list open = %d roots, want 0", len(listed.Comments))
	}

	// Invalid filter value → 400.
	rec = getAuthed(t, e, http.MethodGet, commentsBase+"?resolved=bogus", cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("list bogus filter: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}

	// Guest member (5) → 403; non-member → 404.
	guestCookie := addWorkspaceMemberWithRole(t, e, pool, slug, "cres-guest", 5)
	rec = postAuthedJSON(t, e, http.MethodPost, commentsBase+"/"+root.ID+"/reopen", guestCookie, ``)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("guest reopen: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}
	outsiderCookie := loginTestUser(t, e, pool, uniqueEmail("cres-out"), "test-agent", uniqueIP())
	rec = postAuthedJSON(t, e, http.MethodPost, commentsBase+"/"+root.ID+"/reopen", outsiderCookie, ``)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("non-member reopen: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}

	// Non-author member (15+) reopens → 200, resolution cleared.
	memberCookie := addWorkspaceMemberWithRole(t, e, pool, slug, "cres-member", 15)
	rec = postAuthedJSON(t, e, http.MethodPost, commentsBase+"/"+root.ID+"/reopen", memberCookie, ``)
	if rec.Code != http.StatusOK {
		t.Fatalf("member reopen: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	reopened := decodeResolveComment(t, rec.Body.Bytes())
	if reopened.ResolvedAt != nil || reopened.ResolvedBy != nil {
		t.Fatalf("reopened resolved_at = %v resolved_by = %v, want nil/nil", reopened.ResolvedAt, reopened.ResolvedBy)
	}
}
