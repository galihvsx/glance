package service

// GitHub two-way linkage (C17T0): link issues ↔ PRs/commits.
//
// Plane shows linked PRs on an issue and reflects merge state; glance only
// imported until now. This file covers:
//
//   - issue_external_refs rows: manual link/unlink (member 15+) and the
//     GitHub repo webhook (HMAC-verified, no GitHub App required).
//   - the per-workspace webhook secret (write-time validation, never
//     returned by the API — only github_webhook_configured).
//   - embedding external_refs[] on the issue detail response (see the
//     externalRefsAgg aggregate wired into getIssueRow in issue.go).
//
// Webhook behavior: POST /api/v1/integrations/github/webhook verifies
// X-Hub-Signature-256 against every workspace's stored secret (the
// delivery is signed with the secret the repo admin pasted into GitHub,
// so the matching workspace is the one whose secret verifies). Then:
//
//   - pull_request/opened: auto-link when the PR title or body mentions
//     the issue's display ID (GLC-123 pattern, case-insensitive). The
//     upsert is idempotent: redelivering the same event yields one row.
//   - pull_request/closed: set matching refs to merged (when
//     pull_request.merged) or closed; the mention scan also runs so a
//     missed opened delivery still links (with the closed state).
//
// Everything else (ping, other actions, other events) is acknowledged
// with 200 and ignored. A missing or non-verifying signature is
// ErrGitHubBadSignature — the endpoint never reveals whether any
// workspace has a webhook configured.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrExternalRefConflict is returned when the same (provider,
	// ref_type, ref_id) is already linked to the issue. The handler maps
	// it to 409.
	ErrExternalRefConflict = errors.New("service: external ref already linked")
	// ErrExternalRefNotFound is returned when the ref id matches nothing
	// on the addressed issue. The handler maps it to 404.
	ErrExternalRefNotFound = errors.New("service: external ref not found")
	// ErrInvalidExternalRef is returned for a bad provider, ref_type,
	// repo, ref_id or url. The handler maps it to 400.
	ErrInvalidExternalRef = errors.New("service: invalid external ref")
	// ErrGitHubBadSignature is returned when the webhook delivery's
	// X-Hub-Signature-256 does not verify against any workspace secret.
	// The handler maps it to 401.
	ErrGitHubBadSignature = errors.New("service: bad github webhook signature")
	// ErrGitHubBadSecret is returned when the webhook secret fails
	// write-time validation. The handler maps it to 400.
	ErrGitHubBadSecret = errors.New("service: invalid github webhook secret")
	// ErrGitHubBadPayload is returned when a verified webhook delivery
	// carries malformed JSON or a malformed pull_request payload. The
	// handler maps it to 400.
	ErrGitHubBadPayload = errors.New("service: malformed github webhook payload")
)

// ExternalRef is one linked PR or commit on an issue.
type ExternalRef struct {
	ID        string    `json:"id"`
	IssueID   string    `json:"issue_id"`
	Provider  string    `json:"provider"`
	Repo      string    `json:"repo"`
	RefType   string    `json:"ref_type"`
	RefID     string    `json:"ref_id"`
	URL       string    `json:"url"`
	State     string    `json:"state"`
	CreatedAt time.Time `json:"created_at"`
}

// CreateExternalRefInput carries the fields for a manual link. Provider
// defaults to "github" when empty; only github is supported today.
type CreateExternalRefInput struct {
	Provider string
	Repo     string // owner/name
	RefType  string // pr | commit
	RefID    string // PR number | commit SHA
	URL      string
}

// externalRefColumns is the canonical column list for ExternalRef scans.
const externalRefColumns = `id::text, issue_id::text, provider, repo, ref_type, ref_id, url, state, created_at`

func scanExternalRef(row pgx.Row) (*ExternalRef, error) {
	var r ExternalRef
	if err := row.Scan(&r.ID, &r.IssueID, &r.Provider, &r.Repo, &r.RefType,
		&r.RefID, &r.URL, &r.State, &r.CreatedAt); err != nil {
		return nil, err
	}
	return &r, nil
}

