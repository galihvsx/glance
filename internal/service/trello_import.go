package service

// Trello Cloud importer (C10T0).
//
// Two endpoints, mirroring the GitHub/Jira importer preview/import shape:
//
//	POST .../imports/trello/preview — fetches from the Trello REST v1 API,
//	    maps, validates. No writes. Returns the first 10 cards plus the
//	    total fetched.
//	POST .../imports/trello        — same fetch+map, then inserts.
//
// Request body (JSON): {api_key, token, board_id, max?}. `board_id`
// accepts the 24-hex board id, the 8-char short link, or an
// https://trello.com/b/<shortlink>/... board URL (from which the short
// link is extracted).
//
// TRELLO CLOUD ONLY — Trello has no self-hosted edition, so the host is
// fixed to https://api.trello.com and re-validated on every request via
// the same SSRF-guard pattern as the GitHub/Jira importers (no
// user-controlled host, https only, redirects never followed). The
// board id is a charset-pinned path segment only — it can never steer a
// request off-host.
//
// Mapping:
//   - name        ← card name (falls back to the card short link)
//   - description ← card desc (Trello uses Markdown — kept as-is) + a
//     footer marker (see Dedupe below)
//   - state       ← the card's LIST name matched case-insensitively
//     against glance states. UNMATCHED lists get a NEW state, created in
//     the "unstarted" group (same documented deviation as the Jira
//     importer: glance's state-group vocabulary is
//     triage/backlog/unstarted/started/completed/cancelled — there is no
//     "Default" group; "unstarted" is the neutral tracked group).
//   - priority    ← none (Trello has no priority) → 0
//   - labels      ← Trello board labels (name + color) → glance labels,
//     CREATING missing ones (same deliberate choice as the GitHub/Jira
//     importers). Colors are mapped from Trello's fixed palette
//     (trelloLabelHex) to hex; unknown colors fall back to the default
//     gray. Color-only labels (Trello allows nameless labels) surface
//     as the capitalized color name (e.g. "Sky").
//   - target_date ← card due date (date part, UTC); unparseable or
//     absent due → no target date (documented, never fatal)
//   - comments    ← fetched per card (before-cursor pagination, capped —
//     see trelloMaxCommentsPerCard) and inserted as attributed
//     plain-text comments with their original created_at
//   - assignees   ← best-effort: the card's board members' emails are
//     matched (case-insensitive) against workspace member emails. Trello
//     does not always expose member emails, so misses are the common
//     case — counted (assignee_misses), never fatal
//   - archived cards ← EXCLUDED and counted (archived_skipped): an
//     archived card is hidden from the board; importing it would
//     surprise. Cards are fetched with filter=all so the client does the
//     filtering honestly and reports the count
//   - checklists  ← SKIPPED and counted (checklists_skipped). Documented
//     why: glance has no checklist/subtask item model — importing them
//     would require a new table and a UI surface (a schema migration,
//     which this task forbids). The count comes from the card badges so
//     the caller sees exactly what was left behind. Checklist import is
//     future work alongside the data model for it.
//
// Dedupe / idempotency (no migration — deliberate, consistent with the
// GitHub/Jira importers' contract-first choice): every imported card
// gets a footer marker in its description:
//
//	*Imported from [Ship v2](https://trello.com/c/abc12345)*
//
// Before inserting, the run loads every live issue description of the
// project and parses out previously-imported card short links. The
// needle requires the closing paren — `](https://trello.com/c/` + LINK
// + `)` — so abc1 can never match abc12. A re-run skips already-imported
// cards and reports them as `skipped`. Honest limitations (same as
// GitHub/Jira): deleting the footer defeats dedupe, and cards imported
// before this feature are NOT merged.
//
// Credential handling: the API key + token travel in the JSON request
// body only. Trello's REST v1 API authenticates via `key`+`token` QUERY
// PARAMETERS (not headers — that is Trello's contract, unavoidable), so
// they ride the request URL to the pinned api.trello.com host only. They
// are NEVER written to the database, NEVER logged (no request logging
// anywhere in this codebase, and this file logs nothing), and NEVER
// included in error messages. TestTrelloTokenNeverPersisted pins this.
//
// SSRF safety: the host is fixed to https://api.trello.com. The board id
// is validated to the 24-hex / 8-char-shortlink charset (or extracted
// from an https trello.com board URL) and interpolated as a path segment
// only; the final URL's scheme+host is re-validated on every request
// (trelloClient.urlFor). Redirects are never followed (a 3xx surfaces as
// an upstream error instead of bouncing the credentials at an unvetted
// URL). Per-request timeout + an 8MB response cap bound a hostile
// response. The before-cursor is an opaque card id echoed back in the
// query only — it can never steer a request off-host (a pathological
// cursor sequence aborts via the page backstop).
//
// Notifications: imported comments are inserted as raw rows — they do
// NOT fan out comment notifications or webhook events. A bulk import of
// someone else's history must not ping workspace members.
//
// Error honesty: Trello's own message is surfaced for 404 (bad board id
// or no access) and other upstream failures; a bad key/token is 401; a
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
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// trelloAPIHost is the ONLY host this importer ever dials.
	trelloAPIHost = "api.trello.com"
	// trelloHTTPTimeout bounds a single Trello API request so one slow
	// response cannot stall the import (and cannot hold a request
	// goroutine past the caller's context).
	trelloHTTPTimeout = 15 * time.Second
	// trelloMaxResponseBytes caps a single Trello API response body; a
	// larger body is an upstream anomaly and fails the request.
	trelloMaxResponseBytes = 8 << 20
	// trelloPerPage is the card/comment page size (Trello's max is 1000;
	// 100 keeps per-page bodies small on big boards).
	trelloPerPage = 100
	// trelloDefaultMax applies when the caller passes max <= 0.
	trelloDefaultMax = 100
	// trelloAbsoluteMax clamps max: the fetch is sequential page-by-page,
	// so this bounds the worst-case API cost of one import.
	trelloAbsoluteMax = 1000
	// trelloMaxCommentsPerCard caps comment fetching per card (2 pages
	// of 100). Importing unbounded comment threads would let one noisy
	// card dominate the run.
	trelloMaxCommentsPerCard = 200
	// trelloMaxPages is a backstop against a pathological before-cursor
	// sequence; the page-exhaustion check is the primary loop guard.
	trelloMaxPages = 50
	// trelloNewStateGroup is the group for auto-created states (see the
	// package doc: glance has no "Default" group).
	trelloNewStateGroup = "unstarted"
	// trelloNewStateColor is the color for auto-created states.
	trelloNewStateColor = "#6b7280"
	// trelloNewLabelColor is the fallback color for created labels
	// (unknown/unset Trello colors).
	trelloNewLabelColor = "#6b7280"
)

