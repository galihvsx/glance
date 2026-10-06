package api

// Realtime websocket hub HTTP tests (Task 24): unauthenticated handshake
// rejection (Review Focus #5), channel-level authorization, broadcast
// delivery of service mutations, and single-use ticket auth. Real test
// database, no skips.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"
	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"

	"glance/internal/realtime"
	"glance/internal/service"
)

// testWSServer builds the test echo server with the /ws routes mounted and
// wires the realtime hub into the service layer so service mutations
// broadcast to connected test subscribers. The previous service.Realtime
// value is restored on cleanup.
func testWSServer(t *testing.T, pool *pgxpool.Pool) (*echo.Echo, *realtime.Hub) {
	t.Helper()
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	hub := realtime.NewHub()
	RegisterWSRoutes(e, &WSHandler{Pool: pool, Hub: hub})
	prev := service.Realtime
	service.Realtime = hub
	t.Cleanup(func() { service.Realtime = prev })
	return e, hub
}

func testWSURL(t *testing.T, e *echo.Echo, path string) string {
	t.Helper()
	srv := httptest.NewServer(e)
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http") + path
}

func dialWS(t *testing.T, url string, cookie *http.Cookie) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var hdr http.Header
	if cookie != nil {
		hdr = http.Header{"Cookie": {cookie.String()}}
	}
	conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		t.Fatalf("dial ws: %v", err)
	}
	t.Cleanup(func() { conn.Close(websocket.StatusNormalClosure, "") })
	return conn
}

// dialWSMustFail dials and asserts the handshake is rejected with
// wantStatus. nhooyr surfaces handshake failures as a plain fmt.Errorf
// ("expected handshake response status code 101 but got 401"), so the
// assertion matches on the status in the message.
func dialWSMustFail(t *testing.T, url string, wantStatus int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, _, err := websocket.Dial(ctx, url, nil)
	if err == nil {
		t.Fatalf("dial succeeded, want HTTP %d rejection", wantStatus)
	}
	if want := "but got " + itoa(wantStatus); !strings.Contains(err.Error(), want) {
		t.Fatalf("dial err = %v, want rejection %q", err, want)
	}
}

func sendWS(t *testing.T, conn *websocket.Conn, v any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := wsjson.Write(ctx, conn, v); err != nil {
		t.Fatalf("write ws: %v", err)
	}
}

func readWS(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var v map[string]any
	if err := wsjson.Read(ctx, conn, &v); err != nil {
		t.Fatalf("read ws: %v", err)
	}
	return v
}

