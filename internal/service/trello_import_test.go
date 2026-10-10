package service

// Trello Cloud importer tests (C10T0). A stub httptest server emulates
// the Trello REST v1 API: GET /1/boards/{id} (board info), lists,
// labels, members, cards (before-cursor pagination), and per-card
// comment actions. All tests run against the real test database — no
// skips.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ---------- stub server ----------

// trelloStub emulates the Trello REST v1 API. Handlers can be overridden
// per test for error-mapping cases.
type trelloStub struct {
	mu sync.Mutex
	// seenQuery records the raw query of every request so tests can pin
	// that key+token travel as query params (Trello's auth contract).
	seenQuery []string
	seenHosts []string

	boardHandler   func(w http.ResponseWriter, r *http.Request)
	listsHandler   func(w http.ResponseWriter, r *http.Request)
	labelsHandler  func(w http.ResponseWriter, r *http.Request)
	membersHandler func(w http.ResponseWriter, r *http.Request)
	cardsHandler   func(w http.ResponseWriter, r *http.Request)
	actionsHandler func(w http.ResponseWriter, r *http.Request)
	// checklistsHandler overrides the default /1/cards/{id}/checklists
	// handler (used for failure-injection cases).
	checklistsHandler func(w http.ResponseWriter, r *http.Request)
}

func (s *trelloStub) record(r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seenQuery = append(s.seenQuery, r.URL.RawQuery)
	s.seenHosts = append(s.seenHosts, r.Host)
}

// authOK pins the credential transport: Trello's API takes key+token as
// query parameters. The key/token must never appear in the path or in a
// header value.
func (s *trelloStub) authOK(t *testing.T, apiKey, token string) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.seenQuery) == 0 {
		t.Fatal("no requests recorded")
	}
	for _, q := range s.seenQuery {
		v, err := url.ParseQuery(q)
		if err != nil {
			t.Fatalf("unparseable query %q: %v", q, err)
		}
		if v.Get("key") != apiKey || v.Get("token") != token {
			t.Errorf("query %q missing key+token auth", q)
		}
	}
}

// trelloCardJSON builds one board-cards payload for the stub.
func trelloCardJSON(id, name, desc, due, listID string, labelIDs, memberIDs []string, shortLink string, closed bool, commentCount, checklistCount int) string {
	c := map[string]any{
		"id":        id,
		"name":      name,
		"desc":      desc,
		"due":       nil,
		"idList":    listID,
		"idLabels":  labelIDs,
		"idMembers": memberIDs,
		"shortLink": shortLink,
		"shortUrl":  "https://trello.com/c/" + shortLink,
		"url":       "https://trello.com/c/" + shortLink + "/99-card",
		"closed":    closed,
		"pos":       100,
		"badges":    map[string]any{"comments": commentCount, "checklists": checklistCount},
	}
	if due != "" {
		c["due"] = due
	}
	out, _ := json.Marshal(c)
	return string(out)
}

// trelloStubCards returns the canned cards: 4 live + 1 archived, served
// in two pages via the before cursor (pages of 3, then 2).
func trelloStubCards() []string {
	return []string{
		trelloCardJSON("card1", "Ship v2", "the body", "2026-02-01T12:00:00.000Z", "list1",
			[]string{"label1"}, []string{"member1"}, "abc12345", false, 2, 1),
		trelloCardJSON("card2", "Refactor", "", "", "list2",
			nil, []string{"member2"}, "def67890", false, 0, 0),
		trelloCardJSON("card3", "Docs", "", "", "list3",
			[]string{"label2"}, nil, "ghi11111", false, 1, 2),
		// Archived cards are excluded from the import (counted, never
		// imported).
		trelloCardJSON("card4", "Archived idea", "", "", "list1",
			nil, nil, "jkl22222", true, 0, 0),
		trelloCardJSON("card5", "Fifth", "trailing", "", "list1",
			nil, nil, "mno33333", false, 0, 0),
	}
}

// trelloStubCardsHandler serves cards with before-cursor pagination like
// the real API: ?before=<last id of previous page>. filter=all is
// expected so archived cards are visible to the client (which filters
// them itself, honestly counting them).
func trelloStubCardsHandler(s *trelloStub, cards []string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		if got := r.URL.Query().Get("filter"); got != "all" {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, `{"error":"expected filter=all, got %s"}`, got)
			return
		}
		before := r.URL.Query().Get("before")
		var page []string
		if before == "" {
			page = cards[:3]
		} else if before == "card3" {
			page = cards[3:]
		} else {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, `{"error":"bad before cursor %s"}`, before)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, "[%s]", strings.Join(page, ","))
	}
}

// trelloStubActionsHandler serves comment actions for a card. The stub
// returns them all on one page (the client still honors the cap).
func trelloStubActionsHandler(s *trelloStub, byCard map[string][]map[string]any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		if got := r.URL.Query().Get("filter"); got != "commentCard" {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, `{"error":"expected filter=commentCard, got %s"}`, got)
			return
		}
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		var cardID string
		for i, p := range parts {
			if p == "cards" && i+1 < len(parts) {
				cardID = parts[i+1]
			}
		}
		acts := byCard[cardID]
		if acts == nil {
			acts = []map[string]any{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(acts)
	}
}