var (
	// ErrTrelloBadInput is returned for malformed import input: bad
	// api_key, board id, etc. It fails the request (400).
	ErrTrelloBadInput = errors.New("service: invalid trello import input")
	// ErrTrelloNotFound is returned when Trello answers 404: the board
	// id does not exist, or the key/token cannot see it. Trello's own
	// message is surfaced (404).
	ErrTrelloNotFound = errors.New("service: trello board not found")
	// ErrTrelloUnauthorized is returned when Trello answers 401: the
	// api_key/token pair is missing or invalid (401).
	ErrTrelloUnauthorized = errors.New("service: trello authentication failed")
	// ErrTrelloUpstream is returned for any other non-2xx from Trello
	// (5xx, unexpected 3xx, pagination loop). It maps to 502 with
	// Trello's message surfaced.
	ErrTrelloUpstream = errors.New("service: trello api error")
	// errTrelloSSRF is returned when a constructed request URL does not
	// point at https://api.trello.com exactly. Internal: never user-
	// visible on its own (it indicates a code bug, not user error).
	errTrelloSSRF = errors.New("service: trello request target must be https://api.trello.com")
)

// trelloBoardIDRe pins the full board id (24 hex chars — Trello's Mongo
// ObjectId shape).
var trelloBoardIDRe = regexp.MustCompile(`^[0-9a-fA-F]{24}$`)

// trelloShortLinkRe pins the board short link (8 chars — what trello.com
// puts in /b/ URLs).
var trelloShortLinkRe = regexp.MustCompile(`^[A-Za-z0-9]{8}$`)

// trelloLabelHex maps Trello's fixed label palette to hex colors (from
// Trello's documented palette). Unknown colors fall back to the default
// gray — the client renders them blindly as CSS.
var trelloLabelHex = map[string]string{
	"yellow": "#f2d600",
	"orange": "#ff9f1a",
	"red":    "#eb5a46",
	"purple": "#c377e0",
	"blue":   "#0079bf",
	"sky":    "#00b8d9",
	"lime":   "#51e898",
	"pink":   "#ff78cb",
	"black":  "#355263",
}

// TrelloRateLimitError is returned when Trello reports rate-limit
// exhaustion (429). It is NOT retried: the handler maps it to 429 with
// the Retry-After header so the caller decides when to retry.
type TrelloRateLimitError struct {
	RetryAfter int    // seconds until safe to retry (60 when the header is missing/unparseable)
	Message    string // Trello's message, safe to surface
}

func (e *TrelloRateLimitError) Error() string {
	return "service: trello rate limit exceeded: " + e.Message
}

// TrelloImportInput is the JSON body of the trello import endpoints.
// The API key and token travel here only; they are never stored.
type TrelloImportInput struct {
	APIKey  string `json:"api_key"`
	Token   string `json:"token"`
	BoardID string `json:"board_id"`
	Max     int    `json:"max"`
}

// validate checks the input and returns the normalized board identifier
// (the 24-hex id, the 8-char short link, or the short link extracted
// from an https trello.com board URL) and the clamped max. The
// credentials are opaque and never validated here.
func (in TrelloImportInput) validate() (boardID string, max int, err error) {
	if strings.TrimSpace(in.APIKey) == "" {
		return "", 0, fmt.Errorf("%w: api_key is required (trello has no anonymous API access)", ErrTrelloBadInput)
	}
	raw := strings.TrimSpace(in.BoardID)
	switch {
	case trelloBoardIDRe.MatchString(raw):
		boardID = raw
	case trelloShortLinkRe.MatchString(raw):
		boardID = raw
	case strings.Contains(raw, "trello.com/b/"):
		// Board URL form: extract the short link from the first path
		// segment after /b/. The URL itself is pinned to https
		// trello.com — it is parsed only for the segment, never dialed.
		u, perr := url.Parse(raw)
		if perr != nil || u.Scheme != "https" ||
			(u.Hostname() != "trello.com" && u.Hostname() != "www.trello.com") {
			return "", 0, fmt.Errorf("%w: board URL must be an https trello.com /b/ URL", ErrTrelloBadInput)
		}
		segs := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(segs) < 2 || segs[0] != "b" || !trelloShortLinkRe.MatchString(segs[1]) {
			return "", 0, fmt.Errorf("%w: could not extract a board short link from the URL", ErrTrelloBadInput)
		}
		boardID = segs[1]
	default:
		return "", 0, fmt.Errorf("%w: board_id must be the 24-char board id, the 8-char short link, or an https trello.com board URL", ErrTrelloBadInput)
	}
	max = in.Max
	if max <= 0 {
		max = trelloDefaultMax
	}
	if max > trelloAbsoluteMax {
		max = trelloAbsoluteMax
	}
	return boardID, max, nil
}

// ---------- Trello API client (SSRF-safe) ----------

