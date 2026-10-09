package api

// C8T7 duplicate detection: GET .../issues/similar?q=. Real test
// database, no skips.

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
)

// TestIssueHTTPSimilar: similar?q= → 200 with ranked hits (most similar
// first, similarity > 0.3, archived/draft excluded); missing or blank q
// → 400; the static /similar segment does not shadow GET /:uuid.
func TestIssueHTTPSimilar(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := testIssueServer(t, pool)

	cookie := loginTestUser(t, e, pool, uniqueEmail("issue-sim-http"), "test-agent", uniqueIP())

	slug := uniqueSlug("issue-sim-http")
	createWorkspaceHTTP(t, e, cookie, "Issue Co", slug)
	ident := uniqueProjectIdentifier("HS")
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	base := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/issues"

	createIssueHTTP(t, e, cookie, base, "Fix the login redirect loop")
	createIssueHTTP(t, e, cookie, base, "Login redirect loop fix")
	createIssueHTTP(t, e, cookie, base, "Completely unrelated billing report")

	// Ranked hits, most-similar first.
	rec := getAuthed(t, e, http.MethodGet, base+"/similar?q="+url.QueryEscape("Fix login redirect loop"), cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("similar: status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var got []struct {
		ID         string  `json:"id"`
		DisplayID  string  `json:"display_id"`
		Name       string  `json:"name"`
		State      string  `json:"state"`
		Similarity float64 `json:"similarity"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode similar: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("similar: got %d results, want 2", len(got))
	}
	if got[0].Similarity < got[1].Similarity {
		t.Fatalf("similar: not ranked most-similar-first: %v then %v", got[0].Similarity, got[1].Similarity)
	}
	for _, s := range got {
		if s.ID == "" || s.DisplayID == "" || s.Name == "" || s.State == "" {
			t.Fatalf("similar: incomplete hit: %+v", s)
		}
		if s.Similarity <= 0.3 {
			t.Fatalf("similar: hit below threshold: %+v", s)
		}
	}

	// Missing q and blank q → 400.
	for _, path := range []string{base + "/similar", base + "/similar?q=" + url.QueryEscape("   ")} {
		rec := getAuthed(t, e, http.MethodGet, path, cookie)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("GET %s: status = %d, want 400 (body: %s)", path, rec.Code, rec.Body.String())
		}
	}

	// A uuid-like path still hits getIssue, not /similar.
	rec = getAuthed(t, e, http.MethodGet, base+"/123e4567-e89b-12d3-a456-426614174000", cookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("uuid path: status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
}
