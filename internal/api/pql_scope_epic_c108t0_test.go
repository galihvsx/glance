package api

// PQL epic-scope HTTP tests (C108T0): ?epic= survives PQL mode over
// HTTP — the scope param is parsed by parsePQLListInput and ANDs
// with the PQL predicate, exactly like ?cycle=/?module= (C95T0).
// Fixture rows are attached via SQL (the list endpoint is what's
// under test; epic creation already has its own HTTP tests).

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"testing"
)

func TestIssueListHTTPPQLEpicScope(t *testing.T) {
	pool, e, cookie, base := setupBulkProject(t, "pqlepicscope")
	ctx := context.Background()

	parts := strings.Split(base, "/")
	if len(parts) != 8 {
		t.Fatalf("base = %q, want /api/v1/workspaces/<slug>/projects/<ident>/issues", base)
	}
	slug, ident := parts[4], parts[6]
	var projectID string
	if err := pool.QueryRow(ctx,
		`SELECT p.id::text FROM projects p JOIN workspaces w ON w.id = p.workspace_id
		  WHERE w.slug = $1 AND p.identifier = $2`, slug, ident).Scan(&projectID); err != nil {
		t.Fatalf("project id: %v", err)
	}

	insertEpic := func(name string) string {
		var id string
		if err := pool.QueryRow(ctx,
			`INSERT INTO epics (project_id, name) VALUES ($1::uuid, $2) RETURNING id::text`,
			projectID, name).Scan(&id); err != nil {
			t.Fatalf("insert epic %s: %v", name, err)
		}
		return id
	}
	scopeID := insertEpic("ScopeEpic")
	otherID := insertEpic("OtherEpic")

	// A1, A2 in the scope epic; B in the other epic; C in no epic.
	// A2 and C are high priority so a priority predicate narrows
	// inside the scope (default priority is 0).
	a1 := createIssueHTTP(t, e, cookie, base, "scopeLow")
	a2 := createIssueHTTP(t, e, cookie, base, "scopeHigh")
	bID := createIssueHTTP(t, e, cookie, base, "otherHigh")
	cID := createIssueHTTP(t, e, cookie, base, "outsideHigh")
	for _, q := range []string{
		fmt.Sprintf(`INSERT INTO epic_issues (epic_id, issue_id) VALUES ('%s', '%s')`, scopeID, a1),
		fmt.Sprintf(`INSERT INTO epic_issues (epic_id, issue_id) VALUES ('%s', '%s')`, scopeID, a2),
		fmt.Sprintf(`INSERT INTO epic_issues (epic_id, issue_id) VALUES ('%s', '%s')`, otherID, bID),
		fmt.Sprintf(`UPDATE issues SET priority = 3 WHERE id IN ('%s', '%s')`, a2, cID),
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("fixture SQL %q: %v", q, err)
		}
	}

	suffix := "&order_by=-sequence_id&per_page=100"
	pql := func(pred string) string {
		return "pql=" + url.QueryEscape(pred) + suffix
	}
	want := func(got []string, names ...string) {
		t.Helper()
		sort.Strings(names)
		sorted := append([]string(nil), got...)
		sort.Strings(sorted)
		if strings.Join(sorted, "|") != strings.Join(names, "|") {
			t.Fatalf("got %v, want %v", sorted, names)
		}
	}

	// Invariant 1: scope + catch-all predicate returns only the
	// scope epic's issues.
	want(pqlListNamesHTTP(t, e, cookie, base, pql("priority >= 0")+"&epic="+scopeID), "scopeLow", "scopeHigh")
	// Invariant 1, genuine AND: scope + narrowing predicate returns
	// only the in-scope issue matching the predicate — not the
	// out-of-scope high-priority issue.
	want(pqlListNamesHTTP(t, e, cookie, base, pql("priority >= 2")+"&epic="+scopeID), "scopeHigh")
	// Predicate alone still sees the out-of-scope match (control).
	want(pqlListNamesHTTP(t, e, cookie, base, pql("priority >= 2")), "scopeHigh", "outsideHigh")
	// Scope param with the same epic named in the predicate: AND is
	// consistent, not a conflict.
	want(pqlListNamesHTTP(t, e, cookie, base, pql("epic = ScopeEpic")+"&epic="+scopeID), "scopeLow", "scopeHigh")
	// Invariant 4: predicate naming a DIFFERENT epic than the scope
	// param returns empty (AND), not the predicate's epic.
	want(pqlListNamesHTTP(t, e, cookie, base, pql("epic = OtherEpic")+"&epic="+scopeID))
	// PQL naming an epic with NO scope param behaves exactly as
	// today (C97T0 shape).
	want(pqlListNamesHTTP(t, e, cookie, base, pql("epic = OtherEpic")), "otherHigh")
	// The "none" sentinel survives as a scope too: only issues in
	// no epic.
	want(pqlListNamesHTTP(t, e, cookie, base, pql("priority >= 0")+"&epic=none"), "outsideHigh")
	// Repeat epic params OR inside the scope, still ANDed with the
	// predicate.
	want(pqlListNamesHTTP(t, e, cookie, base, pql("priority >= 0")+"&epic="+scopeID+"&epic="+otherID), "scopeLow", "scopeHigh", "otherHigh")
	// Foreign scope + PQL: empty list, still 200.
	want(pqlListNamesHTTP(t, e, cookie, base, pql("priority >= 0")+"&epic=11111111-1111-1111-1111-111111111111"))
	// Invariant 2: blank ?pql= is still "no PQL" — the visual epic
	// filter applies, not a PQL request.
	want(pqlListNamesHTTP(t, e, cookie, base, "pql=&epic="+scopeID+suffix), "scopeLow", "scopeHigh")
	// Visual epic filter without ?pql= is unchanged.
	want(pqlListNamesHTTP(t, e, cookie, base, "epic="+scopeID+suffix), "scopeLow", "scopeHigh")
	// Invariant 3: invalid epic values in PQL mode 400 honestly
	// (bad UUID, never a silent empty list).
	for _, bad := range []string{"not-a-uuid", "123"} {
		rec := getAuthed(t, e, http.MethodGet, base+"?"+pql("priority >= 0")+"&epic="+bad, cookie)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("malformed epic %q + pql: status = %d, want 400 (body: %s)", bad, rec.Code, rec.Body.String())
		}
	}
}
