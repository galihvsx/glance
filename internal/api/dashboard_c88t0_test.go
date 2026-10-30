package api

// C88T0: bucket params on the intake CSV export
// (GET /api/v1/widgets/:id/export.csv?granularity=...&bucket_start=...),
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
// The bucket window is the SAME half-open UTC calendar period the
// C87 drill-down uses (parseIntakeDrilldownBucket, reused verbatim,
// not reimplemented): day [start, start+1d); week [Monday,
// Monday+7d); month [1st, next 1st). A bucket without a metric
// exports every row created in the bucket (any source/status);
// with a metric it ANDs onto the metric predicate, so
// pending/snoozed keep their C85 EFFECTIVE-status definitions
// inside the bucket. The trend path (?trend=1) never reads the
// bucket params; non-intake and grouped widgets ignore them
// (no 400 on the ignored param, C87 convention).

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"glance/docs"
)

// csvTitles extracts the title column (3rd field) of each data row
// of an intake-row CSV body. Fixture titles contain no commas, so a
// plain split is exact here.
func csvTitles(t *testing.T, body string) []string {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) == 0 {
		t.Fatal("empty csv body")
	}
	if lines[0] != "id,issue_id,title,project,status,status_name,source,snoozed_till,duplicate_to_id,created_at" {
		t.Fatalf("csv header = %q, want the intake-row field set", lines[0])
	}
	out := make([]string, 0, len(lines)-1)
	for _, line := range lines[1:] {
		fields := strings.Split(line, ",")
		if len(fields) < 3 {
			t.Fatalf("csv row = %q, want at least 3 fields", line)
		}
		out = append(out, fields[2])
	}
	return out
}

func csvTitleSet(t *testing.T, body string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, title := range csvTitles(t, body) {
		out[title] = true
	}
	return out
}

func TestC88_BucketGranularityWithoutBucketStart(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	// One without the other → honest 400 (never a silent default).
	if code, _, _ := getIntakeCSV(t, fx, fx.wd.fx.adminCookie, "?granularity=week"); code != http.StatusBadRequest {
		t.Fatalf("granularity-only: status = %d, want 400", code)
	}
	if code, _, _ := getIntakeCSV(t, fx, fx.wd.fx.adminCookie, "?granularity=week&metric=in_app"); code != http.StatusBadRequest {
		t.Fatalf("granularity+metric, no bucket_start: status = %d, want 400", code)
	}
}

func TestC88_BucketBucketStartWithoutGranularity(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	monday := trendMonday(time.Now())
	if code, _, _ := getIntakeCSV(t, fx, fx.wd.fx.adminCookie, "?bucket_start="+trendDate(monday)); code != http.StatusBadRequest {
		t.Fatalf("bucket_start-only: status = %d, want 400", code)
	}
	if code, _, _ := getIntakeCSV(t, fx, fx.wd.fx.adminCookie, "?bucket_start="+trendDate(monday)+"&metric=in_app"); code != http.StatusBadRequest {
		t.Fatalf("bucket_start+metric, no granularity: status = %d, want 400", code)
	}
}

func TestC88_BucketUnknownGranularity(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	monday := trendMonday(time.Now())
	for _, g := range []string{"year", "Week", "fortnight", "daily"} {
		if code, _, _ := getIntakeCSV(t, fx, fx.wd.fx.adminCookie, "?granularity="+g+"&bucket_start="+trendDate(monday)); code != http.StatusBadRequest {
			t.Fatalf("granularity=%s: status = %d, want 400", g, code)
		}
	}
}

func TestC88_BucketMalformedDate(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	for _, d := range []string{"not-a-date", "2026-13-01", "2026-02-30", "2026-10-5", "2026/10/05", "20261005"} {
		if code, _, _ := getIntakeCSV(t, fx, fx.wd.fx.adminCookie, "?granularity=week&bucket_start="+d); code != http.StatusBadRequest {
			t.Fatalf("bucket_start=%s: status = %d, want 400", d, code)
		}
	}
}

