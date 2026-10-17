package api

// Trash HTTP endpoints (C52T0): GET ?deleted=1 lists deleted-only
// rows with deleted_at, POST /:uuid/restore restores with 200, and a
// second restore is a 404. Real test database, no skips.

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestTrashHTTPRestore(t *testing.T) {
	pool := newTestPool(t)
	e := testIssueServer(t, pool)

	cookie := loginTestUser(t, e, pool, uniqueEmail("trash-http"), "test-agent", uniqueIP())
	slug := uniqueSlug("trash-http")
	createWorkspaceHTTP(t, e, cookie, "Trash Co", slug)
	ident := uniqueProjectIdentifier("TR")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	base := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/issues"

	rec := postAuthedJSON(t, e, http.MethodPost, base, cookie, `{"name":"Trash me"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create issue: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}

	// Trash is empty before the delete.
	rec = getAuthed(t, e, http.MethodGet, base+"?deleted=1", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("trash before delete: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var trash struct {
		Results []struct {
			ID        string     `json:"id"`
			DeletedAt *time.Time `json:"deleted_at"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &trash); err != nil {
		t.Fatalf("decode trash: %v", err)
	}
	if len(trash.Results) != 0 {
		t.Fatalf("trash before delete = %d rows, want 0", len(trash.Results))
	}

	// Delete → 204; the issue leaves the normal list and enters trash.
	rec = postAuthedJSON(t, e, http.MethodDelete, base+"/"+created.ID, cookie, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete issue: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, base+"?deleted=1", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("trash after delete: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	trash = struct {
		Results []struct {
			ID        string     `json:"id"`
			DeletedAt *time.Time `json:"deleted_at"`
		} `json:"results"`
	}{}
	if err := json.Unmarshal(rec.Body.Bytes(), &trash); err != nil {
		t.Fatalf("decode trash: %v", err)
	}
	if len(trash.Results) != 1 || trash.Results[0].ID != created.ID {
		t.Fatalf("trash after delete = %+v, want [%s]", trash.Results, created.ID)
	}
	if trash.Results[0].DeletedAt == nil {
		t.Fatalf("trash row DeletedAt = nil, want the soft-delete timestamp")
	}

	// Restore → 200 with the restored issue; deleted_at is absent
	// from the restored row (omitempty, live row).
	rec = postAuthedJSON(t, e, http.MethodPost, base+"/"+created.ID+"/restore", cookie, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("restore issue: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var restored struct {
		ID        string     `json:"id"`
		DeletedAt *time.Time `json:"deleted_at"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &restored); err != nil {
		t.Fatalf("decode restored: %v", err)
	}
	if restored.ID != created.ID || restored.DeletedAt != nil {
		t.Fatalf("restored = %+v, want id %s with no deleted_at", restored, created.ID)
	}

	// Trash is empty again; the issue is back in the normal list.
	rec = getAuthed(t, e, http.MethodGet, base+"?deleted=1", cookie)
	trash = struct {
		Results []struct {
			ID        string     `json:"id"`
			DeletedAt *time.Time `json:"deleted_at"`
		} `json:"results"`
	}{}
	if err := json.Unmarshal(rec.Body.Bytes(), &trash); err != nil {
		t.Fatalf("decode trash: %v", err)
	}
	if len(trash.Results) != 0 {
		t.Fatalf("trash after restore = %d rows, want 0", len(trash.Results))
	}
	rec = getAuthed(t, e, http.MethodGet, base+"/"+created.ID, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("get after restore: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	// Second restore → 404 ("not in trash").
	rec = postAuthedJSON(t, e, http.MethodPost, base+"/"+created.ID+"/restore", cookie, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("second restore: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
}