// trelloStubChecklistsHandler serves a card's checklists: the real
// /1/cards/{id}/checklists endpoint returns the full array (no
// before-cursor pagination), so the stub does the same. It expects
// checkItems=all like the production client sends.
func trelloStubChecklistsHandler(s *trelloStub, byCard map[string][]map[string]any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.record(r)
		if got := r.URL.Query().Get("checkItems"); got != "all" {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, `{"error":"expected checkItems=all, got %s"}`, got)
			return
		}
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		var cardID string
		for i, p := range parts {
			if p == "cards" && i+1 < len(parts) {
				cardID = parts[i+1]
			}
		}
		lists := byCard[cardID]
		if lists == nil {
			lists = []map[string]any{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(lists)
	}
}

// trelloChecklistJSON builds one canned checklist payload.
func trelloChecklistJSON(id, name string, items [][2]string) map[string]any {
	its := []map[string]any{}
	for _, it := range items {
		its = append(its, map[string]any{"id": "ci-" + it[0], "name": it[0], "state": it[1]})
	}
	return map[string]any{"id": id, "name": name, "checkItems": its}
}

func trelloCommentAction(id, who, text, date string) map[string]any {
	return map[string]any{
		"id":   id,
		"type": "commentCard",
		"date": date,
		"data": map[string]any{
			"text": text,
			"card": map[string]any{"id": "card1"},
		},
		"memberCreator": map[string]any{
			"id":       "memberX",
			"username": who,
			"fullName": who + " Full",
		},
	}
}

