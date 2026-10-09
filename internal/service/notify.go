package service

// Notifications (Task 26): in-app notification rows, per-event delivery
// prefs, and email via the shared outbox.
//
// Two delivery paths:
//  1. In-app ROWS are written synchronously inside the mutation's tx via
//     notifyTx — the notification is never lost when the mutation commits.
//  2. EMAIL goes through the shared outbox as "email.notification" rows
//     (mail.Enqueue, same tx); the mail dispatcher already claims "email.%"
//     so no second mail path exists.
//
// notification_prefs gates delivery per (user, event): absent row = defaults
// (in_app on, email off). The actor is never notified of their own action.
//
// After the tx commits, callers broadcast notification.created on each
// recipient's user:{id} channel via announceNotifications — the R1
// Broadcaster pattern (Task 24 deferred these call sites here).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"glance/internal/mail"
)

// Notification event types. One vocabulary serves both the notifications
// "type" column and the notification_prefs event keys.
const (
	NotifyIssueAssigned   = "issue.assigned"
	NotifyIssueUnassigned = "issue.unassigned"
	NotifyCommentCreated  = "comment.created"
	NotifyStateChanged    = "issue.state_changed"
	NotifyIntakeTriaged   = "intake.triaged"
	// NotifyMention fires when a workspace member is @-mentioned in a
	// comment (C8T1). No migration needed: notification_prefs.event is
	// unconstrained TEXT, so adding the key to AllNotifyEvents is enough
	// for prefs to work (absent row = defaults: in_app on, email off).
	NotifyMention = "mention"
	// NotifyDueSoon fires when the daily reminder job sees an issue whose
	// target_date is tomorrow and which was never reminded (C8T6). One
	// notification per issue; the mark is written to issue_reminders.
	NotifyDueSoon = "due_soon"
	// NotifyOverdue fires when the daily reminder job sees an issue whose
	// target_date is before today, throttled to once per 24h (C8T6).
	NotifyOverdue = "overdue"
	// NotifyStale fires when the daily stale-nudge job (C9T5) sees an issue
	// in a non-done state whose updated_at is older than 30 days. One nudge
	// per issue until it is updated again; the mark is written to
	// issue_reminders with kind='stale'. No migration needed:
	// notification_prefs.event is unconstrained TEXT, so adding the key to
	// AllNotifyEvents is enough for prefs to work (absent row = defaults:
	// in_app on, email off).
	NotifyStale = "stale"
	// NotifyDigestDaily is the daily email digest (C10T2). It is NOT a
	// per-event notification: the digest job aggregates the user's last
	// 24h of digest-worthy notifications (issue.assigned, mention,
	// issue.state_changed) into ONE "email.digest" outbox row per day.
	// No migration needed: notification_prefs.event is unconstrained TEXT,
	// so adding the key to AllNotifyEvents is enough for prefs to work.
	// The digest is email-only and opt-in: absent row = email off, and
	// in_app defaults to false — the in_app flag is stored but unused
	// (see defaultNotificationPref). It is never passed to notifyTx, so no
	// per-event rows are ever emitted for this key.
	NotifyDigestDaily = "digest.daily"
)

// AllNotifyEvents lists every event type users can set delivery prefs for.
// SetNotificationPref rejects anything outside this list.
var AllNotifyEvents = []string{
	NotifyIssueAssigned,
	NotifyIssueUnassigned,
	NotifyCommentCreated,
	NotifyStateChanged,
	NotifyIntakeTriaged,
	NotifyMention,
	NotifyDueSoon,
	NotifyOverdue,
	NotifyStale,
	NotifyDigestDaily,
}

var (
	// ErrUnknownNotifyEvent is returned when a pref is set for an event
	// outside AllNotifyEvents.
	ErrUnknownNotifyEvent = errors.New("service: unknown notification event")
	// ErrNotificationNotFound is returned when marking a notification that
	// does not belong to the caller.
	ErrNotificationNotFound = errors.New("service: notification not found")
)

// Notification is one in-app notification row.
type Notification struct {
	ID        string          `json:"id"`
	UserID    string          `json:"user_id"`
	Type      string          `json:"type"`
	Title     string          `json:"title"`
	Payload   json.RawMessage `json:"payload"`
	ReadAt    *time.Time      `json:"read_at"`
	CreatedAt time.Time       `json:"created_at"`
}

// NotificationPref is one (user, event) delivery gate.
type NotificationPref struct {
	Event string `json:"event"`
	InApp bool   `json:"in_app"`
	Email bool   `json:"email"`
}

