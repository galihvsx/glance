package api

// Task 17: bulk operations + Idempotency-Key support.
//
// - POST .../issues/bulk-update {ids, patch} → 200 {results:[{id,ok,error?}]},
//   partial success across items inside one tx (per-item savepoints).
// - POST .../issues/bulk-delete {ids} → same shape, soft-deletes.
// - Idempotency-Key on POST .../issues, .../bulk-update, .../bulk-delete:
//   the winner executes and stores (status, body); a replay returns the
//   stored response byte-identical without re-executing; a loser racing an
//   in-flight request gets 409. Keys are scoped to (user_id, key), 24h TTL.
//
// Real test database, no skips.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"
)

// postAuthedJSONHeaders is postAuthedJSON with extra request headers (for
// Idempotency-Key).
func postAuthedJSONHeaders(t *testing.T, e *echo.Echo, method, path string, cookie *http.Cookie, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

// setupBulkProject creates a user, workspace and project and returns the
// pool, server, session cookie and the issues base path.
func setupBulkProject(t *testing.T, prefix string) (*pgxpool.Pool, *echo.Echo, *http.Cookie, string) {
	t.Helper()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testIssueServer(t, pool)

	cookie := loginTestUser(t, e, pool, uniqueEmail(prefix), "test-agent", uniqueIP())
	slug := uniqueSlug(prefix)
	createWorkspaceHTTP(t, e, cookie, "Bulk Co", slug)
	ident := uniqueProjectIdentifier(strings.ToUpper(prefix[:2]))
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	return pool, e, cookie, "/api/v1/workspaces/" + slug + "/projects/" + ident + "/issues"
}

// createIssueHTTP creates one issue and returns its id.
func createIssueHTTP(t *testing.T, e *echo.Echo, cookie *http.Cookie, base, name string) string {
	t.Helper()
	rec := postAuthedJSON(t, e, http.MethodPost, base, cookie, fmt.Sprintf(`{"name":%q}`, name))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create issue %q: status = %d (body: %s)", name, rec.Code, rec.Body.String())
	}
	var iss struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &iss); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	return iss.ID
}

