package api

// C119T0: parse-level + HTTP tests for the new
// ?state_group= visual list filter and the `state_group`
// PQL field. Parse: repeatable AND comma-separated via
// the parseRepeatIDList idiom (but slugs, not UUIDs —
// parseRepeatSlugList, never the UUID check),
// case-insensitive input normalized to lowercase, NO
// plural alias, "none"/"triage"/unknown values pass the
// parser through and are rejected by the service
// (validateListFilterValues) — the label-filter layering
// from C118. HTTP: the project list endpoint round-trips
// the filter (repeat + comma-separated, case-insensitive,
// 400 on unknown/triage/none). PQL semantics live in the
// service tests (pql_stategroup_c119t0_test.go); this
// file pins the HTTP envelope.

import (
	"context"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"testing"

	"glance/internal/service"
)

func TestParseIssueListInputStateGroup(t *testing.T) {
	// Repeatable + comma-separated values merge, whitespace trimmed.
	in, err := parseIssueListInput(url.Values{
		"state_group": {"started,completed", " backlog"},
	})
	if err != nil {
		t.Fatalf("parse state_group: %v", err)
	}
	if !reflect.DeepEqual(in.FilterStateGroups, []string{"started", "completed", "backlog"}) {
		t.Fatalf("FilterStateGroups = %v, want [started completed backlog]", in.FilterStateGroups)
	}

	// Case-insensitive input is normalized to lowercase.
	in, err = parseIssueListInput(url.Values{
		"state_group": {"Started,COMPLETED"},
	})
	if err != nil {
		t.Fatalf("parse mixed-case state_group: %v", err)
	}
	if !reflect.DeepEqual(in.FilterStateGroups, []string{"started", "completed"}) {
		t.Fatalf("FilterStateGroups = %v, want [started completed] (lowercased)", in.FilterStateGroups)
	}

	// Empty segments are dropped; an all-empty param is no filter.
	in, err = parseIssueListInput(url.Values{"state_group": {" ,,"}})
	if err != nil {
		t.Fatalf("parse empty state_group: %v", err)
	}
	if len(in.FilterStateGroups) != 0 {
		t.Fatalf("FilterStateGroups = %v, want empty (no filter)", in.FilterStateGroups)
	}

	// There is NO plural ?state_groups= alias: it is ignored,
	// never leaked into the filter (do not invent one).
	in, err = parseIssueListInput(url.Values{"state_groups": {"started"}})
	if err != nil {
		t.Fatalf("parse plural state_groups: %v", err)
	}
	if len(in.FilterStateGroups) != 0 {
		t.Fatalf("plural state_groups leaked into filter: %v", in.FilterStateGroups)
	}

	// "none", "triage", and unknown slugs pass the parser
	// through (lowercased) — the service rejects them.
	for _, v := range []string{"none", "triage", "bogus"} {
		in, err = parseIssueListInput(url.Values{"state_group": {v}})
		if err != nil {
			t.Fatalf("parse state_group=%s: %v (want passthrough)", v, err)
		}
		if !reflect.DeepEqual(in.FilterStateGroups, []string{v}) {
			t.Fatalf("FilterStateGroups = %v, want [%s] (service rejects it)", in.FilterStateGroups, v)
		}
	}
}

func TestIssueListHTTPStateGroup(t *testing.T) {
	pool, e, cookie, base := setupBulkProject(t, "sghttp")
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
	// The main setup user is the author; resolve its id.
	var mainID string
	if err := pool.QueryRow(ctx, `SELECT m.user_id::text FROM workspace_members m WHERE m.workspace_id = $1::uuid AND m.role = 20 LIMIT 1`, wsID).Scan(&mainID); err != nil {
		t.Fatalf("main id: %v", err)
	}

	// One issue per group, created via the service with an
	// explicit state (the HTTP create path always lands in
	// the backlog state).
	states, err := service.ListStates(ctx, pool, slug, ident, mainID)
	if err != nil {
		t.Fatalf("ListStates: %v", err)
	}
	byGroup := map[string]string{}
	for _, st := range states {
		byGroup[st.Group] = st.ID
	}
	for group, name := range map[string]string{
		"backlog":   "sgBacklog",
		"unstarted": "sgTodo",
		"started":   "sgStarted",
		"completed": "sgDone",
		"cancelled": "sgCancelled",
	} {
		sid := byGroup[group]
		if sid == "" {
			t.Fatalf("no state for group %q", group)
		}
		if _, err := service.CreateIssue(ctx, pool, slug, ident, mainID, service.CreateIssueInput{Name: name, StateID: &sid}); err != nil {
			t.Fatalf("CreateIssue %q: %v", name, err)
		}
	}

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

	want(pqlListNamesHTTP(t, e, cookie, base, "state_group=started"+suffix), "sgStarted")
	want(pqlListNamesHTTP(t, e, cookie, base, "state_group=started,completed"+suffix), "sgStarted", "sgDone")
	want(pqlListNamesHTTP(t, e, cookie, base, "state_group=started&state_group=cancelled"+suffix), "sgStarted", "sgCancelled")
	// Case-insensitive at the HTTP layer too.
	want(pqlListNamesHTTP(t, e, cookie, base, "state_group=Started"+suffix), "sgStarted")
	// Empty = no filter.
	want(pqlListNamesHTTP(t, e, cookie, base, suffix[1:]), "sgBacklog", "sgTodo", "sgStarted", "sgDone", "sgCancelled")
	// Unknown, triage, and none 400 on the list endpoint
	// (service validation — never a silent empty list).
	for _, v := range []string{"bogus", "triage", "none"} {
		rec := getAuthed(t, e, http.MethodGet, base+"?state_group="+v+suffix, cookie)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("state_group=%s: status = %d, want 400 (body: %s)", v, rec.Code, rec.Body.String())
		}
	}
}
