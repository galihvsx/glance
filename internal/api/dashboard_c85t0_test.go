package api

// C85T0: intake_breakdown drill-down backend API over HTTP — the
// metric drill-down (GET /api/v1/widgets/:id/drilldown?metric=...),
// the funnel counts on the widget payload, and the metric-scoped CSV
// export, plus the OpenAPI pins. Real test database, no skips.
//
// Fixture world (per test, isolated): the C84 widget-data world (two
// projects A/B, five non-intake issues) plus eight intake rows in A,
// one in B, and one in an archived project C:
//   A: in-pending (in_app, pending), in-accepted (in_app, accepted),
//      in-rejected (in_app, rejected), in-snoozed (in_app, snoozed
//      with a future till), in-resurfaced (in_app, snoozed then
//      forced past its till via SQL — the EffectiveStatus case),
//      in-duplicate (in_app, duplicate of a live A issue),
//      in-form (form channel, pending — source flipped via SQL;
//      there is no HTTP path that writes a form source without a
//      public form round-trip, and the source column is the
//      contract under test, not the form submit path).
//   B: b-pending (in_app, pending).
// Status metrics count every source, so pending also includes
// in-form (form channel, still pending) — pending total is 4, not
// 3.
//   C: c-archived (in_app, pending) in a project archived via SQL
//      after creation — it must never appear in any count or row set.
// Expected totals over the live scope: in_app 7, form 1, email 0,
// accepted 1, rejected 1, duplicate 1, pending 4 (in-pending,
// in-form, in-resurfaced, b-pending), snoozed 1 (in-snoozed).

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"glance/docs"
)

// intakeDrilldownFixture is the C85 drill-down world.
type intakeDrilldownFixture struct {
	wd       *widgetDataFixture
	dashURL  string
	widgetID string
	issues   map[string]string // intake title -> issue id
	targetID string
}

func setupIntakeDrilldown(t *testing.T) *intakeDrilldownFixture {
	t.Helper()
	wd := setupWidgetData(t)
	RegisterIntakeRoutes(wd.fx.e, &IssueHandler{Pool: wd.fx.pool})
	fx := &intakeDrilldownFixture{
		wd:      wd,
		dashURL: wd.fx.wsBase + "/dashboards/" + wd.dashID,
		issues:  map[string]string{},
	}
	issues := listIssueIDs(t, wd.fx, wd.identA)
	if len(issues) == 0 {
		t.Fatal("no issues in project A for the duplicate target")
	}
	fx.targetID = issues[0]

	mkIntake := func(ident, name string) string {
		t.Helper()
		rec := postAuthedJSON(t, wd.fx.e, http.MethodPost,
			fmt.Sprintf("%s/projects/%s/issues", wd.fx.wsBase, ident),
			wd.fx.adminCookie, fmt.Sprintf(`{"name":%q,"intake":true}`, name))
		if rec.Code != http.StatusCreated {
			t.Fatalf("create intake issue %q: status = %d (body: %s)", name, rec.Code, rec.Body.String())
		}
		var iss struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &iss); err != nil {
			t.Fatalf("decode intake issue %q: %v", name, err)
		}
		fx.issues[name] = iss.ID
		return iss.ID
	}
	triage := func(ident, issueID, action, body string) {
		t.Helper()
		rec := postAuthedJSON(t, wd.fx.e, http.MethodPost,
			fmt.Sprintf("%s/projects/%s/intake/issues/%s/%s", wd.fx.wsBase, ident, issueID, action),
			wd.fx.adminCookie, body)
		if rec.Code != http.StatusOK {
			t.Fatalf("triage %s %s: status = %d (body: %s)", action, issueID, rec.Code, rec.Body.String())
		}
	}
	sql := func(q string, args ...any) {
		t.Helper()
		if _, err := wd.fx.pool.Exec(context.Background(), q, args...); err != nil {
			t.Fatalf("fixture SQL %q: %v", q, err)
		}
	}

	mkIntake(wd.identA, "in-pending")
	mkIntake(wd.identA, "in-accepted")
	mkIntake(wd.identA, "in-rejected")
	mkIntake(wd.identA, "in-snoozed")
	mkIntake(wd.identA, "in-resurfaced")
	mkIntake(wd.identA, "in-duplicate")
	mkIntake(wd.identA, "in-form")
	mkIntake(wd.identB, "b-pending")

	future := time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)
	triage(wd.identA, fx.issues["in-accepted"], "accept", `{}`)
	triage(wd.identA, fx.issues["in-rejected"], "reject", `{}`)
	triage(wd.identA, fx.issues["in-snoozed"], "snooze", `{"snoozed_till":"`+future+`"}`)
	triage(wd.identA, fx.issues["in-resurfaced"], "snooze", `{"snoozed_till":"`+future+`"}`)
	// Resurface: force the till into the past (the snooze endpoint
	// rightly refuses a past till, so the resurfaced state is built
	// via SQL — the read-time EffectiveStatus rule is what is under
	// test, not the snooze writer).
	sql(`UPDATE intake_issues SET snoozed_till = now() - interval '2 hours' WHERE issue_id = $1::uuid`,
		fx.issues["in-resurfaced"])
	triage(wd.identA, fx.issues["in-duplicate"], "duplicate",
		fmt.Sprintf(`{"duplicate_to_id":%q}`, fx.targetID))
	// Form channel: no HTTP creation path writes source=form without
	// a public form round-trip; flip the column directly.
	sql(`UPDATE intake_issues SET source = 'form' WHERE issue_id = $1::uuid`,
		fx.issues["in-form"])

	// Archived project C: intake row created, then the project is
	// archived — every count and row set must exclude it.
	identC := uniqueProjectIdentifier("WDC")
	createProjectHTTP(t, wd.fx.e, wd.fx.adminCookie,
		strings.TrimPrefix(wd.fx.wsBase, "/api/v1/workspaces/"), "Data C", identC)
	mkIntake(identC, "c-archived")
	sql(`UPDATE projects SET archived_at = now() WHERE identifier = $1`, identC)

	fx.widgetID = createWidgetHTTP(t, wd.fx.e, wd.fx.adminCookie, fx.dashURL,
		`{"title":"Intake","widget_type":"intake_breakdown","config":{}}`)
	return fx
}

