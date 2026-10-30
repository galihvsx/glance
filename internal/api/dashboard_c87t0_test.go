package api

// C87T0: bucket window on the intake_breakdown drill-down
// (GET /api/v1/widgets/:id/drilldown?granularity=...&bucket_start=...),
// plus the OpenAPI pins. Real test database, no skips.
//
// Fixture world: the C85 drill-down world (setupIntakeDrilldown) —
// eight live-scope rows, all created "now" unless a test moves one
// via SQL (setIntakeCreatedAt, from the C86 file): in-pending,
// in-accepted, in-rejected, in-snoozed, in-resurfaced, in-duplicate,
// in-form (all project A) and b-pending (project B), plus the
// archived project's c-archived row that must never appear. All
// bucket math is relative to the test run's now (current Monday /
// 1st / day), so the expectations hold whatever day the suite runs.
// The bucket window is a half-open UTC calendar period: day
// [start, start+1d); week [Monday, Monday+7d); month [1st, next 1st).
// A bucket without a metric lists every row created in the bucket
// (any source/status); with a metric it ANDs onto the metric
// predicate, so pending/snoozed keep their C85 EFFECTIVE-status
// definitions inside the bucket.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"glance/docs"
)

// bucketQuery builds a bucket drill-down query string from a
// granularity, a bucket-start time, and an optional metric.
func bucketQuery(granularity string, start time.Time, metric string) string {
	q := "?granularity=" + granularity + "&bucket_start=" + trendDate(start)
	if metric != "" {
		q += "&metric=" + metric
	}
	return q
}

func TestC87_BucketGranularityWithoutBucketStart(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	// One without the other → honest 400 (never a silent default).
	if code, _ := getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, "?granularity=week"); code != http.StatusBadRequest {
		t.Fatalf("granularity-only: status = %d, want 400", code)
	}
	if code, _ := getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, "?granularity=week&metric=in_app"); code != http.StatusBadRequest {
		t.Fatalf("granularity+metric, no bucket_start: status = %d, want 400", code)
	}
}

func TestC87_BucketBucketStartWithoutGranularity(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	monday := trendMonday(time.Now())
	if code, _ := getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, "?bucket_start="+trendDate(monday)); code != http.StatusBadRequest {
		t.Fatalf("bucket_start-only: status = %d, want 400", code)
	}
	if code, _ := getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, "?bucket_start="+trendDate(monday)+"&metric=in_app"); code != http.StatusBadRequest {
		t.Fatalf("bucket_start+metric, no granularity: status = %d, want 400", code)
	}
}

func TestC87_BucketUnknownGranularity(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	monday := trendMonday(time.Now())
	for _, g := range []string{"year", "Week", "fortnight", "daily"} {
		if code, _ := getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, "?granularity="+g+"&bucket_start="+trendDate(monday)); code != http.StatusBadRequest {
			t.Fatalf("granularity=%s: status = %d, want 400", g, code)
		}
	}
}

func TestC87_BucketMalformedDate(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	for _, d := range []string{"not-a-date", "2026-13-01", "2026-02-30", "2026-10-5", "2026/10/05", "20261005", ""} {
		if d == "" {
			continue // empty is the one-without-other case, covered elsewhere
		}
		if code, _ := getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, "?granularity=week&bucket_start="+d); code != http.StatusBadRequest {
			t.Fatalf("bucket_start=%s: status = %d, want 400", d, code)
		}
	}
}

func TestC87_BucketMisalignedWeekStart(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	monday := trendMonday(time.Now())
	// Tuesday through Sunday of the current week are not Mondays.
	for _, d := range []time.Time{monday.AddDate(0, 0, 1), monday.AddDate(0, 0, 3), monday.AddDate(0, 0, 6)} {
		if code, _ := getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, "?granularity=week&bucket_start="+trendDate(d)); code != http.StatusBadRequest {
			t.Fatalf("week bucket_start=%s: status = %d, want 400", trendDate(d), code)
		}
	}
}

