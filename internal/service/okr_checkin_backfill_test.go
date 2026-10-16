package service

// KR check-in backfill (C42T1, migration 000061): pre-cycle-40
// metric KRs have zero check-in rows (their seed was never written),
// so their timeline shows "No check-ins yet". The migration seeds
// one row per such KR at its current_value. This test re-executes
// the migration statement verbatim (the 000011 precedent) against
// pre-c40-shaped KRs and asserts the seed shape, idempotency, the
// issue-KR exclusion, and that KRs with existing check-ins are
// untouched.

import (
	"testing"
	"time"
)

// krCheckinBackfillSQL is the backfill statement from migration
// 000061, kept as a test-file constant so the test can re-execute
// it verbatim (the 000011 / intakeBackfillSQL pattern). It lives
// here rather than in production code because no production path
// re-runs the backfill — the migration itself is the only caller.
const krCheckinBackfillSQL = `INSERT INTO key_result_checkins (key_result_id, author_id, previous_value, new_value, note, created_at) SELECT kr.id, NULL, NULL, kr.current_value, 'Backfilled history', kr.created_at FROM key_results kr WHERE kr.kind = 'metric' AND NOT EXISTS (SELECT 1 FROM key_result_checkins c WHERE c.key_result_id = kr.id)`

func TestKRCheckinBackfill(t *testing.T) {
	ctx, pool, slug, actor := setupOKRTest(t)

	// Migration 000061 applied cleanly on the freshly migrated test DB.
	var applied bool
	if err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = '000061_kr_checkin_backfill')`,
	).Scan(&applied); err != nil {
		t.Fatalf("migration recorded check: %v", err)
	}
	if !applied {
		t.Fatalf("migration 000061_kr_checkin_backfill not recorded in schema_migrations")
	}

	ob, err := CreateObjective(ctx, pool, slug, actor, ObjectiveInput{Title: "Backfill"})
	if err != nil {
		t.Fatalf("CreateObjective: %v", err)
	}

	// Pre-c40-shaped metric KR: create via the service (which writes
	// a seed check-in), then delete the seed — the KR now looks
	// exactly like a KR created before cycle 40.
	kr, err := CreateKeyResult(ctx, pool, slug, actor, ob.ID, KeyResultInput{
		Title: "NPS", Kind: "metric", Unit: strptr("%"),
		StartValue: fptr(0), TargetValue: fptr(100), CurrentValue: fptr(42),
	})
	if err != nil {
		t.Fatalf("CreateKeyResult: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`DELETE FROM key_result_checkins WHERE key_result_id = $1::uuid`, kr.ID); err != nil {
		t.Fatalf("delete seed check-in: %v", err)
	}
	// Ground truth: the KR's created_at as stored in the row.
	var krCreatedAt time.Time
	if err := pool.QueryRow(ctx,
		`SELECT created_at FROM key_results WHERE id = $1::uuid`, kr.ID,
	).Scan(&krCreatedAt); err != nil {
		t.Fatalf("KR created_at: %v", err)
	}

	// Backfill twice: the second run must add nothing (NOT EXISTS guard).
	for run := 1; run <= 2; run++ {
		if _, err := pool.Exec(ctx, krCheckinBackfillSQL); err != nil {
			t.Fatalf("backfill run %d: %v", run, err)
		}
		ins, err := ListKeyResultCheckins(ctx, pool, slug, actor, ob.ID, kr.ID)
		if err != nil {
			t.Fatalf("ListKeyResultCheckins (run %d): %v", run, err)
		}
		if len(ins) != 1 {
			t.Fatalf("check-ins after backfill run %d = %d, want exactly 1", run, len(ins))
		}
		seed := ins[0]
		if seed.KeyResultID != kr.ID {
			t.Fatalf("seed key_result_id = %s, want %s", seed.KeyResultID, kr.ID)
		}
		if seed.PreviousValue != nil {
			t.Fatalf("seed previous = %v, want nil", *seed.PreviousValue)
		}
		if seed.NewValue != 42 {
			t.Fatalf("seed new = %v, want 42 (current_value)", seed.NewValue)
		}
		if seed.Note == nil || *seed.Note != "Backfilled history" {
			t.Fatalf("seed note = %v, want %q", seed.Note, "Backfilled history")
		}
		// Author NULL: the creator is not recorded on the KR row,
		// so there is nothing to recover.
		if seed.Author != nil {
			t.Fatalf("seed author = %+v, want nil", seed.Author)
		}
		if !seed.CreatedAt.Equal(krCreatedAt) {
			t.Fatalf("seed created_at = %v, want kr.created_at %v", seed.CreatedAt, krCreatedAt)
		}
	}

	// Issue KR: never gains check-ins.
	ikr, err := CreateKeyResult(ctx, pool, slug, actor, ob.ID, KeyResultInput{
		Title: "Ship issues", Kind: "issue", TargetValue: fptr(100),
	})
	if err != nil {
		t.Fatalf("CreateKeyResult issue: %v", err)
	}
	if _, err := pool.Exec(ctx, krCheckinBackfillSQL); err != nil {
		t.Fatalf("backfill (issue KR): %v", err)
	}
	iins, err := ListKeyResultCheckins(ctx, pool, slug, actor, ob.ID, ikr.ID)
	if err != nil {
		t.Fatalf("ListKeyResultCheckins (issue KR): %v", err)
	}
	if len(iins) != 0 {
		t.Fatalf("issue KR check-ins = %d, want 0", len(iins))
	}

	// KR with existing check-ins: untouched — no backfill seed added.
	kept, err := CreateKeyResult(ctx, pool, slug, actor, ob.ID, KeyResultInput{
		Title: "Kept", Kind: "metric",
		StartValue: fptr(0), TargetValue: fptr(100), CurrentValue: fptr(10),
	})
	if err != nil {
		t.Fatalf("CreateKeyResult kept: %v", err)
	}
	if _, err := UpdateKeyResult(ctx, pool, slug, actor, ob.ID, kept.ID, KeyResultPatch{
		CurrentValue: fptr(20),
	}); err != nil {
		t.Fatalf("UpdateKeyResult kept: %v", err)
	}
	if _, err := pool.Exec(ctx, krCheckinBackfillSQL); err != nil {
		t.Fatalf("backfill (kept KR): %v", err)
	}
	kins, err := ListKeyResultCheckins(ctx, pool, slug, actor, ob.ID, kept.ID)
	if err != nil {
		t.Fatalf("ListKeyResultCheckins (kept KR): %v", err)
	}
	// Still exactly the seed + the one change entry, newest first.
	if len(kins) != 2 {
		t.Fatalf("kept KR check-ins = %d, want 2 (seed + change)", len(kins))
	}
	if kins[0].NewValue != 20 || kins[1].NewValue != 10 {
		t.Fatalf("kept KR entries = new %v, %v, want 20, 10", kins[0].NewValue, kins[1].NewValue)
	}
	for _, e := range kins {
		if e.Note != nil && *e.Note == "Backfilled history" {
			t.Fatalf("kept KR gained a backfill row: %+v", e)
		}
	}
}