// trelloClient talks to the Trello REST v1 API. The baseURL is
// https://api.trello.com in production; tests inject an httptest URL via
// newTrelloTestClient (the same "tests only" pattern as the
// GitHub/Jira clients). It is NEVER derived from user input — the board
// id becomes a path segment only.
type trelloClient struct {
	http *http.Client
	// apiKey/token are the caller's credentials, used transiently as
	// key+token QUERY PARAMS (Trello's auth contract — they cannot ride
	// a header). They are never logged, never stored, never echoed in
	// errors.
	apiKey string
	token  string
	// baseURL is scheme + host only (no path).
	baseURL string
	// pageSize is the pagination page size (trelloPerPage in production;
	// the test client uses a smaller one so the before-cursor loop is
	// exercised against the stub).
	pageSize int
	// enforceHost validates scheme+host == https://api.trello.com on
	// every request. False only for the test client (whose override URL
	// is injected by test code, never user input).
	enforceHost bool
}

func newTrelloHTTPClient() *http.Client {
	return &http.Client{
		Timeout: trelloHTTPTimeout,
		// Redirects are NEVER followed: the SSRF guard vets the original
		// target, and the credentials must not be bounced at an unvetted
		// URL. A 3xx surfaces as an upstream error.
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
		},
	}
}

// newTrelloClient builds the production client (fixed api.trello.com
// host, host enforcement on).
func newTrelloClient(apiKey, token string) *trelloClient {
	return &trelloClient{
		http:        newTrelloHTTPClient(),
		apiKey:      apiKey,
		token:       token,
		baseURL:     "https://" + trelloAPIHost,
		enforceHost: true,
		pageSize:    trelloPerPage,
	}
}

// newTrelloTestClient builds a client pointed at a stub server. Tests
// only — production code must never call this.
func newTrelloTestClient(apiKey, token, baseURL string) *trelloClient {
	return &trelloClient{
		http:        newTrelloHTTPClient(),
		apiKey:      apiKey,
		token:       token,
		baseURL:     strings.TrimSuffix(baseURL, "/"),
		enforceHost: false,
		// 3 cards per page against the stub: the stub serves its canned
		// cards in pages of 3 via the before cursor, so the pagination
		// loop is exercised with exactly the production code path.
		pageSize: 3,
	}
}

// urlFor builds the full request URL for an API path and validates the
// target. The path is always constructed internally from validated
// segments — this is defense-in-depth against any future refactor that
// might interpolate user input into the URL.
func (c *trelloClient) urlFor(path string) (string, error) {
	u, err := url.Parse(c.baseURL + path)
	if err != nil {
		return "", fmt.Errorf("%w: %v", errTrelloSSRF, err)
	}
	if c.enforceHost && (u.Scheme != "https" || u.Hostname() != trelloAPIHost) {
		return "", fmt.Errorf("%w (got %q)", errTrelloSSRF, u.Hostname())
	}
	return u.String(), nil
}

// trelloAPIError is Trello's occasional JSON error envelope
// ({"error": "..."}); most failures are plain text instead.
type trelloAPIError struct {
	Error string `json:"error"`
}

// get performs one authenticated GET (key+token as query params) and
// returns the capped body. Non-2xx responses become typed errors; the
// credentials never appear in any of them.
func (c *trelloClient) get(ctx context.Context, path string, params url.Values) ([]byte, error) {
	raw, err := c.urlFor(path)
	if err != nil {
		return nil, err
	}
	if params == nil {
		params = url.Values{}
	}
	params.Set("key", c.apiKey)
	if c.token != "" {
		params.Set("token", c.token)
	}
	full := raw + "?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, full, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: request failed: %v", ErrTrelloUpstream, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, trelloMaxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: read body: %v", ErrTrelloUpstream, err)
	}
	if len(body) > trelloMaxResponseBytes {
		return nil, fmt.Errorf("%w: response exceeded size limit", ErrTrelloUpstream)
	}
	if resp.StatusCode == http.StatusOK {
		return body, nil
	}
	// Error path: surface Trello's own message (best-effort decode —
	// JSON envelope or plain text).
	msg := strings.TrimSpace(string(body))
	var apiErr trelloAPIError
	if json.Unmarshal(body, &apiErr) == nil && strings.TrimSpace(apiErr.Error) != "" {
		msg = strings.TrimSpace(apiErr.Error)
	}
	if msg == "" {
		msg = http.StatusText(resp.StatusCode)
	}
	// Truncate absurdly long upstream messages before they reach the
	// caller (never the credentials — they are not in the body).
	if len(msg) > 500 {
		msg = msg[:500] + "…"
	}
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return nil, fmt.Errorf("%w: %s", ErrTrelloUnauthorized, msg)
	case http.StatusNotFound:
		return nil, fmt.Errorf("%w: %s", ErrTrelloNotFound, msg)
	case http.StatusTooManyRequests:
		retryAfter := 60 // fallback when the header is missing
		if h := resp.Header.Get("Retry-After"); h != "" {
			if n, err := strconv.Atoi(strings.TrimSpace(h)); err == nil && n >= 0 {
				retryAfter = n
			}
		}
		return nil, &TrelloRateLimitError{RetryAfter: retryAfter, Message: msg}
	default:
		return nil, fmt.Errorf("%w: %s", ErrTrelloUpstream, msg)
	}
}

// ---------- Trello payload types ----------

type trelloBoard struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

type trelloList struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Closed bool   `json:"closed"`
}

type trelloBadges struct {
	Comments   int `json:"comments"`
	Checklists int `json:"checklists"`
}

type trelloCard struct {
	ID        string       `json:"id"`
	Name      string       `json:"name"`
	Desc      string       `json:"desc"`
	Due       *string      `json:"due"` // null when unset
	IDList    string       `json:"idList"`
	IDLabels  []string     `json:"idLabels"`
	IDMembers []string     `json:"idMembers"`
	ShortLink string       `json:"shortLink"`
	ShortURL  string       `json:"shortUrl"`
	Closed    bool         `json:"closed"`
	Badges    trelloBadges `json:"badges"`
}

