package api

// Multi-key ?order_by= HTTP tests (C99T1): the param's
// comma-separated key list reorders rows end to end, paginates via
// the T0 composite cursor without dupes/skips, still loses to a PQL
// ORDER BY, and rejects cursors minted under a different key list —
// while invalid lists (unknown key, empty segment, duplicate key)
// are 400, never silently truncated.

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestIssueListHTTPMultiKeyOrderByParam(t *testing.T) {
	_, e, cookie, base := setupBulkProject(t, "ordparam")

	// Two issues share priority 2; their creation (sequence) order is
	// m1 then m2, and m3 has priority 1. As in the T0 test, the proof
	// the second key decides is a pair of queries differing only in
	// the second key's direction, which must give opposite tie orders.
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

	// Multi-key param: priority ASC, then sequence_id ASC — m3 first
	// (priority 1), then the priority-2 tie in creation order.
	got, _, code, body := pqlOrderPage(t, e, cookie, base,
		"order_by="+url.QueryEscape("priority,sequence_id")+"&per_page=100")
	if code != http.StatusOK || strings.Join(got, ",") != "m3,m1,m2" {
		t.Fatalf("priority,sequence_id: status = %d got %v (body: %s), want [m3 m1 m2]", code, got, body)
	}

	// Same keys, second key flipped: the tie order flips to m2, m1.
	got, _, code, body = pqlOrderPage(t, e, cookie, base,
		"order_by="+url.QueryEscape("priority,-sequence_id")+"&per_page=100")
	if code != http.StatusOK || strings.Join(got, ",") != "m3,m2,m1" {
		t.Fatalf("priority,-sequence_id: status = %d got %v (body: %s), want [m3 m2 m1]", code, got, body)
	}

	// Invalid lists are 400, never a silently-dropped key.
	for _, bad := range []string{"bogus", "priority,bogus", "priority,,sequence_id",
		",priority", "priority,", "priority,priority", "priority,-priority"} {
		_, _, code, body := pqlOrderPage(t, e, cookie, base,
			"order_by="+url.QueryEscape(bad)+"&per_page=100")
		if code != http.StatusBadRequest {
			t.Fatalf("order_by=%q: status = %d, want 400 (body: %s)", bad, code, body)
		}
	}
}

func TestIssueListHTTPMultiKeyOrderByParamCursor(t *testing.T) {
	_, e, cookie, base := setupBulkProject(t, "ordparamcur")

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
	order := "priority,sequence_id"

	// Walk every page at per_page=1: the composite cursor must visit
	// m3, m1, m2 exactly once, in order, with no dupes/skips.
	var seen []string
	cursor := ""
	pages := 0
	for {
		pages++
		if pages > 10 {
			t.Fatalf("paginated walk: did not terminate, seen %v", seen)
		}
		q := "order_by=" + url.QueryEscape(order) + "&per_page=1"
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
	if strings.Join(seen, ",") != "m3,m1,m2" {
		t.Fatalf("paginated walk: got %v, want [m3 m1 m2]", seen)
	}
	if pages != 3 {
		t.Fatalf("paginated walk: %d pages, want 3", pages)
	}

	// A cursor minted under the multi-key param list is rejected
	// under a different key list (flipped second key, or single key).
	_, multiCursor, code, body := pqlOrderPage(t, e, cookie, base,
		"order_by="+url.QueryEscape(order)+"&per_page=1")
	if code != http.StatusOK || multiCursor == "" {
		t.Fatalf("multi-key page 1: status = %d cursor = %q (body: %s), want a cursor", code, multiCursor, body)
	}
	for _, other := range []string{"priority,-sequence_id", "priority"} {
		_, _, code, body := pqlOrderPage(t, e, cookie, base,
			"order_by="+url.QueryEscape(other)+"&cursor="+url.QueryEscape(multiCursor))
		if code != http.StatusBadRequest {
			t.Fatalf("multi-key cursor under %q: status = %d, want 400 (body: %s)", other, code, body)
		}
	}
}

func TestIssueListHTTPMultiKeyOrderByParamLosesToPQL(t *testing.T) {
	_, e, cookie, base := setupBulkProject(t, "ordparampql")

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

	// The param alone (sequence DESC) would give m3, m2, m1; the PQL
	// ORDER BY (priority DESC, sequence ASC) gives m1, m2, m3 — and
	// the PQL order must win.
	got, _, code, body := pqlOrderPage(t, e, cookie, base,
		"pql="+url.QueryEscape("priority >= 0 ORDER BY priority DESC, sequence_id")+
			"&order_by="+url.QueryEscape("priority,-sequence_id")+"&per_page=100")
	if code != http.StatusOK || strings.Join(got, ",") != "m1,m2,m3" {
		t.Fatalf("PQL beats multi-key param: status = %d got %v (body: %s), want [m1 m2 m3]", code, got, body)
	}
}
