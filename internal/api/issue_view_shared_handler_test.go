package api

// Shared saved-view HTTP endpoints (C10T1): PATCH {shared} owner-only
// (403 for a non-owner member), GET list annotating shared views with
// owner_id/owner_name, DELETE letting a workspace admin remove any shared
// view (moderation rule) while a plain member gets 403. Real test
// database, no skips.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// sharedHTTPFixture extends the view fixture with a member (B) and a
// second workspace admin (C). cookieA from the base fixture is the
// workspace creator, i.e. an admin already.
func setupSharedHTTPFixture(t *testing.T) (*viewHTTPFixture, *http.Cookie, *http.Cookie) {
	t.Helper()
	fx := setupViewHTTPFixture(t)
	cookieB := fx.cookieB
	cookieC := loginTestUser(t, fx.e, fx.pool, uniqueEmail("vw-c"), "test-agent", uniqueIP())
	userID := func(c *http.Cookie) string { return authedUserID(t, fx.e, c) }
	for _, tc := range []struct {
		cookie *http.Cookie
		role   int
	}{{cookieB, 15}, {cookieC, 20}} {
		if _, err := fx.pool.Exec(context.Background(),
			`INSERT INTO workspace_members (workspace_id, user_id, role)
			 SELECT w.id, $2::uuid, $3 FROM workspaces w
			 JOIN projects p ON p.workspace_id = w.id
			 WHERE p.id = $1::uuid
			 ON CONFLICT (workspace_id, user_id) DO UPDATE SET role = EXCLUDED.role`,
			fx.projectID, userID(tc.cookie), tc.role); err != nil {
			t.Fatalf("add member: %v", err)
		}
	}
	return fx, cookieB, cookieC
}

func patchView(t *testing.T, fx *viewHTTPFixture, cookie *http.Cookie, viewID, body string) *httptest.ResponseRecorder {
	t.Helper()
	return postAuthedJSON(t, fx.e, http.MethodPatch, fx.viewsBase+"/"+viewID, cookie, body)
}

func TestIssueViewShareToggleHTTP(t *testing.T) {
	fx, cookieB, _ := setupSharedHTTPFixture(t)

	rec := postView(t, fx, fx.cookieA, viewPayload("Team bugs"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d", rec.Code)
	}
	id := decodeViews(t, getViews(t, fx, fx.cookieA, fx.viewsBase))[0]["id"].(string)

	// Owner toggles shared on → 200, row annotated shared/owner fields.
	rec = patchView(t, fx, fx.cookieA, id, `{"shared":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner share: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var updated map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode updated: %v", err)
	}
	if updated["shared"] != true {
		t.Fatalf("shared = %v, want true", updated["shared"])
	}
	if updated["owner_id"] == nil || updated["owner_name"] == nil {
		t.Fatalf("owner annotation missing: %v", updated)
	}

	// Member B's list shows the shared view with the annotation.
	views := decodeViews(t, getViews(t, fx, cookieB, fx.viewsBase))
	if len(views) != 1 || views[0]["shared"] != true || views[0]["owner_name"] == nil {
		t.Fatalf("B's list = %v, want the shared view annotated", views)
	}

	// Non-owner member toggling sharing → 403 forbidden envelope.
	rec = patchView(t, fx, cookieB, id, `{"shared":false}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-owner share toggle: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if env.Error.Code != ErrCodeForbidden {
		t.Fatalf("error code = %q, want forbidden", env.Error.Code)
	}

	// Non-owner member renaming the shared view → 403 as well.
	rec = patchView(t, fx, cookieB, id, `{"name":"Hijacked"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-owner rename of shared view: status = %d, want 403", rec.Code)
	}

	// Owner unshares → 200 shared=false.
	rec = patchView(t, fx, fx.cookieA, id, `{"shared":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner unshare: status = %d, want 200", rec.Code)
	}
	if views := decodeViews(t, getViews(t, fx, cookieB, fx.viewsBase)); len(views) != 0 {
		t.Fatalf("B's list after unshare = %d, want 0", len(views))
	}
}

func TestIssueViewDeleteSharedHTTP(t *testing.T) {
	fx, cookieB, cookieC := setupSharedHTTPFixture(t)

	rec := postView(t, fx, fx.cookieA, viewPayload("Team bugs"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d", rec.Code)
	}
	id := decodeViews(t, getViews(t, fx, fx.cookieA, fx.viewsBase))[0]["id"].(string)
	rec = patchView(t, fx, fx.cookieA, id, `{"shared":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("share: status = %d", rec.Code)
	}

	// Plain member deleting a shared view they do not own → 403.
	rec = postAuthedJSON(t, fx.e, http.MethodDelete, fx.viewsBase+"/"+id, cookieB, "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("member delete shared: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}

	// Second admin (not the owner) deleting the shared view → 204.
	rec = postAuthedJSON(t, fx.e, http.MethodDelete, fx.viewsBase+"/"+id, cookieC, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("admin delete shared: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}
	if views := decodeViews(t, getViews(t, fx, fx.cookieA, fx.viewsBase)); len(views) != 0 {
		t.Fatalf("views after admin delete = %d, want 0", len(views))
	}

	// Owner deleting their own shared view → 204.
	rec = postView(t, fx, fx.cookieA, viewPayload("Mine again"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d", rec.Code)
	}
	id2 := decodeViews(t, getViews(t, fx, fx.cookieA, fx.viewsBase))[0]["id"].(string)
	if rec = patchView(t, fx, fx.cookieA, id2, `{"shared":true}`); rec.Code != http.StatusOK {
		t.Fatalf("share: status = %d", rec.Code)
	}
	rec = postAuthedJSON(t, fx.e, http.MethodDelete, fx.viewsBase+"/"+id2, fx.cookieA, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("owner delete shared: status = %d, want 204", rec.Code)
	}
}