// newTrelloStub builds the stub server with the canned board ("Roadmap"
// board id 5abbe4b7ddc1b351ef961414): three lists (one case-variant
// match, one exact match, one unmatched), two labels (one named+colored,
// one color-only), two members (one email match, one without email).
func newTrelloStub(t *testing.T, memberEmail string) (*trelloStub, *httptest.Server) {
	t.Helper()
	s := &trelloStub{}
	cards := trelloStubCards()
	actions := map[string][]map[string]any{
		"card1": {
			trelloCommentAction("act1", "ada", "first comment", "2026-01-03T10:00:00.000Z"),
			trelloCommentAction("act2", "bob", "second comment", "2026-01-04T10:00:00.000Z"),
		},
		"card3": {
			trelloCommentAction("act3", "cara", "docs note", "2026-01-05T10:00:00.000Z"),
		},
	}
	// Checklist fixtures (C11T3): card1 carries one mixed checklist;
	// card3 carries two — one with items and one empty (the empty one
	// must not produce a "##" section). Badges say card1=1, card3=2.
	checklists := map[string][]map[string]any{
		"card1": {
			trelloChecklistJSON("cl1", "Launch tasks", [][2]string{
				{"Write the announcement", "complete"},
				{"Ship the binary", "incomplete"},
			}),
		},
		"card3": {
			trelloChecklistJSON("cl2", "Review pass", [][2]string{
				{"Spellcheck", "incomplete"},
			}),
			trelloChecklistJSON("cl3", "Nothing yet", nil),
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/1/boards/5abbe4b7ddc1b351ef961414", func(w http.ResponseWriter, r *http.Request) {
		if s.boardHandler != nil {
			s.boardHandler(w, r)
			return
		}
		s.record(r)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"5abbe4b7ddc1b351ef961414","name":"Roadmap","url":"https://trello.com/b/abc12345/roadmap"}`)
	})
	mux.HandleFunc("/1/boards/5abbe4b7ddc1b351ef961414/lists", func(w http.ResponseWriter, r *http.Request) {
		if s.listsHandler != nil {
			s.listsHandler(w, r)
			return
		}
		s.record(r)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[
			{"id":"list1","name":"todo","closed":false,"pos":1},
			{"id":"list2","name":"In Progress","closed":false,"pos":2},
			{"id":"list3","name":"QA Review","closed":false,"pos":3}
		]`)
	})
	mux.HandleFunc("/1/boards/5abbe4b7ddc1b351ef961414/labels", func(w http.ResponseWriter, r *http.Request) {
		if s.labelsHandler != nil {
			s.labelsHandler(w, r)
			return
		}
		s.record(r)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[
			{"id":"label1","idBoard":"5abbe4b7ddc1b351ef961414","name":"bug","color":"red"},
			{"id":"label2","idBoard":"5abbe4b7ddc1b351ef961414","name":"","color":"sky"}
		]`)
	})
	mux.HandleFunc("/1/boards/5abbe4b7ddc1b351ef961414/members", func(w http.ResponseWriter, r *http.Request) {
		if s.membersHandler != nil {
			s.membersHandler(w, r)
			return
		}
		s.record(r)
		w.Header().Set("Content-Type", "application/json")
		members := []map[string]any{
			{"id": "member1", "username": "ada", "fullName": "Ada Member", "email": memberEmail},
			{"id": "member2", "username": "ghost", "fullName": "Ghost User", "email": ""},
		}
		_ = json.NewEncoder(w).Encode(members)
	})
	mux.HandleFunc("/1/boards/5abbe4b7ddc1b351ef961414/cards", func(w http.ResponseWriter, r *http.Request) {
		if s.cardsHandler != nil {
			s.cardsHandler(w, r)
			return
		}
		trelloStubCardsHandler(s, cards)(w, r)
	})
	mux.HandleFunc("/1/cards/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/checklists") {
			if s.checklistsHandler != nil {
				s.checklistsHandler(w, r)
				return
			}
			trelloStubChecklistsHandler(s, checklists)(w, r)
			return
		}
		if s.actionsHandler != nil {
			s.actionsHandler(w, r)
			return
		}
		trelloStubActionsHandler(s, actions)(w, r)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return s, srv
}

// trelloImportFixture mirrors the GitHub/Jira fixture shape: actor
// (member) + second member whose email the stub's assignee matches.
func trelloImportFixture(t *testing.T, pool *pgxpool.Pool) (actorID, memberEmail, wsSlug, ident, projectID string) {
	t.Helper()
	ctx := context.Background()
	actorID = createTestUser(t, pool, uniqueTestEmail("trello-import-actor"))
	memberEmail = uniqueTestEmail("trello-member")
	memberID := createTestUser(t, pool, memberEmail)
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-trello"), actorID)
	if _, err := pool.Exec(ctx,
		`INSERT INTO workspace_members (workspace_id, user_id, role)
		 VALUES ($1::uuid, $2::uuid, $3) ON CONFLICT DO NOTHING`,
		ws.ID, memberID, RoleMember); err != nil {
		t.Fatalf("add member: %v", err)
	}
	p := createTestProject(t, pool, ws.Slug, actorID, "Widgets", uniqueTestIdentifier())
	return actorID, memberEmail, ws.Slug, p.Identifier, p.ID
}

func trelloTestInput() TrelloImportInput {
	return TrelloImportInput{
		APIKey:  "apikeyDONOTSTORE_1234567890abcdef",
		Token:   "tokendonotstore_0987654321fedcba",
		BoardID: "5abbe4b7ddc1b351ef961414",
		Max:     100,
	}
}

func trelloTestClient(t *testing.T, s *trelloStub, srv *httptest.Server, in TrelloImportInput) *trelloClient {
	t.Helper()
	return newTrelloTestClient(in.APIKey, in.Token, srv.URL)
}

// ---------- validation ----------

// TestTrelloImportInputValidate pins the input contract: board id
// accepts the 24-hex id, the 8-char short link, or an https trello.com
// board URL (from which the short link is extracted); anything else is
// rejected. Max is clamped like the other importers.
func TestTrelloImportInputValidate(t *testing.T) {
	in := trelloTestInput()
	if _, _, err := in.validate(); err != nil {
		t.Fatalf("valid input rejected: %v", err)
	}
	// Short link form.
	short := trelloTestInput()
	short.BoardID = "AbC123xY"
	boardID, _, err := short.validate()
	if err != nil || boardID != "AbC123xY" {
		t.Errorf("short link → (%q,%v), want (AbC123xY,nil)", boardID, err)
	}
	// Board URL form: short link is extracted from the https trello.com URL.
	urlForm := trelloTestInput()
	urlForm.BoardID = "https://trello.com/b/AbC123xY/roadmap-board"
	boardID, _, err = urlForm.validate()
	if err != nil || boardID != "AbC123xY" {
		t.Errorf("board URL → (%q,%v), want (AbC123xY,nil)", boardID, err)
	}
	bad := []TrelloImportInput{
		{APIKey: "", Token: "t", BoardID: "5abbe4b7ddc1b351ef961414"},         // missing api key
		{APIKey: "k", Token: "t", BoardID: ""},                                // missing board
		{APIKey: "k", Token: "t", BoardID: "AbC123x"},                         // 7-char: not id, not short link
		{APIKey: "k", Token: "t", BoardID: "5abbe4b7ddc1b351ef96141400"},      // 26 hex
		{APIKey: "k", Token: "t", BoardID: "5abbe4b7ddc1b351ef96141g"},        // non-hex 24-char
		{APIKey: "k", Token: "t", BoardID: "AbC 123xY"},                       // space in short link
		{APIKey: "k", Token: "t", BoardID: "../x"},                            // path traversal attempt
		{APIKey: "k", Token: "t", BoardID: "http://trello.com/b/AbC123xY/x"},  // plain http URL
		{APIKey: "k", Token: "t", BoardID: "https://evil.com/b/AbC123xY/x"},   // wrong host URL
		{APIKey: "k", Token: "t", BoardID: "https://trello.com/c/AbC123xY/x"}, // card URL, not board
	}
	for i, b := range bad {
		if _, _, err := b.validate(); !errors.Is(err, ErrTrelloBadInput) {
			t.Errorf("bad input %d: err = %v, want ErrTrelloBadInput", i, err)
		}
	}
	// Token may be empty (public boards need only the API key); a 24-char
	// uppercase hex id is accepted (hex is case-insensitive).
	anon := trelloTestInput()
	anon.Token = ""
	anon.BoardID = "5ABBE4B7DDC1B351EF961414"
	if boardID, _, err := anon.validate(); err != nil || boardID != "5ABBE4B7DDC1B351EF961414" {
		t.Errorf("anonymous 24-hex → (%q,%v)", boardID, err)
	}
	// Max clamping mirrors the GitHub/Jira importers (default 100, abs 1000).
	m := trelloTestInput()
	m.Max = 0
	if _, max, err := m.validate(); err != nil || max != 100 {
		t.Errorf("max=0 → (%d,%v), want (100,nil)", max, err)
	}
	m.Max = 5000
	if _, max, err := m.validate(); err != nil || max != 1000 {
		t.Errorf("max=5000 → (%d,%v), want (1000,nil)", max, err)
	}
}

// ---------- SSRF guard ----------

// TestTrelloSSRFGuard pins the fixed-host rule: the production client
// only ever builds https://api.trello.com/1/... URLs. A tampered base,
// plain HTTP, or any non-api.trello.com host is rejected. There is no
// user-controlled host anywhere: the board id is a charset-pinned path
// segment only.
func TestTrelloSSRFGuard(t *testing.T) {
	c := newTrelloClient("k", "t")
	raw, err := c.urlFor("/1/boards/abc123")
	if err != nil {
		t.Fatalf("urlFor: %v", err)
	}
	if raw != "https://api.trello.com/1/boards/abc123" {
		t.Errorf("urlFor = %q, want the pinned api.trello.com URL", raw)
	}
	hostile := &trelloClient{http: newTrelloHTTPClient(), baseURL: "https://evil.example", enforceHost: true}
	if _, err := hostile.urlFor("/1/boards/abc123"); !errors.Is(err, errTrelloSSRF) {
		t.Errorf("hostile urlFor err = %v, want errTrelloSSRF", err)
	}
	lookalike := &trelloClient{http: newTrelloHTTPClient(), baseURL: "https://api.trello.com.evil.example", enforceHost: true}
	if _, err := lookalike.urlFor("/1/boards/abc123"); !errors.Is(err, errTrelloSSRF) {
		t.Errorf("lookalike urlFor err = %v, want errTrelloSSRF", err)
	}
	plainHTTP := &trelloClient{http: newTrelloHTTPClient(), baseURL: "http://api.trello.com", enforceHost: true}
	if _, err := plainHTTP.urlFor("/1/boards/abc123"); !errors.Is(err, errTrelloSSRF) {
		t.Errorf("http urlFor err = %v, want errTrelloSSRF", err)
	}
	// The checklist fetch path goes through the same urlFor guard: a
	// hostile base must reject it too (C11T3 — the new per-card HTTP
	// call is covered by the same fixed-host rule).
	for _, hostileClient := range []*trelloClient{hostile, lookalike, plainHTTP} {
		if _, err := hostileClient.urlFor("/1/cards/card1/checklists"); !errors.Is(err, errTrelloSSRF) {
			t.Errorf("checklist path on hostile base: err = %v, want errTrelloSSRF", err)
		}
	}
}

// ---------- checklist → markdown (C11T3) ----------

// TestTrelloChecklistMarkdown pins the checklist rendering contract:
// one "## <name>" section per checklist (in Trello order), items as
// "- [x]"/"- [ ]" from the checkItem state, empty checklists skipped
// (documented choice — an empty "##" section is noise), unnamed
// checklists falling back to "Checklist", and no output for no lists.
func TestTrelloChecklistMarkdown(t *testing.T) {
	mk := func(name string, items ...trelloCheckItem) trelloChecklist {
		return trelloChecklist{ID: "cl", Name: name, Items: items}
	}
	got := trelloChecklistMarkdown([]trelloChecklist{
		mk("Launch tasks",
			trelloCheckItem{ID: "i1", Name: "Write the announcement", State: "complete"},
			trelloCheckItem{ID: "i2", Name: "Ship the binary", State: "incomplete"},
		),
		mk("Review pass",
			trelloCheckItem{ID: "i3", Name: "Spellcheck", State: "incomplete"},
		),
	})
	want := "\n\n## Launch tasks\n" +
		"- [x] Write the announcement\n" +
		"- [ ] Ship the binary\n" +
		"\n## Review pass\n" +
		"- [ ] Spellcheck"
	if got != want {
		t.Errorf("markdown =\n%q\nwant\n%q", got, want)
	}
	// Empty checklist: skipped entirely (no "##" section).
	if got := trelloChecklistMarkdown([]trelloChecklist{mk("Nothing yet")}); got != "" {
		t.Errorf("empty checklist → %q, want no section", got)
	}
	// Unnamed checklist: falls back to "Checklist".
	got = trelloChecklistMarkdown([]trelloChecklist{
		mk("", trelloCheckItem{ID: "i1", Name: "a task", State: "complete"}),
	})
	if got != "\n\n## Checklist\n- [x] a task" {
		t.Errorf("unnamed checklist → %q", got)
	}
	// No lists at all: no output.
	if got := trelloChecklistMarkdown(nil); got != "" {
		t.Errorf("nil lists → %q, want empty", got)
	}
}

// ---------- preview ----------

// TestPreviewTrelloImport exercises the preview: two card pages via the
// before cursor, list→state matching (case-insensitive), unmatched list
// flagged as new, named + color-only labels, comment/checklist badge
// counts without fetching comment bodies, and no writes anywhere.
func TestPreviewTrelloImport(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	actorID, memberEmail, wsSlug, ident, projectID := trelloImportFixture(t, pool)

	stub, srv := newTrelloStub(t, memberEmail)
	in := trelloTestInput()
	client := trelloTestClient(t, stub, srv, in)

	prev, err := PreviewTrelloImportWithClient(ctx, pool, client, wsSlug, ident, actorID, in)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if prev.Source != "Roadmap" {
		t.Errorf("source = %q, want Roadmap", prev.Source)
	}
	if prev.BoardID != "5abbe4b7ddc1b351ef961414" {
		t.Errorf("board_id = %q", prev.BoardID)
	}
	// 4 live cards (the archived card is excluded from preview too).
	if prev.Total != 4 {
		t.Errorf("total = %d, want 4 (two pages, archived excluded)", prev.Total)
	}
	if len(prev.Rows) != 4 {
		t.Fatalf("rows = %d, want 4", len(prev.Rows))
	}
	byLink := map[string]TrelloImportPreviewRow{}
	for _, r := range prev.Rows {
		byLink[r.ShortLink] = r
	}
	// "todo" (lowercase) matches the seeded "Todo" state case-insensitively.
	if r := byLink["abc12345"]; r.StateIsNew || r.State != "Todo" {
		t.Errorf("card1: state = %q is_new = %v, want Todo/false", r.State, r.StateIsNew)
	}
	// "In Progress" matches exactly.
	if r := byLink["def67890"]; r.StateIsNew || r.State != "In Progress" {
		t.Errorf("card2: state = %q is_new = %v, want In Progress/false", r.State, r.StateIsNew)
	}
	// Unmatched "QA Review" is flagged new.
	if r := byLink["ghi11111"]; !r.StateIsNew || r.State != "QA Review" {
		t.Errorf("card3: state = %q is_new = %v, want QA Review/true", r.State, r.StateIsNew)
	}
	// Labels: "bug" is new; the color-only label surfaces as "Sky".
	if r := byLink["abc12345"]; len(r.Labels) != 1 || r.Labels[0] != "bug" || len(r.NewLabels) != 1 {
		t.Errorf("card1 labels = %v new = %v, want [bug]/[bug]", r.Labels, r.NewLabels)
	}
	if r := byLink["ghi11111"]; len(r.Labels) != 1 || r.Labels[0] != "Sky" || len(r.NewLabels) != 1 {
		t.Errorf("card3 labels = %v new = %v, want [Sky]/[Sky]", r.Labels, r.NewLabels)
	}
	// Due date is surfaced; comments/checklists are badge counts (bodies
	// NOT fetched at preview time).
	if r := byLink["abc12345"]; r.DueDate != "2026-02-01" {
		t.Errorf("card1 due = %q, want 2026-02-01", r.DueDate)
	}
	if r := byLink["abc12345"]; r.Comments != 2 || r.Checklists != 1 {
		t.Errorf("card1 comments/checklists = %d/%d, want 2/1", r.Comments, r.Checklists)
	}
	// Assignee matched for card1 (member email), missed for card2 (no email).
	if r := byLink["abc12345"]; !r.AssigneeMatched {
		t.Errorf("card1: assignee_matched = false, want true")
	}
	if r := byLink["def67890"]; r.AssigneeMatched {
		t.Errorf("card2: assignee_matched = true, want false")
	}
	// Preview writes nothing: no issues, no states, no labels — scoped to
	// this test's project (the DB is never truncated).
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM issues WHERE project_id = $1::uuid`, projectID).Scan(&n); err != nil || n != 0 {
		t.Errorf("preview wrote %d issues", n)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM states WHERE project_id = $1::uuid AND name = 'QA Review'`, projectID).Scan(&n); err != nil || n != 0 {
		t.Errorf("preview created %d states", n)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM labels WHERE workspace_id = (SELECT workspace_id FROM projects WHERE id = $1::uuid) AND name IN ('bug','Sky')`, projectID).Scan(&n); err != nil || n != 0 {
		t.Errorf("preview created %d labels", n)
	}

	stub.authOK(t, in.APIKey, in.Token)
}

