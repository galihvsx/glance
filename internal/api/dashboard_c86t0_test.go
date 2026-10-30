package api

// C86T0: intake_breakdown trend backend API over HTTP — the
// time-bucketed series (GET /api/v1/widgets/:id/trend) and the trend
// CSV export (GET /api/v1/widgets/:id/export.csv?trend=1), plus the
// OpenAPI pins. Real test database, no skips.
//
// Fixture world: the C85 drill-down world (setupIntakeDrilldown) —
// every live-scope row is created "now" unless a test moves it via
// SQL, so the current bucket always holds in_app 7, form 1,
// accepted 1, rejected 1, duplicate 1, and the archived project's
// row (c-archived) never appears. Tests that move rows set
// created_at explicitly and hand-compute the affected buckets;
// unmoved rows stay in the current bucket. All bucket math is
// relative to the test run's now (current Monday / 1st / day), so
// the expectations hold whatever day the suite runs.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"glance/docs"
)

// getIntakeTrend fetches the intake trend with a raw query string.
func getIntakeTrend(t *testing.T, fx *intakeDrilldownFixture, cookie *http.Cookie, query string) (int, map[string]any) {
	t.Helper()
	rec := getAuthed(t, fx.wd.fx.e, http.MethodGet, "/api/v1/widgets/"+fx.widgetID+"/trend"+query, cookie)
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode trend: %v (body: %s)", err, rec.Body.String())
	}
	return rec.Code, body
}

// getIntakeTrendCSV fetches the trend CSV export with a raw query string.
func getIntakeTrendCSV(t *testing.T, fx *intakeDrilldownFixture, cookie *http.Cookie, query string) (int, http.Header, string) {
	t.Helper()
	rec := getAuthed(t, fx.wd.fx.e, http.MethodGet, "/api/v1/widgets/"+fx.widgetID+"/export.csv"+query, cookie)
	return rec.Code, rec.Header(), rec.Body.String()
}

// trendBuckets extracts the trend's bucket list in order.
func trendBuckets(t *testing.T, body map[string]any) []map[string]any {
	t.Helper()
	raw, ok := body["buckets"].([]any)
	if !ok {
		t.Fatalf("no buckets in trend body: %v", body)
	}
	out := make([]map[string]any, 0, len(raw))
	for _, b := range raw {
		out = append(out, b.(map[string]any))
	}
	return out
}

// trendBucketByStart returns the bucket with the given bucket_start.
func trendBucketByStart(t *testing.T, body map[string]any, start string) map[string]any {
	t.Helper()
	for _, b := range trendBuckets(t, body) {
		if b["bucket_start"] == start {
			return b
		}
	}
	t.Fatalf("no bucket starting %q in body: %v", start, body)
	return nil
}

// trendBucketCounts asserts one bucket's six counts.
func trendBucketCounts(t *testing.T, b map[string]any, inApp, form, email, accepted, rejected, duplicate float64) {
	t.Helper()
	want := map[string]float64{
		"in_app": inApp, "form": form, "email": email,
		"accepted": accepted, "rejected": rejected, "duplicate": duplicate,
	}
	for k, v := range want {
		if b[k] != v {
			t.Fatalf("bucket %s: %s = %v, want %v (bucket: %v)", b["bucket_start"], k, b[k], v, b)
		}
	}
}

// trendZeroBucket asserts a bucket is all zeros.
func trendZeroBucket(t *testing.T, b map[string]any) {
	t.Helper()
	trendBucketCounts(t, b, 0, 0, 0, 0, 0, 0)
}

// trendDayStart returns the UTC midnight of ts's calendar day.
func trendDayStart(ts time.Time) time.Time {
	ts = ts.UTC()
	return time.Date(ts.Year(), ts.Month(), ts.Day(), 0, 0, 0, 0, time.UTC)
}

// trendMonday returns the Monday (UTC midnight) of ts's ISO week.
func trendMonday(ts time.Time) time.Time {
	return trendDayStart(ts).AddDate(0, 0, -int((ts.UTC().Weekday()+6)%7))
}