type trelloLabel struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

type trelloMember struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	FullName string `json:"fullName"`
	Email    string `json:"email"` // empty when Trello does not expose it
}

type trelloActionMember struct {
	Username string `json:"username"`
	FullName string `json:"fullName"`
}

type trelloActionData struct {
	Text string `json:"text"`
}

type trelloAction struct {
	ID            string             `json:"id"`
	Type          string             `json:"type"`
	Date          string             `json:"date"`
	Data          trelloActionData   `json:"data"`
	MemberCreator trelloActionMember `json:"memberCreator"`
}

// ---------- fetch ----------

// trelloCardFields is the explicit field list for the board cards call:
// exactly what the mapping needs, nothing more.
const trelloCardFields = "name,desc,due,idList,idLabels,idMembers,shortLink,shortUrl,url,closed,badges,pos"

// fetchTrelloBoard fetches the board's name/url — this also validates
// the board id and the credentials early (a 404/401 here is the honest
// "bad board or bad key" signal).
func fetchTrelloBoard(ctx context.Context, c *trelloClient, boardID string) (trelloBoard, error) {
	var b trelloBoard
	body, err := c.get(ctx, "/1/boards/"+url.PathEscape(boardID), url.Values{"fields": {"name,url"}})
	if err != nil {
		return b, err
	}
	if err := json.Unmarshal(body, &b); err != nil {
		return b, fmt.Errorf("%w: decode board: %v", ErrTrelloUpstream, err)
	}
	return b, nil
}

// fetchTrelloLists returns the board's open lists (archived lists are
// excluded server-side; closed is also checked defensively).
func fetchTrelloLists(ctx context.Context, c *trelloClient, boardID string) ([]trelloList, error) {
	body, err := c.get(ctx, "/1/boards/"+url.PathEscape(boardID)+"/lists",
		url.Values{"filter": {"open"}, "fields": {"id,name,closed,pos"}, "limit": {"1000"}})
	if err != nil {
		return nil, err
	}
	var lists []trelloList
	if err := json.Unmarshal(body, &lists); err != nil {
		return nil, fmt.Errorf("%w: decode lists: %v", ErrTrelloUpstream, err)
	}
	out := lists[:0]
	for _, l := range lists {
		if !l.Closed {
			out = append(out, l)
		}
	}
	return out, nil
}

// fetchTrelloBoardLabels returns the board's label set.
func fetchTrelloBoardLabels(ctx context.Context, c *trelloClient, boardID string) ([]trelloLabel, error) {
	body, err := c.get(ctx, "/1/boards/"+url.PathEscape(boardID)+"/labels",
		url.Values{"fields": {"id,name,color"}, "limit": {"1000"}})
	if err != nil {
		return nil, err
	}
	var labels []trelloLabel
	if err := json.Unmarshal(body, &labels); err != nil {
		return nil, fmt.Errorf("%w: decode labels: %v", ErrTrelloUpstream, err)
	}
	return labels, nil
}

// fetchTrelloBoardMembers returns the board's members (username + email
// when Trello exposes it — the assignee match is best-effort).
func fetchTrelloBoardMembers(ctx context.Context, c *trelloClient, boardID string) ([]trelloMember, error) {
	body, err := c.get(ctx, "/1/boards/"+url.PathEscape(boardID)+"/members",
		url.Values{"fields": {"id,username,fullName,email"}, "limit": {"1000"}})
	if err != nil {
		return nil, err
	}
	var members []trelloMember
	if err := json.Unmarshal(body, &members); err != nil {
		return nil, fmt.Errorf("%w: decode members: %v", ErrTrelloUpstream, err)
	}
	return members, nil
}

// fetchTrelloCards pages /1/boards/{id}/cards with the before cursor
// until max live cards are collected or the pages run out. Archived
// cards are counted (archivedSkipped) and excluded — they are fetched
// with filter=all so the count is honest.
func fetchTrelloCards(ctx context.Context, c *trelloClient, boardID string, max int) (cards []trelloCard, archivedSkipped int, err error) {
	var before string
	for page := 0; page < trelloMaxPages; page++ {
		params := url.Values{
			"filter": {"all"},
			"fields": {trelloCardFields},
			"limit":  {strconv.Itoa(c.pageSize)},
		}
		if before != "" {
			params.Set("before", before)
		}
		body, err := c.get(ctx, "/1/boards/"+url.PathEscape(boardID)+"/cards", params)
		if err != nil {
			return nil, 0, err
		}
		var list []trelloCard
		if err := json.Unmarshal(body, &list); err != nil {
			return nil, 0, fmt.Errorf("%w: decode cards: %v", ErrTrelloUpstream, err)
		}
		if len(list) == 0 {
			return cards, archivedSkipped, nil
		}
		for _, card := range list {
			if card.Closed {
				archivedSkipped++
				continue
			}
			cards = append(cards, card)
			if len(cards) >= max {
				return cards, archivedSkipped, nil
			}
		}
		if len(list) < c.pageSize {
			return cards, archivedSkipped, nil
		}
		before = list[len(list)-1].ID
	}
	return nil, 0, fmt.Errorf("%w: pagination exceeded %d pages", ErrTrelloUpstream, trelloMaxPages)
}

// fetchTrelloComments pages a card's comment actions (filter=commentCard),
// capped at trelloMaxCommentsPerCard.
func fetchTrelloComments(ctx context.Context, c *trelloClient, cardID string) ([]trelloAction, error) {
	var out []trelloAction
	var before string
	for {
		params := url.Values{
			"filter": {"commentCard"},
			"limit":  {strconv.Itoa(c.pageSize)},
		}
		if before != "" {
			params.Set("before", before)
		}
		body, err := c.get(ctx, "/1/cards/"+url.PathEscape(cardID)+"/actions", params)
		if err != nil {
			return nil, err
		}
		var page []trelloAction
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("%w: decode comments: %v", ErrTrelloUpstream, err)
		}
		out = append(out, page...)
		if len(out) >= trelloMaxCommentsPerCard {
			return out[:trelloMaxCommentsPerCard], nil
		}
		if len(page) < c.pageSize {
			return out, nil
		}
		before = page[len(page)-1].ID
	}
}