// countIssues returns the number of live issues via the list endpoint.
func countIssues(t *testing.T, e *echo.Echo, cookie *http.Cookie, base string) int {
	t.Helper()
	rec := getAuthed(t, e, http.MethodGet, base+"?per_page=100", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list for count: status = %d", rec.Code)
	}
	var page struct {
		Results []json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	return len(page.Results)
}

// countActivities counts issue_activities rows for an issue, via the DB.
func countActivities(t *testing.T, pool *pgxpool.Pool, issueID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM issue_activities WHERE issue_id = $1::uuid`, issueID).Scan(&n); err != nil {
		t.Fatalf("count activities: %v", err)
	}
	return n
}

// TestIdempotentCreate: same Idempotency-Key twice → one issue, the second
// response byte-identical to the first. A different key creates a second
// issue; no key behaves as before.
func TestIdempotentCreate(t *testing.T) {
	pool, e, cookie, base := setupBulkProject(t, "idem-create")

	h := map[string]string{IdempotencyKeyHeader: "key-abc-123"}
	rec1 := postAuthedJSONHeaders(t, e, http.MethodPost, base, cookie, `{"name":"once"}`, h)
	if rec1.Code != http.StatusCreated {
		t.Fatalf("first: status = %d (body: %s)", rec1.Code, rec1.Body.String())
	}
	rec2 := postAuthedJSONHeaders(t, e, http.MethodPost, base, cookie, `{"name":"once"}`, h)
	if rec2.Code != http.StatusCreated {
		t.Fatalf("replay: status = %d (body: %s)", rec2.Code, rec2.Body.String())
	}
	if !bytes.Equal(rec1.Body.Bytes(), rec2.Body.Bytes()) {
		t.Fatalf("replay body differs:\nfirst:  %s\nsecond: %s", rec1.Body.Bytes(), rec2.Body.Bytes())
	}
	if n := countIssues(t, e, cookie, base); n != 1 {
		t.Fatalf("issues = %d, want 1 (duplicate created)", n)
	}

	// A different key is a different request.
	rec3 := postAuthedJSONHeaders(t, e, http.MethodPost, base, cookie, `{"name":"twice"}`,
		map[string]string{IdempotencyKeyHeader: "key-other-456"})
	if rec3.Code != http.StatusCreated {
		t.Fatalf("other key: status = %d", rec3.Code)
	}
	if n := countIssues(t, e, cookie, base); n != 2 {
		t.Fatalf("issues = %d, want 2", n)
	}

	// No key: normal behavior, still creates.
	rec4 := postAuthedJSON(t, e, http.MethodPost, base, cookie, `{"name":"plain"}`)
	if rec4.Code != http.StatusCreated {
		t.Fatalf("no key: status = %d", rec4.Code)
	}
	_ = pool
}

// TestIdempotentCreateRace: N concurrent requests with the same key →
// exactly one issue is created; every 201 body is byte-identical (a loser
// racing the winner may see 409 in_progress instead).
func TestIdempotentCreateRace(t *testing.T) {
	_, e, cookie, base := setupBulkProject(t, "idem-race")

	const n = 10
	h := map[string]string{IdempotencyKeyHeader: "race-key-1"}
	type res struct {
		code int
		body []byte
	}
	out := make([]res, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range out {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			rec := postAuthedJSONHeaders(t, e, http.MethodPost, base, cookie, `{"name":"racy"}`, h)
			out[i] = res{rec.Code, append([]byte(nil), rec.Body.Bytes()...)}
		}(i)
	}
	close(start)
	wg.Wait()

	var first201 []byte
	created := 0
	for i, r := range out {
		switch r.code {
		case http.StatusCreated:
			created++
			if first201 == nil {
				first201 = r.body
			} else if !bytes.Equal(first201, r.body) {
				t.Fatalf("goroutine %d: 201 body differs from first 201", i)
			}
		case http.StatusConflict:
			// Loser racing an in-flight winner: acceptable.
			var env struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(r.body, &env); err != nil || env.Error.Code != "conflict" {
				t.Fatalf("goroutine %d: 409 body is not the envelope: %s", i, r.body)
			}
		default:
			t.Fatalf("goroutine %d: unexpected status %d (body: %s)", i, r.code, r.body)
		}
	}
	if created == 0 {
		t.Fatal("no request returned 201")
	}
	if n := countIssues(t, e, cookie, base); n != 1 {
		t.Fatalf("issues = %d, want exactly 1 after %d concurrent same-key requests", n, len(out))
	}
}

// TestIdempotentInProgress409: a key whose row is stuck in_progress (a
// crashed or still-running winner) → 409 conflict, not a duplicate run.
func TestIdempotentInProgress409(t *testing.T) {
	pool, e, cookie, base := setupBulkProject(t, "idem-prog")

	userID := authedUserID(t, e, cookie)
	if _, err := pool.Exec(t.Context(),
		`INSERT INTO idempotency_keys (user_id, idem_key, endpoint, status)
		 VALUES ($1::uuid, 'stuck-key', 'POST /issues', 'in_progress')`, userID); err != nil {
		t.Fatalf("seed in_progress: %v", err)
	}
	rec := postAuthedJSONHeaders(t, e, http.MethodPost, base, cookie, `{"name":"x"}`,
		map[string]string{IdempotencyKeyHeader: "stuck-key"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil || env.Error.Code != "conflict" {
		t.Fatalf("409 envelope: body=%s", rec.Body.String())
	}
	if n := countIssues(t, e, cookie, base); n != 0 {
		t.Fatalf("issues = %d, want 0 (loser must not execute)", n)
	}
}

// TestIdempotentReplayCompleted: a stored completed response is replayed
// byte-identical without executing the operation.
func TestIdempotentReplayCompleted(t *testing.T) {
	pool, e, cookie, base := setupBulkProject(t, "idem-replay")

	userID := authedUserID(t, e, cookie)
	stored := `{"display_id":"REPLAY-1","note":"stored"}`
	if _, err := pool.Exec(t.Context(),
		`INSERT INTO idempotency_keys (user_id, idem_key, endpoint, status, response_code, response_body)
		 VALUES ($1::uuid, 'done-key', 'POST /issues', 'completed', 201, $2)`,
		userID, stored); err != nil {
		t.Fatalf("seed completed: %v", err)
	}
	// Different payload, same key: the stored response wins, no execution.
	rec := postAuthedJSONHeaders(t, e, http.MethodPost, base, cookie, `{"name":"ignored"}`,
		map[string]string{IdempotencyKeyHeader: "done-key"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	if string(bytes.TrimSpace(rec.Body.Bytes())) != stored {
		t.Fatalf("replay body = %s, want stored %s", rec.Body.Bytes(), stored)
	}
	if n := countIssues(t, e, cookie, base); n != 0 {
		t.Fatalf("issues = %d, want 0 (replay must not execute)", n)
	}
}

// TestIdempotentExpiredKeyReexecutes: a completed row older than 24h is
// garbage-collected on access — the request executes fresh.
func TestIdempotentExpiredKeyReexecutes(t *testing.T) {
	pool, e, cookie, base := setupBulkProject(t, "idem-ttl")

	userID := authedUserID(t, e, cookie)
	if _, err := pool.Exec(t.Context(),
		`INSERT INTO idempotency_keys (user_id, idem_key, endpoint, status, response_code, response_body, created_at)
		 VALUES ($1::uuid, 'old-key', 'POST /issues', 'completed', 201, '{"stale":true}'::jsonb, now() - interval '26 hours')`,
		userID); err != nil {
		t.Fatalf("seed expired: %v", err)
	}
	rec := postAuthedJSONHeaders(t, e, http.MethodPost, base, cookie, `{"name":"fresh"}`,
		map[string]string{IdempotencyKeyHeader: "old-key"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"stale"`) {
		t.Fatalf("stale stored response was replayed: %s", rec.Body.String())
	}
	if n := countIssues(t, e, cookie, base); n != 1 {
		t.Fatalf("issues = %d, want 1 (expired key must re-execute)", n)
	}
}