// trendMonthStart returns the 1st of ts's month (UTC midnight).
func trendMonthStart(ts time.Time) time.Time {
	ts = ts.UTC()
	return time.Date(ts.Year(), ts.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// trendDate formats a bucket start the way the API does.
func trendDate(ts time.Time) string {
	return ts.UTC().Format("2006-01-02")
}

// setIntakeCreatedAt moves one fixture intake row's created_at via
// SQL (the read-time bucketing is what is under test, not any
// writer — there is no HTTP path that backdates intake rows).
func setIntakeCreatedAt(t *testing.T, fx *intakeDrilldownFixture, title string, ts time.Time) {
	t.Helper()
	if _, err := fx.wd.fx.pool.Exec(context.Background(),
		`UPDATE intake_issues SET created_at = $1 WHERE issue_id = $2::uuid`, ts, fx.issues[title]); err != nil {
		t.Fatalf("move %q created_at: %v", title, err)
	}
}

func TestC86_IntakeTrendDefaultWeek12(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	code, body := getIntakeTrend(t, fx, fx.wd.fx.adminCookie, "")
	if code != http.StatusOK {
		t.Fatalf("default trend: status = %d (body: %v)", code, body)
	}
	if body["granularity"] != "week" || body["total_buckets"] != float64(12) {
		t.Fatalf("envelope = %v, want granularity week total_buckets 12", body)
	}
	buckets := trendBuckets(t, body)
	if len(buckets) != 12 {
		t.Fatalf("buckets = %d, want 12", len(buckets))
	}
	// The series ends at the current period: the last bucket is
	// this Monday.
	if buckets[11]["bucket_start"] != trendDate(trendMonday(time.Now())) {
		t.Fatalf("last bucket_start = %v, want this Monday %s", buckets[11]["bucket_start"], trendDate(trendMonday(time.Now())))
	}
	// All live rows were created now, so the current bucket holds
	// the whole live scope — and the archived row is excluded
	// (in_app 7, not 8).
	trendBucketCounts(t, buckets[11], 7, 1, 0, 1, 1, 1)
}

func TestC86_IntakeTrendDayGranularity(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	code, body := getIntakeTrend(t, fx, fx.wd.fx.adminCookie, "?granularity=day&buckets=3")
	if code != http.StatusOK {
		t.Fatalf("day trend: status = %d (body: %v)", code, body)
	}
	if body["granularity"] != "day" || body["total_buckets"] != float64(3) {
		t.Fatalf("envelope = %v, want granularity day total_buckets 3", body)
	}
	buckets := trendBuckets(t, body)
	if len(buckets) != 3 {
		t.Fatalf("buckets = %d, want 3", len(buckets))
	}
	today := trendDayStart(time.Now())
	if buckets[2]["bucket_start"] != trendDate(today) || buckets[0]["bucket_start"] != trendDate(today.AddDate(0, 0, -2)) {
		t.Fatalf("day bucket starts = %v, %v, %v, want today-2..today",
			buckets[0]["bucket_start"], buckets[1]["bucket_start"], buckets[2]["bucket_start"])
	}
	trendBucketCounts(t, buckets[2], 7, 1, 0, 1, 1, 1)
}

func TestC86_IntakeTrendMonthGranularity(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	code, body := getIntakeTrend(t, fx, fx.wd.fx.adminCookie, "?granularity=month&buckets=2")
	if code != http.StatusOK {
		t.Fatalf("month trend: status = %d (body: %v)", code, body)
	}
	buckets := trendBuckets(t, body)
	if len(buckets) != 2 {
		t.Fatalf("buckets = %d, want 2", len(buckets))
	}
	month := trendMonthStart(time.Now())
	if buckets[1]["bucket_start"] != trendDate(month) || buckets[0]["bucket_start"] != trendDate(month.AddDate(0, -1, 0)) {
		t.Fatalf("month bucket starts = %v, %v, want prev-month-1st and this-month-1st",
			buckets[0]["bucket_start"], buckets[1]["bucket_start"])
	}
	trendBucketCounts(t, buckets[1], 7, 1, 0, 1, 1, 1)
}

func TestC86_IntakeTrendWeekBucketsConsecutiveMondays(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	code, body := getIntakeTrend(t, fx, fx.wd.fx.adminCookie, "?granularity=week&buckets=5")
	if code != http.StatusOK {
		t.Fatalf("week trend: status = %d (body: %v)", code, body)
	}
	buckets := trendBuckets(t, body)
	if len(buckets) != 5 {
		t.Fatalf("buckets = %d, want 5", len(buckets))
	}
	monday := trendMonday(time.Now())
	for i, b := range buckets {
		want := trendDate(monday.AddDate(0, 0, -7*(4-i)))
		if b["bucket_start"] != want {
			t.Fatalf("bucket %d start = %v, want %s (consecutive Mondays)", i, b["bucket_start"], want)
		}
	}
}

func TestC86_IntakeTrendUnknownGranularity(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	for _, g := range []string{"year", "daily", "Week", "quarter"} {
		if code, _ := getIntakeTrend(t, fx, fx.wd.fx.adminCookie, "?granularity="+g); code != http.StatusBadRequest {
			t.Fatalf("granularity=%s: status = %d, want 400", g, code)
		}
	}
}

func TestC86_IntakeTrendBucketsOutOfRange(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	for _, q := range []string{"?buckets=0", "?buckets=53", "?buckets=-1", "?buckets=abc"} {
		if code, _ := getIntakeTrend(t, fx, fx.wd.fx.adminCookie, q); code != http.StatusBadRequest {
			t.Fatalf("query %s: status = %d, want 400", q, code)
		}
	}
}

func TestC86_IntakeTrendBucketsBoundaries1And52(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	code, body := getIntakeTrend(t, fx, fx.wd.fx.adminCookie, "?buckets=1")
	if code != http.StatusOK {
		t.Fatalf("buckets=1: status = %d (body: %v)", code, body)
	}
	if buckets := trendBuckets(t, body); len(buckets) != 1 {
		t.Fatalf("buckets=1: len = %d, want 1", len(buckets))
	}
	code, body = getIntakeTrend(t, fx, fx.wd.fx.adminCookie, "?buckets=52")
	if code != http.StatusOK {
		t.Fatalf("buckets=52: status = %d (body: %v)", code, body)
	}
	if buckets := trendBuckets(t, body); len(buckets) != 52 || body["total_buckets"] != float64(52) {
		t.Fatalf("buckets=52: len = %d total = %v, want 52/52", len(buckets), body["total_buckets"])
	}
}

func TestC86_IntakeTrendMondayBoundary(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	monday := trendMonday(time.Now())
	prevMonday := monday.AddDate(0, 0, -7)
	// Monday 00:00 UTC lands in the NEW week; the previous Sunday
	// 23:59:59 stays in the OLD week.
	setIntakeCreatedAt(t, fx, "in-pending", monday)
	setIntakeCreatedAt(t, fx, "in-accepted", prevMonday.AddDate(0, 0, 6).Add(23*time.Hour+59*time.Minute+59*time.Second))
	code, body := getIntakeTrend(t, fx, fx.wd.fx.adminCookie, "?granularity=week&buckets=2")
	if code != http.StatusOK {
		t.Fatalf("week boundary: status = %d (body: %v)", code, body)
	}
	buckets := trendBuckets(t, body)
	if buckets[0]["bucket_start"] != trendDate(prevMonday) || buckets[1]["bucket_start"] != trendDate(monday) {
		t.Fatalf("bucket starts = %v, %v, want %s and %s",
			buckets[0]["bucket_start"], buckets[1]["bucket_start"], trendDate(prevMonday), trendDate(monday))
	}
	trendBucketCounts(t, buckets[0], 1, 0, 0, 1, 0, 0)
	// Current week: the other six live rows (in-pending plus the
	// four other in_app rows plus b-pending = 6 in_app, plus in-form).
	trendBucketCounts(t, buckets[1], 6, 1, 0, 0, 1, 1)
}

func TestC86_IntakeTrendMonthBoundary(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	month := trendMonthStart(time.Now())
	prevMonth := month.AddDate(0, -1, 0)
	// 1st 00:00 UTC lands in the NEW month; the previous month's
	// last day 23:59:59 stays in the OLD month.
	setIntakeCreatedAt(t, fx, "in-pending", month)
	setIntakeCreatedAt(t, fx, "in-accepted", month.Add(-time.Second))
	code, body := getIntakeTrend(t, fx, fx.wd.fx.adminCookie, "?granularity=month&buckets=2")
	if code != http.StatusOK {
		t.Fatalf("month boundary: status = %d (body: %v)", code, body)
	}
	buckets := trendBuckets(t, body)
	if buckets[0]["bucket_start"] != trendDate(prevMonth) || buckets[1]["bucket_start"] != trendDate(month) {
		t.Fatalf("bucket starts = %v, %v, want %s and %s",
			buckets[0]["bucket_start"], buckets[1]["bucket_start"], trendDate(prevMonth), trendDate(month))
	}
	trendBucketCounts(t, buckets[0], 1, 0, 0, 1, 0, 0)
	trendBucketCounts(t, buckets[1], 6, 1, 0, 0, 1, 1)
}

func TestC86_IntakeTrendDayBoundary(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	today := trendDayStart(time.Now())
	setIntakeCreatedAt(t, fx, "in-pending", today)
	setIntakeCreatedAt(t, fx, "in-accepted", today.Add(-time.Second))
	code, body := getIntakeTrend(t, fx, fx.wd.fx.adminCookie, "?granularity=day&buckets=2")
	if code != http.StatusOK {
		t.Fatalf("day boundary: status = %d (body: %v)", code, body)
	}
	buckets := trendBuckets(t, body)
	if buckets[0]["bucket_start"] != trendDate(today.AddDate(0, 0, -1)) || buckets[1]["bucket_start"] != trendDate(today) {
		t.Fatalf("bucket starts = %v, %v, want yesterday and today",
			buckets[0]["bucket_start"], buckets[1]["bucket_start"])
	}
	trendBucketCounts(t, buckets[0], 1, 0, 0, 1, 0, 0)
	trendBucketCounts(t, buckets[1], 6, 1, 0, 0, 1, 1)
}

func TestC86_IntakeTrendPerBucketCountsWeek4(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	monday := trendMonday(time.Now())
	midday := func(m time.Time) time.Time { return m.Add(9 * time.Hour) }
	setIntakeCreatedAt(t, fx, "in-duplicate", midday(monday.AddDate(0, 0, -21)))
	setIntakeCreatedAt(t, fx, "in-accepted", midday(monday.AddDate(0, 0, -14)))
	setIntakeCreatedAt(t, fx, "in-rejected", midday(monday.AddDate(0, 0, -7)))
	code, body := getIntakeTrend(t, fx, fx.wd.fx.adminCookie, "?granularity=week&buckets=4")
	if code != http.StatusOK {
		t.Fatalf("week 4: status = %d (body: %v)", code, body)
	}
	buckets := trendBuckets(t, body)
	if len(buckets) != 4 {
		t.Fatalf("buckets = %d, want 4", len(buckets))
	}
	trendBucketCounts(t, buckets[0], 1, 0, 0, 0, 0, 1)
	trendBucketCounts(t, buckets[1], 1, 0, 0, 1, 0, 0)
	trendBucketCounts(t, buckets[2], 1, 0, 0, 0, 1, 0)
	// Current week: in-pending, in-snoozed, in-resurfaced,
	// b-pending (4 in_app) plus in-form (1 form).
	trendBucketCounts(t, buckets[3], 4, 1, 0, 0, 0, 0)
}

func TestC86_IntakeTrendDecidedByCreationBucket(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	// in-accepted was triaged THIS month (triage ran at fixture
	// time) but is created LAST month: it must count in last
	// month's accepted bucket, not this month's.
	prevMonth := trendMonthStart(time.Now()).AddDate(0, -1, 0)
	setIntakeCreatedAt(t, fx, "in-accepted", prevMonth.AddDate(0, 0, 14).Add(10*time.Hour))
	code, body := getIntakeTrend(t, fx, fx.wd.fx.adminCookie, "?granularity=month&buckets=2")
	if code != http.StatusOK {
		t.Fatalf("decided-by-creation: status = %d (body: %v)", code, body)
	}
	buckets := trendBuckets(t, body)
	trendBucketCounts(t, buckets[0], 1, 0, 0, 1, 0, 0)
	trendBucketCounts(t, buckets[1], 6, 1, 0, 0, 1, 1)
}

func TestC86_IntakeTrendEmailAllZeros(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	code, body := getIntakeTrend(t, fx, fx.wd.fx.adminCookie, "")
	if code != http.StatusOK {
		t.Fatalf("email zeros: status = %d (body: %v)", code, body)
	}
	for _, b := range trendBuckets(t, body) {
		if b["email"] != float64(0) {
			t.Fatalf("bucket %s: email = %v, want 0 (honest zero, never faked)", b["bucket_start"], b["email"])
		}
	}
}

func TestC86_IntakeTrendEmptyBucketsZeroNoGaps(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	code, body := getIntakeTrend(t, fx, fx.wd.fx.adminCookie, "")
	if code != http.StatusOK {
		t.Fatalf("empty buckets: status = %d (body: %v)", code, body)
	}
	buckets := trendBuckets(t, body)
	// All rows are in the current bucket, so the 11 earlier
	// buckets must render as zeros — no gaps, a continuous line.
	for _, b := range buckets[:11] {
		trendZeroBucket(t, b)
	}
	if buckets[11]["in_app"] == float64(0) {
		t.Fatalf("current bucket is zero, want the live rows: %v", buckets[11])
	}
}

func TestC86_IntakeTrendEmptyWorkspaceExcludesOtherWorkspace(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	// A brand-new workspace has no intake rows at all — even
	// though the fixture workspace holds live rows, none leak in.
	slug := uniqueSlug("trend-empty")
	createWorkspaceHTTP(t, fx.wd.fx.e, fx.wd.fx.adminCookie, "Trend Empty Co", slug)
	wsBase := "/api/v1/workspaces/" + slug
	dashID := createDashboardHTTP(t, fx.wd.fx.e, fx.wd.fx.adminCookie, wsBase, `{"name":"empty board"}`)
	wid := createWidgetHTTP(t, fx.wd.fx.e, fx.wd.fx.adminCookie, wsBase+"/dashboards/"+dashID,
		`{"title":"Intake","widget_type":"intake_breakdown","config":{}}`)
	rec := getAuthed(t, fx.wd.fx.e, http.MethodGet, "/api/v1/widgets/"+wid+"/trend", fx.wd.fx.adminCookie)
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode empty trend: %v (body: %s)", err, rec.Body.String())
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("empty trend: status = %d (body: %v)", rec.Code, body)
	}
	buckets := trendBuckets(t, body)
	if len(buckets) != 12 {
		t.Fatalf("empty trend buckets = %d, want 12", len(buckets))
	}
	for _, b := range buckets {
		trendZeroBucket(t, b)
	}
}

func TestC86_IntakeTrendScopeArchivedExcluded(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	// One bucket only: the archived project's row must not appear
	// in any count (in_app 7, not 8).
	code, body := getIntakeTrend(t, fx, fx.wd.fx.adminCookie, "?buckets=1")
	if code != http.StatusOK {
		t.Fatalf("archived scope: status = %d (body: %v)", code, body)
	}
	buckets := trendBuckets(t, body)
	if len(buckets) != 1 {
		t.Fatalf("buckets = %d, want 1", len(buckets))
	}
	trendBucketCounts(t, buckets[0], 7, 1, 0, 1, 1, 1)
}

func TestC86_IntakeTrendIgnoresWidgetScoping(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	// project_ids (and filters) are deliberately ignored for
	// intake_breakdown: a widget scoped to project A still trends
	// B's rows (in_app 7 includes b-pending).
	wid := createWidgetHTTP(t, fx.wd.fx.e, fx.wd.fx.adminCookie, fx.dashURL,
		fmt.Sprintf(`{"title":"Scoped","widget_type":"intake_breakdown","config":{"project_ids":[%q]}}`, fx.wd.projAID))
	rec := getAuthed(t, fx.wd.fx.e, http.MethodGet, "/api/v1/widgets/"+wid+"/trend?buckets=1", fx.wd.fx.adminCookie)
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode scoped trend: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("scoped trend: status = %d (body: %v)", rec.Code, body)
	}
	buckets := trendBuckets(t, body)
	trendBucketCounts(t, buckets[0], 7, 1, 0, 1, 1, 1)
}

