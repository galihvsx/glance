package service

// GitHub issues importer (C8T2).
//
// Two endpoints, mirroring the CSV importer's preview/import shape:
//
//	POST .../imports/github/preview — fetches from GitHub, maps, validates.
//	    No writes. Returns the first 10 issues plus the total fetched.
//	POST .../imports/github        — same fetch+map, then inserts.
//
// Request body (JSON): {owner, repo, token, state_filter?, max?}.
//
// Mapping:
//   - name        ← issue title
//   - description ← issue body + a footer marker (see Dedupe below)
//   - state       ← "open" → the project's backlog-group state; "closed" →
//     the first completed-group state (falls back to backlog when the
//     project has no completed state)
//   - priority    ← none (GitHub has no priority) → 0
//   - labels      ← GitHub label names → glance labels, CREATING missing
//     ones (deliberate difference from the CSV importer, which errors on
//     unknown labels: a GitHub import's labels are the point). Colors come
//     from GitHub's hex when valid, else the default gray.
//   - assignees   ← best-effort: each GitHub assignee's public profile
//     email is matched (case-insensitive) against workspace member emails.
//     GitHub hides emails by default, so misses are the common case —
//     they are counted (assignee_misses) and documented, never fatal.
//   - comments    ← fetched per issue (paginated, capped — see
//     githubMaxCommentsPerIssue) and inserted as attributed plain-text
//     comments with their original created_at.
//   - milestones  ← SKIPPED and counted (glance has no milestones).
//   - pull requests ← excluded (they appear in the /issues endpoint).
//
// Dedupe / idempotency (no migration — deliberate): every imported issue
// gets a footer marker in its description:
//
//	*Imported from [o/r#123](https://github.com/o/r/issues/123)*
//
// Before inserting, the run loads every live issue description of the
// project and parses out previously-imported GitHub issue numbers. The
// needle requires the closing paren — `](https://github.com/o/r/issues/`
// + N + `)` — so #12 can never match #123. A re-run skips already-
// imported issues and reports them as `skipped`. Honest limitations:
// deleting the footer defeats dedupe, and issues imported before this
// feature (or created by hand with a matching title) are NOT merged.
//
// Token handling: the PAT travels in the JSON request body only. It is
// sent as an `Authorization: Bearer` header, and is NEVER written to the
// database, NEVER logged (there is no request-body logging anywhere in
// this codebase, and this file logs nothing), and NEVER included in error
// messages. TestGitHubTokenNeverPersisted pins this.
//
// SSRF safety: the host is fixed to https://api.github.com. owner/repo
// are validated as path segments (strict charset, no ".."), and the
// final URL's scheme+host is re-validated on every request
// (validateGitHubHost) — no user-controlled host can reach the dialer.
// Redirects are never followed (a 3xx from api.github.com surfaces as an
// upstream error instead of bouncing the token at an unvetted URL).
// Per-request timeout + an 8MB response cap bound a hostile response.
//
// Notifications: imported comments are inserted as raw rows — they do
// NOT fan out comment notifications or webhook events. A bulk import of
// someone else's history must not ping workspace members.
//
// Error honesty: GitHub's own message is surfaced for 404 (repo not
// found / private without access) and other upstream failures; a bad
// token is 401; a rate-limited response is 429 with Retry-After and is
// NOT retried silently.

import (
	"context"
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
	// githubAPIHost is the ONLY host this importer ever dials.
	githubAPIHost = "api.github.com"
	// githubHTTPTimeout bounds a single GitHub API request so one slow
	// response cannot stall the import (and cannot hold a request
	// goroutine past the caller's context).
	githubHTTPTimeout = 15 * time.Second
	// githubMaxResponseBytes caps a single GitHub API response body; a
	// larger body is an upstream anomaly and fails the request.
	githubMaxResponseBytes = 8 << 20
	// githubPerPage is the GitHub page size (their maximum).
	githubPerPage = 100
	// githubDefaultMax applies when the caller passes max <= 0.
	githubDefaultMax = 100
	// githubAbsoluteMax clamps max: the fetch is sequential page-by-page,
	// so this bounds the worst-case API cost of one import.
	githubAbsoluteMax = 1000
	// githubMaxCommentsPerIssue caps comment fetching per issue (2 pages
	// of 100). Importing unbounded comment threads would let one noisy
	// issue dominate the run.
	githubMaxCommentsPerIssue = 200
)