func TestC87_BucketMisalignedMonthStart(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	month := trendMonthStart(time.Now())
	for _, d := range []time.Time{month.AddDate(0, 0, 1), month.AddDate(0, 0, 14), month.AddDate(0, 0, 27)} {
		if code, _ := getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, "?granularity=month&bucket_start="+trendDate(d)); code != http.StatusBadRequest {
			t.Fatalf("month bucket_start=%s: status = %d, want 400", trendDate(d), code)
		}
	}
	// A day bucket accepts any calendar day (no alignment rule).
	code, body := getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, "?granularity=day&bucket_start="+trendDate(month.AddDate(0, 0, 14)))
	if code != http.StatusOK {
		t.Fatalf("day bucket mid-month: status = %d (body: %v), want 200", code, body)
	}
}

func TestC87_BucketWeekBoundaries(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	monday := trendMonday(time.Now())
	prevMonday := monday.AddDate(0, 0, -7)
	// Monday 00:00 UTC lands in the NEW week; the previous Sunday
	// 23:59:59 stays in the OLD week.
	setIntakeCreatedAt(t, fx, "in-pending", monday)
	setIntakeCreatedAt(t, fx, "in-accepted", prevMonday.AddDate(0, 0, 6).Add(23*time.Hour+59*time.Minute+59*time.Second))
	code, body := getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, bucketQuery("week", monday, ""))
	if code != http.StatusOK {
		t.Fatalf("current week: status = %d (body: %v)", code, body)
	}
	if body["total"] != float64(7) {
		t.Fatalf("current week total = %v, want 7 (all live rows except in-accepted)", body["total"])
	}
	if titles := intakeDrilldownTitleSet(t, body); !titles["in-pending"] || titles["in-accepted"] {
		t.Fatalf("current week titles = %v, want in-pending without in-accepted", titles)
	}
	code, body = getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, bucketQuery("week", prevMonday, ""))
	if code != http.StatusOK {
		t.Fatalf("previous week: status = %d (body: %v)", code, body)
	}
	if body["total"] != float64(1) {
		t.Fatalf("previous week total = %v, want 1", body["total"])
	}
	if got := intakeDrilldownTitles(t, body); !reflect.DeepEqual(got, []string{"in-accepted"}) {
		t.Fatalf("previous week titles = %v, want [in-accepted]", got)
	}
}

func TestC87_BucketMonthBoundaries(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	month := trendMonthStart(time.Now())
	prevMonth := month.AddDate(0, -1, 0)
	// 1st 00:00 UTC lands in the NEW month; the previous month's
	// last day 23:59:59 stays in the OLD month.
	setIntakeCreatedAt(t, fx, "in-pending", month)
	setIntakeCreatedAt(t, fx, "in-accepted", month.Add(-time.Second))
	code, body := getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, bucketQuery("month", month, ""))
	if code != http.StatusOK {
		t.Fatalf("current month: status = %d (body: %v)", code, body)
	}
	if body["total"] != float64(7) {
		t.Fatalf("current month total = %v, want 7", body["total"])
	}
	if titles := intakeDrilldownTitleSet(t, body); !titles["in-pending"] || titles["in-accepted"] {
		t.Fatalf("current month titles = %v, want in-pending without in-accepted", titles)
	}
	code, body = getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, bucketQuery("month", prevMonth, ""))
	if code != http.StatusOK {
		t.Fatalf("previous month: status = %d (body: %v)", code, body)
	}
	if body["total"] != float64(1) || !reflect.DeepEqual(intakeDrilldownTitles(t, body), []string{"in-accepted"}) {
		t.Fatalf("previous month: total = %v titles = %v, want 1/[in-accepted]", body["total"], intakeDrilldownTitles(t, body))
	}
}

