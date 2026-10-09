package service

// Jira Cloud issues importer (C9T1).
//
// Two endpoints, mirroring the GitHub importer's preview/import shape:
//
//	POST .../imports/jira/preview — fetches from Jira Cloud, maps, validates.
//	    No writes. Returns the first 10 issues plus the total fetched.
//	POST .../imports/jira        — same fetch+map, then inserts.
//
// Request body (JSON): {site, email, api_token, project_key, max?}.
// `site` is the Atlassian subdomain only ("acme" → acme.atlassian.net).
//
// JIRA CLOUD ONLY — Server/Data Center are deliberately NOT supported.
// Server/DC would require a user-controlled host, which breaks the
// SSRF-guard pattern this importer is built on (pinned host, https only,
// no redirects — the same pattern as the C8T2 webhook SSRF guard and the
// GitHub importer). Supporting an arbitrary Jira base URL would let a
// caller point the token at an internal address. Documented, not an
// oversight.
//
// Mapping:
//   - name        ← issue summary (falls back to the issue key)
//   - description ← v3 description is Atlassian Document Format (ADF);
//     extracted to plain text (jiraADFToText, best-effort) + a footer
//     marker (see Dedupe below)
//   - state       ← Jira status name matched case-insensitively against
//     glance states. UNMATCHED statuses get a NEW state, created in the
//     "unstarted" group. Documented deviation from the task brief's
//     "Default group": glance's state-group vocabulary is
//     triage/backlog/unstarted/started/completed/cancelled (CHECK
//     constraint, migration 000006) — there is no "default" group.
//     "unstarted" is the neutral "tracked, not yet worked" group, the
//     honest equivalent.
//   - priority    ← Jira priority name: Highest→4 (urgent), High→3,
//     Medium→2, Low→1, Lowest→1 (glance has no "lowest"; collapses to
//     low — documented), anything else/missing→0 (none)
//   - labels      ← Jira label strings → glance labels, CREATING missing
//     ones (same deliberate choice as the GitHub importer). Jira labels
//     carry no color, so created labels get the default gray.
//   - assignees   ← best-effort: the Jira assignee's emailAddress is
//     matched (case-insensitive) against workspace member emails. Jira
//     Cloud hides emails by default (GDPR), so misses are the common
//     case — counted (assignee_misses), never fatal.
//   - comments    ← fetched per issue (paginated, capped — see
//     jiraMaxCommentsPerIssue) and inserted as attributed plain-text
//     comments with their original created_at. v3 comment bodies are
//     ADF; same extractor as descriptions.
//   - sprints     ← SKIPPED and documented: glance cycles are managed
//     manually; there is no sprint import. (Sprint data lives behind
//     the Agile API / custom fields — not fetched at all.)
//   - issue types / hierarchy ← subtasks and epics import as FLAT
//     issues; no parent links are created. The issue type is shown in
//     the preview so the caller sees what came in.
//
// Dedupe / idempotency (no migration — deliberate, consistent with the
// GitHub importer's contract-first choice): every imported issue gets a
// footer marker in its description:
//
//	*Imported from [PROJ-123](https://acme.atlassian.net/browse/PROJ-123)*
//
// Before inserting, the run loads every live issue description of the
// project and parses out previously-imported Jira keys for this site.
// The needle requires the closing paren — `](https://acme.atlassian.net/browse/`
// + KEY + `)` — so PROJ-1 can never match PROJ-12. A re-run skips
// already-imported issues and reports them as `skipped`. Honest
// limitations (same as GitHub): deleting the footer defeats dedupe, and
// issues imported before this feature are NOT merged. Dedupe is scoped
// per site: the same key from two different tenants never collides.
//
// Token handling: the API token travels in the JSON request body only.
// It is sent as an HTTP Basic (email:api_token) Authorization header,
// and is NEVER written to the database, NEVER logged (no request-body
// logging anywhere in this codebase, and this file logs nothing), and
// NEVER included in error messages. TestJiraTokenNeverPersisted pins
// this.
//
// SSRF safety: the host is derived from the validated `site` subdomain
// ONLY — `https://<site>.atlassian.net`, https, exact host match. `site`
// is pinned to the DNS-label charset (no dots, no slashes, no
// userinfo), `project_key` to `[A-Za-z][A-Za-z0-9]{1,29}` (which also
// makes JQL injection impossible — the key is the only JQL
// interpolation). Every request re-validates scheme+host
// (jiraClient.urlFor); the nextPageToken cursor is opaque and never
// treated as a URL (a repeated token aborts as a pagination loop — it
// can never steer a request off-host). Redirects are never followed.
// Per-request timeout + an 8MB response cap bound a hostile response.
//
// Search API: POST /rest/api/3/search/jql with a JSON body (the legacy
// GET/POST /rest/api/{2,3}/search endpoints were removed by Atlassian in
// 2025 — CHANGE-2046). Pagination is token-based (nextPageToken/isLast,
// max 100 per page); the response carries no total, so the preview
// reports the number actually fetched.
//
// Notifications: imported comments are inserted as raw rows — they do
// NOT fan out comment notifications or webhook events. A bulk import of
// someone else's history must not ping workspace members.
//
// Error honesty: Jira's own errorMessages are surfaced for 404 (bad
// site or project key) and other upstream failures; a bad token is 401;
// a rate-limited response is 429 with Retry-After and is NOT retried
// silently.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// jiraHTTPTimeout bounds a single Jira API request so one slow
	// response cannot stall the import.
	jiraHTTPTimeout = 15 * time.Second
	// jiraMaxResponseBytes caps a single Jira API response body.
	jiraMaxResponseBytes = 8 << 20
	// jiraPerPage is the v3 search/jql page size (their maximum).
	jiraPerPage = 100
	// jiraDefaultMax applies when the caller passes max <= 0.
	jiraDefaultMax = 100
	// jiraAbsoluteMax clamps max: the fetch is sequential page-by-page,
	// so this bounds the worst-case API cost of one import.
	jiraAbsoluteMax = 1000
	// jiraMaxCommentsPerIssue caps comment fetching per issue (2 pages
	// of 100). Importing unbounded comment threads would let one noisy
	// issue dominate the run.
	jiraMaxCommentsPerIssue = 200
	// jiraMaxPages is a backstop against a pathological nextPageToken
	// sequence; the repeat-token check is the primary loop guard.
	jiraMaxPages = 50
	// jiraNewStateGroup is the group for auto-created states (see the
	// package doc: glance has no "default" group).
	jiraNewStateGroup = "unstarted"
	// jiraNewStateColor is the color for auto-created states.
	jiraNewStateColor = "#6b7280"
	// jiraNewLabelColor is the color for created labels (Jira labels
	// carry no color).
	jiraNewLabelColor = "#6b7280"
)