var (
	// ErrGitHubBadInput is returned for malformed import input: bad
	// owner/repo, bad state_filter, etc. It fails the request (400).
	ErrGitHubBadInput = errors.New("service: invalid github import input")
	// ErrGitHubRepoNotFound is returned when GitHub answers 404: the
	// repo does not exist, or it is private and the token cannot see it.
	// GitHub's own message is surfaced (404).
	ErrGitHubRepoNotFound = errors.New("service: github repository not found")
	// ErrGitHubUnauthorized is returned when GitHub answers 401: the
	// token is missing, malformed, or revoked (401).
	ErrGitHubUnauthorized = errors.New("service: github authentication failed")
	// ErrGitHubUpstream is returned for any other non-2xx from GitHub
	// (403 without rate-limit exhaustion, 5xx, unexpected 3xx). It maps
	// to 502 with GitHub's message surfaced.
	ErrGitHubUpstream = errors.New("service: github api error")
	// errGitHubSSRF is returned when a constructed request URL does not
	// point at https://api.github.com exactly. Internal: never user-
	// visible on its own (it indicates a code bug, not user error).
	errGitHubSSRF = errors.New("service: github request target must be https://api.github.com")
)

// githubRepoRe pins owner/repo to GitHub's name charset. It doubles as
// the path-traversal guard: no "/", no "..", no encoding tricks —
// validated segments are interpolated into the path directly.
var githubRepoRe = regexp.MustCompile(`^[A-Za-z0-9_.\-]+$`)

// githubLabelColorRe pins GitHub's 6-hex-digit label colors so a
// malformed color can never reach the labels table (which the web client
// renders blindly as CSS).
var githubLabelColorRe = regexp.MustCompile(`^[0-9a-fA-F]{6}$`)

// GitHubRateLimitError is returned when GitHub reports rate-limit
// exhaustion (403 with X-RateLimit-Remaining: 0, or 429). It is NOT
// retried: the handler maps it to 429 with the Retry-After header so the
// caller decides when to retry.
type GitHubRateLimitError struct {
	RetryAfter int    // seconds until X-RateLimit-Reset
	Message    string // GitHub's message, safe to surface
}

func (e *GitHubRateLimitError) Error() string {
	return "service: github rate limit exceeded: " + e.Message
}

// GitHubImportInput is the JSON body of the github import endpoints.
// Token may be empty for public repos (unauthenticated rate limits
// apply); it is never stored.
type GitHubImportInput struct {
	Owner       string `json:"owner"`
	Repo        string `json:"repo"`
	Token       string `json:"token"`
	StateFilter string `json:"state_filter"`
	Max         int    `json:"max"`
}

// validate checks the input and returns the normalized state filter and
// max. Owner/repo go through the strict name regex (path-safety); the
// token is opaque and never validated here.
func (in GitHubImportInput) validate() (state string, max int, err error) {
	owner := strings.TrimSpace(in.Owner)
	repo := strings.TrimSpace(in.Repo)
	if owner == "" || repo == "" ||
		len(owner) > 100 || len(repo) > 100 ||
		!githubRepoRe.MatchString(owner) || !githubRepoRe.MatchString(repo) ||
		owner == "." || owner == ".." || repo == "." || repo == ".." {
		return "", 0, fmt.Errorf("%w: owner/repo must be 1-100 chars of [A-Za-z0-9_.-]", ErrGitHubBadInput)
	}
	switch f := strings.ToLower(strings.TrimSpace(in.StateFilter)); f {
	case "", "open":
		state = "open"
	case "closed", "all":
		state = f
	default:
		return "", 0, fmt.Errorf("%w: state_filter must be open, closed, or all", ErrGitHubBadInput)
	}
	max = in.Max
	if max <= 0 {
		max = githubDefaultMax
	}
	if max > githubAbsoluteMax {
		max = githubAbsoluteMax
	}
	return state, max, nil
}

// ---------- GitHub API client (SSRF-safe) ----------

// githubClient talks to the GitHub REST API. The baseURL is
// https://api.github.com in production; tests inject an httptest URL via
// newGitHubTestClient (the same "tests only" pattern as
// WebhookDispatcher.allowPrivateTargets). It is NEVER derived from user
// input — owner/repo become path segments only.
type githubClient struct {
	http *http.Client
	// token is the caller's PAT, used transiently as a Bearer header.
	// It is never logged, never stored, never echoed in errors.
	token string
	// baseURL is scheme + host only (no path).
	baseURL string
	// enforceHost validates scheme+host == https://api.github.com on
	// every request. False only for the test client (whose override URL
	// is injected by test code, never user input).
	enforceHost bool
}