// notifyTx writes in-app notification rows and enqueues "email.notification"
// outbox rows for recipientIDs inside the caller's tx, gated by
// notification_prefs (absent = defaults: in_app on, email off). The actor
// is never notified of their own action. Inactive users are skipped.
// Returns the created in-app notifications for post-commit WS broadcast —
// call announceNotifications AFTER the tx commits, never inside it.
func notifyTx(ctx context.Context, tx pgx.Tx, eventType, title, emailBody string, payload map[string]any, actorID string, recipientIDs []string) ([]*Notification, error) {
	seen := make(map[string]bool, len(recipientIDs))
	var ids []string
	for _, id := range recipientIDs {
		id = strings.ToLower(strings.TrimSpace(id))
		if id == "" || seen[id] || id == strings.ToLower(strings.TrimSpace(actorID)) {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, nil
	}

	rows, err := tx.Query(ctx,
		`SELECT u.id::text, u.email::text,
		        COALESCE(p.in_app, TRUE), COALESCE(p.email, FALSE)
		   FROM users u
		   LEFT JOIN notification_prefs p
		     ON p.user_id = u.id AND p.event = $2
		  WHERE u.id = ANY($1::uuid[]) AND u.is_active`,
		ids, eventType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("service: marshal notification payload: %w", err)
	}

	// Drain the recipient rows before writing: pgx forbids issuing a new
	// query on the tx while rows are still open ("conn busy").
	type target struct {
		uid, email string
		inApp      bool
		sendEmail  bool
	}
	var targets []target
	for rows.Next() {
		var tg target
		if err := rows.Scan(&tg.uid, &tg.email, &tg.inApp, &tg.sendEmail); err != nil {
			return nil, err
		}
		targets = append(targets, tg)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var created []*Notification
	for _, tg := range targets {
		if tg.inApp {
			var n Notification
			if err := tx.QueryRow(ctx,
				`INSERT INTO notifications (user_id, type, title, payload)
				 VALUES ($1::uuid, $2, $3, $4::jsonb)
				 RETURNING id::text, created_at`,
				tg.uid, eventType, title, string(data)).Scan(&n.ID, &n.CreatedAt); err != nil {
				return nil, err
			}
			n.UserID, n.Type, n.Title, n.Payload = tg.uid, eventType, title, data
			created = append(created, &n)
		}
		if tg.sendEmail && tg.email != "" {
			body := title
			if emailBody != "" {
				body += "\n\n" + emailBody
			}
			if err := mail.Enqueue(ctx, tx, "email.notification", mail.Message{
				To:      tg.email,
				Subject: title,
				Body:    body,
			}); err != nil {
				return nil, err
			}
		}
	}
	return created, nil
}

// announceNotifications broadcasts notification.created on each
// recipient's user:{id} channel. Call AFTER the tx commits — never inside.
func announceNotifications(notifs []*Notification) {
	for _, n := range notifs {
		announce(
			[]string{userChannel(n.UserID)},
			EventNotificationCreated,
			map[string]any{
				"id":    n.ID,
				"type":  n.Type,
				"title": n.Title,
				"at":    n.CreatedAt,
			},
		)
	}
}

// actorDisplayName returns the user's display name (name, else email).
func actorDisplayName(ctx context.Context, q queryRower, actorID string) string {
	var name, email string
	if err := q.QueryRow(ctx,
		`SELECT COALESCE(NULLIF(name, ''), ''), email::text FROM users WHERE id = $1::uuid`,
		actorID).Scan(&name, &email); err != nil {
		return "Someone"
	}
	if name != "" {
		return name
	}
	return email
}

// issueWatchersTx returns the union of subscriber + assignee user ids for
// an issue — the recipient set for comment/state notifications.
func issueWatchersTx(ctx context.Context, tx pgx.Tx, issueID string) ([]string, error) {
	rows, err := tx.Query(ctx,
		`SELECT user_id::text FROM issue_subscribers WHERE issue_id = $1::uuid
		  UNION
		 SELECT user_id::text FROM issue_assignees WHERE issue_id = $1::uuid`,
		issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// issueNotifyContextTx returns the display id + name used in notification
// titles and payloads.
func issueNotifyContextTx(ctx context.Context, tx pgx.Tx, ident, issueID string) (displayID, name string, err error) {
	var seq int
	if err := tx.QueryRow(ctx,
		`SELECT sequence_id, name FROM issues WHERE id = $1::uuid`,
		issueID).Scan(&seq, &name); err != nil {
		return "", "", err
	}
	return ident + "-" + strconv.Itoa(seq), name, nil
}

// workspaceIDForProjectTx resolves the workspace id for webhook fan-out
// from a project id.
func workspaceIDForProjectTx(ctx context.Context, tx pgx.Tx, projectID string) (string, error) {
	var wsID string
	if err := tx.QueryRow(ctx,
		`SELECT workspace_id::text FROM projects WHERE id = $1::uuid`,
		projectID).Scan(&wsID); err != nil {
		return "", err
	}
	return wsID, nil
}

// ---------- notification queries (API) ----------

// ListNotifications returns the user's notifications, newest first, plus
// the unread count. limit is clamped to 1..100 (default 25).
func ListNotifications(ctx context.Context, pool *pgxpool.Pool, userID string, unreadOnly bool, limit int) ([]*Notification, int, error) {
	if limit <= 0 {
		limit = 25
	}
	if limit > 100 {
		limit = 100
	}
	filter := ""
	if unreadOnly {
		filter = "AND read_at IS NULL"
	}
	rows, err := pool.Query(ctx,
		`SELECT id::text, user_id::text, type, title, payload, read_at, created_at
		   FROM notifications
		  WHERE user_id = $1::uuid `+filter+`
		  ORDER BY created_at DESC
		  LIMIT $2`,
		userID, limit)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []*Notification{}
	for rows.Next() {
		var n Notification
		if err := rows.Scan(&n.ID, &n.UserID, &n.Type, &n.Title, &n.Payload, &n.ReadAt, &n.CreatedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, &n)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	var unread int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM notifications WHERE user_id = $1::uuid AND read_at IS NULL`,
		userID).Scan(&unread); err != nil {
		return nil, 0, err
	}
	return out, unread, nil
}

// MarkNotificationRead marks one notification read. Only the owner's row
// can be marked — anything else is ErrNotificationNotFound (no existence
// leak across users).
func MarkNotificationRead(ctx context.Context, pool *pgxpool.Pool, userID, id string) error {
	tag, err := pool.Exec(ctx,
		`UPDATE notifications SET read_at = now()
		  WHERE id = $1::uuid AND user_id = $2::uuid AND read_at IS NULL`,
		id, userID)
	if err != nil {
		if isInvalidUUID(err) {
			return ErrNotificationNotFound
		}
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotificationNotFound
	}
	return nil
}

// MarkAllNotificationsRead marks every unread notification of the user read.
func MarkAllNotificationsRead(ctx context.Context, pool *pgxpool.Pool, userID string) error {
	_, err := pool.Exec(ctx,
		`UPDATE notifications SET read_at = now()
		  WHERE user_id = $1::uuid AND read_at IS NULL`,
		userID)
	return err
}

// ListNotificationPrefs returns one pref per known event, filling absent
// rows with the defaults (in_app true, email false).
func ListNotificationPrefs(ctx context.Context, pool *pgxpool.Pool, userID string) ([]NotificationPref, error) {
	rows, err := pool.Query(ctx,
		`SELECT event, in_app, email FROM notification_prefs WHERE user_id = $1::uuid`,
		userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byEvent := map[string]NotificationPref{}
	for rows.Next() {
		var p NotificationPref
		if err := rows.Scan(&p.Event, &p.InApp, &p.Email); err != nil {
			return nil, err
		}
		byEvent[p.Event] = p
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]NotificationPref, 0, len(AllNotifyEvents))
	for _, ev := range AllNotifyEvents {
		if p, ok := byEvent[ev]; ok {
			out = append(out, p)
		} else {
			out = append(out, defaultNotificationPref(ev))
		}
	}
	return out, nil
}

// defaultNotificationPref returns the absent-row defaults for one event.
// Most events default to in_app on / email off (email is opt-in by
// convention). The digest is email-only — its in_app toggle is inert, so
// it defaults off to keep the prefs UI honest.
func defaultNotificationPref(event string) NotificationPref {
	if event == NotifyDigestDaily {
		return NotificationPref{Event: event, InApp: false, Email: false}
	}
	return NotificationPref{Event: event, InApp: true, Email: false}
}

// SetNotificationPref upserts the (user, event) delivery gate. Unknown
// events are rejected — prefs only exist for real notification types.
func SetNotificationPref(ctx context.Context, pool *pgxpool.Pool, userID, event string, inApp, email bool) (NotificationPref, error) {
	known := false
	for _, ev := range AllNotifyEvents {
		if ev == event {
			known = true
			break
		}
	}
	if !known {
		return NotificationPref{}, ErrUnknownNotifyEvent
	}
	var p NotificationPref
	if err := pool.QueryRow(ctx,
		`INSERT INTO notification_prefs (user_id, event, in_app, email)
		 VALUES ($1::uuid, $2, $3, $4)
		 ON CONFLICT (user_id, event)
		 DO UPDATE SET in_app = EXCLUDED.in_app, email = EXCLUDED.email
		 RETURNING event, in_app, email`,
		userID, event, inApp, email).Scan(&p.Event, &p.InApp, &p.Email); err != nil {
		return NotificationPref{}, err
	}
	return p, nil
}
