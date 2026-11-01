package api

// C111T0: backend contract proof — PQL ANDs with the archived / draft
// list modes at the HTTP level, following the pql_http_test.go idioms.
//
// Honest semantics, documented here and at each assertion site (do not
// read these tests as claiming an archived-only backend filter — there
// is none):
//   - Archived: ?archived=1 only INCLUDES archived rows server-side
//     (it drops the archived_at IS NULL condition; working-set rows
//     matching the predicate are still returned). The archived-only
//     narrowing is the frontend's client-side archived_at != null
//     filter, per web/src/lib/archived.ts — and one assertion below
//     applies exactly that filter to the server response to prove
//     the view shape holds.
//   - Drafts: ?draft=true selects is_draft = TRUE server-side, so no
//     client-side filter is needed; a PQL query on Drafts never leaks
//     published rows.
// Also proved: a probe failure still 400s with message + position in
// both modes, and a blank ?pql= degrades to the unfiltered mode list
// (the C91 contract, in both modes). hasChildren() semantics are
// untouched here (documented existing behavior: archived/draft
// children never count).

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"
)

func pqlListModeRows(t *testing.T, e *echo.Echo, cookie *http.Cookie, base, query string) []struct {
	Name       string  `json:"name"`
	ArchivedAt *string `json:"archived_at"`
	IsDraft    bool    `json:"is_draft"`
} {
	t.Helper()
	rec := getAuthed(t, e, http.MethodGet, base+"?"+query, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: status = %d (body: %s)", query, rec.Code, rec.Body.String())
	}
	var res struct {
		Results []struct {
			Name       string  `json:"name"`
			ArchivedAt *string `json:"archived_at"`
			IsDraft    bool    `json:"is_draft"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return res.Results
}

func pqlListModeNames(rows []struct {
	Name       string  `json:"name"`
	ArchivedAt *string `json:"archived_at"`
	IsDraft    bool    `json:"is_draft"`
}) map[string]bool {
	names := map[string]bool{}
	for _, r := range rows {
		names[r.Name] = true
	}
	return names
}

func TestIssueListHTTPPQLListModes(t *testing.T) {
	_, e, cookie, base := setupBulkProject(t, "pqllistmode")

	createWithPriority := func(name string, priority int, draft bool) string {
		t.Helper()
		body := fmt.Sprintf(`{"name":%q,"priority":%d,"is_draft":%v}`, name, priority, draft)
		rec := postAuthedJSON(t, e, http.MethodPost, base, cookie, body)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %q: status = %d (body: %s)", name, rec.Code, rec.Body.String())
		}
		var created struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
			t.Fatalf("decode created %q: %v", name, err)
		}
		return created.ID
	}
	archive := func(id string) {
		t.Helper()
		rec := postAuthedJSON(t, e, http.MethodPatch, base+"/"+id, cookie, `{"archived":true}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("archive %s: status = %d (body: %s)", id, rec.Code, rec.Body.String())
		}
	}

	// Seed: archived x predicate-match, working-set x predicate-match,
	// drafts x predicate-match, plus a published row — so each mode's
	// AND can be told apart from a dropped flag.
	archive(createWithPriority("arch-high", 3, false))
	archive(createWithPriority("arch-low", 1, false))
	createWithPriority("live-high", 3, false)
	createWithPriority("live-low", 1, false)
	createWithPriority("draft-high", 3, true)
	createWithPriority("draft-low", 1, true)
	createWithPriority("pub-high", 3, false)

	// Archived mode + PQL: the server returns archived AND working-set
	// rows matching the predicate — archived=1 INCLUDES archived rows,
	// it does not restrict to them. The archived-only view is the
	// frontend's client-side archived_at != null filter on this
	// response, proven by the filtered assertion below.
	rows := pqlListModeRows(t, e, cookie, base,
		"archived=1&pql="+url.QueryEscape("priority >= 3")+"&per_page=100")
	got := pqlListModeNames(rows)
	for _, want := range []string{"arch-high", "live-high", "pub-high"} {
		if !got[want] {
			t.Errorf("archived=1 + priority >= 3: missing %q in %v", want, got)
		}
	}
	for _, notWant := range []string{"arch-low", "live-low", "draft-high", "draft-low"} {
		if got[notWant] {
			t.Errorf("archived=1 + priority >= 3: unexpected %q in %v", notWant, got)
		}
	}
	// The client-side archived-only filter on the same response leaves
	// exactly the archived match — so the Archived view never shows
	// working-set issues.
	archOnly := map[string]bool{}
	for _, r := range rows {
		if r.ArchivedAt != nil {
			archOnly[r.Name] = true
		}
	}
	if len(archOnly) != 1 || !archOnly["arch-high"] {
		t.Errorf("archived=1 response + archived_at != null filter: got %v, want only [arch-high]", archOnly)
	}

	// Draft mode + PQL: only draft rows matching the predicate — no
	// published rows leak in.
	rows = pqlListModeRows(t, e, cookie, base,
		"draft=true&pql="+url.QueryEscape("priority >= 3")+"&per_page=100")
	got = pqlListModeNames(rows)
	if len(got) != 1 || !got["draft-high"] {
		t.Errorf("draft=true + priority >= 3: got %v, want only [draft-high]", got)
	}

	// Probe failures still 400 with message + position in both modes.
	for _, mode := range []string{"archived=1", "draft=true"} {
		rec := getAuthed(t, e, http.MethodGet, base+"?"+mode+"&pql="+url.QueryEscape("state ="), cookie)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s + bad pql: status = %d, want 400 (body: %s)", mode, rec.Code, rec.Body.String())
			continue
		}
		msg, pos, hasPos := pqlErrorBody(t, rec.Body.String())
		if msg == "" || !hasPos || pos < 1 {
			t.Errorf("%s + bad pql: message = %q position present = %v (%v), want a message + 1-based position", mode, msg, hasPos, pos)
		}
		rec = getAuthed(t, e, http.MethodGet, base+"?"+mode+"&pql="+url.QueryEscape("state = NoSuchState"), cookie)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s + unknown state: status = %d, want 400 (body: %s)", mode, rec.Code, rec.Body.String())
			continue
		}
		msg, _, _ = pqlErrorBody(t, rec.Body.String())
		if !strings.Contains(msg, "NoSuchState") {
			t.Errorf("%s + unknown state: message = %q, want it to name NoSuchState", mode, msg)
		}
	}

	// Blank ?pql= degrades to the unfiltered mode list in both modes.
	rows = pqlListModeRows(t, e, cookie, base, "archived=1&pql=&per_page=100")
	got = pqlListModeNames(rows)
	for _, want := range []string{"arch-high", "arch-low", "live-high", "live-low", "pub-high"} {
		if !got[want] {
			t.Errorf("archived=1 + blank pql: missing %q in %v", want, got)
		}
	}
	if got["draft-high"] || got["draft-low"] {
		t.Errorf("archived=1 + blank pql: drafts leaked into %v", got)
	}
	rows = pqlListModeRows(t, e, cookie, base, "draft=true&pql=&per_page=100")
	got = pqlListModeNames(rows)
	if len(got) != 2 || !got["draft-high"] || !got["draft-low"] {
		t.Errorf("draft=true + blank pql: got %v, want [draft-high draft-low]", got)
	}
}
