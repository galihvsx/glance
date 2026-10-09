package api

// Custom field HTTP endpoint tests (C7T2): field CRUD through the routes,
// duplicate-name 409 envelope, guest read 200 / guest mutate 403, bulk
// value set with the typed read-back, validation 400s with the honest
// envelope, cross-project field 404, value clear, and ?include_custom=1
// on the issue detail. Real test database, no skips.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func assertCFEnvelopeCode(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()
	code, _, _, _ := decodeEnvelope(t, rec.Body.Bytes())
	if code != want {
		t.Fatalf("envelope code = %q, want %q (body: %s)", code, want, rec.Body.String())
	}
}

func TestCustomFieldHTTP(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterCustomFieldRoutes(e, &IssueHandler{Pool: pool})

	cookie := loginTestUser(t, e, pool, uniqueEmail("cf-http"), "test-agent/1.0", uniqueIP())
	guestCookie := loginTestUser(t, e, pool, uniqueEmail("cf-http-guest"), "test-agent/1.0", uniqueIP())
	slug := uniqueSlug("cf-http")
	createWorkspaceHTTP(t, e, cookie, "Custom Field Co", slug)
	ident := uniqueProjectIdentifier("CF")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	projBase := "/api/v1/workspaces/" + slug + "/projects/" + ident
	fieldBase := projBase + "/custom-fields"

	// Admin adds the guest (role 5).
	guestID := authedUserID(t, e, guestCookie)
	rec := postAuthedJSON(t, e, http.MethodPost, "/api/v1/workspaces/"+slug+"/members", cookie,
		fmt.Sprintf(`{"user_id":%q,"role":5}`, guestID))
	if rec.Code != http.StatusOK {
		t.Fatalf("add guest: status = %d (body: %s)", rec.Code, rec.Body.String())
	}

	// Create → 201.
	rec = postAuthedJSON(t, e, http.MethodPost, fieldBase, cookie,
		`{"name":"Severity","field_type":"select","options":[{"value":"low"},{"value":"high","color":"#f00"}]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create field: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	var created struct {
		ID        string `json:"id"`
		FieldType string `json:"field_type"`
		Options   []struct {
			Value string `json:"value"`
		} `json:"options"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode field: %v", err)
	}
	if created.FieldType != "select" || len(created.Options) != 2 {
		t.Fatalf("unexpected field shape: %s", rec.Body.String())
	}

	// Duplicate name → 409 with the envelope.
	rec = postAuthedJSON(t, e, http.MethodPost, fieldBase, cookie,
		`{"name":"Severity","field_type":"text"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate name: status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	assertCFEnvelopeCode(t, rec, ErrCodeConflict)

	// Guest read → 200; guest create → 403.
	rec = postAuthedJSON(t, e, http.MethodGet, fieldBase, guestCookie, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("guest list: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodPost, fieldBase, guestCookie,
		`{"name":"Guest field","field_type":"text"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("guest create: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}

	// Guest get one → 200.
	rec = postAuthedJSON(t, e, http.MethodGet, fieldBase+"/"+created.ID, guestCookie, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("guest get: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	// Patch → 200; empty patch → 400.
	rec = postAuthedJSON(t, e, http.MethodPatch, fieldBase+"/"+created.ID, cookie,
		`{"name":"Priority level"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodPatch, fieldBase+"/"+created.ID, cookie, `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty patch: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}

	// Bad field id → 400.
	rec = postAuthedJSON(t, e, http.MethodGet, fieldBase+"/nope", cookie, "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad id: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}

	// Unknown field id → 404.
	rec = postAuthedJSON(t, e, http.MethodGet, fieldBase+"/00000000-0000-0000-0000-000000000000", cookie, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown id: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}

	// Make an issue and a couple more fields for the value endpoints.
	rec = postAuthedJSON(t, e, http.MethodPost, projBase+"/issues", cookie, `{"name":"valued"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create issue: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var iss struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &iss); err != nil {
		t.Fatalf("decode issue: %v", err)
	}
	rec = postAuthedJSON(t, e, http.MethodPost, fieldBase, cookie, `{"name":"Notes","field_type":"text"}`)
	var notesF struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &notesF); err != nil {
		t.Fatalf("decode notes field: %v", err)
	}
	valBase := projBase + "/issues/" + iss.ID + "/custom-values"

	// Bulk set → 200 with the typed values back.
	rec = postAuthedJSON(t, e, http.MethodPut, valBase, cookie,
		`{"values":{"`+created.ID+`":"high","`+notesF.ID+`":"ship it"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("set values: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var setResp struct {
		CustomValues map[string]struct {
			Value any `json:"value"`
		} `json:"custom_values"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &setResp); err != nil {
		t.Fatalf("decode set response: %v", err)
	}
	if setResp.CustomValues[created.ID].Value != "high" || setResp.CustomValues[notesF.ID].Value != "ship it" {
		t.Fatalf("unexpected values: %s", rec.Body.String())
	}

	// Guest may not set values → 403.
	rec = postAuthedJSON(t, e, http.MethodPut, valBase, guestCookie,
		`{"values":{"`+notesF.ID+`":"x"}}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("guest set values: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}

	// Validation: invalid select option → 400 with the honest message.
	rec = postAuthedJSON(t, e, http.MethodPut, valBase, cookie,
		`{"values":{"`+created.ID+`":"critical"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad select option: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	assertCFEnvelopeCode(t, rec, ErrCodeBadRequest)
	if !strings.Contains(rec.Body.String(), "options") {
		t.Fatalf("validation message not honest: %s", rec.Body.String())
	}

	// Validation: bad number → 400.
	rec = postAuthedJSON(t, e, http.MethodPost, fieldBase, cookie, `{"name":"Points","field_type":"number"}`)
	var numF struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &numF); err != nil {
		t.Fatalf("decode number field: %v", err)
	}
	rec = postAuthedJSON(t, e, http.MethodPut, valBase, cookie,
		`{"values":{"`+numF.ID+`":"abc"}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad number: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}

	// Cross-project field → 404.
	ident2 := uniqueProjectIdentifier("CF")
	createProjectHTTP(t, e, cookie, slug, "Other", ident2)
	rec = postAuthedJSON(t, e, http.MethodPost,
		"/api/v1/workspaces/"+slug+"/projects/"+ident2+"/custom-fields", cookie,
		`{"name":"Foreign","field_type":"text"}`)
	var foreignF struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &foreignF); err != nil {
		t.Fatalf("decode foreign field: %v", err)
	}
	rec = postAuthedJSON(t, e, http.MethodPut, valBase, cookie,
		`{"values":{"`+foreignF.ID+`":"x"}}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-project field: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}

	// Clear one value → 204; null in the bulk set also clears.
	rec = postAuthedJSON(t, e, http.MethodDelete, valBase+"/"+notesF.ID, cookie, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("clear value: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodPut, valBase, cookie,
		`{"values":{"`+created.ID+`":null}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("null clear: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	// ?include_custom=1 on the issue detail carries custom values.
	rec = postAuthedJSON(t, e, http.MethodPut, valBase, cookie,
		`{"values":{"`+created.ID+`":"low"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("re-set: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodGet, projBase+"/issues/"+iss.ID+"?include_custom=1", cookie, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("detail include_custom: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var detail struct {
		CustomValues map[string]struct {
			Name      string `json:"name"`
			FieldType string `json:"field_type"`
			Value     any    `json:"value"`
		} `json:"custom_values"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	cv, ok := detail.CustomValues[created.ID]
	if !ok || cv.Value != "low" || cv.FieldType != "select" {
		t.Fatalf("detail custom_values wrong: %s", rec.Body.String())
	}
	// Without the flag the key is absent (byte-identical shape to before).
	rec = postAuthedJSON(t, e, http.MethodGet, projBase+"/issues/"+iss.ID, cookie, "")
	if strings.Contains(rec.Body.String(), "custom_values") {
		t.Fatalf("detail without flag leaked custom_values")
	}

	// Delete field → 204; values cascade.
	rec = postAuthedJSON(t, e, http.MethodDelete, fieldBase+"/"+created.ID, cookie, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete field: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodGet, projBase+"/issues/"+iss.ID+"?include_custom=1", cookie, "")
	if strings.Contains(rec.Body.String(), created.ID) {
		t.Fatalf("field delete did not cascade values: %s", rec.Body.String())
	}
}
