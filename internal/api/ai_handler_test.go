package api

// AI assist HTTP endpoint tests (C5T2): the draft/triage endpoints end
// to end against a real database and a stub OpenAI-compatible provider.
// Real test database, no skips.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"glance/internal/config"
	"glance/internal/service"
)

// aiProviderStub answers /chat/completions with canned content.
type aiProviderStub struct {
	status  int
	content string
	server  *httptest.Server
	gotAuth string
}

func newAIProviderStub(t *testing.T) *aiProviderStub {
	t.Helper()
	s := &aiProviderStub{}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.gotAuth = r.Header.Get("Authorization")
		var body struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		status := s.status
		if status == 0 {
			status = http.StatusOK
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if status == http.StatusOK {
			raw, _ := json.Marshal(s.content)
			fmt.Fprintf(w, `{"choices":[{"message":{"role":"assistant","content":%s}}]}`, raw)
		} else {
			fmt.Fprint(w, `{"error":{"message":"boom"}}`)
		}
	}))
	t.Cleanup(s.server.Close)
	return s
}

func (s *aiProviderStub) aiCfg() config.AIConfig {
	return config.AIConfig{BaseURL: s.server.URL, Model: "stub", APIKey: "sk-handler-test-key"}
}

func testAIServer(t *testing.T, pool *pgxpool.Pool, aiCfg config.AIConfig) *echo.Echo {
	t.Helper()
	e := testServer(t, pool)
	RegisterAuthRoutes(e, &AuthHandler{Pool: pool, Config: &config.Config{OTPPepper: "test-pepper-do-not-use-in-prod"}})
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterTaxonomyRoutes(e, &IssueHandler{Pool: pool})
	RegisterAIRoutes(e, &AIHandler{Pool: pool, AI: aiCfg})
	return e
}

func setupAIHTTP(t *testing.T, e *echo.Echo, pool *pgxpool.Pool) (cookie *http.Cookie, slug, ident string) {
	t.Helper()
	cookie = loginTestUser(t, e, pool, uniqueEmail("ai-http"), "ua", "10.9.1.1")
	slug = uniqueSlug("ai-http")
	createWorkspaceHTTP(t, e, cookie, "AI Co", slug)
	ident = uniqueProjectIdentifier("AIH")
	createProjectHTTP(t, e, cookie, slug, "AI Proj", ident)
	return cookie, slug, ident
}

func TestAIDraftHTTP(t *testing.T) {
	pool := newTestPool(t)
	stub := newAIProviderStub(t)
	stub.content = "## Summary\n\nDrafted description."
	e := testAIServer(t, pool, stub.aiCfg())
	cookie, slug, ident := setupAIHTTP(t, e, pool)

	rec := postAuthedJSON(t, e, http.MethodPost, "/api/v1/ai/draft", cookie,
		fmt.Sprintf(`{"workspace_slug":%q,"project_identifier":%q,"title":"Broken login","context":"Safari only"}`, slug, ident))
	if rec.Code != http.StatusOK {
		t.Fatalf("draft: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var got struct {
		Description string `json:"description"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Description != stub.content {
		t.Errorf("description = %q, want stub content", got.Description)
	}
	if stub.gotAuth != "Bearer sk-handler-test-key" {
		t.Errorf("provider Authorization = %q, want Bearer <redacted>", stub.gotAuth)
	}
}

func TestAITriageHTTP(t *testing.T) {
	pool := newTestPool(t)
	stub := newAIProviderStub(t)
	stub.content = `{"priority": "high", "label_names": ["does-not-exist"], "state_name": "Todo"}`
	e := testAIServer(t, pool, stub.aiCfg())
	cookie, slug, ident := setupAIHTTP(t, e, pool)

	rec := postAuthedJSON(t, e, http.MethodPost, "/api/v1/ai/triage", cookie,
		fmt.Sprintf(`{"workspace_slug":%q,"project_identifier":%q,"title":"Broken login"}`, slug, ident))
	if rec.Code != http.StatusOK {
		t.Fatalf("triage: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var got struct {
		Priority   int      `json:"priority"`
		LabelNames []string `json:"label_names"`
		StateName  *string  `json:"state_name"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Priority != 3 {
		t.Errorf("priority = %d, want 3 for \"high\"", got.Priority)
	}
	if len(got.LabelNames) != 0 {
		t.Errorf("label_names = %v, unknown labels must be dropped", got.LabelNames)
	}
	if got.StateName == nil || *got.StateName != "Todo" {
		t.Errorf("state_name = %v, want canonical \"Todo\"", got.StateName)
	}
}

// TestAIUnconfigured503 pins the honest-unconfigured contract: when no
// API key is configured, both endpoints answer 503 with
// code "ai_not_configured" — never a fake error, never a 500.
func TestAIUnconfigured503(t *testing.T) {
	pool := newTestPool(t)
	e := testAIServer(t, pool, config.AIConfig{})
	cookie, slug, ident := setupAIHTTP(t, e, pool)

	for _, path := range []string{"/api/v1/ai/draft", "/api/v1/ai/triage"} {
		rec := postAuthedJSON(t, e, http.MethodPost, path, cookie,
			fmt.Sprintf(`{"workspace_slug":%q,"project_identifier":%q,"title":"t"}`, slug, ident))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s: status = %d, want 503", path, rec.Code)
		}
		var env struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Fatalf("%s: decode: %v", path, err)
		}
		if env.Error.Code != "ai_not_configured" {
			t.Errorf("%s: code = %q, want ai_not_configured", path, env.Error.Code)
		}
	}
}

