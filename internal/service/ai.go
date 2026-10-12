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

	"github.com/jackc/pgx/v5"
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

// ---------- issue summary (C25T0) ----------

const summarizeSystemPrompt = `You are summarizing a software issue for a busy engineer who has not read it. ` +
	`Use the title, description, comments, and recent activity provided. ` +
	`Respond with a concise Markdown summary using exactly these four sections — ` +
	`omit a section only when there is genuinely nothing to say:\n\n` +
	`- What: what the issue is about (one or two sentences).\n` +
	`- Why: why it matters — the problem, motivation, or impact.\n` +
	`- Current status: where things stand — progress, blockers, decisions made.\n` +
	`- Open questions: what is still unresolved or needs an answer.\n\n` +
	`Keep the whole summary under 200 words. Respond with ONLY the summary — no code fences, no preamble, no commentary.`

// aiSummarizeMaxChars caps the user prompt at roughly 3k tokens so a
// long comment thread or description cannot blow the provider's context
// (or the server's memory budget).
const aiSummarizeMaxChars = 12000

// aiSummarizeMaxComments caps how many comments (oldest-thread tail =
// most recent) feed the prompt.
const aiSummarizeMaxComments = 20

// aiSummarizeMaxHistory caps how many recent activity rows feed the
// prompt (chronological tail = most recent).
const aiSummarizeMaxHistory = 20

// summarizeComment is one comment rendered into the summary prompt.
type summarizeComment struct {
	Actor string
	Text  string
}

// summarizeActivityLine is one recent activity row rendered into the
// summary prompt.
type summarizeActivityLine struct {
	Actor string
	Field string
	Old   string
	New   string
}

// summarizeInput is the assembled issue context for a summary prompt.
// buildSummarizeUserPrompt is a pure function so the prompt assembly
// (sections + input cap) is unit-testable without a DB or provider.
type summarizeInput struct {
	Title       string
	Description string
	Comments    []summarizeComment
	History     []summarizeActivityLine
}

// buildSummarizeUserPrompt assembles the user turn for the summarize
// call and caps it at aiSummarizeMaxChars with a truncation marker.
func buildSummarizeUserPrompt(in summarizeInput) string {
	var sb strings.Builder
	sb.WriteString("Issue: " + strings.TrimSpace(in.Title) + "\n\n")
	sb.WriteString("Description:\n")
	if d := strings.TrimSpace(in.Description); d != "" {
		sb.WriteString(d)
	} else {
		sb.WriteString("(no description)")
	}
	sb.WriteString("\n\n")
	if len(in.Comments) > 0 {
		fmt.Fprintf(&sb, "Comments (%d, oldest first):\n", len(in.Comments))
		for _, c := range in.Comments {
			fmt.Fprintf(&sb, "- %s: %s\n", c.Actor, c.Text)
		}
		sb.WriteString("\n")
	}
	if len(in.History) > 0 {
		fmt.Fprintf(&sb, "Recent activity (%d, oldest first):\n", len(in.History))
		for _, h := range in.History {
			fmt.Fprintf(&sb, "- %s changed %s: %s → %s\n", h.Actor, h.Field, h.Old, h.New)
		}
	}
	out := sb.String()
	if len(out) > aiSummarizeMaxChars {
		out = out[:aiSummarizeMaxChars] + "\n\n[…truncated to fit the AI context]"
	}
	return out
}

// tiptapPlainText extracts plain text from a TipTap JSON doc by
// collecting every text node, one per line. Non-JSON or malformed
// content falls back to the raw string — prompt material is never an
// error path.
func tiptapPlainText(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if s == "" {
		return ""
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return s
	}
	var texts []string
	var walk func(x any)
	walk = func(x any) {
		switch t := x.(type) {
		case map[string]any:
			if txt, ok := t["text"].(string); ok {
				texts = append(texts, txt)
			}
			if content, ok := t["content"].([]any); ok {
				for _, c := range content {
					walk(c)
				}
			}
		case []any:
			for _, c := range t {
				walk(c)
			}
		case string:
			texts = append(texts, t)
		}
	}
	walk(v)
	return strings.Join(texts, "\n")
}

// shortJSONValue renders a raw JSON value as a one-line prompt token:
// strings unquoted, everything else compacted, long values truncated.
func shortJSONValue(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if s == "" {
		return "—"
	}
	var v any
	if err := json.Unmarshal(raw, &v); err == nil {
		if str, ok := v.(string); ok {
			s = str
		} else if b, err := json.Marshal(v); err == nil {
			s = string(b)
		}
	}
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 120 {
		s = s[:117] + "…"
	}
	return s
}

