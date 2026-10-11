package service

// GitHub two-way linkage tests (C17T0): manual link/unlink (validation,
// duplicates, RBAC), the detail-response embedding, the write-only
// workspace webhook secret, and the HMAC-verified inbound webhook
// (auto-link on opened, state updates on closed, idempotent re-delivery,
// bad-signature 401 path). Real test database, no skips.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// linkTestSetup creates a workspace (admin), a project, and one issue,
// returning the ids plus the issue's display ID (e.g. GLC-1).
func linkTestSetup(t *testing.T, pool *pgxpool.Pool, prefix string) (wsSlug, ident, issueID, adminID, displayID string) {
	t.Helper()
	adminID = createTestUser(t, pool, uniqueTestEmail(prefix+"-admin"))
	wsSlug = uniqueTestSlug(prefix + "-ws")
	createTestWorkspace(t, pool, prefix+" Co", wsSlug, adminID)
	ident = uniqueTestIdentifier()
	createTestProject(t, pool, wsSlug, adminID, prefix+" Eng", ident)
	iss := createTestIssue(t, pool, wsSlug, ident, adminID, prefix+" issue")
	issueID = iss.ID
	displayID = fmt.Sprintf("%s-%d", ident, iss.SequenceID)
	return wsSlug, ident, issueID, adminID, displayID
}

func TestCreateExternalRef(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	wsSlug, ident, issueID, adminID, _ := linkTestSetup(t, pool, "ghl")

	ref, err := CreateExternalRef(ctx, pool, wsSlug, ident, issueID, adminID, CreateExternalRefInput{
		Repo:    "acme/repo",
		RefType: "PR", // canonicalized to lowercase
		RefID:   "42",
		URL:     "https://github.com/acme/repo/pull/42",
	})
	if err != nil {
		t.Fatalf("CreateExternalRef: %v", err)
	}
	if ref.Provider != "github" || ref.RefType != "pr" || ref.RefID != "42" ||
		ref.State != "open" || ref.Repo != "acme/repo" || ref.IssueID != issueID {
		t.Fatalf("ref = %+v", ref)
	}

	// The detail response embeds external_refs[].
	detail, err := GetIssue(ctx, pool, wsSlug, ident, issueID, adminID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if len(detail.ExternalRefs) != 1 || detail.ExternalRefs[0].ID != ref.ID {
		t.Fatalf("detail.ExternalRefs = %+v", detail.ExternalRefs)
	}
	// ... and it serializes as an always-present array, even when empty.
	raw, err := json.Marshal(detail)
	if err != nil {
		t.Fatalf("marshal detail: %v", err)
	}
	if !strings.Contains(string(raw), `"external_refs":[`) {
		t.Fatalf("detail JSON missing external_refs array: %s", raw[:200])
	}
}

func TestCreateExternalRefDuplicate(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	wsSlug, ident, issueID, adminID, _ := linkTestSetup(t, pool, "ghdup")

	in := CreateExternalRefInput{Repo: "acme/repo", RefType: "pr", RefID: "7", URL: "https://github.com/acme/repo/pull/7"}
	if _, err := CreateExternalRef(ctx, pool, wsSlug, ident, issueID, adminID, in); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if _, err := CreateExternalRef(ctx, pool, wsSlug, ident, issueID, adminID, in); !errors.Is(err, ErrExternalRefConflict) {
		t.Fatalf("duplicate create: err = %v, want ErrExternalRefConflict", err)
	}
	// Same PR linked to a DIFFERENT issue is fine (unique is per issue).
	iss2 := createTestIssue(t, pool, wsSlug, ident, adminID, "second")
	if _, err := CreateExternalRef(ctx, pool, wsSlug, ident, iss2.ID, adminID, in); err != nil {
		t.Fatalf("same PR on another issue: %v", err)
	}
}

func TestCreateExternalRefValidation(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	wsSlug, ident, issueID, adminID, _ := linkTestSetup(t, pool, "ghval")

	good := CreateExternalRefInput{Repo: "acme/repo", RefType: "pr", RefID: "7", URL: "https://github.com/acme/repo/pull/7"}
	cases := []struct {
		name   string
		mutate func(*CreateExternalRefInput)
	}{
		{"bad provider", func(in *CreateExternalRefInput) { in.Provider = "gitlab" }},
		{"bad ref_type", func(in *CreateExternalRefInput) { in.RefType = "branch" }},
		{"bad repo", func(in *CreateExternalRefInput) { in.Repo = "not a repo" }},
		{"repo traversal", func(in *CreateExternalRefInput) { in.Repo = "../etc" }},
		{"bad ref_id", func(in *CreateExternalRefInput) { in.RefID = "abc!" }},
		{"empty url", func(in *CreateExternalRefInput) { in.URL = "" }},
		{"non-http url", func(in *CreateExternalRefInput) { in.URL = "ftp://x/y" }},
		{"javascript url", func(in *CreateExternalRefInput) { in.URL = "javascript:alert(1)" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := good
			tc.mutate(&in)
			if _, err := CreateExternalRef(ctx, pool, wsSlug, ident, issueID, adminID, in); !errors.Is(err, ErrInvalidExternalRef) {
				t.Fatalf("err = %v, want ErrInvalidExternalRef", err)
			}
		})
	}
	// A commit SHA is a valid ref_id.
	shaIn := good
	shaIn.RefType = "commit"
	shaIn.RefID = "9f3a1b2c4d5e6f7890abcdef1234567890abcd"
	shaIn.URL = "https://github.com/acme/repo/commit/" + shaIn.RefID
	if _, err := CreateExternalRef(ctx, pool, wsSlug, ident, issueID, adminID, shaIn); err != nil {
		t.Fatalf("commit ref: %v", err)
	}
}

func TestExternalRefRBAC(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	wsSlug, ident, issueID, adminID, _ := linkTestSetup(t, pool, "ghrbac")

	guest := createTestUser(t, pool, uniqueTestEmail("ghrbac-guest"))
	addTestMember(t, pool, wsSlug, adminID, guest, RoleGuest)
	outside := createTestUser(t, pool, uniqueTestEmail("ghrbac-outside"))

	in := CreateExternalRefInput{Repo: "acme/repo", RefType: "pr", RefID: "7", URL: "https://github.com/acme/repo/pull/7"}
	if _, err := CreateExternalRef(ctx, pool, wsSlug, ident, issueID, guest, in); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest create: err = %v, want ErrForbidden", err)
	}
	if _, err := CreateExternalRef(ctx, pool, wsSlug, ident, issueID, outside, in); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-member create: err = %v, want ErrNotFound", err)
	}
	if err := DeleteExternalRef(ctx, pool, wsSlug, ident, issueID, guest, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest delete: err = %v, want ErrForbidden", err)
	}
	// Guests MAY read the refs via the detail response.
	detail, err := GetIssue(ctx, pool, wsSlug, ident, issueID, guest)
	if err != nil {
		t.Fatalf("guest detail: %v", err)
	}
	if detail.ExternalRefs == nil {
		t.Fatalf("guest detail: external_refs should be present (empty), got nil")
	}
}

