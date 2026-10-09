package service

// Email digest (C10T2).
//
// Once a day the digest pass aggregates each digest-opted-in user's last
// 24h of digest-worthy events into ONE email. Digest-worthy = the
// notification types that already carry the right recipient gating at
// notify time: issue.assigned (the assignee), mention (the mentioned
// user), issue.state_changed (watchers + assignees). The notifications
// table is the event source: it is the only 24h event log for all three
// (e.g. issue_assignees has no created_at), and the recipient gating
// above is exactly "assigned to me / @mentions / state changes on
// watched-or-assigned issues".
//
// One digest_watermarks row per user per day (migration 000036) makes
// re-runs idempotent: the row is claimed with INSERT ... ON CONFLICT DO
// NOTHING inside the same tx as the "email.digest" outbox row, so a
// second pass the same day (or a concurrent pass) sends nothing. The
// watermark is claimed ONLY when an email is actually sent — an empty
// pass leaves no row, so events arriving later the same day can still be
// picked up by a later pass.
//
// Persistence choice (documented): the pref rides notification_prefs via
// the unconstrained-TEXT event convention (no pref migration — same as
// the mention/stale precedent); the per-user-per-day watermark does NOT
// fit issue_reminders (that table is keyed per issue, not per user), so
// it gets its own table. The digest is email-only: users opt in with the
// digest.daily email pref (default off); per-event email prefs are
// independent — a user with issue.assigned email ON gets both the
// per-event email and the digest.
//
// In-process by design (same known limitation as the cycle, reminder, and
// stale tickers, spec §3): one instance only in v1.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"glance/internal/mail"
)

// digestWindow is how far back the digest aggregates.
const digestWindow = 24 * time.Hour

// digestEventTypes are the notification types aggregated into the digest.
var digestEventTypes = []string{NotifyIssueAssigned, NotifyMention, NotifyStateChanged}

// digestSection renders one digest section header for an event type.
func digestSection(eventType string) string {
	switch eventType {
	case NotifyIssueAssigned:
		return "Assigned to you"
	case NotifyMention:
		return "Mentions"
	case NotifyStateChanged:
		return "State changes"
	default:
		return eventType
	}
}

// RunDigest performs one digest pass against the given clock. The
// explicit now makes the 24h window and the digest_date watermark
// deterministic under a test clock: production passes time.Now(), tests
// pass a fake.
func RunDigest(ctx context.Context, pool *pgxpool.Pool, now time.Time) error {
	users, err := digestRecipients(ctx, pool)
	if err != nil {
		return err
	}
	for _, u := range users {
		if err := digestOneUser(ctx, pool, u, now); err != nil {
			return err
		}
	}
	return nil
}

type digestRecipient struct {
	id    string
	email string
	name  string
}

// digestRecipients returns active users with the digest.daily email pref
// on. Absent pref row = email off (opt-in convention), so those users
// never appear here.
func digestRecipients(ctx context.Context, pool *pgxpool.Pool) ([]digestRecipient, error) {
	rows, err := pool.Query(ctx,
		`SELECT u.id::text, u.email::text, COALESCE(NULLIF(u.name, ''), u.email::text)
		   FROM users u
		   JOIN notification_prefs p ON p.user_id = u.id
		  WHERE p.event = $1 AND p.email = TRUE AND u.is_active`,
		NotifyDigestDaily)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []digestRecipient
	for rows.Next() {
		var u digestRecipient
		if err := rows.Scan(&u.id, &u.email, &u.name); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// digestEvent is one digest-worthy notification row.
type digestEvent struct {
	eventType string
	title     string
}

// digestOneUser aggregates one user's last-24h digest-worthy events and,
// when there is at least one, claims the day's watermark and enqueues a
// single "email.digest" outbox row in one tx. Empty digests send nothing
// and claim nothing.
func digestOneUser(ctx context.Context, pool *pgxpool.Pool, u digestRecipient, now time.Time) error {
	rows, err := pool.Query(ctx,
		`SELECT type, title FROM notifications
		  WHERE user_id = $1::uuid
		    AND type = ANY($2)
		    AND created_at >= $3
		  ORDER BY created_at`,
		u.id, digestEventTypes, now.Add(-digestWindow))
	if err != nil {
		return err
	}
	var events []digestEvent
	for rows.Next() {
		var e digestEvent
		if err := rows.Scan(&e.eventType, &e.title); err != nil {
			rows.Close()
			return err
		}
		events = append(events, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(events) == 0 {
		// Empty digest: no email, no watermark — a later pass the same
		// day can still pick up events that arrive after this pass.
		return nil
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	claimed, err := claimDigestDay(ctx, tx, u.id, now)
	if err != nil {
		return err
	}
	if !claimed {
		// Another pass already sent today's digest — converge silently.
		return nil
	}

	if err := mail.Enqueue(ctx, tx, "email.digest", mail.Message{
		To:      u.email,
		Subject: fmt.Sprintf("Your daily glance digest (%d update%s)", len(events), pluralS(len(events))),
		Body:    buildDigestBody(u.name, events),
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// claimDigestDay inserts the per-user-per-day watermark row. Returns false
// when the row already exists (another pass sent today's digest).
func claimDigestDay(ctx context.Context, tx pgx.Tx, userID string, now time.Time) (bool, error) {
	day := now.UTC().Truncate(24 * time.Hour)
	var claimed string
	err := tx.QueryRow(ctx,
		`INSERT INTO digest_watermarks (user_id, digest_date, sent_at)
		 VALUES ($1::uuid, $2::date, $3)
		 ON CONFLICT (user_id, digest_date) DO NOTHING
		 RETURNING user_id::text`,
		userID, day, now).Scan(&claimed)
	if err != nil {
		// No RETURNING row = conflict absorbed by the guard → another
		// pass owns today's digest.
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// buildDigestBody renders the digest email: one section per event type,
// one bullet per event. Event titles are the human-readable titles
// written by notifyTx at event time.
func buildDigestBody(name string, events []digestEvent) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Hi %s,\n\nHere's what happened in the last 24 hours:\n", name)
	for _, et := range digestEventTypes {
		var titles []string
		for _, e := range events {
			if e.eventType == et {
				titles = append(titles, e.title)
			}
		}
		if len(titles) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n%s (%d):\n", digestSection(et), len(titles))
		for _, t := range titles {
			fmt.Fprintf(&b, "- %s\n", t)
		}
	}
	b.WriteString("\n—\nYou're receiving this because you enabled the daily digest email in Notifications.\n")
	return b.String()
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
