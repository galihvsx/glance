package service

// Linear importer (C31T0).
//
// Three endpoints, mirroring the GitHub/Jira/Trello importer shape:
//
//	POST .../imports/linear/teams   — list the Linear teams the API key
//	    can see, so the UI can offer a picker instead of demanding a
//	    team UUID. No writes.
//	POST .../imports/linear/preview — fetch from Linear, map, validate.
//	    No writes. Returns the first 10 mapped issues plus the total
//	    fetched and the assignee-miss count.
//	POST .../imports/linear        — same fetch+map, then inserts.
//	    200 {created, skipped, failed, errors, assignee_misses}.
//
// Request bodies (JSON): teams {api_key}; preview/import
// {api_key, team_id, max?}. `team_id` is the Linear team UUID.
//
// LINEAR CLOUD ONLY — Linear has no self-hosted edition, so the host is
// fixed to https://api.linear.app and re-validated on every request via
// the same SSRF-guard pattern as the GitHub/Jira/Trello importers (no
// user-controlled host, https only, redirects never followed). The team
// id travels as a GraphQL VARIABLE — it is never interpolated into a
// URL, a query string, or SQL — so it cannot steer a request off-host
// or inject anything.
//
// GraphQL: POST https://api.linear.app/graphql, Authorization Bearer
// <api_key>, JSON body {query, variables}. Cursor pagination
// (first: 50, after), `max` caps the total fetched.
//
// Mapping:
//   - name        ← Linear title (falls back to the identifier)
//   - description ← Linear description (Markdown, kept as-is — Linear
//     bodies are Markdown; no ADF-style conversion needed) + a footer
//     marker (see Dedupe below)
//   - state       ← Linear `state.type` (backlog|unstarted|started|
//     completed|canceled|triage) → the glance state group of the same
//     name (`canceled` → `cancelled`). The state NAME is matched
//     case-insensitively against existing states; otherwise a NEW state
//     is created in the mapped group. One deliberate deviation from a
//     literal "match within the target group": states carry a
//     (project_id, name) uniqueness constraint, so a same-named state
//     in another group cannot be duplicated — it is reused. The group
//     applies at creation time. Unknown state types fall back to the
//     neutral "unstarted" group (same choice as the Jira importer).
//   - priority    ← Linear 0→0 (none), 1→4 (urgent), 2→3 (high),
//     3→2 (medium), 4→1 (low); anything else → 0
//   - labels      ← Linear label names → glance labels, CREATING
//     missing ones with the default gray (same deliberate choice as the
//     GitHub/Jira importers). Linear labels are team-scoped — only
//     labels from the imported team are used.
//   - assignees   ← best-effort: the Linear assignee's email is matched
//     (case-insensitive) against workspace member emails. Misses are
//     counted (assignee_misses), never fatal.
//   - comments    ← imported as attributed comments with their
//     original createdAt, body kept as Markdown. Comments arrive
//     inline in the issues query — no per-issue fetch.
//   - target_date ← Linear dueDate (date-only); unparseable or absent
//     due → no target date (documented, never fatal)
//   - sub-issues / parent links / cycles / projects ← NOT imported
//     (flat issues, same as the Jira importer's subtask/epic choice).
//     Documented.
//
// Dedupe / idempotency (no migration — deliberate, consistent with the
// GitHub/Jira importers' contract-first choice): every imported issue
// gets a footer marker in its description:
//
//	*Imported from [ENG-123](https://linear.app/acme/issue/ENG-123/ship-v2)*
//
// Before inserting, the run loads every live issue description of the
// project and parses out previously-imported Linear identifiers. The
// parser reads the identifier out of the `*Imported from [...](...)`
// marker and requires the closing paren, so ENG-1 can never match
// ENG-12. A re-run skips already-imported issues and reports them as
// `skipped`. Honest limitations (same as GitHub/Jira): deleting the
// footer defeats dedupe, and issues imported before this feature are
// NOT merged. Dedupe is keyed by identifier — the team-key prefix
// (ENG-) makes cross-team collisions unlikely; two different Linear
// workspaces reusing the same team key could collide, and that case
// is accepted rather than solved with a migration.
//
// Token handling: the API key travels in the JSON request body only.
// It is sent as a Bearer Authorization header, and is NEVER written to
// the database, NEVER logged (no request-body logging anywhere in this
// codebase, and this file logs nothing), and NEVER included in error
// messages. TestLinearTokenNeverPersisted pins this.
//
// SSRF safety: the host is fixed to https://api.linear.app, https only,
// exact host match, no redirects, per-request timeout + an 8MB
// response cap. The `after` cursor is an opaque string echoed back in
// the GraphQL variables only — it can never steer a request off-host
// (a repeated cursor aborts as a pagination loop).
//
// Notifications: imported comments are inserted as raw rows — they do
// NOT fan out comment notifications or webhook events. A bulk import of
// someone else's history must not ping workspace members.
//
// Error honesty: a bad key / Linear 401 → 502 with a GENERIC message
// (never the key, never Linear's message — the handler maps
// ErrLinearUnauthorized to a fixed string); Linear GraphQL `errors[]`
// → 502 with the first message (truncated); team not found → 400; a
// rate-limited response is 429 with Retry-After and is NOT retried
// silently.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// linearAPIHost is the ONLY host this importer ever dials.
	linearAPIHost = "api.linear.app"
	// linearHTTPTimeout bounds a single Linear API request so one slow
	// response cannot stall the import.
	linearHTTPTimeout = 15 * time.Second
	// linearMaxResponseBytes caps a single Linear API response body.
	linearMaxResponseBytes = 8 << 20
	// linearPerPage is the issues page size (the plan pins first: 50).
	linearPerPage = 50
	// linearMaxCommentsPerIssue caps comments fetched inline per issue
	// (the comments connection in the issues query).
	linearMaxCommentsPerIssue = 100
	// linearDefaultMax applies when the caller passes max <= 0.
	linearDefaultMax = 100
	// linearAbsoluteMax clamps max: the fetch is sequential page-by-page,
	// so this bounds the worst-case API cost of one import.
	//
	// DELIBERATE DEVIATION from the Jira/Trello importers (which clamp
	// at 1000): the C31T0 task pins the Linear hard cap at 500.
	linearAbsoluteMax = 500
	// linearMaxPages is a backstop against a pathological cursor
	// sequence; the repeat-cursor check is the primary loop guard.
	linearMaxPages = 50
	// linearNewStateColor is the color for auto-created states.
	linearNewStateColor = "#6b7280"
	// linearNewLabelColor is the color for created labels (the plan
	// pins the default gray, same as GitHub/Jira — Linear's own label
	// colors are not carried over).
	linearNewLabelColor = "#6b7280"
)