func TestC88_BucketMisalignedWeekStart(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	monday := trendMonday(time.Now())
	// Tuesday through Sunday of the current week are not Mondays.
	for _, d := range []time.Time{monday.AddDate(0, 0, 1), monday.AddDate(0, 0, 3), monday.AddDate(0, 0, 6)} {
		if code, _, _ := getIntakeCSV(t, fx, fx.wd.fx.adminCookie, "?granularity=week&bucket_start="+trendDate(d)); code != http.StatusBadRequest {
			t.Fatalf("week bucket_start=%s: status = %d, want 400", trendDate(d), code)
		}
	}
}

func TestC88_BucketMisalignedMonthStart(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	month := trendMonthStart(time.Now())
	for _, d := range []time.Time{month.AddDate(0, 0, 1), month.AddDate(0, 0, 14), month.AddDate(0, 0, 27)} {
		if code, _, _ := getIntakeCSV(t, fx, fx.wd.fx.adminCookie, "?granularity=month&bucket_start="+trendDate(d)); code != http.StatusBadRequest {
			t.Fatalf("month bucket_start=%s: status = %d, want 400", trendDate(d), code)
		}
	}
	// A day bucket accepts any calendar day (no alignment rule).
	code, _, _ := getIntakeCSV(t, fx, fx.wd.fx.adminCookie, "?granularity=day&bucket_start="+trendDate(month.AddDate(0, 0, 14)))
	if code != http.StatusOK {
		t.Fatalf("day bucket mid-month: status = %d, want 200", code)
	}
}

func TestC88_BucketInvalidMetric(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	monday := trendMonday(time.Now())
	for _, q := range []string{
		bucketQuery("week", monday, "avg_decide"),
		bucketQuery("week", monday, "bogus"),
	} {
		if code, _, _ := getIntakeCSV(t, fx, fx.wd.fx.adminCookie, q); code != http.StatusBadRequest {
			t.Fatalf("csv %s: status = %d, want 400", q, code)
		}
	}
}

func TestC88_BucketWithoutMetricReturnsAllSources(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	// No rows moved: every live row was created in the current
	// week (both channels, every status).
	code, header, body := getIntakeCSV(t, fx, fx.wd.fx.adminCookie, bucketQuery("week", trendMonday(time.Now()), ""))
	if code != http.StatusOK {
		t.Fatalf("bucket csv: status = %d (body: %s)", code, body)
	}
	if header.Get("X-Result-Truncated") != "" {
		t.Fatalf("X-Result-Truncated = %q, want absent (8 rows < cap)", header.Get("X-Result-Truncated"))
	}
	want := map[string]bool{"in-pending": true, "in-accepted": true, "in-rejected": true,
		"in-snoozed": true, "in-resurfaced": true, "in-duplicate": true, "in-form": true, "b-pending": true}
	if got := csvTitleSet(t, body); len(got) != len(want) {
		t.Fatalf("bucket csv titles = %v, want %v", got, want)
	} else {
		for title := range want {
			if !got[title] {
				t.Fatalf("bucket csv titles = %v, want %v", got, want)
			}
		}
	}
}

func TestC88_BucketWithMetricComposes(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	monday := trendMonday(time.Now())
	code, _, body := getIntakeCSV(t, fx, fx.wd.fx.adminCookie, bucketQuery("week", monday, "in_app"))
	if code != http.StatusOK {
		t.Fatalf("bucket+in_app csv: status = %d (body: %s)", code, body)
	}
	titles := csvTitleSet(t, body)
	if len(titles) != 7 || titles["in-form"] {
		t.Fatalf("bucket+in_app titles = %v, want the 7 in_app rows without in-form", titles)
	}
	code, _, body = getIntakeCSV(t, fx, fx.wd.fx.adminCookie, bucketQuery("week", monday, "form"))
	if code != http.StatusOK {
		t.Fatalf("bucket+form csv: status = %d (body: %s)", code, body)
	}
	if titles := csvTitles(t, body); len(titles) != 1 || titles[0] != "in-form" {
		t.Fatalf("bucket+form titles = %v, want [in-form]", titles)
	}
}