func TestDeleteExternalRef(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	wsSlug, ident, issueID, adminID, _ := linkTestSetup(t, pool, "ghdel")

	in := CreateExternalRefInput{Repo: "acme/repo", RefType: "pr", RefID: "7", URL: "https://github.com/acme/repo/pull/7"}
	ref, err := CreateExternalRef(ctx, pool, wsSlug, ident, issueID, adminID, in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := DeleteExternalRef(ctx, pool, wsSlug, ident, issueID, adminID, ref.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := DeleteExternalRef(ctx, pool, wsSlug, ident, issueID, adminID, ref.ID); !errors.Is(err, ErrExternalRefNotFound) {
		t.Fatalf("double delete: err = %v, want ErrExternalRefNotFound", err)
	}
	// A ref on another issue is not addressable from this one.
	iss2 := createTestIssue(t, pool, wsSlug, ident, adminID, "second")
	ref2, err := CreateExternalRef(ctx, pool, wsSlug, ident, iss2.ID, adminID, in)
	if err != nil {
		t.Fatalf("create on issue 2: %v", err)
	}
	if err := DeleteExternalRef(ctx, pool, wsSlug, ident, issueID, adminID, ref2.ID); !errors.Is(err, ErrExternalRefNotFound) {
		t.Fatalf("cross-issue delete: err = %v, want ErrExternalRefNotFound", err)
	}
	if err := DeleteExternalRef(ctx, pool, wsSlug, ident, issueID, adminID, "not-a-uuid"); !errors.Is(err, ErrExternalRefNotFound) {
		t.Fatalf("malformed ref id: err = %v, want ErrExternalRefNotFound", err)
	}
}

// ---------- webhook secret ----------

func TestSetWorkspaceGitHubSecret(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	admin := createTestUser(t, pool, uniqueTestEmail("ghsec-admin"))
	wsSlug := uniqueTestSlug("ghsec-ws")
	ws := createTestWorkspace(t, pool, "Sec Co", wsSlug, admin)
	if ws.GitHubWebhookConfigured {
		t.Fatalf("fresh workspace should not be configured")
	}

	secret := "s3cr3t-wh00k-abc123"
	ws2, err := SetWorkspaceGitHubSecret(ctx, pool, wsSlug, admin, &secret)
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	if !ws2.GitHubWebhookConfigured {
		t.Fatalf("configured = false after set")
	}
	// The secret is stored but never serialized: the Workspace JSON must
	// not contain it.
	raw, _ := json.Marshal(ws2)
	if strings.Contains(string(raw), secret) {
		t.Fatalf("secret leaked in workspace JSON: %s", raw)
	}
	var stored string
	if err := pool.QueryRow(ctx, `SELECT github_webhook_secret FROM workspaces WHERE slug = $1`, wsSlug).Scan(&stored); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if stored != secret {
		t.Fatalf("stored = %q, want %q", stored, secret)
	}

	// Validation rejects. (Leading/trailing whitespace is trimmed by
	// design before validation — interior whitespace is rejected.)
	for _, bad := range []string{"has space", "tab\there", "new\nline", strings.Repeat("x", 257)} {
		b := bad
		if _, err := SetWorkspaceGitHubSecret(ctx, pool, wsSlug, admin, &b); !errors.Is(err, ErrGitHubBadSecret) {
			t.Fatalf("secret %q: err = %v, want ErrGitHubBadSecret", bad, err)
		}
	}
	// Empty clears.
	empty := ""
	ws3, err := SetWorkspaceGitHubSecret(ctx, pool, wsSlug, admin, &empty)
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if ws3.GitHubWebhookConfigured {
		t.Fatalf("configured = true after clear")
	}

	// Non-admin is forbidden; non-member is not-found.
	member := createTestUser(t, pool, uniqueTestEmail("ghsec-member"))
	addTestMember(t, pool, wsSlug, admin, member, RoleMember)
	if _, err := SetWorkspaceGitHubSecret(ctx, pool, wsSlug, member, &secret); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member set: err = %v, want ErrForbidden", err)
	}
	outside := createTestUser(t, pool, uniqueTestEmail("ghsec-outside"))
	if _, err := SetWorkspaceGitHubSecret(ctx, pool, wsSlug, outside, &secret); !errors.Is(err, ErrNotFound) {
		t.Fatalf("outsider set: err = %v, want ErrNotFound", err)
	}
}

// ---------- inbound webhook ----------

// signDelivery builds an X-Hub-Signature-256 value for body under secret.
func signDelivery(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func prPayload(action string, number int, title, body, repo string, merged bool) []byte {
	p := map[string]any{
		"action": action,
		"pull_request": map[string]any{
			"number":   number,
			"title":    title,
			"body":     body,
			"html_url": fmt.Sprintf("https://github.com/%s/pull/%d", repo, number),
			"merged":   merged,
		},
		"repository": map[string]any{"full_name": repo},
	}
	b, _ := json.Marshal(p)
	return b
}

func countRefs(t *testing.T, pool *pgxpool.Pool, issueID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM issue_external_refs WHERE issue_id = $1::uuid`, issueID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func refState(t *testing.T, pool *pgxpool.Pool, issueID, refID string) string {
	t.Helper()
	var s string
	if err := pool.QueryRow(context.Background(),
		`SELECT state FROM issue_external_refs WHERE issue_id = $1::uuid AND ref_id = $2`, issueID, refID).Scan(&s); err != nil {
		t.Fatalf("state: %v", err)
	}
	return s
}

// webhookTestSetup creates a workspace with a webhook secret and one
// issue, returning everything a delivery needs.
func webhookTestSetup(t *testing.T, pool *pgxpool.Pool, prefix string) (wsSlug, ident, issueID, displayID, secret string) {
	t.Helper()
	var adminID string
	wsSlug, ident, issueID, adminID, displayID = linkTestSetup(t, pool, prefix)
	// The test database persists across runs and is never truncated: the
	// secret must be unique across runs, not just within one, or a stale
	// workspace from a previous run would verify the same deliveries.
	secret = fmt.Sprintf("whsec-%s-%d-%d", prefix, os.Getpid(), testSeq.Add(1))
	if _, err := SetWorkspaceGitHubSecret(context.Background(), pool, wsSlug, adminID, &secret); err != nil {
		t.Fatalf("set secret: %v", err)
	}
	return wsSlug, ident, issueID, displayID, secret
}

func TestGitHubWebhookOpenedAutoLinks(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	_, _, issueID, displayID, secret := webhookTestSetup(t, pool, "ghook1")

	// Lowercase mention: the GLC-123 pattern is case-insensitive.
	body := prPayload("opened", 7, "Fix login ("+strings.ToLower(displayID)+")",
		"Closes "+displayID, "acme/repo", false)
	if err := HandleGitHubWebhook(ctx, pool, "pull_request", signDelivery(secret, body), body); err != nil {
		t.Fatalf("webhook: %v", err)
	}
	if n := countRefs(t, pool, issueID); n != 1 {
		t.Fatalf("refs = %d, want 1", n)
	}
	var repo, refID, url, state string
	if err := pool.QueryRow(ctx,
		`SELECT repo, ref_id, url, state FROM issue_external_refs WHERE issue_id = $1::uuid`,
		issueID).Scan(&repo, &refID, &url, &state); err != nil {
		t.Fatalf("read ref: %v", err)
	}
	if repo != "acme/repo" || refID != "7" || state != "open" || url != "https://github.com/acme/repo/pull/7" {
		t.Fatalf("ref = %q %q %q %q", repo, refID, url, state)
	}
}

func TestGitHubWebhookRedeliveryIdempotent(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	_, _, issueID, displayID, secret := webhookTestSetup(t, pool, "ghook2")

	body := prPayload("opened", 7, "Work on "+displayID, "", "acme/repo", false)
	sig := signDelivery(secret, body)
	for i := 0; i < 2; i++ {
		if err := HandleGitHubWebhook(ctx, pool, "pull_request", sig, body); err != nil {
			t.Fatalf("delivery %d: %v", i, err)
		}
	}
	if n := countRefs(t, pool, issueID); n != 1 {
		t.Fatalf("refs after redelivery = %d, want 1", n)
	}
	if s := refState(t, pool, issueID, "7"); s != "open" {
		t.Fatalf("state = %q, want open", s)
	}
}

func TestGitHubWebhookClosedMerged(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	_, _, issueID, displayID, secret := webhookTestSetup(t, pool, "ghook3")

	opened := prPayload("opened", 7, "Work on "+displayID, "", "acme/repo", false)
	if err := HandleGitHubWebhook(ctx, pool, "pull_request", signDelivery(secret, opened), opened); err != nil {
		t.Fatalf("opened: %v", err)
	}
	closed := prPayload("closed", 7, "Work on "+displayID, "", "acme/repo", true)
	if err := HandleGitHubWebhook(ctx, pool, "pull_request", signDelivery(secret, closed), closed); err != nil {
		t.Fatalf("closed: %v", err)
	}
	if s := refState(t, pool, issueID, "7"); s != "merged" {
		t.Fatalf("state = %q, want merged", s)
	}

	// Non-merged close → closed.
	closed2 := prPayload("closed", 8, "WIP "+displayID, "", "acme/repo", false)
	if err := HandleGitHubWebhook(ctx, pool, "pull_request", signDelivery(secret, closed2), closed2); err != nil {
		t.Fatalf("closed unmerged: %v", err)
	}
	if s := refState(t, pool, issueID, "8"); s != "closed" {
		t.Fatalf("state = %q, want closed", s)
	}
}

func TestGitHubWebhookClosedUpdatesManualRef(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	wsSlug, ident, issueID, _, secret := webhookTestSetup(t, pool, "ghook4")

	// Resolve the workspace creator to link through the service.
	var creatorID string
	if err := pool.QueryRow(ctx,
		`SELECT m.user_id::text FROM workspace_members m
		   JOIN workspaces w ON w.id = m.workspace_id
		  WHERE w.slug = $1 AND m.role = 20 LIMIT 1`, wsSlug).Scan(&creatorID); err != nil {
		t.Fatalf("creator: %v", err)
	}
	if _, err := CreateExternalRef(ctx, pool, wsSlug, ident, issueID, creatorID, CreateExternalRefInput{
		Repo: "acme/repo", RefType: "pr", RefID: "9", URL: "https://github.com/acme/repo/pull/9",
	}); err != nil {
		t.Fatalf("manual link: %v", err)
	}
	// Close payload mentions NOTHING — the state must still flip via the
	// (workspace, repo, number) match.
	closed := prPayload("closed", 9, "Unrelated title", "no mentions here", "acme/repo", true)
	if err := HandleGitHubWebhook(ctx, pool, "pull_request", signDelivery(secret, closed), closed); err != nil {
		t.Fatalf("closed: %v", err)
	}
	if s := refState(t, pool, issueID, "9"); s != "merged" {
		t.Fatalf("state = %q, want merged", s)
	}
}

func TestGitHubWebhookBadSignature(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	_, _, issueID, displayID, _ := webhookTestSetup(t, pool, "ghook5")

	body := prPayload("opened", 7, "Work on "+displayID, "", "acme/repo", false)
	if err := HandleGitHubWebhook(ctx, pool, "pull_request", signDelivery("wrong-secret", body), body); !errors.Is(err, ErrGitHubBadSignature) {
		t.Fatalf("wrong secret: err = %v, want ErrGitHubBadSignature", err)
	}
	if err := HandleGitHubWebhook(ctx, pool, "pull_request", "", body); !errors.Is(err, ErrGitHubBadSignature) {
		t.Fatalf("missing signature: err = %v, want ErrGitHubBadSignature", err)
	}
	if err := HandleGitHubWebhook(ctx, pool, "pull_request", "sha1=deadbeef", body); !errors.Is(err, ErrGitHubBadSignature) {
		t.Fatalf("wrong scheme: err = %v, want ErrGitHubBadSignature", err)
	}
	// Nothing was linked by the rejected deliveries.
	if n := countRefs(t, pool, issueID); n != 0 {
		t.Fatalf("refs = %d, want 0", n)
	}
}

func TestGitHubWebhookIgnoresOtherEvents(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	_, _, issueID, displayID, secret := webhookTestSetup(t, pool, "ghook6")

	ping := []byte(`{"zen":"Keep it logically awesome."}`)
	if err := HandleGitHubWebhook(ctx, pool, "ping", signDelivery(secret, ping), ping); err != nil {
		t.Fatalf("ping: %v", err)
	}
	syncBody := prPayload("synchronize", 7, "Work on "+displayID, "", "acme/repo", false)
	if err := HandleGitHubWebhook(ctx, pool, "pull_request", signDelivery(secret, syncBody), syncBody); err != nil {
		t.Fatalf("synchronize: %v", err)
	}
	if n := countRefs(t, pool, issueID); n != 0 {
		t.Fatalf("refs = %d, want 0", n)
	}
}

func TestGitHubWebhookUnknownIdentifier(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	_, _, issueID, _, secret := webhookTestSetup(t, pool, "ghook7")

	// ZZZ-999 is no project's identifier; UTF-8 is the classic false
	// positive. Neither may error nor link.
	body := prPayload("opened", 7, "Fix ZZZ-999 and UTF-8 handling", "", "acme/repo", false)
	if err := HandleGitHubWebhook(ctx, pool, "pull_request", signDelivery(secret, body), body); err != nil {
		t.Fatalf("webhook: %v", err)
	}
	if n := countRefs(t, pool, issueID); n != 0 {
		t.Fatalf("refs = %d, want 0", n)
	}
}

func TestGitHubWebhookBadPayload(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	_, _, _, _, secret := webhookTestSetup(t, pool, "ghook8")

	notJSON := []byte("this is not json")
	if err := HandleGitHubWebhook(ctx, pool, "pull_request", signDelivery(secret, notJSON), notJSON); !errors.Is(err, ErrGitHubBadPayload) {
		t.Fatalf("not JSON: err = %v, want ErrGitHubBadPayload", err)
	}
	empty := []byte(`{"action":"opened"}`)
	if err := HandleGitHubWebhook(ctx, pool, "pull_request", signDelivery(secret, empty), empty); !errors.Is(err, ErrGitHubBadPayload) {
		t.Fatalf("missing pull_request: err = %v, want ErrGitHubBadPayload", err)
	}
}