func TestC87_BucketDayBoundaries(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	today := trendDayStart(time.Now())
	// Today 00:00 UTC lands in the NEW day; yesterday 23:59:59
	// stays in the OLD day.
	setIntakeCreatedAt(t, fx, "in-pending", today)
	setIntakeCreatedAt(t, fx, "in-accepted", today.Add(-time.Second))
	code, body := getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, bucketQuery("day", today, ""))
	if code != http.StatusOK {
		t.Fatalf("today: status = %d (body: %v)", code, body)
	}
	if body["total"] != float64(7) {
		t.Fatalf("today total = %v, want 7", body["total"])
	}
	if titles := intakeDrilldownTitleSet(t, body); !titles["in-pending"] || titles["in-accepted"] {
		t.Fatalf("today titles = %v, want in-pending without in-accepted", titles)
	}
	code, body = getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, bucketQuery("day", today.AddDate(0, 0, -1), ""))
	if code != http.StatusOK {
		t.Fatalf("yesterday: status = %d (body: %v)", code, body)
	}
	if body["total"] != float64(1) || !reflect.DeepEqual(intakeDrilldownTitles(t, body), []string{"in-accepted"}) {
		t.Fatalf("yesterday: total = %v titles = %v, want 1/[in-accepted]", body["total"], intakeDrilldownTitles(t, body))
	}
}

func TestC87_BucketWithoutMetricReturnsAllSources(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	// No rows moved: every live row was created in the current
	// week (both channels, every status).
	code, body := getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, bucketQuery("week", trendMonday(time.Now()), ""))
	if code != http.StatusOK {
		t.Fatalf("current week: status = %d (body: %v)", code, body)
	}
	if body["total"] != float64(8) {
		t.Fatalf("current week total = %v, want 8 (all live rows)", body["total"])
	}
	want := map[string]bool{"in-pending": true, "in-accepted": true, "in-rejected": true,
		"in-snoozed": true, "in-resurfaced": true, "in-duplicate": true, "in-form": true, "b-pending": true}
	if got := intakeDrilldownTitleSet(t, body); !reflect.DeepEqual(got, want) {
		t.Fatalf("current week titles = %v, want %v", got, want)
	}
	// The bucket without a metric carries an empty metric echo —
	// "all rows in this bucket", not a missing-metric error.
	if body["metric"] != "" {
		t.Fatalf("metric = %v, want empty for a bucket without a metric", body["metric"])
	}
}

func TestC87_BucketMetricFormWeek(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	monday := trendMonday(time.Now())
	// in-form is the only form row, created this week.
	code, body := getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, bucketQuery("week", monday, "form"))
	if code != http.StatusOK {
		t.Fatalf("form this week: status = %d (body: %v)", code, body)
	}
	if body["total"] != float64(1) || !reflect.DeepEqual(intakeDrilldownTitles(t, body), []string{"in-form"}) {
		t.Fatalf("form this week: total = %v titles = %v, want 1/[in-form]", body["total"], intakeDrilldownTitles(t, body))
	}
	// Last week holds no form rows: the metric AND the bucket
	// compose to an honest empty list, not an error.
	code, body = getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, bucketQuery("week", monday.AddDate(0, 0, -7), "form"))
	if code != http.StatusOK {
		t.Fatalf("form last week: status = %d (body: %v)", code, body)
	}
	if body["total"] != float64(0) || len(body["items"].([]any)) != 0 {
		t.Fatalf("form last week: total = %v items = %v, want 0/empty", body["total"], body["items"])
	}
}

func TestC87_BucketMetricAcceptedMonth(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	month := trendMonthStart(time.Now())
	// in-accepted is the only accepted row, created this month.
	code, body := getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, bucketQuery("month", month, "accepted"))
	if code != http.StatusOK {
		t.Fatalf("accepted this month: status = %d (body: %v)", code, body)
	}
	if body["total"] != float64(1) || !reflect.DeepEqual(intakeDrilldownTitles(t, body), []string{"in-accepted"}) {
		t.Fatalf("accepted this month: total = %v titles = %v, want 1/[in-accepted]", body["total"], intakeDrilldownTitles(t, body))
	}
	// Last month holds no accepted rows.
	code, body = getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, bucketQuery("month", month.AddDate(0, -1, 0), "accepted"))
	if code != http.StatusOK {
		t.Fatalf("accepted last month: status = %d (body: %v)", code, body)
	}
	if body["total"] != float64(0) {
		t.Fatalf("accepted last month total = %v, want 0", body["total"])
	}
}