// getIntakeDrilldown fetches the intake drill-down with a raw query.
func getIntakeDrilldown(t *testing.T, fx *intakeDrilldownFixture, cookie *http.Cookie, query string) (int, map[string]any) {
	t.Helper()
	return getDrilldown(t, fx.wd.fx, fx.widgetID, cookie, query)
}

// intakeDrilldownTitles extracts the drill-down row titles in order.
func intakeDrilldownTitles(t *testing.T, body map[string]any) []string {
	t.Helper()
	items, ok := body["items"].([]any)
	if !ok {
		t.Fatalf("no items in drilldown body: %v", body)
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.(map[string]any)["title"].(string))
	}
	return out
}

// intakeDrilldownTitleSet extracts the drill-down row titles as a set.
func intakeDrilldownTitleSet(t *testing.T, body map[string]any) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, title := range intakeDrilldownTitles(t, body) {
		out[title] = true
	}
	return out
}

// intakeDrilldownRowByTitle returns one drill-down row by its title.
func intakeDrilldownRowByTitle(t *testing.T, body map[string]any, title string) map[string]any {
	t.Helper()
	for _, it := range body["items"].([]any) {
		row := it.(map[string]any)
		if row["title"] == title {
			return row
		}
	}
	t.Fatalf("no drilldown row titled %q (body: %v)", title, body)
	return nil
}

// getIntakeCSV fetches the widget CSV export with a raw query string.
func getIntakeCSV(t *testing.T, fx *intakeDrilldownFixture, cookie *http.Cookie, query string) (int, http.Header, string) {
	t.Helper()
	rec := getAuthed(t, fx.wd.fx.e, http.MethodGet, "/api/v1/widgets/"+fx.widgetID+"/export.csv"+query, cookie)
	return rec.Code, rec.Header(), rec.Body.String()
}

func TestC85_IntakeDrilldownInApp(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	code, body := getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, "?metric=in_app")
	if code != http.StatusOK {
		t.Fatalf("in_app drilldown: status = %d (body: %v)", code, body)
	}
	if body["total"] != float64(7) || body["metric"] != "in_app" {
		t.Fatalf("envelope = %v, want total 7 metric in_app", body)
	}
	want := map[string]bool{"in-pending": true, "in-accepted": true, "in-rejected": true,
		"in-snoozed": true, "in-resurfaced": true, "in-duplicate": true, "b-pending": true}
	if got := intakeDrilldownTitleSet(t, body); !reflect.DeepEqual(got, want) {
		t.Fatalf("in_app titles = %v, want %v", got, want)
	}
}

func TestC85_IntakeDrilldownForm(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	code, body := getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, "?metric=form")
	if code != http.StatusOK {
		t.Fatalf("form drilldown: status = %d (body: %v)", code, body)
	}
	if body["total"] != float64(1) {
		t.Fatalf("total = %v, want 1", body["total"])
	}
	if got := intakeDrilldownTitles(t, body); !reflect.DeepEqual(got, []string{"in-form"}) {
		t.Fatalf("form titles = %v, want [in-form]", got)
	}
}