var (
	// ErrLinearBadInput is returned for malformed import input: missing
	// api_key or team_id. It fails the request (400).
	ErrLinearBadInput = errors.New("service: invalid linear import input")
	// ErrLinearTeamNotFound is returned when Linear answers with a
	// null team: the team_id does not exist, or the key cannot see it.
	// It maps to 400 (the team id is caller-supplied input, not a
	// missing glance resource).
	ErrLinearTeamNotFound = errors.New("service: linear team not found")
	// ErrLinearUnauthorized is returned when Linear answers 401: the
	// API key is missing or invalid. The handler maps it to 502 with a
	// GENERIC message (per the cycle-31 plan) — Linear's own message is
	// never surfaced, so the key can never leak through it.
	ErrLinearUnauthorized = errors.New("service: linear authentication failed")
	// ErrLinearUpstream is returned for any other non-2xx from Linear,
	// a GraphQL errors[] array, or a pagination loop. It maps to 502.
	ErrLinearUpstream = errors.New("service: linear api error")
	// errLinearSSRF is returned when a constructed request URL does not
	// point at https://api.linear.app exactly. Internal: never user-
	// visible on its own (it indicates a code bug, not user error).
	errLinearSSRF = errors.New("service: linear request target must be https://api.linear.app")
)

// LinearRateLimitError is returned when Linear reports rate-limit
// exhaustion (429). It is NOT retried: the handler maps it to 429 with
// the Retry-After header so the caller decides when to retry.
type LinearRateLimitError struct {
	RetryAfter int    // seconds (60 when the header is missing/unparseable)
	Message    string // Linear's message, safe to surface
}

func (e *LinearRateLimitError) Error() string {
	return "service: linear rate limit exceeded: " + e.Message
}

// LinearImportInput is the JSON body of the linear preview/import
// endpoints. The API key travels here only; it is never stored.
type LinearImportInput struct {
	APIKey string `json:"api_key"`
	TeamID string `json:"team_id"`
	Max    int    `json:"max"`
}

// validate checks the input and returns the normalized team id and the
// clamped max. The API key is opaque and never validated here. The team
// id is passed to Linear as a GraphQL variable, never interpolated
// into a URL or query string, so no charset pin is needed — a bogus id
// simply yields "team not found".
func (in LinearImportInput) validate() (teamID string, max int, err error) {
	if strings.TrimSpace(in.APIKey) == "" {
		return "", 0, fmt.Errorf("%w: api_key is required (linear has no anonymous API access)", ErrLinearBadInput)
	}
	teamID = strings.TrimSpace(in.TeamID)
	if teamID == "" || len(teamID) > 200 {
		return "", 0, fmt.Errorf("%w: team_id is required", ErrLinearBadInput)
	}
	max = in.Max
	if max <= 0 {
		max = linearDefaultMax
	}
	if max > linearAbsoluteMax {
		max = linearAbsoluteMax
	}
	return teamID, max, nil
}

// ---------- Linear GraphQL client (SSRF-safe) ----------

// linearClient talks to the Linear GraphQL API. The baseURL is
// https://api.linear.app in production; tests inject an httptest URL
// via newLinearTestClient. It is NEVER derived from user input.
type linearClient struct {
	http *http.Client
	// apiKey is the caller's personal API key, used transiently as a
	// Bearer Authorization header. Never logged, never stored, never
	// echoed in errors.
	apiKey string
	// baseURL is scheme + host only (no path).
	baseURL string
	// pageSize is the issues pagination page size (linearPerPage in
	// production; the test client uses a smaller one so the cursor loop
	// is exercised against the stub).
	pageSize int
	// enforceHost validates scheme+host on every request. False only
	// for the test client (whose override URL is injected by test
	// code, never user input).
	enforceHost bool
}