func TestC87_BucketPendingEffectiveStatusComposition(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	monday := trendMonday(time.Now())
	// Move only the resurfaced row into last week: it is
	// effectively pending, so last week's pending set holds exactly
	// it, and this week's pending set loses exactly it.
	setIntakeCreatedAt(t, fx, "in-resurfaced", monday.AddDate(0, 0, -7).Add(9*time.Hour))
	code, body := getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, bucketQuery("week", monday, "pending"))
	if code != http.StatusOK {
		t.Fatalf("pending this week: status = %d (body: %v)", code, body)
	}
	if body["total"] != float64(3) {
		t.Fatalf("pending this week total = %v, want 3", body["total"])
	}
	if titles := intakeDrilldownTitleSet(t, body); !reflect.DeepEqual(titles, map[string]bool{"in-pending": true, "in-form": true, "b-pending": true}) {
		t.Fatalf("pending this week titles = %v, want in-pending/in-form/b-pending", titles)
	}
	code, body = getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, bucketQuery("week", monday.AddDate(0, 0, -7), "pending"))
	if code != http.StatusOK {
		t.Fatalf("pending last week: status = %d (body: %v)", code, body)
	}
	if body["total"] != float64(1) {
		t.Fatalf("pending last week total = %v, want 1", body["total"])
	}
	row := intakeDrilldownRowByTitle(t, body, "in-resurfaced")
	if row["status"] != float64(0) || row["status_name"] != "pending" {
		t.Fatalf("resurfaced row in bucket status = %v/%v, want 0/pending", row["status"], row["status_name"])
	}
}

func TestC87_BucketSnoozedEffectiveStatusComposition(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	monday := trendMonday(time.Now())
	lastWeek := monday.AddDate(0, 0, -7).Add(9 * time.Hour)
	// Both the still-snoozed row and the resurfaced row move into
	// last week: only the still-snoozed one counts as snoozed
	// there, and this week's snoozed set empties.
	setIntakeCreatedAt(t, fx, "in-snoozed", lastWeek)
	setIntakeCreatedAt(t, fx, "in-resurfaced", lastWeek)
	code, body := getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, bucketQuery("week", monday, "snoozed"))
	if code != http.StatusOK {
		t.Fatalf("snoozed this week: status = %d (body: %v)", code, body)
	}
	if body["total"] != float64(0) {
		t.Fatalf("snoozed this week total = %v, want 0", body["total"])
	}
	code, body = getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, bucketQuery("week", monday.AddDate(0, 0, -7), "snoozed"))
	if code != http.StatusOK {
		t.Fatalf("snoozed last week: status = %d (body: %v)", code, body)
	}
	if body["total"] != float64(1) || !reflect.DeepEqual(intakeDrilldownTitles(t, body), []string{"in-snoozed"}) {
		t.Fatalf("snoozed last week: total = %v titles = %v, want 1/[in-snoozed]", body["total"], intakeDrilldownTitles(t, body))
	}
}

func TestC87_BucketArchivedProjectExcluded(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	// c-archived was created "now" (inside the current week
	// bucket) but its project is archived — it must not appear.
	code, body := getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, bucketQuery("week", trendMonday(time.Now()), ""))
	if code != http.StatusOK {
		t.Fatalf("current week: status = %d (body: %v)", code, body)
	}
	if body["total"] != float64(8) {
		t.Fatalf("current week total = %v, want 8 (archived row excluded)", body["total"])
	}
	if titles := intakeDrilldownTitleSet(t, body); titles["c-archived"] {
		t.Fatalf("archived project's row leaked into the bucket: %v", titles)
	}
	code, body = getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, bucketQuery("week", trendMonday(time.Now()), "pending"))
	if code != http.StatusOK {
		t.Fatalf("pending current week: status = %d (body: %v)", code, body)
	}
	if titles := intakeDrilldownTitleSet(t, body); titles["c-archived"] {
		t.Fatalf("archived project's row leaked into pending bucket: %v", titles)
	}
}

