package api

// PQL multi-key ORDER BY HTTP tests (C99T0): ?pql= with a trailing
// ORDER BY clause of several keys end to end — the second key decides
// when the first ties (not the id tiebreak), a multi-key cursor walks
// every page without dupes or skips, and a cursor minted under a
// different key list is rejected, never silently honored.

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestIssueListHTTPPQLMultiKeyOrderBy(t *testing.T) {
	_, e, cookie, base := setupBulkProject(t, "pqlmultiord")

	// Two issues share priority 2; their creation (sequence) order is
	// m1 then m2, and m3 has priority 1. The id tiebreak is a random
	// UUID order, so the proof the second key decides is a pair of
	// queries that differ only in the second key's direction: they
	// must give opposite tie orders (m1,m2 vs m2,m1). An implementation
	// ignoring the second key would give the same tie order twice.
	for _, iss := range []struct {
		name     string
		priority int
	}{{"m1", 2}, {"m2", 2}, {"m3", 1}} {
		rec := postAuthedJSON(t, e, http.MethodPost, base, cookie,
			fmt.Sprintf(`{"name":%q,"priority":%d}`, iss.name, iss.priority))
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %q: status = %d (body: %s)", iss.name, rec.Code, rec.Body.String())
		}
	}
	pql := func(q string) string { return "pql=" + url.QueryEscape(q) }

	// Multi-key: priority DESC, then sequence_id ASC (default).
	got, _, code, body := pqlOrderPage(t, e, cookie, base,
		pql("priority >= 0 ORDER BY priority DESC, sequence_id")+"&per_page=100")
	if code != http.StatusOK || strings.Join(got, ",") != "m1,m2,m3" {
		t.Fatalf("multi-key priority DESC, sequence_id: status = %d got %v (body: %s), want [m1 m2 m3]", code, got, body)
	}

	// Same keys, second key flipped to DESC: the tie order flips to
	// m2, m1 — the pair (ASC above, DESC here) proves the second key
	// decides, since an ignored second key would give one tie order.
	got, _, code, body = pqlOrderPage(t, e, cookie, base,
		pql("priority >= 0 ORDER BY priority DESC, sequence_id DESC")+"&per_page=100")
	if code != http.StatusOK || strings.Join(got, ",") != "m2,m1,m3" {
		t.Fatalf("multi-key priority DESC, sequence_id DESC: status = %d got %v (body: %s), want [m2 m1 m3]", code, got, body)
	}

	// Mixed directions, all keys explicit: priority ASC puts m3 first,
	// then sequence_id DESC breaks the priority-2 tie as m2, m1.
	got, _, code, body = pqlOrderPage(t, e, cookie, base,
		pql("priority >= 0 ORDER BY priority ASC, sequence_id DESC")+"&per_page=100")
	if code != http.StatusOK || strings.Join(got, ",") != "m3,m2,m1" {
		t.Fatalf("multi-key priority ASC, sequence_id DESC: status = %d got %v (body: %s), want [m3 m2 m1]", code, got, body)
	}

	// PQL multi-key ORDER BY still beats ?order_by=.
	got, _, code, body = pqlOrderPage(t, e, cookie, base,
		pql("priority >= 0 ORDER BY priority DESC, sequence_id")+"&order_by=-sequence_id&per_page=100")
	if code != http.StatusOK || strings.Join(got, ",") != "m1,m2,m3" {
		t.Fatalf("multi-key beats param: status = %d got %v (body: %s), want [m1 m2 m3]", code, got, body)
	}
}

func TestIssueListHTTPPQLMultiKeyCursor(t *testing.T) {
	_, e, cookie, base := setupBulkProject(t, "pqlmulticur")

	for _, iss := range []struct {
		name     string
		priority int
	}{{"m1", 2}, {"m2", 2}, {"m3", 1}} {
		rec := postAuthedJSON(t, e, http.MethodPost, base, cookie,
			fmt.Sprintf(`{"name":%q,"priority":%d}`, iss.name, iss.priority))
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %q: status = %d (body: %s)", iss.name, rec.Code, rec.Body.String())
		}
	}
	pql := func(q string) string { return "pql=" + url.QueryEscape(q) }
	order := "priority >= 0 ORDER BY priority DESC, sequence_id"

	// Walk every page at per_page=1: the multi-value cursor must
	// visit m1, m2, m3 exactly once, in order, with no dupes/skips,
	// and the last page must carry no next cursor.
	var seen []string
	cursor := ""
	pages := 0
	for {
		pages++
		if pages > 10 {
			t.Fatalf("paginated walk: did not terminate, seen %v", seen)
		}
		q := pql(order) + "&per_page=1"
		if cursor != "" {
			q += "&cursor=" + url.QueryEscape(cursor)
		}
		got, next, code, body := pqlOrderPage(t, e, cookie, base, q)
		if code != http.StatusOK {
			t.Fatalf("page %d: status = %d (body: %s), want 200", pages, code, body)
		}
		seen = append(seen, got...)
		cursor = next
		if cursor == "" {
			break
		}
	}
	if strings.Join(seen, ",") != "m1,m2,m3" {
		t.Fatalf("paginated walk: got %v, want [m1 m2 m3]", seen)
	}
	if pages != 3 {
		t.Fatalf("paginated walk: %d pages, want 3", pages)
	}

	// A cursor minted under the multi-key order is rejected under a
	// different key list (single-key priority DESC here).
	_, multiCursor, code, body := pqlOrderPage(t, e, cookie, base,
		pql(order)+"&per_page=1")
	if code != http.StatusOK || multiCursor == "" {
		t.Fatalf("multi-key page 1: status = %d cursor = %q (body: %s), want a cursor", code, multiCursor, body)
	}
	_, _, code, body = pqlOrderPage(t, e, cookie, base,
		pql("priority >= 0 ORDER BY priority DESC")+"&cursor="+url.QueryEscape(multiCursor))
	if code != http.StatusBadRequest {
		t.Fatalf("multi-key cursor under single-key order: status = %d, want 400 (body: %s)", code, body)
	}

	// And the reverse: a single-key cursor is rejected under the
	// multi-key order (composite key differs).
	_, singleCursor, code, body := pqlOrderPage(t, e, cookie, base,
		pql("priority >= 0 ORDER BY priority DESC")+"&per_page=1")
	if code != http.StatusOK || singleCursor == "" {
		t.Fatalf("single-key page 1: status = %d cursor = %q (body: %s), want a cursor", code, singleCursor, body)
	}
	_, _, code, body = pqlOrderPage(t, e, cookie, base,
		pql(order)+"&cursor="+url.QueryEscape(singleCursor))
	if code != http.StatusBadRequest {
		t.Fatalf("single-key cursor under multi-key order: status = %d, want 400 (body: %s)", code, body)
	}

	// A cursor minted under another multi-key list is rejected too.
	_, _, code, body = pqlOrderPage(t, e, cookie, base,
		pql("priority >= 0 ORDER BY priority DESC, updated_at")+"&cursor="+url.QueryEscape(multiCursor))
	if code != http.StatusBadRequest {
		t.Fatalf("multi-key cursor under a different multi-key list: status = %d, want 400 (body: %s)", code, body)
	}
}
