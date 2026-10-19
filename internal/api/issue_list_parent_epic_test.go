package api

// C75T0: parse-level tests for the new ?parent= / ?epic= list filters.
// parseIssueListInput is shared by the list and export endpoints, so a
// clear 400 here covers both (C73/C74 ruling).

import (
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func TestParseIssueListInputParentEpic(t *testing.T) {
	uuid1 := "11111111-1111-1111-1111-111111111111"
	uuid2 := "22222222-2222-2222-2222-222222222222"

	// Repeatable + comma-separated values merge, whitespace trimmed.
	in, err := parseIssueListInput(url.Values{
		"parent": {uuid1 + "," + uuid2, " none "},
		"epic":   {uuid1},
	})
	if err != nil {
		t.Fatalf("parse parent/epic: %v", err)
	}
	if !reflect.DeepEqual(in.Parents, []string{uuid1, uuid2, "none"}) {
		t.Fatalf("Parents = %v, want [%s %s none]", in.Parents, uuid1, uuid2)
	}
	if !reflect.DeepEqual(in.Epics, []string{uuid1}) {
		t.Fatalf("Epics = %v, want [%s]", in.Epics, uuid1)
	}

	// "none" alone parses (the top-level / no-epic sentinel).
	in, err = parseIssueListInput(url.Values{"parent": {"none"}, "epic": {"none"}})
	if err != nil {
		t.Fatalf("parse none sentinels: %v", err)
	}
	if !reflect.DeepEqual(in.Parents, []string{"none"}) || !reflect.DeepEqual(in.Epics, []string{"none"}) {
		t.Fatalf("sentinels = %v / %v, want [none]", in.Parents, in.Epics)
	}

	// Empty segments are dropped; an all-empty param is no filter.
	in, err = parseIssueListInput(url.Values{"parent": {" ,,"}})
	if err != nil {
		t.Fatalf("parse empty parent: %v", err)
	}
	if len(in.Parents) != 0 {
		t.Fatalf("Parents = %v, want empty (no filter)", in.Parents)
	}

	// Malformed UUIDs are 400-class errors naming the param and the bad
	// value — never silently ignored, never a 500.
	for _, tc := range []struct {
		label string
		param string
		qp    url.Values
	}{
		{"parent", "parent", url.Values{"parent": {"not-a-uuid"}}},
		{"epic", "epic", url.Values{"epic": {"definitely-not-a-uuid"}}},
		{"parent-mixed", "parent", url.Values{"parent": {uuid1 + ",bogus"}}},
	} {
		_, err := parseIssueListInput(tc.qp)
		if err == nil {
			t.Fatalf("parse %s with malformed UUID: no error", tc.label)
		}
		if !strings.Contains(err.Error(), "invalid "+tc.param) {
			t.Fatalf("parse %s malformed UUID: error %q does not name the param", tc.label, err.Error())
		}
		if !strings.Contains(err.Error(), `"none"`) {
			t.Fatalf("parse %s malformed UUID: error %q does not mention the none sentinel", tc.label, err.Error())
		}
	}
}