// actorRefName renders an ActorRef's name for prompts, falling back
// to the email when the name is unset.
func actorRefName(a ActorRef) string {
	if a.Name != nil && strings.TrimSpace(*a.Name) != "" {
		return *a.Name
	}
	return a.Email
}

// flattenCommentTexts flattens a comment tree depth-first (oldest
// first, replies nested after their parent) into prompt comments,
// keeping only the most recent aiSummarizeMaxComments.
func flattenCommentTexts(nodes []*CommentNode) []summarizeComment {
	var all []summarizeComment
	var walk func(n *CommentNode)
	walk = func(n *CommentNode) {
		text := tiptapPlainText(n.Content)
		if len(text) > 800 {
			text = text[:797] + "…"
		}
		all = append(all, summarizeComment{Actor: actorRefName(n.Actor), Text: text})
		for _, r := range n.Replies {
			walk(r)
		}
	}
	for _, n := range nodes {
		walk(n)
	}
	if len(all) > aiSummarizeMaxComments {
		all = all[len(all)-aiSummarizeMaxComments:]
	}
	return all
}

// SummarizeIssue asks the provider to write a TL;DR of an existing
// issue from its title, description, comments, and recent activity.
// Member-scoped: the actor must hold role >= RoleMember (guests read but
// may not spend AI budget); non-members and guests get ErrNotFound —
// the tenancy boundary — and the provider is never called. A missing
// issue UUID surfaces ErrIssueNotFound.
func SummarizeIssue(ctx context.Context, pool *pgxpool.Pool, aiCfg config.AIConfig,
	wsSlug, identifier, issueUUID, actorID string) (string, error) {
	if !aiCfg.Configured() {
		return "", ErrAINotConfigured
	}
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return "", err
	}
	_, projectID, role, err := resolveIssueProject(ctx, pool, wsSlug, ident, actorID)
	if err != nil {
		return "", err
	}
	if role < RoleMember {
		return "", ErrNotFound
	}
	item, err := getIssueRow(ctx, pool, ident, projectID, issueUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return "", ErrIssueNotFound
		}
		return "", err
	}

	comments, err := ListComments(ctx, pool, wsSlug, ident, issueUUID, actorID)
	if err != nil {
		return "", err
	}
	history, err := GetIssueHistory(ctx, pool, wsSlug, ident, issueUUID, actorID)
	if err != nil {
		return "", err
	}

	in := summarizeInput{
		Title:       item.Name,
		Description: tiptapPlainText(item.Description),
		Comments:    flattenCommentTexts(comments),
	}
	if len(history) > aiSummarizeMaxHistory {
		history = history[len(history)-aiSummarizeMaxHistory:]
	}
	for _, h := range history {
		in.History = append(in.History, summarizeActivityLine{
			Actor: actorRefName(h.Actor),
			Field: h.Field,
			Old:   shortJSONValue(h.OldValue),
			New:   shortJSONValue(h.NewValue),
		})
	}

	content, err := newAIClient(aiCfg).complete(ctx, summarizeSystemPrompt,
		buildSummarizeUserPrompt(in))
	if err != nil {
		return "", err
	}
	return stripCodeFence(content), nil
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

// ---------- AI sub-issue breakdown (C25T1) ----------

const subIssuesSystemPrompt = `You are breaking a software issue into concrete sub-tasks for a busy engineer. ` +
	`Respond with ONLY a JSON array — no code fences, no preamble, no commentary — of at most the requested number of items, each with a "title" and optionally a "description": ` +
	`[{"title": "...", "description": "..."}, ...]. ` +
	`Titles must be concrete and actionable (one clear deliverable each), no duplicates, and cover the whole issue without scope creep. ` +
	`Descriptions are one or two sentences of acceptance context; omit a description only when the title is self-explanatory.`

// aiSubIssuesMaxChars caps the breakdown prompt the same way the
// summary prompt is capped (see aiSummarizeMaxChars).
const aiSubIssuesMaxChars = 12000

// subIssueMaxDefault / subIssueMaxCap are the default and hard clamp
// for the per-request `max` the UI sends.
const subIssueMaxDefault = 5
const subIssueMaxCap = 10

// SubIssueSuggestion is one AI-proposed sub-task: an actionable title
// plus an optional one-to-two-sentence acceptance description.
type SubIssueSuggestion struct {
	Title       string  `json:"title"`
	Description *string `json:"description,omitempty"`
}

// subIssuesInput is the assembled issue context for a breakdown
// prompt. Comments reuse the summarize comment type and flattening.
type subIssuesInput struct {
	Title       string
	Description string
	Comments    []summarizeComment
}