var (
	// githubRefRepoRe validates the owner/name pair: same charset the
	// GitHub importer uses (githubRepoRe in github_import.go), one slash.
	githubRefRepoRe = regexp.MustCompile(`^[A-Za-z0-9_.\-]+/[A-Za-z0-9_.\-]+$`)
	// githubRefIDRe validates ref_id: a PR number (digits) or a commit
	// SHA (hex, 7-40 chars).
	githubRefIDRe = regexp.MustCompile(`^([0-9]+|[0-9a-fA-F]{7,40})$`)
)

// validGitHubRepo rejects charset-valid but dangerous segments: "." and
// ".." (path traversal if the value is ever interpolated into a path)
// and leading/trailing dots (not valid GitHub names).
func validGitHubRepo(repo string) bool {
	if !githubRefRepoRe.MatchString(repo) || len(repo) > 201 {
		return false
	}
	for _, seg := range strings.Split(repo, "/") {
		if seg == "." || seg == ".." || strings.HasPrefix(seg, ".") || strings.HasSuffix(seg, ".") {
			return false
		}
	}
	return true
}

// normalizeExternalRefInput validates and canonicalizes a manual link.
// ref_type is lowercased; provider defaults to github.
func normalizeExternalRefInput(in CreateExternalRefInput) (CreateExternalRefInput, error) {
	out := in
	out.Provider = strings.ToLower(strings.TrimSpace(out.Provider))
	if out.Provider == "" {
		out.Provider = "github"
	}
	if out.Provider != "github" {
		return out, fmt.Errorf("%w: unsupported provider %q", ErrInvalidExternalRef, in.Provider)
	}
	out.RefType = strings.ToLower(strings.TrimSpace(out.RefType))
	if out.RefType != "pr" && out.RefType != "commit" {
		return out, fmt.Errorf("%w: ref_type must be pr or commit", ErrInvalidExternalRef)
	}
	out.Repo = strings.TrimSpace(out.Repo)
	if !validGitHubRepo(out.Repo) {
		return out, fmt.Errorf("%w: repo must be owner/name", ErrInvalidExternalRef)
	}
	out.RefID = strings.TrimSpace(out.RefID)
	if !githubRefIDRe.MatchString(out.RefID) {
		return out, fmt.Errorf("%w: ref_id must be a PR number or commit SHA", ErrInvalidExternalRef)
	}
	out.URL = strings.TrimSpace(out.URL)
	if out.URL == "" || len(out.URL) > 2048 {
		return out, fmt.Errorf("%w: url is required", ErrInvalidExternalRef)
	}
	lower := strings.ToLower(out.URL)
	if !(strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "http://")) {
		return out, fmt.Errorf("%w: url must be http(s)", ErrInvalidExternalRef)
	}
	return out, nil
}

// announceExternalRefChange fans out issue.updated after a ref
// mutation so open issue-detail pages refetch their embedded refs
// (C62T1). Best-effort: the row fetch is skipped entirely when no
// broadcaster is wired, and a lookup failure skips the broadcast
// rather than failing the already-committed mutation (same
// best-effort posture as announceCycleUpdated).
func announceExternalRefChange(ctx context.Context, pool *pgxpool.Pool, wsSlug, ident, projectID, issueID string) {
	if Realtime == nil {
		return
	}
	iss, err := getIssueRow(ctx, pool, ident, projectID, issueID)
	if err != nil {
		return
	}
	announceIssueUpdated(wsSlug, ident, issueID, &iss.Issue)
}

