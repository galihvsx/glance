package api

// PQL HTTP tests (C91T0): ?pql= on the issue list endpoint end to
// end — filtered results, 400 with message + position on parse and
// unknown-name failures, and the exclusivity rule (visual params
// ignored under PQL, honored under a blank pql).

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"
)

func pqlListNamesHTTP(t *testing.T, e *echo.Echo, cookie *http.Cookie, base, query string) []string {
	t.Helper()
	rec := getAuthed(t, e, http.MethodGet, base+"?"+query, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: status = %d (body: %s)", query, rec.Code, rec.Body.String())
	}
	var res struct {
		Results []struct {
			Name string `json:"name"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	names := make([]string, 0, len(res.Results))
	for _, r := range res.Results {
		names = append(names, r.Name)
	}
	return names
}

func pqlErrorBody(t *testing.T, body string) (message string, position float64, hasPosition bool) {
	t.Helper()
	var env struct {
		Error struct {
			Code    string         `json:"code"`
			Message string         `json:"message"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if env.Error.Code != "bad_request" {
		t.Fatalf("error code = %q, want bad_request (body: %s)", env.Error.Code, body)
	}
	if p, ok := env.Error.Details["position"]; ok {
		if f, ok := p.(float64); ok {
			return env.Error.Message, f, true
		}
	}
	return env.Error.Message, 0, false
}

func TestIssueListHTTPPQL(t *testing.T) {
	_, e, cookie, base := setupBulkProject(t, "pqlhttp")

	for name, priority := range map[string]int{"hip": 3, "lop": 1, "urg": 4} {
		rec := postAuthedJSON(t, e, http.MethodPost, base, cookie,
			fmt.Sprintf(`{"name":%q,"priority":%d}`, name, priority))
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %q: status = %d (body: %s)", name, rec.Code, rec.Body.String())
		}
	}

	// A valid PQL query filters; sort + pagination still apply.
	got := pqlListNamesHTTP(t, e, cookie, base,
		"pql="+url.QueryEscape("priority >= 3")+"&order_by=-sequence_id&per_page=1")
	if len(got) != 1 {
		t.Fatalf("pql + per_page=1: got %v, want exactly 1 row", got)
	}
	got = pqlListNamesHTTP(t, e, cookie, base, "pql="+url.QueryEscape("priority >= 3"))
	if len(got) != 2 || !strings.Contains(strings.Join(got, ","), "hip") || !strings.Contains(strings.Join(got, ","), "urg") {
		t.Fatalf("priority >= 3: got %v, want hip + urg", got)
	}

	// Parse failure: 400 with message + position (never 500).
	rec := getAuthed(t, e, http.MethodGet, base+"?pql="+url.QueryEscape("state ="), cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("parse error: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	msg, pos, hasPos := pqlErrorBody(t, rec.Body.String())
	if !hasPos || pos < 1 {
		t.Fatalf("parse error: message = %q position present = %v (%v), want a 1-based position", msg, hasPos, pos)
	}

	// Unknown name: 400 naming the name.
	rec = getAuthed(t, e, http.MethodGet, base+"?pql="+url.QueryEscape("state = NoSuchState"), cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown state: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	msg, _, _ = pqlErrorBody(t, rec.Body.String())
	if !strings.Contains(msg, "NoSuchState") {
		t.Fatalf("unknown state: message = %q, want it to name NoSuchState", msg)
	}

	// Exclusivity: a malformed visual param cannot 400 a PQL request.
	got = pqlListNamesHTTP(t, e, cookie, base,
		"pql="+url.QueryEscape("priority = 3")+"&priority=abc")
	if len(got) != 1 || got[0] != "hip" {
		t.Fatalf("pql + malformed visual priority: got %v, want [hip]", got)
	}

	// Blank pql is absent: the visual filter applies as usual.
	got = pqlListNamesHTTP(t, e, cookie, base, "pql=&priority=1")
	if len(got) != 1 || got[0] != "lop" {
		t.Fatalf("blank pql + priority=1: got %v, want [lop]", got)
	}
}
