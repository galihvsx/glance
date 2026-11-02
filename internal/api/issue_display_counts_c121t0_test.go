package api

// C121T0 API suite: the list + detail endpoints serialize
// attachment_count, link_count, and relation_count on every row,
// with zero present (not omitted) for bare issues. Real test
// database, no skips.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

type displayCountRow struct {
	ID              string `json:"id"`
	AttachmentCount int    `json:"attachment_count"`
	LinkCount       int    `json:"link_count"`
	RelationCount   int    `json:"relation_count"`
}

func TestIssueListDisplayCountsHTTP(t *testing.T) {
	pool := newTestPool(t)
	e := testServer(t, pool)
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterIssueLinkRoutes(e, &IssueHandler{Pool: pool})
	RegisterSatelliteRoutes(e, &IssueHandler{Pool: pool})

	// setupAIHTTP builds a user + workspace + project and returns an
	// authed cookie for them (reused here to avoid duplicating the
	// login boilerplate).
	cookie, slug, ident := setupAIHTTP(t, e, pool)
	base := "/api/v1/workspaces/" + slug + "/projects/" + ident
	issueBase := base + "/issues"

	rich := createIssueHTTP(t, e, cookie, issueBase, "rich")
	bare := createIssueHTTP(t, e, cookie, issueBase, "bare")
	other := createIssueHTTP(t, e, cookie, issueBase, "other")
	third := createIssueHTTP(t, e, cookie, issueBase, "third")

	// 2 attachments on rich (direct SQL: upload needs object storage).
	for _, name := range []string{"a.png", "b.png"} {
		if _, err := pool.Exec(context.Background(),
			`INSERT INTO attachments (issue_id, filename, content_type, size_bytes, stored_path, uploaded_by)
			 VALUES ($1::uuid, $2, 'image/png', 10, $3, (SELECT id FROM users LIMIT 1))`,
			rich, name, "test/"+name); err != nil {
			t.Fatalf("insert attachment %s: %v", name, err)
		}
	}
	// 1 link + 1 relation in each direction for rich.
	rec := postAuthedJSON(t, e, http.MethodPost, issueBase+"/"+rich+"/links", cookie,
		`{"target_issue_id":"`+other+`","kind":"blocks"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create link: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodPost, issueBase+"/"+rich+"/relations", cookie,
		`{"related_issue_id":"`+other+`","type":"blocked_by"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("create relation rich: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	rec = postAuthedJSON(t, e, http.MethodPost, issueBase+"/"+third+"/relations", cookie,
		`{"related_issue_id":"`+rich+`","type":"blocked_by"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("create relation third: status = %d (body: %s)", rec.Code, rec.Body.String())
	}

	rec = getAuthed(t, e, http.MethodGet, issueBase+"?per_page=100", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	// Zero must be serialized, not omitted: the bare row's raw JSON
	// carries all three zero fields.
	raw := rec.Body.String()
	if !strings.Contains(raw, `"attachment_count":0`) ||
		!strings.Contains(raw, `"link_count":0`) ||
		!strings.Contains(raw, `"relation_count":0`) {
		t.Fatalf("list JSON omits a zero count field: %s", raw)
	}
	var got struct {
		Results []displayCountRow `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	byID := map[string]displayCountRow{}
	for _, r := range got.Results {
		byID[r.ID] = r
	}
	want := map[string]displayCountRow{
		rich:  {ID: rich, AttachmentCount: 2, LinkCount: 1, RelationCount: 2},
		bare:  {ID: bare},
		other: {ID: other, LinkCount: 1, RelationCount: 1},
		third: {ID: third, RelationCount: 1},
	}
	for id, w := range want {
		r, ok := byID[id]
		if !ok {
			t.Fatalf("issue %s missing from list", id)
		}
		if r != w {
			t.Fatalf("list %s: counts = %+v, want %+v", id, r, w)
		}
	}

	// Detail shares the serializer: same real counts.
	rec = getAuthed(t, e, http.MethodGet, issueBase+"/"+rich, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("detail: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var detail displayCountRow
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if detail != want[rich] {
		t.Fatalf("detail rich: counts = %+v, want %+v", detail, want[rich])
	}
}