// parseTrelloTime parses Trello's ISO-8601 timestamps
// ("2026-02-01T12:00:00.000Z").
func parseTrelloTime(s string) time.Time {
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

// ---------- dedupe marker ----------

// trelloCardFooter is appended to every imported card's description. It
// is both the user-visible provenance and the dedupe marker.
func trelloCardFooter(name, shortURL string) string {
	return fmt.Sprintf("\n\n---\n*Imported from [%s](%s)*", name, shortURL)
}

// trelloShortURL returns the card's canonical short URL, falling back to
// the reconstructed form when the API omits it.
func trelloShortURL(card trelloCard) string {
	if strings.TrimSpace(card.ShortURL) != "" {
		return strings.TrimSpace(card.ShortURL)
	}
	return "https://trello.com/c/" + strings.TrimSpace(card.ShortLink)
}

// parseImportedTrelloShortLinks scans issue descriptions for dedupe
// markers and returns the set of Trello card short links already
// imported. Single pass — O(total length). The closing paren
// disambiguates abc1 from abc12 (same needle rule as the other
// importers).
func parseImportedTrelloShortLinks(descriptions []string) map[string]bool {
	const prefix = "](https://trello.com/c/"
	out := map[string]bool{}
	isLinkChar := func(c byte) bool {
		return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9'
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
			for j < len(rest) && isLinkChar(rest[j]) {
				j++
			}
			if j > 0 && j < len(rest) && rest[j] == ')' {
				out[rest[:j]] = true
			}
		}
	}
	return out
}

// loadImportedTrelloShortLinks returns the set of card short links
// already imported into this project (dedupe scan).
func loadImportedTrelloShortLinks(ctx context.Context, pool *pgxpool.Pool, projectID string) (map[string]bool, error) {
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
	return parseImportedTrelloShortLinks(descs), nil
}

// ---------- mapping ----------

// normalizeTrelloLabelName trims and truncates to the labels table's
// 120-char contract (same bound CreateLabel enforces). Truncation is by
// rune so multi-byte characters are never split. Empty → "".
func normalizeTrelloLabelName(name string) string {
	name = strings.TrimSpace(name)
	if r := []rune(name); len(r) > 120 {
		name = string(r[:120])
	}
	return name
}

// trelloLabelName resolves a Trello label's glance name: the label's own
// name when set, else the capitalized color ("Sky" for sky) — Trello
// allows nameless color-only labels, and dropping them silently would
// lose data. Empty name AND empty color → "" (skipped).
func trelloLabelName(l trelloLabel) string {
	if nm := normalizeTrelloLabelName(l.Name); nm != "" {
		return nm
	}
	color := strings.ToLower(strings.TrimSpace(l.Color))
	if color == "" {
		return ""
	}
	return strings.ToUpper(color[:1]) + color[1:]
}

// trelloLabelColor maps a Trello palette color to a glance-safe CSS
// color. Unknown/empty colors → the default gray.
func trelloLabelColor(color string) string {
	if hex, ok := trelloLabelHex[strings.ToLower(strings.TrimSpace(color))]; ok {
		return hex
	}
	return trelloNewLabelColor
}

// normalizeTrelloStateName trims and truncates a Trello list name to the
// states table's 120-char contract before creating a state from it.
func normalizeTrelloStateName(name string) string {
	name = strings.TrimSpace(name)
	if r := []rune(name); len(r) > 120 {
		name = string(r[:120])
	}
	return name
}

// trelloMappedCard is one Trello card mapped to glance fields, ready to
// insert (state resolved at insert time — unmatched lists create states
// inside the import transaction).
type trelloMappedCard struct {
	card         trelloCard
	listName     string
	name         string
	description  string // desc + footer marker (ALWAYS present)
	targetDate   *time.Time
	labelNames   []string
	labelIDs     []string
	assigneeID   *string
	comments     []trelloAction
	commentCount int
	checklists   int
}

// trelloImportRun carries per-run state: the client, lookups, and the
// label/state/member resolution maps.
type trelloImportRun struct {
	client    *trelloClient
	boardID   string
	boardName string
	lu        *importLookups
	// listNames maps list id -> list name (open lists only).
	listNames map[string]string
	// labelIDs memoizes lower(label name) -> id (existing + created).
	labelIDs map[string]string
	// labelColors maps lower(label name) -> hex for creation.
	labelColors map[string]string
	// labelsCreated accumulates names of labels created during the run.
	labelsCreated []string
	// stateIDs memoizes lower(state name) -> id (existing + created).
	stateIDs map[string]string
	// stateNames maps state id -> real (case-correct) name, for the
	// preview path.
	stateNames map[string]string
	// statesCreated accumulates list names that became new states.
	statesCreated []string
	// memberEmails maps member id -> lower(email) for assignee matching.
	memberEmails map[string]string
	// memberUsernames maps member id -> username for preview display.
	memberUsernames map[string]string
	// labelTable maps Trello label id -> resolved name+hex (set once per
	// run by setLabelTable after fetching the board labels).
	labelTable map[string]trelloLabelEntry
	// assigneeMisses counts (card, member) pairs with no member match —
	// surfaced in the result, documented, never fatal.
	assigneeMisses int
	// checklistsSkipped accumulates the badge checklist counts of all
	// imported cards (documented skip, see package doc).
	checklistsSkipped int
}