var (
	// ErrJiraBadInput is returned for malformed import input: bad site,
	// email, token, or project key. It fails the request (400).
	ErrJiraBadInput = errors.New("service: invalid jira import input")
	// ErrJiraNotFound is returned when Jira answers 404: the site or
	// project key does not exist, or the token cannot see it. Jira's
	// own errorMessages are surfaced (404).
	ErrJiraNotFound = errors.New("service: jira site or project not found")
	// ErrJiraUnauthorized is returned when Jira answers 401: the
	// email/token pair is missing or invalid (401).
	ErrJiraUnauthorized = errors.New("service: jira authentication failed")
	// ErrJiraUpstream is returned for any other non-2xx from Jira
	// (5xx, unexpected 3xx, pagination loop). It maps to 502 with
	// Jira's message surfaced.
	ErrJiraUpstream = errors.New("service: jira api error")
	// errJiraSSRF is returned when a constructed request URL does not
	// point at https://<site>.atlassian.net exactly. Internal: never
	// user-visible on its own (it indicates a code bug, not user error).
	errJiraSSRF = errors.New("service: jira request target must be https://<site>.atlassian.net")
)

// jiraSiteRe pins `site` to a single DNS label: lowercase alphanumerics
// and hyphens, no dots — a full host or URL can never pass. This is the
// core of the SSRF guard: the dialed host is ALWAYS
// <site>.atlassian.net, never user-controlled.
var jiraSiteRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// jiraProjectKeyRe pins the project key to Jira's key charset. The key is
// the only value interpolated into JQL, so this regex is also the JQL-
// injection guard: no quotes, no spaces, no operators can pass.
var jiraProjectKeyRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]{1,29}$`)

// JiraRateLimitError is returned when Jira reports rate-limit exhaustion
// (429). It is NOT retried: the handler maps it to 429 with the
// Retry-After header so the caller decides when to retry.
type JiraRateLimitError struct {
	RetryAfter int    // seconds (60 when the header is missing/unparseable)
	Message    string // Jira's message, safe to surface
}

func (e *JiraRateLimitError) Error() string {
	return "service: jira rate limit exceeded: " + e.Message
}

// JiraImportInput is the JSON body of the jira import endpoints.
// The API token travels here only; it is never stored.
type JiraImportInput struct {
	Site       string `json:"site"`
	Email      string `json:"email"`
	APIToken   string `json:"api_token"`
	ProjectKey string `json:"project_key"`
	Max        int    `json:"max"`
}

// validate checks the input and returns the normalized site (lowercase),
// project key (uppercase), and clamped max. The token is opaque and
// never validated here.
func (in JiraImportInput) validate() (site, key string, max int, err error) {
	site = strings.ToLower(strings.TrimSpace(in.Site))
	if site == "" || len(site) > 63 || !jiraSiteRe.MatchString(site) {
		return "", "", 0, fmt.Errorf("%w: site must be the atlassian subdomain only (e.g. \"acme\")", ErrJiraBadInput)
	}
	email := strings.TrimSpace(in.Email)
	if email == "" || len(email) > 254 || strings.ContainsAny(email, " \t\r\n") {
		return "", "", 0, fmt.Errorf("%w: email is required", ErrJiraBadInput)
	}
	if strings.TrimSpace(in.APIToken) == "" {
		return "", "", 0, fmt.Errorf("%w: api_token is required (jira cloud has no anonymous access)", ErrJiraBadInput)
	}
	key = strings.ToUpper(strings.TrimSpace(in.ProjectKey))
	if key == "" || !jiraProjectKeyRe.MatchString(key) {
		return "", "", 0, fmt.Errorf("%w: project_key must be 2-30 chars, starting with a letter, [A-Za-z0-9]", ErrJiraBadInput)
	}
	max = in.Max
	if max <= 0 {
		max = jiraDefaultMax
	}
	if max > jiraAbsoluteMax {
		max = jiraAbsoluteMax
	}
	return site, key, max, nil
}

// ---------- Jira API client (SSRF-safe) ----------

// jiraClient talks to the Jira Cloud REST v3 API. The baseURL is
// https://<site>.atlassian.net in production; tests inject an httptest
// URL via newJiraTestClient. It is NEVER derived from freeform user
// input — only from the validated site subdomain.
type jiraClient struct {
	http *http.Client
	// email/apiToken are the caller's credentials, used transiently as
	// an HTTP Basic Authorization header. Never logged, never stored,
	// never echoed in errors.
	email    string
	apiToken string
	// baseURL is scheme + host only (no path).
	baseURL string
	// expectedHost is "<site>.atlassian.net" — the ONLY host the guard
	// accepts when enforcement is on.
	expectedHost string
	// enforceHost validates scheme+host on every request. False only
	// for the test client (whose override URL is injected by test
	// code, never user input).
	enforceHost bool
}

func newJiraHTTPClient() *http.Client {
	return &http.Client{
		Timeout: jiraHTTPTimeout,
		// Redirects are NEVER followed: the SSRF guard vets the original
		// target, and the Basic credentials must not be bounced at an
		// unvetted URL. A 3xx surfaces as an upstream error.
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
		},
	}
}

// newJiraClient builds the production client: host pinned to
// https://<site>.atlassian.net, host enforcement on. site must already
// be validated (validate() normalizes it); this re-checks defensively.
func newJiraClient(email, apiToken, site string) (*jiraClient, error) {
	site = strings.ToLower(strings.TrimSpace(site))
	if !jiraSiteRe.MatchString(site) {
		return nil, fmt.Errorf("%w: site must be the atlassian subdomain only", ErrJiraBadInput)
	}
	host := site + ".atlassian.net"
	return &jiraClient{
		http:         newJiraHTTPClient(),
		email:        email,
		apiToken:     apiToken,
		baseURL:      "https://" + host,
		expectedHost: host,
		enforceHost:  true,
	}, nil
}

// newJiraTestClient builds a client pointed at a stub server. Tests
// only — production code must never call this.
func newJiraTestClient(email, apiToken, baseURL string) *jiraClient {
	return &jiraClient{
		http:        newJiraHTTPClient(),
		email:       email,
		apiToken:    apiToken,
		baseURL:     strings.TrimSuffix(baseURL, "/"),
		enforceHost: false,
	}
}

// urlFor builds the full request URL for an API path and validates the
// target. The path is always constructed internally from validated
// segments — defense-in-depth against any future refactor that might
// interpolate user input into the URL.
func (c *jiraClient) urlFor(path string) (string, error) {
	u, err := url.Parse(c.baseURL + path)
	if err != nil {
		return "", fmt.Errorf("%w: %v", errJiraSSRF, err)
	}
	if c.enforceHost && (u.Scheme != "https" || u.Hostname() != c.expectedHost) {
		return "", fmt.Errorf("%w (got %q)", errJiraSSRF, u.Hostname())
	}
	return u.String(), nil
}

// basicAuth returns the Authorization header value. The token only ever
// appears here, on the wire, to the pinned host.
func (c *jiraClient) basicAuth() string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(c.email+":"+c.apiToken))
}

// jiraAPIError is Jira's error envelope.
type jiraAPIError struct {
	ErrorMessages []string          `json:"errorMessages"`
	Errors        map[string]string `json:"errors"`
}

// do performs one authenticated request and returns the capped body.
// Non-2xx responses become typed errors; the credentials never appear in
// any of them.
func (c *jiraClient) do(ctx context.Context, method, path string, payload any) ([]byte, error) {
	raw, err := c.urlFor(path)
	if err != nil {
		return nil, err
	}
	var bodyReader io.Reader
	if payload != nil {
		rawJSON, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("%w: encode request: %v", ErrJiraUpstream, err)
		}
		bodyReader = bytes.NewReader(rawJSON)
	}
	req, err := http.NewRequestWithContext(ctx, method, raw, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", c.basicAuth())
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: request failed: %v", ErrJiraUpstream, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, jiraMaxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: read body: %v", ErrJiraUpstream, err)
	}
	if len(body) > jiraMaxResponseBytes {
		return nil, fmt.Errorf("%w: response exceeded size limit", ErrJiraUpstream)
	}
	if resp.StatusCode == http.StatusOK {
		return body, nil
	}
	// Error path: surface Jira's own errorMessages (best-effort decode).
	var apiErr jiraAPIError
	_ = json.Unmarshal(body, &apiErr)
	msgs := append([]string{}, apiErr.ErrorMessages...)
	for k, v := range apiErr.Errors {
		msgs = append(msgs, k+": "+v)
	}
	msg := strings.TrimSpace(strings.Join(msgs, "; "))
	if msg == "" {
		msg = http.StatusText(resp.StatusCode)
	}
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return nil, fmt.Errorf("%w: %s", ErrJiraUnauthorized, msg)
	case http.StatusNotFound:
		return nil, fmt.Errorf("%w: %s", ErrJiraNotFound, msg)
	case http.StatusTooManyRequests:
		retryAfter := 60 // fallback when the header is missing
		if h := resp.Header.Get("Retry-After"); h != "" {
			if n, err := strconv.Atoi(strings.TrimSpace(h)); err == nil && n >= 0 {
				retryAfter = n
			}
		}
		return nil, &JiraRateLimitError{RetryAfter: retryAfter, Message: msg}
	default:
		return nil, fmt.Errorf("%w: %s", ErrJiraUpstream, msg)
	}
}

// ---------- Jira payload types ----------

type jiraNamed struct {
	Name string `json:"name"`
}

type jiraUser struct {
	DisplayName  string `json:"displayName"`
	EmailAddress string `json:"emailAddress"`
}

type jiraStatus struct {
	Name     string    `json:"name"`
	Category jiraNamed `json:"statusCategory"`
}

type jiraCommentPage struct {
	Comments []jiraComment `json:"comments"`
	Total    int           `json:"total"`
}

type jiraComment struct {
	ID      string          `json:"id"`
	Author  jiraUser        `json:"author"`
	Body    json.RawMessage `json:"body"` // ADF
	Created string          `json:"created"`
	Updated string          `json:"updated"`
}

type jiraIssueFields struct {
	Summary     string          `json:"summary"`
	Description json.RawMessage `json:"description"` // ADF (v3)
	Status      jiraStatus      `json:"status"`
	Labels      []string        `json:"labels"`
	Assignee    *jiraUser       `json:"assignee"`
	Priority    *jiraNamed      `json:"priority"`
	Comment     jiraCommentPage `json:"comment"`
	Created     string          `json:"created"`
	Updated     string          `json:"updated"`
	IssueType   jiraNamed       `json:"issuetype"`
}

type jiraIssue struct {
	Key    string          `json:"key"`
	Fields jiraIssueFields `json:"fields"`
}

type jiraSearchRequest struct {
	JQL           string   `json:"jql"`
	Fields        []string `json:"fields"`
	MaxResults    int      `json:"maxResults"`
	NextPageToken *string  `json:"nextPageToken,omitempty"`
}

type jiraSearchResponse struct {
	Issues        []json.RawMessage `json:"issues"`
	NextPageToken *string           `json:"nextPageToken"`
	IsLast        bool              `json:"isLast"`
}

type jiraCommentListResponse struct {
	Comments   []jiraComment `json:"comments"`
	Total      int           `json:"total"`
	MaxResults int           `json:"maxResults"`
	StartAt    int           `json:"startAt"`
}

// jiraSearchFields is the explicit field list for the search call. v3
// changed field defaults — request exactly what the mapping needs.
var jiraSearchFields = []string{
	"summary", "description", "status", "labels", "assignee",
	"priority", "comment", "created", "updated", "issuetype",
}

// ---------- fetch ----------

// fetchJiraIssues pages POST /rest/api/3/search/jql via nextPageToken
// until max issues are collected, the API reports isLast, or no further
// token is returned. The token is an opaque cursor: it is echoed back in
// the request body only, never interpreted as a URL — a repeated token
// aborts as a pagination loop before any request could be steered
// off-host.
func fetchJiraIssues(ctx context.Context, c *jiraClient, projectKey string, max int) ([]jiraIssue, error) {
	var out []jiraIssue
	var token *string
	seen := map[string]bool{}
	jql := fmt.Sprintf(`project = "%s" ORDER BY key ASC`, projectKey)
	for page := 0; page < jiraMaxPages; page++ {
		body, err := c.do(ctx, http.MethodPost, "/rest/api/3/search/jql", jiraSearchRequest{
			JQL:           jql,
			Fields:        jiraSearchFields,
			MaxResults:    jiraPerPage,
			NextPageToken: token,
		})
		if err != nil {
			return nil, err
		}
		var resp jiraSearchResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("%w: decode search response: %v", ErrJiraUpstream, err)
		}
		for _, raw := range resp.Issues {
			var is jiraIssue
			if err := json.Unmarshal(raw, &is); err != nil {
				return nil, fmt.Errorf("%w: decode issue: %v", ErrJiraUpstream, err)
			}
			out = append(out, is)
			if len(out) >= max {
				return out, nil
			}
		}
		if resp.IsLast || resp.NextPageToken == nil || *resp.NextPageToken == "" {
			return out, nil
		}
		if seen[*resp.NextPageToken] {
			return nil, fmt.Errorf("%w: pagination loop detected", ErrJiraUpstream)
		}
		seen[*resp.NextPageToken] = true
		token = resp.NextPageToken
	}
	return nil, fmt.Errorf("%w: pagination exceeded %d pages", ErrJiraUpstream, jiraMaxPages)
}

// fetchJiraComments pages an issue's comments (startAt-based), capped at
// jiraMaxCommentsPerIssue.
func fetchJiraComments(ctx context.Context, c *jiraClient, key string) ([]jiraComment, error) {
	var out []jiraComment
	startAt := 0
	for {
		path := fmt.Sprintf("/rest/api/3/issue/%s/comment?orderBy=created&maxResults=%d&startAt=%d",
			url.PathEscape(key), jiraPerPage, startAt)
		body, err := c.do(ctx, http.MethodGet, path, nil)
		if err != nil {
			return nil, err
		}
		var resp jiraCommentListResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("%w: decode comments: %v", ErrJiraUpstream, err)
		}
		out = append(out, resp.Comments...)
		if len(out) >= jiraMaxCommentsPerIssue {
			return out[:jiraMaxCommentsPerIssue], nil
		}
		if len(out) >= resp.Total || len(resp.Comments) == 0 {
			return out, nil
		}
		startAt += len(resp.Comments)
	}
}

// parseJiraTime parses Jira's timestamps ("2026-01-03T10:00:00.000+0000"
// — note the colon-less offset, which time.RFC3339 rejects).
func parseJiraTime(s string) time.Time {
	for _, layout := range []string{
		"2006-01-02T15:04:05.999-0700",
		"2006-01-02T15:04:05-0700",
		time.RFC3339,
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// ---------- ADF → plain text ----------

// Jira Cloud v3 serves description and comment bodies as Atlassian
// Document Format (ADF) — there is no plain-text rendering. jiraADFToText
// is a best-effort extractor covering the node types a real issue body
// uses; unknown node types recurse into their content rather than
// failing. Marks (bold/italic/code) are dropped — glance stores plain
// text.

type jiraADFNode struct {
	Type    string                     `json:"type"`
	Text    string                     `json:"text"`
	Attrs   map[string]json.RawMessage `json:"attrs"`
	Content []jiraADFNode              `json:"content"`
}

func jiraADFAttrString(attrs map[string]json.RawMessage, key, fallback string) string {
	raw, ok := attrs[key]
	if !ok {
		return fallback
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return fallback
	}
	return s
}

// jiraADFInline renders inline content (text, mentions, emoji, ...).
func jiraADFInline(n jiraADFNode) string {
	switch n.Type {
	case "text":
		return n.Text
	case "hardBreak":
		return "\n"
	case "mention":
		return jiraADFAttrString(n.Attrs, "text", "@"+jiraADFAttrString(n.Attrs, "id", "mention"))
	case "emoji":
		return jiraADFAttrString(n.Attrs, "shortName", "")
	case "date":
		return jiraADFAttrString(n.Attrs, "timestamp", "")
	case "status":
		return jiraADFAttrString(n.Attrs, "text", "")
	case "inlineCard":
		return jiraADFAttrString(n.Attrs, "url", "")
	default:
		// Unknown inline-ish node: recurse rather than drop.
		var b strings.Builder
		for _, c := range n.Content {
			b.WriteString(jiraADFInline(c))
		}
		return b.String()
	}
}

// jiraADFBlocks renders a block-level node list, blocks separated by
// blank lines.
func jiraADFBlocks(nodes []jiraADFNode, depth int) string {
	var parts []string
	for _, n := range nodes {
		if s := jiraADFBlock(n, depth); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "\n\n")
}

func jiraADFBlock(n jiraADFNode, depth int) string {
	switch n.Type {
	case "paragraph", "heading":
		var b strings.Builder
		for _, c := range n.Content {
			b.WriteString(jiraADFInline(c))
		}
		return b.String()
	case "blockquote":
		inner := jiraADFBlocks(n.Content, depth)
		if inner == "" {
			return ""
		}
		lines := strings.Split(inner, "\n")
		for i := range lines {
			lines[i] = "> " + lines[i]
		}
		return strings.Join(lines, "\n")
	case "codeBlock":
		var b strings.Builder
		for _, c := range n.Content {
			b.WriteString(jiraADFInline(c))
		}
		return "```\n" + b.String() + "\n```"
	case "bulletList", "orderedList":
		var lines []string
		for i, item := range n.Content {
			prefix := "- "
			if n.Type == "orderedList" {
				prefix = fmt.Sprintf("%d. ", i+1)
			}
			body := jiraADFBlock(item, depth+1)
			blines := strings.Split(body, "\n")
			for j := 1; j < len(blines); j++ {
				blines[j] = "  " + blines[j]
			}
			lines = append(lines, strings.Repeat("  ", depth)+prefix+strings.Join(blines, "\n"))
		}
		return strings.Join(lines, "\n")
	case "listItem":
		return jiraADFBlocks(n.Content, depth)
	case "rule":
		return "---"
	case "media":
		name := jiraADFAttrString(n.Attrs, "alt", jiraADFAttrString(n.Attrs, "filename", "attachment"))
		return "[attachment: " + name + "]"
	case "mediaSingle":
		return jiraADFBlocks(n.Content, depth)
	case "table":
		var rows []string
		for _, r := range n.Content {
			var cells []string
			for _, c := range r.Content {
				cells = append(cells, jiraADFBlock(c, depth))
			}
			rows = append(rows, "| "+strings.Join(cells, " | ")+" |")
		}
		return strings.Join(rows, "\n")
	case "tableRow":
		var cells []string
		for _, c := range n.Content {
			cells = append(cells, jiraADFBlock(c, depth))
		}
		return strings.Join(cells, " | ")
	case "tableCell", "tableHeader":
		return jiraADFBlocks(n.Content, depth)
	case "taskList":
		return jiraADFBlocks(n.Content, depth)
	case "taskItem":
		checked := ""
		if raw, ok := n.Attrs["state"]; ok {
			var st string
			if json.Unmarshal(raw, &st) == nil && st == "DONE" {
				checked = "x"
			}
		}
		return "[" + checked + "] " + jiraADFBlocks(n.Content, depth)
	case "panel", "expand":
		return jiraADFBlocks(n.Content, depth)
	default:
		// Unknown block: best-effort recurse (ADF is extensible).
		if len(n.Content) > 0 {
			return jiraADFBlocks(n.Content, depth)
		}
		return jiraADFInline(n)
	}
}

// jiraADFToText extracts plain text from an ADF document. Empty/null
// input → "". A bare JSON string is returned as-is (defensive: some
// fields arrive pre-rendered).
func jiraADFToText(raw json.RawMessage) string {
	t := bytes.TrimSpace(raw)
	if len(t) == 0 || string(t) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(t, &s); err == nil {
		return strings.TrimSpace(s)
	}
	var n jiraADFNode
	if err := json.Unmarshal(t, &n); err != nil {
		return ""
	}
	return strings.TrimSpace(jiraADFBlock(n, 0))
}

// ---------- dedupe marker ----------

// jiraIssueFooter is appended to every imported issue's description.
// It is both the user-visible provenance and the dedupe marker.
func jiraIssueFooter(site, key string) string {
	return fmt.Sprintf("\n\n---\n*Imported from [%s](https://%s.atlassian.net/browse/%s)*",
		key, site, key)
}

// parseImportedJiraKeys scans issue descriptions for dedupe markers and
// returns the set of Jira keys already imported from this site. Single
// pass — O(total length). The closing paren disambiguates PROJ-1 from
// PROJ-12 (same needle rule as the GitHub importer).
func parseImportedJiraKeys(descriptions []string, site string) map[string]bool {
	prefix := "](https://" + site + ".atlassian.net/browse/"
	out := map[string]bool{}
	isKeyChar := func(c byte) bool {
		return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-'
	}
	for _, d := range descriptions {
		rest := d
		for {
			i := strings.Index(rest, prefix)
			if i < 0 {
				break
			}
			rest = rest[i+len(prefix):]
			j := 0
			for j < len(rest) && isKeyChar(rest[j]) {
				j++
			}
			if j > 0 && j < len(rest) && rest[j] == ')' {
				out[rest[:j]] = true
			}
		}
	}
	return out
}

// loadImportedJiraKeys returns the set of Jira keys already imported
// into this project from this site (dedupe scan).
func loadImportedJiraKeys(ctx context.Context, pool *pgxpool.Pool, projectID, site string) (map[string]bool, error) {
	rows, err := pool.Query(ctx,
		`SELECT description::text FROM issues
		  WHERE project_id = $1::uuid AND deleted_at IS NULL AND description IS NOT NULL`,
		projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var descs []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		descs = append(descs, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return parseImportedJiraKeys(descs, site), nil
}

// ---------- mapping ----------

// jiraPriorityValue maps a Jira priority name to glance's 0-4 scale.
// Lowest collapses to 1: glance has no "lowest" step.
func jiraPriorityValue(name string) int {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "highest":
		return 4
	case "high":
		return 3
	case "medium":
		return 2
	case "low", "lowest":
		return 1
	default:
		return 0
	}
}

// normalizeJiraLabelName trims and truncates to the labels table's
// 120-char contract (same bound CreateLabel enforces). Truncation is by
// rune so multi-byte characters are never split. Empty → "" (skipped).
func normalizeJiraLabelName(name string) string {
	name = strings.TrimSpace(name)
	if r := []rune(name); len(r) > 120 {
		name = string(r[:120])
	}
	return name
}

// normalizeJiraStateName trims and truncates a Jira status name to the
// states table's 120-char contract before creating a state from it.
func normalizeJiraStateName(name string) string {
	name = strings.TrimSpace(name)
	if r := []rune(name); len(r) > 120 {
		name = string(r[:120])
	}
	return name
}

// jiraMappedIssue is one Jira issue mapped to glance fields, ready to
// insert (state resolved at insert time — unmatched statuses create
// states inside the import transaction).
type jiraMappedIssue struct {
	key         string
	name        string
	description string // ADF-extracted text + footer marker (ALWAYS present)
	statusName  string
	priority    int
	labelNames  []string
	labelIDs    []string
	assigneeID  *string
	issueType   string
	comments    []jiraComment
}

// jiraImportRun carries per-run state: the client, lookups, and the
// label/state resolution maps.
type jiraImportRun struct {
	client     *jiraClient
	site       string
	projectKey string
	lu         *importLookups
	// stateIDs memoizes lower(status name) -> state id (existing +
	// created this run).
	stateIDs map[string]string
	// stateNames maps state id -> real (case-correct) name, for the
	// preview path.
	stateNames map[string]string
	// statesCreated accumulates status names that became new states.
	statesCreated []string
	// labelIDs memoizes lower(label) -> id (existing + created).
	labelIDs map[string]string
	// labelsCreated accumulates names of labels created during the run.
	labelsCreated []string
	// assigneeMisses counts issues whose Jira assignee email matched no
	// workspace member — surfaced in the result, never fatal.
	assigneeMisses int
}

// mapJiraIssue maps one fetched issue to glance fields. The assignee is
// resolved best-effort from the inline emailAddress (no extra API call —
// unlike GitHub, Jira returns it in the issue payload); misses are
// counted, never fatal.
func (r *jiraImportRun) mapJiraIssue(ji jiraIssue) *jiraMappedIssue {
	f := ji.Fields
	name := strings.TrimSpace(f.Summary)
	if name == "" {
		name = ji.Key
	}
	m := &jiraMappedIssue{
		key:         ji.Key,
		name:        name,
		description: strings.TrimSpace(jiraADFToText(f.Description) + jiraIssueFooter(r.site, ji.Key)),
		statusName:  strings.TrimSpace(f.Status.Name),
		issueType:   strings.TrimSpace(f.IssueType.Name),
	}
	if f.Priority != nil {
		m.priority = jiraPriorityValue(f.Priority.Name)
	}
	seen := map[string]bool{}
	for _, l := range f.Labels {
		nm := normalizeJiraLabelName(l)
		if nm == "" || seen[strings.ToLower(nm)] {
			continue
		}
		seen[strings.ToLower(nm)] = true
		m.labelNames = append(m.labelNames, nm)
		if id, ok := r.labelIDs[strings.ToLower(nm)]; ok {
			m.labelIDs = append(m.labelIDs, id)
		}
		// else: resolved at insert time (labels are created on import)
	}
	if f.Assignee != nil {
		if email := strings.ToLower(strings.TrimSpace(f.Assignee.EmailAddress)); email != "" {
			if id, ok := r.lu.members[email]; ok {
				id := id
				m.assigneeID = &id
			} else {
				r.assigneeMisses++
			}
		} else {
			// No email exposed (Jira hides them by default) — a miss
			// only worth counting when there IS an assignee we can't
			// resolve. Documented, never fatal.
			r.assigneeMisses++
		}
	}
	return m
}

// ensureState resolves a Jira status name to a glance state id, creating
// the state (in jiraNewStateGroup) when nothing matches. Creation
// happens inside the import transaction so a failed import never leaves
// orphan states behind.
func (r *jiraImportRun) ensureState(ctx context.Context, tx pgx.Tx, statusName string) (id string, isNew bool, err error) {
	nm := normalizeJiraStateName(statusName)
	if nm == "" {
		return "", false, fmt.Errorf("%w: issue has an empty jira status", ErrJiraBadInput)
	}
	key := strings.ToLower(nm)
	if id, ok := r.stateIDs[key]; ok {
		return id, false, nil
	}
	var seq int
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(MAX(sequence),0)+10000 FROM states WHERE project_id = $1::uuid`,
		r.lu.projectID).Scan(&seq); err != nil {
		return "", false, err
	}
	if err := tx.QueryRow(ctx,
		`INSERT INTO states (project_id, name, "group", color, sequence)
		 VALUES ($1::uuid, $2, $3, $4, $5)
		 ON CONFLICT (project_id, name) DO NOTHING
		 RETURNING id::text`,
		r.lu.projectID, nm, jiraNewStateGroup, jiraNewStateColor, seq).Scan(&id); err != nil {
		// ON CONFLICT DO NOTHING + RETURNING yields no row on conflict
		// (a concurrent import created it): fall back to a lookup.
		if errors.Is(err, pgx.ErrNoRows) {
			if err := tx.QueryRow(ctx,
				`SELECT id::text FROM states WHERE project_id = $1::uuid AND name = $2`,
				r.lu.projectID, nm).Scan(&id); err != nil {
				return "", false, err
			}
		} else {
			return "", false, err
		}
	}
	r.stateIDs[key] = id
	r.statesCreated = append(r.statesCreated, nm)
	return id, true, nil
}

