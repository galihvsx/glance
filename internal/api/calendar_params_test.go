package api

// C3T5: calendar params on the issue list endpoint — start_after /
// start_before bracket issues.start_date, and undated=1 matches issues with
// neither a target nor a start date. They ride the same parseTime loop and
// ListIssuesInput as the other date filters.

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestCalendarListParams(t *testing.T) {
	_, e, cookie, base := setupBulkProject(t, "calparams")

	// dueOnly: target Oct 12, no start. startOnly: start Oct 5, no target.
	// undated: neither.
	dueID := createIssueHTTP(t, e, cookie, base, "due only")
	startID := createIssueHTTP(t, e, cookie, base, "start only")
	undatedID := createIssueHTTP(t, e, cookie, base, "undated")
	for id, body := range map[string]string{
		dueID:   `{"target_date":"2026-10-12"}`,
		startID: `{"start_date":"2026-10-05"}`,
	} {
		rec := postAuthedJSON(t, e, http.MethodPatch, base+"/"+id, cookie, body)
		if rec.Code != http.StatusOK {
			t.Fatalf("set dates on %s: status = %d (body: %s)", id, rec.Code, rec.Body.String())
		}
	}

	listIDs := func(query string) map[string]bool {
		rec := getAuthed(t, e, http.MethodGet, base+"?"+query, cookie)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: status = %d (body: %s)", query, rec.Code, rec.Body.String())
		}
		var res struct {
			Results []struct {
				ID string `json:"id"`
			} `json:"results"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
			t.Fatalf("decode: %v", err)
		}
		m := map[string]bool{}
		for _, r := range res.Results {
			m[r.ID] = true
		}
		return m
	}

	oct := "start_after=2026-10-01T00:00:00Z&start_before=2026-11-01T00:00:00Z"
	byStart := listIDs(oct)
	if len(byStart) != 1 || !byStart[startID] {
		t.Errorf("start range: got %v, want only start-only", byStart)
	}

	byUndated := listIDs("undated=1")
	if len(byUndated) != 1 || !byUndated[undatedID] {
		t.Errorf("undated=1: got %v, want only undated", byUndated)
	}

	byDue := listIDs("due_after=2026-10-01T00:00:00Z&due_before=2026-11-01T00:00:00Z")
	if len(byDue) != 1 || !byDue[dueID] {
		t.Errorf("due range: got %v, want only due-only", byDue)
	}

	// Malformed start_after is a 400, same as the other date params.
	rec := getAuthed(t, e, http.MethodGet, base+"?start_after=not-a-date", cookie)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad start_after: status = %d, want 400", rec.Code)
	}
}
