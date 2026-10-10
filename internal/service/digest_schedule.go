package service

// Digest scheduling granularity (C11T2) and per-user timezone (C13T1).
//
// Per-user digest schedule: frequency (daily|weekly, default daily),
// send-after hour (0-23 in the user's own timezone, default 8), and the
// IANA timezone the hour is evaluated in (default: the glance server's
// local zone — exactly the pre-C13T1 behavior for existing users).
// Stored in notification_prefs with NO migration, using the same
// unconstrained-TEXT event convention as C10T2's digest.daily key — but
// with the value encoded in the key suffix:
//
//	digest.frequency:daily | digest.frequency:weekly
//	digest.hour:0 ... digest.hour:23
//	digest.tz:Asia/Makassar  (any IANA name time.LoadLocation accepts)
//
// Why the value rides in the key: notification_prefs has no value column,
// only in_app/email booleans. A boolean could encode the two frequencies,
// but the 24-valued hour cannot ride booleans at all, so all prefs use
// one uniform key-suffix mechanism instead of two ad-hoc encodings. These
// keys are deliberately NOT in AllNotifyEvents: ListNotificationPrefs only
// lists known event keys (schedule rows stay invisible there) and
// SetNotificationPref rejects unknown events, so the rows are managed
// exclusively through GetDigestSchedule / SetDigestSchedule (exposed as
// GET/PUT /api/v1/digest-schedule). The in_app/email flags on schedule
// rows are meaningless and stored FALSE/FALSE.
//
// Timezone semantics (C13T1): the hour gate, the "today" claim window,
// and the digest_watermarks day are all computed in the user's zone, so a
// user crossing midnight in their own zone neither double-sends nor
// starves. DST transitions are handled by time.LoadLocation; the
// ambiguous local hour on fall-back resolves to the same local day, so
// the two passes share one watermark claim. Invalid zones are rejected
// at write (400) and never silently corrected — a malformed hand-edited
// row is ignored on read and the default applies instead.

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Digest schedule frequencies.
const (
	DigestFrequencyDaily  = "daily"
	DigestFrequencyWeekly = "weekly"
)

// Digest schedule defaults: daily, sent after 08:00 in the server-local
// zone (empty Timezone = server's local zone).
const (
	DefaultDigestFrequency = DigestFrequencyDaily
	DefaultDigestHour      = 8
)

// digestScheduleKeyPrefixes are the notification_prefs event prefixes
// carrying schedule values; the value follows the colon.
const (
	digestFrequencyKeyPrefix = "digest.frequency:"
	digestHourKeyPrefix      = "digest.hour:"
	digestTzKeyPrefix        = "digest.tz:"
)

// digestScheduleRowFilter matches every notification_prefs row that
// carries part of a digest schedule.
const digestScheduleRowFilter = `(event LIKE 'digest.frequency:%' OR event LIKE 'digest.hour:%' OR event LIKE 'digest.tz:%')`

var (
	// ErrBadDigestFrequency is returned when the frequency is not
	// daily|weekly.
	ErrBadDigestFrequency = errors.New("service: digest frequency must be daily or weekly")
	// ErrBadDigestHour is returned when the hour is outside 0-23.
	ErrBadDigestHour = errors.New("service: digest hour must be 0-23")
	// ErrBadDigestTimezone is returned when the timezone is not a
	// loadable IANA name. Empty is not an error: it selects the
	// server-local default.
	ErrBadDigestTimezone = errors.New("service: digest timezone must be a valid IANA timezone name")
)

// DigestSchedule is one user's digest cadence: how often the digest may
// be sent, the hour (in the user's timezone) it becomes due, and the
// IANA timezone the hour is evaluated in. An empty Timezone means the
// glance server's local zone (pre-C13T1 behavior, and the default for
// users who never set one).
type DigestSchedule struct {
	Frequency string `json:"frequency"`
	Hour      int    `json:"hour"`
	Timezone  string `json:"tz"`
}

// EffectiveTimezone returns the IANA name of the zone the digest hour is
// evaluated in: the stored pref, or the server's local zone name when
// the user never set one.
func (s DigestSchedule) EffectiveTimezone() string {
	if s.Timezone != "" {
		return s.Timezone
	}
	return time.Local.String()
}

// scheduleLocation resolves the zone a digest pass evaluates a user in.
// A stored-but-unloadable zone can only arise from hand-edited rows (the
// write path validates); the digest pass must never fail on it, so it
// falls back to the server-local default.
func scheduleLocation(sched DigestSchedule) *time.Location {
	if sched.Timezone == "" {
		return time.Local
	}
	if loc, err := time.LoadLocation(sched.Timezone); err == nil {
		return loc
	}
	return time.Local
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
		return
	}
	if v, ok := strings.CutPrefix(event, digestTzKeyPrefix); ok {
		// An unloadable zone (hand-edited row) is ignored — the digest
		// pass never fails on a bad pref row; the default applies.
		if _, err := time.LoadLocation(v); err == nil {
			sched.Timezone = v
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
		  WHERE user_id = ANY($1::uuid[]) AND `+digestScheduleRowFilter,
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

// validateDigestTimezone checks that tz is empty (server-local default)
// or a loadable IANA timezone name.
func validateDigestTimezone(tz string) error {
	if tz == "" {
		return nil
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return ErrBadDigestTimezone
	}
	return nil
}

// SetDigestSchedule validates and stores the user's digest schedule,
// replacing any previous schedule rows (exactly one frequency row, one
// hour row, and one tz row remain afterwards; an empty tz leaves no tz
// row, selecting the server-local default). Invalid input is rejected
// WITHOUT touching the stored schedule.
func SetDigestSchedule(ctx context.Context, pool *pgxpool.Pool, userID, frequency string, hour int, tz string) (DigestSchedule, error) {
	if frequency != DigestFrequencyDaily && frequency != DigestFrequencyWeekly {
		return DigestSchedule{}, ErrBadDigestFrequency
	}
	if hour < 0 || hour > 23 {
		return DigestSchedule{}, ErrBadDigestHour
	}
	if err := validateDigestTimezone(tz); err != nil {
		return DigestSchedule{}, err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return DigestSchedule{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx,
		`DELETE FROM notification_prefs
		  WHERE user_id = $1::uuid AND `+digestScheduleRowFilter,
		userID); err != nil {
		return DigestSchedule{}, err
	}
	events := []string{
		digestFrequencyKeyPrefix + frequency,
		digestHourKeyPrefix + strconv.Itoa(hour),
	}
	if tz != "" {
		events = append(events, digestTzKeyPrefix+tz)
	}
	for _, event := range events {
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
	return DigestSchedule{Frequency: frequency, Hour: hour, Timezone: tz}, nil
}