func TestC87_BucketOtherWorkspaceExcluded(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	// A brand-new workspace has no intake rows at all — even
	// though the fixture workspace holds live rows, none leak in.
	slug := uniqueSlug("bucket-empty")
	createWorkspaceHTTP(t, fx.wd.fx.e, fx.wd.fx.adminCookie, "Bucket Empty Co", slug)
	wsBase := "/api/v1/workspaces/" + slug
	dashID := createDashboardHTTP(t, fx.wd.fx.e, fx.wd.fx.adminCookie, wsBase, `{"name":"empty board"}`)
	wid := createWidgetHTTP(t, fx.wd.fx.e, fx.wd.fx.adminCookie, wsBase+"/dashboards/"+dashID,
		`{"title":"Intake","widget_type":"intake_breakdown","config":{}}`)
	rec := getAuthed(t, fx.wd.fx.e, http.MethodGet, "/api/v1/widgets/"+wid+"/drilldown"+bucketQuery("week", trendMonday(time.Now()), ""), fx.wd.fx.adminCookie)
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode empty bucket: %v (body: %s)", err, rec.Body.String())
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("empty bucket: status = %d (body: %v)", rec.Code, body)
	}
	if body["total"] != float64(0) || len(body["items"].([]any)) != 0 {
		t.Fatalf("empty bucket: total = %v items = %v, want 0/empty", body["total"], body["items"])
	}
}

func TestC87_BucketAuthZ(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	privID := createDashboardHTTP(t, fx.wd.fx.e, fx.wd.fx.adminCookie, fx.wd.fx.wsBase,
		`{"name":"hidden","visibility":"private"}`)
	wid := createWidgetHTTP(t, fx.wd.fx.e, fx.wd.fx.adminCookie, fx.wd.fx.wsBase+"/dashboards/"+privID,
		`{"title":"ib","widget_type":"intake_breakdown","config":{}}`)
	dashURL := fx.wd.fx.wsBase + "/dashboards/" + privID
	bucket := bucketQuery("week", trendMonday(time.Now()), "")
	// Private + unshared: the member gets 404 (no existence leak).
	rec := getAuthed(t, fx.wd.fx.e, http.MethodGet, "/api/v1/widgets/"+wid+"/drilldown"+bucket, fx.wd.fx.memberCookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unshared bucket drilldown: status = %d, want 404", rec.Code)
	}
	// AuthZ runs before bucket validation: even a garbage bucket
	// param on a private unshared widget reads 404, not 400.
	rec = getAuthed(t, fx.wd.fx.e, http.MethodGet, "/api/v1/widgets/"+wid+"/drilldown?granularity=bogus&bucket_start=2026-01-05", fx.wd.fx.memberCookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unshared garbage bucket: status = %d, want 404", rec.Code)
	}
	// View-shared: the member CAN drill a bucket.
	code, _ := putShares(t, fx.wd.fx.e, fx.wd.fx.adminCookie, dashURL,
		fmt.Sprintf(`{"shares":[{"member_id":%q,"access":"view"}]}`, fx.wd.fx.memberID))
	if code != http.StatusOK {
		t.Fatalf("PUT shares: status = %d", code)
	}
	rec = getAuthed(t, fx.wd.fx.e, http.MethodGet, "/api/v1/widgets/"+wid+"/drilldown"+bucket, fx.wd.fx.memberCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("shared bucket drilldown: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestC87_BucketPaginationTotalReflectsBucket(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	monday := trendMonday(time.Now())
	// Current-week bucket holds 8 rows; in_app inside it holds 7.
	// total is the bucket-filtered count, not the unfiltered one.
	code, body := getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, bucketQuery("week", monday, "")+"&per_page=3&page=3")
	if code != http.StatusOK {
		t.Fatalf("page 3: status = %d (body: %v)", code, body)
	}
	if body["total"] != float64(8) || body["per_page"] != float64(3) || body["page"] != float64(3) {
		t.Fatalf("envelope = %v, want total 8 per_page 3 page 3", body)
	}
	if len(body["items"].([]any)) != 2 {
		t.Fatalf("page 3 items = %d, want 2 (8 rows, 3 per page)", len(body["items"].([]any)))
	}
	code, body = getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, bucketQuery("week", monday, "in_app")+"&per_page=2&page=4")
	if code != http.StatusOK {
		t.Fatalf("in_app page 4: status = %d (body: %v)", code, body)
	}
	if body["total"] != float64(7) || len(body["items"].([]any)) != 1 {
		t.Fatalf("in_app page 4: total = %v items = %d, want 7/1", body["total"], len(body["items"].([]any)))
	}
	// A page past the end is empty but still 200, total unchanged.
	code, body = getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, bucketQuery("week", monday, "")+"&per_page=3&page=4")
	if code != http.StatusOK || len(body["items"].([]any)) != 0 || body["total"] != float64(8) {
		t.Fatalf("page 4: status = %d items = %v total = %v, want 200/empty/8", code, body["items"], body["total"])
	}
}