// ---------- result types ----------

// JiraImportPreviewRow is one issue as the preview endpoint reports it.
type JiraImportPreviewRow struct {
	Key             string   `json:"key"`
	Title           string   `json:"title"`
	Status          string   `json:"status"`
	State           string   `json:"state"` // matched glance state name (or the status name when new)
	StateIsNew      bool     `json:"state_is_new"`
	Labels          []string `json:"labels"`
	NewLabels       []string `json:"new_labels"`
	Priority        int      `json:"priority"`
	Assignee        string   `json:"assignee,omitempty"` // Jira display name
	AssigneeMatched bool     `json:"assignee_matched"`
	Comments        int      `json:"comments"`
	IssueType       string   `json:"issue_type"`
	AlreadyImported bool     `json:"already_imported"`
}

// JiraImportPreview is the preview response: no writes happened.
// Total is the number of issues actually fetched — the v3 search API
// returns no total count (isLast/nextPageToken pagination only).
type JiraImportPreview struct {
	Source string                 `json:"source"`
	Total  int                    `json:"total"`
	Rows   []JiraImportPreviewRow `json:"rows"`
}

// JiraImportResult is the import outcome. Errors reuse ImportRowError
// with Row carrying the issue's ordinal in the fetched batch
// (documented — there are no CSV rows here), so the frontend renders
// all importers identically.
type JiraImportResult struct {
	Source         string           `json:"source"`
	Created        int              `json:"created"`
	Skipped        int              `json:"skipped"`
	Failed         int              `json:"failed"`
	LabelsCreated  []string         `json:"labels_created"`
	StatesCreated  []string         `json:"states_created"`
	AssigneeMisses int              `json:"assignee_misses"`
	Errors         []ImportRowError `json:"errors"`
}

