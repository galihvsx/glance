package api

// C6T4: the ?draft= tri-state filter on the issue list endpoint —
// draft=true shows drafts only, absent/false excludes drafts from the
// working set, and garbage is 400.

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestDraftListParam(t *testing.T) {
	_, e, cookie, base := setupBulkProject(t, "draftparam")

	liveID := createIssueHTTP(t, e, cookie, base, "live issue")
	// Drafts are created via the create body (is_draft is a create/PATCH
	// field, not a list default).
	rec := postAuthedJSON(t, e, http.MethodPost, base, cookie,
		`{"name":"draft issue","is_draft":true}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create draft: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode draft: %v", err)
	}
	draftID := created.ID

	listIDs := func(query string) map[string]bool {
		t.Helper()
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

	// draft=true: drafts only.
	only := listIDs("draft=true")
	if !only[draftID] || only[liveID] {
		t.Errorf("draft=true: got %v, want only the draft", only)
	}
	// draft=false: live only.
	noDrafts := listIDs("draft=false")
	if !noDrafts[liveID] || noDrafts[draftID] {
		t.Errorf("draft=false: got %v, want only the live issue", noDrafts)
	}
	// Absent: working-set default — drafts excluded.
	def := listIDs("")
	if !def[liveID] || def[draftID] {
		t.Errorf("no param: got %v, want only the live issue", def)
	}
	// Garbage → 400, not silent coercion.
	rec = getAuthed(t, e, http.MethodGet, base+"?draft=maybe", cookie)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("draft=maybe: status = %d, want 400", rec.Code)
	}
}
