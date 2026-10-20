package api

// C80T0: parse-level tests for ?due_date_empty= on the issue list.
// parseIssueListInput is shared by the list and export endpoints, so a
// clear 400 here covers both (C73/C74 ruling).

import (
	"net/url"
	"testing"
)

func TestParseIssueListInputDueDateEmpty(t *testing.T) {
	// "1" and "true" both select the filter.
	for _, raw := range []string{"1", "true"} {
		in, err := parseIssueListInput(url.Values{"due_date_empty": {raw}})
		if err != nil {
			t.Fatalf("parse due_date_empty=%q: %v", raw, err)
		}
		if !in.DueDateEmpty {
			t.Fatalf("due_date_empty=%q: DueDateEmpty = false, want true", raw)
		}
	}

	// Absent = no filter (default list behavior unchanged).
	in, err := parseIssueListInput(url.Values{})
	if err != nil {
		t.Fatalf("parse empty: %v", err)
	}
	if in.DueDateEmpty {
		t.Fatalf("absent due_date_empty: DueDateEmpty = true, want false")
	}

	// Combining the empty filter with a range on the same field is a
	// 400 — the two predicates contradict each other.
	for _, tc := range []struct {
		label string
		qp    url.Values
	}{
		{"due_after", url.Values{"due_date_empty": {"1"}, "due_after": {"2026-10-01T00:00:00Z"}}},
		{"due_before", url.Values{"due_date_empty": {"1"}, "due_before": {"2026-11-01T00:00:00Z"}}},
		{"both ranges", url.Values{"due_date_empty": {"true"}, "due_after": {"2026-10-01T00:00:00Z"}, "due_before": {"2026-11-01T00:00:00Z"}}},
	} {
		if _, err := parseIssueListInput(tc.qp); err == nil {
			t.Fatalf("due_date_empty + %s: want a 400-class error, got nil", tc.label)
		}
	}

	// due_date_empty + undated is allowed: plain AND, just redundant
	// (both require target_date IS NULL).
	in, err = parseIssueListInput(url.Values{"due_date_empty": {"1"}, "undated": {"1"}})
	if err != nil {
		t.Fatalf("due_date_empty + undated: %v", err)
	}
	if !in.DueDateEmpty || !in.Undated {
		t.Fatalf("due_date_empty + undated: flags = %v/%v, want both true", in.DueDateEmpty, in.Undated)
	}

	// due_date_empty composes at parse level with the other dimensions.
	in, err = parseIssueListInput(url.Values{
		"due_date_empty": {"1"},
		"state":          {"11111111-1111-1111-1111-111111111111"},
		"assignee":       {"22222222-2222-2222-2222-222222222222"},
	})
	if err != nil {
		t.Fatalf("parse combined: %v", err)
	}
	if !in.DueDateEmpty || in.State != "11111111-1111-1111-1111-111111111111" ||
		len(in.Assignees) != 1 || in.Assignees[0] != "22222222-2222-2222-2222-222222222222" {
		t.Fatalf("combined parse = %+v, want all filters carried", in)
	}
}
