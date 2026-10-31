package api

// PQL epic + estimate HTTP tests (C97T0): ?pql= on the issue list
// endpoint filters by epic name and estimate-point key end to end,
// and unknown names are 400 with the name in the message. Fixture
// rows are attached via SQL (the list endpoint is what's under test;
// epic/estimate creation already has its own HTTP tests).

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestIssueListHTTPPQLEpicEstimate(t *testing.T) {
	pool, e, cookie, base := setupBulkProject(t, "pqlepicest")
	ctx := context.Background()

	// Resolve the project id from the base path so the fixture rows
	// can be inserted directly.
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

	var epicID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO epics (project_id, name) VALUES ($1::uuid, 'Platform') RETURNING id::text`,
		projectID).Scan(&epicID); err != nil {
		t.Fatalf("insert epic: %v", err)
	}
	var estID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO estimates (project_id, name) VALUES ($1::uuid, 'Fib') RETURNING id::text`,
		projectID).Scan(&estID); err != nil {
		t.Fatalf("insert estimate: %v", err)
	}
	var pointS, pointM string
	if err := pool.QueryRow(ctx,
		`INSERT INTO estimate_points (estimate_id, key, value) VALUES ($1::uuid, 'S', 1) RETURNING id::text`,
		estID).Scan(&pointS); err != nil {
		t.Fatalf("insert point S: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO estimate_points (estimate_id, key, value) VALUES ($1::uuid, 'M', 3) RETURNING id::text`,
		estID).Scan(&pointM); err != nil {
		t.Fatalf("insert point M: %v", err)
	}

	// A: in epic + estimate S. B: in epic, no estimate. C: no epic,
	// estimate M. D: neither.
	aID := createIssueHTTP(t, e, cookie, base, "epicA")
	bID := createIssueHTTP(t, e, cookie, base, "epicB")
	cID := createIssueHTTP(t, e, cookie, base, "epicC")
	createIssueHTTP(t, e, cookie, base, "epicD")
	for _, q := range []string{
		fmt.Sprintf(`INSERT INTO epic_issues (epic_id, issue_id) VALUES ('%s', '%s')`, epicID, aID),
		fmt.Sprintf(`INSERT INTO epic_issues (epic_id, issue_id) VALUES ('%s', '%s')`, epicID, bID),
		fmt.Sprintf(`UPDATE issues SET estimate_point_id = '%s' WHERE id = '%s'`, pointS, aID),
		fmt.Sprintf(`UPDATE issues SET estimate_point_id = '%s' WHERE id = '%s'`, pointM, cID),
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("fixture SQL %q: %v", q, err)
		}
	}
	got := pqlListNamesHTTP(t, e, cookie, base, "pql="+url.QueryEscape("epic = Platform"))
	if len(got) != 2 || !strings.Contains(strings.Join(got, ","), "epicA") || !strings.Contains(strings.Join(got, ","), "epicB") {
		t.Fatalf("epic = Platform: got %v, want epicA + epicB", got)
	}
	got = pqlListNamesHTTP(t, e, cookie, base, "pql="+url.QueryEscape("epic = none"))
	if len(got) != 2 || !strings.Contains(strings.Join(got, ","), "epicC") || !strings.Contains(strings.Join(got, ","), "epicD") {
		t.Fatalf("epic = none: got %v, want epicC + epicD", got)
	}
	got = pqlListNamesHTTP(t, e, cookie, base, "pql="+url.QueryEscape("estimate = S"))
	if len(got) != 1 || got[0] != "epicA" {
		t.Fatalf("estimate = S: got %v, want [epicA]", got)
	}
	got = pqlListNamesHTTP(t, e, cookie, base, "pql="+url.QueryEscape("estimate = none"))
	if len(got) != 2 || !strings.Contains(strings.Join(got, ","), "epicB") || !strings.Contains(strings.Join(got, ","), "epicD") {
		t.Fatalf("estimate = none: got %v, want epicB + epicD", got)
	}
	got = pqlListNamesHTTP(t, e, cookie, base, "pql="+url.QueryEscape("epic = Platform AND estimate = none"))
	if len(got) != 1 || got[0] != "epicB" {
		t.Fatalf("epic AND estimate=none: got %v, want [epicB]", got)
	}

	// Unknown names: 400 naming the name.
	for _, q := range []string{"epic = NoSuchEpic", "estimate = XXL"} {
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
