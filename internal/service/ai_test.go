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
	wsSlug, ident, actorID := setupAIProject(t, pool)

	_, err := DraftIssueDescription(context.Background(), pool, config.AIConfig{}, wsSlug, ident, actorID, "title", "")
	if !errors.Is(err, ErrAINotConfigured) {
		t.Fatalf("err = %v, want ErrAINotConfigured", err)
	}
}

func TestDraftDescription_NonMemberNotFound(t *testing.T) {
	pool := newTestPool(t)
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
	wsSlug, ident, actorID := setupAIProject(t, pool)

	_, err := TriageIssue(context.Background(), pool, config.AIConfig{}, wsSlug, ident, actorID, "title", "")
	if !errors.Is(err, ErrAINotConfigured) {
		t.Fatalf("err = %v, want ErrAINotConfigured", err)
	}
}

func TestAIProvider500(t *testing.T) {
	pool := newTestPool(t)
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

// ---------- C25T0: AI issue summary ----------

func tiptapDocForTest(texts ...string) json.RawMessage {
	parts := make([]string, 0, len(texts))
	for _, t := range texts {
		raw, _ := json.Marshal(t)
		parts = append(parts, `{"type":"paragraph","content":[{"type":"text","text":`+string(raw)+`}]}`)
	}
	return json.RawMessage(`{"type":"doc","content":[` + strings.Join(parts, ",") + `]}`)
}

func setupSummaryIssue(t *testing.T, pool *pgxpool.Pool) (wsSlug, ident, actorID, issueID string) {
	t.Helper()
	ctx := context.Background()
	wsSlug, ident, actorID = setupAIProject(t, pool)
	iss := createTestIssue(t, pool, wsSlug, ident, actorID, "Login button broken")
	issueID = iss.ID
	// Rich prompt material: a description, two comments, and a priority
	// change activity.
	desc := tiptapDocForTest("The button does nothing on Safari.", "Console shows no errors.")
	if _, err := UpdateIssue(ctx, pool, wsSlug, ident, issueID, actorID,
		IssuePatch{Description: PatchField[json.RawMessage]{Set: true, Value: &desc}}); err != nil {
		t.Fatalf("UpdateIssue description: %v", err)
	}
	for _, text := range []string{"Reproduced on Safari 26.", "Suspect the click handler was removed in #123."} {
		if _, err := CreateComment(ctx, pool, wsSlug, ident, issueID, actorID, tiptapDocForTest(text), nil); err != nil {
			t.Fatalf("CreateComment: %v", err)
		}
	}
	if _, err := UpdateIssue(ctx, pool, wsSlug, ident, issueID, actorID,
		IssuePatch{Priority: intPtr(3)}); err != nil {
		t.Fatalf("UpdateIssue priority: %v", err)
	}
	return wsSlug, ident, actorID, issueID
}

func TestSummarizeIssue_Parses(t *testing.T) {
	pool := newTestPool(t)
	wsSlug, ident, actorID, issueID := setupSummaryIssue(t, pool)

	stub := newAIStub(t)
	stub.content = "## TL;DR\n\nWhat: broken login button on Safari."
	cfg := stub.cfg(aiTestKey)

	got, err := SummarizeIssue(context.Background(), pool, cfg, wsSlug, ident, issueID, actorID)
	if err != nil {
		t.Fatalf("SummarizeIssue: %v", err)
	}
	if got != stub.content {
		t.Errorf("summary = %q, want stub content verbatim", got)
	}
	if stub.requestN != 1 {
		t.Fatalf("provider hit %d times, want 1", stub.requestN)
	}
	if stub.gotAuth != "Bearer "+aiTestKey {
		t.Errorf("Authorization header = %q, want Bearer <key>", stub.gotAuth)
	}
	// The prompt must assemble the full issue context: title,
	// description, comments, and recent activity.
	for _, want := range []string{
		"Login button broken",     // title
		"does nothing on Safari",  // description text
		"Reproduced on Safari 26", // comment text
		"priority",                // activity line
	} {
		if !strings.Contains(stub.gotUser, want) {
			t.Errorf("user prompt missing %q:\n%s", want, stub.gotUser)
		}
	}
	// The system prompt pins the TL;DR shape.
	for _, want := range []string{"What", "Why", "Current status", "Open questions"} {
		if !strings.Contains(stub.gotSystem, want) {
			t.Errorf("system prompt missing %q:\n%s", want, stub.gotSystem)
		}
	}
}

func TestSummarizeIssue_StripsCodeFence(t *testing.T) {
	pool := newTestPool(t)
	wsSlug, ident, actorID, issueID := setupSummaryIssue(t, pool)

	stub := newAIStub(t)
	stub.content = "```markdown\nA tidy summary.\n```"
	cfg := stub.cfg(aiTestKey)

	got, err := SummarizeIssue(context.Background(), pool, cfg, wsSlug, ident, issueID, actorID)
	if err != nil {
		t.Fatalf("SummarizeIssue: %v", err)
	}
	if got != "A tidy summary." {
		t.Errorf("fence not stripped: %q", got)
	}
}

func TestSummarizeIssue_NotConfigured(t *testing.T) {
	pool := newTestPool(t)
	wsSlug, ident, actorID, issueID := setupSummaryIssue(t, pool)

	_, err := SummarizeIssue(context.Background(), pool, config.AIConfig{}, wsSlug, ident, issueID, actorID)
	if !errors.Is(err, ErrAINotConfigured) {
		t.Fatalf("err = %v, want ErrAINotConfigured", err)
	}
}

func TestSummarizeIssue_NonMemberNotFound(t *testing.T) {
	pool := newTestPool(t)
	wsSlug, ident, _, issueID := setupSummaryIssue(t, pool)
	outsider := createTestUser(t, pool, uniqueTestEmail("ai-sum-outsider"))

	stub := newAIStub(t)
	cfg := stub.cfg(aiTestKey)

	_, err := SummarizeIssue(context.Background(), pool, cfg, wsSlug, ident, issueID, outsider)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound (tenancy boundary)", err)
	}
	if stub.requestN != 0 {
		t.Error("provider hit for non-member; tenancy must resolve before any provider call")
	}
}