// bulkResults decodes the {results:[...]} envelope.
func bulkResults(t *testing.T, rec *httptest.ResponseRecorder) []struct {
	ID    string  `json:"id"`
	OK    bool    `json:"ok"`
	Error *string `json:"error"`
} {
	t.Helper()
	var env struct {
		Results []struct {
			ID    string  `json:"id"`
			OK    bool    `json:"ok"`
			Error *string `json:"error"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode bulk results: %v (body: %s)", err, rec.Body.String())
	}
	return env.Results
}

// TestBulkUpdatePartial: mixed valid/invalid/deleted ids → per-item results;
// valid ones succeed inside the same tx, invalid ones report errors.
func TestBulkUpdatePartial(t *testing.T) {
	pool, e, cookie, base := setupBulkProject(t, "bulk-upd")

	id1 := createIssueHTTP(t, e, cookie, base, "one")
	id2 := createIssueHTTP(t, e, cookie, base, "two")
	id3 := createIssueHTTP(t, e, cookie, base, "three")
	// Soft-delete id3 so it counts as invalid for the bulk update.
	if rec := postAuthedJSON(t, e, http.MethodDelete, base+"/"+id3, cookie, ``); rec.Code != http.StatusNoContent {
		t.Fatalf("pre-delete: status = %d", rec.Code)
	}

	body := fmt.Sprintf(`{"ids":[%q,%q,"00000000-0000-0000-0000-000000000000",%q,"not-a-uuid"],"patch":{"priority":1}}`,
		id1, id2, id3)
	rec := postAuthedJSON(t, e, http.MethodPost, base+"/bulk-update", cookie, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("bulk-update: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	results := bulkResults(t, rec)
	if len(results) != 5 {
		t.Fatalf("results = %d, want 5", len(results))
	}
	if !results[0].OK || !results[1].OK {
		t.Fatalf("valid items failed: %+v", results[:2])
	}
	for i, r := range results[2:] {
		if r.OK || r.Error == nil || *r.Error == "" {
			t.Fatalf("results[%d] = %+v, want ok=false with error", i+2, r)
		}
	}

	// Valid items actually updated; failures rolled back per-item.
	for _, id := range []string{id1, id2} {
		rec := getAuthed(t, e, http.MethodGet, base+"/"+id, cookie)
		var iss struct {
			Priority int `json:"priority"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &iss); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if iss.Priority != 1 {
			t.Fatalf("issue %s priority = %d, want 1", id, iss.Priority)
		}
	}
	_ = pool
}

