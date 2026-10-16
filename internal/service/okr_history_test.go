package service

// Goals-list progress sparkline (C43T2): the series builder replays
// metric-KR check-in rows into per-objective progress-over-time
// points, and ListObjectives/GetObjective populate progress_history
// from the same helper.

import (
	"testing"
	"time"
)

func mustTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func f64ptr(v float64) *float64 { return &v }

func TestBuildObjectiveProgressHistory(t *testing.T) {
	t0 := mustTime("2026-01-01T00:00:00Z")
	t1 := mustTime("2026-01-02T00:00:00Z")
	t2 := mustTime("2026-01-03T00:00:00Z")

	metric := func(id string, start, target, current float64, created time.Time) historyKR {
		return historyKR{ID: id, Kind: "metric", StartValue: start,
			TargetValue: target, CurrentValue: current, CreatedAt: created}
	}
	seed := func(kr string, v float64, at time.Time) historyCheckin {
		return historyCheckin{KeyResultID: kr, PreviousValue: nil, NewValue: v, CreatedAt: at}
	}
	move := func(kr string, prev, v float64, at time.Time) historyCheckin {
		return historyCheckin{KeyResultID: kr, PreviousValue: f64ptr(prev), NewValue: v, CreatedAt: at}
	}
	assertPoints := func(t *testing.T, got []ObjectiveProgressPoint, want []ObjectiveProgressPoint) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("points = %+v, want %+v", got, want)
		}
		for i := range want {
			if !got[i].T.Equal(want[i].T) || got[i].Progress != want[i].Progress {
				t.Fatalf("point %d = %+v, want %+v", i, got[i], want[i])
			}
		}
	}

	t.Run("two metric KRs replay to exact means", func(t *testing.T) {
		krs := []historyKR{
			metric("kr1", 0, 100, 100, t0),
			metric("kr2", 0, 10, 5, t0),
		}
		cis := []historyCheckin{
			seed("kr1", 0, t0), seed("kr2", 0, t0),
			move("kr1", 0, 50, t1), move("kr2", 0, 5, t1),
			move("kr1", 50, 100, t2),
		}
		assertPoints(t, buildObjectiveProgressHistory(krs, cis), []ObjectiveProgressPoint{
			{T: t0, Progress: 0},
			{T: t1, Progress: 0.5},
			{T: t2, Progress: 0.75},
		})
	})

	t.Run("issue KR contributes its current progress throughout", func(t *testing.T) {
		krs := []historyKR{
			metric("kr1", 0, 100, 100, t0),
			{ID: "kr2", Kind: "issue", CreatedAt: t0, IssueProgress: 0.5},
		}
		cis := []historyCheckin{
			seed("kr1", 0, t0),
			move("kr1", 0, 100, t1),
		}
		assertPoints(t, buildObjectiveProgressHistory(krs, cis), []ObjectiveProgressPoint{
			{T: t0, Progress: 0.25},
			{T: t1, Progress: 0.75},
		})
	})

	t.Run("KR contributes only from its creation onward", func(t *testing.T) {
		krs := []historyKR{
			metric("kr1", 0, 100, 100, t0),
			metric("kr2", 0, 10, 10, t1),
		}
		cis := []historyCheckin{
			seed("kr1", 0, t0),
			seed("kr2", 10, t1),
			move("kr1", 0, 100, t2),
		}
		assertPoints(t, buildObjectiveProgressHistory(krs, cis), []ObjectiveProgressPoint{
			{T: t0, Progress: 0},
			{T: t1, Progress: 0.5},
			{T: t2, Progress: 1},
		})
	})

	t.Run("same-timestamp check-ins collapse to one post-event point", func(t *testing.T) {
		krs := []historyKR{
			metric("kr1", 0, 100, 100, t0),
			metric("kr2", 0, 10, 5, t1),
		}
		cis := []historyCheckin{
			seed("kr1", 0, t0),
			// Two moves for kr1 at t1: the point uses the last one.
			move("kr1", 0, 50, t1),
			move("kr1", 50, 100, t1),
			seed("kr2", 5, t1),
		}
		assertPoints(t, buildObjectiveProgressHistory(krs, cis), []ObjectiveProgressPoint{
			{T: t0, Progress: 0},
			{T: t1, Progress: 0.75},
		})
	})

	t.Run("orphan check-in rows for unknown KRs are skipped", func(t *testing.T) {
		krs := []historyKR{metric("kr1", 0, 100, 50, t0)}
		cis := []historyCheckin{
			seed("kr1", 0, t0),
			move("gone-kr", 0, 100, t1),
			move("kr1", 0, 50, t1),
		}
		assertPoints(t, buildObjectiveProgressHistory(krs, cis), []ObjectiveProgressPoint{
			{T: t0, Progress: 0},
			{T: t1, Progress: 0.5},
		})
	})

	t.Run("metric KR without a seed row falls back to current value", func(t *testing.T) {
		krs := []historyKR{
			// No seed row; current 80 is the replay's starting value.
			metric("kr1", 0, 100, 80, t0),
			metric("kr2", 0, 100, 100, t0),
		}
		cis := []historyCheckin{
			seed("kr2", 0, t0),
			move("kr2", 0, 100, t1),
			move("kr1", 80, 20, t2),
		}
		assertPoints(t, buildObjectiveProgressHistory(krs, cis), []ObjectiveProgressPoint{
			{T: t0, Progress: 0.4},
			{T: t1, Progress: 0.9},
			{T: t2, Progress: 0.6},
		})
	})

	t.Run("descending KR replays through metricKRProgress", func(t *testing.T) {
		krs := []historyKR{metric("kr1", 100, 0, 0, t0)}
		cis := []historyCheckin{
			seed("kr1", 100, t0),
			move("kr1", 100, 0, t1),
		}
		assertPoints(t, buildObjectiveProgressHistory(krs, cis), []ObjectiveProgressPoint{
			{T: t0, Progress: 0},
			{T: t1, Progress: 1},
		})
	})

	t.Run("unsorted input replays chronologically", func(t *testing.T) {
		krs := []historyKR{metric("kr1", 0, 100, 100, t0)}
		cis := []historyCheckin{
			move("kr1", 50, 100, t2),
			seed("kr1", 0, t0),
			move("kr1", 0, 50, t1),
		}
		assertPoints(t, buildObjectiveProgressHistory(krs, cis), []ObjectiveProgressPoint{
			{T: t0, Progress: 0},
			{T: t1, Progress: 0.5},
			{T: t2, Progress: 1},
		})
	})

	t.Run("empty input yields no points", func(t *testing.T) {
		if got := buildObjectiveProgressHistory(nil, nil); len(got) != 0 {
			t.Fatalf("empty = %+v, want empty", got)
		}
		if got := buildObjectiveProgressHistory(
			[]historyKR{metric("kr1", 0, 100, 50, t0)}, nil); len(got) != 0 {
			t.Fatalf("no check-ins = %+v, want empty", got)
		}
	})

	t.Run("series caps at the last 60 points", func(t *testing.T) {
		krs := []historyKR{metric("kr1", 0, 1000, 70, t0)}
		cis := []historyCheckin{seed("kr1", 0, t0)}
		for i := 1; i <= 70; i++ {
			cis = append(cis, move("kr1", float64(i-1), float64(i),
				t0.Add(time.Duration(i)*24*time.Hour)))
		}
		got := buildObjectiveProgressHistory(krs, cis)
		if len(got) != 60 {
			t.Fatalf("points = %d, want 60", len(got))
		}
		// 71 events total (seed + 70 moves); the first 11 drop.
		wantFirst := t0.Add(11 * 24 * time.Hour)
		if !got[0].T.Equal(wantFirst) || got[0].Progress != 0.011 {
			t.Fatalf("first kept = %+v, want {T: %v, Progress: 0.011}", got[0], wantFirst)
		}
		if got[59].Progress != 0.07 {
			t.Fatalf("last = %+v, want Progress 0.07", got[59])
		}
	})
}