func newLinearHTTPClient() *http.Client {
	return &http.Client{
		Timeout: linearHTTPTimeout,
		// Redirects are NEVER followed: the SSRF guard vets the original
		// target, and the Bearer credentials must not be bounced at an
		// unvetted URL. A 3xx surfaces as an upstream error.
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
		},
	}
}

// newLinearClient builds the production client: host pinned to
// https://api.linear.app, host enforcement on.
func newLinearClient(apiKey string) *linearClient {
	return &linearClient{
		http:        newLinearHTTPClient(),
		apiKey:      apiKey,
		baseURL:     "https://" + linearAPIHost,
		enforceHost: true,
		pageSize:    linearPerPage,
	}
}

// newLinearTestClient builds a client pointed at a stub server. Tests
// only — production code must never call this.
func newLinearTestClient(apiKey, baseURL string) *linearClient {
	return &linearClient{
		http:        newLinearHTTPClient(),
		apiKey:      apiKey,
		baseURL:     strings.TrimSuffix(baseURL, "/"),
		enforceHost: false,
		// 2 issues per page against the stub: the stub serves its canned
		// issues in pages of 2 via the after cursor, so the pagination
		// loop is exercised with exactly the production code path.
		pageSize: 2,
	}
}

// urlFor builds the full request URL for the GraphQL endpoint and
// validates the target.
func (c *linearClient) urlFor(path string) (string, error) {
	u, err := url.Parse(c.baseURL + path)
	if err != nil {
		return "", fmt.Errorf("%w: %v", errLinearSSRF, err)
	}
	if c.enforceHost && (u.Scheme != "https" || u.Hostname() != linearAPIHost) {
		return "", fmt.Errorf("%w (got %q)", errLinearSSRF, u.Hostname())
	}
	return u.String(), nil
}

// linearGraphQLRequest is the JSON body of a Linear GraphQL call.
type linearGraphQLRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables,omitempty"`
}

// linearGraphQLError is one entry of Linear's errors[] array.
type linearGraphQLError struct {
	Message string `json:"message"`
}

// linearGraphQLResponse is Linear's response envelope.
type linearGraphQLResponse struct {
	Data   json.RawMessage      `json:"data"`
	Errors []linearGraphQLError `json:"errors"`
}

// truncateLinearMessage bounds an upstream message before it reaches
// the caller. The API key is never in the body — this is size hygiene
// only.
func truncateLinearMessage(msg string) string {
	msg = strings.TrimSpace(msg)
	if len(msg) > 500 {
		msg = msg[:500] + "…"
	}
	return msg
}

// doGraphQL performs one authenticated GraphQL call and returns the
// `data` payload. A non-empty errors[] array becomes ErrLinearUpstream
// with the first message; non-2xx responses become typed errors. The
// API key never appears in any of them.
func (c *linearClient) doGraphQL(ctx context.Context, query string, variables map[string]any) (json.RawMessage, error) {
	raw, err := c.urlFor("/graphql")
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(linearGraphQLRequest{Query: query, Variables: variables})
	if err != nil {
		return nil, fmt.Errorf("%w: encode request: %v", ErrLinearUpstream, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, raw, strings.NewReader(string(payload)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: request failed: %v", ErrLinearUpstream, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, linearMaxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: read body: %v", ErrLinearUpstream, err)
	}
	if len(body) > linearMaxResponseBytes {
		return nil, fmt.Errorf("%w: response exceeded size limit", ErrLinearUpstream)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		// Generic: the handler maps this to 502 with a fixed message.
		// Linear's own 401 body is deliberately NOT surfaced.
		return nil, ErrLinearUnauthorized
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		retryAfter := 60 // fallback when the header is missing
		if h := resp.Header.Get("Retry-After"); h != "" {
			if n, err := strconv.Atoi(strings.TrimSpace(h)); err == nil && n >= 0 {
				retryAfter = n
			}
		}
		return nil, &LinearRateLimitError{RetryAfter: retryAfter, Message: "linear rate limit exceeded"}
	}
	if resp.StatusCode != http.StatusOK {
		// Best-effort: surface a truncated message from the body when
		// it decodes as a GraphQL envelope; otherwise the status text.
		msg := http.StatusText(resp.StatusCode)
		var env linearGraphQLResponse
		if json.Unmarshal(body, &env) == nil && len(env.Errors) > 0 && env.Errors[0].Message != "" {
			msg = env.Errors[0].Message
		}
		return nil, fmt.Errorf("%w: %s", ErrLinearUpstream, truncateLinearMessage(msg))
	}
	var env linearGraphQLResponse
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("%w: decode response: %v", ErrLinearUpstream, err)
	}
	if len(env.Errors) > 0 {
		msg := env.Errors[0].Message
		if strings.TrimSpace(msg) == "" {
			msg = "linear GraphQL error"
		}
		return nil, fmt.Errorf("%w: %s", ErrLinearUpstream, truncateLinearMessage(msg))
	}
	if len(env.Data) == 0 || string(env.Data) == "null" {
		return nil, fmt.Errorf("%w: empty data in linear response", ErrLinearUpstream)
	}
	return env.Data, nil
}

