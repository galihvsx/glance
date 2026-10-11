package api

// GitHub two-way linkage HTTP tests (C17T0): manual link/unlink through
// the routes (201/409/400/403/404 envelopes), the write-only workspace
// webhook secret via PATCH (never returned, validation, clear), and the
// public HMAC-verified webhook endpoint (200 on valid delivery, 401 on
// bad/missing signature, auto-link visible on the issue detail). Real
// test database, no skips.

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"
)

func TestGitHubRefHTTP(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterGitHubLinkRoutes(e, &IssueHandler{Pool: pool})

	admin := loginTestUser(t, e, pool, uniqueEmail("ghref-admin"), "ua", "127.0.0.1")
	slug := uniqueSlug("ghref-ws")
	createWorkspaceHTTP(t, e, admin, "GHRef Co", slug)
	ident := uniqueProjectIdentifier("GR")
	createProjectHTTP(t, e, admin, slug, "Engineering", ident)
	issueBase := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/issues"
	issID := createIssueHTTP(t, e, admin, issueBase, "Link me")
	refsBase := issueBase + "/" + issID + "/refs"

	// Create → 201, provider defaults to github.
	rec := postAuthedJSON(t, e, http.MethodPost, refsBase, admin,
		`{"repo":"acme/repo","ref_type":"pr","ref_id":"42","url":"https://github.com/acme/repo/pull/42"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create ref: status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	var created struct {
		ID       string `json:"id"`
		Provider string `json:"provider"`
		Repo     string `json:"repo"`
		RefType  string `json:"ref_type"`
		RefID    string `json:"ref_id"`
		State    string `json:"state"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode ref: %v", err)
	}
	if created.Provider != "github" || created.RefType != "pr" || created.State != "open" || created.ID == "" {
		t.Fatalf("created = %+v", created)
	}

	// Duplicate → 409 envelope.
	rec = postAuthedJSON(t, e, http.MethodPost, refsBase, admin,
		`{"repo":"acme/repo","ref_type":"pr","ref_id":"42","url":"https://github.com/acme/repo/pull/42"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate ref: status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}

	// Bad ref_type → 400.
	rec = postAuthedJSON(t, e, http.MethodPost, refsBase, admin,
		`{"repo":"acme/repo","ref_type":"branch","ref_id":"x","url":"https://github.com/acme/repo"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad ref_type: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}

	// The issue detail embeds external_refs[].
	rec = getAuthed(t, e, http.MethodGet, issueBase+"/"+issID, admin)
	if rec.Code != http.StatusOK {
		t.Fatalf("get issue: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var detail struct {
		ExternalRefs []struct {
			ID    string `json:"id"`
			State string `json:"state"`
		} `json:"external_refs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if len(detail.ExternalRefs) != 1 || detail.ExternalRefs[0].ID != created.ID {
		t.Fatalf("detail external_refs = %+v", detail.ExternalRefs)
	}

	// Guest member may not link → 403.
	guest := loginTestUser(t, e, pool, uniqueEmail("ghref-guest"), "ua", "127.0.0.1")
	guestID := authedUserID(t, e, guest)
	rec = postAuthedJSON(t, e, http.MethodPost, "/api/v1/workspaces/"+slug+"/members", admin,
		fmt.Sprintf(`{"user_id":%q,"role":5}`, guestID))
	if rec.Code != http.StatusOK {
		t.Fatalf("add guest: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodPost, refsBase, guest,
		`{"repo":"acme/repo","ref_type":"pr","ref_id":"43","url":"https://github.com/acme/repo/pull/43"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("guest create: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}

	// Delete → 204; again → 404.
	rec = postAuthedJSON(t, e, http.MethodDelete, refsBase+"/"+created.ID, admin, ``)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete ref: status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodDelete, refsBase+"/"+created.ID, admin, ``)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("double delete: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}

	// Unauthenticated → 401 (the refs routes sit behind RequireAuth).
	req := httptest.NewRequest(http.MethodPost, refsBase, strings.NewReader(`{}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec2 := httptest.NewRecorder()
	e.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated: status = %d, want 401", rec2.Code)
	}
}

// signWebhook builds the X-Hub-Signature-256 header value.
func signWebhook(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// postWebhook delivers a raw (unauthenticated) webhook request.
func postWebhook(t *testing.T, e *echo.Echo, event, signature string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/integrations/github/webhook", strings.NewReader(string(body)))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.Header.Set("X-GitHub-Event", event)
	if signature != "" {
		req.Header.Set("X-Hub-Signature-256", signature)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func TestGitHubWebhookHTTP(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterGitHubWebhookRoutes(e, &GitHubWebhookHandler{Pool: pool})

	admin := loginTestUser(t, e, pool, uniqueEmail("ghook-admin"), "ua", "127.0.0.1")
	slug := uniqueSlug("ghook-ws")
	createWorkspaceHTTP(t, e, admin, "GHook Co", slug)
	ident := uniqueProjectIdentifier("GW")
	createProjectHTTP(t, e, admin, slug, "Engineering", ident)
	issueBase := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/issues"
	issID := createIssueHTTP(t, e, admin, issueBase, "Webhook me")

	// Read the display ID for the mention.
	rec := getAuthed(t, e, http.MethodGet, issueBase+"/"+issID, admin)
	var det struct {
		DisplayID string `json:"display_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &det); err != nil {
		t.Fatalf("decode detail: %v", err)
	}

	// Configure the webhook secret via PATCH (write-only).
	secret := fmt.Sprintf("whsec-http-%s", uniqueSlug("s"))
	rec = postAuthedJSON(t, e, http.MethodPatch, "/api/v1/workspaces/"+slug, admin,
		fmt.Sprintf(`{"github_webhook_secret":%q}`, secret))
	if rec.Code != http.StatusOK {
		t.Fatalf("set secret: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"github_webhook_configured":true`) {
		t.Fatalf("secret response missing configured flag: %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), secret) {
		t.Fatalf("secret leaked in PATCH response")
	}

	// Valid opened delivery (no session cookie — the route is public) →
	// 200, and the issue detail shows the linked PR.
	payload := fmt.Sprintf(`{"action":"opened","pull_request":{"number":11,"title":"Fix %s","body":"closes %s","html_url":"https://github.com/acme/repo/pull/11","merged":false},"repository":{"full_name":"acme/repo"}}`,
		det.DisplayID, det.DisplayID)
	rec = postWebhook(t, e, "pull_request", signWebhook(secret, []byte(payload)), []byte(payload))
	if rec.Code != http.StatusOK {
		t.Fatalf("webhook: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, issueBase+"/"+issID, admin)
	var det2 struct {
		ExternalRefs []struct {
			RefID string `json:"ref_id"`
			State string `json:"state"`
		} `json:"external_refs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &det2); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if len(det2.ExternalRefs) != 1 || det2.ExternalRefs[0].RefID != "11" || det2.ExternalRefs[0].State != "open" {
		t.Fatalf("detail external_refs = %+v", det2.ExternalRefs)
	}

	// Bad signature → 401; missing signature → 401.
	rec = postWebhook(t, e, "pull_request", signWebhook("wrong", []byte(payload)), []byte(payload))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad signature: status = %d, want 401 (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postWebhook(t, e, "pull_request", "", []byte(payload))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing signature: status = %d, want 401 (body: %s)", rec.Code, rec.Body.String())
	}

	// Closed+merged delivery flips the ref state.
	closed := fmt.Sprintf(`{"action":"closed","pull_request":{"number":11,"title":"Fix %s","body":"","html_url":"https://github.com/acme/repo/pull/11","merged":true},"repository":{"full_name":"acme/repo"}}`,
		det.DisplayID)
	rec = postWebhook(t, e, "pull_request", signWebhook(secret, []byte(closed)), []byte(closed))
	if rec.Code != http.StatusOK {
		t.Fatalf("closed webhook: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	rec = getAuthed(t, e, http.MethodGet, issueBase+"/"+issID, admin)
	var det3 struct {
		ExternalRefs []struct {
			State string `json:"state"`
		} `json:"external_refs"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &det3); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if len(det3.ExternalRefs) != 1 || det3.ExternalRefs[0].State != "merged" {
		t.Fatalf("after close: external_refs = %+v", det3.ExternalRefs)
	}
}

func TestWorkspaceGitHubSecretHTTP(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})

	admin := loginTestUser(t, e, pool, uniqueEmail("ghsec-admin"), "ua", "127.0.0.1")
	slug := uniqueSlug("ghsec-ws")
	createWorkspaceHTTP(t, e, admin, "GHSec Co", slug)
	wsPath := "/api/v1/workspaces/" + slug

	// Set → 200, configured true, secret never echoed.
	secret := "http-secret-abc123"
	rec := postAuthedJSON(t, e, http.MethodPatch, wsPath, admin, fmt.Sprintf(`{"github_webhook_secret":%q}`, secret))
	if rec.Code != http.StatusOK {
		t.Fatalf("set: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var ws struct {
		GitHubConfigured bool `json:"github_webhook_configured"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &ws); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !ws.GitHubConfigured {
		t.Fatalf("github_webhook_configured = false after set")
	}
	if strings.Contains(rec.Body.String(), secret) {
		t.Fatalf("secret leaked in response: %s", rec.Body.String())
	}

	// Invalid → 400, no oracle detail.
	rec = postAuthedJSON(t, e, http.MethodPatch, wsPath, admin, `{"github_webhook_secret":"has space"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad secret: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}

	// Explicit null clears → configured false.
	rec = postAuthedJSON(t, e, http.MethodPatch, wsPath, admin, `{"github_webhook_secret":null}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"github_webhook_configured":false`) {
		t.Fatalf("clear response: %s", rec.Body.String())
	}

	// Non-admin member → 403.
	member := loginTestUser(t, e, pool, uniqueEmail("ghsec-member"), "ua", "127.0.0.1")
	memberID := authedUserID(t, e, member)
	rec = postAuthedJSON(t, e, http.MethodPost, wsPath+"/members", admin,
		fmt.Sprintf(`{"user_id":%q,"role":15}`, memberID))
	if rec.Code != http.StatusOK {
		t.Fatalf("add member: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodPatch, wsPath, member, `{"github_webhook_secret":"x"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("member set: status = %d, want 403 (body: %s)", rec.Code, rec.Body.String())
	}
}