// TestBulkDeletePartial: valid ids soft-deleted, bogus ids report errors.
func TestBulkDeletePartial(t *testing.T) {
	_, e, cookie, base := setupBulkProject(t, "bulk-del")

	id1 := createIssueHTTP(t, e, cookie, base, "gone-one")
	id2 := createIssueHTTP(t, e, cookie, base, "gone-two")

	body := fmt.Sprintf(`{"ids":[%q,"00000000-0000-0000-0000-000000000000",%q]}`, id1, id2)
	rec := postAuthedJSON(t, e, http.MethodPost, base+"/bulk-delete", cookie, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("bulk-delete: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	results := bulkResults(t, rec)
	if len(results) != 3 || !results[0].OK || results[1].OK || results[1].Error == nil || !results[2].OK {
		t.Fatalf("results = %+v, want [ok, err, ok]", results)
	}

	// Deleted issues read as 404; the survivor is untouched.
	for _, id := range []string{id1, id2} {
		if rec := getAuthed(t, e, http.MethodGet, base+"/"+id, cookie); rec.Code != http.StatusNotFound {
			t.Fatalf("get %s after bulk-delete: status = %d, want 404", id, rec.Code)
		}
	}
}

// TestBulkValidation: empty ids, oversized batches, empty patches → 400;
// the error envelope, not partial results.
func TestBulkValidation(t *testing.T) {
	_, e, cookie, base := setupBulkProject(t, "bulk-val")

	for path, body := range map[string]string{
		base + "/bulk-update": `{"ids":[],"patch":{"priority":1}}`,
		base + "/bulk-delete": `{"ids":[]}`,
	} {
		rec := postAuthedJSON(t, e, http.MethodPost, path, cookie, body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("POST %s empty ids: status = %d, want 400 (body: %s)", path, rec.Code, rec.Body.String())
		}
	}

	// 101 ids → 400.
	ids := make([]string, 101)
	for i := range ids {
		ids[i] = `"00000000-0000-0000-0000-000000000000"`
	}
	rec := postAuthedJSON(t, e, http.MethodPost, base+"/bulk-update", cookie,
		`{"ids":[`+strings.Join(ids, ",")+`],"patch":{"priority":1}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("oversized batch: status = %d, want 400", rec.Code)
	}

	// Empty patch → 400 (ErrNothingToUpdate).
	id := createIssueHTTP(t, e, cookie, base, "v")
	rec = postAuthedJSON(t, e, http.MethodPost, base+"/bulk-update", cookie,
		fmt.Sprintf(`{"ids":[%q],"patch":{}}`, id))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty patch: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
}

// TestIdempotentBulkUpdate: same key twice on bulk-update → the second
// response is byte-identical and no new activity rows are written (no
// duplicate effect).
func TestIdempotentBulkUpdate(t *testing.T) {
	pool, e, cookie, base := setupBulkProject(t, "idem-bulk")

	id1 := createIssueHTTP(t, e, cookie, base, "b1")
	id2 := createIssueHTTP(t, e, cookie, base, "b2")

	h := map[string]string{IdempotencyKeyHeader: "bulk-key-1"}
	body := fmt.Sprintf(`{"ids":[%q,%q],"patch":{"priority":3}}`, id1, id2)
	rec1 := postAuthedJSONHeaders(t, e, http.MethodPost, base+"/bulk-update", cookie, body, h)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first: status = %d (body: %s)", rec1.Code, rec1.Body.String())
	}
	before := countActivities(t, pool, id1) + countActivities(t, pool, id2)

	rec2 := postAuthedJSONHeaders(t, e, http.MethodPost, base+"/bulk-update", cookie, body, h)
	if rec2.Code != http.StatusOK {
		t.Fatalf("replay: status = %d (body: %s)", rec2.Code, rec2.Body.String())
	}
	if !bytes.Equal(rec1.Body.Bytes(), rec2.Body.Bytes()) {
		t.Fatalf("replay body differs:\n%s\n%s", rec1.Body.Bytes(), rec2.Body.Bytes())
	}
	if after := countActivities(t, pool, id1) + countActivities(t, pool, id2); after != before {
		t.Fatalf("activity rows grew on replay: %d → %d (duplicate effect)", before, after)
	}
}