func TestC85_IntakeDrilldownEmailEmpty(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	code, body := getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, "?metric=email")
	if code != http.StatusOK {
		t.Fatalf("email drilldown: status = %d (body: %v)", code, body)
	}
	// Honest empty: no email ingestion, never fake rows — but still
	// a 200 with the metric envelope, not an error.
	if body["total"] != float64(0) || body["metric"] != "email" {
		t.Fatalf("envelope = %v, want total 0 metric email", body)
	}
	if items := body["items"].([]any); len(items) != 0 {
		t.Fatalf("email items = %d, want 0", len(items))
	}
}

func TestC85_IntakeDrilldownAccepted(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	code, body := getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, "?metric=accepted")
	if code != http.StatusOK {
		t.Fatalf("accepted drilldown: status = %d (body: %v)", code, body)
	}
	if body["total"] != float64(1) {
		t.Fatalf("total = %v, want 1", body["total"])
	}
	if got := intakeDrilldownTitles(t, body); !reflect.DeepEqual(got, []string{"in-accepted"}) {
		t.Fatalf("accepted titles = %v, want [in-accepted]", got)
	}
}

func TestC85_IntakeDrilldownRejected(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	code, body := getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, "?metric=rejected")
	if code != http.StatusOK {
		t.Fatalf("rejected drilldown: status = %d (body: %v)", code, body)
	}
	if body["total"] != float64(1) {
		t.Fatalf("total = %v, want 1", body["total"])
	}
	if got := intakeDrilldownTitles(t, body); !reflect.DeepEqual(got, []string{"in-rejected"}) {
		t.Fatalf("rejected titles = %v, want [in-rejected]", got)
	}
}

func TestC85_IntakeDrilldownDuplicate(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	code, body := getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, "?metric=duplicate")
	if code != http.StatusOK {
		t.Fatalf("duplicate drilldown: status = %d (body: %v)", code, body)
	}
	if body["total"] != float64(1) {
		t.Fatalf("total = %v, want 1", body["total"])
	}
	row := intakeDrilldownRowByTitle(t, body, "in-duplicate")
	// Duplicate is status code 4 with its target id carried.
	if row["status"] != float64(4) || row["status_name"] != "duplicate" {
		t.Fatalf("duplicate row status = %v/%v, want 4/duplicate", row["status"], row["status_name"])
	}
	if row["duplicate_to_id"] != fx.targetID {
		t.Fatalf("duplicate_to_id = %v, want the target %s", row["duplicate_to_id"], fx.targetID)
	}
}

func TestC85_IntakeDrilldownPendingIncludesResurfaced(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	code, body := getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, "?metric=pending")
	if code != http.StatusOK {
		t.Fatalf("pending drilldown: status = %d (body: %v)", code, body)
	}
	if body["total"] != float64(4) {
		t.Fatalf("total = %v, want 4", body["total"])
	}
	// in-form is pending too: status metrics count every source.
	want := map[string]bool{"in-pending": true, "in-form": true, "in-resurfaced": true, "b-pending": true}
	if got := intakeDrilldownTitleSet(t, body); !reflect.DeepEqual(got, want) {
		t.Fatalf("pending titles = %v, want %v", got, want)
	}
	// The resurfaced row reads back with EFFECTIVE status pending
	// (code 0), even though its stored status is snoozed — and its
	// (past) snoozed_till is still visible.
	row := intakeDrilldownRowByTitle(t, body, "in-resurfaced")
	if row["status"] != float64(0) || row["status_name"] != "pending" {
		t.Fatalf("resurfaced row status = %v/%v, want 0/pending", row["status"], row["status_name"])
	}
	if row["snoozed_till"] == nil || row["snoozed_till"] == "" {
		t.Fatalf("resurfaced row snoozed_till = %v, want the past till", row["snoozed_till"])
	}
}

func TestC85_IntakeDrilldownSnoozedExcludesResurfaced(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	code, body := getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, "?metric=snoozed")
	if code != http.StatusOK {
		t.Fatalf("snoozed drilldown: status = %d (body: %v)", code, body)
	}
	if body["total"] != float64(1) {
		t.Fatalf("total = %v, want 1", body["total"])
	}
	if got := intakeDrilldownTitles(t, body); !reflect.DeepEqual(got, []string{"in-snoozed"}) {
		t.Fatalf("snoozed titles = %v, want [in-snoozed]", got)
	}
	row := intakeDrilldownRowByTitle(t, body, "in-snoozed")
	if row["status"] != float64(2) || row["status_name"] != "snoozed" {
		t.Fatalf("snoozed row status = %v/%v, want 2/snoozed", row["status"], row["status_name"])
	}
	if row["snoozed_till"] == nil || row["snoozed_till"] == "" {
		t.Fatalf("snoozed row snoozed_till = %v, want the future till", row["snoozed_till"])
	}
}

