package service

// C28T0: recurring issue schedules — pure scheduling math (NextOccurrence)
// and template rendering. These are DB-free and table-driven.

import (
	"testing"
	"time"
)

func utc(y int, m time.Month, d, h, min, s int) time.Time {
	return time.Date(y, m, d, h, min, s, 0, time.UTC)
}

func dailySchedule(startsOn time.Time, hour, interval int) *RecurringSchedule {
	return &RecurringSchedule{
		Frequency: "daily",
		Interval:  interval,
		Hour:      hour,
		StartsOn:  startsOn,
	}
}

func TestNextOccurrenceDaily(t *testing.T) {
	s := dailySchedule(utc(2026, 10, 1, 0, 0, 0), 9, 1)

	cases := []struct {
		name string
		from time.Time
		want time.Time
	}{
		{"before first run", utc(2026, 10, 1, 0, 0, 0), utc(2026, 10, 1, 9, 0, 0)},
		{"exactly on run", utc(2026, 10, 1, 9, 0, 0), utc(2026, 10, 1, 9, 0, 0)},
		{"just after run", utc(2026, 10, 1, 9, 0, 1), utc(2026, 10, 2, 9, 0, 0)},
		{"mid day skips past run", utc(2026, 10, 5, 12, 0, 0), utc(2026, 10, 6, 9, 0, 0)},
		{"far future", utc(2027, 3, 14, 8, 59, 59), utc(2027, 3, 14, 9, 0, 0)},
	}
	for _, c := range cases {
		if got := s.NextOccurrence(c.from); !got.Equal(c.want) {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

func TestNextOccurrenceDailyInterval(t *testing.T) {
	// Every 3 days from 2026-10-01 09:00: runs on 1, 4, 7, 10...
	s := dailySchedule(utc(2026, 10, 1, 0, 0, 0), 9, 3)

	cases := []struct {
		name string
		from time.Time
		want time.Time
	}{
		{"on first run day", utc(2026, 10, 1, 0, 0, 0), utc(2026, 10, 1, 9, 0, 0)},
		{"off day jumps to next run", utc(2026, 10, 2, 0, 0, 0), utc(2026, 10, 4, 9, 0, 0)},
		{"after run on run day", utc(2026, 10, 4, 10, 0, 0), utc(2026, 10, 7, 9, 0, 0)},
		{"from 10-05 lands on 10-07", utc(2026, 10, 5, 0, 0, 0), utc(2026, 10, 7, 9, 0, 0)},
	}
	for _, c := range cases {
		if got := s.NextOccurrence(c.from); !got.Equal(c.want) {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

func TestNextOccurrenceDailyHourPreserved(t *testing.T) {
	s := dailySchedule(utc(2026, 10, 1, 0, 0, 0), 17, 1)
	if got := s.NextOccurrence(utc(2026, 10, 3, 23, 59, 59)); !got.Equal(utc(2026, 10, 4, 17, 0, 0)) {
		t.Errorf("got %s", got)
	}
	s0 := dailySchedule(utc(2026, 10, 1, 0, 0, 0), 0, 1)
	if got := s0.NextOccurrence(utc(2026, 10, 1, 0, 0, 0)); !got.Equal(utc(2026, 10, 1, 0, 0, 0)) {
		t.Errorf("midnight hour: got %s", got)
	}
}

func TestNextOccurrenceWeekly(t *testing.T) {
	// 2026-10-05 is a Monday.
	newWeekly := func(days []int, interval int, startsOn time.Time) *RecurringSchedule {
		return &RecurringSchedule{
			Frequency:  "weekly",
			Interval:   interval,
			DaysOfWeek: days,
			Hour:       9,
			StartsOn:   startsOn,
		}
	}

	s := newWeekly([]int{1}, 1, utc(2026, 10, 5, 0, 0, 0))
	cases := []struct {
		name string
		from time.Time
		want time.Time
	}{
		{"first monday", utc(2026, 10, 5, 0, 0, 0), utc(2026, 10, 5, 9, 0, 0)},
		{"tuesday rolls to next monday", utc(2026, 10, 6, 0, 0, 0), utc(2026, 10, 12, 9, 0, 0)},
		{"sunday night", utc(2026, 10, 11, 23, 0, 0), utc(2026, 10, 12, 9, 0, 0)},
	}
	for _, c := range cases {
		if got := s.NextOccurrence(c.from); !got.Equal(c.want) {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}

	// Multi-weekday Mon/Wed/Fri.
	mwf := newWeekly([]int{1, 3, 5}, 1, utc(2026, 10, 5, 0, 0, 0))
	mwfCases := []struct {
		name string
		from time.Time
		want time.Time
	}{
		{"wed morning before run", utc(2026, 10, 7, 0, 0, 0), utc(2026, 10, 7, 9, 0, 0)},
		{"wed afternoon jumps to friday", utc(2026, 10, 7, 12, 0, 0), utc(2026, 10, 9, 9, 0, 0)},
		{"friday night jumps to monday", utc(2026, 10, 9, 12, 0, 0), utc(2026, 10, 12, 9, 0, 0)},
		{"saturday", utc(2026, 10, 10, 0, 0, 0), utc(2026, 10, 12, 9, 0, 0)},
	}
	for _, c := range mwfCases {
		if got := mwf.NextOccurrence(c.from); !got.Equal(c.want) {
			t.Errorf("mwf %s: got %s, want %s", c.name, got, c.want)
		}
	}

	// Bi-weekly: every other Monday from 2026-10-05.
	bi := newWeekly([]int{1}, 2, utc(2026, 10, 5, 0, 0, 0))
	if got := bi.NextOccurrence(utc(2026, 10, 6, 0, 0, 0)); !got.Equal(utc(2026, 10, 19, 9, 0, 0)) {
		t.Errorf("biweekly skipped week: got %s", got)
	}
	if got := bi.NextOccurrence(utc(2026, 10, 19, 9, 0, 0)); !got.Equal(utc(2026, 10, 19, 9, 0, 0)) {
		t.Errorf("biweekly on run: got %s", got)
	}

	// starts_on mid-week, first weekday after starts_on.
	mid := newWeekly([]int{1}, 1, utc(2026, 10, 7, 0, 0, 0)) // starts Wednesday
	if got := mid.NextOccurrence(utc(2026, 10, 7, 0, 0, 0)); !got.Equal(utc(2026, 10, 12, 9, 0, 0)) {
		t.Errorf("mid-week start: got %s", got)
	}
}

func TestNextOccurrenceMonthly(t *testing.T) {
	newMonthly := func(day, interval int, startsOn time.Time) *RecurringSchedule {
		d := day
		return &RecurringSchedule{
			Frequency:  "monthly",
			Interval:   interval,
			DayOfMonth: &d,
			Hour:       9,
			StartsOn:   startsOn,
		}
	}

	s := newMonthly(15, 1, utc(2026, 10, 1, 0, 0, 0))
	cases := []struct {
		name string
		from time.Time
		want time.Time
	}{
		{"this month", utc(2026, 10, 1, 0, 0, 0), utc(2026, 10, 15, 9, 0, 0)},
		{"after run day jumps month", utc(2026, 10, 15, 10, 0, 0), utc(2026, 11, 15, 9, 0, 0)},
		{"late month", utc(2026, 10, 20, 0, 0, 0), utc(2026, 11, 15, 9, 0, 0)},
	}
	for _, c := range cases {
		if got := s.NextOccurrence(c.from); !got.Equal(c.want) {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}

	// Clamp: day 31 in February (2026 is not a leap year).
	clamp := newMonthly(31, 1, utc(2026, 1, 15, 0, 0, 0))
	if got := clamp.NextOccurrence(utc(2026, 2, 1, 0, 0, 0)); !got.Equal(utc(2026, 2, 28, 9, 0, 0)) {
		t.Errorf("feb clamp: got %s", got)
	}
	if got := clamp.NextOccurrence(utc(2026, 2, 28, 10, 0, 0)); !got.Equal(utc(2026, 3, 31, 9, 0, 0)) {
		t.Errorf("after feb clamp: got %s", got)
	}

	// Leap year: day 29 in Feb 2028.
	leap := newMonthly(29, 1, utc(2027, 1, 1, 0, 0, 0))
	if got := leap.NextOccurrence(utc(2027, 2, 1, 0, 0, 0)); !got.Equal(utc(2027, 2, 28, 9, 0, 0)) {
		t.Errorf("non-leap feb 29 clamps to 28: got %s", got)
	}
	if got := leap.NextOccurrence(utc(2028, 1, 30, 0, 0, 0)); !got.Equal(utc(2028, 2, 29, 9, 0, 0)) {
		t.Errorf("leap feb 29: got %s", got)
	}

	// Every 2 months on the 10th.
	bi := newMonthly(10, 2, utc(2026, 1, 1, 0, 0, 0))
	if got := bi.NextOccurrence(utc(2026, 3, 1, 0, 0, 0)); !got.Equal(utc(2026, 3, 10, 9, 0, 0)) {
		t.Errorf("bimonthly on month: got %s", got)
	}
	if got := bi.NextOccurrence(utc(2026, 3, 11, 0, 0, 0)); !got.Equal(utc(2026, 5, 10, 9, 0, 0)) {
		t.Errorf("bimonthly skips april: got %s", got)
	}
}

func TestNextOccurrenceEndsOn(t *testing.T) {
	ends := utc(2026, 10, 5, 0, 0, 0)
	s := &RecurringSchedule{
		Frequency: "daily",
		Interval:  1,
		Hour:      9,
		StartsOn:  utc(2026, 10, 1, 0, 0, 0),
		EndsOn:    &ends,
	}

	// Occurrence ON ends_on still runs (only strictly-after exhausts).
	if got := s.NextOccurrence(utc(2026, 10, 5, 0, 0, 0)); !got.Equal(utc(2026, 10, 5, 9, 0, 0)) {
		t.Errorf("on ends_on: got %s", got)
	}
	// Past ends_on → zero time (exhausted).
	if got := s.NextOccurrence(utc(2026, 10, 6, 0, 0, 0)); !got.IsZero() {
		t.Errorf("past ends_on: got %s, want zero", got)
	}
	if got := s.NextOccurrence(utc(2026, 10, 5, 10, 0, 0)); !got.IsZero() {
		t.Errorf("same day after run: got %s, want zero", got)
	}
}

func TestNextOccurrenceInvalidFrequency(t *testing.T) {
	s := &RecurringSchedule{Frequency: "yearly", Interval: 1, Hour: 9, StartsOn: utc(2026, 10, 1, 0, 0, 0)}
	if got := s.NextOccurrence(utc(2026, 10, 1, 0, 0, 0)); !got.IsZero() {
		t.Errorf("bad frequency: got %s, want zero", got)
	}
}

func TestRenderTemplates(t *testing.T) {
	desc := "Sprint starting {{date}} — review at {{datetime}}. Keep {{unknown}}."
	s := &RecurringSchedule{
		TitleTemplate:       "Daily standup {{date}}",
		DescriptionTemplate: &desc,
	}
	runAt := utc(2026, 10, 15, 9, 0, 0)

	if got := s.RenderTitle(runAt); got != "Daily standup 2026-10-15" {
		t.Errorf("RenderTitle = %q", got)
	}
	want := "Sprint starting 2026-10-15 — review at 2026-10-15T09:00:00Z. Keep {{unknown}}."
	if got := s.RenderDescription(runAt); got != want {
		t.Errorf("RenderDescription = %q", got)
	}

	// Nil description renders empty; no tokens pass through untouched.
	s2 := &RecurringSchedule{TitleTemplate: "No tokens here"}
	if got := s2.RenderDescription(runAt); got != "" {
		t.Errorf("nil description = %q, want empty", got)
	}
	if got := s2.RenderTitle(runAt); got != "No tokens here" {
		t.Errorf("tokenless title = %q", got)
	}
}