func TestC88_BucketWeekBoundaries(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	monday := trendMonday(time.Now())
	prevMonday := monday.AddDate(0, 0, -7)
	// Monday 00:00 UTC lands in the NEW week; the previous Sunday
	// 23:59:59 stays in the OLD week.
	setIntakeCreatedAt(t, fx, "in-pending", monday)
	setIntakeCreatedAt(t, fx, "in-accepted", prevMonday.AddDate(0, 0, 6).Add(23*time.Hour+59*time.Minute+59*time.Second))
	code, _, body := getIntakeCSV(t, fx, fx.wd.fx.adminCookie, bucketQuery("week", monday, ""))
	if code != http.StatusOK {
		t.Fatalf("current week csv: status = %d (body: %s)", code, body)
	}
	titles := csvTitleSet(t, body)
	if len(titles) != 7 || !titles["in-pending"] || titles["in-accepted"] {
		t.Fatalf("current week titles = %v, want 7 rows with in-pending, without in-accepted", titles)
	}
	code, _, body = getIntakeCSV(t, fx, fx.wd.fx.adminCookie, bucketQuery("week", prevMonday, ""))
	if code != http.StatusOK {
		t.Fatalf("previous week csv: status = %d (body: %s)", code, body)
	}
	if titles := csvTitles(t, body); len(titles) != 1 || titles[0] != "in-accepted" {
		t.Fatalf("previous week titles = %v, want [in-accepted]", titles)
	}
}

func TestC88_BucketMonthBoundaries(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	month := trendMonthStart(time.Now())
	prevMonth := month.AddDate(0, -1, 0)
	// 1st 00:00 UTC lands in the NEW month; the previous month's
	// last day 23:59:59 stays in the OLD month.
	setIntakeCreatedAt(t, fx, "in-pending", month)
	setIntakeCreatedAt(t, fx, "in-accepted", month.Add(-time.Second))
	code, _, body := getIntakeCSV(t, fx, fx.wd.fx.adminCookie, bucketQuery("month", month, ""))
	if code != http.StatusOK {
		t.Fatalf("current month csv: status = %d (body: %s)", code, body)
	}
	titles := csvTitleSet(t, body)
	if len(titles) != 7 || !titles["in-pending"] || titles["in-accepted"] {
		t.Fatalf("current month titles = %v, want 7 rows with in-pending, without in-accepted", titles)
	}
	code, _, body = getIntakeCSV(t, fx, fx.wd.fx.adminCookie, bucketQuery("month", prevMonth, ""))
	if code != http.StatusOK {
		t.Fatalf("previous month csv: status = %d (body: %s)", code, body)
	}
	if titles := csvTitles(t, body); len(titles) != 1 || titles[0] != "in-accepted" {
		t.Fatalf("previous month titles = %v, want [in-accepted]", titles)
	}
}

func TestC88_BucketDayBoundaries(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	today := trendDayStart(time.Now())
	// Today 00:00 UTC lands in the NEW day; yesterday 23:59:59
	// stays in the OLD day.
	setIntakeCreatedAt(t, fx, "in-pending", today)
	setIntakeCreatedAt(t, fx, "in-accepted", today.Add(-time.Second))
	code, _, body := getIntakeCSV(t, fx, fx.wd.fx.adminCookie, bucketQuery("day", today, ""))
	if code != http.StatusOK {
		t.Fatalf("today csv: status = %d (body: %s)", code, body)
	}
	titles := csvTitleSet(t, body)
	if len(titles) != 7 || !titles["in-pending"] || titles["in-accepted"] {
		t.Fatalf("today titles = %v, want 7 rows with in-pending, without in-accepted", titles)
	}
	code, _, body = getIntakeCSV(t, fx, fx.wd.fx.adminCookie, bucketQuery("day", today.AddDate(0, 0, -1), ""))
	if code != http.StatusOK {
		t.Fatalf("yesterday csv: status = %d (body: %s)", code, body)
	}
	if titles := csvTitles(t, body); len(titles) != 1 || titles[0] != "in-accepted" {
		t.Fatalf("yesterday titles = %v, want [in-accepted]", titles)
	}
}

