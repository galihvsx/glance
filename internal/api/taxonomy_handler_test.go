package api

// Taxonomy HTTP endpoint tests (Task 16): label CRUD through the
// project-nested routes (201 / 409 on duplicate / 204 delete), idempotent
// assign/unassign (204 twice), estimate scale create + list, and the spec
// §5 error envelope on a foreign label. Real test database, no skips.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestTaxonomyHTTP(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterTaxonomyRoutes(e, &IssueHandler{Pool: pool})

	cookie := loginTestUser(t, e, pool, uniqueEmail("tax-http"), "test-agent", uniqueIP())
	slug := uniqueSlug("tax-http")
	createWorkspaceHTTP(t, e, cookie, "Tax Co", slug)
	ident := uniqueProjectIdentifier("TX")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	projBase := "/api/v1/workspaces/" + slug + "/projects/" + ident

	// Create label → 201.
	rec := postAuthedJSON(t, e, http.MethodPost, projBase+"/labels", cookie,
		`{"name":"bug","color":"#ef4444"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create label: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	var label struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &label); err != nil {
		t.Fatalf("decode label: %v", err)
	}
	if label.Name != "bug" || label.Color != "#ef4444" {
		t.Fatalf("label = %+v, want bug/#ef4444", label)
	}

	// Duplicate name → 409 envelope.
	rec = postAuthedJSON(t, e, http.MethodPost, projBase+"/labels", cookie,
		`{"name":"bug"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate label: status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"code":"conflict"`) {
		t.Fatalf("409 envelope: body=%s", rec.Body.String())
	}

	// Bad color → 400.
	rec = postAuthedJSON(t, e, http.MethodPost, projBase+"/labels", cookie,
		`{"name":"nope","color":"red"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad color: status = %d, want 400", rec.Code)
	}

	// List → wrapped shape.
	rec = getAuthed(t, e, http.MethodGet, projBase+"/labels", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list labels: status = %d", rec.Code)
	}
	var listed struct {
		Labels []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"labels"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode labels: %v", err)
	}
	if len(listed.Labels) != 1 {
		t.Fatalf("labels = %d, want 1", len(listed.Labels))
	}

	// Create an issue and assign the label — twice (idempotent) → 204.
	rec = postAuthedJSON(t, e, http.MethodPost, projBase+"/issues", cookie, `{"name":"labeled"}`)
	var iss struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &iss); err != nil {
		t.Fatalf("decode issue: %v", err)
	}
	assignURL := projBase + "/issues/" + iss.ID + "/labels/" + label.ID
	for i := 0; i < 2; i++ {
		rec = postAuthedJSON(t, e, http.MethodPost, assignURL, cookie, ``)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("assign label (try %d): status = %d, want 204", i+1, rec.Code)
		}
	}

	// The issue detail now carries the label.
	rec = getAuthed(t, e, http.MethodGet, projBase+"/issues/"+iss.ID, cookie)
	var detail struct {
		Labels []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"labels"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if len(detail.Labels) != 1 || detail.Labels[0].ID != label.ID {
		t.Fatalf("detail labels = %+v, want the bug label", detail.Labels)
	}

	// Unassign twice (idempotent) → 204, and the label is gone from detail.
	for i := 0; i < 2; i++ {
		rec = postAuthedJSON(t, e, http.MethodDelete, assignURL, cookie, ``)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("unassign label (try %d): status = %d, want 204", i+1, rec.Code)
		}
	}
	rec = getAuthed(t, e, http.MethodGet, projBase+"/issues/"+iss.ID, cookie)
	detail.Labels = nil
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if len(detail.Labels) != 0 {
		t.Fatalf("detail labels after unassign = %+v, want empty", detail.Labels)
	}

	// Estimate scale: create → 201, list → wrapped with points.
	rec = postAuthedJSON(t, e, http.MethodPost, projBase+"/estimates", cookie,
		`{"name":"Fibonacci","points":[{"key":"1","value":1},{"key":"2","value":2},{"key":"3","value":3}]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create estimate: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	var est struct {
		ID     string `json:"id"`
		Points []struct {
			ID  string `json:"id"`
			Key string `json:"key"`
		} `json:"points"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &est); err != nil {
		t.Fatalf("decode estimate: %v", err)
	}
	if len(est.Points) != 3 {
		t.Fatalf("points = %d, want 3", len(est.Points))
	}
	rec = getAuthed(t, e, http.MethodGet, projBase+"/estimates", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list estimates: status = %d", rec.Code)
	}

	// Set the issue's estimate_point_id via PATCH.
	rec = postAuthedJSON(t, e, http.MethodPatch, projBase+"/issues/"+iss.ID, cookie,
		`{"estimate_point_id":"`+est.Points[1].ID+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch estimate_point_id: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var patched struct {
		EstimatePointID *string `json:"estimate_point_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &patched); err != nil {
		t.Fatalf("decode patched: %v", err)
	}
	if patched.EstimatePointID == nil || *patched.EstimatePointID != est.Points[1].ID {
		t.Fatalf("estimate_point_id = %v, want %s", patched.EstimatePointID, est.Points[1].ID)
	}

	// Delete label → 204, then 404.
	rec = postAuthedJSON(t, e, http.MethodDelete, projBase+"/labels/"+label.ID, cookie, ``)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete label: status = %d, want 204", rec.Code)
	}
	rec = getAuthed(t, e, http.MethodGet, projBase+"/labels/"+label.ID, cookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get deleted label: status = %d, want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"code":"not_found"`) {
		t.Fatalf("404 envelope: body=%s", rec.Body.String())
	}
}