func newGitHubHTTPClient() *http.Client {
	return &http.Client{
		Timeout: githubHTTPTimeout,
		// Redirects are NEVER followed: the SSRF guard vets the original
		// target, and the Authorization header must not be bounced at an
		// unvetted URL. A 3xx surfaces as an upstream error.
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
		},
	}
}

// newGitHubClient builds the production client (fixed api.github.com
// host, host enforcement on).
func newGitHubClient(token string) *githubClient {
	return &githubClient{
		http:        newGitHubHTTPClient(),
		token:       token,
		baseURL:     "https://" + githubAPIHost,
		enforceHost: true,
	}
}

// newGitHubTestClient builds a client pointed at a stub server. Tests
// only — production code must never call this.
func newGitHubTestClient(token, baseURL string) *githubClient {
	return &githubClient{
		http:        newGitHubHTTPClient(),
		token:       token,
		baseURL:     strings.TrimSuffix(baseURL, "/"),
		enforceHost: false,
	}
}

// urlFor builds the full request URL for an API path and validates the
// target. The path is always constructed internally from validated
// segments — this is defense-in-depth against any future refactor that
// might interpolate user input into the URL.
func (c *githubClient) urlFor(path string) (string, error) {
	u, err := url.Parse(c.baseURL + path)
	if err != nil {
		return "", fmt.Errorf("%w: %v", errGitHubSSRF, err)
	}
	if c.enforceHost && (u.Scheme != "https" || u.Hostname() != githubAPIHost) {
		return "", fmt.Errorf("%w (got %q)", errGitHubSSRF, u.Hostname())
	}
	return u.String(), nil
}

// githubAPIError is GitHub's error envelope: {"message": "..."}.
type githubAPIError struct {
	Message string `json:"message"`
}

// get performs one authenticated GET and returns the capped body. Non-2xx
// responses become typed errors; the token never appears in any of them.
func (c *githubClient) get(ctx context.Context, path string) ([]byte, http.Header, error) {
	raw, err := c.urlFor(path)
	if err != nil {
		return nil, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: request failed: %v", ErrGitHubUpstream, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, githubMaxResponseBytes+1))
	if err != nil {
		return nil, nil, fmt.Errorf("%w: read body: %v", ErrGitHubUpstream, err)
	}
	if len(body) > githubMaxResponseBytes {
		return nil, nil, fmt.Errorf("%w: response exceeded size limit", ErrGitHubUpstream)
	}
	if resp.StatusCode == http.StatusOK {
		return body, resp.Header, nil
	}
	// Error path: surface GitHub's own message (best-effort decode).
	var apiErr githubAPIError
	_ = json.Unmarshal(body, &apiErr)
	msg := strings.TrimSpace(apiErr.Message)
	if msg == "" {
		msg = http.StatusText(resp.StatusCode)
	}
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return nil, nil, fmt.Errorf("%w: %s", ErrGitHubUnauthorized, msg)
	case http.StatusNotFound:
		return nil, nil, fmt.Errorf("%w: %s", ErrGitHubRepoNotFound, msg)
	case http.StatusForbidden, http.StatusTooManyRequests:
		if resp.Header.Get("X-RateLimit-Remaining") == "0" || resp.StatusCode == http.StatusTooManyRequests {
			retryAfter := 60 // fallback when the reset header is missing
			if reset := resp.Header.Get("X-RateLimit-Reset"); reset != "" {
				if ts, err := strconv.ParseInt(reset, 10, 64); err == nil {
					if d := int(ts - time.Now().Unix()); d > 0 {
						retryAfter = d
					}
				}
			}
			return nil, nil, &GitHubRateLimitError{RetryAfter: retryAfter, Message: msg}
		}
		return nil, nil, fmt.Errorf("%w: %s", ErrGitHubUpstream, msg)
	default:
		return nil, nil, fmt.Errorf("%w: %s", ErrGitHubUpstream, msg)
	}
}

// checkNextURL validates a Link-header next URL: it must share the
// scheme+host of the client's own baseURL. In production that is
// https://api.github.com; in tests it is the stub server. A hostile
// Link header (different host) aborts the fetch before any follow-up
// request — and the Authorization header — goes there. Returns the
// request path to fetch next.
func (c *githubClient) checkNextURL(raw string) (string, error) {
	nu, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("%w: bad link header url: %v", ErrGitHubUpstream, err)
	}
	base, err := url.Parse(c.baseURL)
	if err != nil {
		return "", fmt.Errorf("%w: bad client base url: %v", errGitHubSSRF, err)
	}
	if nu.Scheme != base.Scheme || nu.Host != base.Host {
		return "", fmt.Errorf("%w (link header pointed at %q)", errGitHubSSRF, nu.Host)
	}
	return nu.RequestURI(), nil
}