func TestC85_IntakeDrilldownMissingMetric(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	// No metric at all → honest 400.
	if code, _ := getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, ""); code != http.StatusBadRequest {
		t.Fatalf("missing metric: status = %d, want 400", code)
	}
	// A group param alone does not substitute for a metric.
	if code, _ := getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, "?group=Backlog"); code != http.StatusBadRequest {
		t.Fatalf("group-only: status = %d, want 400", code)
	}
}

func TestC85_IntakeDrilldownAvgDecide(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	// avg_decide is an aggregate, not a row set → honest 400.
	code, body := getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, "?metric=avg_decide")
	if code != http.StatusBadRequest {
		t.Fatalf("avg_decide: status = %d, want 400 (body: %v)", code, body)
	}
}

func TestC85_IntakeDrilldownUnknownMetric(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	for _, m := range []string{"total", "count", "In_App", "avg_decide_seconds"} {
		if code, _ := getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, "?metric="+m); code != http.StatusBadRequest {
			t.Fatalf("metric=%s: status = %d, want 400", m, code)
		}
	}
}

func TestC85_IntakeDrilldownGroupIgnored(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	// group is meaningless for intake tiles: accepted and ignored
	// (same pattern as C84's group_by/metric ignores).
	code, body := getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, "?metric=accepted&group=Backlog")
	if code != http.StatusOK {
		t.Fatalf("metric+group: status = %d (body: %v)", code, body)
	}
	if body["total"] != float64(1) || body["metric"] != "accepted" {
		t.Fatalf("envelope = %v, want total 1 metric accepted", body)
	}
}

