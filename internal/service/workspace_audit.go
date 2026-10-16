package service

// Workspace audit log (C37T0): an append-only, workspace-scoped record
// of control-plane events — membership, role, project lifecycle,
// workspace settings, and integration configuration. The data-plane
// activity feed (issue_activities) covers issue CRUD/comments; the
// instance-admin audit_log covers instance-admin mutations.
//
// Write contract (same as the admin audit precedent): emitters that
// already hold the mutation's tx call RecordWorkspaceAuditTx with that
// tx, so the audit row commits or rolls back atomically with the
// mutation — an audit-write failure inside a tx fails the mutation,
// which is the accepted atomicity trade-off. Emitters with no tx use
// RecordWorkspaceAudit (pool variant) and MUST log-and-continue on
// error: a failed audit write must never 500 the mutation it annotates.
//
// Read contract: ListWorkspaceAuditLog is workspace-Admin-only
// (role == RoleAdmin). Non-members get ErrNotFound (the workspace
// existence must not leak); member non-admins get ErrForbidden.
//
// Secret rules (hard, merge-blocking if violated): meta NEVER carries
// a token value, a webhook secret, a full webhook URL (host only), or
// an OTP. Every emitter below was reviewed against this rule.
//
// Deliberately NOT wired: API-token create/revoke
// (integration.token_created / token_revoked). API tokens are
// user-scoped (auth.CreateToken / auth.RevokeToken take a userID and
// no workspace), and workspace_audit_log.workspace_id is NOT NULL, so
// there is no correct workspace to attribute a token event to —
// fanning one token event out to every workspace the user admins
// would fabricate witnessed history. Token lifecycle belongs in a
// future global (non-workspace) auth log, the same documented v1
// limitation as global sign-in/out events. Likewise there is no
// invite-accept/join call site in the codebase: InviteMembers inserts
// the membership directly, so it emits both member.invited (per
// invited email) and member.added (via "invite") in the same tx.
//
// IP: the annotated service functions carry no request IP (their
// signatures predate this log and changing ~11 signatures plus their
// call sites is out of scope for this task), so v1 emitters record
// IP as NULL. The column exists and RecordWorkspaceAuditTx accepts an
// IP for future plumbing of the handler's RealIP; no new proxy-trust
// logic was added (per the v1 scope).

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Workspace audit actions. Dotted "scope.verb" names so the UI can
// group them by scope (member / project / workspace / integration).
const (
	WorkspaceAuditMemberInvited     = "member.invited"
	WorkspaceAuditMemberAdded       = "member.added"
	WorkspaceAuditMemberRoleChanged = "member.role_changed"
	WorkspaceAuditMemberRemoved     = "member.removed"

	WorkspaceAuditProjectCreated  = "project.created"
	WorkspaceAuditProjectArchived = "project.archived"
	WorkspaceAuditProjectRestored = "project.restored"
	WorkspaceAuditProjectDeleted  = "project.deleted"

	WorkspaceAuditWorkspaceSettingsUpdated = "workspace.settings_updated"

	WorkspaceAuditIntegrationWebhookCreated    = "integration.webhook_created"
	WorkspaceAuditIntegrationWebhookDeleted    = "integration.webhook_deleted"
	WorkspaceAuditIntegrationGitHubConfigured  = "integration.github_configured"
	WorkspaceAuditIntegrationSlackConfigured   = "integration.slack_configured"
	WorkspaceAuditIntegrationSlackDisconnected = "integration.slack_disconnected"
)

// ErrInvalidWorkspaceAuditFilter is returned when a list filter is
// malformed (bad actor_id uuid, bad since/until RFC 3339). Handlers map
// it to 400.
var ErrInvalidWorkspaceAuditFilter = errors.New("service: invalid workspace audit-log filter")

// WorkspaceAuditEvent is one event to append to workspace_audit_log.
// ActorEmail is resolved from users when empty; IP is an optional
// dotted-quad/IPv6 literal ("" = NULL). Meta must never carry secrets
// (see the package comment).
type WorkspaceAuditEvent struct {
	ActorID    string
	ActorEmail string
	IP         string
	Action     string
	EntityType string
	EntityID   string
	Meta       map[string]any
}

// WorkspaceAuditEntry is one workspace_audit_log row for
// GET /api/v1/workspaces/{slug}/audit-log. ActorID/ActorEmail are nil
// when the actor's account has since been deleted — the row survives
// them. IP is nil when the write path recorded none (v1 emitters).
type WorkspaceAuditEntry struct {
	ID         string         `json:"id"`
	At         time.Time      `json:"at"`
	ActorID    *string        `json:"actor_id"`
	ActorEmail *string        `json:"actor_email"`
	Action     string         `json:"action"`
	EntityType string         `json:"entity_type"`
	EntityID   string         `json:"entity_id"`
	IP         *string        `json:"ip"`
	Meta       map[string]any `json:"meta"`
}

