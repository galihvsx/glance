package api

// C80T0: ?due_date_empty=1 on the issue list endpoint, end to end.
// Mirrors calendar_params_test.go: due-only, start-only and undated
// issues, then the filter and its 400 conflict with due_after/before.

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestIssueListHTTPDueDateEmpty(t *testing.T) {
	_, e, cookie, base := setupBulkProject(t, "dueempty")

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

	// due_date_empty=1: the undated issue AND the start-only issue
	// (target_date IS NULL); the due-dated issue is excluded.
	byEmpty := listIDs("due_date_empty=1")
	if len(byEmpty) != 2 || !byEmpty[undatedID] || !byEmpty[startID] {
		t.Errorf("due_date_empty=1: got %v, want start-only + undated", byEmpty)
	}
	if byEmpty[dueID] {
		t.Errorf("due_date_empty=1: matched the due-dated issue")
	}

	// Conflicting same-field range + empty is a 400.
	for _, q := range []string{
		"due_date_empty=1&due_after=2026-10-01T00:00:00Z",
		"due_date_empty=1&due_before=2026-11-01T00:00:00Z",
	} {
		rec := getAuthed(t, e, http.MethodGet, base+"?"+q, cookie)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("GET %s: status = %d, want 400", q, rec.Code)
		}
	}
}

// TestExportCSVDueDateEmpty pins that the export endpoint honors
// ?due_date_empty= through the shared parseIssueListInput — the list
// and the export see the same filtered working set (C8T3).
func TestExportCSVDueDateEmpty(t *testing.T) {
	_, e, cookie, base, _, _ := setupExportProject(t, "exp-dueempty")

	for name, body := range map[string]string{
		"dated":   `{"name":"dated","target_date":"2026-12-31"}`,
		"undated": `{"name":"undated"}`,
	} {
		rec := postAuthedJSON(t, e, http.MethodPost, base, cookie, body)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %s: status = %d (body: %s)", name, rec.Code, rec.Body.String())
		}
	}

	rec := getExport(t, e, base+"/export?format=csv&due_date_empty=1", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("export: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	recs := parseCSVBody(t, rec.Body.String())
	if len(recs) != 2 {
		t.Fatalf("records = %d, want 2 (header + the undated row)", len(recs))
	}
	if recs[1][1] != "undated" {
		t.Fatalf("exported row = %q, want the undated issue", recs[1][1])
	}

	// The same conflict is 400 on the export path.
	rec = getExport(t, e, base+"/export?format=csv&due_date_empty=1&due_after=2026-01-01T00:00:00Z", cookie)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("export conflict: status = %d, want 400", rec.Code)
	}
}
