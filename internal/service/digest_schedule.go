package service

// Digest scheduling granularity (C11T2).
//
// Per-user digest schedule: frequency (daily|weekly, default daily) and
// send-after hour (0-23 in the glance server's local timezone, default
// 8). Stored in notification_prefs with NO migration, using the same
// unconstrained-TEXT event convention as C10T2's digest.daily key — but
// with the value encoded in the key suffix:
//
//	digest.frequency:daily | digest.frequency:weekly
//	digest.hour:0 ... digest.hour:23
//
// Why the value rides in the key: notification_prefs has no value column,
// only in_app/email booleans. A boolean could encode the two frequencies,
// but the 24-valued hour cannot ride booleans at all, so both prefs use
// one uniform key-suffix mechanism instead of two ad-hoc encodings. These
// keys are deliberately NOT in AllNotifyEvents: ListNotificationPrefs only
// lists known event keys (schedule rows stay invisible there) and
// SetNotificationPref rejects unknown events, so the rows are managed
// exclusively through GetDigestSchedule / SetDigestSchedule (exposed as
// GET/PUT /api/v1/digest-schedule). The in_app/email flags on schedule
// rows are meaningless and stored FALSE/FALSE.
//
// Timezone caveat (documented honestly): the hour is interpreted in the
// glance server's local timezone (time.Local), NOT the user's. A user in
// UTC+9 on a UTC server gets the digest at 08:00 UTC. Per-user timezones
// are future work.

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Digest schedule frequencies.
const (
	DigestFrequencyDaily  = "daily"
	DigestFrequencyWeekly = "weekly"
)

// Digest schedule defaults: daily, sent after 08:00 server-local.
const (
	DefaultDigestFrequency = DigestFrequencyDaily
	DefaultDigestHour      = 8
)

// digestScheduleKeyPrefixes are the notification_prefs event prefixes
// carrying schedule values; the value follows the colon.
const (
	digestFrequencyKeyPrefix = "digest.frequency:"
	digestHourKeyPrefix      = "digest.hour:"
)

var (
	// ErrBadDigestFrequency is returned when the frequency is not
	// daily|weekly.
	ErrBadDigestFrequency = errors.New("service: digest frequency must be daily or weekly")
	// ErrBadDigestHour is returned when the hour is outside 0-23.
	ErrBadDigestHour = errors.New("service: digest hour must be 0-23")
)

// DigestSchedule is one user's digest cadence: how often the digest may
// be sent, and the server-local hour it becomes due.
type DigestSchedule struct {
	Frequency string `json:"frequency"`
	Hour      int    `json:"hour"`
}

// defaultDigestSchedule is the schedule for users with no stored rows.
func defaultDigestSchedule() DigestSchedule {
	return DigestSchedule{Frequency: DefaultDigestFrequency, Hour: DefaultDigestHour}
}

// applyDigestScheduleEvent folds one stored schedule event into sched.
// Malformed values (hand-edited rows) are ignored — the digest pass must
// never fail because of a bad pref row; defaults apply instead.
func applyDigestScheduleEvent(sched *DigestSchedule, event string) {
	if v, ok := strings.CutPrefix(event, digestFrequencyKeyPrefix); ok {
		if v == DigestFrequencyDaily || v == DigestFrequencyWeekly {
			sched.Frequency = v
		}
		return
	}
	if v, ok := strings.CutPrefix(event, digestHourKeyPrefix); ok {
		if h, err := strconv.Atoi(v); err == nil && h >= 0 && h <= 23 {
			sched.Hour = h
		}
	}
}

// loadDigestSchedules reads digest schedules for the given users in one
// query. Users with no schedule rows are absent from the map — callers
// fall back to defaultDigestSchedule().
func loadDigestSchedules(ctx context.Context, pool *pgxpool.Pool, userIDs []string) (map[string]DigestSchedule, error) {
	out := map[string]DigestSchedule{}
	if len(userIDs) == 0 {
		return out, nil
	}
	rows, err := pool.Query(ctx,
		`SELECT user_id::text, event FROM notification_prefs
		  WHERE user_id = ANY($1::uuid[])
		    AND (event LIKE 'digest.frequency:%' OR event LIKE 'digest.hour:%')`,
		userIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var uid, event string
		if err := rows.Scan(&uid, &event); err != nil {
			return nil, err
		}
		sched := out[uid]
		if _, ok := out[uid]; !ok {
			sched = defaultDigestSchedule()
		}
		applyDigestScheduleEvent(&sched, event)
		out[uid] = sched
	}
	return out, rows.Err()
}

// GetDigestSchedule returns the user's digest schedule; absent rows yield
// the defaults (daily, 08:00 server-local).
func GetDigestSchedule(ctx context.Context, pool *pgxpool.Pool, userID string) (DigestSchedule, error) {
	m, err := loadDigestSchedules(ctx, pool, []string{userID})
	if err != nil {
		return defaultDigestSchedule(), err
	}
	if s, ok := m[userID]; ok {
		return s, nil
	}
	return defaultDigestSchedule(), nil
}

// SetDigestSchedule validates and stores the user's digest schedule,
// replacing any previous schedule rows (exactly one frequency row and
// one hour row remain afterwards). Invalid input is rejected WITHOUT
// touching the stored schedule.
func SetDigestSchedule(ctx context.Context, pool *pgxpool.Pool, userID, frequency string, hour int) (DigestSchedule, error) {
	if frequency != DigestFrequencyDaily && frequency != DigestFrequencyWeekly {
		return DigestSchedule{}, ErrBadDigestFrequency
	}
	if hour < 0 || hour > 23 {
		return DigestSchedule{}, ErrBadDigestHour
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return DigestSchedule{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx,
		`DELETE FROM notification_prefs
		  WHERE user_id = $1::uuid
		    AND (event LIKE 'digest.frequency:%' OR event LIKE 'digest.hour:%')`,
		userID); err != nil {
		return DigestSchedule{}, err
	}
	for _, event := range []string{
		digestFrequencyKeyPrefix + frequency,
		digestHourKeyPrefix + strconv.Itoa(hour),
	} {
		// in_app/email are meaningless on schedule rows; FALSE/FALSE keeps
		// them inert under every boolean-based pref reading.
		if _, err := tx.Exec(ctx,
			`INSERT INTO notification_prefs (user_id, event, in_app, email)
			 VALUES ($1::uuid, $2, FALSE, FALSE)`,
			userID, event); err != nil {
			return DigestSchedule{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return DigestSchedule{}, err
	}
	return DigestSchedule{Frequency: frequency, Hour: hour}, nil
}
