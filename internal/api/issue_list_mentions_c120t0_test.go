package api

// C120T0: parse-level + HTTP tests for the new ?mentions=
// visual list filter. Parse: repeatable AND comma-separated
// via parseRepeatIDList, NO plural alias, malformed UUIDs
// 400 naming the param, "none" passes the parser through
// AND the service (the assignee none-passthrough branch,
// NOT the created_by rejection branch) — proved by the HTTP
// tests below, where mentions=none matches issues with no
// mention rows. HTTP: the project list endpoint round-trips
// the filter (repeat + comma-separated, none, malformed
// 400), and the export endpoint honors it (it shares
// parseIssueListInput). Filter semantics live in the service
// tests (issue_mentions_filter_c120t0_test.go,
// pql_mentions_c120t0_test.go); this file pins the HTTP
// envelope.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"testing"

	"glance/internal/service"
)

func TestParseIssueListInputMentions(t *testing.T) {
	uuid1 := "11111111-1111-1111-1111-111111111111"
	uuid2 := "22222222-2222-2222-2222-222222222222"

	// Repeatable + comma-separated values merge, whitespace trimmed.
	in, err := parseIssueListInput(url.Values{
		"mentions": {uuid1 + "," + uuid2, " " + uuid1},
	})
	if err != nil {
		t.Fatalf("parse mentions: %v", err)
	}
	if !reflect.DeepEqual(in.FilterMentions, []string{uuid1, uuid2, uuid1}) {
		t.Fatalf("FilterMentions = %v, want [%s %s %s]", in.FilterMentions, uuid1, uuid2, uuid1)
	}

	// Empty segments are dropped; an all-empty param is no filter.
	in, err = parseIssueListInput(url.Values{"mentions": {" ,,"}})
	if err != nil {
		t.Fatalf("parse empty mentions: %v", err)
	}
	if len(in.FilterMentions) != 0 {
		t.Fatalf("FilterMentions = %v, want empty (no filter)", in.FilterMentions)
	}

	// There is NO plural ?mentionss= alias: it is ignored,
	// never leaked into the filter (do not invent one).
	in, err = parseIssueListInput(url.Values{"mentionss": {uuid1}})
	if err != nil {
		t.Fatalf("parse plural mentionss: %v", err)
	}
	if len(in.FilterMentions) != 0 {
		t.Fatalf("plural mentionss leaked into filter: %v", in.FilterMentions)
	}

	// "none" passes this parser through — and, unlike
	// created_by, the SERVICE keeps it as the sentinel (the
	// HTTP tests below prove mentions=none returns the
	// unmentioned issues instead of 400ing).
	in, err = parseIssueListInput(url.Values{"mentions": {"none"}})
	if err != nil {
		t.Fatalf("parse none passthrough: %v", err)
	}
	if !reflect.DeepEqual(in.FilterMentions, []string{"none"}) {
		t.Fatalf("FilterMentions = %v, want [none]", in.FilterMentions)
	}

	// Malformed UUIDs are 400-class errors naming the param and
	// the bad value — never silently ignored, never a 500.
	for _, tc := range []struct {
		label string
		qp    url.Values
	}{
		{"mentions", url.Values{"mentions": {"not-a-uuid"}}},
		{"mentions-mixed", url.Values{"mentions": {uuid1 + ",bogus"}}},
		{"mentions-mixed-none", url.Values{"mentions": {"none,bogus"}}},
	} {
		_, err := parseIssueListInput(tc.qp)
		if err == nil {
			t.Fatalf("parse %s with malformed UUID: no error", tc.label)
		}
		if !strings.Contains(err.Error(), "invalid mentions") {
			t.Fatalf("parse %s malformed UUID: error %q does not name the param", tc.label, err.Error())
		}
	}
}