// newJiraImportRun validates input, gates membership (member 15+ via
// loadImportLookups — same gate as the CSV/GitHub importers), and seeds
// the resolution maps. The client is injected so tests can point it at
// a stub.
func newJiraImportRun(ctx context.Context, pool *pgxpool.Pool, client *jiraClient,
	wsSlug, identifier, actorID string, in JiraImportInput) (*jiraImportRun, string, string, int, error) {
	site, key, max, err := in.validate()
	if err != nil {
		return nil, "", "", 0, err
	}
	lu, err := loadImportLookups(ctx, pool, wsSlug, identifier, actorID)
	if err != nil {
		return nil, "", "", 0, err
	}
	r := &jiraImportRun{
		client:     client,
		site:       site,
		projectKey: key,
		lu:         lu,
		stateIDs:   map[string]string{},
		stateNames: map[string]string{},
		labelIDs:   map[string]string{},
	}
	for k, v := range lu.states {
		r.stateIDs[k] = v
	}
	for k, v := range lu.labels {
		r.labelIDs[k] = v
	}
	// Real (case-correct) state names for the preview path. One extra
	// query per run; the map is tiny.
	rows, err := pool.Query(ctx,
		`SELECT id::text, name FROM states WHERE project_id = $1::uuid`, lu.projectID)
	if err != nil {
		return nil, "", "", 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, "", "", 0, err
		}
		r.stateNames[id] = name
	}
	if err := rows.Err(); err != nil {
		return nil, "", "", 0, err
	}
	return r, site, key, max, nil
}