func TestC88_BucketPendingSnoozedEffectiveStatus(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	monday := trendMonday(time.Now())
	// pending inside the bucket = the C85 effective set: pending +
	// resurfaced snoozed (in-resurfaced) + form pending, across
	// projects — 4 rows, none of them the still-snoozed row.
	code, _, body := getIntakeCSV(t, fx, fx.wd.fx.adminCookie, bucketQuery("week", monday, "pending"))
	if code != http.StatusOK {
		t.Fatalf("bucket+pending csv: status = %d (body: %s)", code, body)
	}
	titles := csvTitleSet(t, body)
	want := map[string]bool{"in-pending": true, "in-form": true, "in-resurfaced": true, "b-pending": true}
	if len(titles) != len(want) {
		t.Fatalf("bucket+pending titles = %v, want %v", titles, want)
	}
	for title := range want {
		if !titles[title] {
			t.Fatalf("bucket+pending titles = %v, want %v", titles, want)
		}
	}
	// The resurfaced row exports with its EFFECTIVE status (0/pending).
	for _, line := range strings.Split(strings.TrimSpace(body), "\n")[1:] {
		if strings.Contains(line, "in-resurfaced") && !strings.Contains(line, ",0,pending,") {
			t.Fatalf("resurfaced csv row = %q, want effective status 0/pending", line)
		}
	}
	// snoozed inside the bucket = still-snoozed only.
	code, _, body = getIntakeCSV(t, fx, fx.wd.fx.adminCookie, bucketQuery("week", monday, "snoozed"))
	if code != http.StatusOK {
		t.Fatalf("bucket+snoozed csv: status = %d (body: %s)", code, body)
	}
	if titles := csvTitles(t, body); len(titles) != 1 || titles[0] != "in-snoozed" {
		t.Fatalf("bucket+snoozed titles = %v, want [in-snoozed]", titles)
	}
}

func TestC88_BucketArchivedProjectExcluded(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	code, _, body := getIntakeCSV(t, fx, fx.wd.fx.adminCookie, bucketQuery("week", trendMonday(time.Now()), ""))
	if code != http.StatusOK {
		t.Fatalf("bucket csv: status = %d (body: %s)", code, body)
	}
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) != 9 {
		t.Fatalf("csv lines = %d, want 9 (header + 8 live rows, archived excluded)", len(lines))
	}
	if strings.Contains(body, "c-archived") {
		t.Fatalf("bucket csv leaks the archived project's row:\n%s", body)
	}
}