func TestC85_IntakeDrilldownPagination(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	// in_app has 7 rows: page 2 of per_page 2 holds 2 rows.
	code, body := getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, "?metric=in_app&per_page=2&page=2")
	if code != http.StatusOK {
		t.Fatalf("page 2: status = %d (body: %v)", code, body)
	}
	if body["total"] != float64(7) || body["per_page"] != float64(2) || body["page"] != float64(2) {
		t.Fatalf("envelope = %v, want total 7 per_page 2 page 2", body)
	}
	if len(body["items"].([]any)) != 2 {
		t.Fatalf("page 2 items = %d, want 2", len(body["items"].([]any)))
	}
	// Page 4 holds the last row; page 5 is empty but still 200.
	code, body = getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, "?metric=in_app&per_page=2&page=4")
	if code != http.StatusOK || len(body["items"].([]any)) != 1 {
		t.Fatalf("page 4: status = %d items = %v, want 200 with 1 item", code, body["items"])
	}
	code, body = getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, "?metric=in_app&per_page=2&page=5")
	if code != http.StatusOK || len(body["items"].([]any)) != 0 {
		t.Fatalf("page 5: status = %d items = %v, want 200 with 0 items", code, body["items"])
	}
	// No per_page: the C84 convention — the widget's max_rows,
	// default 10 for a widget without max_rows.
	code, body = getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, "?metric=in_app")
	if code != http.StatusOK || body["per_page"] != float64(10) {
		t.Fatalf("default per_page: status = %d envelope = %v, want per_page 10", code, body)
	}
	// An explicit per_page of 3 is honored.
	code, body = getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, "?metric=in_app&per_page=3")
	if code != http.StatusOK || body["per_page"] != float64(3) || len(body["items"].([]any)) != 3 {
		t.Fatalf("per_page=3: status = %d envelope = %v, want per_page 3 with 3 items", code, body)
	}
	// max_rows is rejected for intake_breakdown widgets (the audit
	// finding behind the flat default-10 per_page): a config carrying
	// it is a 400, not a silent fallback.
	rec := postWidgetExpect(t, fx.wd.fx.e, fx.wd.fx.adminCookie, fx.dashURL,
		`{"title":"Intake3","widget_type":"intake_breakdown","config":{"max_rows":3}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("max_rows on intake_breakdown: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestC85_IntakeDrilldownPerPageValidation(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	for _, q := range []string{"?metric=in_app&per_page=0", "?metric=in_app&per_page=101",
		"?metric=in_app&per_page=abc", "?metric=in_app&page=0", "?metric=in_app&page=x"} {
		if code, _ := getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, q); code != http.StatusBadRequest {
			t.Fatalf("query %s: status = %d, want 400", q, code)
		}
	}
}

func TestC85_IntakeFunnelCounts(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	code, body := getWidgetData(t, fx.wd.fx, fx.widgetID, fx.wd.fx.adminCookie)
	if code != http.StatusOK {
		t.Fatalf("data: status = %d (body: %v)", code, body)
	}
	intake, ok := body["intake"].(map[string]any)
	if !ok {
		t.Fatalf("no intake in body: %v", body)
	}
	// Existing six fields byte-identical in meaning.
	want := map[string]float64{
		"in_app_volume": 7, "form_volume": 1, "email_volume": 0,
		"total_accepted": 1, "total_rejected": 1,
		// New funnel fields only.
		"total_duplicate": 1, "pending": 4, "snoozed": 1,
	}
	for k, v := range want {
		if intake[k] != v {
			t.Fatalf("intake.%s = %v, want %v (intake: %v)", k, intake[k], v, intake)
		}
	}
	// avg_decide_seconds is a mean over the 3 decided rows: present
	// and non-negative (exact value is timing-dependent).
	avg, ok := intake["avg_decide_seconds"].(float64)
	if !ok || avg < 0 {
		t.Fatalf("avg_decide_seconds = %v, want a non-negative number", intake["avg_decide_seconds"])
	}
	// Funnel counts reconcile with the drill-down totals exactly.
	for metric, field := range map[string]string{
		"duplicate": "total_duplicate", "pending": "pending", "snoozed": "snoozed",
	} {
		_, dd := getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, "?metric="+metric)
		if dd["total"] != intake[field] {
			t.Fatalf("drilldown %s total = %v, want funnel %s = %v", metric, dd["total"], field, intake[field])
		}
	}
}

func TestC85_IntakeDrilldownScopeArchivedExcluded(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	// The archived project's row appears in NO metric, including
	// the broad in_app volume set.
	code, body := getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, "?metric=in_app&per_page=100")
	if code != http.StatusOK {
		t.Fatalf("in_app: status = %d (body: %v)", code, body)
	}
	if titles := intakeDrilldownTitleSet(t, body); titles["c-archived"] {
		t.Fatalf("archived project's row leaked into in_app: %v", titles)
	}
	code, body = getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, "?metric=pending&per_page=100")
	if code != http.StatusOK {
		t.Fatalf("pending: status = %d (body: %v)", code, body)
	}
	if titles := intakeDrilldownTitleSet(t, body); titles["c-archived"] {
		t.Fatalf("archived project's row leaked into pending: %v", titles)
	}
}

func TestC85_IntakeDrilldownIgnoresWidgetScoping(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	// project_ids (and filters) are deliberately ignored for
	// intake_breakdown (Plane parity, like computeIntakeBreakdown):
	// a widget scoped to project A still drills B's rows.
	wid := createWidgetHTTP(t, fx.wd.fx.e, fx.wd.fx.adminCookie, fx.dashURL,
		fmt.Sprintf(`{"title":"Scoped","widget_type":"intake_breakdown","config":{"project_ids":[%q]}}`, fx.wd.projAID))
	rec := getAuthed(t, fx.wd.fx.e, http.MethodGet, "/api/v1/widgets/"+wid+"/drilldown?metric=pending", fx.wd.fx.adminCookie)
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode scoped drilldown: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("scoped drilldown: status = %d (body: %v)", rec.Code, body)
	}
	if titles := intakeDrilldownTitleSet(t, body); !titles["b-pending"] {
		t.Fatalf("project-B row missing under a project-A-scoped widget: %v", titles)
	}
}

func TestC85_IntakeDrilldownAuthZ(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	privID := createDashboardHTTP(t, fx.wd.fx.e, fx.wd.fx.adminCookie, fx.wd.fx.wsBase,
		`{"name":"hidden","visibility":"private"}`)
	wid := createWidgetHTTP(t, fx.wd.fx.e, fx.wd.fx.adminCookie, fx.wd.fx.wsBase+"/dashboards/"+privID,
		`{"title":"ib","widget_type":"intake_breakdown","config":{}}`)
	dashURL := fx.wd.fx.wsBase + "/dashboards/" + privID
	// Private + unshared: the member gets 404 (no existence leak) —
	// even for a metric that would otherwise be valid.
	rec := getAuthed(t, fx.wd.fx.e, http.MethodGet, "/api/v1/widgets/"+wid+"/drilldown?metric=pending", fx.wd.fx.memberCookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unshared drilldown: status = %d, want 404", rec.Code)
	}
	// View-shared: the member CAN drill (read affordance).
	code, _ := putShares(t, fx.wd.fx.e, fx.wd.fx.adminCookie, dashURL,
		fmt.Sprintf(`{"shares":[{"member_id":%q,"access":"view"}]}`, fx.wd.fx.memberID))
	if code != http.StatusOK {
		t.Fatalf("PUT shares: status = %d", code)
	}
	rec = getAuthed(t, fx.wd.fx.e, http.MethodGet, "/api/v1/widgets/"+wid+"/drilldown?metric=pending", fx.wd.fx.memberCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("shared drilldown: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	// Guest: 403 like every other dashboard read.
	rec = getAuthed(t, fx.wd.fx.e, http.MethodGet, "/api/v1/widgets/"+wid+"/drilldown?metric=pending", fx.wd.fx.guestCookie)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("guest drilldown: status = %d, want 403", rec.Code)
	}
}

func TestC85_IntakeDrilldownRowShape(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	code, body := getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, "?metric=accepted")
	if code != http.StatusOK {
		t.Fatalf("accepted: status = %d (body: %v)", code, body)
	}
	row := intakeDrilldownRowByTitle(t, body, "in-accepted")
	// The intake-list payload field names, reused: id (intake row),
	// issue_id, status/status_name, snoozed_till, duplicate_to_id,
	// created_at — plus title and the {id, name} project ref from
	// the WidgetRowItem vocabulary, plus source.
	for _, k := range []string{"id", "issue_id", "title", "project", "status", "status_name", "source", "created_at"} {
		if _, ok := row[k]; !ok {
			t.Fatalf("row missing key %q (row: %v)", k, row)
		}
	}
	if row["id"] == "" || row["id"] == row["issue_id"] {
		t.Fatalf("row id = %v, issue_id = %v: want a distinct non-empty intake row id", row["id"], row["issue_id"])
	}
	if row["issue_id"] != fx.issues["in-accepted"] {
		t.Fatalf("issue_id = %v, want %s", row["issue_id"], fx.issues["in-accepted"])
	}
	if row["status"] != float64(3) || row["status_name"] != "accepted" || row["source"] != "in_app" {
		t.Fatalf("row = %v, want status 3/accepted source in_app", row)
	}
	proj := row["project"].(map[string]any)
	if proj["id"] != fx.wd.projAID || proj["name"] != "Data A" {
		t.Fatalf("project = %v, want {id: %s, name: Data A}", proj, fx.wd.projAID)
	}
	// Accepted rows carry no snoozed_till / duplicate_to_id.
	if _, ok := row["snoozed_till"]; ok {
		t.Fatalf("accepted row carries snoozed_till = %v, want absent", row["snoozed_till"])
	}
	if _, ok := row["duplicate_to_id"]; ok {
		t.Fatalf("accepted row carries duplicate_to_id = %v, want absent", row["duplicate_to_id"])
	}
}

func TestC85_IntakeCSVMetric(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	code, header, body := getIntakeCSV(t, fx, fx.wd.fx.adminCookie, "?metric=pending")
	if code != http.StatusOK {
		t.Fatalf("csv pending: status = %d (body: %s)", code, body)
	}
	if ct := header.Get("Content-Type"); ct != "text/csv" {
		t.Fatalf("Content-Type = %q, want text/csv", ct)
	}
	if header.Get("X-Result-Truncated") != "" {
		t.Fatalf("X-Result-Truncated = %q, want absent (4 rows < cap)", header.Get("X-Result-Truncated"))
	}
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) != 5 {
		t.Fatalf("csv lines = %d, want 5 (header + 4 pending rows)", len(lines))
	}
	if lines[0] != "id,issue_id,title,project,status,status_name,source,snoozed_till,duplicate_to_id,created_at" {
		t.Fatalf("csv header = %q, want the intake-row field set", lines[0])
	}
	joined := strings.Join(lines[1:], "\n")
	for _, title := range []string{"in-pending", "in-form", "in-resurfaced", "b-pending"} {
		if !strings.Contains(joined, title) {
			t.Fatalf("csv body missing pending row %q:\n%s", title, body)
		}
	}
	// The resurfaced row exports with its EFFECTIVE status (0/pending).
	for _, line := range lines[1:] {
		if strings.Contains(line, "in-resurfaced") && !strings.Contains(line, ",0,pending,") {
			t.Fatalf("resurfaced csv row = %q, want effective status 0/pending", line)
		}
	}
}

func TestC85_IntakeCSVMetricEmail(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	code, _, body := getIntakeCSV(t, fx, fx.wd.fx.adminCookie, "?metric=email")
	if code != http.StatusOK {
		t.Fatalf("csv email: status = %d (body: %s)", code, body)
	}
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) != 1 {
		t.Fatalf("csv lines = %d, want 1 (header only — honest empty)", len(lines))
	}
}

func TestC85_IntakeCSVWithoutMetricUnchanged(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	code, _, body := getIntakeCSV(t, fx, fx.wd.fx.adminCookie, "")
	if code != http.StatusOK {
		t.Fatalf("csv aggregate: status = %d (body: %s)", code, body)
	}
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) != 7 {
		t.Fatalf("csv lines = %d, want 7 (header + the six aggregate rows, funnel counts NOT added)", len(lines))
	}
	if lines[0] != "metric,value" {
		t.Fatalf("csv header = %q, want metric,value", lines[0])
	}
	if lines[1] != "in_app_volume,7" || lines[3] != "form_volume,1" {
		t.Fatalf("csv aggregate rows = %v, want in_app_volume,7 and form_volume,1", lines)
	}
}

func TestC85_IntakeCSVMetricValidation(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	for _, q := range []string{"?metric=avg_decide", "?metric=bogus"} {
		if code, _, _ := getIntakeCSV(t, fx, fx.wd.fx.adminCookie, q); code != http.StatusBadRequest {
			t.Fatalf("csv %s: status = %d, want 400", q, code)
		}
	}
	// A metric on a non-intake widget is ignored (chart aggregate
	// export unchanged), not a 400.
	bar := createWidgetHTTP(t, fx.wd.fx.e, fx.wd.fx.adminCookie, fx.dashURL,
		`{"title":"By state","widget_type":"bar","config":{"group_by":"state"}}`)
	rec := getAuthed(t, fx.wd.fx.e, http.MethodGet, "/api/v1/widgets/"+bar+"/export.csv?metric=pending", fx.wd.fx.adminCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("bar csv + metric: status = %d, want 200 (metric ignored)", rec.Code)
	}
	if !strings.HasPrefix(rec.Body.String(), "key,label,value") {
		t.Fatalf("bar csv body = %q, want the chart aggregate header", rec.Body.String())
	}
}

func TestC85_IntakeCSVMetricRequiresRead(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	privID := createDashboardHTTP(t, fx.wd.fx.e, fx.wd.fx.adminCookie, fx.wd.fx.wsBase,
		`{"name":"hidden","visibility":"private"}`)
	wid := createWidgetHTTP(t, fx.wd.fx.e, fx.wd.fx.adminCookie, fx.wd.fx.wsBase+"/dashboards/"+privID,
		`{"title":"ib","widget_type":"intake_breakdown","config":{}}`)
	rec := getAuthed(t, fx.wd.fx.e, http.MethodGet, "/api/v1/widgets/"+wid+"/export.csv?metric=pending", fx.wd.fx.memberCookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unshared metric csv: status = %d, want 404", rec.Code)
	}
}

func TestC85_IntakeCSVMetricTruncation(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	// 10,101 bulk in_app pending rows in project A via one SQL
	// statement (sequence ids far above the HTTP-created ones, so no
	// UNIQUE collision): past the 10,000-row cap the export truncates
	// and sets X-Result-Truncated.
	adminID := authedUserID(t, fx.wd.fx.e, fx.wd.fx.adminCookie)
	var intakeID string
	if err := fx.wd.fx.pool.QueryRow(context.Background(),
		`SELECT id::text FROM intake WHERE project_id = $1::uuid AND is_default`,
		fx.wd.projAID).Scan(&intakeID); err != nil {
		t.Fatalf("default intake id: %v", err)
	}
	if _, err := fx.wd.fx.pool.Exec(context.Background(),
		`INSERT INTO issues (project_id, sequence_id, name, state_id, created_by)
		 SELECT $1::uuid, 100000 + g, 'bulk-' || g, $2::uuid, $3::uuid
		   FROM generate_series(1, 10101) g`,
		fx.wd.projAID, fx.wd.backlogA, adminID); err != nil {
		t.Fatalf("bulk issues: %v", err)
	}
	if _, err := fx.wd.fx.pool.Exec(context.Background(),
		`INSERT INTO intake_issues (intake_id, issue_id, status, source)
		 SELECT $1::uuid, i.id, 0, 'in_app'
		   FROM issues i WHERE i.project_id = $2::uuid AND i.sequence_id >= 100000`,
		intakeID, fx.wd.projAID); err != nil {
		t.Fatalf("bulk intake rows: %v", err)
	}
	code, header, body := getIntakeCSV(t, fx, fx.wd.fx.adminCookie, "?metric=in_app")
	if code != http.StatusOK {
		t.Fatalf("csv in_app bulk: status = %d", code)
	}
	if header.Get("X-Result-Truncated") != "true" {
		t.Fatalf("X-Result-Truncated = %q, want true (10,108 rows > cap)", header.Get("X-Result-Truncated"))
	}
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) != 10001 {
		t.Fatalf("csv lines = %d, want 10001 (header + 10,000 capped rows)", len(lines))
	}
}

// TestOpenAPIDocShapesIntakeDrilldown pins the C85T0 API contract in
// docs/openapi.yaml: the metric/per_page drill-down params, the
// IntakeDrilldown + IntakeDrilldownItem schemas, the funnel fields on
// IntakeBreakdown, and the metric param on export.csv. If the
// implementation drifts from the pinned contract, this test fails
// first.
func TestOpenAPIDocShapesIntakeDrilldown(t *testing.T) {
	var spec struct {
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(docs.OpenAPI, &spec); err != nil {
		t.Fatalf("embedded openapi spec is not valid JSON: %v", err)
	}
	decode := func(name string, v any) {
		t.Helper()
		raw, ok := spec.Components.Schemas[name]
		if !ok {
			t.Fatalf("spec has no %s schema", name)
		}
		if err := json.Unmarshal(raw, v); err != nil {
			t.Fatalf("decode %s schema: %v", name, err)
		}
	}

	// Drill-down op: metric + per_page params alongside group/page.
	var drillOp struct {
		Summary    string `json:"summary"`
		Parameters []struct {
			Name     string `json:"name"`
			In       string `json:"in"`
			Required bool   `json:"required"`
			Schema   struct {
				Enum []string `json:"enum"`
			} `json:"schema"`
		} `json:"parameters"`
	}
	if err := json.Unmarshal(spec.Paths["/api/v1/widgets/{widget_id}/drilldown"]["get"], &drillOp); err != nil {
		t.Fatalf("decode drilldown op: %v", err)
	}
	var metricParam, perPageParam *struct {
		Name     string `json:"name"`
		In       string `json:"in"`
		Required bool   `json:"required"`
		Schema   struct {
			Enum []string `json:"enum"`
		} `json:"schema"`
	}
	for i := range drillOp.Parameters {
		p := &drillOp.Parameters[i]
		switch p.Name {
		case "metric":
			metricParam = p
		case "per_page":
			perPageParam = p
		}
	}
	if metricParam == nil || metricParam.Required || metricParam.In != "query" {
		t.Fatalf("drilldown metric param = %+v, want optional query param", metricParam)
	}
	wantMetrics := []string{"in_app", "form", "email", "accepted", "rejected", "duplicate", "pending", "snoozed"}
	if got := metricParam.Schema.Enum; !reflect.DeepEqual(got, wantMetrics) {
		t.Fatalf("drilldown metric enum = %v, want %v", got, wantMetrics)
	}
	if perPageParam == nil || perPageParam.Required || perPageParam.In != "query" {
		t.Fatalf("drilldown per_page param = %+v, want optional query param", perPageParam)
	}

	// IntakeBreakdown: the six original fields plus the funnel three.
	var intake struct {
		Props map[string]json.RawMessage `json:"properties"`
	}
	decode("IntakeBreakdown", &intake)
	for _, prop := range []string{"in_app_volume", "email_volume", "form_volume", "total_accepted",
		"total_rejected", "avg_decide_seconds", "total_duplicate", "pending", "snoozed"} {
		if _, ok := intake.Props[prop]; !ok {
			t.Fatalf("IntakeBreakdown missing %s property", prop)
		}
	}

	// IntakeDrilldown + IntakeDrilldownItem schemas with their keys.
	var dd struct {
		Props map[string]json.RawMessage `json:"properties"`
	}
	decode("IntakeDrilldown", &dd)
	for _, prop := range []string{"items", "page", "per_page", "total", "metric"} {
		if _, ok := dd.Props[prop]; !ok {
			t.Fatalf("IntakeDrilldown missing %s property", prop)
		}
	}
	var row struct {
		Props map[string]json.RawMessage `json:"properties"`
	}
	decode("IntakeDrilldownItem", &row)
	for _, prop := range []string{"id", "issue_id", "title", "project", "status", "status_name",
		"source", "snoozed_till", "duplicate_to_id", "created_at"} {
		if _, ok := row.Props[prop]; !ok {
			t.Fatalf("IntakeDrilldownItem missing %s property", prop)
		}
	}

	// export.csv op: the metric param, and a description documenting
	// the intake-row field set.
	var exportOp struct {
		Description string `json:"description"`
		Parameters  []struct {
			Name string `json:"name"`
			In   string `json:"in"`
		} `json:"parameters"`
	}
	if err := json.Unmarshal(spec.Paths["/api/v1/widgets/{widget_id}/export.csv"]["get"], &exportOp); err != nil {
		t.Fatalf("decode export op: %v", err)
	}
	foundMetric := false
	for _, p := range exportOp.Parameters {
		if p.Name == "metric" && p.In == "query" {
			foundMetric = true
		}
	}
	if !foundMetric {
		t.Fatal("export.csv op missing metric query param")
	}
	if !strings.Contains(exportOp.Description, "snoozed_till") {
		t.Fatal("export.csv description does not document the intake-row field set")
	}
}