// CreateExternalRef manually links a PR or commit to an issue. Member
// (15)+; guests get ErrForbidden, non-members ErrNotFound (tenancy
// hygiene via resolveSatelliteIssue). A duplicate (issue, provider,
// ref_type, ref_id) is ErrExternalRefConflict — the unique index is the
// backstop, so a racing double create serializes into a conflict.
func CreateExternalRef(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, issueID, actorID string, in CreateExternalRefInput) (*ExternalRef, error) {
	in, err := normalizeExternalRefInput(in)
	if err != nil {
		return nil, err
	}
	issueID = normalizeIssueID(issueID)
	ident, projectID, role, err := resolveSatelliteIssue(ctx, pool, wsSlug, identifier, issueID, actorID)
	if err != nil {
		return nil, err
	}
	if err := requireSatelliteWriter(role); err != nil {
		return nil, err
	}
	ref, err := scanExternalRef(pool.QueryRow(ctx,
		`INSERT INTO issue_external_refs (issue_id, provider, repo, ref_type, ref_id, url, state)
		 VALUES ($1::uuid, $2, $3, $4, $5, $6, 'open')
		 RETURNING `+externalRefColumns,
		issueID, in.Provider, in.Repo, in.RefType, in.RefID, in.URL))
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrExternalRefConflict
		}
		return nil, err
	}
	// C62T1: ref changes move the issue's displayed state — announce so
	// collaborators and other tabs refresh.
	announceExternalRefChange(ctx, pool, wsSlug, ident, projectID, issueID)
	return ref, nil
}

// DeleteExternalRef unlinks a ref from an issue. The ref must belong to
// the addressed issue — anything else is ErrExternalRefNotFound. Member
// (15)+.
func DeleteExternalRef(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, issueID, actorID, refID string) error {
	issueID = normalizeIssueID(issueID)
	ident, projectID, role, err := resolveSatelliteIssue(ctx, pool, wsSlug, identifier, issueID, actorID)
	if err != nil {
		return err
	}
	if err := requireSatelliteWriter(role); err != nil {
		return err
	}
	ct, err := pool.Exec(ctx,
		`DELETE FROM issue_external_refs WHERE id = $1::uuid AND issue_id = $2::uuid`,
		strings.ToLower(strings.TrimSpace(refID)), issueID)
	if err != nil {
		if isInvalidUUID(err) {
			return ErrExternalRefNotFound
		}
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrExternalRefNotFound
	}
	// C62T1: announce so open detail pages drop the unlinked row.
	announceExternalRefChange(ctx, pool, wsSlug, ident, projectID, issueID)
	return nil
}

// ---------- workspace webhook secret ----------

// validateGitHubWebhookSecret enforces the write-time contract: the
// secret is an opaque string, 1-256 chars, printable ASCII without
// whitespace (copy-paste accidents are the common failure). An empty
// value clears the configuration (handled by the caller, like the Slack
// URL path).
func validateGitHubWebhookSecret(s string) error {
	if len(s) == 0 || len(s) > 256 {
		return fmt.Errorf("%w: secret must be 1-256 characters", ErrGitHubBadSecret)
	}
	for _, r := range s {
		if r < 0x21 || r > 0x7E {
			return fmt.Errorf("%w: secret must be printable ASCII without whitespace", ErrGitHubBadSecret)
		}
	}
	return nil
}