// ---------- Linear payload types ----------

// LinearTeam is one Linear team (the picker entry).
type LinearTeam struct {
	ID   string `json:"id"`
	Key  string `json:"key"`
	Name string `json:"name"`
}

type linearTeamsData struct {
	Teams struct {
		Nodes []LinearTeam `json:"nodes"`
	} `json:"teams"`
}

type linearState struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type linearLabelNode struct {
	Name string `json:"name"`
}

type linearUser struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

type linearComment struct {
	Body      string      `json:"body"`
	User      *linearUser `json:"user"`
	CreatedAt string      `json:"createdAt"`
}

type linearIssue struct {
	ID          string      `json:"id"`
	Identifier  string      `json:"identifier"`
	Title       string      `json:"title"`
	Description string      `json:"description"`
	State       linearState `json:"state"`
	Priority    int         `json:"priority"`
	Labels      struct {
		Nodes []linearLabelNode `json:"nodes"`
	} `json:"labels"`
	Assignee *linearUser `json:"assignee"`
	Comments struct {
		Nodes []linearComment `json:"nodes"`
	} `json:"comments"`
	CreatedAt string  `json:"createdAt"`
	UpdatedAt string  `json:"updatedAt"`
	DueDate   *string `json:"dueDate"`
	URL       string  `json:"url"`
}

type linearTeamIssuesData struct {
	Team *struct {
		ID     string `json:"id"`
		Issues struct {
			Nodes    []linearIssue `json:"nodes"`
			PageInfo struct {
				HasNextPage bool   `json:"hasNextPage"`
				EndCursor   string `json:"endCursor"`
			} `json:"pageInfo"`
		} `json:"issues"`
	} `json:"team"`
}

// linearTeamsQuery lists the teams the API key can see.
const linearTeamsQuery = `query {
  teams {
    nodes { id key name }
  }
}`

// linearIssuesQuery fetches one page of a team's issues. Comments ride
// along inline (first 100) — no per-issue fetch. The team id is a
// variable, never interpolated into the query text.
const linearIssuesQuery = `query($teamId: ID!, $first: Int!, $after: String) {
  team(id: $teamId) {
    id
    issues(first: $first, after: $after) {
      nodes {
        id identifier title description
        state { name type }
        priority
        labels(first: 100) { nodes { name } }
        assignee { email name }
        comments(first: 100) { nodes { body user { name email } createdAt } }
        createdAt updatedAt dueDate url
      }
      pageInfo { hasNextPage endCursor }
    }
  }
}`

// ---------- fetch ----------

// fetchLinearTeams returns the teams visible to the API key.
func fetchLinearTeams(ctx context.Context, c *linearClient) ([]LinearTeam, error) {
	data, err := c.doGraphQL(ctx, linearTeamsQuery, nil)
	if err != nil {
		return nil, err
	}
	var d linearTeamsData
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("%w: decode teams: %v", ErrLinearUpstream, err)
	}
	if d.Teams.Nodes == nil {
		return []LinearTeam{}, nil
	}
	return d.Teams.Nodes, nil
}

// fetchLinearIssues pages a team's issues via the after cursor until
// max issues are collected or Linear reports hasNextPage=false. A null
// team is "team not found" (400). A repeated cursor aborts as a
// pagination loop before any request could be steered off-host.
func fetchLinearIssues(ctx context.Context, c *linearClient, teamID string, max int) ([]linearIssue, error) {
	var out []linearIssue
	var after *string
	seen := map[string]bool{}
	for page := 0; page < linearMaxPages; page++ {
		variables := map[string]any{"teamId": teamID, "first": c.pageSize}
		if after != nil {
			variables["after"] = *after
		}
		data, err := c.doGraphQL(ctx, linearIssuesQuery, variables)
		if err != nil {
			return nil, err
		}
		var d linearTeamIssuesData
		if err := json.Unmarshal(data, &d); err != nil {
			return nil, fmt.Errorf("%w: decode issues: %v", ErrLinearUpstream, err)
		}
		if d.Team == nil {
			return nil, ErrLinearTeamNotFound
		}
		out = append(out, d.Team.Issues.Nodes...)
		if len(out) >= max {
			return out[:max], nil
		}
		if !d.Team.Issues.PageInfo.HasNextPage || d.Team.Issues.PageInfo.EndCursor == "" {
			return out, nil
		}
		if seen[d.Team.Issues.PageInfo.EndCursor] {
			return nil, fmt.Errorf("%w: pagination loop detected", ErrLinearUpstream)
		}
		seen[d.Team.Issues.PageInfo.EndCursor] = true
		cursor := d.Team.Issues.PageInfo.EndCursor
		after = &cursor
	}
	return nil, fmt.Errorf("%w: pagination exceeded %d pages", ErrLinearUpstream, linearMaxPages)
}