func TestC87_BucketEchoOnlyWhenPresent(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	monday := trendMonday(time.Now())
	code, body := getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, bucketQuery("week", monday, "in_app"))
	if code != http.StatusOK {
		t.Fatalf("bucket: status = %d (body: %v)", code, body)
	}
	if body["granularity"] != "week" || body["bucket_start"] != trendDate(monday) {
		t.Fatalf("bucket echo = %v/%v, want week/%s", body["granularity"], body["bucket_start"], trendDate(monday))
	}
	// Without a bucket, both keys are absent — existing callers'
	// payloads stay byte-identical in meaning.
	code, body = getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, "?metric=in_app")
	if code != http.StatusOK {
		t.Fatalf("no bucket: status = %d (body: %v)", code, body)
	}
	if _, ok := body["granularity"]; ok {
		t.Fatalf("granularity echoed without a bucket: %v", body)
	}
	if _, ok := body["bucket_start"]; ok {
		t.Fatalf("bucket_start echoed without a bucket: %v", body)
	}
}

func TestC87_BucketMetricStillRequiredWithoutBucket(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	// No bucket, no metric → the C85 required-metric 400 stands.
	if code, _ := getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, ""); code != http.StatusBadRequest {
		t.Fatalf("no metric no bucket: status = %d, want 400", code)
	}
	// A bucket does not rescue an aggregate or unknown metric.
	monday := trendMonday(time.Now())
	for _, q := range []string{
		bucketQuery("week", monday, "avg_decide"),
		bucketQuery("week", monday, "bogus"),
	} {
		if code, _ := getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, q); code != http.StatusBadRequest {
			t.Fatalf("query %s: status = %d, want 400", q, code)
		}
	}
}