// parseGitHubNextLink extracts the rel="next" URL from a GitHub Link
// header. Empty string = no further pages.
func parseGitHubNextLink(header string) string {
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if !strings.Contains(part, `rel="next"`) {
			continue
		}
		start := strings.Index(part, "<")
		end := strings.Index(part, ">")
		if start >= 0 && end > start {
			return part[start+1 : end]
		}
	}
	return ""
}

// ---------- GitHub payload types ----------

type githubLabel struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

type githubUserRef struct {
	Login string `json:"login"`
}

type githubMilestone struct {
	Title string `json:"title"`
}

type githubIssue struct {
	Number    int              `json:"number"`
	Title     string           `json:"title"`
	Body      *string          `json:"body"`
	State     string           `json:"state"` // "open" | "closed"
	HTMLURL   string           `json:"html_url"`
	Labels    []githubLabel    `json:"labels"`
	Assignees []githubUserRef  `json:"assignees"`
	Milestone *githubMilestone `json:"milestone"`
	Comments  int              `json:"comments"`
	// PullRequest is present (non-null) for pull requests, which the
	// /issues endpoint also returns. Presence = skip.
	PullRequest *json.RawMessage `json:"pull_request"`
	CreatedAt   time.Time        `json:"created_at"`
	UpdatedAt   time.Time        `json:"updated_at"`
}

type githubComment struct {
	User      githubUserRef `json:"user"`
	Body      *string       `json:"body"`
	HTMLURL   string        `json:"html_url"`
	CreatedAt time.Time     `json:"created_at"`
}

type githubUser struct {
	Login string  `json:"login"`
	Email *string `json:"email"` // null when private (the common case)
}

// ---------- fetch ----------

// fetchGitHubIssues pages /repos/{owner}/{repo}/issues via Link headers
// until max issues are collected or the pages run out. Pull requests are
// filtered out here (counted for the result summary).
func fetchGitHubIssues(ctx context.Context, c *githubClient, owner, repo, state string, max int) (issues []githubIssue, prsSkipped int, err error) {
	path := fmt.Sprintf("/repos/%s/%s/issues?state=%s&per_page=%d",
		url.PathEscape(owner), url.PathEscape(repo), state, githubPerPage)
	for {
		body, hdr, err := c.get(ctx, path)
		if err != nil {
			return nil, 0, err
		}
		var page []githubIssue
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, 0, fmt.Errorf("%w: decode issues: %v", ErrGitHubUpstream, err)
		}
		for _, is := range page {
			if is.PullRequest != nil {
				prsSkipped++
				continue
			}
			issues = append(issues, is)
			if len(issues) >= max {
				return issues, prsSkipped, nil
			}
		}
		next := parseGitHubNextLink(hdr.Get("Link"))
		if next == "" {
			return issues, prsSkipped, nil
		}
		path, err = c.checkNextURL(next)
		if err != nil {
			return nil, 0, err
		}
	}
}

// fetchGitHubComments pages an issue's comments, capped at
// githubMaxCommentsPerIssue.
func fetchGitHubComments(ctx context.Context, c *githubClient, owner, repo string, number int) ([]githubComment, error) {
	path := fmt.Sprintf("/repos/%s/%s/issues/%d/comments?per_page=%d",
		url.PathEscape(owner), url.PathEscape(repo), number, githubPerPage)
	var out []githubComment
	for {
		body, hdr, err := c.get(ctx, path)
		if err != nil {
			return nil, err
		}
		var page []githubComment
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("%w: decode comments: %v", ErrGitHubUpstream, err)
		}
		out = append(out, page...)
		if len(out) >= githubMaxCommentsPerIssue {
			return out[:githubMaxCommentsPerIssue], nil
		}
		next := parseGitHubNextLink(hdr.Get("Link"))
		if next == "" {
			return out, nil
		}
		path, err = c.checkNextURL(next)
		if err != nil {
			return nil, err
		}
	}
}

// ---------- dedupe marker ----------

// githubIssueFooter is appended to every imported issue's description.
// It is both the user-visible provenance and the dedupe marker.
func githubIssueFooter(owner, repo string, number int) string {
	return fmt.Sprintf("\n\n---\n*Imported from [%s/%s#%d](https://github.com/%s/%s/issues/%d)*",
		owner, repo, number, owner, repo, number)
}