// previewStateName resolves the glance state display name for a Jira
// status without writing: matched → the existing state's real name;
// unmatched → the status name flagged new.
func (r *jiraImportRun) previewStateName(statusName string) (string, bool) {
	nm := normalizeJiraStateName(statusName)
	if nm == "" {
		return "", true
	}
	if id, ok := r.stateIDs[strings.ToLower(nm)]; ok {
		if name, ok := r.stateNames[id]; ok {
			return name, false
		}
	}
	return nm, true
}

// jiraPreviewTimeout bounds the whole preview run.
const jiraPreviewTimeout = 2 * time.Minute

// jiraImportTimeout bounds the whole import run.
const jiraImportTimeout = 10 * time.Minute

// PreviewJiraImport fetches the issues and reports how the first 10
// would map. No writes: labels/states are reported as "would create",
// comments are counted but their bodies are NOT fetched (import time
// only).
func PreviewJiraImport(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string, in JiraImportInput) (*JiraImportPreview, error) {
	ctx, cancel := context.WithTimeout(ctx, jiraPreviewTimeout)
	defer cancel()
	site, _, _, err := in.validate()
	if err != nil {
		return nil, err
	}
	client, err := newJiraClient(in.Email, in.APIToken, site)
	if err != nil {
		return nil, err
	}
	return PreviewJiraImportWithClient(ctx, pool, client, wsSlug, identifier, actorID, in)
}

