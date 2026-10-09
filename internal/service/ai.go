package service

// AI assist backend (Cycle 5, C5T2): provider-agnostic OpenAI-compatible
// /chat/completions over stdlib net/http only — zero new dependencies.
// glance ships no model, no key, no vendor lock-in: any compatible
// endpoint works via GLANCE_AI_*.
//
// Security contract: the API key is sent ONLY as the Authorization
// header. It is NEVER logged and NEVER appears in error values —
// every error path below carries status codes and generic messages.
// The system prompts pin the model to the project's REAL states,
// labels, and priorities (passed in the prompt); returned names are
// mapped back to real rows case-insensitively and unknowns are dropped,
// so the model can never invent taxonomy.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"glance/internal/config"
)

var (
	// ErrAINotConfigured is returned when AI assist is called without a
	// configured provider key. The handler maps it to 503
	// (code ai_not_configured).
	ErrAINotConfigured = errors.New("service: ai assist is not configured")
	// ErrAIProvider is returned when the provider is unreachable, times
	// out, answers non-2xx, or returns unparseable JSON. The handler
	// maps it to 502 (code bad_gateway). Its message is deliberately
	// generic: it must never carry the key, the request, or the raw
	// provider body.
	ErrAIProvider = errors.New("service: ai provider error")
)

// aiTimeout bounds every provider round-trip. The caller's context can
// cancel sooner; this is the hard ceiling.
const aiTimeout = 30 * time.Second

// aiMaxBody caps provider response bodies — a misbehaving endpoint must
// not blow the process memory budget.
const aiMaxBody = 4 << 20

// aiClient is the stdlib-only OpenAI-compatible chat client. One value
// per call is fine: http.Client is safe for concurrent use but the
// handler path is request-scoped anyway.
type aiClient struct {
	baseURL string
	model   string
	apiKey  string
	http    *http.Client
}

func newAIClient(cfg config.AIConfig) *aiClient {
	return &aiClient{
		baseURL: strings.TrimSuffix(strings.TrimSpace(cfg.BaseURL), "/"),
		model:   cfg.Model,
		apiKey:  cfg.APIKey,
		http:    &http.Client{Timeout: aiTimeout},
	}
}

type aiChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type aiChatRequest struct {
	Model       string          `json:"model"`
	Messages    []aiChatMessage `json:"messages"`
	Temperature float64         `json:"temperature"`
	MaxTokens   int             `json:"max_tokens"`
}

type aiChatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// complete sends one system+user turn and returns the assistant content.
// Every failure collapses to ErrAIProvider with a generic message — the
// key stays in the header, never in the error.
func (c *aiClient) complete(ctx context.Context, system, user string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, aiTimeout)
	defer cancel()

	payload, err := json.Marshal(aiChatRequest{
		Model: c.model,
		Messages: []aiChatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		Temperature: 0.2,
		MaxTokens:   1024,
	})
	if err != nil {
		return "", ErrAIProvider
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return "", ErrAIProvider
	}
	req.Header.Set("Content-Type", "application/json")
	// The key leaves the process ONLY here, as a Bearer <redacted> Never in a URL,
	// never in a log line, never in an error value.
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", ErrAIProvider
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Drain a bounded prefix so a broken provider cannot hold the
		// connection open; the body itself is never surfaced (it could
		// echo anything, and errors stay generic by contract).
		io.CopyN(io.Discard, resp.Body, 4096)
		return "", fmt.Errorf("%w: status %d", ErrAIProvider, resp.StatusCode)
	}

	var out aiChatResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, aiMaxBody)).Decode(&out); err != nil {
		return "", fmt.Errorf("%w: bad response", ErrAIProvider)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("%w: empty response", ErrAIProvider)
	}
	return out.Choices[0].Message.Content, nil
}

