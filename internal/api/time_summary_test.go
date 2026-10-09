package api

// C6T8: the project time-summary endpoint —
// GET .../projects/:identifier/time/summary?days=&group_by=.
// Completed entries aggregate by day (default), week, user, or issue;
// garbage params are 400.

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"
)

func TestTimeSummaryHTTP(t *testing.T) {
	pool, e, cookie, issueBase := setupBulkProject(t, "timesum")
	RegisterSatelliteRoutes(e, &IssueHandler{Pool: pool})

	issueID := createIssueHTTP(t, e, cookie, issueBase, "timed work")
	// Log one hour via the API (timestamps must be RFC3339).
	start := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339)
	end := time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339)
	rec := postAuthedJSON(t, e, http.MethodPost, issueBase+"/"+issueID+"/time/log",
		cookie, `{"started_at":`+strconv.Quote(start)+`,"ended_at":`+strconv.Quote(end)+`}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("log time: status = %d (body: %s)", rec.Code, rec.Body.String())
	}

	// Project base = issue base minus the trailing "/issues".
	projBase := issueBase[:len(issueBase)-len("/issues")]

	get := func(query string) (int, map[string]any) {
		t.Helper()
		rec := getAuthed(t, e, http.MethodGet, projBase+"/time/summary"+query, cookie)
		var body map[string]any
		if rec.Code == http.StatusOK {
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode summary: %v", err)
			}
		}
		return rec.Code, body
	}

	code, body := get("?days=7&group_by=day")
	if code != http.StatusOK {
		t.Fatalf("summary: status = %d", code)
	}
	if body["group_by"] != "day" {
		t.Errorf("group_by = %v, want day", body["group_by"])
	}
	if body["total_seconds"] != float64(3600) {
		t.Errorf("total_seconds = %v, want 3600", body["total_seconds"])
	}
	buckets, _ := body["buckets"].([]any)
	if len(buckets) != 7 {
		t.Errorf("buckets = %d, want 7 zero-filled days", len(buckets))
	}

	// Defaults: no params → days=30, group_by=day.
	code, body = get("")
	if code != http.StatusOK || body["group_by"] != "day" {
		t.Errorf("defaults: code=%d group_by=%v", code, body["group_by"])
	}

	// user grouping carries the member bucket.
	code, body = get("?group_by=user")
	if code != http.StatusOK {
		t.Fatalf("user summary: status = %d", code)
	}
	buckets, _ = body["buckets"].([]any)
	if len(buckets) != 1 {
		t.Errorf("user buckets = %d, want 1", len(buckets))
	}

	// Garbage params → 400, not silent coercion.
	for _, q := range []string{"?days=abc", "?days=0", "?group_by=fortnight"} {
		if code, _ := get(q); code != http.StatusBadRequest {
			t.Errorf("GET %s: status = %d, want 400", q, code)
		}
	}
}