// parseLinearTime parses Linear's ISO-8601 timestamps
// ("2026-01-03T10:00:00.000Z").
func parseLinearTime(s string) time.Time {
	for _, layout := range []string{
		time.RFC3339,
		"2006-01-02T15:04:05.999-0700",
		"2006-01-02T15:04:05-0700",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// parseLinearDueDate parses Linear's date-only dueDate ("2026-02-01").
// Linear serves dates; an RFC3339 timestamp is accepted defensively
// (date part kept). Empty/unparseable → nil, never fatal.
func parseLinearDueDate(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		out := t.Format("2006-01-02")
		return &out
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		out := t.Format("2006-01-02")
		return &out
	}
	return nil
}

// ---------- dedupe marker ----------

// linearIssueFooter is appended to every imported issue's description.
// It is both the user-visible provenance and the dedupe marker.
func linearIssueFooter(identifier, url string) string {
	return fmt.Sprintf("\n\n---\n*Imported from [%s](%s)*", identifier, url)
}

// parseImportedLinearIdentifiers scans issue descriptions for dedupe
// markers and returns the set of Linear identifiers already imported.
// Single pass — O(total length). The parser reads the identifier out of
// the `*Imported from [IDENT](URL)` marker and requires the closing
// paren, so ENG-1 can never match ENG-12 (same needle rule as the other
// importers).
func parseImportedLinearIdentifiers(descriptions []string) map[string]bool {
	const prefix = "*Imported from ["
	out := map[string]bool{}
	isIdentChar := func(c byte) bool {
		return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-'
	}
	for _, d := range descriptions {
		search := d
		for {
			i := strings.Index(search, prefix)
			if i < 0 {
				break
			}
			after := search[i+len(prefix):]
			j := 0
			for j < len(after) && isIdentChar(after[j]) {
				j++
			}
			if j > 0 && j+1 < len(after) && after[j] == ']' && after[j+1] == '(' {
				rest := after[j+2:]
				if k := strings.IndexByte(rest, ')'); k >= 0 {
					out[after[:j]] = true
					search = rest[k+1:]
					continue
				}
			}
			search = after
		}
	}
	return out
}

// loadImportedLinearIdentifiers returns the set of Linear identifiers
// already imported into this project (dedupe scan).
func loadImportedLinearIdentifiers(ctx context.Context, pool *pgxpool.Pool, projectID string) (map[string]bool, error) {
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
	return parseImportedLinearIdentifiers(descs), nil
}

// ---------- mapping ----------

// linearStateGroup maps a Linear state.type to a glance state group.
// canceled → cancelled (spelling differs); triage maps to glance's
// triage group; unknown types fall back to the neutral unstarted group.
func linearStateGroup(stateType string) string {
	switch strings.ToLower(strings.TrimSpace(stateType)) {
	case "backlog":
		return "backlog"
	case "unstarted":
		return "unstarted"
	case "started":
		return "started"
	case "completed":
		return "completed"
	case "canceled":
		return "cancelled"
	case "triage":
		return "triage"
	default:
		return "unstarted"
	}
}

// linearPriorityValue maps Linear's 0-4 priority to glance's 0-4 scale.
// Linear: 0=none, 1=urgent, 2=high, 3=medium, 4=low (inverted vs
// glance's 1=low … 4=urgent). Anything else → 0 (none).
func linearPriorityValue(p int) int {
	switch p {
	case 1:
		return 4
	case 2:
		return 3
	case 3:
		return 2
	case 4:
		return 1
	default:
		return 0
	}
}

// normalizeLinearLabelName trims and truncates to the labels table's
// 120-char contract (same bound CreateLabel enforces). Truncation is by
// rune so multi-byte characters are never split. Empty → "" (skipped).
func normalizeLinearLabelName(name string) string {
	name = strings.TrimSpace(name)
	if r := []rune(name); len(r) > 120 {
		name = string(r[:120])
	}
	return name
}

// normalizeLinearStateName trims and truncates a Linear state name to
// the states table's 120-char contract before creating a state from it.
func normalizeLinearStateName(name string) string {
	name = strings.TrimSpace(name)
	if r := []rune(name); len(r) > 120 {
		name = string(r[:120])
	}
	return name
}

// linearMappedIssue is one Linear issue mapped to glance fields, ready
// to insert (state resolved at insert time — unmatched states create
// states inside the import transaction).
type linearMappedIssue struct {
	identifier    string
	name          string
	description   string // description (Markdown) + footer marker (ALWAYS present)
	stateName     string
	stateGroup    string
	priority      int
	labelNames    []string
	labelIDs      []string
	assigneeID    *string
	assigneeEmail string
	dueDate       *string // YYYY-MM-DD or nil
	comments      []linearComment
	url           string
}

// linearImportRun carries per-run state: the client, lookups, and the
// label/state resolution maps.
type linearImportRun struct {
	client *linearClient
	teamID string
	lu     *importLookups
	// stateIDs memoizes lower(state name) -> state id (existing +
	// created this run). Keyed by NAME only: (project_id, name) is
	// unique, so a same-named state in another group is reused (see the
	// package doc).
	stateIDs map[string]string
	// stateNames maps state id -> real (case-correct) name, for the
	// preview path.
	stateNames map[string]string
	// labelIDs memoizes lower(label) -> id (existing + created).
	labelIDs map[string]string
	// labelsCreated accumulates names of labels created during the run.
	labelsCreated []string
	// statesCreated accumulates names of states created during the run.
	statesCreated []string
	// assigneeMisses counts issues whose Linear assignee email matched
	// no workspace member — surfaced in the result, never fatal.
	assigneeMisses int
}

// mapLinearIssue maps one fetched issue to glance fields. The assignee
// is resolved best-effort from the inline email; misses are counted,
// never fatal.
func (r *linearImportRun) mapLinearIssue(li linearIssue) *linearMappedIssue {
	name := strings.TrimSpace(li.Title)
	if name == "" {
		name = li.Identifier
	}
	m := &linearMappedIssue{
		identifier: li.Identifier,
		name:       name,
		stateName:  strings.TrimSpace(li.State.Name),
		stateGroup: linearStateGroup(li.State.Type),
		priority:   linearPriorityValue(li.Priority),
		comments:   li.Comments.Nodes,
		url:        strings.TrimSpace(li.URL),
	}
	if li.DueDate != nil {
		m.dueDate = parseLinearDueDate(*li.DueDate)
	}
	seen := map[string]bool{}
	for _, l := range li.Labels.Nodes {
		nm := normalizeLinearLabelName(l.Name)
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
	if li.Assignee != nil {
		m.assigneeEmail = strings.ToLower(strings.TrimSpace(li.Assignee.Email))
		if m.assigneeEmail != "" {
			if id, ok := r.lu.members[m.assigneeEmail]; ok {
				id := id
				m.assigneeID = &id
			} else {
				r.assigneeMisses++
			}
		} else {
			// An assignee with no exposed email cannot be matched —
			// counted, never fatal.
			r.assigneeMisses++
		}
	}
	m.description = strings.TrimSpace(strings.TrimSpace(li.Description) + linearIssueFooter(m.identifier, m.url))
	return m
}

// ensureState resolves a Linear state name to a glance state id,
// creating the state (in the mapped group) when nothing matches.
// Creation happens inside the import transaction so a failed import
// never leaves orphan states behind.
func (r *linearImportRun) ensureState(ctx context.Context, tx pgx.Tx, stateName, group string) (id string, err error) {
	nm := normalizeLinearStateName(stateName)
	if nm == "" {
		return "", fmt.Errorf("%w: issue has an empty linear state", ErrLinearBadInput)
	}
	key := strings.ToLower(nm)
	if id, ok := r.stateIDs[key]; ok {
		return id, nil
	}
	var seq int
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(MAX(sequence),0)+10000 FROM states WHERE project_id = $1::uuid`,
		r.lu.projectID).Scan(&seq); err != nil {
		return "", err
	}
	if err := tx.QueryRow(ctx,
		`INSERT INTO states (project_id, name, "group", color, sequence)
		 VALUES ($1::uuid, $2, $3, $4, $5)
		 ON CONFLICT (project_id, name) DO NOTHING
		 RETURNING id::text`,
		r.lu.projectID, nm, group, linearNewStateColor, seq).Scan(&id); err != nil {
		// ON CONFLICT DO NOTHING + RETURNING yields no row on conflict
		// (a concurrent import created it): fall back to a lookup.
		if errors.Is(err, pgx.ErrNoRows) {
			if err := tx.QueryRow(ctx,
				`SELECT id::text FROM states WHERE project_id = $1::uuid AND name = $2`,
				r.lu.projectID, nm).Scan(&id); err != nil {
				return "", err
			}
		} else {
			return "", err
		}
	}
	r.stateIDs[key] = id
	r.statesCreated = append(r.statesCreated, nm)
	return id, nil
}

// previewStateName resolves the glance state display name for a Linear
// state without writing: matched → the existing state's real name;
// unmatched → the Linear state name flagged new.
func (r *linearImportRun) previewStateName(stateName string) (string, bool) {
	nm := normalizeLinearStateName(stateName)
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

// ---------- result types ----------

// LinearImportPreviewRow is one issue as the preview endpoint reports
// it (the pinned C31 contract: C31T1 builds against this shape).
type LinearImportPreviewRow struct {
	Identifier      string   `json:"identifier"`
	Title           string   `json:"title"`
	State           string   `json:"state"` // matched glance state name (or the Linear state name when new)
	Priority        int      `json:"priority"`
	Labels          []string `json:"labels"`
	AssigneeEmail   *string  `json:"assignee_email"` // nil when the issue has no assignee
	AssigneeMatched bool     `json:"assignee_matched"`
	DueDate         *string  `json:"due_date"` // YYYY-MM-DD or null
	URL             string   `json:"url"`
}

// LinearImportPreview is the preview response: no writes happened.
type LinearImportPreview struct {
	Issues         []LinearImportPreviewRow `json:"issues"`
	Total          int                      `json:"total"`
	AssigneeMisses int                      `json:"assignee_misses"`
}

// LinearImportError is one per-issue import failure. Identifier (not a
// row ordinal) keys the error to the Linear issue, per the pinned C31
// contract.
type LinearImportError struct {
	Identifier string `json:"identifier"`
	Message    string `json:"message"`
}

// LinearImportResult is the import outcome (the pinned C31 contract).
type LinearImportResult struct {
	Created        int                 `json:"created"`
	Skipped        int                 `json:"skipped"`
	Failed         int                 `json:"failed"`
	Errors         []LinearImportError `json:"errors"`
	AssigneeMisses int                 `json:"assignee_misses"`
}

// LinearTeamsResult is the teams endpoint response.
type LinearTeamsResult struct {
	Teams []LinearTeam `json:"teams"`
}

// newLinearImportRun validates input, gates membership (member 15+ via
// loadImportLookups — same gate as the CSV/GitHub/Jira/Trello
// importers), and seeds the resolution maps. The client is injected so
// tests can point it at a stub.
func newLinearImportRun(ctx context.Context, pool *pgxpool.Pool, client *linearClient,
	wsSlug, identifier, actorID string, in LinearImportInput) (*linearImportRun, string, int, error) {
	teamID, max, err := in.validate()
	if err != nil {
		return nil, "", 0, err
	}
	lu, err := loadImportLookups(ctx, pool, wsSlug, identifier, actorID)
	if err != nil {
		return nil, "", 0, err
	}
	r := &linearImportRun{
		client:     client,
		teamID:     teamID,
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
		return nil, "", 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, "", 0, err
		}
		r.stateNames[id] = name
	}
	if err := rows.Err(); err != nil {
		return nil, "", 0, err
	}
	return r, teamID, max, nil
}

// linearPreviewTimeout bounds the whole preview run.
const linearPreviewTimeout = 2 * time.Minute

// linearImportTimeout bounds the whole import run.
const linearImportTimeout = 10 * time.Minute

// ListLinearTeams returns the teams visible to the API key. Member
// (15)+ — the gate is the same loadImportLookups check as the other
// importers, so a non-member cannot use this project route to probe a
// Linear key.
func ListLinearTeams(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, apiKey string) (*LinearTeamsResult, error) {
	ctx, cancel := context.WithTimeout(ctx, linearPreviewTimeout)
	defer cancel()
	return ListLinearTeamsWithClient(ctx, pool, newLinearClient(apiKey), wsSlug, identifier, actorID, apiKey)
}

// ListLinearTeamsWithClient is the injectable-client variant (tests).
func ListLinearTeamsWithClient(ctx context.Context, pool *pgxpool.Pool, client *linearClient,
	wsSlug, identifier, actorID, apiKey string) (*LinearTeamsResult, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("%w: api_key is required (linear has no anonymous API access)", ErrLinearBadInput)
	}
	if _, err := loadImportLookups(ctx, pool, wsSlug, identifier, actorID); err != nil {
		return nil, err
	}
	teams, err := fetchLinearTeams(ctx, client)
	if err != nil {
		return nil, err
	}
	return &LinearTeamsResult{Teams: teams}, nil
}

// PreviewLinearImport fetches the team's issues and reports how the
// first 10 would map. No writes: labels/states are reported by name
// only (the preview does not say "would create" — C31T1 renders the
// mapped state name; creation happens at import time).
func PreviewLinearImport(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string, in LinearImportInput) (*LinearImportPreview, error) {
	ctx, cancel := context.WithTimeout(ctx, linearPreviewTimeout)
	defer cancel()
	return PreviewLinearImportWithClient(ctx, pool, newLinearClient(in.APIKey), wsSlug, identifier, actorID, in)
}

// PreviewLinearImportWithClient is the injectable-client variant (tests).
func PreviewLinearImportWithClient(ctx context.Context, pool *pgxpool.Pool, client *linearClient,
	wsSlug, identifier, actorID string, in LinearImportInput) (*LinearImportPreview, error) {
	r, teamID, max, err := newLinearImportRun(ctx, pool, client, wsSlug, identifier, actorID, in)
	if err != nil {
		return nil, err
	}
	issues, err := fetchLinearIssues(ctx, r.client, teamID, max)
	if err != nil {
		return nil, err
	}
	out := &LinearImportPreview{
		Issues: []LinearImportPreviewRow{},
		Total:  len(issues),
	}
	// Map ALL fetched issues so assignee_misses reflects the whole
	// fetch, but emit only the first 10 rows.
	for i, li := range issues {
		m := r.mapLinearIssue(li)
		if i >= 10 {
			continue
		}
		stateName, _ := r.previewStateName(m.stateName)
		row := LinearImportPreviewRow{
			Identifier:      m.identifier,
			Title:           m.name,
			State:           stateName,
			Priority:        m.priority,
			Labels:          m.labelNames,
			AssigneeMatched: m.assigneeID != nil,
			DueDate:         m.dueDate,
			URL:             m.url,
		}
		if li.Assignee != nil {
			email := strings.ToLower(strings.TrimSpace(li.Assignee.Email))
			if email != "" {
				email := email
				row.AssigneeEmail = &email
			}
		}
		if row.Labels == nil {
			row.Labels = []string{}
		}
		out.Issues = append(out.Issues, row)
	}
	out.AssigneeMisses = r.assigneeMisses
	return out, nil
}

// ImportLinearIssues fetches the team's issues, then inserts the
// not-yet-imported ones in ONE transaction (same two-phase shape as the
// other importers: per-issue errors collected, never aborting the
// batch). Member (15)+.
func ImportLinearIssues(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string, in LinearImportInput) (*LinearImportResult, error) {
	ctx, cancel := context.WithTimeout(ctx, linearImportTimeout)
	defer cancel()
	return ImportLinearIssuesWithClient(ctx, pool, newLinearClient(in.APIKey), wsSlug, identifier, actorID, in)
}

// ImportLinearIssuesWithClient is the injectable-client variant (tests).
func ImportLinearIssuesWithClient(ctx context.Context, pool *pgxpool.Pool, client *linearClient,
	wsSlug, identifier, actorID string, in LinearImportInput) (*LinearImportResult, error) {
	r, teamID, max, err := newLinearImportRun(ctx, pool, client, wsSlug, identifier, actorID, in)
	if err != nil {
		return nil, err
	}
	issues, err := fetchLinearIssues(ctx, r.client, teamID, max)
	if err != nil {
		return nil, err
	}
	imported, err := loadImportedLinearIdentifiers(ctx, pool, r.lu.projectID)
	if err != nil {
		return nil, err
	}

	res := &LinearImportResult{
		Errors: []LinearImportError{},
	}

	// Phase 1: map (all HTTP already happened in fetchLinearIssues —
	// comments ride inline, so no per-issue fetch can hold the tx).
	var valid []*linearMappedIssue
	for _, li := range issues {
		if imported[li.Identifier] {
			res.Skipped++
			continue
		}
		m := r.mapLinearIssue(li)
		if strings.TrimSpace(m.identifier) == "" {
			res.Failed++
			res.Errors = append(res.Errors, LinearImportError{Identifier: "", Message: "issue has no linear identifier"})
			continue
		}
		if strings.TrimSpace(m.name) == "" {
			res.Failed++
			res.Errors = append(res.Errors, LinearImportError{Identifier: m.identifier, Message: "issue has no title"})
			continue
		}
		if strings.TrimSpace(m.stateName) == "" {
			// An empty state cannot map to a glance state (and must
			// not abort the batch — same per-issue philosophy as the
			// other importers).
			res.Failed++
			res.Errors = append(res.Errors, LinearImportError{Identifier: m.identifier, Message: "issue has no linear state"})
			continue
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

	// Resolve states first (unmatched states create states in the
	// mapped group, inside this tx).
	for _, m := range valid {
		if _, err := r.ensureState(ctx, tx, m.stateName, m.stateGroup); err != nil {
			return nil, err
		}
	}

	// Collect every label name across the batch, ensure them once.
	// (Same shape as the other importers; Linear labels get the default
	// gray — the API color is not carried over, per the plan.)
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
			r.lu.wsID, nm, linearNewLabelColor); err != nil {
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
		stateID := r.stateIDs[strings.ToLower(normalizeLinearStateName(m.stateName))]
		var seq int
		if err := tx.QueryRow(ctx,
			`INSERT INTO issue_sequences (project_id, last_value) VALUES ($1::uuid, 1)
			 ON CONFLICT (project_id) DO UPDATE SET last_value = issue_sequences.last_value + 1
			 RETURNING last_value`,
			r.lu.projectID).Scan(&seq); err != nil {
			return nil, fmt.Errorf("service: increment issue sequence: %w", err)
		}
		var targetDate any
		if m.dueDate != nil {
			targetDate = *m.dueDate
		}
		var issueID string
		err = tx.QueryRow(ctx,
			`INSERT INTO issues (project_id, sequence_id, name, description, priority,
				state_id, target_date, created_by)
			 VALUES ($1::uuid, $2, $3, $4::jsonb, $5, $6::uuid, $7, $8::uuid)
			 RETURNING id::text`,
			r.lu.projectID, seq, m.name, strconv.Quote(m.description),
			m.priority, stateID, targetDate, actorID).Scan(&issueID)
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
		for _, lc := range m.comments {
			cbody := strings.TrimSpace(lc.Body)
			if cbody == "" {
				continue
			}
			who := ""
			if lc.User != nil {
				who = strings.TrimSpace(lc.User.Name)
				if who == "" {
					who = strings.TrimSpace(lc.User.Email)
				}
			}
			if who == "" {
				who = "unknown Linear user"
			}
			when := parseLinearTime(lc.CreatedAt)
			whenStr := lc.CreatedAt
			if when.IsZero() {
				// Unparseable timestamp: fall back to now rather than
				// storing the zero time (Linear's format is stable, so
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
		newVal := fmt.Sprintf(`{"sequence_id":%d,"name":%s,"linear_issue":%s}`,
			seq, strconv.Quote(m.name), strconv.Quote(m.identifier))
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
	return res, nil
}