// buildSubIssuesUserPrompt assembles the user turn for the breakdown
// call and caps it at aiSubIssuesMaxChars with a truncation marker.
// Pure: unit-testable without a DB or provider.
func buildSubIssuesUserPrompt(in subIssuesInput, max int) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Break the following issue into at most %d concrete sub-tasks.\n\n", max)
	sb.WriteString("Issue: " + strings.TrimSpace(in.Title) + "\n\n")
	sb.WriteString("Description:\n")
	if d := strings.TrimSpace(in.Description); d != "" {
		sb.WriteString(d)
	} else {
		sb.WriteString("(no description)")
	}
	sb.WriteString("\n\n")
	if len(in.Comments) > 0 {
		fmt.Fprintf(&sb, "Comments (%d, oldest first):\n", len(in.Comments))
		for _, c := range in.Comments {
			fmt.Fprintf(&sb, "- %s: %s\n", c.Actor, c.Text)
		}
	}
	out := sb.String()
	if len(out) > aiSubIssuesMaxChars {
		out = out[:aiSubIssuesMaxChars] + "\n\n[…truncated to fit the AI context]"
	}
	return out
}

// clampSubIssueMax normalizes the request's max: absent/zero/negative
// → default 5, beyond 10 → 10, otherwise as-is. Pure: unit-tested.
func clampSubIssueMax(max int) int {
	if max <= 0 {
		return subIssueMaxDefault
	}
	if max > subIssueMaxCap {
		return subIssueMaxCap
	}
	return max
}

// parseSubIssueSuggestions parses the provider's JSON-array answer
// defensively: code fences stripped, prose tolerated around the array
// (slice from the first '[' to the last ']'), titles trimmed with
// empties dropped, descriptions trimmed with empties dropped, output
// clamped to max. Anything else (object instead of array, truncated or
// non-JSON content, zero usable titles) is a provider-shaped failure →
// ErrAIProvider, never a crash. Pure: unit-tested.
func parseSubIssueSuggestions(raw string, max int) ([]SubIssueSuggestion, error) {
	bad := func() ([]SubIssueSuggestion, error) {
		return nil, fmt.Errorf("%w: bad response", ErrAIProvider)
	}
	s := stripCodeFence(raw)
	start := strings.Index(s, "[")
	end := strings.LastIndex(s, "]")
	if start < 0 || end < 0 || end <= start {
		return bad()
	}
	var items []SubIssueSuggestion
	if err := json.Unmarshal([]byte(s[start:end+1]), &items); err != nil {
		return bad()
	}
	out := make([]SubIssueSuggestion, 0, len(items))
	for _, it := range items {
		title := strings.TrimSpace(it.Title)
		if title == "" {
			continue
		}
		var desc *string
		if it.Description != nil {
			if d := strings.TrimSpace(*it.Description); d != "" {
				d := d
				desc = &d
			}
		}
		out = append(out, SubIssueSuggestion{Title: title, Description: desc})
		if len(out) >= max {
			break
		}
	}
	if len(out) == 0 {
		return bad()
	}
	return out, nil
}

// SuggestSubIssues asks the provider to break an issue (title +
// description + comments) into at most `max` concrete sub-tasks.
// Member-scoped like SummarizeIssue: the actor must hold role >=
// RoleMember (guests and non-members get ErrNotFound — the tenancy
// boundary), a missing issue UUID surfaces ErrIssueNotFound, and the
// provider is never called on either path. Malformed provider answers
// surface ErrAIProvider with a generic, honest message.
func SuggestSubIssues(ctx context.Context, pool *pgxpool.Pool, aiCfg config.AIConfig,
	wsSlug, identifier, issueUUID, actorID string, max int) ([]SubIssueSuggestion, error) {
	max = clampSubIssueMax(max)
	if !aiCfg.Configured() {
		return nil, ErrAINotConfigured
	}
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return nil, err
	}
	_, projectID, role, err := resolveIssueProject(ctx, pool, wsSlug, ident, actorID)
	if err != nil {
		return nil, err
	}
	if role < RoleMember {
		return nil, ErrNotFound
	}
	item, err := getIssueRow(ctx, pool, ident, projectID, issueUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return nil, ErrIssueNotFound
		}
		return nil, err
	}

	comments, err := ListComments(ctx, pool, wsSlug, ident, issueUUID, actorID)
	if err != nil {
		return nil, err
	}

	in := subIssuesInput{
		Title:       item.Name,
		Description: tiptapPlainText(item.Description),
		Comments:    flattenCommentTexts(comments),
	}

	content, err := newAIClient(aiCfg).complete(ctx, subIssuesSystemPrompt,
		buildSubIssuesUserPrompt(in, max))
	if err != nil {
		return nil, err
	}
	return parseSubIssueSuggestions(content, max)
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