func TestC86_IntakeTrendAuthZ(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	privID := createDashboardHTTP(t, fx.wd.fx.e, fx.wd.fx.adminCookie, fx.wd.fx.wsBase,
		`{"name":"hidden","visibility":"private"}`)
	wid := createWidgetHTTP(t, fx.wd.fx.e, fx.wd.fx.adminCookie, fx.wd.fx.wsBase+"/dashboards/"+privID,
		`{"title":"ib","widget_type":"intake_breakdown","config":{}}`)
	dashURL := fx.wd.fx.wsBase + "/dashboards/" + privID
	// Private + unshared: 404 (no existence leak), even for a
	// request that would otherwise be valid.
	rec := getAuthed(t, fx.wd.fx.e, http.MethodGet, "/api/v1/widgets/"+wid+"/trend", fx.wd.fx.memberCookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unshared trend: status = %d, want 404", rec.Code)
	}
	// View-shared: the member CAN fetch the trend.
	code, _ := putShares(t, fx.wd.fx.e, fx.wd.fx.adminCookie, dashURL,
		fmt.Sprintf(`{"shares":[{"member_id":%q,"access":"view"}]}`, fx.wd.fx.memberID))
	if code != http.StatusOK {
		t.Fatalf("PUT shares: status = %d", code)
	}
	rec = getAuthed(t, fx.wd.fx.e, http.MethodGet, "/api/v1/widgets/"+wid+"/trend", fx.wd.fx.memberCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("shared trend: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	// Guest: 403 like every other dashboard read.
	rec = getAuthed(t, fx.wd.fx.e, http.MethodGet, "/api/v1/widgets/"+wid+"/trend", fx.wd.fx.guestCookie)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("guest trend: status = %d, want 403", rec.Code)
	}
}

func TestC86_IntakeTrendNonIntakeWidget(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	bar := createWidgetHTTP(t, fx.wd.fx.e, fx.wd.fx.adminCookie, fx.dashURL,
		`{"title":"By state","widget_type":"bar","config":{"group_by":"state"}}`)
	rec := getAuthed(t, fx.wd.fx.e, http.MethodGet, "/api/v1/widgets/"+bar+"/trend", fx.wd.fx.adminCookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bar trend: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestC86_IntakeTrendResponseShape(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	code, body := getIntakeTrend(t, fx, fx.wd.fx.adminCookie, "?buckets=2")
	if code != http.StatusOK {
		t.Fatalf("shape: status = %d (body: %v)", code, body)
	}
	for _, k := range []string{"granularity", "buckets", "total_buckets"} {
		if _, ok := body[k]; !ok {
			t.Fatalf("trend missing top-level key %q (body: %v)", k, body)
		}
	}
	// Bucket keys are exactly the documented field set — no
	// synonyms, no extra fields.
	wantKeys := []string{"bucket_start", "in_app", "form", "email", "accepted", "rejected", "duplicate"}
	for _, b := range trendBuckets(t, body) {
		if len(b) != len(wantKeys) {
			t.Fatalf("bucket keys = %v, want exactly %v", b, wantKeys)
		}
		for _, k := range wantKeys {
			if _, ok := b[k]; !ok {
				t.Fatalf("bucket missing key %q (bucket: %v)", k, b)
			}
		}
		if _, ok := b["bucket_start"].(string); !ok {
			t.Fatalf("bucket_start = %v, want a string date", b["bucket_start"])
		}
	}
}

// trendBucketCSVRow renders a decoded JSON bucket as its CSV row,
// for the JSON↔CSV reconciliation test.
func trendBucketCSVRow(b map[string]any) string {
	take := func(k string) string {
		return strconv.Itoa(int(b[k].(float64)))
	}
	return strings.Join([]string{b["bucket_start"].(string),
		take("in_app"), take("form"), take("email"),
		take("accepted"), take("rejected"), take("duplicate")}, ",")
}

func TestC86_IntakeCSVTrendMatchesJSON(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	code, jsonBody := getIntakeTrend(t, fx, fx.wd.fx.adminCookie, "")
	if code != http.StatusOK {
		t.Fatalf("json trend: status = %d (body: %v)", code, jsonBody)
	}
	code, header, body := getIntakeTrendCSV(t, fx, fx.wd.fx.adminCookie, "?trend=1")
	if code != http.StatusOK {
		t.Fatalf("csv trend: status = %d (body: %s)", code, body)
	}
	if ct := header.Get("Content-Type"); ct != "text/csv" {
		t.Fatalf("Content-Type = %q, want text/csv", ct)
	}
	if header.Get("X-Result-Truncated") != "" {
		t.Fatalf("X-Result-Truncated = %q, want absent (12 rows < cap)", header.Get("X-Result-Truncated"))
	}
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) != 13 {
		t.Fatalf("csv lines = %d, want 13 (header + 12 buckets)", len(lines))
	}
	if lines[0] != "bucket_start,in_app,form,email,accepted,rejected,duplicate" {
		t.Fatalf("csv header = %q, want the trend column set", lines[0])
	}
	buckets := trendBuckets(t, jsonBody)
	for i, b := range buckets {
		if lines[i+1] != trendBucketCSVRow(b) {
			t.Fatalf("csv row %d = %q, want %q (JSON series)", i, lines[i+1], trendBucketCSVRow(b))
		}
	}
}

func TestC86_IntakeCSVTrendDayBuckets3(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	code, _, body := getIntakeTrendCSV(t, fx, fx.wd.fx.adminCookie, "?trend=1&granularity=day&buckets=3")
	if code != http.StatusOK {
		t.Fatalf("csv day trend: status = %d (body: %s)", code, body)
	}
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) != 4 {
		t.Fatalf("csv lines = %d, want 4 (header + 3 buckets)", len(lines))
	}
	if lines[0] != "bucket_start,in_app,form,email,accepted,rejected,duplicate" {
		t.Fatalf("csv header = %q, want the trend column set", lines[0])
	}
	// The last row is today, carrying the whole live scope.
	if !strings.HasPrefix(lines[3], trendDate(trendDayStart(time.Now()))+",7,1,0,1,1,1") {
		t.Fatalf("csv last row = %q, want today's bucket with the live totals", lines[3])
	}
}

func TestC86_IntakeCSVTrendValidation(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	for _, q := range []string{"?trend=1&granularity=bogus", "?trend=1&buckets=99",
		"?trend=1&buckets=0", "?trend=1&buckets=abc"} {
		if code, _, _ := getIntakeTrendCSV(t, fx, fx.wd.fx.adminCookie, q); code != http.StatusBadRequest {
			t.Fatalf("csv %s: status = %d, want 400", q, code)
		}
	}
}

func TestC86_IntakeCSVTrendNonIntake(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	bar := createWidgetHTTP(t, fx.wd.fx.e, fx.wd.fx.adminCookie, fx.dashURL,
		`{"title":"By state","widget_type":"bar","config":{"group_by":"state"}}`)
	rec := getAuthed(t, fx.wd.fx.e, http.MethodGet, "/api/v1/widgets/"+bar+"/export.csv?trend=1", fx.wd.fx.adminCookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bar csv trend: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestC86_IntakeCSVWithoutTrendUnchanged(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	code, _, body := getIntakeTrendCSV(t, fx, fx.wd.fx.adminCookie, "")
	if code != http.StatusOK {
		t.Fatalf("csv aggregate: status = %d (body: %s)", code, body)
	}
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) != 7 {
		t.Fatalf("csv lines = %d, want 7 (header + the six aggregate rows)", len(lines))
	}
	if lines[0] != "metric,value" {
		t.Fatalf("csv header = %q, want metric,value", lines[0])
	}
	if lines[1] != "in_app_volume,7" || lines[3] != "form_volume,1" {
		t.Fatalf("csv aggregate rows = %v, want in_app_volume,7 and form_volume,1", lines)
	}
}

func TestC86_IntakeCSVTrendRequiresRead(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	privID := createDashboardHTTP(t, fx.wd.fx.e, fx.wd.fx.adminCookie, fx.wd.fx.wsBase,
		`{"name":"hidden","visibility":"private"}`)
	wid := createWidgetHTTP(t, fx.wd.fx.e, fx.wd.fx.adminCookie, fx.wd.fx.wsBase+"/dashboards/"+privID,
		`{"title":"ib","widget_type":"intake_breakdown","config":{}}`)
	rec := getAuthed(t, fx.wd.fx.e, http.MethodGet, "/api/v1/widgets/"+wid+"/export.csv?trend=1", fx.wd.fx.memberCookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unshared trend csv: status = %d, want 404", rec.Code)
	}
}

// TestOpenAPIDocShapesIntakeTrend pins the C86T0 API contract in
// docs/openapi.yaml: the trend path + granularity/buckets params,
// the IntakeTrend + IntakeTrendBucket schemas, and the trend params
// on export.csv. If the implementation drifts from the pinned
// contract, this test fails first.
func TestOpenAPIDocShapesIntakeTrend(t *testing.T) {
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

	// Trend op: granularity enum + buckets range/default.
	var trendOp struct {
		Summary    string `json:"summary"`
		Parameters []struct {
			Name     string `json:"name"`
			In       string `json:"in"`
			Required bool   `json:"required"`
			Schema   struct {
				Type    string   `json:"type"`
				Enum    []string `json:"enum"`
				Minimum *int     `json:"minimum"`
				Maximum *int     `json:"maximum"`
				Default any      `json:"default"`
			} `json:"schema"`
		} `json:"parameters"`
	}
	raw, ok := spec.Paths["/api/v1/widgets/{widget_id}/trend"]
	if !ok {
		t.Fatal("spec has no /api/v1/widgets/{widget_id}/trend path")
	}
	if err := json.Unmarshal(raw["get"], &trendOp); err != nil {
		t.Fatalf("decode trend op: %v", err)
	}
	if trendOp.Summary == "" {
		t.Fatal("trend op missing summary")
	}
	var granParam, bucketsParam *struct {
		Name     string `json:"name"`
		In       string `json:"in"`
		Required bool   `json:"required"`
		Schema   struct {
			Type    string   `json:"type"`
			Enum    []string `json:"enum"`
			Minimum *int     `json:"minimum"`
			Maximum *int     `json:"maximum"`
			Default any      `json:"default"`
		} `json:"schema"`
	}
	for i := range trendOp.Parameters {
		p := &trendOp.Parameters[i]
		switch p.Name {
		case "granularity":
			granParam = p
		case "buckets":
			bucketsParam = p
		}
	}
	if granParam == nil || granParam.Required || granParam.In != "query" {
		t.Fatalf("trend granularity param = %+v, want optional query param", granParam)
	}
	if want := []string{"day", "week", "month"}; !reflect.DeepEqual(granParam.Schema.Enum, want) {
		t.Fatalf("trend granularity enum = %v, want %v", granParam.Schema.Enum, want)
	}
	if bucketsParam == nil || bucketsParam.Required || bucketsParam.In != "query" {
		t.Fatalf("trend buckets param = %+v, want optional query param", bucketsParam)
	}
	if bucketsParam.Schema.Minimum == nil || *bucketsParam.Schema.Minimum != 1 ||
		bucketsParam.Schema.Maximum == nil || *bucketsParam.Schema.Maximum != 52 {
		t.Fatalf("trend buckets range = %v-%v, want 1-52", bucketsParam.Schema.Minimum, bucketsParam.Schema.Maximum)
	}

	// IntakeTrend + IntakeTrendBucket schemas with their keys.
	var trend struct {
		Props map[string]json.RawMessage `json:"properties"`
	}
	decode("IntakeTrend", &trend)
	for _, prop := range []string{"granularity", "buckets", "total_buckets"} {
		if _, ok := trend.Props[prop]; !ok {
			t.Fatalf("IntakeTrend missing %s property", prop)
		}
	}
	var bucket struct {
		Props map[string]json.RawMessage `json:"properties"`
	}
	decode("IntakeTrendBucket", &bucket)
	for _, prop := range []string{"bucket_start", "in_app", "form", "email", "accepted", "rejected", "duplicate"} {
		if _, ok := bucket.Props[prop]; !ok {
			t.Fatalf("IntakeTrendBucket missing %s property", prop)
		}
	}

	// export.csv op: trend + granularity + buckets params, and a
	// description documenting the trend column set.
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
	found := map[string]bool{}
	for _, p := range exportOp.Parameters {
		if p.In == "query" {
			found[p.Name] = true
		}
	}
	for _, name := range []string{"trend", "granularity", "buckets"} {
		if !found[name] {
			t.Fatalf("export.csv op missing %s query param", name)
		}
	}
	if !strings.Contains(exportOp.Description, "bucket_start") {
		t.Fatal("export.csv description does not document the trend column set")
	}
}