// parseImportedGitHubNumbers scans issue descriptions for dedupe markers
// and returns the set of GitHub issue numbers already imported from this
// owner/repo. Single pass over the descriptions — O(total length).
func parseImportedGitHubNumbers(descriptions []string, owner, repo string) map[int]bool {
	prefix := "](https://github.com/" + owner + "/" + repo + "/issues/"
	out := map[int]bool{}
	for _, d := range descriptions {
		rest := d
		for {
			i := strings.Index(rest, prefix)
			if i < 0 {
				break
			}
			rest = rest[i+len(prefix):]
			j := 0
			for j < len(rest) && rest[j] >= '0' && rest[j] <= '9' {
				j++
			}
			// The closing paren is what disambiguates #12 from #123.
			if j > 0 && j < len(rest) && rest[j] == ')' {
				if n, err := strconv.Atoi(rest[:j]); err == nil {
					out[n] = true
				}
			}
		}
	}
	return out
}

// loadImportedGitHubNumbers returns the set of GitHub issue numbers
// already imported into this project from owner/repo (dedupe scan).
func loadImportedGitHubNumbers(ctx context.Context, pool *pgxpool.Pool, projectID, owner, repo string) (map[int]bool, error) {
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
	return parseImportedGitHubNumbers(descs, owner, repo), nil
}

// ---------- mapping ----------

// githubTargetStates resolves the glance states GitHub open/closed map
// to: open → backlog-group state; closed → first completed-group state,
// falling back to the backlog state when the project has no completed
// state (e.g. a customized taxonomy).
func githubTargetStates(ctx context.Context, q queryRower, projectID string) (openID, closedID string, err error) {
	openID, err = backlogState(ctx, q, projectID)
	if err != nil {
		return "", "", err
	}
	err = q.QueryRow(ctx,
		`SELECT id::text FROM states
		  WHERE project_id = $1::uuid AND "group" = 'completed'
		  ORDER BY sequence LIMIT 1`, projectID).Scan(&closedID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			closedID = openID // no completed state: keep it honest, don't invent one
		} else {
			return "", "", err
		}
	}
	return openID, closedID, nil
}

// githubMappedIssue is one GitHub issue mapped to glance fields, ready to
// insert (labels/assignees resolved against the run's lookup maps).
type githubMappedIssue struct {
	gh          githubIssue
	name        string
	description string // body + footer marker (marker ALWAYS present)
	stateID     string
	labelIDs    []string
	labelNames  []string // for label creation bookkeeping
	assigneeID  *string
	comments    []githubComment
}

// assigneeCacheEntry memoizes one login's profile lookup for the run.
type assigneeCacheEntry struct {
	userID string
	ok     bool
}

// githubImportRun carries per-run state: the client, lookups, target
// states, and the assignee profile cache.
type githubImportRun struct {
	client        *githubClient
	owner         string
	repo          string
	lu            *importLookups
	openState     string
	closedState   string
	assigneeCache map[string]assigneeCacheEntry
	// assigneeMisses counts (issue, login) pairs with no member match —
	// surfaced in the result, documented for the caller.
	assigneeMisses int
	// labelsCreated accumulates names of labels created during the run.
	labelsCreated []string
	// labelIDs memoizes resolved label ids (existing + created).
	labelIDs map[string]string // lower(name) -> id
}

// resolveAssignee best-effort matches a GitHub login to a workspace
// member via the login's public profile email. Any failure — 404, private
// email, transient error — is a MISS, never fatal: assignee lookup must
// not abort an import of real data.
func (r *githubImportRun) resolveAssignee(ctx context.Context, login string) *string {
	if e, ok := r.assigneeCache[login]; ok {
		if !e.ok {
			return nil
		}
		id := e.userID
		return &id
	}
	entry := assigneeCacheEntry{}
	defer func() { r.assigneeCache[login] = entry }()
	body, _, err := r.client.get(ctx, "/users/"+url.PathEscape(login))
	if err != nil {
		return nil // best-effort: profile fetch failure = miss
	}
	var gu githubUser
	if err := json.Unmarshal(body, &gu); err != nil {
		return nil
	}
	if gu.Email == nil || strings.TrimSpace(*gu.Email) == "" {
		return nil // private email: the common case
	}
	id, ok := r.lu.members[strings.ToLower(strings.TrimSpace(*gu.Email))]
	if !ok {
		return nil
	}
	entry = assigneeCacheEntry{userID: id, ok: true}
	out := id
	return &out
}