// mapTrelloCard maps one fetched card to glance fields. Assignees are
// resolved best-effort from the pre-fetched board members' emails; each
// miss is counted, never fatal.
func (r *trelloImportRun) mapTrelloCard(card trelloCard) *trelloMappedCard {
	name := strings.TrimSpace(card.Name)
	if name == "" {
		name = strings.TrimSpace(card.ShortLink)
	}
	shortURL := trelloShortURL(card)
	m := &trelloMappedCard{
		card:         card,
		listName:     r.listNames[card.IDList],
		name:         name,
		description:  strings.TrimSpace(card.Desc) + trelloCardFooter(name, shortURL),
		commentCount: card.Badges.Comments,
		checklists:   card.Badges.Checklists,
	}
	if card.Due != nil && strings.TrimSpace(*card.Due) != "" {
		if due := parseTrelloTime(strings.TrimSpace(*card.Due)); !due.IsZero() {
			d := due.UTC()
			m.targetDate = &d
		}
		// Unparseable due → no target date (documented, never fatal).
	}
	for _, lid := range card.IDLabels {
		nm, hex, ok := r.labelForID(lid)
		if !ok || nm == "" {
			continue
		}
		m.labelNames = append(m.labelNames, nm)
		key := strings.ToLower(nm)
		if id, ok := r.labelIDs[key]; ok {
			m.labelIDs = append(m.labelIDs, id)
		} else if _, ok := r.labelColors[key]; !ok {
			r.labelColors[key] = hex
		}
		// else: resolved at insert time (labels are created on import)
	}
	// Every member is evaluated (each miss is documented); the first
	// successful match takes glance's single assignee slot.
	for _, mid := range card.IDMembers {
		email, ok := r.memberEmails[mid]
		if !ok || email == "" {
			r.assigneeMisses++
			continue
		}
		if id, ok := r.lu.members[email]; ok {
			if m.assigneeID == nil {
				id := id
				m.assigneeID = &id
			}
		} else {
			r.assigneeMisses++
		}
	}
	return m
}

// trelloLabelEntry is the resolved board label (name + hex).
type trelloLabelEntry struct {
	name string
	hex  string
}

// labelForID resolves a Trello label id to its glance name + hex color.
func (r *trelloImportRun) labelForID(id string) (name, hex string, ok bool) {
	if lt, ok := r.labelTable[id]; ok {
		return lt.name, lt.hex, true
	}
	return "", "", false
}

// setLabelTable resolves the board's label set into the run's label
// table (Trello label id -> glance name + hex).
func (r *trelloImportRun) setLabelTable(labels []trelloLabel) {
	r.labelTable = map[string]trelloLabelEntry{}
	for _, l := range labels {
		if nm := trelloLabelName(l); nm != "" {
			r.labelTable[l.ID] = trelloLabelEntry{name: nm, hex: trelloLabelColor(l.Color)}
		}
	}
}

