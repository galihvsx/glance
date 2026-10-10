package service

// Admin audit log (C15T1): an append-only record of every instance-admin
// mutation — user deactivate/reactivate/role change and workspace
// delete. Write paths call recordAuditTx with the SAME pgx.Tx as the
// mutation, so the log cannot drift from the underlying change; there
// is no update or delete path for audit_log anywhere in the service or
// API layers. The only reader is ListAuditLog (paginated, filtered).

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

// Audit actions. Dotted "scope.verb" names so the UI can group them and
// future admin write paths slot in without a registry.
const (
	AuditActionUserRoleChanged  = "user.role_changed"
	AuditActionUserDeactivated  = "user.deactivated"
	AuditActionUserReactivated  = "user.reactivated"
	AuditActionWorkspaceDeleted = "workspace.deleted"
)

// AuditInfo carries who-did-it metadata for admin write paths. Handlers
// fill it from the authenticated user (actor) and the request (IP).
type AuditInfo struct {
	ActorID string
	IP      string
}

// AuditEntry is one audit_log row for GET /api/v1/admin/audit-log.
// ActorID/ActorEmail are nil when the actor's account has since been
// deleted — the row survives them. WorkspaceID is nil for user-scoped
// actions and for workspace deletions (the workspace is gone).
type AuditEntry struct {
	ID          string         `json:"id"`
	At          time.Time      `json:"at"`
	ActorID     *string        `json:"actor_id"`
	ActorEmail  *string        `json:"actor_email"`
	Action      string         `json:"action"`
	EntityType  string         `json:"entity_type"`
	EntityID    string         `json:"entity_id"`
	WorkspaceID *string        `json:"workspace_id"`
	IP          *string        `json:"ip"`
	Meta        map[string]any `json:"meta"`
}

// recordAuditTx appends one row to audit_log inside tx — the caller must
// be a write path already holding the mutation's transaction. A nil
// workspaceID means "no workspace"; empty meta means none.
func recordAuditTx(ctx context.Context, tx pgx.Tx, info AuditInfo, action,
	entityType, entityID, workspaceID string, meta map[string]any) error {
	metaJSON := []byte("{}")
	if len(meta) > 0 {
		var err error
		metaJSON, err = json.Marshal(meta)
		if err != nil {
			return err
		}
	}
	var workspaceIDArg any
	if workspaceID != "" {
		workspaceIDArg = workspaceID
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_log (actor_id, action, entity_type, entity_id, workspace_id, ip, meta)
		VALUES ($1::uuid, $2, $3, $4, $5::uuid, NULLIF($6, ''), $7::jsonb)`,
		info.ActorID, action, entityType, entityID, workspaceIDArg, info.IP, string(metaJSON),
	); err != nil {
		return err
	}
	return nil
}

// AuditLogFilter is the exact-match filter contract for the audit-log
// list endpoint. Empty fields are ignored.
type AuditLogFilter struct {
	Action     string
	ActorID    string
	EntityType string
}

// ListAuditLog returns audit entries newest-first (at DESC, id tiebreak),
// paginated, with the total row count for the page envelope. actor_email
// is resolved via LEFT JOIN so rows survive actor deletion.
func ListAuditLog(ctx context.Context, pool interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}, limit, offset int, f AuditLogFilter) ([]AuditEntry, int64, error) {
	if limit < 1 {
		limit = 25
	}
	if limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}

	// Exact-match filters build from a whitelist of known columns —
	// no string interpolation of caller input into the query.
	where := "WHERE true"
	args := []any{}
	if f.Action != "" {
		args = append(args, f.Action)
		where += " AND a.action = $" + strconv.Itoa(len(args))
	}
	if f.ActorID != "" {
		args = append(args, f.ActorID)
		where += " AND a.actor_id = $" + strconv.Itoa(len(args)) + "::uuid"
	}
	if f.EntityType != "" {
		args = append(args, f.EntityType)
		where += " AND a.entity_type = $" + strconv.Itoa(len(args))
	}

	var total int64
	countArgs := append([]any{}, args...)
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM audit_log a `+where, countArgs...).Scan(&total); err != nil {
		return nil, 0, err
	}

	args = append(args, limit, offset)
	rows, err := pool.Query(ctx, `
		SELECT a.id::text, a.at, a.actor_id::text, u.email, a.action,
		       a.entity_type, a.entity_id, a.workspace_id::text, a.ip, a.meta
		FROM audit_log a
		LEFT JOIN users u ON u.id = a.actor_id
		`+where+`
		ORDER BY a.at DESC, a.id DESC
		LIMIT $`+strconv.Itoa(len(args)-1)+` OFFSET $`+strconv.Itoa(len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []AuditEntry{}
	for rows.Next() {
		var e AuditEntry
		var metaRaw []byte
		if err := rows.Scan(&e.ID, &e.At, &e.ActorID, &e.ActorEmail, &e.Action,
			&e.EntityType, &e.EntityID, &e.WorkspaceID, &e.IP, &metaRaw); err != nil {
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
