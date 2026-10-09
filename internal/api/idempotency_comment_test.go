package api

// C2T8 bug bundle B: POST .../issues/{uuid}/comments honors
// Idempotency-Key. Same key twice → one comment, the second response
// byte-identical to the first. Real test database, no skips.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"

	"glance/internal/config"
)

// countComments returns the number of comments on an issue via the list
// endpoint.
func countComments(t *testing.T, e *echo.Echo, cookie *http.Cookie, commentsPath string) int {
	t.Helper()
	rec := getAuthed(t, e, http.MethodGet, commentsPath, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list comments: status = %d (body: %s)", rec.Code, rec.Body.String())
	}
	var tree struct {
		Comments []json.RawMessage `json:"comments"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &tree); err != nil {
		t.Fatalf("decode comments: %v", err)
	}
	return len(tree.Comments)
}

// setupCommentProject creates a user, workspace, project and issue on a
// server with satellite (comment) routes registered up front, and
// returns the session cookie and the comments base path.
func setupCommentProject(t *testing.T, prefix string) (*echo.Echo, *http.Cookie, string) {
	t.Helper()
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	e := echo.New()
	RegisterAuthRoutes(e, &AuthHandler{Pool: pool, Config: &config.Config{OTPPepper: "test-pepper-do-not-use-in-prod"}})
	RegisterWorkspaceRoutes(e, &WorkspaceHandler{Pool: pool})
	RegisterProjectRoutes(e, &ProjectHandler{Pool: pool})
	RegisterIssueRoutes(e, &IssueHandler{Pool: pool})
	RegisterSatelliteRoutes(e, &IssueHandler{Pool: pool})

	cookie := loginTestUser(t, e, pool, uniqueEmail(prefix), "test-agent", uniqueIP())
	slug := uniqueSlug(prefix)
	createWorkspaceHTTP(t, e, cookie, "Comment Co", slug)
	ident := uniqueProjectIdentifier(strings.ToUpper(prefix[:2]))
	createProjectHTTP(t, e, cookie, slug, "Engineering", ident)
	base := "/api/v1/workspaces/" + slug + "/projects/" + ident + "/issues"
	issueID := createIssueHTTP(t, e, cookie, base, "commented")
	return e, cookie, base + "/" + issueID + "/comments"
}

// TestIdempotentCommentCreate: same Idempotency-Key twice → one comment,
// byte-identical replay. A different key creates a second comment; no
// key behaves as before (unprotected).
func TestIdempotentCommentCreate(t *testing.T) {
	e, cookie, commentsPath := setupCommentProject(t, "idem-comment")

	h := map[string]string{IdempotencyKeyHeader: "comment-key-1"}
	body := `{"content":"hello"}`
	rec1 := postAuthedJSONHeaders(t, e, http.MethodPost, commentsPath, cookie, body, h)
	if rec1.Code != http.StatusCreated {
		t.Fatalf("first: status = %d (body: %s)", rec1.Code, rec1.Body.String())
	}
	rec2 := postAuthedJSONHeaders(t, e, http.MethodPost, commentsPath, cookie, body, h)
	if rec2.Code != http.StatusCreated {
		t.Fatalf("replay: status = %d (body: %s)", rec2.Code, rec2.Body.String())
	}
	if !bytes.Equal(rec1.Body.Bytes(), rec2.Body.Bytes()) {
		t.Fatalf("replay body differs:\nfirst:  %s\nsecond: %s", rec1.Body.Bytes(), rec2.Body.Bytes())
	}
	if n := countComments(t, e, cookie, commentsPath); n != 1 {
		t.Fatalf("comments = %d, want 1 (double-POST with same key must not duplicate)", n)
	}

	// A different key creates a second comment.
	h2 := map[string]string{IdempotencyKeyHeader: "comment-key-2"}
	rec3 := postAuthedJSONHeaders(t, e, http.MethodPost, commentsPath, cookie, body, h2)
	if rec3.Code != http.StatusCreated {
		t.Fatalf("second key: status = %d (body: %s)", rec3.Code, rec3.Body.String())
	}
	if n := countComments(t, e, cookie, commentsPath); n != 2 {
		t.Fatalf("comments = %d, want 2 after distinct key", n)
	}

	// No key at all: unprotected path still works (creates a third).
	rec4 := postAuthedJSON(t, e, http.MethodPost, commentsPath, cookie, body)
	if rec4.Code != http.StatusCreated {
		t.Fatalf("no key: status = %d (body: %s)", rec4.Code, rec4.Body.String())
	}
	if n := countComments(t, e, cookie, commentsPath); n != 3 {
		t.Fatalf("comments = %d, want 3 after keyless POST", n)
	}
}