func TestC87_GroupedDrilldownIgnoresBucketParams(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	bar := createWidgetHTTP(t, fx.wd.fx.e, fx.wd.fx.adminCookie, fx.dashURL,
		`{"title":"By state","widget_type":"bar","config":{"group_by":"state"}}`)
	// Garbage bucket params on a grouped widget: ignored, not 400.
	rec := getAuthed(t, fx.wd.fx.e, http.MethodGet, "/api/v1/widgets/"+bar+"/drilldown?group=Backlog&granularity=bogus&bucket_start=not-a-date", fx.wd.fx.adminCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("grouped + garbage bucket: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	// Well-formed bucket params are ignored the same way.
	rec = getAuthed(t, fx.wd.fx.e, http.MethodGet, "/api/v1/widgets/"+bar+"/drilldown?group=Backlog&"+bucketQuery("week", trendMonday(time.Now()), "")[1:], fx.wd.fx.adminCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("grouped + bucket: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode grouped drilldown: %v", err)
	}
	if _, ok := body["granularity"]; ok {
		t.Fatalf("grouped drilldown echoed granularity: %v", body)
	}
	if _, ok := body["bucket_start"]; ok {
		t.Fatalf("grouped drilldown echoed bucket_start: %v", body)
	}
}

func TestC87_BucketEmptyBucketHonestEmpty(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	// A bucket with no rows at all: 200 with an empty list and
	// the bucket echo — never an error, never a spinner payload.
	monday := trendMonday(time.Now())
	lastYear := time.Date(monday.Year()-1, monday.Month(), monday.Day(), 0, 0, 0, 0, time.UTC)
	// Realign to that year's Monday for the week granularity.
	lastYearMonday := lastYear.AddDate(0, 0, -int((lastYear.Weekday()+6)%7))
	code, body := getIntakeDrilldown(t, fx, fx.wd.fx.adminCookie, bucketQuery("week", lastYearMonday, ""))
	if code != http.StatusOK {
		t.Fatalf("empty bucket: status = %d (body: %v)", code, body)
	}
	if body["total"] != float64(0) || len(body["items"].([]any)) != 0 {
		t.Fatalf("empty bucket: total = %v items = %v, want 0/empty", body["total"], body["items"])
	}
	if body["granularity"] != "week" || body["bucket_start"] != trendDate(lastYearMonday) {
		t.Fatalf("empty bucket echo = %v/%v, want week/%s", body["granularity"], body["bucket_start"], trendDate(lastYearMonday))
	}
}

// TestOpenAPIDocShapesBucketDrilldown pins the C87T0 API contract in
// docs/openapi.yaml: the granularity + bucket_start drill-down
// params and the echoed fields on IntakeDrilldown. If the
// implementation drifts from the pinned contract, this test fails
// first.
func TestOpenAPIDocShapesBucketDrilldown(t *testing.T) {
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

	// Drill-down op: granularity + bucket_start params alongside
	// group/metric/per_page/page.
	var drillOp struct {
		Description string `json:"description"`
		Parameters  []struct {
			Name     string `json:"name"`
			In       string `json:"in"`
			Required bool   `json:"required"`
			Schema   struct {
				Enum   []string `json:"enum"`
				Format string   `json:"format"`
			} `json:"schema"`
		} `json:"parameters"`
	}
	if err := json.Unmarshal(spec.Paths["/api/v1/widgets/{widget_id}/drilldown"]["get"], &drillOp); err != nil {
		t.Fatalf("decode drilldown op: %v", err)
	}
	var granularityParam, bucketStartParam *struct {
		Name     string `json:"name"`
		In       string `json:"in"`
		Required bool   `json:"required"`
		Schema   struct {
			Enum   []string `json:"enum"`
			Format string   `json:"format"`
		} `json:"schema"`
	}
	for i := range drillOp.Parameters {
		p := &drillOp.Parameters[i]
		switch p.Name {
		case "granularity":
			granularityParam = p
		case "bucket_start":
			bucketStartParam = p
		}
	}
	if granularityParam == nil || granularityParam.Required || granularityParam.In != "query" {
		t.Fatalf("drilldown granularity param = %+v, want optional query param", granularityParam)
	}
	if got := granularityParam.Schema.Enum; !reflect.DeepEqual(got, []string{"day", "week", "month"}) {
		t.Fatalf("drilldown granularity enum = %v, want [day week month]", got)
	}
	if bucketStartParam == nil || bucketStartParam.Required || bucketStartParam.In != "query" {
		t.Fatalf("drilldown bucket_start param = %+v, want optional query param", bucketStartParam)
	}
	if bucketStartParam.Schema.Format != "date" {
		t.Fatalf("drilldown bucket_start format = %q, want date", bucketStartParam.Schema.Format)
	}
	if !strings.Contains(drillOp.Description, "bucket_start") {
		t.Fatal("drilldown description does not document the bucket window")
	}

	// IntakeDrilldown: the echoed bucket fields alongside the
	// existing envelope.
	var dd struct {
		Props map[string]json.RawMessage `json:"properties"`
	}
	decode("IntakeDrilldown", &dd)
	for _, prop := range []string{"items", "page", "per_page", "total", "metric", "granularity", "bucket_start"} {
		if _, ok := dd.Props[prop]; !ok {
			t.Fatalf("IntakeDrilldown missing %s property", prop)
		}
	}
}