func subscribeWS(t *testing.T, conn *websocket.Conn, channel string) {
	t.Helper()
	sendWS(t, conn, map[string]string{"action": "subscribe", "channel": channel})
	m := readWS(t, conn)
	if m["action"] != "subscribed" || m["channel"] != channel {
		t.Fatalf("subscribe %s: got %+v, want subscribed ack", channel, m)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func setupWSProject(t *testing.T) (*pgxpool.Pool, *echo.Echo, *http.Cookie, string, string) {
	t.Helper()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e, _ := testWSServer(t, pool)
	cookie := loginTestUser(t, e, pool, uniqueEmail("wsx"), "ws-test", uniqueIP())
	slug := uniqueSlug("wsrt")
	createWorkspaceHTTP(t, e, cookie, "WS Co", slug)
	ident := uniqueProjectIdentifier("WR")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	return pool, e, cookie, slug, ident
}

func TestUnauthenticatedSubscribeRejected(t *testing.T) {
	_, e, _, _, _ := setupWSProject(t)
	url := testWSURL(t, e, "/ws")

	// 1. No cookie, no ticket → 401.
	dialWSMustFail(t, url, http.StatusUnauthorized)

	// 2. Bad ticket → 401.
	dialWSMustFail(t, url+"?ticket=does-not-exist", http.StatusUnauthorized)

	// 3. Channel-level auth: a valid session for workspace A must not
	// receive workspace B's events.
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e2, _ := testWSServer(t, pool)
	cookieA := loginTestUser(t, e2, pool, uniqueEmail("wsa"), "ws-test", uniqueIP())
	slugA := uniqueSlug("wsa")
	createWorkspaceHTTP(t, e2, cookieA, "A Co", slugA)
	cookieB := loginTestUser(t, e2, pool, uniqueEmail("wsb"), "ws-test", uniqueIP())
	slugB := uniqueSlug("wsb")
	createWorkspaceHTTP(t, e2, cookieB, "B Co", slugB)
	url2 := testWSURL(t, e2, "/ws")

	conn := dialWS(t, url2, cookieA)

	// Subscribing to B's workspace channel → error, not subscribed.
	sendWS(t, conn, map[string]string{"action": "subscribe", "channel": "workspace:" + slugB})
	if m := readWS(t, conn); m["action"] != "error" {
		t.Fatalf("subscribe foreign workspace: got %+v, want error", m)
	}

	// Subscribing to another user's personal channel → error.
	sendWS(t, conn, map[string]string{"action": "subscribe", "channel": "user:00000000-0000-0000-0000-000000000000"})
	if m := readWS(t, conn); m["action"] != "error" {
		t.Fatalf("subscribe foreign user channel: got %+v, want error", m)
	}

	// Sanity: own workspace subscribes fine (connection still usable).
	subscribeWS(t, conn, "workspace:"+slugA)
}

func TestBroadcastReachesSubscriber(t *testing.T) {
	_, e, cookie, slug, ident := setupWSProject(t)
	url := testWSURL(t, e, "/ws")
	base := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/issues"

	conn := dialWS(t, url, cookie)
	subscribeWS(t, conn, "project:"+slug+":"+ident)
	subscribeWS(t, conn, "workspace:"+slug)

	// Create via HTTP → issue.created fanned out to project + workspace.
	rec := postAuthedJSON(t, e, http.MethodPost, base, cookie, `{"name":"Realtime ship"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create issue: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}

	seenCreated := map[string]bool{}
	for i := 0; i < 2; i++ {
		m := readWS(t, conn)
		if m["event"] != service.EventIssueCreated {
			t.Fatalf("got %+v, want issue.created", m)
		}
		ch, _ := m["channel"].(string)
		seenCreated[ch] = true
		data, ok := m["data"].(map[string]any)
		if !ok || data["id"] != created.ID || data["display_id"] != ident+"-1" {
			t.Fatalf("event data = %#v, want id/display_id", m["data"])
		}
		if _, ok := m["at"]; !ok {
			t.Fatal("event missing at timestamp")
		}
	}
	for _, ch := range []string{"project:" + slug + ":" + ident, "workspace:" + slug} {
		if !seenCreated[ch] {
			t.Fatalf("no issue.created on %s (seen %v)", ch, seenCreated)
		}
	}

	// Subscribe to the issue channel, then PATCH → issue.updated on all three.
	subscribeWS(t, conn, "issue:"+created.ID)
	rec = postAuthedJSON(t, e, http.MethodPatch, base+"/"+created.ID, cookie, `{"name":"Realtime ship v2"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch issue: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		m := readWS(t, conn)
		if m["event"] != service.EventIssueUpdated {
			t.Fatalf("got %+v, want issue.updated", m)
		}
		ch, _ := m["channel"].(string)
		seen[ch] = true
	}
	for _, ch := range []string{"issue:" + created.ID, "project:" + slug + ":" + ident, "workspace:" + slug} {
		if !seen[ch] {
			t.Fatalf("no issue.updated on %s (seen %v)", ch, seen)
		}
	}
}

// expectNoWSMessage asserts no event arrives on conn within d. Used to prove
// a cross-workspace broadcast does not leak to an unauthorized subscriber.
// NOTE: nhooyr closes the whole connection when a read's context expires
// (see timeoutLoop in nhooyr.io/websocket), so this must be the LAST read on
// conn — the connection is unusable afterwards.
func expectNoWSMessage(t *testing.T, conn *websocket.Conn, d time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	var v map[string]any
	if err := wsjson.Read(ctx, conn, &v); err == nil {
		t.Fatalf("got unexpected ws message %+v, want none", v)
	}
}

// TestProjectChannelIsWorkspaceQualified is the regression test for the
// cross-workspace leak (review finding, spec §6 R11): project identifiers
// are unique per workspace only, so the bare project:ENG scheme fanned both
// workspaces' events to one literal channel. The amended scheme is
// project:{slug}:{identifier}.
func TestProjectChannelIsWorkspaceQualified(t *testing.T) {
	// Two workspaces, the SAME project identifier ENG in each. U is a
	// member of ws-a only.
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e, _ := testWSServer(t, pool)

	cookieU := loginTestUser(t, e, pool, uniqueEmail("pqa"), "ws-test", uniqueIP())
	slugA := uniqueSlug("pqa")
	createWorkspaceHTTP(t, e, cookieU, "A Co", slugA)
	createProjectHTTP(t, e, cookieU, slugA, "Engineering A", "ENG")

	cookieB := loginTestUser(t, e, pool, uniqueEmail("pqb"), "ws-test", uniqueIP())
	slugB := uniqueSlug("pqb")
	createWorkspaceHTTP(t, e, cookieB, "B Co", slugB)
	createProjectHTTP(t, e, cookieB, slugB, "Engineering B", "ENG")

	url := testWSURL(t, e, "/ws")
	conn := dialWS(t, url, cookieU)

	// U subscribes to ws-a's qualified project channel — legitimate.
	subscribeWS(t, conn, "project:"+slugA+":ENG")

	// U cannot subscribe to ws-b's project channel — not a member there.
	sendWS(t, conn, map[string]string{"action": "subscribe", "channel": "project:" + slugB + ":ENG"})
	if m := readWS(t, conn); m["action"] != "error" {
		t.Fatalf("subscribe foreign project channel: got %+v, want error", m)
	}

	// The old bare scheme is rejected as malformed, not authorized.
	sendWS(t, conn, map[string]string{"action": "subscribe", "channel": "project:ENG"})
	if m := readWS(t, conn); m["action"] != "error" {
		t.Fatalf("subscribe bare project channel: got %+v, want error", m)
	}

	// An issue created in ws-a's ENG MUST reach U on the qualified channel.
	rec := postAuthedJSON(t, e, http.MethodPost,
		"/api/v1/workspaces/"+slugA+"/projects/ENG/issues", cookieU, `{"name":"A public"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create issue in ws-a: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	m := readWS(t, conn)
	if m["event"] != service.EventIssueCreated {
		t.Fatalf("got %+v, want issue.created", m)
	}
	if ch, _ := m["channel"].(string); ch != "project:"+slugA+":ENG" {
		t.Fatalf("channel = %q, want %q", ch, "project:"+slugA+":ENG")
	}

	// An issue created in ws-b's ENG must NOT reach U (the leak this test
	// pins down). The broadcast is synchronous inside postAuthedJSON, so if
	// the leak existed the event would already be queued — the 500ms window
	// only guards against async flakiness. Last read: the timed-out read
	// closes the connection (nhooyr), so nothing follows.
	rec = postAuthedJSON(t, e, http.MethodPost,
		"/api/v1/workspaces/"+slugB+"/projects/ENG/issues", cookieB, `{"name":"B secret"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create issue in ws-b: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	expectNoWSMessage(t, conn, 500*time.Millisecond)
}

func TestTicketSingleUse(t *testing.T) {
	_, e, cookie, _, _ := setupWSProject(t)
	url := testWSURL(t, e, "/ws")

	// Ticket endpoint requires auth.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/ws/ticket", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("ticket without auth: status = %d, want 401", rec.Code)
	}

	rec = getAuthed(t, e, http.MethodPost, "/api/v1/ws/ticket", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("ticket: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var tr struct {
		Ticket string `json:"ticket"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &tr); err != nil {
		t.Fatalf("decode ticket: %v", err)
	}
	if tr.Ticket == "" {
		t.Fatal("empty ticket")
	}

	// First use connects.
	dialWS(t, url+"?ticket="+tr.Ticket, nil)

	// Second use is rejected — single-use.
	dialWSMustFail(t, url+"?ticket="+tr.Ticket, http.StatusUnauthorized)
}
