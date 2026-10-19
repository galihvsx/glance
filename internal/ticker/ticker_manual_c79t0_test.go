package ticker

// Manual start/end vs the ticker (C79T0): the documented interplay
// contract — a manually-started cycle is never reverted to upcoming by
// the activate pass, and it still auto-completes at end_date via the
// complete pass. Manual end needs no ticker test: EndCycle delegates to
// the exact CompleteCycle the ticker calls (covered by
// TestTickerCompletesEndedCycle and the service EndCycle tests).

import (
	"testing"
	"time"

	"glance/internal/service"
)

// TestTickerNeverRevertsManualStart: a cycle started early (future
// start_date, status current) survives a RunOnce whose clock is still
// before start_date — the activate pass only touches status='upcoming'
// rows, so a manual start is never reverted.
func TestTickerNeverRevertsManualStart(t *testing.T) {
	ctx, pool, slug, ident, actor, _ := tickerTestSetup(t, "ticker-manual")
	tk := &Ticker{Pool: pool}
	now := time.Now()

	c, err := service.CreateCycle(ctx, pool, slug, ident, actor, service.CycleInput{
		Name: "Sprint 9", StartDate: now.AddDate(0, 0, 7), EndDate: now.AddDate(0, 0, 21),
	})
	if err != nil {
		t.Fatalf("CreateCycle: %v", err)
	}
	if _, err := service.StartCycle(ctx, pool, slug, ident, actor, c.ID); err != nil {
		t.Fatalf("StartCycle: %v", err)
	}

	// Clock still a week before the scheduled start: the ticker must
	// leave the manually-started cycle alone.
	if err := tk.RunOnce(ctx, now); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	got, err := service.GetCycle(ctx, pool, slug, ident, actor, c.ID)
	if err != nil {
		t.Fatalf("GetCycle: %v", err)
	}
	if got.Status != "current" {
		t.Fatalf("status = %q, want current (manual start must never be reverted)", got.Status)
	}
	if !got.StartDate.Equal(c.StartDate) {
		t.Fatalf("start_date = %v, want the untouched scheduled %v", got.StartDate, c.StartDate)
	}
}

// TestTickerAutoCompletesManualStart: the override applies to the start
// only — once end_date passes, the ticker's complete pass still
// completes a manually-started cycle.
func TestTickerAutoCompletesManualStart(t *testing.T) {
	ctx, pool, slug, ident, actor, _ := tickerTestSetup(t, "ticker-manual-end")
	tk := &Ticker{Pool: pool}
	now := time.Now()

	c, err := service.CreateCycle(ctx, pool, slug, ident, actor, service.CycleInput{
		Name: "Sprint 9", StartDate: now.AddDate(0, 0, 7), EndDate: now.AddDate(0, 0, 21),
	})
	if err != nil {
		t.Fatalf("CreateCycle: %v", err)
	}
	if _, err := service.StartCycle(ctx, pool, slug, ident, actor, c.ID); err != nil {
		t.Fatalf("StartCycle: %v", err)
	}

	// Clock past the scheduled end: the ticker completes it like any
	// scheduled cycle.
	if err := tk.RunOnce(ctx, now.AddDate(0, 0, 22)); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	got, err := service.GetCycle(ctx, pool, slug, ident, actor, c.ID)
	if err != nil {
		t.Fatalf("GetCycle: %v", err)
	}
	if got.Status != "completed" {
		t.Fatalf("status = %q, want completed (manual starts still end on schedule)", got.Status)
	}
}
