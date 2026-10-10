package service

// Email digest (C10T2).
//
// Once a day the digest pass aggregates each digest-opted-in user's
// digest-worthy events since the last successfully-sent digest into ONE
// email. Digest-worthy = the
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
//
// Scheduling granularity (C11T2): each opted-in user has a digest schedule
// (frequency daily|weekly, send-after hour 0-23 server-local — see
// digest_schedule.go). The pass gates every user on due-ness BEFORE the
// tx: the server-local hour gate (current hour >= pref hour) plus the
// frequency gate (daily: no watermark for today; weekly: last watermark
// at least 7 days old). A skipped pass claims nothing, so a later pass
// can still send. The pass runs hourly (ticker.DigestTicker) because the
// hour pref only makes sense with sub-daily passes — a 24h ticker firing
// at a fixed wall-clock time would starve users whose pref hour is later
// than the pass time. Watermark claiming stays the race guard: two
// concurrent passes may both see "due", exactly one wins the INSERT.
//
// Aggregation window (C12T0): notifications in (last_watermark_date,
// now], i.e. since the last successfully-sent digest — capped at 7 days
// for weekly users and 24h for daily users, one code path for both. A
// weekly digest honestly covers the trailing 7 days (previously: the
// trailing 24h — a dishonest "weekly" digest, fixed here); a daily
// digest covers the trailing ~24h, unchanged from before.
//
// Empty window → no email, no watermark claim (existing behavior — a
// re-run converges silently).

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

// digestWindowCaps are the maximum aggregation windows per frequency:
// the digest covers notifications in (last_watermark_date, now], so a
// weekly digest honestly aggregates the trailing 7 days and a daily
// digest the trailing ~24h, one code path for both.
const (
	digestDailyWindowCap  = 24 * time.Hour
	digestWeeklyWindowCap = 7 * 24 * time.Hour
)

// digestWindowCap returns the maximum aggregation window for a digest
// frequency.
func digestWindowCap(frequency string) time.Duration {
	if frequency == DigestFrequencyWeekly {
		return digestWeeklyWindowCap
	}
	return digestDailyWindowCap
}

// digestWindowPhrase is the honest window label used in the digest
// subject and body.
func digestWindowPhrase(frequency string) string {
	if frequency == DigestFrequencyWeekly {
		return "past 7 days"
	}
	return "past 24 hours"
}

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
// explicit now makes the aggregation window and the digest_date
// watermark deterministic under a test clock: production passes
// time.Now(), tests pass a fake.
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
	id       string
	email    string
	name     string
	schedule DigestSchedule
}

// digestRecipients returns active users with the digest.daily email pref
// on. Absent pref row = email off (opt-in convention), so those users
// never appear here. Each recipient's digest schedule is loaded in one
// batched query (C11T2); users with no schedule rows get the defaults.
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(out))
	for _, u := range out {
		ids = append(ids, u.id)
	}
	schedules, err := loadDigestSchedules(ctx, pool, ids)
	if err != nil {
		return nil, err
	}
	for i := range out {
		if s, ok := schedules[out[i].id]; ok {
			out[i].schedule = s
		} else {
			out[i].schedule = defaultDigestSchedule()
		}
	}
	return out, nil
}

// digestEvent is one digest-worthy notification row.
type digestEvent struct {
	eventType string
	title     string
}

// digestWindowStart returns the start of one user's aggregation
// window: the send time of the last successfully-sent digest, floored
// at the frequency's cap (7 days for weekly, 24h for daily) so a stale
// or absent watermark cannot drag ancient events into the digest. A
// user with no watermark gets the trailing cap period. The strict lower
// bound pairs with claimDigestDay's sent_at: each digest covers exactly
// the notifications that arrived after the previous digest was sent —
// no gaps, no double-sends.
func digestWindowStart(ctx context.Context, pool *pgxpool.Pool, userID, frequency string, now time.Time) (time.Time, error) {
	cap := digestWindowCap(frequency)
	floor := now.Add(-cap)
	var last *time.Time
	if err := pool.QueryRow(ctx,
		`SELECT MAX(sent_at) FROM digest_watermarks WHERE user_id = $1::uuid`,
		userID).Scan(&last); err != nil {
		return time.Time{}, err
	}
	if last == nil || last.Before(floor) {
		return floor, nil
	}
	return *last, nil
}

// digestOneUser aggregates one user's digest-worthy events in the
// (last_watermark_date, now] window and, when there is at least one,
// claims the day's watermark and enqueues a single "email.digest"
// outbox row in one tx. Empty digests send nothing and claim nothing.
func digestOneUser(ctx context.Context, pool *pgxpool.Pool, u digestRecipient, now time.Time) error {
	since, err := digestWindowStart(ctx, pool, u.id, u.schedule.Frequency, now)
	if err != nil {
		return err
	}
	rows, err := pool.Query(ctx,
		`SELECT type, title FROM notifications
		  WHERE user_id = $1::uuid
		    AND type = ANY($2)
		    AND created_at > $3
		    AND created_at <= $4
		  ORDER BY created_at`,
		u.id, digestEventTypes, since, now)
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

	// C11T2 due-ness: a not-due pass sends nothing and claims nothing, so
	// a later pass (same day for daily, later week for weekly) can still
	// send. The watermark claim inside the tx stays the race guard for
	// concurrent passes.
	due, err := digestDue(ctx, pool, u.id, u.schedule, now)
	if err != nil {
		return err
	}
	if !due {
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

	freqWord := "daily"
	if u.schedule.Frequency == DigestFrequencyWeekly {
		freqWord = "weekly"
	}
	window := digestWindowPhrase(u.schedule.Frequency)
	if err := mail.Enqueue(ctx, tx, "email.digest", mail.Message{
		To:      u.email,
		Subject: fmt.Sprintf("Your %s digest — %s", freqWord, window),
		Body:    buildDigestBody(u.name, window, events),
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// digestDue reports whether the user's digest is due at now (C11T2). The
// hour gate compares against the server's local timezone (documented
// caveat — not the user's timezone). Daily users are due when no
// watermark exists for today; weekly users when the last watermark is at
// least 7 days old (inclusive boundary: last_sent <= today-7d). A user
// with no watermark at all is due on the first pass.
func digestDue(ctx context.Context, pool *pgxpool.Pool, userID string, sched DigestSchedule, now time.Time) (bool, error) {
	if now.In(time.Local).Hour() < sched.Hour {
		return false, nil
	}
	today := now.UTC().Truncate(24 * time.Hour)
	if sched.Frequency == DigestFrequencyWeekly {
		var last *time.Time
		if err := pool.QueryRow(ctx,
			`SELECT MAX(digest_date) FROM digest_watermarks WHERE user_id = $1::uuid`,
			userID).Scan(&last); err != nil {
			return false, err
		}
		if last == nil {
			return true, nil
		}
		return !last.After(today.AddDate(0, 0, -7)), nil
	}
	var sentToday bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM digest_watermarks
		                WHERE user_id = $1::uuid AND digest_date = $2::date)`,
		userID, today).Scan(&sentToday); err != nil {
		return false, err
	}
	return !sentToday, nil
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
// written by notifyTx at event time. window is the honest aggregation
// window label ("past 7 days" / "past 24 hours").
func buildDigestBody(name, window string, events []digestEvent) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Hi %s,\n\nHere's what happened in the %s:\n", name, window)
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
	b.WriteString("\n—\nYou're receiving this because you enabled the digest email in Notifications.\n")
	return b.String()
}
