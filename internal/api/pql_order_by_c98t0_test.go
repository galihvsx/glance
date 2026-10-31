package api

// PQL ORDER BY HTTP tests (C98T0): ?pql= with a trailing ORDER BY
// clause end to end — the PQL order beats ?order_by=, ASC is the
// default, unknown/multi-key sort clauses are 400, and cursors are
// validated against the EFFECTIVE order (PQL ORDER BY if present,
// else the param), so a cursor minted under the other order is
// rejected, never silently honored.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"
)

func pqlOrderPage(t *testing.T, e *echo.Echo, cookie *http.Cookie, base, query string) ([]string, string, int, string) {
	t.Helper()
	rec := getAuthed(t, e, http.MethodGet, base+"?"+query, cookie)
	var res struct {
		Results []struct {
			Name string `json:"name"`
		} `json:"results"`
		NextCursor string `json:"next_cursor"`
	}
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
			t.Fatalf("decode: %v", err)
		}
	}
	names := make([]string, 0, len(res.Results))
	for _, r := range res.Results {
		names = append(names, r.Name)
	}
	return names, res.NextCursor, rec.Code, rec.Body.String()
}

func TestIssueListHTTPPQLOrderBy(t *testing.T) {
	_, e, cookie, base := setupBulkProject(t, "pqlorderby")

	// Distinct priorities + creation (sequence) order both known, so
	// a PQL order and a param order give visibly different sequences.
	// Created in sequence order p1, p3, p4 (not map iteration, which
	// would randomize the sequence ids).
	for _, iss := range []struct {
		name     string
		priority int
	}{{"p1", 1}, {"p3", 3}, {"p4", 4}} {
		rec := postAuthedJSON(t, e, http.MethodPost, base, cookie,
			fmt.Sprintf(`{"name":%q,"priority":%d}`, iss.name, iss.priority))
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %q: status = %d (body: %s)", iss.name, rec.Code, rec.Body.String())
		}
	}
	pql := func(q string) string { return "pql=" + url.QueryEscape(q) }

	// PQL ORDER BY beats ?order_by=: the param asks sequence ASC
	// (p1, p3, p4) but the query says priority DESC (p4, p3, p1).
	got, _, code, body := pqlOrderPage(t, e, cookie, base,
		pql("priority >= 0 ORDER BY priority DESC")+"&order_by=sequence_id&per_page=100")
	if code != http.StatusOK || strings.Join(got, ",") != "p4,p3,p1" {
		t.Fatalf("PQL ORDER BY beats param: status = %d got %v (body: %s), want [p4 p3 p1]", code, got, body)
	}

	// Direction defaults to ASC when omitted.
	got, _, code, body = pqlOrderPage(t, e, cookie, base,
		pql("priority >= 0 ORDER BY priority")+"&per_page=100")
	if code != http.StatusOK || strings.Join(got, ",") != "p1,p3,p4" {
		t.Fatalf("ORDER BY ASC default: status = %d got %v (body: %s), want [p1 p3 p4]", code, got, body)
	}

	// Case-insensitive keywords + explicit ASC.
	got, _, code, body = pqlOrderPage(t, e, cookie, base,
		pql("priority >= 0 order by priority asc")+"&per_page=100")
	if code != http.StatusOK || strings.Join(got, ",") != "p1,p3,p4" {
		t.Fatalf("lowercase order by asc: status = %d got %v (body: %s), want [p1 p3 p4]", code, got, body)
	}

	// Without ORDER BY, ?order_by= applies exactly as today.
	got, _, code, body = pqlOrderPage(t, e, cookie, base,
		pql("priority >= 0")+"&order_by=-sequence_id&per_page=100")
	if code != http.StatusOK || strings.Join(got, ",") != "p4,p3,p1" {
		t.Fatalf("no ORDER BY + param: status = %d got %v (body: %s), want [p4 p3 p1]", code, got, body)
	}

	// Cursor minted under the PQL order follows under the same order.
	_, cursor, code, body := pqlOrderPage(t, e, cookie, base,
		pql("priority >= 0 ORDER BY priority DESC")+"&per_page=1")
	if code != http.StatusOK || cursor == "" {
		t.Fatalf("PQL page 1: status = %d cursor = %q (body: %s), want a cursor", code, cursor, body)
	}
	got, _, code, body = pqlOrderPage(t, e, cookie, base,
		pql("priority >= 0 ORDER BY priority DESC")+"&per_page=1&cursor="+url.QueryEscape(cursor))
	if code != http.StatusOK || strings.Join(got, ",") != "p3" {
		t.Fatalf("follow PQL cursor: status = %d got %v (body: %s), want [p3]", code, got, body)
	}

	// Cursor minted under the PARAM order is rejected once the query
	// carries its own ORDER BY (effective order differs).
	_, paramCursor, code, body := pqlOrderPage(t, e, cookie, base,
		pql("priority >= 0")+"&order_by=sequence_id&per_page=1")
	if code != http.StatusOK || paramCursor == "" {
		t.Fatalf("param page 1: status = %d cursor = %q (body: %s), want a cursor", code, paramCursor, body)
	}
	_, _, code, body = pqlOrderPage(t, e, cookie, base,
		pql("priority >= 0 ORDER BY priority DESC")+"&cursor="+url.QueryEscape(paramCursor))
	if code != http.StatusBadRequest {
		t.Fatalf("PQL ORDER BY + param-order cursor: status = %d, want 400 (body: %s)", code, body)
	}

	// And the reverse: a PQL-order cursor is rejected under the param
	// order (no ORDER BY in the query this time).
	_, _, code, body = pqlOrderPage(t, e, cookie, base,
		pql("priority >= 0")+"&order_by=sequence_id&cursor="+url.QueryEscape(cursor))
	if code != http.StatusBadRequest {
		t.Fatalf("param order + PQL-order cursor: status = %d, want 400 (body: %s)", code, body)
	}

	// Unknown sort field is 400 with message + position. (Multi-key
	// ORDER BY is valid since C99T0; see pql_multi_order_c99t0_test.go.)
	for _, q := range []string{
		"priority >= 0 ORDER BY bogus",
	} {
		rec := getAuthed(t, e, http.MethodGet, base+"?"+pql(q), cookie)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("pql %q: status = %d, want 400 (body: %s)", q, rec.Code, rec.Body.String())
		}
		msg, _, hasPos := pqlErrorBody(t, rec.Body.String())
		if !hasPos {
			t.Fatalf("pql %q: message = %q, want a 1-based position", q, msg)
		}
	}
}
