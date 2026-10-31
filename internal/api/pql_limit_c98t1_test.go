package api

// PQL LIMIT HTTP tests (C98T1): ?pql= with a trailing LIMIT clause
// end to end — at most n rows in a single page, next_cursor always
// null when LIMIT is present (even when more rows match), at most
// min(per_page, n) rows, a cursor combined with LIMIT is 400 rather
// than silently ignored, and malformed LIMIT clauses are 400.

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
)

func TestIssueListHTTPPQLLimit(t *testing.T) {
	_, e, cookie, base := setupBulkProject(t, "pqllimit")

	// Distinct priorities so ORDER BY + LIMIT gives a visible order.
	for _, iss := range []struct {
		name     string
		priority int
	}{{"p1", 1}, {"p2", 2}, {"p3", 3}, {"p4", 4}} {
		rec := postAuthedJSON(t, e, http.MethodPost, base, cookie,
			fmt.Sprintf(`{"name":%q,"priority":%d}`, iss.name, iss.priority))
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %q: status = %d (body: %s)", iss.name, rec.Code, rec.Body.String())
		}
	}
	pql := func(q string) string { return "pql=" + url.QueryEscape(q) }

	// Standalone LIMIT: at most n rows, next_cursor null even though
	// 4 rows match.
	names, cursor, code, body := pqlOrderPage(t, e, cookie, base,
		pql("priority >= 0 LIMIT 2")+"&per_page=100")
	if code != http.StatusOK || len(names) != 2 || cursor != "" {
		t.Fatalf("LIMIT 2: status = %d names = %v cursor = %q, want 2 rows + null cursor (body: %s)", code, names, cursor, body)
	}

	// LIMIT composes with ORDER BY: top 2 by priority DESC.
	names, cursor, code, body = pqlOrderPage(t, e, cookie, base,
		pql("priority >= 0 ORDER BY priority DESC LIMIT 2")+"&per_page=100")
	if code != http.StatusOK || cursor != "" ||
		len(names) != 2 || names[0] != "p4" || names[1] != "p3" {
		t.Fatalf("ORDER BY + LIMIT: status = %d names = %v cursor = %q, want [p4 p3] + null cursor (body: %s)", code, names, cursor, body)
	}

	// min(per_page, n): per_page=2 wins over LIMIT 10, and the cursor
	// is still null (LIMIT means a single page, not a smaller first
	// page of a longer walk).
	names, cursor, code, body = pqlOrderPage(t, e, cookie, base,
		pql("priority >= 0 LIMIT 10")+"&per_page=2")
	if code != http.StatusOK || len(names) != 2 || cursor != "" {
		t.Fatalf("LIMIT 10 + per_page=2: status = %d names = %v cursor = %q, want 2 rows + null cursor (body: %s)", code, names, cursor, body)
	}

	// A cursor combined with LIMIT is 400, never silently ignored:
	// mint a real cursor from a paged non-LIMIT query first.
	_, pageCursor, code, body := pqlOrderPage(t, e, cookie, base,
		pql("priority >= 0 ORDER BY priority DESC")+"&per_page=1")
	if code != http.StatusOK || pageCursor == "" {
		t.Fatalf("mint cursor: status = %d cursor = %q (body: %s)", code, pageCursor, body)
	}
	_, _, code, body = pqlOrderPage(t, e, cookie, base,
		pql("priority >= 0 ORDER BY priority DESC LIMIT 2")+"&per_page=100&cursor="+url.QueryEscape(pageCursor))
	if code != http.StatusBadRequest {
		t.Fatalf("cursor + LIMIT: status = %d, want 400 (body: %s)", code, body)
	}
	_, _, code, body = pqlOrderPage(t, e, cookie, base,
		pql("priority >= 0 LIMIT 2")+"&per_page=100&cursor="+url.QueryEscape(pageCursor))
	if code != http.StatusBadRequest {
		t.Fatalf("cursor + standalone LIMIT: status = %d, want 400 (body: %s)", code, body)
	}

	// Without LIMIT, paging behaves exactly as today (cursor minted).
	_, pageCursor, code, body = pqlOrderPage(t, e, cookie, base,
		pql("priority >= 0 ORDER BY priority DESC")+"&per_page=2")
	if code != http.StatusOK || pageCursor == "" {
		t.Fatalf("no LIMIT still pages: status = %d cursor = %q (body: %s)", code, pageCursor, body)
	}

	// Malformed LIMIT clauses are 400.
	for _, q := range []string{
		"priority >= 0 LIMIT 0",
		"priority >= 0 LIMIT 101",
		"priority >= 0 LIMIT abc",
		"priority >= 0 LIMIT 2 LIMIT 3",
		"priority >= 0 LIMIT 2 ORDER BY priority",
	} {
		_, _, code, body := pqlOrderPage(t, e, cookie, base, pql(q)+"&per_page=100")
		if code != http.StatusBadRequest {
			t.Fatalf("pql %q: status = %d, want 400 (body: %s)", q, code, body)
		}
	}
}