// Summarize is member (15+)-gated: a guest can read the issue but may not
// spend AI budget. Guests get the same tenancy-boundary 404.
func TestSummarizeIssue_GuestForbidden(t *testing.T) {
	pool := newTestPool(t)
	wsSlug, ident, actorID, issueID := setupSummaryIssue(t, pool)
	guest := createTestUser(t, pool, uniqueTestEmail("ai-sum-guest"))
	if err := UpsertMember(context.Background(), pool, wsSlug, actorID, guest, RoleGuest); err != nil {
		t.Fatalf("UpsertMember guest: %v", err)
	}

	stub := newAIStub(t)
	cfg := stub.cfg(aiTestKey)

	_, err := SummarizeIssue(context.Background(), pool, cfg, wsSlug, ident, issueID, guest)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound for guest (member-only endpoint)", err)
	}
	if stub.requestN != 0 {
		t.Error("provider hit for guest; member gate must fire first")
	}
}

func TestSummarizeIssue_IssueNotFound(t *testing.T) {
	pool := newTestPool(t)
	wsSlug, ident, actorID, _ := setupSummaryIssue(t, pool)

	stub := newAIStub(t)
	cfg := stub.cfg(aiTestKey)

	_, err := SummarizeIssue(context.Background(), pool, cfg, wsSlug, ident, "00000000-0000-0000-0000-000000000000", actorID)
	if !errors.Is(err, ErrIssueNotFound) {
		t.Fatalf("err = %v, want ErrIssueNotFound", err)
	}
	if stub.requestN != 0 {
		t.Error("provider hit for missing issue")
	}
}

func TestSummarizeIssue_Provider500(t *testing.T) {
	pool := newTestPool(t)
	wsSlug, ident, actorID, issueID := setupSummaryIssue(t, pool)

	stub := newAIStub(t)
	stub.status = http.StatusInternalServerError
	cfg := stub.cfg(aiTestKey)

	_, err := SummarizeIssue(context.Background(), pool, cfg, wsSlug, ident, issueID, actorID)
	if !errors.Is(err, ErrAIProvider) {
		t.Fatalf("err = %v, want ErrAIProvider", err)
	}
}

// buildSummarizeUserPrompt is a pure function: unit-test the prompt
// assembly (sections + input token cap) without DB or provider.
func TestBuildSummarizeUserPrompt_Sections(t *testing.T) {
	prompt := buildSummarizeUserPrompt(summarizeInput{
		Title:       "Broken login",
		Description: "Button does nothing.",
		Comments: []summarizeComment{
			{Actor: "Ada", Text: "Reproduced."},
		},
		History: []summarizeActivityLine{
			{Actor: "Ada", Field: "priority", Old: "1", New: "3"},
		},
	})
	for _, want := range []string{
		"Broken login", "Button does nothing.", "Ada", "Reproduced.", "priority", "1", "3",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q:\n%s", want, prompt)
		}
	}
	if len(prompt) > aiSummarizeMaxChars {
		t.Errorf("prompt length %d exceeds cap %d", len(prompt), aiSummarizeMaxChars)
	}
}

func TestBuildSummarizeUserPrompt_CapsInput(t *testing.T) {
	big := strings.Repeat("x", aiSummarizeMaxChars*2)
	prompt := buildSummarizeUserPrompt(summarizeInput{Title: "t", Description: big})
	if len(prompt) > aiSummarizeMaxChars+100 {
		t.Errorf("prompt not capped: len %d", len(prompt))
	}
	if !strings.Contains(prompt, "truncated to fit the AI context") {
		t.Errorf("capped prompt must carry a truncation marker:\n%s", prompt[len(prompt)-200:])
	}
}

func TestTiptapPlainText(t *testing.T) {
	got := tiptapPlainText(tiptapDocForTest("hello", "world"))
	if got != "hello\nworld" {
		t.Errorf("tiptapPlainText = %q, want %q", got, "hello\nworld")
	}
	if tiptapPlainText(nil) != "" {
		t.Errorf("nil doc must yield empty string")
	}
	// Non-JSON content falls back to the raw string (never an error).
	if got := tiptapPlainText(json.RawMessage("just text")); got != "just text" {
		t.Errorf("plain fallback = %q, want %q", got, "just text")
	}
	// Malformed JSON is returned raw, not dropped.
	if got := tiptapPlainText(json.RawMessage("{oops")); got != "{oops" {
		t.Errorf("malformed fallback = %q, want %q", got, "{oops")
	}
}