// workspaceAuditQuerier abstracts *pgxpool.Pool and pgx.Tx for the
// audit insert + actor-email lookup.
type workspaceAuditQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// recordWorkspaceAudit inserts one row. A caller-supplied ActorEmail
// wins; otherwise it is resolved from users (snapshot semantics). An
// unparseable IP degrades to NULL rather than failing the insert —
// the IP is annotation, never the mutation's payload.
func recordWorkspaceAudit(ctx context.Context, q workspaceAuditQuerier, workspaceID string, ev WorkspaceAuditEvent) error {
	actorEmail := ev.ActorEmail
	if actorEmail == "" && ev.ActorID != "" {
		if err := q.QueryRow(ctx, `SELECT email FROM users WHERE id = $1::uuid`,
			ev.ActorID).Scan(&actorEmail); err != nil {
			if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			actorEmail = ""
		}
	}
	ip := strings.TrimSpace(ev.IP)
	if ip != "" && net.ParseIP(ip) == nil {
		ip = ""
	}
	metaJSON := []byte("{}")
	if len(ev.Meta) > 0 {
		var err error
		metaJSON, err = json.Marshal(ev.Meta)
		if err != nil {
			return err
		}
	}
	var actorIDArg any
	if ev.ActorID != "" {
		actorIDArg = ev.ActorID
	}
	var actorEmailArg any
	if actorEmail != "" {
		actorEmailArg = actorEmail
	}
	if _, err := q.Exec(ctx, `
		INSERT INTO workspace_audit_log
			(workspace_id, actor_id, actor_email, action, entity_type, entity_id, ip, meta)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, NULLIF($7, '')::inet, $8::jsonb)`,
		workspaceID, actorIDArg, actorEmailArg, ev.Action, ev.EntityType, ev.EntityID, ip, string(metaJSON),
	); err != nil {
		return err
	}
	return nil
}

// RecordWorkspaceAuditTx appends one row inside tx — the caller must
// be a write path already holding the mutation's transaction.
func RecordWorkspaceAuditTx(ctx context.Context, tx pgx.Tx, workspaceID string, ev WorkspaceAuditEvent) error {
	return recordWorkspaceAudit(ctx, tx, workspaceID, ev)
}

// RecordWorkspaceAudit appends one row via the pool, for emitters with
// no tx. Callers MUST log-and-continue on error (best-effort): the
// audit write must never fail the mutation it annotates.
func RecordWorkspaceAudit(ctx context.Context, pool *pgxpool.Pool, workspaceID string, ev WorkspaceAuditEvent) error {
	return recordWorkspaceAudit(ctx, pool, workspaceID, ev)
}

// recordWorkspaceAuditInTx is the emitter shorthand: actor email is
// resolved inside the tx, IP is NULL (see the package comment).
func recordWorkspaceAuditInTx(ctx context.Context, tx pgx.Tx, workspaceID, actorID,
	action, entityType, entityID string, meta map[string]any) error {
	return RecordWorkspaceAuditTx(ctx, tx, workspaceID, WorkspaceAuditEvent{
		ActorID: actorID, Action: action, EntityType: entityType,
		EntityID: entityID, Meta: meta,
	})
}