// ---------- end-to-end import ----------

// TestImportTrelloCardsEndToEnd is the full pipeline: before-cursor
// pagination, markdown description, footer-marker dedupe, comment import
// (attributed, original timestamps), label creation with Trello hex
// colors, due→target_date, assignee match + miss, unmatched-list state
// creation in the unstarted group, archived-card exclusion, checklist
// import as markdown sections (C11T3), and rerun dedupe.
func TestImportTrelloCardsEndToEnd(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	actorID, memberEmail, wsSlug, ident, projectID := trelloImportFixture(t, pool)

	stub, srv := newTrelloStub(t, memberEmail)
	in := trelloTestInput()
	client := trelloTestClient(t, stub, srv, in)

	res, err := ImportTrelloCardsWithClient(ctx, pool, client, wsSlug, ident, actorID, in)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.Source != "Roadmap" {
		t.Errorf("source = %q, want Roadmap", res.Source)
	}
	if res.Created != 4 {
		t.Errorf("created = %d, want 4", res.Created)
	}
	if res.Skipped != 0 {
		t.Errorf("skipped = %d, want 0", res.Skipped)
	}
	if res.ArchivedSkipped != 1 {
		t.Errorf("archived_skipped = %d, want 1", res.ArchivedSkipped)
	}
	if len(res.LabelsCreated) != 2 { // bug, Sky
		t.Errorf("labels_created = %v, want 2", res.LabelsCreated)
	}
	if len(res.StatesCreated) != 1 { // "QA Review"
		t.Errorf("states_created = %v, want 1 (QA Review)", res.StatesCreated)
	}
	if res.AssigneeMisses != 1 { // ghost has no email
		t.Errorf("assignee_misses = %d, want 1", res.AssigneeMisses)
	}
	if res.ChecklistsSkipped != 0 {
		// C11T3: checklists are no longer skipped — they are appended
		// to the description as markdown. The field is retained for
		// API compatibility with the frontend import-result stat.
		t.Errorf("checklists_skipped = %d, want 0", res.ChecklistsSkipped)
	}

	// The new state lands in the unstarted group (glance has no "Default"
	// group; unstarted is the neutral tracked group — documented in the
	// importer package doc).
	var group string
	if err := pool.QueryRow(ctx,
		`SELECT "group" FROM states WHERE project_id = $1::uuid AND name = 'QA Review'`, projectID).Scan(&group); err != nil {
		t.Fatalf("QA Review state: %v", err)
	} else if group != "unstarted" {
		t.Errorf("QA Review group = %q, want unstarted", group)
	}

	// The color-only label is created with Trello's sky hex, not the
	// default gray.
	var color string
	if err := pool.QueryRow(ctx,
		`SELECT color FROM labels WHERE workspace_id = (SELECT workspace_id FROM projects WHERE id = $1::uuid) AND name = 'Sky'`, projectID).Scan(&color); err != nil {
		t.Fatalf("Sky label: %v", err)
	} else if strings.ToLower(color) != "#00b8d9" {
		t.Errorf("Sky label color = %q, want #00b8d9", color)
	}
	if err := pool.QueryRow(ctx,
		`SELECT color FROM labels WHERE workspace_id = (SELECT workspace_id FROM projects WHERE id = $1::uuid) AND name = 'bug'`, projectID).Scan(&color); err != nil {
		t.Fatalf("bug label: %v", err)
	} else if strings.ToLower(color) != "#eb5a46" {
		t.Errorf("bug label color = %q, want #eb5a46", color)
	}

	// card1: description is the markdown desc + footer marker; target_date
	// is the due date (date part, UTC); assignee matched the member.
	var desc, stateName string
	var targetDate *string
	var assigneeID *string
	if err := pool.QueryRow(ctx, `
		SELECT i.description::text, s.name, i.target_date::text, a.user_id::text
		FROM issues i JOIN states s ON s.id = i.state_id
		LEFT JOIN issue_assignees a ON a.issue_id = i.id
		WHERE i.project_id = $1::uuid AND i.name = 'Ship v2'`, projectID).Scan(&desc, &stateName, &targetDate, &assigneeID); err != nil {
		t.Fatalf("card1 row: %v", err)
	}
	if !strings.Contains(desc, "the body") {
		t.Errorf("card1 description missing markdown desc: %q", desc)
	}
	if !strings.Contains(desc, "*Imported from [Ship v2](https://trello.com/c/abc12345)*") {
		t.Errorf("card1 missing footer marker: %q", desc)
	}
	// C11T3: card1's checklist is appended as markdown AFTER the card's
	// own description and BEFORE the dedupe footer (documented order).
	// The description is stored as a JSON string (strconv.Quote), so
	// unquote before asserting on the real markdown.
	unq, err := strconv.Unquote(desc)
	if err != nil {
		t.Fatalf("unquote card1 description: %v", err)
	}
	bodyIdx := strings.Index(unq, "the body")
	sectionIdx := strings.Index(unq, "\n\n## Launch tasks\n- [x] Write the announcement\n- [ ] Ship the binary")
	footerIdx := strings.Index(unq, "\n\n---\n*Imported from")
	if bodyIdx < 0 || sectionIdx < 0 || footerIdx < 0 {
		t.Fatalf("card1 description missing an expected section: %q", unq)
	}
	if !(bodyIdx < sectionIdx && sectionIdx < footerIdx) {
		t.Errorf("card1 section order wrong: body=%d checklist=%d footer=%d", bodyIdx, sectionIdx, footerIdx)
	}
	// card3: the non-empty checklist renders; the empty one is skipped
	// (no "## Nothing yet" section).
	var desc3 string
	if err := pool.QueryRow(ctx,
		`SELECT description::text FROM issues WHERE project_id = $1::uuid AND name = 'Docs'`, projectID).Scan(&desc3); err != nil {
		t.Fatalf("card3 row: %v", err)
	}
	unq3, err := strconv.Unquote(desc3)
	if err != nil {
		t.Fatalf("unquote card3 description: %v", err)
	}
	if !strings.Contains(unq3, "\n\n## Review pass\n- [ ] Spellcheck") {
		t.Errorf("card3 missing checklist section: %q", unq3)
	}
	if strings.Contains(unq3, "Nothing yet") {
		t.Errorf("card3 rendered an empty checklist section: %q", unq3)
	}
	// Cards without checklists: description unchanged — no "##" section
	// (card2 has an empty desc, card5 has a plain-text desc).
	for _, tc := range []struct{ name, desc string }{
		{"Refactor", ""},
		{"Fifth", "trailing"},
	} {
		var d string
		if err := pool.QueryRow(ctx,
			`SELECT description::text FROM issues WHERE project_id = $1::uuid AND name = $2`, projectID, tc.name).Scan(&d); err != nil {
			t.Fatalf("%s row: %v", tc.name, err)
		}
		unqd, err := strconv.Unquote(d)
		if err != nil {
			t.Fatalf("unquote %s description: %v", tc.name, err)
		}
		if strings.Contains(unqd, "##") {
			t.Errorf("%s gained a checklist section without checklists: %q", tc.name, unqd)
		}
		if tc.desc != "" && !strings.HasPrefix(unqd, tc.desc) {
			t.Errorf("%s description no longer starts with the card desc: %q", tc.name, unqd)
		}
	}
	if stateName != "Todo" {
		t.Errorf("card1 state = %q, want Todo", stateName)
	}
	if targetDate == nil || *targetDate != "2026-02-01" {
		t.Errorf("card1 target_date = %v, want 2026-02-01", targetDate)
	}
	if assigneeID == nil {
		t.Error("card1: assignee not matched to the member")
	}
	// card3 (unmatched list) lands in the created state.
	if err := pool.QueryRow(ctx, `
		SELECT s.name FROM issues i JOIN states s ON s.id = i.state_id
		WHERE i.project_id = $1::uuid AND i.name = 'Docs'`, projectID).Scan(&stateName); err != nil {
		t.Fatalf("card3 row: %v", err)
	} else if stateName != "QA Review" {
		t.Errorf("card3 state = %q, want QA Review", stateName)
	}

	// Comments: card1 has 2, card3 has 1, all attributed with original
	// timestamps.
	var comments int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM comments c JOIN issues i ON i.id = c.issue_id WHERE i.project_id = $1::uuid`, projectID).Scan(&comments); err != nil {
		t.Fatalf("comment count: %v", err)
	} else if comments != 3 {
		t.Errorf("comments = %d, want 3", comments)
	}
	var content, created string
	if err := pool.QueryRow(ctx, `
		SELECT c.content::text, c.created_at::text FROM comments c
		JOIN issues i ON i.id = c.issue_id
		WHERE i.project_id = $1::uuid AND i.name = 'Ship v2' ORDER BY c.created_at LIMIT 1`, projectID).Scan(&content, &created); err != nil {
		t.Fatalf("comment row: %v", err)
	}
	if !strings.Contains(content, "originally posted by ada") {
		t.Errorf("comment missing attribution: %q", content)
	}
	if !strings.Contains(created, "2026-01-03") {
		t.Errorf("comment created_at = %q, want the original 2026-01-03", created)
	}

	// Rerun: everything is skipped via the footer marker (dedupe), no
	// duplicate states or labels are created.
	res2, err := ImportTrelloCardsWithClient(ctx, pool, client, wsSlug, ident, actorID, in)
	if err != nil {
		t.Fatalf("rerun: %v", err)
	}
	if res2.Created != 0 || res2.Skipped != 4 {
		t.Errorf("rerun: created=%d skipped=%d, want 0/4", res2.Created, res2.Skipped)
	}
	if len(res2.StatesCreated) != 0 || len(res2.LabelsCreated) != 0 {
		t.Errorf("rerun created states=%v labels=%v, want none", res2.StatesCreated, res2.LabelsCreated)
	}

	stub.authOK(t, in.APIKey, in.Token)
}

// TestImportTrelloChecklistFetchFailure: a failing checklist fetch is
// enrichment-only (like comments) — the card still imports and the
// failure is recorded as a per-card error, never aborting the batch.
func TestImportTrelloChecklistFetchFailure(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	actorID, memberEmail, wsSlug, ident, projectID := trelloImportFixture(t, pool)

	stub, srv := newTrelloStub(t, memberEmail)
	stub.checklistsHandler = func(w http.ResponseWriter, r *http.Request) {
		stub.record(r)
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `boom`)
	}
	in := trelloTestInput()
	client := trelloTestClient(t, stub, srv, in)

	res, err := ImportTrelloCardsWithClient(ctx, pool, client, wsSlug, ident, actorID, in)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.Created != 4 {
		t.Errorf("created = %d, want 4 (checklist failure must not abort cards)", res.Created)
	}
	found := false
	for _, e := range res.Errors {
		if strings.Contains(e.Message, "checklists not imported") {
			found = true
		}
	}
	if !found {
		t.Errorf("errors = %v, want a per-card 'checklists not imported' entry", res.Errors)
	}
	// The card with the failed fetch still has a clean description: card
	// desc + footer only, no half-rendered checklist.
	var desc string
	if err := pool.QueryRow(ctx,
		`SELECT description::text FROM issues WHERE project_id = $1::uuid AND name = 'Ship v2'`, projectID).Scan(&desc); err != nil {
		t.Fatalf("card1 row: %v", err)
	}
	if strings.Contains(desc, "##") {
		t.Errorf("card1 gained a checklist section from a failed fetch: %q", desc)
	}
	stub.authOK(t, in.APIKey, in.Token)
}

// TestTrelloTokenNeverPersisted pins the credential handling: key+token
// on the wire as query params, never in the database, never in error
// messages.
func TestTrelloTokenNeverPersisted(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	actorID, memberEmail, wsSlug, ident, _ := trelloImportFixture(t, pool)

	const apiKey = "apikeyDONOTSTORE_1234567890abcdef"
	const token = "tokendonotstore_0987654321fedcba"
	stub, srv := newTrelloStub(t, memberEmail)
	client := newTrelloTestClient(apiKey, token, srv.URL)

	in := trelloTestInput()
	if _, err := ImportTrelloCardsWithClient(ctx, pool, client, wsSlug, ident, actorID, in); err != nil {
		t.Fatalf("import: %v", err)
	}
	assertTokenAbsentFromDB(t, pool, apiKey)
	assertTokenAbsentFromDB(t, pool, token)
	stub.authOK(t, apiKey, token)

	// Failed run (401): the error must not echo either credential.
	stub.boardHandler = func(w http.ResponseWriter, r *http.Request) {
		stub.record(r)
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `invalid token`)
	}
	_, err := ImportTrelloCardsWithClient(ctx, pool, client, wsSlug, ident, actorID, in)
	if err == nil {
		t.Fatal("expected 401 error")
	}
	if !errors.Is(err, ErrTrelloUnauthorized) {
		t.Errorf("err = %v, want ErrTrelloUnauthorized", err)
	}
	if strings.Contains(err.Error(), token) || strings.Contains(err.Error(), apiKey) {
		t.Errorf("401 error leaks a credential: %q", err.Error())
	}
	assertTokenAbsentFromDB(t, pool, apiKey)
	assertTokenAbsentFromDB(t, pool, token)
}

// TestTrelloImportErrorMapping pins upstream error mapping: 404 on a bad
// board id, 401 on a bad token, 429 with Retry-After, and 5xx as a
// generic upstream error.
func TestTrelloImportErrorMapping(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	actorID, _, wsSlug, ident, _ := trelloImportFixture(t, pool)
	in := trelloTestInput()

	cases := []struct {
		name    string
		status  int
		body    string
		headers map[string]string
		// wantRateLimit pins the typed rate-limit error (mirrors the
		// GitHub/Jira importers: 429 is never retried silently, the
		// handler surfaces Retry-After). wantErr pins the sentinel
		// otherwise.
		wantRateLimit bool
		wantErr       error
	}{
		{"not found", 404, `{"error":"invalid id"}`, nil, false, ErrTrelloNotFound},
		{"unauthorized", 401, `invalid token`, nil, false, ErrTrelloUnauthorized},
		{"rate limited", 429, `Rate limit exceeded`, map[string]string{"Retry-After": "30"}, true, nil},
		{"server error", 500, `boom`, nil, false, ErrTrelloUpstream},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub, srv := newTrelloStub(t, "nobody@example.com")
			stub.boardHandler = func(w http.ResponseWriter, r *http.Request) {
				stub.record(r)
				for k, v := range tc.headers {
					w.Header().Set(k, v)
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}
			client := newTrelloTestClient(in.APIKey, in.Token, srv.URL)
			_, err := PreviewTrelloImportWithClient(ctx, pool, client, wsSlug, ident, actorID, in)
			if tc.wantRateLimit {
				var rl *TrelloRateLimitError
				if !errors.As(err, &rl) {
					t.Errorf("err = %v, want *TrelloRateLimitError", err)
				} else if rl.RetryAfter != 30 {
					t.Errorf("retry_after = %d, want 30", rl.RetryAfter)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}
