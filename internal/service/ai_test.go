package service

// AI assist backend tests (C5T2): provider-agnostic OpenAI-compatible
// /chat/completions over stdlib HTTP. Every test runs against a real
// httptest stub provider — no network, no key leaves the test process.
// All tests run against the real test database — no skips. Reuses the
// harness from workspace_test.go / project_test.go.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"glance/internal/config"
)

// aiStub is an OpenAI-compatible /chat/completions stub. It records the
// last request so tests can assert the Bearer <redacted> carried the key and the
// system prompt listed the project's real taxonomy.
type aiStub struct {
	t         *testing.T
	server    *httptest.Server
	status    int    // HTTP status to answer; 0 means 200
	content   string // raw choices[0].message.content
	gotAuth   string
	gotPath   string
	gotSystem string
	gotUser   string
	gotModel  string
	requestN  int
}

func newAIStub(t *testing.T) *aiStub {
	t.Helper()
	s := &aiStub{t: t}
	s.server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.server.Close)
	return s
}

type chatMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func (s *aiStub) serve(w http.ResponseWriter, r *http.Request) {
	s.requestN++
	s.gotAuth = r.Header.Get("Authorization")
	s.gotPath = r.URL.Path
	var body struct {
		Model    string    `json:"model"`
		Messages []chatMsg `json:"messages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	s.gotModel = body.Model
	for _, m := range body.Messages {
		if m.Role == "system" {
			s.gotSystem = m.Content
		}
		if m.Role == "user" {
			s.gotUser = m.Content
		}
	}
	status := s.status
	if status == 0 {
		status = http.StatusOK
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if status == http.StatusOK {
		raw, _ := json.Marshal(s.content)
		fmt.Fprintf(w, `{"id":"chatcmpl-test","object":"chat.completion","created":1,"model":%q,"choices":[{"index":0,"message":{"role":"assistant","content":%s},"finish_reason":"stop"}],"usage":{}}`, body.Model, raw)
	} else {
		fmt.Fprintf(w, `{"error":{"message":"stub provider failure","type":"server_error"}}`)
	}
}

func (s *aiStub) cfg(key string) config.AIConfig {
	return config.AIConfig{BaseURL: s.server.URL, Model: "stub-model", APIKey: key}
}

const aiTestKey = "sk-ai-test-key-never-log-me"

func setupAIProject(t *testing.T, pool *pgxpool.Pool) (wsSlug, ident, actorID string) {
	t.Helper()
	ctx := context.Background()
	actorID = createTestUser(t, pool, uniqueTestEmail("ai"))
	wsSlug = uniqueTestSlug("ai")
	createTestWorkspace(t, pool, "AI Co", wsSlug, actorID)
	ident = uniqueTestIdentifier()
	createTestProject(t, pool, wsSlug, actorID, "AI Proj", ident)
	// Workspace-scoped labels the triage model must map to (never invent).
	for _, name := range []string{"bug", "frontend"} {
		if _, err := CreateLabel(ctx, pool, wsSlug, ident, actorID, LabelInput{Name: name}); err != nil {
			t.Fatalf("CreateLabel %q: %v", name, err)
		}
	}
	return wsSlug, ident, actorID
}

func TestDraftDescription_Parses(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	wsSlug, ident, actorID := setupAIProject(t, pool)

	stub := newAIStub(t)
	stub.content = "## Summary\n\nThe login button does nothing on click.\n\n## Steps\n\n1. Open the app\n2. Click login"
	cfg := stub.cfg(aiTestKey)

	got, err := DraftIssueDescription(context.Background(), pool, cfg, wsSlug, ident, actorID, "Login button broken", "happens on Safari")
	if err != nil {
		t.Fatalf("DraftIssueDescription: %v", err)
	}
	if got != stub.content {
		t.Errorf("draft = %q, want stub content verbatim", got)
	}
	if stub.requestN != 1 {
		t.Fatalf("provider hit %d times, want 1", stub.requestN)
	}
	if stub.gotAuth != "Bearer "+aiTestKey {
		t.Errorf("Authorization header = %q, want Bearer <key>", stub.gotAuth)
	}
	if stub.gotPath != "/chat/completions" {
		t.Errorf("path = %q, want /chat/completions", stub.gotPath)
	}
	if !strings.Contains(stub.gotUser, "Login button broken") {
		t.Errorf("user prompt missing title: %q", stub.gotUser)
	}
	if !strings.Contains(stub.gotUser, "Safari") {
		t.Errorf("user prompt missing context: %q", stub.gotUser)
	}
}

func TestDraftDescription_StripsCodeFence(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	wsSlug, ident, actorID := setupAIProject(t, pool)

	stub := newAIStub(t)
	stub.content = "```markdown\nA clean description.\n```"
	cfg := stub.cfg(aiTestKey)

	got, err := DraftIssueDescription(context.Background(), pool, cfg, wsSlug, ident, actorID, "title", "")
	if err != nil {
		t.Fatalf("DraftIssueDescription: %v", err)
	}
	if got != "A clean description." {
		t.Errorf("fence not stripped: %q", got)
	}
}

func TestDraftDescription_NotConfigured(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	wsSlug, ident, actorID := setupAIProject(t, pool)

	_, err := DraftIssueDescription(context.Background(), pool, config.AIConfig{}, wsSlug, ident, actorID, "title", "")
	if !errors.Is(err, ErrAINotConfigured) {
		t.Fatalf("err = %v, want ErrAINotConfigured", err)
	}
}

func TestDraftDescription_NonMemberNotFound(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	wsSlug, ident, _ := setupAIProject(t, pool)
	outsider := createTestUser(t, pool, uniqueTestEmail("ai-outsider"))

	stub := newAIStub(t)
	cfg := stub.cfg(aiTestKey)

	_, err := DraftIssueDescription(context.Background(), pool, cfg, wsSlug, ident, outsider, "title", "")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound (tenancy boundary)", err)
	}
	if stub.requestN != 0 {
		t.Errorf("provider hit for non-member; tenancy must resolve before any provider call")
	}
}

func TestTriage_MapsToRealLabelsAndStates(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	wsSlug, ident, actorID := setupAIProject(t, pool)

	stub := newAIStub(t)
	stub.content = `{"priority": 3, "label_names": ["BUG", "frontend", "does-not-exist"], "state_name": "in progress"}`
	cfg := stub.cfg(aiTestKey)

	got, err := TriageIssue(context.Background(), pool, cfg, wsSlug, ident, actorID, "Login button broken", "does nothing on click")
	if err != nil {
		t.Fatalf("TriageIssue: %v", err)
	}
	if got.Priority != 3 {
		t.Errorf("priority = %d, want 3", got.Priority)
	}
	// Case-insensitive mapping to canonical names; unknown label dropped.
	wantLabels := []string{"bug", "frontend"}
	if len(got.LabelNames) != len(wantLabels) {
		t.Fatalf("labels = %v, want %v", got.LabelNames, wantLabels)
	}
	for i, w := range wantLabels {
		if got.LabelNames[i] != w {
			t.Errorf("labels[%d] = %q, want %q", i, got.LabelNames[i], w)
		}
	}
	if got.StateName == nil || *got.StateName != "In Progress" {
		t.Errorf("state_name = %v, want canonical \"In Progress\"", got.StateName)
	}
	// The system prompt must list the project's real taxonomy so the
	// model cannot invent states/labels.
	for _, want := range []string{"Backlog", "In Progress", "bug", "frontend", "0=None", "4=Urgent"} {
		if !strings.Contains(stub.gotSystem, want) {
			t.Errorf("system prompt missing %q:\n%s", want, stub.gotSystem)
		}
	}
}

func TestTriage_PriorityNameString(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	wsSlug, ident, actorID := setupAIProject(t, pool)

	stub := newAIStub(t)
	stub.content = `{"priority": "urgent", "label_names": [], "state_name": null}`
	cfg := stub.cfg(aiTestKey)

	got, err := TriageIssue(context.Background(), pool, cfg, wsSlug, ident, actorID, "title", "")
	if err != nil {
		t.Fatalf("TriageIssue: %v", err)
	}
	if got.Priority != 4 {
		t.Errorf("priority = %d, want 4 for \"urgent\"", got.Priority)
	}
	if len(got.LabelNames) != 0 {
		t.Errorf("labels = %v, want empty (never nil)", got.LabelNames)
	}
	if got.StateName != nil {
		t.Errorf("state_name = %v, want nil for null", *got.StateName)
	}
}

func TestTriage_UnknownStateDropped(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	wsSlug, ident, actorID := setupAIProject(t, pool)

	stub := newAIStub(t)
	stub.content = `{"priority": 1, "label_names": [], "state_name": "No Such State"}`
	cfg := stub.cfg(aiTestKey)

	got, err := TriageIssue(context.Background(), pool, cfg, wsSlug, ident, actorID, "title", "")
	if err != nil {
		t.Fatalf("TriageIssue: %v", err)
	}
	if got.StateName != nil {
		t.Errorf("state_name = %q, want nil for unknown state (never invent)", *got.StateName)
	}
}

func TestTriage_NotConfigured(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	wsSlug, ident, actorID := setupAIProject(t, pool)

	_, err := TriageIssue(context.Background(), pool, config.AIConfig{}, wsSlug, ident, actorID, "title", "")
	if !errors.Is(err, ErrAINotConfigured) {
		t.Fatalf("err = %v, want ErrAINotConfigured", err)
	}
}

func TestAIProvider500(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	wsSlug, ident, actorID := setupAIProject(t, pool)

	stub := newAIStub(t)
	stub.status = http.StatusInternalServerError
	cfg := stub.cfg(aiTestKey)

	_, err := DraftIssueDescription(context.Background(), pool, cfg, wsSlug, ident, actorID, "title", "")
	if !errors.Is(err, ErrAIProvider) {
		t.Fatalf("draft err = %v, want ErrAIProvider", err)
	}
	_, err = TriageIssue(context.Background(), pool, cfg, wsSlug, ident, actorID, "title", "")
	if !errors.Is(err, ErrAIProvider) {
		t.Fatalf("triage err = %v, want ErrAIProvider", err)
	}
}

// TestAIKeyNeverLogged is the redaction contract: the API key must not
// appear in logs or error strings on ANY path — success, provider
// error, or unparseable provider JSON.
func TestAIKeyNeverLogged(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	wsSlug, ident, actorID := setupAIProject(t, pool)

	var buf strings.Builder
	old := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(old) })

	stub := newAIStub(t)
	cfg := stub.cfg(aiTestKey)

	var errs []string
	desc, err := DraftIssueDescription(context.Background(), pool, cfg, wsSlug, ident, actorID, "title", "")
	if err != nil {
		errs = append(errs, err.Error())
	}
	_ = desc
	// Provider 500 path.
	stub.status = http.StatusInternalServerError
	_, err = DraftIssueDescription(context.Background(), pool, cfg, wsSlug, ident, actorID, "title", "")
	if err != nil {
		errs = append(errs, err.Error())
	}
	// Garbage JSON path.
	stub.status = 0
	stub.content = "not json at all {{{"
	_, err = TriageIssue(context.Background(), pool, cfg, wsSlug, ident, actorID, "title", "")
	if err != nil {
		errs = append(errs, err.Error())
	}

	out := buf.String()
	if strings.Contains(out, aiTestKey) {
		t.Errorf("API key appeared in log output:\n%s", out)
	}
	for _, e := range errs {
		if strings.Contains(e, aiTestKey) {
			t.Errorf("API key appeared in error string: %q", e)
		}
	}
	if len(errs) != 2 {
		t.Fatalf("expected 2 error paths, got %d", len(errs))
	}
}