func TestListObjectivesProgressHistory(t *testing.T) {
	ctx, pool, slug, actor := setupOKRTest(t)

	ob, err := CreateObjective(ctx, pool, slug, actor, ObjectiveInput{Title: "Sparkline"})
	if err != nil {
		t.Fatalf("CreateObjective: %v", err)
	}
	kr1, err := CreateKeyResult(ctx, pool, slug, actor, ob.ID, KeyResultInput{
		Title: "KR1", Kind: "metric",
		StartValue: fptr(0), TargetValue: fptr(100), CurrentValue: fptr(25),
	})
	if err != nil {
		t.Fatalf("CreateKeyResult kr1: %v", err)
	}
	kr2, err := CreateKeyResult(ctx, pool, slug, actor, ob.ID, KeyResultInput{
		Title: "KR2", Kind: "metric",
		StartValue: fptr(10), TargetValue: fptr(20),
	})
	if err != nil {
		t.Fatalf("CreateKeyResult kr2: %v", err)
	}
	if _, err := UpdateKeyResult(ctx, pool, slug, actor, ob.ID, kr1.ID, KeyResultPatch{
		CurrentValue: fptr(50),
	}); err != nil {
		t.Fatalf("UpdateKeyResult: %v", err)
	}

	// Pin every timestamp so the replay is exact: both KRs created
	// at T0, both seeds at T0, the value change at T1.
	t0 := mustTime("2026-01-01T00:00:00Z")
	t1 := mustTime("2026-01-02T00:00:00Z")
	for _, krID := range []string{kr1.ID, kr2.ID} {
		if _, err := pool.Exec(ctx,
			`UPDATE key_results SET created_at = $1 WHERE id = $2::uuid`, t0, krID); err != nil {
			t.Fatalf("pin kr created_at: %v", err)
		}
		if _, err := pool.Exec(ctx,
			`UPDATE key_result_checkins SET created_at = $1
			 WHERE key_result_id = $2::uuid AND previous_value IS NULL`, t0, krID); err != nil {
			t.Fatalf("pin seed created_at: %v", err)
		}
	}
	if _, err := pool.Exec(ctx,
		`UPDATE key_result_checkins SET created_at = $1
		 WHERE key_result_id = $2::uuid AND previous_value IS NOT NULL`, t1, kr1.ID); err != nil {
		t.Fatalf("pin move created_at: %v", err)
	}

	want := []ObjectiveProgressPoint{
		{T: t0, Progress: 0.125}, // mean(0.25, 0): kr2 current 0 below start 10 clamps to 0
		{T: t1, Progress: 0.25},  // mean(0.5, 0)
	}
	list, err := ListObjectives(ctx, pool, slug, actor)
	if err != nil {
		t.Fatalf("ListObjectives: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("objectives = %d, want 1", len(list))
	}
	got := list[0]
	if got.Progress != 0.25 {
		t.Fatalf("current progress = %v, want 0.25", got.Progress)
	}
	if len(got.ProgressHistory) != len(want) {
		t.Fatalf("history = %+v, want %+v", got.ProgressHistory, want)
	}
	for i := range want {
		if !got.ProgressHistory[i].T.Equal(want[i].T) || got.ProgressHistory[i].Progress != want[i].Progress {
			t.Fatalf("history[%d] = %+v, want %+v", i, got.ProgressHistory[i], want[i])
		}
	}

	// GetObjective populates the same history via the shared helper.
	single, err := GetObjective(ctx, pool, slug, actor, ob.ID)
	if err != nil {
		t.Fatalf("GetObjective: %v", err)
	}
	if len(single.ProgressHistory) != len(want) {
		t.Fatalf("GetObjective history = %+v, want %+v", single.ProgressHistory, want)
	}
	for i := range want {
		if !single.ProgressHistory[i].T.Equal(want[i].T) || single.ProgressHistory[i].Progress != want[i].Progress {
			t.Fatalf("GetObjective history[%d] = %+v, want %+v", i, single.ProgressHistory[i], want[i])
		}
	}
}