// TestAIProviderFailure502: a failing provider surfaces as 502, not 500.
func TestAIProviderFailure502(t *testing.T) {
	pool := newTestPool(t)
	stub := newAIProviderStub(t)
	stub.status = http.StatusInternalServerError
	e := testAIServer(t, pool, stub.aiCfg())
	cookie, slug, ident := setupAIHTTP(t, e, pool)

	rec := postAuthedJSON(t, e, http.MethodPost, "/api/v1/ai/draft", cookie,
		fmt.Sprintf(`{"workspace_slug":%q,"project_identifier":%q,"title":"t"}`, slug, ident))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("draft: status = %d (body: %s), want 502", rec.Code, rec.Body.String())
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Error.Code != "bad_gateway" {
		t.Errorf("code = %q, want bad_gateway", env.Error.Code)
	}
}

func TestAIDraftValidation(t *testing.T) {
	pool := newTestPool(t)
	stub := newAIProviderStub(t)
	e := testAIServer(t, pool, stub.aiCfg())
	cookie, slug, ident := setupAIHTTP(t, e, pool)

	// Missing title → 400.
	rec := postAuthedJSON(t, e, http.MethodPost, "/api/v1/ai/draft", cookie,
		fmt.Sprintf(`{"workspace_slug":%q,"project_identifier":%q}`, slug, ident))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing title: status = %d, want 400", rec.Code)
	}
	// Missing project scope → 400.
	rec = postAuthedJSON(t, e, http.MethodPost, "/api/v1/ai/draft", cookie, `{"title":"t"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing scope: status = %d, want 400", rec.Code)
	}
	// Malformed body → 400.
	rec = postAuthedJSON(t, e, http.MethodPost, "/api/v1/ai/draft", cookie, `{"title":`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed body: status = %d, want 400", rec.Code)
	}
}

// TestAINonMember404 pins member-scoping: a workspace outsider gets 404
// (the tenancy boundary), and the provider is never called.
func TestAINonMember404(t *testing.T) {
	pool := newTestPool(t)
	stub := newAIProviderStub(t)
	stub.content = "x"
	e := testAIServer(t, pool, stub.aiCfg())
	cookie, slug, ident := setupAIHTTP(t, e, pool)
	_ = cookie

	outsider := loginTestUser(t, e, pool, uniqueEmail("ai-out"), "ua", "10.9.1.2")
	rec := postAuthedJSON(t, e, http.MethodPost, "/api/v1/ai/draft", outsider,
		fmt.Sprintf(`{"workspace_slug":%q,"project_identifier":%q,"title":"t"}`, slug, ident))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("outsider draft: status = %d, want 404", rec.Code)
	}
	if stub.gotAuth != "" {
		t.Error("provider called for non-member; tenancy must resolve first")
	}
}

// TestAIKeyNeverLoggedHTTP: the handler's error paths must not leak the
// key into logs or responses.
func TestAIKeyNeverLoggedHTTP(t *testing.T) {
	pool := newTestPool(t)
	stub := newAIProviderStub(t)
	stub.status = http.StatusInternalServerError
	e := testAIServer(t, pool, stub.aiCfg())
	cookie, slug, ident := setupAIHTTP(t, e, pool)

	var buf strings.Builder
	old := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(old) })

	rec := postAuthedJSON(t, e, http.MethodPost, "/api/v1/ai/draft", cookie,
		fmt.Sprintf(`{"workspace_slug":%q,"project_identifier":%q,"title":"t"}`, slug, ident))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
	if strings.Contains(buf.String(), "sk-handler-test-key") {
		t.Errorf("key in logs: %s", buf.String())
	}
	if strings.Contains(rec.Body.String(), "sk-handler-test-key") {
		t.Errorf("key in response body: %s", rec.Body.String())
	}
}

// TestAIStatus pins the availability probe contract (C5T3): GET
// /api/v1/ai/status reports {configured} honestly without calling the
// provider — key configured → true, key unset → false. The UI needs
// this to render the honest disabled state without burning a provider
// call (neither /draft nor /triage can probe cheaply: both validate
// input first and then call the provider on a valid title).
func TestAIStatus(t *testing.T) {
	pool := newTestPool(t)
	stub := newAIProviderStub(t)
	e := testAIServer(t, pool, stub.aiCfg())
	cookie, _, _ := setupAIHTTP(t, e, pool)

	rec := getAuthed(t, e, http.MethodGet, "/api/v1/ai/status", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: code = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var got struct {
		Configured bool `json:"configured"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Configured {
		t.Error("configured = false, want true with an API key set")
	}
	if stub.gotAuth != "" {
		t.Errorf("status probed the provider (Authorization = %q), must not", stub.gotAuth)
	}
}

// TestAIStatusUnconfigured: without an API key the probe reports false.
func TestAIStatusUnconfigured(t *testing.T) {
	pool := newTestPool(t)
	e := testAIServer(t, pool, config.AIConfig{})
	cookie, _, _ := setupAIHTTP(t, e, pool)

	rec := getAuthed(t, e, http.MethodGet, "/api/v1/ai/status", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: code = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var got struct {
		Configured bool `json:"configured"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Configured {
		t.Error("configured = true, want false without an API key")
	}
}

// TestAIStatusRequiresAuth: the probe is auth-only, not public.
func TestAIStatusRequiresAuth(t *testing.T) {
	pool := newTestPool(t)
	stub := newAIProviderStub(t)
	e := testAIServer(t, pool, stub.aiCfg())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/ai/status", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: code = %d, want 401 without a session", rec.Code)
	}
}

// ---------- C25T0: AI issue summary ----------

// setupAISummarize mirrors setupAIHTTP but also returns the login
// email so the test can resolve the user's id for service-layer issue
// creation.
func setupAISummarize(t *testing.T, e *echo.Echo, pool *pgxpool.Pool) (cookie *http.Cookie, slug, ident, email string) {
	t.Helper()
	email = uniqueEmail("ai-sum")
	cookie = loginTestUser(t, e, pool, email, "ua", "10.9.1.5")
	slug = uniqueSlug("ai-sum")
	createWorkspaceHTTP(t, e, cookie, "AI Co", slug)
	ident = uniqueProjectIdentifier("AIS")
	createProjectHTTP(t, e, cookie, slug, "AI Proj", ident)
	return cookie, slug, ident, email
}

// summarizeIssueHTTP creates an issue directly via the service layer and
// returns its UUID.
func summarizeIssueHTTP(t *testing.T, pool *pgxpool.Pool, slug, ident, email, name string) string {
	t.Helper()
	var actorID string
	if err := pool.QueryRow(context.Background(),
		`SELECT id::text FROM users WHERE email = $1`, strings.ToLower(email)).Scan(&actorID); err != nil {
		t.Fatalf("resolve user id: %v", err)
	}
	iss, err := service.CreateIssue(context.Background(), pool, slug, ident, actorID,
		service.CreateIssueInput{Name: name})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	return iss.ID
}

func TestAISummarizeHTTP(t *testing.T) {
	pool := newTestPool(t)
	stub := newAIProviderStub(t)
	stub.content = "## TL;DR\n\nWhat: broken login."
	e := testAIServer(t, pool, stub.aiCfg())
	cookie, slug, ident, email := setupAISummarize(t, e, pool)
	issueID := summarizeIssueHTTP(t, pool, slug, ident, email, "Broken login")

	rec := postAuthedJSON(t, e, http.MethodPost, "/api/v1/ai/summarize", cookie,
		fmt.Sprintf(`{"workspace_slug":%q,"project_identifier":%q,"issue_uuid":%q}`, slug, ident, issueID))
	if rec.Code != http.StatusOK {
		t.Fatalf("summarize: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var got struct {
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Summary != stub.content {
		t.Errorf("summary = %q, want stub content", got.Summary)
	}
	if stub.gotAuth != "Bearer sk-handler-test-key" {
		t.Errorf("provider Authorization = %q, want Bearer <redacted>", stub.gotAuth)
	}
}

// TestAISummarizeUnconfigured503: the honest-unconfigured contract
// extends to /summarize.
func TestAISummarizeUnconfigured503(t *testing.T) {
	pool := newTestPool(t)
	e := testAIServer(t, pool, config.AIConfig{})
	cookie, slug, ident, email := setupAISummarize(t, e, pool)
	issueID := summarizeIssueHTTP(t, pool, slug, ident, email, "Broken login")

	rec := postAuthedJSON(t, e, http.MethodPost, "/api/v1/ai/summarize", cookie,
		fmt.Sprintf(`{"workspace_slug":%q,"project_identifier":%q,"issue_uuid":%q}`, slug, ident, issueID))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("summarize: status = %d, want 503", rec.Code)
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Error.Code != "ai_not_configured" {
		t.Errorf("code = %q, want ai_not_configured", env.Error.Code)
	}
}

// TestAISummarizeProviderFailure502: provider failure surfaces as 502.
func TestAISummarizeProviderFailure502(t *testing.T) {
	pool := newTestPool(t)
	stub := newAIProviderStub(t)
	stub.status = http.StatusInternalServerError
	e := testAIServer(t, pool, stub.aiCfg())
	cookie, slug, ident, email := setupAISummarize(t, e, pool)
	issueID := summarizeIssueHTTP(t, pool, slug, ident, email, "Broken login")

	rec := postAuthedJSON(t, e, http.MethodPost, "/api/v1/ai/summarize", cookie,
		fmt.Sprintf(`{"workspace_slug":%q,"project_identifier":%q,"issue_uuid":%q}`, slug, ident, issueID))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("summarize: status = %d (body: %s), want 502", rec.Code, rec.Body.String())
	}
}

// TestAISummarizeValidation: missing issue_uuid / scope → 400.
func TestAISummarizeValidation(t *testing.T) {
	pool := newTestPool(t)
	stub := newAIProviderStub(t)
	e := testAIServer(t, pool, stub.aiCfg())
	cookie, slug, ident := setupAIHTTP(t, e, pool)

	// Missing issue_uuid → 400.
	rec := postAuthedJSON(t, e, http.MethodPost, "/api/v1/ai/summarize", cookie,
		fmt.Sprintf(`{"workspace_slug":%q,"project_identifier":%q}`, slug, ident))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing issue_uuid: status = %d, want 400", rec.Code)
	}
	// Missing project scope → 400.
	rec = postAuthedJSON(t, e, http.MethodPost, "/api/v1/ai/summarize", cookie, `{"issue_uuid":"00000000-0000-0000-0000-000000000000"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing scope: status = %d, want 400", rec.Code)
	}
	// Malformed body → 400.
	rec = postAuthedJSON(t, e, http.MethodPost, "/api/v1/ai/summarize", cookie, `{"issue_uuid":`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed body: status = %d, want 400", rec.Code)
	}
	// Unknown issue UUID → 404 (not a 500, not a provider call).
	rec = postAuthedJSON(t, e, http.MethodPost, "/api/v1/ai/summarize", cookie,
		fmt.Sprintf(`{"workspace_slug":%q,"project_identifier":%q,"issue_uuid":"00000000-0000-0000-0000-000000000000"}`, slug, ident))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown issue: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
	if stub.gotAuth != "" {
		t.Error("provider called for missing issue; lookup must precede the provider call")
	}
}

// TestAISummarizeNonMember404: outsiders get the tenancy-boundary 404
// and the provider is never called.
func TestAISummarizeNonMember404(t *testing.T) {
	pool := newTestPool(t)
	stub := newAIProviderStub(t)
	stub.content = "x"
	e := testAIServer(t, pool, stub.aiCfg())
	_, slug, ident, email := setupAISummarize(t, e, pool)
	issueID := summarizeIssueHTTP(t, pool, slug, ident, email, "Broken login")

	outsider := loginTestUser(t, e, pool, uniqueEmail("ai-sum-out"), "ua", "10.9.1.3")
	rec := postAuthedJSON(t, e, http.MethodPost, "/api/v1/ai/summarize", outsider,
		fmt.Sprintf(`{"workspace_slug":%q,"project_identifier":%q,"issue_uuid":%q}`, slug, ident, issueID))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("outsider summarize: status = %d, want 404", rec.Code)
	}
	if stub.gotAuth != "" {
		t.Error("provider called for non-member; tenancy must resolve first")
	}
}

// ---------- C25T1: AI sub-issue breakdown ----------

func TestAISubIssuesHTTP(t *testing.T) {
	pool := newTestPool(t)
	stub := newAIProviderStub(t)
	stub.content = `[{"title":"Add provider config","description":"Wire the endpoints."},{"title":"Swap middleware"}]`
	e := testAIServer(t, pool, stub.aiCfg())
	cookie, slug, ident, email := setupAISummarize(t, e, pool)
	issueID := summarizeIssueHTTP(t, pool, slug, ident, email, "Migrate auth to OAuth2")

	rec := postAuthedJSON(t, e, http.MethodPost, "/api/v1/ai/subissues", cookie,
		fmt.Sprintf(`{"workspace_slug":%q,"project_identifier":%q,"issue_uuid":%q,"max":4}`, slug, ident, issueID))
	if rec.Code != http.StatusOK {
		t.Fatalf("subissues: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var got struct {
		Suggestions []struct {
			Title       string  `json:"title"`
			Description *string `json:"description"`
		} `json:"suggestions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Suggestions) != 2 {
		t.Fatalf("suggestions = %+v, want 2", got.Suggestions)
	}
	if got.Suggestions[0].Title != "Add provider config" ||
		got.Suggestions[0].Description == nil || *got.Suggestions[0].Description != "Wire the endpoints." {
		t.Errorf("suggestions[0] = %+v", got.Suggestions[0])
	}
	if got.Suggestions[1].Description != nil {
		t.Errorf("suggestions[1].description = %q, want absent", *got.Suggestions[1].Description)
	}
	if stub.gotAuth != "Bearer sk-handler-test-key" {
		t.Errorf("provider Authorization = %q, want Bearer sk-handler-test-key", stub.gotAuth)
	}
}

// TestAISubIssuesUnconfigured503: the honest-unconfigured contract
// extends to /subissues.
func TestAISubIssuesUnconfigured503(t *testing.T) {
	pool := newTestPool(t)
	e := testAIServer(t, pool, config.AIConfig{})
	cookie, slug, ident, email := setupAISummarize(t, e, pool)
	issueID := summarizeIssueHTTP(t, pool, slug, ident, email, "Migrate auth")

	rec := postAuthedJSON(t, e, http.MethodPost, "/api/v1/ai/subissues", cookie,
		fmt.Sprintf(`{"workspace_slug":%q,"project_identifier":%q,"issue_uuid":%q}`, slug, ident, issueID))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("subissues: status = %d, want 503", rec.Code)
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Error.Code != "ai_not_configured" {
		t.Errorf("code = %q, want ai_not_configured", env.Error.Code)
	}
}

// TestAISubIssuesProviderFailure502: malformed provider JSON surfaces
// as 502 with an honest message.
func TestAISubIssuesProviderFailure502(t *testing.T) {
	pool := newTestPool(t)
	stub := newAIProviderStub(t)
	stub.content = `[{"title": "truncated...`
	e := testAIServer(t, pool, stub.aiCfg())
	cookie, slug, ident, email := setupAISummarize(t, e, pool)
	issueID := summarizeIssueHTTP(t, pool, slug, ident, email, "Migrate auth")

	rec := postAuthedJSON(t, e, http.MethodPost, "/api/v1/ai/subissues", cookie,
		fmt.Sprintf(`{"workspace_slug":%q,"project_identifier":%q,"issue_uuid":%q}`, slug, ident, issueID))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("subissues: status = %d (body: %s), want 502", rec.Code, rec.Body.String())
	}
}

// TestAISubIssuesValidation: missing issue_uuid / scope → 400,
// unknown issue → 404 with no provider call.
func TestAISubIssuesValidation(t *testing.T) {
	pool := newTestPool(t)
	stub := newAIProviderStub(t)
	e := testAIServer(t, pool, stub.aiCfg())
	cookie, slug, ident := setupAIHTTP(t, e, pool)

	// Missing issue_uuid → 400.
	rec := postAuthedJSON(t, e, http.MethodPost, "/api/v1/ai/subissues", cookie,
		fmt.Sprintf(`{"workspace_slug":%q,"project_identifier":%q}`, slug, ident))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing issue_uuid: status = %d, want 400", rec.Code)
	}
	// Missing project scope → 400.
	rec = postAuthedJSON(t, e, http.MethodPost, "/api/v1/ai/subissues", cookie, `{"issue_uuid":"00000000-0000-0000-0000-000000000000"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing scope: status = %d, want 400", rec.Code)
	}
	// Malformed body → 400.
	rec = postAuthedJSON(t, e, http.MethodPost, "/api/v1/ai/subissues", cookie, `{"issue_uuid":`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed body: status = %d, want 400", rec.Code)
	}
	// Unknown issue UUID → 404 (not a 500, not a provider call).
	rec = postAuthedJSON(t, e, http.MethodPost, "/api/v1/ai/subissues", cookie,
		fmt.Sprintf(`{"workspace_slug":%q,"project_identifier":%q,"issue_uuid":"00000000-0000-0000-0000-000000000000"}`, slug, ident))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown issue: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
	if stub.gotAuth != "" {
		t.Error("provider called for missing issue; lookup must precede the provider call")
	}
}

// TestAISubIssuesNonMember404: outsiders get the tenancy-boundary 404
// and the provider is never called.
func TestAISubIssuesNonMember404(t *testing.T) {
	pool := newTestPool(t)
	stub := newAIProviderStub(t)
	stub.content = "x"
	e := testAIServer(t, pool, stub.aiCfg())
	_, slug, ident, email := setupAISummarize(t, e, pool)
	issueID := summarizeIssueHTTP(t, pool, slug, ident, email, "Migrate auth")

	outsider := loginTestUser(t, e, pool, uniqueEmail("ai-sub-out"), "ua", "10.9.1.4")

	rec := postAuthedJSON(t, e, http.MethodPost, "/api/v1/ai/subissues", outsider,
		fmt.Sprintf(`{"workspace_slug":%q,"project_identifier":%q,"issue_uuid":%q}`, slug, ident, issueID))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("outsider subissues: status = %d, want 404", rec.Code)
	}
	if stub.gotAuth != "" {
		t.Error("provider called for non-member; tenancy must resolve first")
	}
}
