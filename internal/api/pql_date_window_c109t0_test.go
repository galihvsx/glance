package api

// PQL date-window HTTP tests (C109T0): ?due_after/?due_before,
// ?start_after/?start_before, and ?undated= survive PQL mode over
// HTTP — the window params are parsed by parsePQLListInput and AND
// with the PQL predicate, exactly like the cycle/module/epic scope
// containers (C95T0/C108T0). Fixture dates are attached via SQL (the
// list endpoint is what's under test; date setting already has its
// own HTTP tests).

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"testing"
)

func TestIssueListHTTPPQLDateWindow(t *testing.T) {
	pool, e, cookie, base := setupBulkProject(t, "pqldatewin")
	ctx := context.Background()

	// dueInHigh / dueInLow: target inside the Nov window.
	// dueOutHigh: target outside it (December). startInHigh /
	// startOutHigh: start-dated only (no target). undatedHigh /
	// undatedLow: neither date. Priority separates the predicate
	// match (3) from the non-match (1) inside each window.
	dueInHigh := createIssueHTTP(t, e, cookie, base, "dueInHigh")
	dueInLow := createIssueHTTP(t, e, cookie, base, "dueInLow")
	dueOutHigh := createIssueHTTP(t, e, cookie, base, "dueOutHigh")
	startInHigh := createIssueHTTP(t, e, cookie, base, "startInHigh")
	startOutHigh := createIssueHTTP(t, e, cookie, base, "startOutHigh")
	undatedHigh := createIssueHTTP(t, e, cookie, base, "undatedHigh")
	undatedLow := createIssueHTTP(t, e, cookie, base, "undatedLow")
	for _, q := range []string{
		fmt.Sprintf(`UPDATE issues SET target_date = '2026-11-10'::date WHERE id = '%s'`, dueInHigh),
		fmt.Sprintf(`UPDATE issues SET target_date = '2026-11-15'::date WHERE id = '%s'`, dueInLow),
		fmt.Sprintf(`UPDATE issues SET target_date = '2026-12-20'::date WHERE id = '%s'`, dueOutHigh),
		fmt.Sprintf(`UPDATE issues SET start_date = '2026-11-12'::date WHERE id = '%s'`, startInHigh),
		fmt.Sprintf(`UPDATE issues SET start_date = '2026-12-22'::date WHERE id = '%s'`, startOutHigh),
		fmt.Sprintf(`UPDATE issues SET priority = 3 WHERE id IN ('%s', '%s', '%s', '%s', '%s')`,
			dueInHigh, dueOutHigh, startInHigh, startOutHigh, undatedHigh),
		fmt.Sprintf(`UPDATE issues SET priority = 1 WHERE id IN ('%s', '%s')`, dueInLow, undatedLow),
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("fixture SQL %q: %v", q, err)
		}
	}

	suffix := "&order_by=-sequence_id&per_page=100"
	pql := func(pred string) string {
		return "pql=" + url.QueryEscape(pred) + suffix
	}
	dueWindow := "due_after=2026-11-01T00:00:00Z&due_before=2026-12-01T00:00:00Z"
	startWindow := "start_after=2026-11-01T00:00:00Z&start_before=2026-12-01T00:00:00Z"
	want := func(got []string, names ...string) {
		t.Helper()
		sort.Strings(names)
		sorted := append([]string(nil), got...)
		sort.Strings(sorted)
		if strings.Join(sorted, "|") != strings.Join(names, "|") {
			t.Fatalf("got %v, want %v", sorted, names)
		}
	}

	// Invariant 1: due window + predicate ANDs — only the in-window
	// issue matching the predicate, not the out-of-window match or
	// the in-window non-match. NULL-target rows are outside the
	// due window by construction (target_date >= fails on NULL).
	want(pqlListNamesHTTP(t, e, cookie, base, pql("priority >= 2")+"&"+dueWindow), "dueInHigh")
	want(pqlListNamesHTTP(t, e, cookie, base, pql("priority >= 0")+"&"+dueWindow), "dueInHigh", "dueInLow")
	// Predicate alone still sees the out-of-window match (control).
	want(pqlListNamesHTTP(t, e, cookie, base, pql("priority >= 2")), "dueInHigh", "dueOutHigh", "startInHigh", "startOutHigh", "undatedHigh")
	// Invariant 2: start window, same shape.
	want(pqlListNamesHTTP(t, e, cookie, base, pql("priority >= 2")+"&"+startWindow), "startInHigh")
	want(pqlListNamesHTTP(t, e, cookie, base, pql("priority >= 0")+"&"+startWindow), "startInHigh")
	// Invariant 3: undated + predicate returns only undated matches.
	want(pqlListNamesHTTP(t, e, cookie, base, pql("priority >= 2")+"&undated=1"), "undatedHigh")
	want(pqlListNamesHTTP(t, e, cookie, base, pql("priority >= 0")+"&undated=1"), "undatedHigh", "undatedLow")
	// Invariant 4: window + PQL date predicate outside the window
	// returns empty (AND), not the predicate's dates.
	want(pqlListNamesHTTP(t, e, cookie, base, pql("due >= 2026-12-01")+"&"+dueWindow))
	want(pqlListNamesHTTP(t, e, cookie, base, pql("start >= 2026-12-01")+"&"+startWindow))
	// Controls: each predicate alone finds its out-of-window row.
	want(pqlListNamesHTTP(t, e, cookie, base, pql("due >= 2026-12-01")), "dueOutHigh")
	want(pqlListNamesHTTP(t, e, cookie, base, pql("start >= 2026-12-01")), "startOutHigh")
	// PQL date predicate with NO window param behaves exactly as
	// today.
	want(pqlListNamesHTTP(t, e, cookie, base, pql("due >= 2026-11-01")), "dueInHigh", "dueInLow", "dueOutHigh")
	// due_date_empty is a visual filter dimension, not a window: in
	// PQL mode it is ignored, and it does NOT 400 against a due
	// window the way the visual path's combination rule does.
	want(pqlListNamesHTTP(t, e, cookie, base, pql("priority >= 2")+"&due_date_empty=1"),
		"dueInHigh", "dueOutHigh", "startInHigh", "startOutHigh", "undatedHigh")
	want(pqlListNamesHTTP(t, e, cookie, base, pql("priority >= 2")+"&"+dueWindow+"&due_date_empty=1"), "dueInHigh")
	// Invariant 6: blank ?pql= is still "no PQL" — the visual window
	// applies, not a PQL request.
	want(pqlListNamesHTTP(t, e, cookie, base, "pql=&"+dueWindow+suffix), "dueInHigh", "dueInLow")
	// Visual-path behavior unchanged: window without ?pql=.
	want(pqlListNamesHTTP(t, e, cookie, base, dueWindow+suffix), "dueInHigh", "dueInLow")
	want(pqlListNamesHTTP(t, e, cookie, base, startWindow+suffix), "startInHigh")
	want(pqlListNamesHTTP(t, e, cookie, base, "undated=1"+suffix), "undatedHigh", "undatedLow")
	// Invariant 5: invalid window values in PQL mode 400 honestly
	// (bad RFC3339, never a silent empty list or a dropped window).
	for _, bad := range []string{
		"due_after=not-a-date", "due_before=2026-13-45T00:00:00Z",
		"start_after=garbage", "start_before=2026-02-30T00:00:00Z",
	} {
		rec := getAuthed(t, e, http.MethodGet, base+"?"+pql("priority >= 0")+"&"+bad, cookie)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("malformed window %q + pql: status = %d, want 400 (body: %s)", bad, rec.Code, rec.Body.String())
		}
	}
}