// ensureState resolves a Trello list name to a glance state id, creating
// the state (in trelloNewStateGroup) when nothing matches. Creation
// happens inside the import transaction so a failed import never leaves
// orphan states behind.
func (r *trelloImportRun) ensureState(ctx context.Context, tx pgx.Tx, listName string) (id string, err error) {
	nm := normalizeTrelloStateName(listName)
	if nm == "" {
		return "", fmt.Errorf("%w: card is on an unnamed trello list", ErrTrelloBadInput)
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
		r.lu.projectID, nm, trelloNewStateGroup, trelloNewStateColor, seq).Scan(&id); err != nil {
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

// previewStateName resolves the glance state display name for a Trello
// list without writing: matched → the existing state's real name;
// unmatched → the list name flagged new.
func (r *trelloImportRun) previewStateName(listName string) (string, bool) {
	nm := normalizeTrelloStateName(listName)
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

// TrelloImportPreviewRow is one card as the preview endpoint reports it.
type TrelloImportPreviewRow struct {
	ID              string   `json:"id"`
	ShortLink       string   `json:"short_link"`
	ShortURL        string   `json:"short_url"`
	Name            string   `json:"name"`
	ListName        string   `json:"list_name"`
	State           string   `json:"state"` // matched glance state name (or the list name when new)
	StateIsNew      bool     `json:"state_is_new"`
	Labels          []string `json:"labels"`
	NewLabels       []string `json:"new_labels"`
	DueDate         string   `json:"due_date,omitempty"` // YYYY-MM-DD (UTC) when the card has a due date
	Assignees       []string `json:"assignees"`          // Trello usernames
	AssigneeMatched bool     `json:"assignee_matched"`
	Comments        int      `json:"comments"`
	Checklists      int      `json:"checklists"`
	AlreadyImported bool     `json:"already_imported"`
}

// TrelloImportPreview is the preview response: no writes happened.
type TrelloImportPreview struct {
	Source  string                   `json:"source"`
	BoardID string                   `json:"board_id"`
	Total   int                      `json:"total"`
	Rows    []TrelloImportPreviewRow `json:"rows"`
}

// TrelloImportResult is the import outcome. Errors reuse ImportRowError
// with Row carrying the card's ordinal in the fetched batch
// (documented — there are no CSV rows here), so the frontend renders
// all importers identically.
type TrelloImportResult struct {
	Source            string           `json:"source"`
	BoardID           string           `json:"board_id"`
	Created           int              `json:"created"`
	Skipped           int              `json:"skipped"`
	ArchivedSkipped   int              `json:"archived_skipped"`
	Failed            int              `json:"failed"`
	LabelsCreated     []string         `json:"labels_created"`
	StatesCreated     []string         `json:"states_created"`
	AssigneeMisses    int              `json:"assignee_misses"`
	ChecklistsSkipped int              `json:"checklists_skipped"`
	Errors            []ImportRowError `json:"errors"`
}

// newTrelloImportRun validates input, gates membership (member 15+ via
// loadImportLookups — same gate as the CSV/GitHub/Jira importers), and
// seeds the resolution maps. The client is injected so tests can point
// it at a stub. All HTTP happens here — after this returns, the run is
// pure mapping + DB work.
func newTrelloImportRun(ctx context.Context, pool *pgxpool.Pool, client *trelloClient,
	wsSlug, identifier, actorID string, in TrelloImportInput) (*trelloImportRun, string, int, error) {
	boardID, max, err := in.validate()
	if err != nil {
		return nil, "", 0, err
	}
	lu, err := loadImportLookups(ctx, pool, wsSlug, identifier, actorID)
	if err != nil {
		return nil, "", 0, err
	}
	board, err := fetchTrelloBoard(ctx, client, boardID)
	if err != nil {
		return nil, "", 0, err
	}
	r := &trelloImportRun{
		client:          client,
		boardID:         boardID,
		boardName:       strings.TrimSpace(board.Name),
		lu:              lu,
		listNames:       map[string]string{},
		labelIDs:        map[string]string{},
		labelColors:     map[string]string{},
		stateIDs:        map[string]string{},
		stateNames:      map[string]string{},
		memberEmails:    map[string]string{},
		memberUsernames: map[string]string{},
		labelTable:      map[string]trelloLabelEntry{},
	}
	for k, v := range lu.states {
		r.stateIDs[k] = v
	}
	for k, v := range lu.labels {
		r.labelIDs[k] = v
	}
	lists, err := fetchTrelloLists(ctx, client, boardID)
	if err != nil {
		return nil, "", 0, err
	}
	for _, l := range lists {
		r.listNames[l.ID] = l.Name
	}
	labels, err := fetchTrelloBoardLabels(ctx, client, boardID)
	if err != nil {
		return nil, "", 0, err
	}
	r.setLabelTable(labels)
	members, err := fetchTrelloBoardMembers(ctx, client, boardID)
	if err != nil {
		return nil, "", 0, err
	}
	for _, m := range members {
		r.memberEmails[m.ID] = strings.ToLower(strings.TrimSpace(m.Email))
		r.memberUsernames[m.ID] = strings.TrimSpace(m.Username)
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
	return r, boardID, max, nil
}

// previewRow builds the preview row for one mapped card.
func (r *trelloImportRun) previewRow(m *trelloMappedCard, alreadyImported bool) TrelloImportPreviewRow {
	stateName, isNew := r.previewStateName(m.listName)
	row := TrelloImportPreviewRow{
		ID:              m.card.ID,
		ShortLink:       m.card.ShortLink,
		ShortURL:        trelloShortURL(m.card),
		Name:            m.name,
		ListName:        m.listName,
		State:           stateName,
		StateIsNew:      isNew,
		AssigneeMatched: m.assigneeID != nil,
		Comments:        m.commentCount,
		Checklists:      m.checklists,
		AlreadyImported: alreadyImported,
	}
	if m.targetDate != nil {
		row.DueDate = m.targetDate.Format("2006-01-02")
	}
	for _, nm := range m.labelNames {
		row.Labels = append(row.Labels, nm)
		if _, ok := r.labelIDs[strings.ToLower(nm)]; !ok {
			row.NewLabels = append(row.NewLabels, nm)
		}
	}
	seen := map[string]bool{}
	for _, mid := range m.card.IDMembers {
		// Username lookup is best-effort for display only.
		un := r.memberUsernames[mid]
		if un == "" {
			un = mid
		}
		if !seen[un] {
			seen[un] = true
			row.Assignees = append(row.Assignees, un)
		}
	}
	return row
}

// trelloPreviewTimeout bounds the whole preview run: per-request
// timeouts (trelloHTTPTimeout) cap each call, but a large max could
// otherwise stack many slow pages behind one HTTP request.
const trelloPreviewTimeout = 2 * time.Minute

// trelloImportTimeout bounds the whole import run for the same reason
// (card pages + per-card comment pages).
const trelloImportTimeout = 10 * time.Minute

// PreviewTrelloImport fetches the board's cards and reports how the
// first 10 would map. No writes: labels/states are reported as "would
// create", comments are counted (badges) but their bodies are NOT
// fetched (import time only).
func PreviewTrelloImport(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string, in TrelloImportInput) (*TrelloImportPreview, error) {
	ctx, cancel := context.WithTimeout(ctx, trelloPreviewTimeout)
	defer cancel()
	return PreviewTrelloImportWithClient(ctx, pool, newTrelloClient(in.APIKey, in.Token), wsSlug, identifier, actorID, in)
}

// PreviewTrelloImportWithClient is the injectable-client variant (tests).
func PreviewTrelloImportWithClient(ctx context.Context, pool *pgxpool.Pool, client *trelloClient,
	wsSlug, identifier, actorID string, in TrelloImportInput) (*TrelloImportPreview, error) {
	r, boardID, max, err := newTrelloImportRun(ctx, pool, client, wsSlug, identifier, actorID, in)
	if err != nil {
		return nil, err
	}
	cards, _, err := fetchTrelloCards(ctx, r.client, boardID, max)
	if err != nil {
		return nil, err
	}
	imported, err := loadImportedTrelloShortLinks(ctx, pool, r.lu.projectID)
	if err != nil {
		return nil, err
	}
	out := &TrelloImportPreview{
		Source:  r.boardName,
		BoardID: boardID,
		Total:   len(cards),
		Rows:    []TrelloImportPreviewRow{},
	}
	n := len(cards)
	if n > 10 {
		n = 10
	}
	for i := 0; i < n; i++ {
		m := r.mapTrelloCard(cards[i])
		out.Rows = append(out.Rows, r.previewRow(m, imported[cards[i].ShortLink]))
	}
	return out, nil
}

// ImportTrelloCards fetches the board's cards, then inserts the not-yet-
// imported ones in ONE transaction (same two-phase shape as the other
// importers: per-card errors collected, never aborting the batch).
// Member (15)+.
func ImportTrelloCards(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID string, in TrelloImportInput) (*TrelloImportResult, error) {
	ctx, cancel := context.WithTimeout(ctx, trelloImportTimeout)
	defer cancel()
	return ImportTrelloCardsWithClient(ctx, pool, newTrelloClient(in.APIKey, in.Token), wsSlug, identifier, actorID, in)
}

// ImportTrelloCardsWithClient is the injectable-client variant (tests).
func ImportTrelloCardsWithClient(ctx context.Context, pool *pgxpool.Pool, client *trelloClient,
	wsSlug, identifier, actorID string, in TrelloImportInput) (*TrelloImportResult, error) {
	r, boardID, max, err := newTrelloImportRun(ctx, pool, client, wsSlug, identifier, actorID, in)
	if err != nil {
		return nil, err
	}
	cards, archivedSkipped, err := fetchTrelloCards(ctx, r.client, boardID, max)
	if err != nil {
		return nil, err
	}
	imported, err := loadImportedTrelloShortLinks(ctx, pool, r.lu.projectID)
	if err != nil {
		return nil, err
	}

	res := &TrelloImportResult{
		Source:          r.boardName,
		BoardID:         boardID,
		ArchivedSkipped: archivedSkipped,
		Errors:          []ImportRowError{},
		LabelsCreated:   []string{},
		StatesCreated:   []string{},
	}

	// Phase 1: map + fetch comments (all HTTP happens BEFORE the tx — a
	// slow comment page must never hold a pool connection).
	var valid []*trelloMappedCard
	for ord, card := range cards {
		if imported[card.ShortLink] {
			res.Skipped++
			continue
		}
		m := r.mapTrelloCard(card)
		if strings.TrimSpace(m.name) == "" {
			res.Failed++
			res.Errors = append(res.Errors, ImportRowError{Row: ord + 1, Message: "card has no name"})
			continue
		}
		if strings.TrimSpace(m.listName) == "" {
			// A card whose list vanished mid-fetch cannot map to a
			// state (and must not abort the batch — same per-card
			// philosophy as the other importers).
			res.Failed++
			res.Errors = append(res.Errors, ImportRowError{Row: ord + 1, Message: "card's trello list is not on the board"})
			continue
		}
		if m.commentCount > 0 {
			comments, err := fetchTrelloComments(ctx, r.client, card.ID)
			if err != nil {
				// Comments are enrichment, not the card itself:
				// record and continue with what we have.
				res.Errors = append(res.Errors, ImportRowError{
					Row:     ord + 1,
					Message: fmt.Sprintf("comments not imported: %v", err),
				})
			} else {
				m.comments = comments
			}
		}
		r.checklistsSkipped += m.checklists
		valid = append(valid, m)
	}

	if len(valid) == 0 {
		res.AssigneeMisses = r.assigneeMisses
		res.ChecklistsSkipped = r.checklistsSkipped
		return res, nil
	}

	// Phase 2: insert everything atomically.
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// Resolve states first (unmatched lists create states in the
	// unstarted group, inside this tx).
	for _, m := range valid {
		if _, err := r.ensureState(ctx, tx, m.listName); err != nil {
			return nil, err
		}
	}

	// Collect every label name across the batch, ensure them once.
	// Trello labels carry their palette color (unknown → default gray).
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
		color := r.labelColors[lkey]
		if color == "" {
			color = trelloNewLabelColor
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
		r.labelIDs[lkey] = id
		r.labelsCreated = append(r.labelsCreated, nm)
	}
	// Fill in label ids on the mapped cards now that all exist.
	for _, m := range valid {
		m.labelIDs = m.labelIDs[:0]
		for _, nm := range m.labelNames {
			if id, ok := r.labelIDs[strings.ToLower(nm)]; ok {
				m.labelIDs = append(m.labelIDs, id)
			}
		}
	}

	for _, m := range valid {
		stateID := r.stateIDs[strings.ToLower(normalizeTrelloStateName(m.listName))]
		var seq int
		if err := tx.QueryRow(ctx,
			`INSERT INTO issue_sequences (project_id, last_value) VALUES ($1::uuid, 1)
			 ON CONFLICT (project_id) DO UPDATE SET last_value = issue_sequences.last_value + 1
			 RETURNING last_value`,
			r.lu.projectID).Scan(&seq); err != nil {
			return nil, fmt.Errorf("service: increment issue sequence: %w", err)
		}
		var issueID string
		var targetDate any
		if m.targetDate != nil {
			targetDate = m.targetDate.Format("2006-01-02")
		}
		err = tx.QueryRow(ctx,
			`INSERT INTO issues (project_id, sequence_id, name, description, priority,
				state_id, target_date, created_by)
			 VALUES ($1::uuid, $2, $3, $4::jsonb, 0, $5::uuid, $6, $7::uuid)
			 RETURNING id::text`,
			r.lu.projectID, seq, m.name, strconv.Quote(m.description),
			stateID, targetDate, actorID).Scan(&issueID)
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
		for _, tc := range m.comments {
			cbody := strings.TrimSpace(tc.Data.Text)
			if cbody == "" {
				continue
			}
			who := strings.TrimSpace(tc.MemberCreator.FullName)
			if who == "" {
				who = strings.TrimSpace(tc.MemberCreator.Username)
			}
			if who == "" {
				who = "unknown Trello user"
			}
			when := parseTrelloTime(tc.Date)
			whenStr := tc.Date
			if when.IsZero() {
				// Unparseable timestamp: fall back to now rather than
				// storing the zero time (Trello's format is stable, so
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
		newVal := fmt.Sprintf(`{"sequence_id":%d,"name":%s,"trello_card":%s}`,
			seq, strconv.Quote(m.name), strconv.Quote(m.card.ShortLink))
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
	res.ChecklistsSkipped = r.checklistsSkipped
	res.LabelsCreated = r.labelsCreated
	res.StatesCreated = r.statesCreated
	return res, nil
}