// stripCodeFence removes a wrapping markdown code fence, which chat
// models love to add even when told to emit bare text/JSON.
func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	// Drop the opening fence line (may carry a language tag).
	if i := strings.Index(s, "\n"); i >= 0 {
		s = s[i+1:]
	} else {
		return ""
	}
	if i := strings.LastIndex(s, "```"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

const draftSystemPrompt = `You are an assistant drafting issue descriptions for a software issue tracker. ` +
	`Write a concise, clear issue description in Markdown based on the given title and optional context. ` +
	`Use short sections (Summary, Steps to reproduce, Expected) only when the title implies a bug; ` +
	`otherwise a short paragraph plus a bulleted acceptance-criteria list. ` +
	`Respond with ONLY the description text — no code fences, no preamble, no commentary.`

// DraftIssueDescription asks the provider to draft an issue description.
// Member-scoped: the actor must belong to the workspace (non-members get
// ErrNotFound — the tenancy boundary — and the provider is never called).
func DraftIssueDescription(ctx context.Context, pool *pgxpool.Pool, aiCfg config.AIConfig,
	wsSlug, identifier, actorID, title, extraContext string) (string, error) {
	if !aiCfg.Configured() {
		return "", ErrAINotConfigured
	}
	if _, _, _, err := resolveIssueProject(ctx, pool, wsSlug, identifier, actorID); err != nil {
		return "", err
	}
	user := "Issue title: " + strings.TrimSpace(title)
	if strings.TrimSpace(extraContext) != "" {
		user += "\nAdditional context: " + strings.TrimSpace(extraContext)
	}
	content, err := newAIClient(aiCfg).complete(ctx, draftSystemPrompt, user)
	if err != nil {
		return "", err
	}
	return stripCodeFence(content), nil
}

// TriageResult is the model's triage suggestion mapped to the project's
// real taxonomy. LabelNames are canonical workspace label names
// (unknowns dropped); StateName is the canonical state name or nil when
// the model picked nothing real.
type TriageResult struct {
	Priority   int      `json:"priority"`
	LabelNames []string `json:"label_names"`
	StateName  *string  `json:"state_name,omitempty"`
}

type aiTriageRaw struct {
	Priority   any      `json:"priority"`
	LabelNames []string `json:"label_names"`
	StateName  *string  `json:"state_name"`
}

// triageSystemPrompt pins the model to the project's actual taxonomy:
// real state names (with groups), real workspace label names, and the
// fixed 0-4 priority vocabulary. Unknown values are dropped by the
// mapper below — the model cannot invent taxonomy.
func triageSystemPrompt(states []State, labels []Label) string {
	var sb strings.Builder
	sb.WriteString("You are triaging a software issue. Suggest a priority, labels, and a state.\n")
	sb.WriteString("Respond with ONLY a JSON object, no code fences, no commentary:\n")
	sb.WriteString(`{"priority": <0-4 or one of the names below>, "label_names": ["..."], "state_name": "<name>" or null}` + "\n\n")
	sb.WriteString("Priorities (use the number or name): 0=None, 1=Low, 2=Medium, 3=High, 4=Urgent.\n\n")
	sb.WriteString("Valid states (use state_name exactly as written, or null if none fits — prefer an unstarted/backlog state):\n")
	for _, s := range states {
		fmt.Fprintf(&sb, "- %s (group: %s)\n", s.Name, s.Group)
	}
	sb.WriteString("\nValid labels (use label_names exactly as written; omit unknown ones):\n")
	for _, l := range labels {
		fmt.Fprintf(&sb, "- %s\n", l.Name)
	}
	sb.WriteString("\nNever invent a state, label, or priority outside these lists.")
	return sb.String()
}

// mapPriority converts the model's priority (number or name) to 0-4.
// Anything unrecognized falls back to 0 (None) rather than failing the
// whole triage — a missing suggestion is less wrong than a dropped one.
func mapPriority(raw any) int {
	switch v := raw.(type) {
	case float64:
		if n := int(v); n >= 0 && n <= 4 {
			return n
		}
	case string:
		if n, ok := importPriorityNames[strings.ToLower(strings.TrimSpace(v))]; ok {
			return n
		}
	}
	return 0
}

// TriageIssue asks the provider to triage an issue and maps the answer
// onto the project's real states and workspace labels. Member-scoped
// like DraftIssueDescription.
func TriageIssue(ctx context.Context, pool *pgxpool.Pool, aiCfg config.AIConfig,
	wsSlug, identifier, actorID, title, description string) (TriageResult, error) {
	out := TriageResult{LabelNames: []string{}}
	if !aiCfg.Configured() {
		return out, ErrAINotConfigured
	}
	if _, _, _, err := resolveIssueProject(ctx, pool, wsSlug, identifier, actorID); err != nil {
		return out, err
	}
	states, err := ListStates(ctx, pool, wsSlug, identifier, actorID)
	if err != nil {
		return out, err
	}
	labels, err := ListLabels(ctx, pool, wsSlug, identifier, actorID)
	if err != nil {
		return out, err
	}

	user := "Issue title: " + strings.TrimSpace(title)
	if strings.TrimSpace(description) != "" {
		user += "\nIssue description: " + strings.TrimSpace(description)
	}
	content, err := newAIClient(aiCfg).complete(ctx, triageSystemPrompt(states, labels), user)
	if err != nil {
		return out, err
	}

	var raw aiTriageRaw
	if err := json.Unmarshal([]byte(stripCodeFence(content)), &raw); err != nil {
		return out, fmt.Errorf("%w: bad response", ErrAIProvider)
	}

	// Map label names to canonical workspace labels, case-insensitive;
	// drop unknowns, dedupe, preserve model order.
	canonLabels := make(map[string]string, len(labels))
	for _, l := range labels {
		canonLabels[strings.ToLower(l.Name)] = l.Name
	}
	seen := make(map[string]struct{}, len(raw.LabelNames))
	for _, name := range raw.LabelNames {
		canon, ok := canonLabels[strings.ToLower(strings.TrimSpace(name))]
		if !ok {
			continue
		}
		if _, dup := seen[canon]; dup {
			continue
		}
		seen[canon] = struct{}{}
		out.LabelNames = append(out.LabelNames, canon)
	}

	// Map the state name to the canonical project state name; unknown
	// (or null) → nil. Never invent.
	if raw.StateName != nil {
		canonStates := make(map[string]string, len(states))
		for _, s := range states {
			canonStates[strings.ToLower(s.Name)] = s.Name
		}
		if canon, ok := canonStates[strings.ToLower(strings.TrimSpace(*raw.StateName))]; ok {
			out.StateName = &canon
		}
	}

	out.Priority = mapPriority(raw.Priority)
	return out, nil
}