// SetWorkspaceGitHubSecret sets or clears the workspace's GitHub webhook
// secret. Admin (role 20) only; non-members get ErrNotFound. raw nil (or
// empty after trimming) clears the column. The secret is validated before
// anything is written and is NEVER returned — the response carries only
// the GitHubWebhookConfigured flag.
func SetWorkspaceGitHubSecret(ctx context.Context, pool *pgxpool.Pool, wsSlug, actorID string, raw *string) (*Workspace, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var ws Workspace
	var role int
	err = tx.QueryRow(ctx,
		`SELECT w.id::text, w.slug::text, w.name, w.created_at, w.updated_at,
		        w.slack_webhook_url IS NOT NULL, w.github_webhook_secret IS NOT NULL, m.role
		   FROM workspaces w
		   JOIN workspace_members m ON m.workspace_id = w.id
		  WHERE w.slug = $1 AND m.user_id = $2::uuid`,
		wsSlug, actorID).Scan(&ws.ID, &ws.Slug, &ws.Name, &ws.CreatedAt, &ws.UpdatedAt,
		&ws.SlackConfigured, &ws.GitHubWebhookConfigured, &role)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if role != RoleAdmin {
		return nil, ErrForbidden
	}

	var newSecret any // nil interface → SQL NULL (clears the column)
	configured := false
	if raw != nil {
		s := strings.TrimSpace(*raw)
		if s != "" {
			if err := validateGitHubWebhookSecret(s); err != nil {
				return nil, err
			}
			newSecret = s
			configured = true
		}
	}
	if err := tx.QueryRow(ctx,
		`UPDATE workspaces SET github_webhook_secret = $1, updated_at = now()
		  WHERE id = $2::uuid
		  RETURNING github_webhook_secret IS NOT NULL`,
		newSecret, ws.ID).Scan(&ws.GitHubWebhookConfigured); err != nil {
		return nil, err
	}
	// C37T0: integration.github_configured on every successful set
	// (including a secret rotation). Meta carries only the boolean —
	// never the secret. Clearing emits nothing: there is no
	// github_disconnected event in the v1 catalogue.
	if configured {
		if err := recordWorkspaceAuditInTx(ctx, tx, ws.ID, actorID,
			WorkspaceAuditIntegrationGitHubConfigured, "integration", "github",
			map[string]any{"configured": true}); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &ws, nil
}

// ---------- inbound webhook ----------

// GitHubWebhookMaxBytes caps a delivery body: PR payloads are tens of KB;
// anything larger is not a pull_request event we handle.
const GitHubWebhookMaxBytes = 2 << 20

// githubIssueMentionRe finds display-ID mentions (GLC-123) in PR titles
// and bodies, case-insensitive. Identifiers are 1-12 [A-Z0-9] per
// normalizeIdentifier; the sequence is 1-10 digits. Non-project
// identifiers (UTF-8 is the classic) simply match no project and are
// ignored downstream.
var githubIssueMentionRe = regexp.MustCompile(`(?i)\b([A-Z0-9]{1,12})-([0-9]{1,10})\b`)

// maxRefsPerDelivery bounds the mention scan: one delivery links at most
// this many distinct issues.
const maxRefsPerDelivery = 20

type githubPRPayload struct {
	Action      string `json:"action"`
	PullRequest struct {
		Number  int    `json:"number"`
		Title   string `json:"title"`
		Body    string `json:"body"`
		HTMLURL string `json:"html_url"`
		Merged  bool   `json:"merged"`
	} `json:"pull_request"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
}

// verifyGitHubSignature reports whether body verifies against secret per
// the X-Hub-Signature-256 contract ("sha256=<hex>"). Constant-time
// compare; a malformed header never verifies.
func verifyGitHubSignature(secret string, body []byte, signature string) bool {
	if !strings.HasPrefix(signature, "sha256=") {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	return subtle.ConstantTimeCompare([]byte(want), []byte(signature)) == 1
}

// resolveWebhookWorkspace finds the workspace whose stored webhook secret
// verifies the delivery. Every configured workspace is tried; the first
// match wins. None matching (or none configured) is ErrGitHubBadSignature
// — the same 401 either way, so the endpoint never reveals which
// workspaces have webhooks configured.
func resolveWebhookWorkspace(ctx context.Context, pool *pgxpool.Pool, body []byte, signature string) (string, error) {
	rows, err := pool.Query(ctx,
		`SELECT id::text, github_webhook_secret FROM workspaces
		  WHERE github_webhook_secret IS NOT NULL AND github_webhook_secret <> ''`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var wsID, secret string
		if err := rows.Scan(&wsID, &secret); err != nil {
			return "", err
		}
		if verifyGitHubSignature(secret, body, signature) {
			return wsID, rows.Err()
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return "", ErrGitHubBadSignature
}

// extractIssueMentions returns the distinct (IDENTIFIER, sequence) pairs
// mentioned in text, uppercasing the identifier to the canonical project
// form.
func extractIssueMentions(text string) [][2]string {
	var out [][2]string
	seen := map[string]bool{}
	for _, m := range githubIssueMentionRe.FindAllStringSubmatch(text, maxRefsPerDelivery+1) {
		ident := strings.ToUpper(m[1])
		seq, err := strconv.Atoi(m[2])
		// sequence_id is a Postgres integer: larger mentions can never
		// match and would error the comparison.
		if err != nil || seq <= 0 || seq > 2147483647 {
			continue
		}
		key := ident + "-" + m[2]
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, [2]string{ident, m[2]})
		if len(out) >= maxRefsPerDelivery {
			break
		}
	}
	return out
}

// upsertPRRef links (or refreshes) the PR ref on one issue. The unique
// (issue_id, provider, ref_type, ref_id) index makes the upsert atomic —
// a redelivered event collapses into the same row instead of erroring or
// duplicating. The event's state wins on conflict: applying the same
// event twice is a fixed point.
func upsertPRRef(ctx context.Context, q queryRower, issueID, repo, number, url, state string) error {
	_, err := scanExternalRef(q.QueryRow(ctx,
		`INSERT INTO issue_external_refs (issue_id, provider, repo, ref_type, ref_id, url, state)
		 VALUES ($1::uuid, 'github', $2, 'pr', $3, $4, $5)
		 ON CONFLICT (issue_id, provider, ref_type, ref_id)
		 DO UPDATE SET url = EXCLUDED.url, state = EXCLUDED.state
		 RETURNING `+externalRefColumns,
		issueID, repo, number, url, state))
	return err
}

// linkMentionedIssues resolves every mentioned display ID against the
// workspace's projects and upserts the PR ref with the given state.
// Unknown identifiers and deleted issues are skipped silently — a PR body
// mentioning GLC-999 or "UTF-8" is not an error. It returns the ids of the
// issues that gained a ref row (used by the C63T0 inbound broadcast).
func linkMentionedIssues(ctx context.Context, pool *pgxpool.Pool, wsID, repo, number, url, state, text string) ([]string, error) {
	var linked []string
	for _, mention := range extractIssueMentions(text) {
		var issueID string
		err := pool.QueryRow(ctx,
			`SELECT i.id::text FROM issues i
			  JOIN projects p ON p.id = i.project_id
			 WHERE p.workspace_id = $1::uuid AND p.identifier = $2
			   AND i.sequence_id = $3 AND i.deleted_at IS NULL`,
			wsID, mention[0], mention[1]).Scan(&issueID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			return nil, err
		}
		if err := upsertPRRef(ctx, pool, issueID, repo, number, url, state); err != nil {
			return nil, err
		}
		linked = append(linked, issueID)
	}
	return linked, nil
}

// closePRRefs flips every ref for (workspace, repo, PR number) to the
// closed state — this catches refs linked manually via POST /refs, which
// the mention scan below would otherwise miss. It returns the ids of the
// affected issues (used by the C63T0 inbound broadcast).
func closePRRefs(ctx context.Context, pool *pgxpool.Pool, wsID, repo, number, state string) ([]string, error) {
	rows, err := pool.Query(ctx,
		`UPDATE issue_external_refs r SET state = $4
		   FROM issues i JOIN projects p ON p.id = i.project_id
		  WHERE r.issue_id = i.id AND p.workspace_id = $1::uuid
		    AND r.provider = 'github' AND r.repo = $2
		    AND r.ref_type = 'pr' AND r.ref_id = $3
		  RETURNING r.issue_id::text`,
		wsID, repo, number, state)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[string]bool{}
	var affected []string
	for rows.Next() {
		var issueID string
		if err := rows.Scan(&issueID); err != nil {
			return nil, err
		}
		if !seen[issueID] {
			seen[issueID] = true
			affected = append(affected, issueID)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return affected, nil
}

// announceInboundRefChanges fans out issue.updated per issue whose refs
// changed on an inbound webhook delivery (C63T0) — the same event the
// manual ref paths send via announceExternalRefChange. Best-effort: a
// routing-lookup failure skips that issue rather than failing the
// delivery, and duplicates collapse into one event per issue (the closed
// path's state flip and mention rescan can touch the same issue).
func announceInboundRefChanges(ctx context.Context, pool *pgxpool.Pool, issueIDs []string) {
	if Realtime == nil {
		return
	}
	seen := map[string]bool{}
	for _, issueID := range issueIDs {
		if seen[issueID] {
			continue
		}
		seen[issueID] = true
		var wsSlug, ident, projectID string
		if err := pool.QueryRow(ctx,
			`SELECT w.slug, p.identifier, p.id::text
			   FROM issues i
			   JOIN projects p ON p.id = i.project_id
			   JOIN workspaces w ON w.id = p.workspace_id
			  WHERE i.id = $1::uuid AND i.deleted_at IS NULL`,
			issueID).Scan(&wsSlug, &ident, &projectID); err != nil {
			continue
		}
		announceExternalRefChange(ctx, pool, wsSlug, ident, projectID, issueID)
	}
}

// HandleGitHubWebhook processes one verified delivery. event is the
// X-GitHub-Event header, signature the X-Hub-Signature-256 header, body
// the raw request bytes (capped by the handler).
//
// Only pull_request/opened and pull_request/closed mutate anything; every
// other event (including ping) is acknowledged and ignored. A malformed
// JSON body or pull_request payload is ErrGitHubBadPayload; a
// non-verifying signature is ErrGitHubBadSignature.
func HandleGitHubWebhook(ctx context.Context, pool *pgxpool.Pool, event, signature string, body []byte) error {
	wsID, err := resolveWebhookWorkspace(ctx, pool, body, signature)
	if err != nil {
		return err
	}
	if event != "pull_request" {
		return nil
	}
	var p githubPRPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return fmt.Errorf("%w: %v", ErrGitHubBadPayload, err)
	}
	repo := strings.TrimSpace(p.Repository.FullName)
	if !validGitHubRepo(repo) || p.PullRequest.Number <= 0 {
		return fmt.Errorf("%w: missing repository.full_name or pull_request.number", ErrGitHubBadPayload)
	}
	number := strconv.Itoa(p.PullRequest.Number)
	url := strings.TrimSpace(p.PullRequest.HTMLURL)
	if url == "" {
		url = "https://github.com/" + repo + "/pull/" + number
	}
	text := p.PullRequest.Title + "\n" + p.PullRequest.Body

	switch p.Action {
	case "opened":
		// Auto-link mentioned issues, state open. Idempotent: a
		// redelivered opened collapses into the same rows. C63T0:
		// announce per affected issue, same event the manual path
		// sends — best-effort, delivery success is unaffected.
		linked, err := linkMentionedIssues(ctx, pool, wsID, repo, number, url, "open", text)
		if err != nil {
			return err
		}
		announceInboundRefChanges(ctx, pool, linked)
		return nil
	case "closed":
		state := "closed"
		if p.PullRequest.Merged {
			state = "merged"
		}
		affected, err := closePRRefs(ctx, pool, wsID, repo, number, state)
		if err != nil {
			return err
		}
		// Also run the mention scan with the closed state, so a missed
		// opened delivery still links the PR (with the right state).
		linked, err := linkMentionedIssues(ctx, pool, wsID, repo, number, url, state, text)
		if err != nil {
			return err
		}
		// C63T0: broadcast for every issue the delivery touched —
		// announceInboundRefChanges dedupes the flip+rescan overlap.
		announceInboundRefChanges(ctx, pool, append(affected, linked...))
		return nil
	default:
		return nil
	}
}