// PreviewJiraImportWithClient is the injectable-client variant (tests).
func PreviewJiraImportWithClient(ctx context.Context, pool *pgxpool.Pool, client *jiraClient,
	wsSlug, identifier, actorID string, in JiraImportInput) (*JiraImportPreview, error) {
	r, site, key, max, err := newJiraImportRun(ctx, pool, client, wsSlug, identifier, actorID, in)
	if err != nil {
		return nil, err
	}
	issues, err := fetchJiraIssues(ctx, r.client, key, max)
	if err != nil {
		return nil, err
	}
	imported, err := loadImportedJiraKeys(ctx, pool, r.lu.projectID, site)
	if err != nil {
		return nil, err
	}
	out := &JiraImportPreview{
		Source: site + "/" + key,
		Total:  len(issues),
		Rows:   []JiraImportPreviewRow{},
	}
	n := len(issues)
	if n > 10 {
		n = 10
	}
	for i := 0; i < n; i++ {
		m := r.mapJiraIssue(issues[i])
		stateName, isNew := r.previewStateName(m.statusName)
		row := JiraImportPreviewRow{
			Key:             m.key,
			Title:           m.name,
			Status:          m.statusName,
			State:           stateName,
			StateIsNew:      isNew,
			Priority:        m.priority,
			Comments:        issues[i].Fields.Comment.Total,
			IssueType:       m.issueType,
			AlreadyImported: imported[m.key],
			AssigneeMatched: m.assigneeID != nil,
		}
		if issues[i].Fields.Assignee != nil {
			row.Assignee = strings.TrimSpace(issues[i].Fields.Assignee.DisplayName)
		}
		for _, nm := range m.labelNames {
			row.Labels = append(row.Labels, nm)
			if _, ok := r.labelIDs[strings.ToLower(nm)]; !ok {
				row.NewLabels = append(row.NewLabels, nm)
			}
		}
		out.Rows = append(out.Rows, row)
	}
	return out, nil
}

