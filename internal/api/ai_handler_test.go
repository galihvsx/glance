package api

// AI assist HTTP endpoint tests (C5T2): the draft/triage endpoints end
// to end against a real database and a stub OpenAI-compatible provider.
// Real test database, no skips.

import (
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
	migrateTestDB(t, pool)
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
	migrateTestDB(t, pool)
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
	migrateTestDB(t, pool)
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
	migrateTestDB(t, pool)
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
	migrateTestDB(t, pool)
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
	migrateTestDB(t, pool)
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
	migrateTestDB(t, pool)
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