// webhookHost returns just the hostname of a webhook URL for audit
// meta — never the full URL, which may embed tokens in its query
// string. An unparseable URL yields "" rather than an error.
func webhookHost(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// WorkspaceAuditFilter is the filter contract for the audit-log list
// endpoint. Empty fields are ignored. Since/Until are RFC 3339
// timestamps; Actor is a case-insensitive actor_email substring.
type WorkspaceAuditFilter struct {
	Action     string
	ActorID    string
	Actor      string
	EntityType string
	Since      string
	Until      string
}

// ListWorkspaceAuditLog returns audit entries newest-first
// (at DESC, id DESC), paginated, with the total row count for the
// page envelope. The caller must be a workspace Admin; non-members
// get ErrNotFound, member non-admins ErrForbidden.
func ListWorkspaceAuditLog(ctx context.Context, pool *pgxpool.Pool, slug, callerID string,
	limit, offset int, f WorkspaceAuditFilter) ([]WorkspaceAuditEntry, int64, error) {
	wsID, role, err := workspaceIDForActor(pool.QueryRow(ctx,
		`SELECT w.id::text, m.role
		 FROM workspaces w
		 JOIN workspace_members m ON m.workspace_id = w.id
		 WHERE w.slug = $1 AND m.user_id = $2::uuid`,
		slug, callerID))
	if err != nil {
		return nil, 0, err
	}
	if role != RoleAdmin {
		return nil, 0, ErrForbidden
	}

	if limit < 1 {
		limit = 25
	}
	if offset < 0 {
		offset = 0
	}

	// Filters build from a whitelist of known columns — no string
	// interpolation of caller input into the query. Since/Until parse
	// as RFC 3339; Actor matches via strpos (no LIKE wildcards from
	// caller input).
	where := "WHERE workspace_id = $1::uuid"
	args := []any{wsID}
	if f.Action != "" {
		args = append(args, f.Action)
		where += " AND action = $" + strconv.Itoa(len(args))
	}
	if f.ActorID != "" {
		if _, err := uuid.Parse(f.ActorID); err != nil {
			return nil, 0, fmt.Errorf("%w: actor_id must be a uuid", ErrInvalidWorkspaceAuditFilter)
		}
		args = append(args, f.ActorID)
		where += " AND actor_id = $" + strconv.Itoa(len(args)) + "::uuid"
	}
	if f.Actor != "" {
		args = append(args, strings.ToLower(f.Actor))
		where += " AND strpos(lower(COALESCE(actor_email, '')), $" + strconv.Itoa(len(args)) + ") > 0"
	}
	if f.EntityType != "" {
		args = append(args, f.EntityType)
		where += " AND entity_type = $" + strconv.Itoa(len(args))
	}
	if f.Since != "" {
		since, err := time.Parse(time.RFC3339, f.Since)
		if err != nil {
			return nil, 0, fmt.Errorf("%w: since must be RFC 3339", ErrInvalidWorkspaceAuditFilter)
		}
		args = append(args, since)
		where += " AND at >= $" + strconv.Itoa(len(args))
	}
	if f.Until != "" {
		until, err := time.Parse(time.RFC3339, f.Until)
		if err != nil {
			return nil, 0, fmt.Errorf("%w: until must be RFC 3339", ErrInvalidWorkspaceAuditFilter)
		}
		args = append(args, until)
		where += " AND at <= $" + strconv.Itoa(len(args))
	}

	var total int64
	countArgs := append([]any{}, args...)
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM workspace_audit_log `+where, countArgs...).Scan(&total); err != nil {
		return nil, 0, err
	}

	args = append(args, limit, offset)
	rows, err := pool.Query(ctx, `
		SELECT id::text, at, actor_id::text, actor_email, action,
		       entity_type, entity_id, host(ip), meta
		FROM workspace_audit_log
		`+where+`
		ORDER BY at DESC, id DESC
		LIMIT $`+strconv.Itoa(len(args)-1)+` OFFSET $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []WorkspaceAuditEntry{}
	for rows.Next() {
		var e WorkspaceAuditEntry
		var metaRaw []byte
		if err := rows.Scan(&e.ID, &e.At, &e.ActorID, &e.ActorEmail, &e.Action,
			&e.EntityType, &e.EntityID, &e.IP, &metaRaw); err != nil {
			return nil, 0, err
		}
		e.Meta = map[string]any{}
		if len(metaRaw) > 0 {
			if err := json.Unmarshal(metaRaw, &e.Meta); err != nil {
				return nil, 0, err
			}
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// WorkspaceAuditCSV renders entries as CSV with the export header
// (at,actor_email,action,entity_type,entity_id,ip,meta), meta as
// compact JSON. encoding/csv handles quoting; json.Marshal on a map
// emits keys sorted, so meta is deterministic.
func WorkspaceAuditCSV(entries []WorkspaceAuditEntry) ([]byte, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	if err := w.Write([]string{"at", "actor_email", "action", "entity_type", "entity_id", "ip", "meta"}); err != nil {
		return nil, err
	}
	for _, e := range entries {
		metaJSON, err := json.Marshal(e.Meta)
		if err != nil {
			return nil, err
		}
		actorEmail := ""
		if e.ActorEmail != nil {
			actorEmail = *e.ActorEmail
		}
		ip := ""
		if e.IP != nil {
			ip = *e.IP
		}
		rec := []string{
			e.At.UTC().Format(time.RFC3339),
			actorEmail,
			e.Action,
			e.EntityType,
			e.EntityID,
			ip,
			string(metaJSON),
		}
		if err := w.Write(rec); err != nil {
			return nil, err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
