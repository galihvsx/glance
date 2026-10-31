package api

// PQL parent + hasParent()/hasChildren() HTTP tests (C97T1): ?pql=
// on the issue list endpoint filters by parent issue key and by the
// hierarchy functions end to end, and unknown/malformed keys are 400
// with the key in the message. Parent links are attached via SQL (the
// list endpoint is what's under test; parent assignment already has
// its own HTTP tests).

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestIssueListHTTPPQLParent(t *testing.T) {
	pool, e, cookie, base := setupBulkProject(t, "pqlparent")
	ctx := context.Background()

	parts := strings.Split(base, "/")
	if len(parts) != 8 {
		t.Fatalf("base = %q, want /api/v1/workspaces/<slug>/projects/<ident>/issues", base)
	}
	ident := parts[6]

	// P: parent. C1, C2: live children. CA: archived child (must not
	// count for hasChildren, and never lists). T: top-level.
	pID := createIssueHTTP(t, e, cookie, base, "parP")
	c1ID := createIssueHTTP(t, e, cookie, base, "parC1")
	c2ID := createIssueHTTP(t, e, cookie, base, "parC2")
	caID := createIssueHTTP(t, e, cookie, base, "parCA")
	createIssueHTTP(t, e, cookie, base, "parT")
	for _, q := range []string{
		fmt.Sprintf(`UPDATE issues SET parent_id = '%s' WHERE id = '%s'`, pID, c1ID),
		fmt.Sprintf(`UPDATE issues SET parent_id = '%s' WHERE id = '%s'`, pID, c2ID),
		fmt.Sprintf(`UPDATE issues SET parent_id = '%s', archived_at = now() WHERE id = '%s'`, pID, caID),
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("fixture SQL %q: %v", q, err)
		}
	}
	var seq int
	if err := pool.QueryRow(ctx, `SELECT sequence_id FROM issues WHERE id = $1::uuid`, pID).Scan(&seq); err != nil {
		t.Fatalf("parent sequence: %v", err)
	}
	key := fmt.Sprintf("%s-%d", ident, seq)

	got := pqlListNamesHTTP(t, e, cookie, base, "pql="+url.QueryEscape("parent = "+key))
	if len(got) != 2 || !strings.Contains(strings.Join(got, ","), "parC1") || !strings.Contains(strings.Join(got, ","), "parC2") {
		t.Fatalf("parent = %s: got %v, want parC1 + parC2", key, got)
	}
	got = pqlListNamesHTTP(t, e, cookie, base, "pql="+url.QueryEscape("parent = none"))
	if len(got) != 2 || !strings.Contains(strings.Join(got, ","), "parP") || !strings.Contains(strings.Join(got, ","), "parT") {
		t.Fatalf("parent = none: got %v, want parP + parT", got)
	}
	got = pqlListNamesHTTP(t, e, cookie, base, "pql="+url.QueryEscape("hasParent()"))
	if len(got) != 2 || !strings.Contains(strings.Join(got, ","), "parC1") || !strings.Contains(strings.Join(got, ","), "parC2") {
		t.Fatalf("hasParent(): got %v, want parC1 + parC2", got)
	}
	// Only parP has live children: parCA is archived, so it counts
	// for nothing, and parP is still the sole hasChildren() hit.
	got = pqlListNamesHTTP(t, e, cookie, base, "pql="+url.QueryEscape("hasChildren()"))
	if len(got) != 1 || got[0] != "parP" {
		t.Fatalf("hasChildren(): got %v, want [parP]", got)
	}
	got = pqlListNamesHTTP(t, e, cookie, base, "pql="+url.QueryEscape("parent in ("+key+", none)"))
	if len(got) != 4 {
		t.Fatalf("parent in (key, none): got %v, want 4 issues", got)
	}

	// Unknown, foreign-identifier, and malformed keys: 400 naming
	// the key.
	for _, q := range []string{
		"parent = " + ident + "-99999",
		"parent = OTHER-1",
		"parent = NOPE",
	} {
		rec := getAuthed(t, e, http.MethodGet, base+"?pql="+url.QueryEscape(q), cookie)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("pql %q: status = %d, want 400 (body: %s)", q, rec.Code, rec.Body.String())
		}
		msg, _, _ := pqlErrorBody(t, rec.Body.String())
		want := strings.Split(q, " = ")[1]
		if !strings.Contains(msg, want) {
			t.Fatalf("pql %q: message = %q, want it to name %q", q, msg, want)
		}
	}
}