func TestIssueListHTTPMentions(t *testing.T) {
	pool, e, cookie, base := setupBulkProject(t, "mhttp")
	ctx := context.Background()

	parts := strings.Split(base, "/")
	if len(parts) != 8 {
		t.Fatalf("base = %q, want /api/v1/workspaces/<slug>/projects/<ident>/issues", base)
	}
	slug, ident := parts[4], parts[6]
	var wsID string
	if err := pool.QueryRow(ctx,
		`SELECT w.id::text FROM workspaces w WHERE w.slug = $1`, slug).Scan(&wsID); err != nil {
		t.Fatalf("workspace id: %v", err)
	}

	// Bob: workspace member ("Bob Builder") mentioned by the
	// main user's comments on two issues.
	bobEmail := uniqueEmail("m-http-bob")
	loginTestUser(t, e, pool, bobEmail, "test-agent", uniqueIP())
	var bobID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users WHERE email = $1`, bobEmail).Scan(&bobID); err != nil {
		t.Fatalf("bob id: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO workspace_members (workspace_id, user_id, role) VALUES ($1::uuid, $2::uuid, 15)`,
		wsID, bobID); err != nil {
		t.Fatalf("bob membership: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE users SET name = 'Bob Builder' WHERE id = $1::uuid`, bobID); err != nil {
		t.Fatalf("name Bob: %v", err)
	}
	// The main setup user is the comment author; resolve its id.
	var mainID string
	if err := pool.QueryRow(ctx, `SELECT m.user_id::text FROM workspace_members m WHERE m.workspace_id = $1::uuid AND m.role = 20 LIMIT 1`, wsID).Scan(&mainID); err != nil {
		t.Fatalf("main id: %v", err)
	}

	commentMentioningBob := json.RawMessage(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"hey @Bob Builder take a look"}]}]}`)
	mkIssue := func(name string, mention bool) {
		t.Helper()
		iss, err := service.CreateIssue(ctx, pool, slug, ident, mainID, service.CreateIssueInput{Name: name})
		if err != nil {
			t.Fatalf("CreateIssue %q: %v", name, err)
		}
		if mention {
			if _, err := service.CreateComment(ctx, pool, slug, ident, iss.ID, mainID, commentMentioningBob, nil); err != nil {
				t.Fatalf("CreateComment on %q: %v", name, err)
			}
		}
	}
	mkIssue("mA", true)
	mkIssue("mB", false)
	mkIssue("mC", true)

	suffix := "&order_by=-sequence_id&per_page=100"
	want := func(got []string, names ...string) {
		t.Helper()
		sort.Strings(names)
		sorted := append([]string(nil), got...)
		sort.Strings(sorted)
		if strings.Join(sorted, "|") != strings.Join(names, "|") {
			t.Fatalf("got %v, want %v", sorted, names)
		}
	}

	want(pqlListNamesHTTP(t, e, cookie, base, "mentions="+bobID+suffix), "mA", "mC")
	want(pqlListNamesHTTP(t, e, cookie, base, "mentions="+bobID+",none"+suffix), "mA", "mB", "mC")
	want(pqlListNamesHTTP(t, e, cookie, base, "mentions="+bobID+"&mentions=none"+suffix), "mA", "mB", "mC")
	want(pqlListNamesHTTP(t, e, cookie, base, "mentions=none"+suffix), "mB")
	// Empty = no filter.
	want(pqlListNamesHTTP(t, e, cookie, base, suffix[1:]), "mA", "mB", "mC")
	// Malformed values 400 on the list endpoint.
	rec := getAuthed(t, e, http.MethodGet, base+"?mentions=not-a-uuid"+suffix, cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed mentions: status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	// The export endpoint shares parseIssueListInput, so the
	// filter applies there too (spot check).
	rec = getExport(t, e, base+"/export?format=csv&mentions="+bobID, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("export mentions: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	recs := parseCSVBody(t, rec.Body.String())
	names := make([]string, 0, len(recs)-1)
	for _, r := range recs[1:] {
		names = append(names, r[1])
	}
	sort.Strings(names)
	if strings.Join(names, "|") != "mA|mC" {
		t.Fatalf("export mentions filter: got %v, want [mA mC]", names)
	}
}