// normalizeGitHubLabelName trims and truncates to the labels table's 120-
// char contract (same bound CreateLabel enforces). Truncation is by rune
// so multi-byte characters are never split. Empty → "" (skipped).
func normalizeGitHubLabelName(name string) string {
	name = strings.TrimSpace(name)
	if r := []rune(name); len(r) > 120 {
		name = string(r[:120])
	}
	return name
}

// githubLabelColor maps a GitHub label color to a glance-safe CSS color.
func githubLabelColor(ghColor string) string {
	if githubLabelColorRe.MatchString(strings.TrimSpace(ghColor)) {
		return "#" + strings.TrimSpace(ghColor)
	}
	return "#6b7280"
}

// mapGitHubIssue maps one fetched issue to glance fields. Labels are
// resolved to ids (creating missing ones is the caller's job — preview
// must not write); the assignee is resolved best-effort.
func (r *githubImportRun) mapGitHubIssue(ctx context.Context, gh githubIssue) *githubMappedIssue {
	name := strings.TrimSpace(gh.Title)
	if name == "" {
		name = fmt.Sprintf("%s/%s#%d", r.owner, r.repo, gh.Number)
	}
	body := ""
	if gh.Body != nil {
		body = strings.TrimSpace(*gh.Body)
	}
	m := &githubMappedIssue{
		gh:          gh,
		name:        name,
		description: body + githubIssueFooter(r.owner, r.repo, gh.Number),
	}
	if strings.ToLower(gh.State) == "closed" {
		m.stateID = r.closedState
	} else {
		m.stateID = r.openState
	}
	seen := map[string]bool{}
	for _, l := range gh.Labels {
		nm := normalizeGitHubLabelName(l.Name)
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
	// Every assignee is evaluated (each miss is documented); the first
	// successful match takes glance's single assignee slot for the issue.
	for _, a := range gh.Assignees {
		login := strings.TrimSpace(a.Login)
		if login == "" {
			continue
		}
		if id := r.resolveAssignee(ctx, login); id != nil {
			if m.assigneeID == nil {
				m.assigneeID = id
			}
		} else {
			r.assigneeMisses++
		}
	}
	return m
}

// ---------- result types ----------

// GitHubImportPreviewRow is one issue as the preview endpoint reports it.
type GitHubImportPreviewRow struct {
	Number          int      `json:"number"`
	Title           string   `json:"title"`
	State           string   `json:"state"`
	Labels          []string `json:"labels"`
	NewLabels       []string `json:"new_labels"`
	Assignees       []string `json:"assignees"`
	AssigneeMatched bool     `json:"assignee_matched"`
	Comments        int      `json:"comments"`
	Milestone       *string  `json:"milestone,omitempty"`
	AlreadyImported bool     `json:"already_imported"`
}

// GitHubImportPreview is the preview response: no writes happened.
type GitHubImportPreview struct {
	Source string                   `json:"source"`
	Total  int                      `json:"total"`
	Rows   []GitHubImportPreviewRow `json:"rows"`
}

// GitHubImportResult is the import outcome. Errors reuse ImportRowError
// with Row carrying the GitHub issue number (documented — there are no
// CSV rows here), so the frontend renders both importers identically.
type GitHubImportResult struct {
	Source            string           `json:"source"`
	Created           int              `json:"created"`
	Skipped           int              `json:"skipped"`
	PRsSkipped        int              `json:"prs_skipped"`
	Failed            int              `json:"failed"`
	LabelsCreated     []string         `json:"labels_created"`
	AssigneeMisses    int              `json:"assignee_misses"`
	MilestonesSkipped int              `json:"milestones_skipped"`
	Errors            []ImportRowError `json:"errors"`
}

// previewRow builds the preview row for one mapped issue.
func previewRow(m *githubMappedIssue, alreadyImported bool) GitHubImportPreviewRow {
	row := GitHubImportPreviewRow{
		Number:          m.gh.Number,
		Title:           m.name,
		State:           m.gh.State,
		Comments:        m.gh.Comments,
		AlreadyImported: alreadyImported,
		AssigneeMatched: m.assigneeID != nil,
	}
	for _, l := range m.gh.Labels {
		if nm := normalizeGitHubLabelName(l.Name); nm != "" {
			row.Labels = append(row.Labels, nm)
		}
	}
	// NewLabels = labels not already in the workspace (would be created).
	// Note: r.labelIDs isn't accessible here; compute in the caller.
	if m.gh.Milestone != nil {
		t := strings.TrimSpace(m.gh.Milestone.Title)
		if t != "" {
			row.Milestone = &t
		}
	}
	for _, a := range m.gh.Assignees {
		if login := strings.TrimSpace(a.Login); login != "" {
			row.Assignees = append(row.Assignees, login)
		}
	}
	return row
}

// newGitHubImportRun validates input, gates membership (member 15+ via
// loadImportLookups — same gate as the CSV importer), and resolves the
// target states. The client is injected so tests can point it at a stub.
func newGitHubImportRun(ctx context.Context, pool *pgxpool.Pool, client *githubClient,
	wsSlug, identifier, actorID string, in GitHubImportInput) (*githubImportRun, string, int, error) {
	state, max, err := in.validate()
	if err != nil {
		return nil, "", 0, err
	}
	lu, err := loadImportLookups(ctx, pool, wsSlug, identifier, actorID)
	if err != nil {
		return nil, "", 0, err
	}
	openID, closedID, err := githubTargetStates(ctx, pool, lu.projectID)
	if err != nil {
		return nil, "", 0, err
	}
	r := &githubImportRun{
		client:        client,
		owner:         strings.TrimSpace(in.Owner),
		repo:          strings.TrimSpace(in.Repo),
		lu:            lu,
		openState:     openID,
		closedState:   closedID,
		assigneeCache: map[string]assigneeCacheEntry{},
		labelIDs:      map[string]string{},
	}
	for k, v := range lu.labels {
		r.labelIDs[k] = v
	}
	return r, state, max, nil
}

// githubPreviewTimeout bounds the whole preview run: per-request
// timeouts (githubHTTPTimeout) cap each call, but a large max could
// otherwise stack many slow pages behind one HTTP request.
const githubPreviewTimeout = 2 * time.Minute

// githubImportTimeout bounds the whole import run for the same reason
// (issue pages + per-issue comment pages + assignee profile lookups).
const githubImportTimeout = 10 * time.Minute

// PreviewGitHubImport fetches the issues and reports how the first 10
// would map. No writes: labels are reported as "would create", comments
// are counted but their bodies are NOT fetched (import time only).
func PreviewGitHubImport(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string, in GitHubImportInput) (*GitHubImportPreview, error) {
	ctx, cancel := context.WithTimeout(ctx, githubPreviewTimeout)
	defer cancel()
	return PreviewGitHubImportWithClient(ctx, pool, newGitHubClient(in.Token), wsSlug, identifier, actorID, in)
}

// PreviewGitHubImportWithClient is the injectable-client variant (tests).
func PreviewGitHubImportWithClient(ctx context.Context, pool *pgxpool.Pool, client *githubClient,
	wsSlug, identifier, actorID string, in GitHubImportInput) (*GitHubImportPreview, error) {
	r, state, max, err := newGitHubImportRun(ctx, pool, client, wsSlug, identifier, actorID, in)
	if err != nil {
		return nil, err
	}
	issues, _, err := fetchGitHubIssues(ctx, r.client, r.owner, r.repo, state, max)
	if err != nil {
		return nil, err
	}
	imported, err := loadImportedGitHubNumbers(ctx, pool, r.lu.projectID, r.owner, r.repo)
	if err != nil {
		return nil, err
	}
	out := &GitHubImportPreview{
		Source: r.owner + "/" + r.repo,
		Total:  len(issues),
		Rows:   []GitHubImportPreviewRow{},
	}
	n := len(issues)
	if n > 10 {
		n = 10
	}
	for i := 0; i < n; i++ {
		m := r.mapGitHubIssue(ctx, issues[i])
		row := previewRow(m, imported[issues[i].Number])
		for _, nm := range m.labelNames {
			if _, ok := r.labelIDs[strings.ToLower(nm)]; !ok {
				row.NewLabels = append(row.NewLabels, nm)
			}
		}
		out.Rows = append(out.Rows, row)
	}
	return out, nil
}

// ImportGitHubIssues fetches the issues, then inserts the not-yet-
// imported ones in ONE transaction (same two-phase shape as the CSV
// importer: per-issue errors collected, never aborting the batch).
// Member (15)+.
func ImportGitHubIssues(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string, in GitHubImportInput) (*GitHubImportResult, error) {
	ctx, cancel := context.WithTimeout(ctx, githubImportTimeout)
	defer cancel()
	return ImportGitHubIssuesWithClient(ctx, pool, newGitHubClient(in.Token), wsSlug, identifier, actorID, in)
}

// ImportGitHubIssuesWithClient is the injectable-client variant (tests).
func ImportGitHubIssuesWithClient(ctx context.Context, pool *pgxpool.Pool, client *githubClient,
	wsSlug, identifier, actorID string, in GitHubImportInput) (*GitHubImportResult, error) {
	r, state, max, err := newGitHubImportRun(ctx, pool, client, wsSlug, identifier, actorID, in)
	if err != nil {
		return nil, err
	}
	issues, prsSkipped, err := fetchGitHubIssues(ctx, r.client, r.owner, r.repo, state, max)
	if err != nil {
		return nil, err
	}
	imported, err := loadImportedGitHubNumbers(ctx, pool, r.lu.projectID, r.owner, r.repo)
	if err != nil {
		return nil, err
	}

	res := &GitHubImportResult{
		Source:        r.owner + "/" + r.repo,
		PRsSkipped:    prsSkipped,
		Errors:        []ImportRowError{},
		LabelsCreated: []string{},
	}

	// Phase 1: map + fetch comments (all HTTP happens BEFORE the tx — a
	// slow comment page must never hold a pool connection).
	var valid []*githubMappedIssue
	for _, gh := range issues {
		if imported[gh.Number] {
			res.Skipped++
			continue
		}
		m := r.mapGitHubIssue(ctx, gh)
		if strings.TrimSpace(m.name) == "" {
			res.Failed++
			res.Errors = append(res.Errors, ImportRowError{Row: gh.Number, Message: "issue has no title"})
			continue
		}
		if m.gh.Milestone != nil {
			res.MilestonesSkipped++
		}
		if gh.Comments > 0 {
			comments, err := fetchGitHubComments(ctx, r.client, r.owner, r.repo, gh.Number)
			if err != nil {
				// Comments are enrichment, not the issue itself: a
				// failed comment fetch fails the ISSUE's comments, not
				// the issue. Record and continue with what we have.
				res.Errors = append(res.Errors, ImportRowError{
					Row:     gh.Number,
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

	// Collect every label name across the batch, ensure them once.
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
	// GitHub colors for the labels being created (first color seen wins
	// when two issues disagree on a label's color).
	labelColors := map[string]string{} // lower(name) -> #rrggbb
	for _, m := range valid {
		for _, l := range m.gh.Labels {
			nm := normalizeGitHubLabelName(l.Name)
			key := strings.ToLower(nm)
			if nm == "" || !seenLabels[key] {
				continue
			}
			if _, ok := labelColors[key]; !ok {
				labelColors[key] = githubLabelColor(l.Color)
			}
		}
	}
	for _, nm := range allLabels {
		key := strings.ToLower(nm)
		if _, ok := r.labelIDs[key]; ok {
			continue
		}
		color := labelColors[key]
		if color == "" {
			color = "#6b7280"
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO labels (workspace_id, name, color)
			 VALUES ($1::uuid, $2, $3)
			 ON CONFLICT (workspace_id, name) DO NOTHING`,
			r.lu.wsID, nm, color); err != nil {
			return nil, err
		}
		var id string
		if err := tx.QueryRow(ctx,
			`SELECT id::text FROM labels WHERE workspace_id = $1::uuid AND name = $2`,
			r.lu.wsID, nm).Scan(&id); err != nil {
			return nil, err
		}
		r.labelIDs[key] = id
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
			 VALUES ($1::uuid, $2, $3, $4::jsonb, 0, $5::uuid, $6::uuid)
			 RETURNING id::text`,
			r.lu.projectID, seq, m.name, strconv.Quote(m.description),
			m.stateID, actorID).Scan(&issueID)
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
		for _, gc := range m.comments {
			cbody := ""
			if gc.Body != nil {
				cbody = strings.TrimSpace(*gc.Body)
			}
			if cbody == "" {
				continue
			}
			attributed := fmt.Sprintf("%s\n\n— originally posted by @%s on %s (%s)",
				cbody, gc.User.Login,
				gc.CreatedAt.UTC().Format("2006-01-02 15:04 MST"), gc.HTMLURL)
			if _, err := tx.Exec(ctx,
				`INSERT INTO comments (issue_id, actor_id, content, created_at)
				 VALUES ($1::uuid, $2::uuid, $3::jsonb, $4)`,
				issueID, actorID, strconv.Quote(attributed), gc.CreatedAt); err != nil {
				return nil, err
			}
		}
		newVal := fmt.Sprintf(`{"sequence_id":%d,"name":%s,"github_issue":%d}`,
			seq, strconv.Quote(m.name), m.gh.Number)
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
	return res, nil
}
