package api

// Task 28 (T29): malformed UUID path params must answer 400 bad_request
// (spec §5 envelope), never reach a ::uuid cast (500). These tests pin
// the global pass: the DRY requireUUIDParam helper plus a spread of the
// touched routes.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestValidUUID pins the canonical 8-4-4-4-12 shape check.
func TestValidUUID(t *testing.T) {
	valid := []string{
		"123e4567-e89b-12d3-a456-426614174000",
		"123E4567-E89B-12D3-A456-426614174000", // uppercase hex ok
		"00000000-0000-0000-0000-000000000000",
	}
	for _, s := range valid {
		if !validUUID(s) {
			t.Errorf("validUUID(%q) = false, want true", s)
		}
	}
	invalid := []string{
		"",
		"not-a-uuid",
		"123e4567-e89b-12d3-a456-42661417400",   // too short
		"123e4567-e89b-12d3-a456-4266141740000", // too long
		"123e4567_e89b_12d3_a456_426614174000",  // wrong separators
		"123e4567-e89b-12d3-a456-42661417400g",  // non-hex
		" 123e4567-e89b-12d3-a456-426614174000", // leading space
	}
	for _, s := range invalid {
		if validUUID(s) {
			t.Errorf("validUUID(%q) = true, want false", s)
		}
	}
}

// TestMalformedUUIDPathParams400 walks a spread of the routes touched by
// the Task 28 UUID pass and asserts each answers 400 with the spec §5
// error envelope (code bad_request) for a malformed UUID — proving the
// value never reaches a ::uuid cast.
func TestMalformedUUIDPathParams400(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	ih := &IssueHandler{Pool: pool}
	RegisterIssueRoutes(e, ih)
	RegisterTaxonomyRoutes(e, ih)
	RegisterSatelliteRoutes(e, ih)
	RegisterIntakeRoutes(e, ih)
	RegisterCycleRoutes(e, ih)
	RegisterNotifyRoutes(e, &NotifyHandler{Pool: pool})

	cookie := loginTestUser(t, e, pool, uniqueEmail("uuid400"), "test-agent", uniqueIP())
	slug := uniqueSlug("uuid400")
	createWorkspaceHTTP(t, e, cookie, "UUID Co", slug)
	ident := uniqueProjectIdentifier("UU")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	projBase := "/api/v1/workspaces/" + slug + "/projects/" + ident

	cases := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"getIssue", http.MethodGet, projBase + "/issues/not-a-uuid", ""},
		{"patchIssue", http.MethodPatch, projBase + "/issues/not-a-uuid", `{"name":"x"}`},
		{"deleteIssue", http.MethodDelete, projBase + "/issues/not-a-uuid", ""},
		{"updateComment", http.MethodPatch, projBase + "/issues/not-a-uuid/comments/not-a-uuid", `{"content":{"type":"doc","content":[]}}`},
		{"deleteComment", http.MethodDelete, projBase + "/issues/not-a-uuid/comments/not-a-uuid", ""},
		{"addCommentReaction", http.MethodPost, projBase + "/issues/not-a-uuid/comments/not-a-uuid/reactions", `{"emoji":"👍"}`},
		{"assignLabel", http.MethodPost, projBase + "/issues/not-a-uuid/labels/not-a-uuid", ""},
		{"unassignLabel", http.MethodDelete, projBase + "/issues/not-a-uuid/labels/not-a-uuid", ""},
		{"assignAssignee", http.MethodPost, projBase + "/issues/not-a-uuid/assignees/not-a-uuid", ""},
		{"getLabel", http.MethodGet, projBase + "/labels/not-a-uuid", ""},
		{"deleteLabel", http.MethodDelete, projBase + "/labels/not-a-uuid", ""},
		{"deleteRelation", http.MethodDelete, projBase + "/issues/not-a-uuid/relations/not-a-uuid", ""},
		{"getCycle", http.MethodGet, projBase + "/cycles/not-a-uuid", ""},
		{"patchCycle", http.MethodPatch, projBase + "/cycles/not-a-uuid", `{"name":"x"}`},
		{"deleteCycle", http.MethodDelete, projBase + "/cycles/not-a-uuid", ""},
		{"addCycleIssues", http.MethodPost, projBase + "/cycles/not-a-uuid/issues", `{"issue_ids":[]}`},
		{"getWebhook", http.MethodGet, "/api/v1/workspaces/" + slug + "/webhooks/not-a-uuid", ""},
		{"patchWebhook", http.MethodPatch, "/api/v1/workspaces/" + slug + "/webhooks/not-a-uuid", `{}`},
		{"deleteWebhook", http.MethodDelete, "/api/v1/workspaces/" + slug + "/webhooks/not-a-uuid", ""},
		{"markNotificationRead", http.MethodPost, "/api/v1/notifications/not-a-uuid/read", ""},
		{"acceptIntake", http.MethodPost, projBase + "/intake/issues/not-a-uuid/accept", ""},
		{"rejectIntake", http.MethodPost, projBase + "/intake/issues/not-a-uuid/reject", ""},
	}
	for _, tc := range cases {
		var rec *httptest.ResponseRecorder
		if tc.body != "" || tc.method == http.MethodPost || tc.method == http.MethodPatch {
			rec = postAuthedJSON(t, e, tc.method, tc.path, cookie, tc.body)
		} else {
			rec = getAuthed(t, e, tc.method, tc.path, cookie)
		}
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400 (body: %s)", tc.name, rec.Code, rec.Body.String())
			continue
		}
		var env struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Errorf("%s: body is not the error envelope: %v", tc.name, err)
			continue
		}
		if env.Error.Code != ErrCodeBadRequest {
			t.Errorf("%s: error.code = %q, want %q", tc.name, env.Error.Code, ErrCodeBadRequest)
		}
		if env.Error.Message == "" {
			t.Errorf("%s: empty error message", tc.name)
		}
	}
}