// ImportJiraIssues fetches the issues, then inserts the not-yet-
// imported ones in ONE transaction (same two-phase shape as the GitHub
// importer: per-issue errors collected, never aborting the batch).
// Member (15)+.
func ImportJiraIssues(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string, in JiraImportInput) (*JiraImportResult, error) {
	ctx, cancel := context.WithTimeout(ctx, jiraImportTimeout)
	defer cancel()
	site, _, _, err := in.validate()
	if err != nil {
		return nil, err
	}
	client, err := newJiraClient(in.Email, in.APIToken, site)
	if err != nil {
		return nil, err
	}
	return ImportJiraIssuesWithClient(ctx, pool, client, wsSlug, identifier, actorID, in)
}

// ImportJiraIssuesWithClient is the injectable-client variant (tests).
func ImportJiraIssuesWithClient(ctx context.Context, pool *pgxpool.Pool, client *jiraClient,
	wsSlug, identifier, actorID string, in JiraImportInput) (*JiraImportResult, error) {
	r, site, key, max, err := newJiraImportRun(ctx, pool, client, wsSlug, identifier, actorID, in)
	if err != nil {
		return nil, err
	}
	issues, err := fetchJiraIssues(ctx, r.client, key, max)
	if err != nil {
		return nil, err
	}
	imported, err := loadImportedJiraKeys(ctx, pool, r.lu.projectID, site)
	if err != nil {
		return nil, err
	}

	res := &JiraImportResult{
		Source:        site + "/" + key,
		Errors:        []ImportRowError{},
		LabelsCreated: []string{},
		StatesCreated: []string{},
	}

	// Phase 1: map + fetch comments (all HTTP happens BEFORE the tx — a
	// slow comment page must never hold a pool connection).
	var valid []*jiraMappedIssue
	for ord, ji := range issues {
		if imported[ji.Key] {
			res.Skipped++
			continue
		}
		m := r.mapJiraIssue(ji)
		if strings.TrimSpace(m.name) == "" {
			res.Failed++
			res.Errors = append(res.Errors, ImportRowError{Row: ord + 1, Message: "issue has no summary"})
			continue
		}
		if strings.TrimSpace(m.statusName) == "" {
			// An empty status cannot map to a state (and must not abort
			// the batch — same per-issue philosophy as the CSV importer).
			res.Failed++
			res.Errors = append(res.Errors, ImportRowError{Row: ord + 1, Message: "issue has no jira status"})
			continue
		}
		if ji.Fields.Comment.Total > 0 {
			comments, err := fetchJiraComments(ctx, r.client, ji.Key)
			if err != nil {
				// Comments are enrichment, not the issue itself:
				// record and continue with what we have.
				res.Errors = append(res.Errors, ImportRowError{
					Row:     ord + 1,
					Message: fmt.Sprintf("comments not imported: %v", err),
				})
			} else {
				m.comments = comments
			}
		}
		valid = append(valid, m)
	}

	if len(valid) == 0 {
		res.AssigneeMisses = r.assigneeMisses
		return res, nil
	}

	// Phase 2: insert everything atomically.
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// Resolve states first (unmatched statuses create states in the
	// unstarted group, inside this tx).
	for _, m := range valid {
		if _, _, err := r.ensureState(ctx, tx, m.statusName); err != nil {
			return nil, err
		}
	}

	// Collect every label name across the batch, ensure them once.
	// (Same shape as the GitHub importer; Jira labels get the default
	// gray — the API carries no color.)
	var allLabels []string
	seenLabels := map[string]bool{}
	for _, m := range valid {
		for _, nm := range m.labelNames {
			if !seenLabels[strings.ToLower(nm)] {
				seenLabels[strings.ToLower(nm)] = true
				allLabels = append(allLabels, nm)
			}
		}
	}
	for _, nm := range allLabels {
		lkey := strings.ToLower(nm)
		if _, ok := r.labelIDs[lkey]; ok {
			continue
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO labels (workspace_id, name, color)
			 VALUES ($1::uuid, $2, $3)
			 ON CONFLICT (workspace_id, name) DO NOTHING`,
			r.lu.wsID, nm, jiraNewLabelColor); err != nil {
			return nil, err
		}
		var id string
		if err := tx.QueryRow(ctx,
			`SELECT id::text FROM labels WHERE workspace_id = $1::uuid AND name = $2`,
			r.lu.wsID, nm).Scan(&id); err != nil {
			return nil, err
		}
		r.labelIDs[lkey] = id
		r.labelsCreated = append(r.labelsCreated, nm)
	}
	// Fill in label ids on the mapped issues now that all exist.
	for _, m := range valid {
		m.labelIDs = m.labelIDs[:0]
		for _, nm := range m.labelNames {
			if id, ok := r.labelIDs[strings.ToLower(nm)]; ok {
				m.labelIDs = append(m.labelIDs, id)
			}
		}
	}

	for _, m := range valid {
		stateID := r.stateIDs[strings.ToLower(normalizeJiraStateName(m.statusName))]
		var seq int
		if err := tx.QueryRow(ctx,
			`INSERT INTO issue_sequences (project_id, last_value) VALUES ($1::uuid, 1)
			 ON CONFLICT (project_id) DO UPDATE SET last_value = issue_sequences.last_value + 1
			 RETURNING last_value`,
			r.lu.projectID).Scan(&seq); err != nil {
			return nil, fmt.Errorf("service: increment issue sequence: %w", err)
		}
		var issueID string
		err = tx.QueryRow(ctx,
			`INSERT INTO issues (project_id, sequence_id, name, description, priority,
				state_id, created_by)
			 VALUES ($1::uuid, $2, $3, $4::jsonb, $5, $6::uuid, $7::uuid)
			 RETURNING id::text`,
			r.lu.projectID, seq, m.name, strconv.Quote(m.description),
			m.priority, stateID, actorID).Scan(&issueID)
		if err != nil {
			return nil, err
		}
		for _, lid := range m.labelIDs {
			if _, err := tx.Exec(ctx,
				`INSERT INTO issue_labels (issue_id, label_id) VALUES ($1::uuid, $2::uuid)
				 ON CONFLICT DO NOTHING`, issueID, lid); err != nil {
				return nil, err
			}
		}
		if m.assigneeID != nil {
			if _, err := tx.Exec(ctx,
				`INSERT INTO issue_assignees (issue_id, user_id) VALUES ($1::uuid, $2::uuid)
				 ON CONFLICT DO NOTHING`, issueID, *m.assigneeID); err != nil {
				return nil, err
			}
		}
		// Imported comments: raw rows, attributed, original timestamps.
		// Deliberately NOT via CreateComment: a bulk import of foreign
		// history must not fire mention parsing, watcher notifications,
		// or webhook events.
		for _, jc := range m.comments {
			cbody := strings.TrimSpace(jiraADFToText(jc.Body))
			if cbody == "" {
				continue
			}
			who := strings.TrimSpace(jc.Author.DisplayName)
			if who == "" {
				who = "unknown Jira user"
			}
			when := parseJiraTime(jc.Created)
			whenStr := jc.Created
			if when.IsZero() {
				// Unparseable timestamp: fall back to now rather than
				// storing the zero time (Jira's format is stable, so
				// this is defensive only).
				when = time.Now()
				whenStr = when.UTC().Format("2006-01-02 15:04 MST")
			} else {
				whenStr = when.UTC().Format("2006-01-02 15:04 MST")
			}
			attributed := fmt.Sprintf("%s\n\n— originally posted by %s on %s",
				cbody, who, whenStr)
			if _, err := tx.Exec(ctx,
				`INSERT INTO comments (issue_id, actor_id, content, created_at)
				 VALUES ($1::uuid, $2::uuid, $3::jsonb, $4)`,
				issueID, actorID, strconv.Quote(attributed), when); err != nil {
				return nil, err
			}
		}
		newVal := fmt.Sprintf(`{"sequence_id":%d,"name":%s,"jira_key":%s}`,
			seq, strconv.Quote(m.name), strconv.Quote(m.key))
		if _, err := tx.Exec(ctx,
			`INSERT INTO issue_activities (issue_id, actor_id, field, new_value)
			 VALUES ($1::uuid, $2::uuid, '_created', $3::jsonb)`,
			issueID, actorID, newVal); err != nil {
			return nil, err
		}
		res.Created++
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	res.AssigneeMisses = r.assigneeMisses
	res.LabelsCreated = r.labelsCreated
	res.StatesCreated = r.statesCreated
	return res, nil
}