func TestC88_BucketOtherWorkspaceExcluded(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	// A brand-new workspace has no intake rows at all — even
	// though the fixture workspace holds live rows, none leak in.
	slug := uniqueSlug("bucket-csv-empty")
	createWorkspaceHTTP(t, fx.wd.fx.e, fx.wd.fx.adminCookie, "Bucket CSV Empty Co", slug)
	wsBase := "/api/v1/workspaces/" + slug
	dashID := createDashboardHTTP(t, fx.wd.fx.e, fx.wd.fx.adminCookie, wsBase, `{"name":"empty board"}`)
	wid := createWidgetHTTP(t, fx.wd.fx.e, fx.wd.fx.adminCookie, wsBase+"/dashboards/"+dashID,
		`{"title":"Intake","widget_type":"intake_breakdown","config":{}}`)
	rec := getAuthed(t, fx.wd.fx.e, http.MethodGet, "/api/v1/widgets/"+wid+"/export.csv"+bucketQuery("week", trendMonday(time.Now()), ""), fx.wd.fx.adminCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("empty bucket csv: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	lines := strings.Split(strings.TrimSpace(rec.Body.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("empty bucket csv lines = %d, want 1 (header only — honest empty)", len(lines))
	}
}

func TestC88_BucketAuthZ(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	privID := createDashboardHTTP(t, fx.wd.fx.e, fx.wd.fx.adminCookie, fx.wd.fx.wsBase,
		`{"name":"hidden","visibility":"private"}`)
	wid := createWidgetHTTP(t, fx.wd.fx.e, fx.wd.fx.adminCookie, fx.wd.fx.wsBase+"/dashboards/"+privID,
		`{"title":"ib","widget_type":"intake_breakdown","config":{}}`)
	dashURL := fx.wd.fx.wsBase + "/dashboards/" + privID
	bucket := bucketQuery("week", trendMonday(time.Now()), "")
	// Private + unshared: the member gets 404 (no existence leak).
	rec := getAuthed(t, fx.wd.fx.e, http.MethodGet, "/api/v1/widgets/"+wid+"/export.csv"+bucket, fx.wd.fx.memberCookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unshared bucket csv: status = %d, want 404", rec.Code)
	}
	// AuthZ runs before bucket validation: even a garbage bucket
	// param on a private unshared widget reads 404, not 400.
	rec = getAuthed(t, fx.wd.fx.e, http.MethodGet, "/api/v1/widgets/"+wid+"/export.csv?granularity=bogus&bucket_start=2026-01-05", fx.wd.fx.memberCookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unshared garbage bucket csv: status = %d, want 404", rec.Code)
	}
	// View-shared: the member CAN export a bucket.
	code, _ := putShares(t, fx.wd.fx.e, fx.wd.fx.adminCookie, dashURL,
		fmt.Sprintf(`{"shares":[{"member_id":%q,"access":"view"}]}`, fx.wd.fx.memberID))
	if code != http.StatusOK {
		t.Fatalf("PUT shares: status = %d", code)
	}
	rec = getAuthed(t, fx.wd.fx.e, http.MethodGet, "/api/v1/widgets/"+wid+"/export.csv"+bucket, fx.wd.fx.memberCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("shared bucket csv: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestC88_BucketTruncation(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	// 10,101 bulk in_app pending rows in project A via one SQL
	// statement (sequence ids far above the HTTP-created ones, so no
	// UNIQUE collision): past the 10,000-row cap the bucket export
	// truncates and sets X-Result-Truncated. The bulk rows are
	// created "now", so they fall in the current-week bucket.
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
	code, header, body := getIntakeCSV(t, fx, fx.wd.fx.adminCookie, bucketQuery("week", trendMonday(time.Now()), ""))
	if code != http.StatusOK {
		t.Fatalf("bucket csv bulk: status = %d", code)
	}
	if header.Get("X-Result-Truncated") != "true" {
		t.Fatalf("X-Result-Truncated = %q, want true (10,109 rows > cap)", header.Get("X-Result-Truncated"))
	}
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) != 10001 {
		t.Fatalf("csv lines = %d, want 10001 (header + 10,000 capped rows)", len(lines))
	}
}

func TestC88_BucketFilenameSuffix(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	monday := trendMonday(time.Now())
	code, header, _ := getIntakeCSV(t, fx, fx.wd.fx.adminCookie, bucketQuery("week", monday, ""))
	if code != http.StatusOK {
		t.Fatalf("bucket csv: status = %d", code)
	}
	cd := header.Get("Content-Disposition")
	if !strings.Contains(cd, "-week-"+trendDate(monday)+".csv") {
		t.Fatalf("Content-Disposition = %q, want the -week-<bucket_start>.csv suffix", cd)
	}
	// Bucket + metric composes the same way in the name: the bucket
	// suffix alone (existing metric exports carry no metric segment).
	code, header, _ = getIntakeCSV(t, fx, fx.wd.fx.adminCookie, bucketQuery("week", monday, "in_app"))
	if code != http.StatusOK {
		t.Fatalf("bucket+metric csv: status = %d", code)
	}
	if cd := header.Get("Content-Disposition"); !strings.Contains(cd, "-week-"+trendDate(monday)+".csv") {
		t.Fatalf("bucket+metric Content-Disposition = %q, want the -week-<bucket_start>.csv suffix", cd)
	}
	// A month bucket names its month, not the week.
	month := trendMonthStart(time.Now())
	code, header, _ = getIntakeCSV(t, fx, fx.wd.fx.adminCookie, bucketQuery("month", month, ""))
	if code != http.StatusOK {
		t.Fatalf("month bucket csv: status = %d", code)
	}
	if cd := header.Get("Content-Disposition"); !strings.Contains(cd, "-month-"+trendDate(month)+".csv") {
		t.Fatalf("month Content-Disposition = %q, want the -month-<bucket_start>.csv suffix", cd)
	}
}

func TestC88_GroupedWidgetIgnoresBucketParams(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	bar := createWidgetHTTP(t, fx.wd.fx.e, fx.wd.fx.adminCookie, fx.dashURL,
		`{"title":"By state","widget_type":"bar","config":{"group_by":"state"}}`)
	// Aligned bucket params on a grouped widget: ignored, aggregate
	// export unchanged — not a 400.
	rec := getAuthed(t, fx.wd.fx.e, http.MethodGet, "/api/v1/widgets/"+bar+"/export.csv"+bucketQuery("week", trendMonday(time.Now()), ""), fx.wd.fx.adminCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("bar csv + bucket: status = %d, want 200 (bucket ignored)", rec.Code)
	}
	if !strings.HasPrefix(rec.Body.String(), "key,label,value") {
		t.Fatalf("bar csv body = %q, want the chart aggregate header", rec.Body.String())
	}
	// Even garbage bucket params are ignored on a grouped widget
	// (C87 convention — do not 400 on the ignored param).
	rec = getAuthed(t, fx.wd.fx.e, http.MethodGet, "/api/v1/widgets/"+bar+"/export.csv?granularity=bogus&bucket_start=not-a-date", fx.wd.fx.adminCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("bar csv + garbage bucket: status = %d, want 200 (bucket ignored)", rec.Code)
	}
	if !strings.HasPrefix(rec.Body.String(), "key,label,value") {
		t.Fatalf("bar csv body = %q, want the chart aggregate header", rec.Body.String())
	}
}

func TestC88_TrendPathUnaffectedByBucketParams(t *testing.T) {
	fx := setupIntakeDrilldown(t)
	// bucket_start is not a trend param: the trend CSV is the
	// series, not the bucket rows, and a garbage bucket_start does
	// not 400 the trend path.
	code, _, body := getIntakeTrendCSV(t, fx, fx.wd.fx.adminCookie, "?trend=1&bucket_start=not-a-date")
	if code != http.StatusOK {
		t.Fatalf("trend csv + bucket_start: status = %d, want 200", code)
	}
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if lines[0] != "bucket_start,in_app,form,email,accepted,rejected,duplicate" {
		t.Fatalf("trend csv header = %q, want the trend column set", lines[0])
	}
	// An aligned bucket_start alongside trend params is likewise
	// ignored: the day trend still ends today with the live totals.
	code, _, body = getIntakeTrendCSV(t, fx, fx.wd.fx.adminCookie, "?trend=1&granularity=day&buckets=3&bucket_start="+trendDate(trendDayStart(time.Now())))
	if code != http.StatusOK {
		t.Fatalf("trend csv + aligned bucket_start: status = %d, want 200", code)
	}
	lines = strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) != 4 {
		t.Fatalf("trend csv lines = %d, want 4 (header + 3 day buckets)", len(lines))
	}
	if !strings.HasPrefix(lines[3], trendDate(trendDayStart(time.Now()))+",7,1,0,1,1,1") {
		t.Fatalf("trend csv last row = %q, want today's bucket with the live totals", lines[3])
	}
}

// TestOpenAPIDocShapesBucketCSVExport pins the C88T0 API contract in
// docs/openapi.yaml: the bucket_start param on export.csv, the
// granularity param shared with the trend path, and a description
// documenting the bucket-filtered row export and its filename
// suffix. If the implementation drifts from the pinned contract,
// this test fails first.
func TestOpenAPIDocShapesBucketCSVExport(t *testing.T) {
	var spec struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(docs.OpenAPI, &spec); err != nil {
		t.Fatalf("embedded openapi spec is not valid JSON: %v", err)
	}
	var exportOp struct {
		Description string `json:"description"`
		Parameters  []struct {
			Name        string `json:"name"`
			In          string `json:"in"`
			Description string `json:"description"`
			Schema      struct {
				Type   string   `json:"type"`
				Enum   []string `json:"enum"`
				Format string   `json:"format"`
			} `json:"schema"`
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
		if p.Name == "bucket_start" {
			if p.In != "query" || p.Schema.Type != "string" {
				t.Fatalf("bucket_start param = %+v, want a query string param", p)
			}
		}
	}
	for _, name := range []string{"granularity", "bucket_start"} {
		if !found[name] {
			t.Fatalf("export.csv op missing %s query param", name)
		}
	}
	if !strings.Contains(exportOp.Description, "bucket_start") {
		t.Fatal("export.csv description does not document the bucket params")
	}
	if !strings.Contains(exportOp.Description, "-<granularity>-<bucket_start>") {
		t.Fatal("export.csv description does not document the bucket filename suffix")
	}
	if !strings.Contains(exportOp.Description, "created in the bucket") {
		t.Fatal("export.csv description does not document the bucket-filtered row set")
	}
}
